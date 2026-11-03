// Package testenv provides the end-to-end test environment shared by the
// integration and security suites: the real API handler, a real host agent
// process, PostgreSQL, Redis and (when available) Docker.
//
// Required environment:
//
//	TEST_PG_ADMIN_URL  postgres URL with CREATEDB (a fresh database is created per test)
//	TEST_REDIS_URL     redis URL (a dedicated DB number; it is flushed)
//
// Docker-based tests additionally require a reachable Docker daemon
// (TEST_DOCKER_SOCKET) and the image itk-cloud/ubuntu-dev:1.0.
package testenv

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"github.com/itk-cloud/itk-cloud/internal/app"
	"github.com/itk-cloud/itk-cloud/internal/modules/users"
	"github.com/itk-cloud/itk-cloud/internal/platform/config"
	"github.com/itk-cloud/itk-cloud/internal/platform/crypto"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
)

var agentBin string

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// Main builds the agent binary and runs the tests (call from TestMain).
func Main(m *testing.M) {
	if os.Getenv("TEST_PG_ADMIN_URL") == "" || os.Getenv("TEST_REDIS_URL") == "" {
		fmt.Println("integration tests skipped: TEST_PG_ADMIN_URL and TEST_REDIS_URL are required")
		os.Exit(0)
	}
	crypto.SetArgon2ParamsForTesting(crypto.Argon2Params{Memory: 8 * 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16})
	dir, err := os.MkdirTemp("", "itk-it-bin")
	if err != nil {
		panic(err)
	}
	agentBin = filepath.Join(dir, "itk-cloud-agent")
	cmd := exec.Command("go", "build", "-o", agentBin, "./apps/agent")
	cmd.Dir = repoRoot()
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		panic("build agent: " + err.Error())
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Env is a running test environment.
type Env struct {
	T        *testing.T
	Cfg      *config.Config
	Deps     *app.Deps
	Svcs     *app.Services
	Worker   *app.Worker
	Server   *httptest.Server
	Base     string
	AgentCmd *exec.Cmd
	Identity *age.X25519Identity
	DockerOK bool
	dbName   string
	adminURL string
	agentEnv []string
}

// Opts configures Setup.
type Opts struct {
	GPU bool
}

func dockerSocket() string {
	if s := os.Getenv("TEST_DOCKER_SOCKET"); s != "" {
		return s
	}
	home, _ := os.UserHomeDir()
	for _, s := range []string{filepath.Join(home, ".docker/run/docker.sock"), "/var/run/docker.sock"} {
		if c, err := net.Dial("unix", s); err == nil {
			c.Close()
			return s
		}
	}
	return "/var/run/docker.sock"
}

func dockerAvailable() bool {
	out, err := exec.Command("docker", "image", "inspect", "itk-cloud/ubuntu-dev:1.0", "--format", "{{.Id}}").CombinedOutput()
	return err == nil && strings.HasPrefix(strings.TrimSpace(string(out)), "sha256:")
}

// Setup starts a fresh environment (new database, agent process, API server).
func Setup(t *testing.T, opts Opts) *Env {
	t.Helper()
	ctx := context.Background()
	adminURL := os.Getenv("TEST_PG_ADMIN_URL")
	adminConn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	dbName := "itk_it_" + randHex(6)
	if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatal(err)
	}
	adminConn.Close(ctx)
	u, _ := url.Parse(adminURL)
	u.Path = "/" + dbName
	dbURL := u.String()

	rdbURL := os.Getenv("TEST_REDIS_URL")
	ropt, _ := redis.ParseURL(rdbURL)
	rc := redis.NewClient(ropt)
	_ = rc.FlushDB(ctx).Err()
	rc.Close()

	tmp, err := os.MkdirTemp("", "itk-it-")
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := age.GenerateX25519Identity()
	secret := randHex(24)
	sock := filepath.Join(os.TempDir(), "itk-"+randHex(4)+".sock")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	vars := map[string]string{
		"APP_ENV": "test", "APP_BASE_URL": base, "APP_LOG_LEVEL": "error", "DATABASE_URL": dbURL, "REDIS_URL": rdbURL,
		"SESSION_SECRET": randHex(24), "ENCRYPTION_KEY": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		"AGENT_SOCKET_PATH": sock, "AGENT_SHARED_SECRET": secret, "WORKSPACE_ROOT": filepath.Join(tmp, "ws"),
		"BACKUP_ROOT": filepath.Join(tmp, "backups"), "BACKUP_AGE_RECIPIENT": identity.Recipient().String(),
		"CONFIG_DIR": repoRoot() + "/deployments/caddy", "GPU_ENABLED": fmt.Sprint(opts.GPU),
		"PROJECT_DB_ADMIN_URL": adminURL, "APP_PROXY_ORIGIN": "http://127.0.0.2:1",
	}
	cfg, err := config.FromEnv(func(k string) string { return vars[k] })
	if err != nil {
		t.Fatal(err)
	}
	agentEnv := append(os.Environ(),
		"AGENT_SOCKET_PATH="+sock, "AGENT_SHARED_SECRET="+secret, "AGENT_SOCKET_GROUP=nonexistent-itk-group",
		"WORKSPACE_ROOT="+filepath.Join(tmp, "ws"), "BACKUP_ROOT="+filepath.Join(tmp, "backups"),
		"AGENT_STORAGE_DRIVER=directory", "DOCKER_SOCKET="+dockerSocket(), "APP_LOG_LEVEL=error", "NVIDIA_SMI_PATH=nvidia-smi-not-installed")
	env := &Env{T: t, Cfg: cfg, Base: base, Identity: identity, dbName: dbName, adminURL: adminURL, agentEnv: agentEnv}
	env.StartAgent()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps, err := app.NewDeps(ctx, cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, deps.Pool); err != nil {
		t.Fatal(err)
	}
	env.Deps = deps
	env.Svcs = app.NewServices(deps)
	env.Worker = app.NewWorker(deps, env.Svcs)
	srv := httptest.NewUnstartedServer(app.NewAPIHandler(deps, env.Svcs))
	srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	env.Server = srv
	env.DockerOK = dockerAvailable()
	// Register the agent node so capability lookups work.
	_, _ = env.Svcs.Agent.Refresh(ctx)

	t.Cleanup(func() {
		srv.Close()
		env.StopAgent()
		// Remove docker leftovers created by this test run.
		if env.DockerOK {
			out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label=itk.managed=true").Output()
			for _, id := range strings.Fields(string(out)) {
				ws, _ := exec.Command("docker", "inspect", "-f", `{{index .Config.Labels "itk.workspace_id"}}`, id).Output()
				var exists bool
				_ = deps.Pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM workspaces WHERE id::text=$1)`, strings.TrimSpace(string(ws))).Scan(&exists)
				if exists {
					_ = exec.Command("docker", "rm", "-f", id).Run()
					_ = exec.Command("docker", "network", "rm", "itk-ws-"+strings.TrimSpace(string(ws))).Run()
				}
			}
		}
		deps.Close()
		c, err := pgx.Connect(context.Background(), adminURL)
		if err == nil {
			_, _ = c.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1`, dbName)
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+dbName)
			// Drop project databases/roles created by this run.
			rows, _ := c.Query(context.Background(), `SELECT datname FROM pg_database WHERE datname LIKE 'itk\_%' AND datname NOT LIKE 'itk_it_%'`)
			var leftovers []string
			for rows != nil && rows.Next() {
				var n string
				_ = rows.Scan(&n)
				leftovers = append(leftovers, n)
			}
			if rows != nil {
				rows.Close()
			}
			_ = leftovers // project DBs are dropped by the tests themselves
			c.Close(context.Background())
		}
		os.RemoveAll(tmp)
	})
	return env
}

// StartAgent starts the agent process.
func (e *Env) StartAgent() {
	cmd := exec.Command(agentBin)
	cmd.Env = e.agentEnv
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	if err := cmd.Start(); err != nil {
		e.T.Fatal(err)
	}
	e.AgentCmd = cmd
	sock := ""
	for _, kv := range e.agentEnv {
		if strings.HasPrefix(kv, "AGENT_SOCKET_PATH=") {
			sock = strings.TrimPrefix(kv, "AGENT_SOCKET_PATH=")
		}
	}
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("unix", sock); err == nil {
			c.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.T.Fatalf("agent did not start: %s", stderr.String())
}

// StopAgent stops the agent process.
func (e *Env) StopAgent() {
	if e.AgentCmd != nil && e.AgentCmd.Process != nil {
		_ = e.AgentCmd.Process.Kill()
		_ = e.AgentCmd.Wait()
		e.AgentCmd = nil
	}
}

// Drain runs queued jobs until none is ready (or timeout).
func (e *Env) Drain() {
	e.T.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		ran, err := e.Worker.Runner.RunOnce(ctx)
		if err != nil {
			e.T.Fatalf("job runner: %v", err)
		}
		if !ran {
			return
		}
	}
	e.T.Fatal("jobs did not drain in time")
}

// CreateAdmin bootstraps a super administrator.
func (e *Env) CreateAdmin(username, password string) {
	if _, err := users.CreateAdmin(context.Background(), e.Deps.Pool, username, username+"@itk.test", "Admin "+username, password); err != nil {
		e.T.Fatal(err)
	}
}

// Base returns the API base URL of the environment the client talks to.
func (c *Client) Base() string { return c.e.Base }

// Client is an HTTP client with cookie jar and CSRF handling.
type Client struct {
	e    *Env
	HTTP *http.Client
	CSRF string
	// MFASecret is set after enrollment.
	MFASecret string
	LastStep  int64
}

// nextCode returns a TOTP code for a step not used before (the server
// rejects replays), waiting if necessary.
func (c *Client) nextCode() string {
	for {
		cur := crypto.TOTPStep(time.Now())
		s := max(cur-1, c.LastStep+1)
		if s <= cur+1 {
			c.LastStep = s
			code, _ := crypto.TOTPCode(c.MFASecret, s)
			return code
		}
		time.Sleep(time.Second)
	}
}

// NewClient creates a browser-like client.
func (e *Env) NewClient() *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{e: e, HTTP: &http.Client{Jar: jar, Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// Resp is a decoded response.
type Resp struct {
	Status int
	Body   map[string]any
	Raw    []byte
	Header http.Header
}

// Data returns the "data" object.
func (r Resp) Data() map[string]any {
	d, _ := r.Body["data"].(map[string]any)
	return d
}

// List returns the "data" array.
func (r Resp) List() []any {
	d, _ := r.Body["data"].([]any)
	return d
}

// ErrCode returns error.code.
func (r Resp) ErrCode() string {
	if e, ok := r.Body["error"].(map[string]any); ok {
		s, _ := e["code"].(string)
		return s
	}
	return ""
}

func (c *Client) Do(method, path string, body any, headers ...string) Resp {
	c.e.T.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.e.Base+path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", c.e.Base)
	if c.CSRF != "" {
		req.Header.Set("X-CSRF-Token", c.CSRF)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		c.e.T.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := Resp{Status: resp.StatusCode, Raw: raw, Header: resp.Header}
	_ = json.Unmarshal(raw, &out.Body)
	if d := out.Data(); d != nil {
		if tok, ok := d["csrf_token"].(string); ok && tok != "" {
			c.CSRF = tok
		}
		if me, ok := d["me"].(map[string]any); ok {
			if tok, ok := me["csrf_token"].(string); ok {
				c.CSRF = tok
			}
		}
	}
	return out
}

// Expect asserts the status code.
func (r Resp) Expect(t *testing.T, status int, what string) Resp {
	t.Helper()
	if r.Status != status {
		t.Fatalf("%s: expected HTTP %d, got %d: %s", what, status, r.Status, string(r.Raw))
	}
	return r
}

// Login logs in and completes MFA when enrolled.
func (c *Client) Login(username, password string) Resp {
	c.e.T.Helper()
	r := c.Do("POST", "/api/v1/auth/login", map[string]string{"username": username, "password": password})
	if r.Status == 200 && c.MFASecret != "" {
		v := c.Do("POST", "/api/v1/auth/mfa/verify", map[string]string{"code": c.nextCode()})
		v.Expect(c.e.T, 200, "mfa verify")
	}
	return r
}

// EnrollMFA runs the MFA setup flow for the current session.
func (c *Client) EnrollMFA() {
	c.e.T.Helper()
	s := c.Do("POST", "/api/v1/auth/mfa/setup", nil).Expect(c.e.T, 200, "mfa setup")
	c.MFASecret = s.Data()["secret"].(string)
	c.LastStep = crypto.TOTPStep(time.Now())
	code, _ := crypto.TOTPCode(c.MFASecret, c.LastStep)
	c.Do("POST", "/api/v1/auth/mfa/enable", map[string]string{"code": code}).Expect(c.e.T, 200, "mfa enable")
}

// AdminClient returns a logged-in, MFA-verified super admin client.
func (e *Env) AdminClient() *Client {
	e.CreateAdmin("root", "Root-Passw0rd!x")
	c := e.NewClient()
	c.Login("root", "Root-Passw0rd!x").Expect(e.T, 200, "admin login")
	c.EnrollMFA()
	return c
}

// CreateUser creates a user via the API and returns a logged-in client
// (password changed from the temporary one).
func (e *Env) CreateUser(admin *Client, username string, roles []string, quota string) *Client {
	e.T.Helper()
	body := map[string]any{"username": username, "email": username + "@itk.test", "full_name": "User " + username, "roles": roles}
	if quota != "" {
		body["quota_key"] = quota
	}
	r := admin.Do("POST", "/api/v1/users", body).Expect(e.T, 201, "create user "+username)
	temp := r.Data()["temporary_password"].(string)
	c := e.NewClient()
	c.Login(username, temp).Expect(e.T, 200, "login "+username)
	c.Do("POST", "/api/v1/auth/change-password", map[string]string{"current_password": temp, "new_password": "Str0ng-Pass-" + username}).
		Expect(e.T, 204, "change password")
	return c
}

// Str returns m[k] as a string.
func Str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}
