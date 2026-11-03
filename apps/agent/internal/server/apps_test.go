package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itk-cloud/itk-cloud/apps/agent/internal/config"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestWrapWithGate(t *testing.T) {
	dir := t.TempDir()
	s := &Server{cfg: &config.Config{ToolsDir: dir}}
	ports := []agentproto.PortSpec{{Port: 8888, Name: "jupyter"}, {Port: 13337, Name: "vscode"}, {Port: 6080, Name: "desktop"}}
	base := []string{"sleep", "infinity"}

	// No gate installed: unchanged, no mount.
	if cmd, m := s.wrapWithGate(base, ports); m != nil || len(cmd) != 2 {
		t.Fatalf("without tools: cmd=%v mount=%v", cmd, m)
	}
	touch(t, filepath.Join(dir, "bin", "itk-app-gate"))
	// Gate but no code-server: only the desktop app.
	cmd, m := s.wrapWithGate(base, ports)
	if m == nil || cmd[0] != gateBinary || strings.Contains(strings.Join(cmd, " "), "vscode:") || !strings.Contains(strings.Join(cmd, " "), "desktop:6080:6081:") {
		t.Fatalf("gate only: %v", cmd)
	}
	if m["ReadOnly"] != true || m["Target"] != toolsMountPath {
		t.Fatalf("mount = %v", m)
	}
	touch(t, filepath.Join(dir, "code-server", "bin", "code-server"))
	cmd, _ = s.wrapWithGate(base, ports)
	joined := strings.Join(cmd, " ")
	if !strings.Contains(joined, "vscode:13337:13338:") || !strings.HasSuffix(joined, "-- sleep infinity") {
		t.Fatalf("full: %v", cmd)
	}
	// Ports without built-in names are not gated.
	if cmd, m := s.wrapWithGate(base, ports[:1]); m != nil || len(cmd) != 2 {
		t.Fatalf("plain ports gated: %v", cmd)
	}
}
