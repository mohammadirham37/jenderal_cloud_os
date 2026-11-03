package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/itk-cloud/itk-cloud/apps/agent/internal/native"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// session abstracts a running terminal (container exec or native PTY).
type session interface {
	io.ReadWriter
	Resize(cols, rows int) error
	Close() error
}

type execSession struct {
	s      *Server
	execID string
	conn   io.ReadWriteCloser
	reader io.Reader
}

func (e *execSession) Read(p []byte) (int, error)  { return e.reader.Read(p) }
func (e *execSession) Write(p []byte) (int, error) { return e.conn.Write(p) }
func (e *execSession) Close() error                { return e.conn.Close() }
func (e *execSession) Resize(cols, rows int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return e.s.docker.ResizeExec(ctx, e.execID, cols, rows)
}

type ptySession struct {
	f    *os.File
	stop func()
}

func (p *ptySession) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *ptySession) Write(b []byte) (int, error) { return p.f.Write(b) }
func (p *ptySession) Close() error                { p.stop(); return p.f.Close() }
func (p *ptySession) Resize(cols, rows int) error {
	return pty.Setsize(p.f, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (s *Server) openContainerShell(ctx context.Context, t *agentproto.TerminalAttach) (session, error) {
	id, err := s.resolveContainer(ctx, &agentproto.ContainerRef{ContainerID: t.ContainerID, RuntimeID: t.RuntimeID})
	if err != nil {
		return nil, err
	}
	cj, err := s.docker.InspectContainer(ctx, id)
	if err != nil {
		return nil, err
	}
	if !cj.State.Running {
		return nil, &agentproto.Error{Code: agentproto.ErrCodeConflict, Message: "container is not running"}
	}
	uid := strconv.Itoa(t.RunAsUID)
	execID, err := s.docker.CreateExec(ctx, id, map[string]any{
		"AttachStdin": true, "AttachStdout": true, "AttachStderr": true, "Tty": true,
		"User":        uid + ":" + uid,
		"WorkingDir":  "/home/jenderal",
		"Env":         []string{"TERM=xterm-256color", "HOME=/home/jenderal", "LANG=C.UTF-8"},
		"Cmd":         []string{"/bin/sh", "-c", "if [ -x /bin/bash ]; then exec /bin/bash -l; else exec /bin/sh -l; fi"},
		"ConsoleSize": []int{t.Rows, t.Cols},
	})
	if err != nil {
		return nil, err
	}
	conn, br, err := s.docker.StartExecTTY(ctx, execID)
	if err != nil {
		return nil, err
	}
	es := &execSession{s: s, execID: execID, conn: conn, reader: br}
	_ = es.Resize(t.Cols, t.Rows)
	return es, nil
}

func (s *Server) openNativeShell(t *agentproto.TerminalAttach) (session, error) {
	if !s.cfg.NativeEnabled {
		return nil, &agentproto.Error{Code: agentproto.ErrCodeForbidden, Message: "native workspaces are disabled on this host"}
	}
	home, err := s.storage.DataDir(t.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if !native.HomeExists(home) {
		return nil, &agentproto.Error{Code: agentproto.ErrCodeNotFound, Message: "workspace storage is not provisioned"}
	}
	cmd, err := native.ShellCommand(t, home)
	if err != nil {
		return nil, &agentproto.Error{Code: agentproto.ErrCodeUnavailable, Message: err.Error()}
	}
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(t.Cols), Rows: uint16(t.Rows)})
	if err != nil {
		return nil, err
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			go func() { _ = cmd.Wait() }()
		})
	}
	return &ptySession{f: f, stop: stop}, nil
}

func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	req, p, err := s.streamRequest(r, agentproto.OpTerminalAttach)
	if err != nil {
		writeErr(w, err)
		return
	}
	t := p.(*agentproto.TerminalAttach)
	start := time.Now()
	ctx := r.Context()
	var sess session
	if t.RuntimeID != "" {
		sess, err = s.openContainerShell(ctx, t)
	} else {
		sess, err = s.openNativeShell(t)
	}
	if err != nil {
		s.logOp(req, start, err)
		writeErr(w, err)
		return
	}
	defer sess.Close()
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true}) // unix socket peer, already authenticated
	if err != nil {
		s.logOp(req, start, err)
		return
	}
	ws.SetReadLimit(64 << 10)
	s.logOp(req, start, nil)
	reason := s.pump(ctx, ws, sess, time.Duration(t.IdleTimeoutSec)*time.Second, time.Duration(t.MaxDurationSec)*time.Second)
	s.log.Info("terminal session ended", "request_id", req.RequestID, "actor_id", req.ActorID,
		"workspace_id", t.WorkspaceID, "reason", reason, "duration_s", int(time.Since(start).Seconds()))
	_ = ws.Close(websocket.StatusNormalClosure, reason)
}

// pump relays bytes between the WebSocket and the terminal, enforcing idle
// and maximum session durations. It returns the end reason.
func (s *Server) pump(ctx context.Context, ws *websocket.Conn, sess session, idle, maxDur time.Duration) string {
	ctx, cancel := context.WithTimeout(ctx, maxDur)
	defer cancel()
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	done := make(chan string, 3)

	go func() { // terminal -> websocket
		buf := make([]byte, 32<<10)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				last.Store(time.Now().UnixNano())
				if werr := ws.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					done <- "client_disconnected"
					return
				}
			}
			if err != nil {
				done <- "shell_exited"
				return
			}
		}
	}()
	go func() { // websocket -> terminal
		for {
			typ, data, err := ws.Read(ctx)
			if err != nil {
				done <- "client_disconnected"
				return
			}
			last.Store(time.Now().UnixNano())
			if typ == websocket.MessageText {
				var c agentproto.TerminalControl
				if json.Unmarshal(data, &c) == nil && c.Type == "resize" && c.Cols >= 10 && c.Cols <= 1000 && c.Rows >= 5 && c.Rows <= 500 {
					_ = sess.Resize(c.Cols, c.Rows)
				}
				continue
			}
			if _, err := sess.Write(data); err != nil {
				done <- "shell_exited"
				return
			}
		}
	}()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case reason := <-done:
			return reason
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return "max_duration"
			}
			return "cancelled"
		case <-tick.C:
			if time.Since(time.Unix(0, last.Load())) > idle {
				return "idle_timeout"
			}
		}
	}
}
