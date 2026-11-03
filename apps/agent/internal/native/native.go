// Package native supports Linux-native workspaces: a dedicated unprivileged
// Linux account per workspace (no sudo, locked password) and shells started
// inside a transient systemd scope that enforces CPU, memory and task limits
// via cgroups. Native workspaces are disabled unless explicitly enabled.
package native

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// ErrUnsupported is returned on hosts without the required tooling.
var ErrUnsupported = errors.New("native workspaces require Linux with systemd")

// Supported reports whether native workspaces can run on this host.
func Supported() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	for _, b := range []string{"useradd", "systemd-run"} {
		if _, err := exec.LookPath(b); err != nil {
			return false
		}
	}
	return true
}

func run(ctx context.Context, name string, args ...string) error {
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// EnsureUser creates (if needed) the dedicated account and returns its ids.
func EnsureUser(ctx context.Context, linuxUser, home, group, workspaceID string) (agentproto.NativeUser, error) {
	if !Supported() {
		return agentproto.NativeUser{}, ErrUnsupported
	}
	if u, err := user.Lookup(linuxUser); err == nil {
		return ids(u)
	}
	if err := run(ctx, "useradd", "--no-create-home", "--home-dir", home, "--shell", "/bin/bash",
		"--gid", group, "--comment", "Jenderal Cloud OS workspace "+workspaceID, linuxUser); err != nil {
		return agentproto.NativeUser{}, err
	}
	u, err := user.Lookup(linuxUser)
	if err != nil {
		return agentproto.NativeUser{}, err
	}
	return ids(u)
}

func ids(u *user.User) (agentproto.NativeUser, error) {
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return agentproto.NativeUser{}, err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return agentproto.NativeUser{}, err
	}
	if uid < 1000 {
		return agentproto.NativeUser{}, errors.New("refusing to use a system account")
	}
	return agentproto.NativeUser{UID: uid, GID: gid}, nil
}

// DeleteUser removes the dedicated account (home is removed with storage).
func DeleteUser(ctx context.Context, linuxUser string) error {
	if !Supported() {
		return nil
	}
	if _, err := user.Lookup(linuxUser); err != nil {
		return nil
	}
	return run(ctx, "userdel", linuxUser)
}

// ShellCommand builds the command for an interactive native shell confined
// by a transient systemd scope with resource limits.
func ShellCommand(t *agentproto.TerminalAttach, home string) (*exec.Cmd, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	u, err := user.Lookup(t.LinuxUser)
	if err != nil {
		return nil, fmt.Errorf("unknown workspace user: %w", err)
	}
	nu, err := ids(u)
	if err != nil {
		return nil, err
	}
	args := []string{"--scope", "--quiet", "--collect",
		"--uid=" + strconv.Itoa(nu.UID), "--gid=" + strconv.Itoa(nu.GID),
		"--slice=itk-native.slice",
		"-p", "TasksMax=" + strconv.Itoa(max(t.PidsLimit, 64)),
	}
	if t.MemoryBytes > 0 {
		args = append(args, "-p", "MemoryMax="+strconv.FormatInt(t.MemoryBytes, 10), "-p", "MemorySwapMax=0")
	}
	if t.CPUMillis > 0 {
		args = append(args, "-p", "CPUQuota="+strconv.Itoa(t.CPUMillis/10)+"%")
	}
	args = append(args, "--", "/bin/bash", "-l")
	cmd := exec.Command("systemd-run", args...)
	cmd.Dir = home
	cmd.Env = []string{
		"HOME=" + home, "USER=" + t.LinuxUser, "LOGNAME=" + t.LinuxUser, "SHELL=/bin/bash",
		"TERM=xterm-256color", "LANG=C.UTF-8", "PATH=/usr/local/bin:/usr/bin:/bin",
	}
	return cmd, nil
}

// HomeExists reports whether the native home directory exists.
func HomeExists(home string) bool {
	st, err := os.Stat(home)
	return err == nil && st.IsDir()
}
