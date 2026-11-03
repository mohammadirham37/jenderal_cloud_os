package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/itk-cloud/itk-cloud/apps/agent/internal/config"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

func updateServer(t *testing.T) (*Server, *[]string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{UpdateRepo: "owner/repo", UpdateDir: filepath.Join(dir, "update"),
		UpdateLogFile: filepath.Join(dir, "update.log"), UpdateUnit: "itk-cloud-updater.service"}
	var calls []string
	orig := systemctl
	systemctl = func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "is-active" {
			return "active", nil
		}
		return "", nil
	}
	t.Cleanup(func() { systemctl = orig })
	return &Server{cfg: cfg, log: slog.New(slog.NewTextHandler(io.Discard, nil))}, &calls
}

func TestPlatformUpdateRejectsForeignRepoAndArch(t *testing.T) {
	s, calls := updateServer(t)
	req := &agentproto.Request{ActorID: "a", RequestID: "r"}
	_, err := s.platformUpdate(context.Background(), req, &agentproto.PlatformUpdate{Version: "1.2.3", Repo: "evil/repo", Arch: runtime.GOARCH})
	if !agentproto.IsCode(err, agentproto.ErrCodeForbidden) {
		t.Fatalf("foreign repo: got %v", err)
	}
	other := "arm64"
	if runtime.GOARCH == "arm64" {
		other = "amd64"
	}
	if _, err := s.platformUpdate(context.Background(), req, &agentproto.PlatformUpdate{Version: "1.2.3", Repo: "owner/repo", Arch: other}); err == nil {
		t.Fatal("foreign arch must be rejected")
	}
	if len(*calls) != 0 {
		t.Fatalf("systemctl must not be called: %v", *calls)
	}
}

func TestPlatformUpdateQueuesAndRejectsConcurrent(t *testing.T) {
	s, calls := updateServer(t)
	req := &agentproto.Request{ActorID: "actor-1", RequestID: "req-1"}
	p := &agentproto.PlatformUpdate{Version: "1.2.3", Repo: "owner/repo", Arch: runtime.GOARCH}
	st, err := s.platformUpdate(context.Background(), req, p)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != agentproto.UpdateQueued || st.Version != "1.2.3" {
		t.Fatalf("unexpected status %+v", st)
	}
	if len(*calls) != 1 || (*calls)[0] != "start --no-block itk-cloud-updater.service" {
		t.Fatalf("unexpected systemctl calls %v", *calls)
	}
	b, err := os.ReadFile(filepath.Join(s.cfg.UpdateDir, updateRequestFile))
	if err != nil {
		t.Fatal(err)
	}
	var r updateRequest
	if err := json.Unmarshal(b, &r); err != nil || r.Version != "1.2.3" || r.Repo != "owner/repo" || r.RequestedBy != "actor-1" {
		t.Fatalf("bad request file %s (%v)", b, err)
	}
	if fi, _ := os.Stat(filepath.Join(s.cfg.UpdateDir, updateRequestFile)); fi.Mode().Perm() != 0o600 {
		t.Fatalf("request file mode %v", fi.Mode().Perm())
	}
	if _, err := s.platformUpdate(context.Background(), req, p); !agentproto.IsCode(err, agentproto.ErrCodeConflict) {
		t.Fatalf("second update while running: got %v", err)
	}
}

func TestUpdateStatusIdleStaleAndLog(t *testing.T) {
	s, _ := updateServer(t)
	if st := s.updateStatus(context.Background()); st.State != agentproto.UpdateIdle || st.Log == nil {
		t.Fatalf("no status file must be idle with empty log, got %+v", st)
	}
	old := agentproto.UpdateStatus{State: agentproto.UpdateUpgrading, UpdatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)}
	if got := markStale(old, time.Now()); got.State != agentproto.UpdateFailed {
		t.Fatalf("stale running state must become failed, got %+v", got)
	}
	old.UnitActive = true
	if got := markStale(old, time.Now()); got.State != agentproto.UpdateUpgrading {
		t.Fatal("active unit must not be marked stale")
	}
	fresh := agentproto.UpdateStatus{State: agentproto.UpdateQueued, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	if got := markStale(fresh, time.Now()); got.State != agentproto.UpdateQueued {
		t.Fatal("recent queued state must not be marked stale")
	}

	var sb strings.Builder
	for i := 0; i < 150; i++ {
		sb.WriteString("\x1b[34m==>\x1b[0m line ")
		sb.WriteString(strings.Repeat("x", i%3))
		sb.WriteString("\n")
	}
	if err := os.WriteFile(s.cfg.UpdateLogFile, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := tailLines(s.cfg.UpdateLogFile, updateLogLines)
	if len(lines) != updateLogLines || strings.Contains(lines[0], "\x1b") || !strings.HasPrefix(lines[0], "==> line") {
		t.Fatalf("unexpected tail (%d lines): %q", len(lines), lines[0])
	}
}
