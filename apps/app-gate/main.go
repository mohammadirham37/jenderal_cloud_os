// Command itk-app-gate runs inside workspace containers (bind-mounted
// read-only from the host at /opt/itk-tools/bin). It fronts built-in web
// applications — VS Code (code-server) and the remote desktop browser — with
// the per-container app token that the Jenderal Cloud OS app proxy adds after
// it has authorised the user, starts each application on first use and stops
// it again after an idle period to save memory.
//
// Usage:
//
//	itk-app-gate [--app NAME:LISTEN:UPSTREAM:COMMAND]... [-- CMD ARGS...]
//
// Each --app publishes LISTEN (0.0.0.0) and proxies to 127.0.0.1:UPSTREAM,
// starting COMMAND (run through /bin/sh -c) when the upstream is not
// listening. The optional trailing command is the container's main process:
// the gate supervises it and exits with its status.
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type appSpec struct {
	name     string
	listen   int
	upstream int
	command  string
}

func parseApp(s string) (appSpec, error) {
	parts := strings.SplitN(s, ":", 4)
	if len(parts) != 4 || parts[0] == "" || parts[3] == "" {
		return appSpec{}, fmt.Errorf("invalid --app %q (want NAME:LISTEN:UPSTREAM:COMMAND)", s)
	}
	l, err1 := strconv.Atoi(parts[1])
	u, err2 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || l < 1024 || l > 65535 || u < 1024 || u > 65535 || l == u {
		return appSpec{}, fmt.Errorf("invalid ports in --app %q", s)
	}
	return appSpec{name: parts[0], listen: l, upstream: u, command: parts[3]}, nil
}

func parseArgs(args []string) ([]appSpec, []string, error) {
	var apps []appSpec
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			return apps, args[i+1:], nil
		case a == "--app" && i+1 < len(args):
			spec, err := parseApp(args[i+1])
			if err != nil {
				return nil, nil, err
			}
			apps = append(apps, spec)
			i++
		case strings.HasPrefix(a, "--app="):
			spec, err := parseApp(strings.TrimPrefix(a, "--app="))
			if err != nil {
				return nil, nil, err
			}
			apps = append(apps, spec)
		default:
			return nil, nil, fmt.Errorf("unexpected argument %q", a)
		}
	}
	return apps, nil, nil
}

// app is one gated application.
type app struct {
	spec  appSpec
	token string
	idle  time.Duration

	mu       sync.Mutex
	proc     *exec.Cmd
	done     chan struct{}
	active   atomic.Int64 // in-flight requests and open websockets
	lastUsed atomic.Int64 // unix nanos
}

func (a *app) upstreamAddr() string { return "127.0.0.1:" + strconv.Itoa(a.spec.upstream) }

func (a *app) listening() bool {
	c, err := net.DialTimeout("tcp", a.upstreamAddr(), 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// ensure starts the application if needed and waits until it accepts
// connections.
func (a *app) ensure(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.listening() {
		return nil
	}
	if a.proc == nil {
		cmd := exec.Command("/bin/sh", "-c", a.spec.command)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		cmd.Env = append(os.Environ(), "ITK_APP_PORT="+strconv.Itoa(a.spec.upstream))
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start %s: %w", a.spec.name, err)
		}
		log.Printf("%s: started (pid %d)", a.spec.name, cmd.Process.Pid)
		a.proc, a.done = cmd, make(chan struct{})
		go func(cmd *exec.Cmd, done chan struct{}) {
			err := cmd.Wait()
			log.Printf("%s: exited: %v", a.spec.name, err)
			a.mu.Lock()
			if a.proc == cmd {
				a.proc = nil
			}
			a.mu.Unlock()
			close(done)
		}(cmd, a.done)
	}
	done := a.done
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if a.listening() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return fmt.Errorf("%s exited during start-up", a.spec.name)
		case <-time.After(250 * time.Millisecond):
		}
	}
	return fmt.Errorf("%s did not start listening in time", a.spec.name)
}

func (a *app) stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proc == nil || a.proc.Process == nil {
		return
	}
	pid := a.proc.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	done := a.done
	go func() {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	}()
}

func (a *app) idleLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if a.active.Load() > 0 {
				continue
			}
			if time.Since(time.Unix(0, a.lastUsed.Load())) > a.idle {
				a.mu.Lock()
				running := a.proc != nil
				a.mu.Unlock()
				if running {
					log.Printf("%s: idle for %s, stopping", a.spec.name, a.idle)
					a.stop()
				}
			}
		}
	}
}

func validToken(r *http.Request, token string) bool {
	h := r.Header.Get("Authorization")
	const p = "token "
	if !strings.HasPrefix(h, p) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(h, p)), []byte(token)) == 1
}

func (a *app) handler() http.Handler {
	target := &url.URL{Scheme: "http", Host: a.upstreamAddr()}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = pr.In.Host // apps compare Origin with Host
			pr.Out.Header.Del("Authorization")
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			log.Printf("%s: proxy error: %v", a.spec.name, err)
			http.Error(w, a.spec.name+" is not responding", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.token == "" || !validToken(r, a.token) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		a.active.Add(1)
		defer func() {
			a.active.Add(-1)
			a.lastUsed.Store(time.Now().UnixNano())
		}()
		a.lastUsed.Store(time.Now().UnixNano())
		if err := a.ensure(r.Context()); err != nil {
			log.Printf("%s: %v", a.spec.name, err)
			http.Error(w, a.spec.name+" could not be started: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		rp.ServeHTTP(w, r)
	})
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("itk-app-gate: ")
	specs, cmdArgs, err := parseArgs(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	idle := 30 * time.Minute
	if v, err := time.ParseDuration(os.Getenv("ITK_APP_IDLE")); err == nil && v >= time.Minute {
		idle = v
	}
	token := os.Getenv("ITK_APP_TOKEN")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var apps []*app
	for _, s := range specs {
		a := &app{spec: s, token: token, idle: idle}
		a.lastUsed.Store(time.Now().UnixNano())
		apps = append(apps, a)
		srv := &http.Server{Addr: ":" + strconv.Itoa(s.listen), Handler: a.handler(), ReadHeaderTimeout: 30 * time.Second}
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("%s: listen: %v", s.name, err)
			}
		}()
		go a.idleLoop(ctx)
	}
	if token == "" && len(apps) > 0 {
		log.Print("ITK_APP_TOKEN is not set: all app requests will be refused")
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	stopAll := func() {
		for _, a := range apps {
			a.stop()
		}
	}

	if len(cmdArgs) == 0 {
		s := <-sigs
		log.Printf("received %s, shutting down", s)
		stopAll()
		time.Sleep(500 * time.Millisecond)
		return
	}
	child := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		log.Fatalf("start %s: %v", cmdArgs[0], err)
	}
	go func() {
		for s := range sigs {
			_ = child.Process.Signal(s)
		}
	}()
	err = child.Wait()
	stopAll()
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) {
		os.Exit(ee.ExitCode())
	}
	if err != nil {
		log.Fatal(err)
	}
}
