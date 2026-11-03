// Package security contains focused security regression tests: IDOR/BOLA
// across every resource type, session fixation, secret leakage, injection
// attempts, unauthorized WebSocket access and response hardening.
package security

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/itk-cloud/itk-cloud/tests/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

// fixture creates two students with their own resources.
type fixture struct {
	e                                         *testenv.Env
	admin, alice, bob                         *testenv.Client
	aliceWS, aliceProject, aliceTerm, aliceID string
}

func newFixture(t *testing.T) *fixture {
	e := testenv.Setup(t, testenv.Opts{GPU: true})
	f := &fixture{e: e}
	f.admin = e.AdminClient()
	f.alice = e.CreateUser(f.admin, "alice", []string{"lecturer"}, "research")
	f.bob = e.CreateUser(f.admin, "bob", []string{"lecturer"}, "research")
	f.aliceID = testenv.Str(f.alice.Do("GET", "/api/v1/auth/me", nil).Data(), "id")
	f.aliceProject = testenv.Str(f.alice.Do("POST", "/api/v1/projects", map[string]any{"name": "Alice Research"}).Expect(t, 201, "project").Data(), "id")
	f.aliceWS = testenv.Str(f.alice.Do("POST", "/api/v1/workspaces", map[string]any{"name": "alice ws", "template_key": "ubuntu-dev", "project_id": f.aliceProject}).
		Expect(t, 202, "workspace").Data(), "id")
	return f
}

// TestIDOR checks that a user cannot read or modify another user's resources
// through any endpoint, and that the response does not reveal existence.
func TestIDOR(t *testing.T) {
	f := newFixture(t)
	ws, proj := f.aliceWS, f.aliceProject
	// Alice's GPU request and database.
	var gpuID string
	_ = f.e.Deps.Pool.QueryRow(context.Background(), `INSERT INTO gpu_devices (uuid, index, name) VALUES ('GPU-aaaaaaaa-1111-2222-3333-444444444444',0,'T') RETURNING id`).Scan(&gpuID)
	req := testenv.Str(f.alice.Do("POST", "/api/v1/gpu/requests", map[string]any{"workspace_id": ws, "duration_hours": 1, "reason": "alice experiment run"}).Expect(t, 201, "gpu req").Data(), "id")
	dbResp := f.alice.Do("POST", "/api/v1/databases", map[string]any{"project_id": proj, "display_name": "alice db"}).Expect(t, 202, "db").Data()
	dbID := testenv.Str(dbResp["database"].(map[string]any), "id")
	f.e.Drain()

	cases := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/workspaces/" + ws, nil},
		{"PATCH", "/api/v1/workspaces/" + ws, map[string]any{"name": "pwned"}},
		{"POST", "/api/v1/workspaces/" + ws + "/start", nil},
		{"POST", "/api/v1/workspaces/" + ws + "/stop", nil},
		{"DELETE", "/api/v1/workspaces/" + ws, nil},
		{"GET", "/api/v1/workspaces/" + ws + "/jobs", nil},
		{"POST", "/api/v1/workspaces/" + ws + "/members", map[string]any{"user_id": f.aliceID, "role": "editor"}},
		{"GET", "/api/v1/workspaces/" + ws + "/files", nil},
		{"GET", "/api/v1/workspaces/" + ws + "/files/download?path=.bashrc", nil},
		{"POST", "/api/v1/workspaces/" + ws + "/files/mkdir", map[string]any{"path": "x"}},
		{"DELETE", "/api/v1/workspaces/" + ws + "/files?path=x", nil},
		{"POST", "/api/v1/workspaces/" + ws + "/terminals", nil},
		{"GET", "/api/v1/monitoring/workspaces/" + ws, nil},
		{"GET", "/api/v1/projects/" + proj, nil},
		{"PATCH", "/api/v1/projects/" + proj, map[string]any{"name": "pwned"}},
		{"POST", "/api/v1/projects/" + proj + "/members", map[string]any{"username": "bob", "role": "maintainer"}},
		{"GET", "/api/v1/projects/" + proj + "/activity", nil},
		{"DELETE", "/api/v1/projects/" + proj, nil},
		{"GET", "/api/v1/databases/" + dbID, nil},
		{"POST", "/api/v1/databases/" + dbID + "/credentials/reset", nil},
		{"DELETE", "/api/v1/databases/" + dbID + "?confirm=x", nil},
		{"POST", "/api/v1/gpu/requests/" + req + "/cancel", nil},
		{"GET", "/api/v1/users/" + f.aliceID, nil},
		{"PATCH", "/api/v1/users/" + f.aliceID, map[string]any{"full_name": "pwned"}},
		{"GET", "/api/v1/users/" + f.aliceID + "/usage", nil},
		{"POST", "/api/v1/gpu/requests", map[string]any{"workspace_id": ws, "duration_hours": 1, "reason": "steal alice workspace"}},
		{"POST", "/api/v1/containers", map[string]any{"workspace_id": ws, "template_id": "00000000-0000-0000-0000-000000000000", "name": "evil"}},
	}
	for _, c := range cases {
		r := f.bob.Do(c.method, c.path, c.body)
		if r.Status != 404 && r.Status != 403 {
			t.Errorf("%s %s by another user: expected 404/403, got %d %s", c.method, c.path, r.Status, r.Raw)
		}
		if r.Status == 403 && strings.Contains(c.path, ws) {
			t.Errorf("%s %s leaks workspace existence with 403", c.method, c.path)
		}
	}
	// Lists never include other users' resources.
	for _, p := range []string{"/api/v1/workspaces", "/api/v1/projects", "/api/v1/containers"} {
		r := f.bob.Do("GET", p, nil).Expect(t, 200, p)
		if strings.Contains(string(r.Raw), ws) || strings.Contains(string(r.Raw), proj) {
			t.Errorf("%s exposes another user's resources", p)
		}
	}
	dbs := f.bob.Do("GET", "/api/v1/databases", nil).Expect(t, 200, "db list")
	if strings.Contains(string(dbs.Raw), dbID) {
		t.Error("database list exposes another project's databases")
	}
	reqs := f.bob.Do("GET", "/api/v1/gpu/requests", nil).Expect(t, 200, "gpu requests")
	if strings.Contains(string(reqs.Raw), req) {
		t.Error("GPU request list exposes another user's requests")
	}
	// Alice's resources are unchanged.
	d := f.alice.Do("GET", "/api/v1/workspaces/"+ws, nil).Expect(t, 200, "alice ws").Data()
	if testenv.Str(d, "name") != "alice ws" {
		t.Fatal("workspace was modified by another user")
	}
}

func TestSessionFixationAndCookieFlags(t *testing.T) {
	e := testenv.Setup(t, testenv.Opts{})
	e.CreateAdmin("root", "Root-Passw0rd!x")
	c := e.NewClient()
	// An attacker-chosen session cookie is ignored and replaced at login.
	u, _ := http.NewRequest("GET", e.Base, nil)
	c.HTTP.Jar.SetCookies(u.URL, []*http.Cookie{{Name: "itk_session", Value: "attacker-chosen-value", Path: "/"}})
	r := c.Do("POST", "/api/v1/auth/login", map[string]string{"username": "root", "password": "Root-Passw0rd!x"}).Expect(t, 200, "login")
	var setCookie string
	for _, sc := range r.Header.Values("Set-Cookie") {
		if strings.HasPrefix(sc, "itk_session=") {
			setCookie = sc
		}
	}
	if setCookie == "" || strings.Contains(setCookie, "attacker-chosen-value") {
		t.Fatalf("login must issue a new session cookie, got %q", setCookie)
	}
	for _, flag := range []string{"HttpOnly", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(setCookie, flag) {
			t.Errorf("session cookie missing %s: %s", flag, setCookie)
		}
	}
	// The MFA step rotates the session again (pre-MFA token is revoked).
	c.EnrollMFA()
	var preMFA string
	for _, ck := range c.HTTP.Jar.Cookies(u.URL) {
		if ck.Name == "itk_session" {
			preMFA = ck.Value
		}
	}
	if preMFA == "" || strings.Contains(setCookie, preMFA) {
		t.Fatal("session token must change after MFA verification")
	}
	old := e.NewClient()
	old.HTTP.Jar.SetCookies(u.URL, []*http.Cookie{{Name: "itk_session", Value: strings.TrimPrefix(strings.Split(setCookie, ";")[0], "itk_session="), Path: "/"}})
	old.Do("GET", "/api/v1/auth/me", nil).Expect(t, 401, "pre-MFA token must be revoked")
}

func TestNoSecretLeakage(t *testing.T) {
	f := newFixture(t)
	forbidden := []string{"password_hash", "argon2id", "secret_encrypted", "token_hash", "password_encrypted",
		"AGENT_SHARED_SECRET", "ENCRYPTION_KEY", "recovery_codes\":[\"", "stack", "goroutine", "panic"}
	paths := []string{"/api/v1/auth/me", "/api/v1/users", "/api/v1/users/" + f.aliceID, "/api/v1/workspaces/" + f.aliceWS,
		"/api/v1/admin/settings", "/api/v1/admin/audit-logs?per_page=100", "/api/v1/databases", "/api/v1/admin/agent/status",
		"/api/v1/config", "/api/v1/admin/backups", "/api/v1/roles", "/api/v1/dashboard"}
	for _, p := range paths {
		r := f.admin.Do("GET", p, nil)
		body := string(r.Raw)
		for _, s := range forbidden {
			if strings.Contains(body, s) {
				t.Errorf("%s response contains sensitive marker %q", p, s)
			}
		}
		if strings.Contains(body, f.e.Cfg.WorkspaceRoot) {
			t.Errorf("%s response exposes the host workspace path", p)
		}
	}
	// Audit metadata never contains passwords.
	var n int
	_ = f.e.Deps.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs WHERE metadata::text ILIKE '%Str0ng-Pass%' OR metadata::text ILIKE '%password":"%'`).Scan(&n)
	if n > 0 {
		t.Fatal("audit log contains password material")
	}
	// Errors do not leak internals.
	r := f.admin.Do("GET", "/api/v1/workspaces/not-a-uuid", nil)
	if r.Status != 404 || strings.Contains(string(r.Raw), "sql") || strings.Contains(string(r.Raw), "pgx") {
		t.Fatalf("unexpected error body: %d %s", r.Status, r.Raw)
	}
}

func TestInjectionInputsAreInert(t *testing.T) {
	f := newFixture(t)
	payloads := []string{"$(touch /tmp/itk-pwned)", "`id`", "; rm -rf / #", "<script>alert(1)</script>", "' OR '1'='1"}
	for i, p := range payloads {
		// Names are stored as data, never executed or interpreted.
		r := f.alice.Do("POST", "/api/v1/projects", map[string]any{"name": p, "slug": "inj-" + string(rune('a'+i)) + "-x"})
		if r.Status != 201 {
			t.Fatalf("project with payload name: %d %s", r.Status, r.Raw)
		}
		if testenv.Str(r.Data(), "name") != p {
			t.Fatalf("payload was altered: %v", r.Data())
		}
	}
	// Search parameters are parameterised (no SQL injection).
	r := f.admin.Do("GET", "/api/v1/users?q="+strings.ReplaceAll("' OR 1=1 --", " ", "%20"), nil).Expect(t, 200, "search")
	if len(r.List()) != 0 {
		t.Fatalf("SQL-like search returned rows: %d", len(r.List()))
	}
	f.admin.Do("GET", "/api/v1/users?sort=username%20DESC%2C%20(SELECT%201)", nil).Expect(t, 422, "sort injection")
	// Unknown JSON fields are rejected (mass assignment).
	f.alice.Do("PATCH", "/api/v1/users/"+f.aliceID, map[string]any{"full_name": "A", "roles": []string{"super_admin"}}).Expect(t, 403, "self role change")
	f.alice.Do("POST", "/api/v1/projects", map[string]any{"name": "x", "owner_id": "00000000-0000-0000-0000-000000000000"}).Expect(t, 400, "unknown field")
}

func TestUnauthorizedWebSocketAndHeaders(t *testing.T) {
	e := testenv.Setup(t, testenv.Opts{})
	// No session: the terminal WebSocket endpoint refuses the upgrade.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, strings.Replace(e.Base, "http", "ws", 1)+"/api/v1/terminals/00000000-0000-0000-0000-000000000001/ws",
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {e.Base}}})
	if err == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatalf("anonymous WebSocket must be rejected with 401 (err=%v)", err)
	}
	// Security headers on API responses.
	r, err := http.Get(e.Base + "/api/v1/config")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	for h, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Cache-Control": "no-store"} {
		if got := r.Header.Get(h); got != want {
			t.Errorf("header %s = %q, want %q", h, got, want)
		}
	}
	if !strings.Contains(r.Header.Get("Content-Security-Policy"), "default-src 'none'") {
		t.Error("API responses must carry a restrictive CSP")
	}
	// Oversized JSON bodies are rejected.
	big := strings.Repeat("a", 2<<20)
	req, _ := http.NewRequest("POST", e.Base+"/api/v1/auth/login", strings.NewReader(`{"username":"`+big+`","password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 413 && res.StatusCode != 400 {
		t.Fatalf("oversized body must be rejected, got %d", res.StatusCode)
	}
	// The app proxy is not served on the main origin.
	c := e.AdminClient()
	ws := testenv.Str(c.Do("POST", "/api/v1/workspaces", map[string]any{"name": "p", "template_key": "ubuntu-dev"}).Expect(t, 202, "ws").Data(), "id")
	c.Do("GET", "/api/v1/workspaces/"+ws+"/proxy/8888/", nil).Expect(t, 404, "proxy on main origin")
}
