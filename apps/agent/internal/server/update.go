package server

// Platform self-update (platform.update, platform.update_status).
//
// The agent does not download or execute release code itself. It validates
// the request against its own root-only configuration (repository, CPU
// architecture), writes a root-owned request file and starts the
// itk-cloud-updater.service oneshot unit (installer/self-update.sh). The
// updater runs in its own systemd unit, so it survives the restart of the
// agent and API during the upgrade. It reports progress in status.json and
// logs to the update log; platform.update_status returns both.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

const (
	updateRequestFile = "request.json"
	updateStatusFile  = "status.json"
	updateLogLines    = 100
	updateLogMaxLine  = 500
	// A running state whose updater unit is no longer active and that has
	// not been updated for this long is reported as failed.
	updateStaleAfter = 2 * time.Minute
)

// updateMu serialises platform.update requests.
var updateMu sync.Mutex

// systemctl runs systemctl with a timeout (variable for tests).
var systemctl = func(ctx context.Context, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "systemctl", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// updateRequest is the file read by installer/self-update.sh.
type updateRequest struct {
	Version     string `json:"version"`
	Repo        string `json:"repo"`
	Arch        string `json:"arch"`
	FromVersion string `json:"from_version"`
	RequestedBy string `json:"requested_by"`
	RequestID   string `json:"request_id"`
	RequestedAt string `json:"requested_at"`
}

func (s *Server) platformUpdate(ctx context.Context, req *agentproto.Request, p *agentproto.PlatformUpdate) (agentproto.UpdateStatus, error) {
	if p.Repo != s.cfg.UpdateRepo {
		return agentproto.UpdateStatus{}, &agentproto.Error{Code: agentproto.ErrCodeForbidden, Message: "repository does not match the agent's UPDATE_REPO"}
	}
	if p.Arch != runtime.GOARCH {
		return agentproto.UpdateStatus{}, invalid("architecture does not match this host")
	}
	updateMu.Lock()
	defer updateMu.Unlock()

	cur := s.updateStatus(ctx)
	if agentproto.UpdateRunning(cur.State) {
		return cur, &agentproto.Error{Code: agentproto.ErrCodeConflict, Message: "an update is already running"}
	}
	if err := ensureRootDir(s.cfg.UpdateDir); err != nil {
		return agentproto.UpdateStatus{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := writeFileAtomic(s.cfg.UpdateDir, updateRequestFile, updateRequest{
		Version: p.Version, Repo: p.Repo, Arch: p.Arch, FromVersion: Version,
		RequestedBy: req.ActorID, RequestID: req.RequestID, RequestedAt: now,
	}, 0o600); err != nil {
		return agentproto.UpdateStatus{}, err
	}
	st := agentproto.UpdateStatus{State: agentproto.UpdateQueued, Version: p.Version, FromVersion: Version,
		Message: "Waiting for the updater to start", StartedAt: now, UpdatedAt: now}
	if err := writeFileAtomic(s.cfg.UpdateDir, updateStatusFile, st, 0o644); err != nil {
		return agentproto.UpdateStatus{}, err
	}
	if out, err := systemctl(ctx, "start", "--no-block", s.cfg.UpdateUnit); err != nil {
		st.State, st.Message, st.FinishedAt = agentproto.UpdateFailed, "Could not start "+s.cfg.UpdateUnit+": "+truncate(out, 300), now
		_ = writeFileAtomic(s.cfg.UpdateDir, updateStatusFile, st, 0o644)
		return st, &agentproto.Error{Code: agentproto.ErrCodeUnavailable, Message: st.Message}
	}
	s.log.Info("platform update queued", "version", p.Version, "from_version", Version, "actor_id", req.ActorID)
	st.UnitActive = true
	st.Log = tailLines(s.cfg.UpdateLogFile, updateLogLines)
	return st, nil
}

// updateStatus reads the updater status file and log tail. It never fails:
// a missing status file means no update has run yet.
func (s *Server) updateStatus(ctx context.Context) agentproto.UpdateStatus {
	st := readUpdateStatus(filepath.Join(s.cfg.UpdateDir, updateStatusFile))
	if agentproto.UpdateRunning(st.State) {
		out, _ := systemctl(ctx, "is-active", s.cfg.UpdateUnit)
		st.UnitActive = out == "active" || out == "activating" || out == "deactivating" || out == "reloading"
	}
	st = markStale(st, time.Now())
	st.Log = tailLines(s.cfg.UpdateLogFile, updateLogLines)
	return st
}

func readUpdateStatus(path string) agentproto.UpdateStatus {
	st := agentproto.UpdateStatus{State: agentproto.UpdateIdle}
	f, err := os.Open(path)
	if err != nil {
		return st
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return st
	}
	var parsed agentproto.UpdateStatus
	if json.Unmarshal(b, &parsed) != nil || parsed.State == "" {
		st.State, st.Message = agentproto.UpdateFailed, "The updater status file is unreadable"
		return st
	}
	parsed.Message = truncate(parsed.Message, 1000)
	parsed.Log = nil
	return parsed
}

// markStale turns a running state into failed when the updater unit is gone
// and the status has not changed for updateStaleAfter.
func markStale(st agentproto.UpdateStatus, now time.Time) agentproto.UpdateStatus {
	if !agentproto.UpdateRunning(st.State) || st.UnitActive {
		return st
	}
	t, err := time.Parse(time.RFC3339, st.UpdatedAt)
	if err == nil && now.Sub(t) < updateStaleAfter {
		return st
	}
	last := st.State
	st.State = agentproto.UpdateFailed
	st.Message = "The updater stopped unexpectedly (last state: " + last + "). See the update log."
	return st
}

// tailLines returns the last n lines of a file (reading at most 128 KiB).
func tailLines(path string, n int) []string {
	lines := []string{}
	f, err := os.Open(path)
	if err != nil {
		return lines
	}
	defer f.Close()
	const max = 128 << 10
	if fi, err := f.Stat(); err == nil && fi.Size() > max {
		if _, err := f.Seek(-max, io.SeekEnd); err != nil {
			return lines
		}
	}
	b, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return lines
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), max)
	for sc.Scan() {
		lines = append(lines, truncate(stripANSI(sc.Text()), updateLogMaxLine))
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// stripANSI removes terminal colour escape sequences (the installer colours
// its output when attached to a terminal).
func stripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// ensureRootDir creates dir (0700) and refuses to use it unless it is a real
// directory owned by root and not writable by group or others.
func ensureRootDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create update directory: %w", err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("stat update directory: %w", err)
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("update directory is not a directory")
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return errors.New("update directory must not be group/world writable")
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && os.Geteuid() == 0 {
		return errors.New("update directory must be owned by root")
	}
	return nil
}

func writeFileAtomic(dir, name string, v any, mode os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, name))
}
