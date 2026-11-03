package server

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Built-in workspace applications. A template opts in by publishing a port
// with one of these names; the agent then runs the container's command under
// itk-app-gate, which checks the per-container app token and starts the
// application on first use.
const (
	toolsMountPath = "/opt/itk-tools"
	gateBinary     = toolsMountPath + "/bin/itk-app-gate"

	// VS Code (code-server from the host tools directory).
	appVSCode    = "vscode"
	vscodePort   = 13337
	vscodeTarget = 13338
	// Remote desktop browser (noVNC; the session script ships in the image).
	appDesktop    = "desktop"
	desktopPort   = 6080
	desktopTarget = 6081
)

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// toolsAvailable reports whether the host tools directory contains the gate.
func (s *Server) toolsAvailable() bool {
	return s.cfg.ToolsDir != "" && fileExists(filepath.Join(s.cfg.ToolsDir, "bin", "itk-app-gate"))
}

// gatedApps returns the itk-app-gate --app arguments for the built-in apps
// requested by the container's ports.
func (s *Server) gatedApps(ports []agentproto.PortSpec) []string {
	if !s.toolsAvailable() {
		return nil
	}
	var args []string
	for _, p := range ports {
		switch {
		case p.Name == appVSCode && p.Port == vscodePort:
			if !fileExists(filepath.Join(s.cfg.ToolsDir, "code-server", "bin", "code-server")) {
				continue
			}
			cmd := toolsMountPath + "/code-server/bin/code-server" +
				" --bind-addr 127.0.0.1:" + strconv.Itoa(vscodeTarget) +
				" --auth none --disable-telemetry --disable-update-check --disable-workspace-trust" +
				" --user-data-dir \"$HOME/.local/share/code-server\"" +
				" --extensions-dir \"$HOME/.local/share/code-server/extensions\"" +
				" \"$HOME\""
			args = append(args, "--app", appVSCode+":"+strconv.Itoa(vscodePort)+":"+strconv.Itoa(vscodeTarget)+":"+cmd)
		case p.Name == appDesktop && p.Port == desktopPort:
			args = append(args, "--app", appDesktop+":"+strconv.Itoa(desktopPort)+":"+strconv.Itoa(desktopTarget)+":/usr/local/bin/start-desktop-session")
		}
	}
	return args
}

// wrapWithGate prefixes the container command with itk-app-gate when the
// container publishes built-in apps, and returns the extra read-only mount
// for the host tools directory (nil when not needed).
func (s *Server) wrapWithGate(cmd []string, ports []agentproto.PortSpec) ([]string, map[string]any) {
	apps := s.gatedApps(ports)
	if len(apps) == 0 {
		return cmd, nil
	}
	wrapped := append([]string{gateBinary}, apps...)
	wrapped = append(wrapped, "--")
	wrapped = append(wrapped, cmd...)
	mount := map[string]any{
		"Type": "bind", "Source": s.cfg.ToolsDir, "Target": toolsMountPath, "ReadOnly": true,
		"BindOptions": map[string]any{"Propagation": "rprivate"},
	}
	return wrapped, mount
}
