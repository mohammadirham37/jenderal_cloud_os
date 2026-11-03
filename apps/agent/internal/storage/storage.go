// Package storage provisions per-workspace storage.
//
// Layout under WORKSPACE_ROOT:
//
//	<workspace-id>/disk.img   loopback ext4 image (loopback driver only)
//	<workspace-id>/data/      mounted volume / directory exposed to the user
//
// The loopback driver enforces the storage quota at the filesystem level.
// The directory driver only measures usage (used on development machines or
// filesystems where loop mounts are not desired) and reports Enforced=false.
package storage

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Manager manages workspace storage.
type Manager struct {
	Root   string
	Driver string
	mu     sync.Mutex
	locks  map[string]*sync.Mutex
	usage  sync.Map // workspaceID -> cachedUsage (directory driver)
}

type cachedUsage struct {
	used int64
	at   time.Time
}

// New creates a manager.
func New(root, driver string) *Manager {
	return &Manager{Root: root, Driver: driver, locks: map[string]*sync.Mutex{}}
}

func (m *Manager) lock(id string) func() {
	m.mu.Lock()
	l, ok := m.locks[id]
	if !ok {
		l = &sync.Mutex{}
		m.locks[id] = l
	}
	m.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Dir returns the workspace directory (never derived from user input other
// than a validated UUID).
func (m *Manager) Dir(workspaceID string) (string, error) {
	if !agentproto.IsUUID(workspaceID) {
		return "", errors.New("invalid workspace id")
	}
	return filepath.Join(m.Root, workspaceID), nil
}

// DataDir returns the user-visible data directory.
func (m *Manager) DataDir(workspaceID string) (string, error) {
	d, err := m.Dir(workspaceID)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "data"), nil
}

// Enforced reports whether quotas are enforced by the filesystem.
func (m *Manager) Enforced() bool { return m.Driver == "loopback" }

// Provision creates storage for a workspace. It is idempotent.
func (m *Manager) Provision(ctx context.Context, p *agentproto.WorkspaceProvision) error {
	unlock := m.lock(p.WorkspaceID)
	defer unlock()
	dir, _ := m.Dir(p.WorkspaceID)
	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(dir, 0o711); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o711); err != nil {
		return err
	}
	if err := os.MkdirAll(data, 0o750); err != nil {
		return err
	}
	if m.Driver == "loopback" {
		img := filepath.Join(dir, "disk.img")
		if _, err := os.Stat(img); errors.Is(err, fs.ErrNotExist) {
			if err := createImage(ctx, img, p.StorageBytes, p.OwnerUID); err != nil {
				_ = os.Remove(img)
				return err
			}
		}
		if err := ensureMounted(ctx, img, data); err != nil {
			return err
		}
	}
	// The mounted filesystem root must be owned by the workspace user. When
	// the agent is not root (development only) ownership stays with the
	// agent user.
	if os.Geteuid() == 0 {
		if err := os.Chown(data, p.OwnerUID, p.OwnerUID); err != nil {
			return err
		}
	}
	return os.Chmod(data, 0o750)
}

// EnsureMounted remounts the workspace volume if needed (e.g. after reboot).
func (m *Manager) EnsureMounted(ctx context.Context, workspaceID string) error {
	if m.Driver != "loopback" {
		return nil
	}
	unlock := m.lock(workspaceID)
	defer unlock()
	dir, err := m.Dir(workspaceID)
	if err != nil {
		return err
	}
	img := filepath.Join(dir, "disk.img")
	if _, err := os.Stat(img); err != nil {
		return fmt.Errorf("workspace storage not provisioned: %w", err)
	}
	return ensureMounted(ctx, img, filepath.Join(dir, "data"))
}

// RemountAll remounts all provisioned workspaces at agent startup.
func (m *Manager) RemountAll(ctx context.Context) []error {
	if m.Driver != "loopback" {
		return nil
	}
	entries, err := os.ReadDir(m.Root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return []error{err}
	}
	var errs []error
	for _, e := range entries {
		if e.IsDir() && agentproto.IsUUID(e.Name()) {
			if err := m.EnsureMounted(ctx, e.Name()); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", e.Name(), err))
			}
		}
	}
	return errs
}

// Destroy unmounts and deletes workspace storage. Idempotent.
func (m *Manager) Destroy(ctx context.Context, workspaceID string) error {
	unlock := m.lock(workspaceID)
	defer unlock()
	dir, err := m.Dir(workspaceID)
	if err != nil {
		return err
	}
	data := filepath.Join(dir, "data")
	if m.Driver == "loopback" {
		if mounted(data) {
			if err := run(ctx, "umount", data); err != nil {
				if err2 := run(ctx, "umount", "-l", data); err2 != nil {
					return fmt.Errorf("unmount: %w", err)
				}
			}
		}
		img := filepath.Join(dir, "disk.img")
		for _, dev := range loopDevices(ctx, img) {
			_ = run(ctx, "losetup", "-d", dev)
		}
	}
	m.usage.Delete(workspaceID)
	return os.RemoveAll(dir)
}

// Usage returns storage usage.
func (m *Manager) Usage(ctx context.Context, workspaceID string) (agentproto.WorkspaceUsage, error) {
	data, err := m.DataDir(workspaceID)
	if err != nil {
		return agentproto.WorkspaceUsage{}, err
	}
	if _, err := os.Stat(data); err != nil {
		return agentproto.WorkspaceUsage{}, err
	}
	if m.Driver == "loopback" {
		used, total, err := statfs(data)
		if err != nil {
			return agentproto.WorkspaceUsage{}, err
		}
		return agentproto.WorkspaceUsage{UsedBytes: used, LimitBytes: total, Enforced: true}, nil
	}
	if v, ok := m.usage.Load(workspaceID); ok {
		cu := v.(cachedUsage)
		if time.Since(cu.at) < time.Minute {
			return agentproto.WorkspaceUsage{UsedBytes: cu.used}, nil
		}
	}
	var used int64
	err = filepath.WalkDir(data, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				used += info.Size()
			}
		}
		return nil
	})
	if err != nil {
		return agentproto.WorkspaceUsage{}, err
	}
	m.usage.Store(workspaceID, cachedUsage{used: used, at: time.Now()})
	return agentproto.WorkspaceUsage{UsedBytes: used}, nil
}

func createImage(ctx context.Context, img string, size int64, uid int) error {
	f, err := os.OpenFile(img, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	owner := strconv.Itoa(uid) + ":" + strconv.Itoa(uid)
	// -m 0: no reserved blocks; root_owner gives the workspace user the fs root.
	return run(ctx, "mkfs.ext4", "-q", "-F", "-m", "0", "-E", "root_owner="+owner, img)
}

// ensureMounted attaches the image to a loop device with direct I/O and
// mounts it. Direct I/O keeps the backing file out of the page cache: without
// it, large writes charge cached pages of disk.img to the container's memory
// cgroup and the writer is OOM-killed long before the quota is reached.
func ensureMounted(ctx context.Context, img, data string) error {
	if mounted(data) {
		for _, dev := range loopDevices(ctx, img) {
			_ = run(ctx, "losetup", "--direct-io=on", dev)
		}
		return nil
	}
	for _, dev := range loopDevices(ctx, img) { // stale attachment from a crash
		_ = run(ctx, "losetup", "-d", dev)
	}
	dev, err := output(ctx, "losetup", "--find", "--show", "--direct-io=on", img)
	if err != nil {
		return err
	}
	if err := run(ctx, "mount", "-o", "nosuid,nodev", dev, data); err != nil {
		_ = run(ctx, "losetup", "-d", dev)
		return err
	}
	return nil
}

// loopDevices returns the loop devices attached to img.
func loopDevices(ctx context.Context, img string) []string {
	out, err := output(ctx, "losetup", "-j", img)
	if err != nil {
		return nil
	}
	var devs []string
	for _, line := range strings.Split(out, "\n") {
		if dev, _, ok := strings.Cut(line, ":"); ok && strings.HasPrefix(dev, "/dev/loop") {
			devs = append(devs, dev)
		}
	}
	return devs
}

// output runs an allow-listed helper and returns its trimmed stdout.
func output(ctx context.Context, name string, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s failed: %v", name, err)
	}
	return strings.TrimSpace(string(b)), nil
}

// mounted checks /proc/self/mountinfo for the mount point.
func mounted(dir string) bool {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) > 4 && unescapeMount(f[4]) == dir {
			return true
		}
	}
	return false
}

func unescapeMount(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}

// run executes an allow-listed helper binary with fixed arguments.
func run(ctx context.Context, name string, args ...string) error {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("%s failed: %v: %s", name, err, msg)
	}
	return nil
}
