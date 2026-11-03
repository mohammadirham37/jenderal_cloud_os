package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func dockerInspect(t *testing.T, id, format string) string {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f", format, id).CombinedOutput()
	if err != nil {
		t.Fatalf("docker inspect: %v %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func waitStatus(t *testing.T, e *Env, c *Client, id, want string) map[string]any {
	t.Helper()
	e.Drain()
	d := c.Do("GET", "/api/v1/workspaces/"+id, nil).Expect(t, 200, "get workspace").Data()
	if str(d, "status") != want {
		var jobs []map[string]any
		rows, _ := e.Deps.Pool.Query(context.Background(), `SELECT kind, status, coalesce(last_error,'') FROM background_jobs WHERE resource_id=$1`, id)
		for rows.Next() {
			var k, s, le string
			_ = rows.Scan(&k, &s, &le)
			jobs = append(jobs, map[string]any{"kind": k, "status": s, "error": le})
		}
		rows.Close()
		t.Fatalf("workspace status = %s (reason %v), want %s; jobs: %v", str(d, "status"), d["status_reason"], want, jobs)
	}
	return d
}

func upload(t *testing.T, c *Client, path, name, content string) Resp {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write([]byte(content))
	mw.Close()
	req, _ := http.NewRequest("POST", c.Base()+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-CSRF-Token", c.CSRF)
	req.Header.Set("Origin", c.Base())
	resp, err := c.HTTP.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := Resp{Status: resp.StatusCode, Raw: raw}
	_ = json.Unmarshal(raw, &out.Body)
	return out
}

// terminalRun opens a terminal WebSocket, runs a command and returns output
// until marker appears.
func terminalRun(t *testing.T, c *Client, wsPath, cmd, marker string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	u, _ := http.NewRequest("GET", c.Base()+wsPath, nil)
	h := http.Header{"Origin": {c.Base()}}
	for _, ck := range c.HTTP.Jar.Cookies(u.URL) {
		h.Add("Cookie", ck.Name+"="+ck.Value)
	}
	conn, resp, err := websocket.Dial(ctx, strings.Replace(c.Base(), "http", "ws", 1)+wsPath, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		body := ""
		if resp != nil {
			b, _ := io.ReadAll(resp.Body)
			body = string(b)
		}
		t.Fatalf("terminal dial: %v %s", err, body)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":100,"rows":30}`))
	if err := conn.Write(ctx, websocket.MessageBinary, []byte(cmd+"\n")); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for !strings.Contains(out.String(), marker) {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("terminal read: %v (output so far: %q)", err, out.String())
		}
		out.Write(data)
	}
	return out.String()
}

func TestContainerWorkspaceLifecycle(t *testing.T) {
	e := setup(t, envOpts{})
	if !e.DockerOK {
		t.Skip("docker or image jenderal/ubuntu-dev:1.0 not available")
	}
	admin := e.AdminClient()
	student := e.CreateUser(admin, "mhs1", []string{"student"}, "")
	other := e.CreateUser(admin, "mhs2", []string{"student"}, "")
	assistant := e.CreateUser(admin, "asisten", []string{"assistant"}, "")

	// Project.
	proj := student.Do("POST", "/api/v1/projects", map[string]any{"name": "Tugas Akhir", "description": "TA"}).Expect(t, 201, "create project").Data()
	projID := str(proj, "id")

	// Template catalog.
	tpls := student.Do("GET", "/api/v1/templates", nil).Expect(t, 200, "templates").List()
	if len(tpls) != 12 {
		t.Fatalf("expected 12 templates (11 development + web browser), got %d", len(tpls))
	}

	// Create workspace (idempotent with Idempotency-Key).
	body := map[string]any{"name": "Praktikum Linux", "template_key": "ubuntu-dev", "project_id": projID,
		"cpu_millis": 500, "memory_bytes": 512 << 20, "storage_bytes": 1 << 30}
	w1 := student.Do("POST", "/api/v1/workspaces", body, "Idempotency-Key", "create-ws-0001").Expect(t, 202, "create workspace").Data()
	w2 := student.Do("POST", "/api/v1/workspaces", body, "Idempotency-Key", "create-ws-0001").Expect(t, 202, "replay create").Data()
	wsID := str(w1, "id")
	if str(w2, "id") != wsID {
		t.Fatal("idempotent create returned a different workspace")
	}
	waitStatus(t, e, student, wsID, "ready")

	// Isolation: other users and assistants cannot access private data.
	other.Do("GET", "/api/v1/workspaces/"+wsID, nil).Expect(t, 404, "other user get workspace")
	other.Do("GET", "/api/v1/workspaces/"+wsID+"/files", nil).Expect(t, 404, "other user files")
	other.Do("POST", "/api/v1/workspaces/"+wsID+"/start", nil).Expect(t, 404, "other user start")
	other.Do("DELETE", "/api/v1/workspaces/"+wsID, nil).Expect(t, 404, "other user delete")
	assistant.Do("GET", "/api/v1/workspaces/"+wsID, nil).Expect(t, 200, "assistant can view metadata")
	assistant.Do("GET", "/api/v1/workspaces/"+wsID+"/files", nil).Expect(t, 403, "assistant cannot read files")

	// Start → running, with hardened container settings actually applied.
	student.Do("POST", "/api/v1/workspaces/"+wsID+"/start", nil).Expect(t, 202, "start")
	student.Do("POST", "/api/v1/workspaces/"+wsID+"/start", nil).Expect(t, 409, "concurrent start")
	d := waitStatus(t, e, student, wsID, "running")
	ctr := d["containers"].([]any)[0].(map[string]any)
	rid := str(ctr, "runtime_id")
	checks := map[string]string{
		"{{.Config.User}}":                              "1000:1000",
		"{{.HostConfig.NanoCpus}}":                      "500000000",
		"{{.HostConfig.Memory}}":                        "536870912",
		"{{.HostConfig.MemorySwap}}":                    "536870912",
		"{{.HostConfig.PidsLimit}}":                     "512",
		"{{.HostConfig.Privileged}}":                    "false",
		"{{json .HostConfig.CapDrop}}":                  `["ALL"]`,
		"{{.HostConfig.NetworkMode}}":                   "itk-ws-" + wsID,
		"{{index .Config.Labels \"itk.workspace_id\"}}": wsID,
	}
	for f, want := range checks {
		if got := dockerInspect(t, rid, f); got != want {
			t.Errorf("container %s = %q, want %q", f, got, want)
		}
	}
	if so := dockerInspect(t, rid, "{{json .HostConfig.SecurityOpt}}"); !strings.Contains(so, "no-new-privileges") {
		t.Errorf("SecurityOpt missing no-new-privileges: %s", so)
	}
	if mounts := dockerInspect(t, rid, "{{json .Mounts}}"); strings.Contains(mounts, "docker.sock") {
		t.Fatal("docker socket must never be mounted")
	}

	// Terminal: runs as the unprivileged user, inside the workspace.
	term := student.Do("POST", "/api/v1/workspaces/"+wsID+"/terminals", map[string]int{"cols": 100, "rows": 30}).Expect(t, 201, "open terminal").Data()
	out := terminalRun(t, student, str(term, "websocket_path"), "echo ITK-$((40+2)) uid=$(id -u) home=$HOME", "ITK-42 uid=1000 home=/home/jenderal")
	if !strings.Contains(out, "uid=1000") {
		t.Fatalf("unexpected terminal output %q", out)
	}
	// Reconnect re-validates and works again.
	terminalRun(t, student, str(term, "websocket_path"), "echo again-$((1+1))", "again-2")
	// Another user cannot open or hijack the terminal.
	other.Do("POST", "/api/v1/workspaces/"+wsID+"/terminals", nil).Expect(t, 404, "other user terminal")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h := http.Header{"Origin": {e.Base}}
	u, _ := http.NewRequest("GET", e.Base, nil)
	for _, ck := range other.HTTP.Jar.Cookies(u.URL) {
		h.Add("Cookie", ck.Name+"="+ck.Value)
	}
	if _, resp, err := websocket.Dial(ctx, strings.Replace(e.Base, "http", "ws", 1)+str(term, "websocket_path"), &websocket.DialOptions{HTTPHeader: h}); err == nil || resp == nil || resp.StatusCode != 404 {
		t.Fatalf("other user must not attach to the terminal (err=%v)", err)
	}
	// Cross-site WebSocket (wrong Origin) is rejected.
	h2 := http.Header{"Origin": {"http://evil.example"}}
	for _, ck := range student.HTTP.Jar.Cookies(u.URL) {
		h2.Add("Cookie", ck.Name+"="+ck.Value)
	}
	if _, _, err := websocket.Dial(ctx, strings.Replace(e.Base, "http", "ws", 1)+str(term, "websocket_path"), &websocket.DialOptions{HTTPHeader: h2}); err == nil {
		t.Fatal("cross-origin WebSocket must be rejected")
	}

	// Files.
	fb := "/api/v1/workspaces/" + wsID + "/files"
	student.Do("POST", fb+"/mkdir", map[string]string{"path": "src"}).Expect(t, 201, "mkdir")
	upload(t, student, fb+"/upload?path=src", "hello.py", "print('halo')\n").Expect(t, 201, "upload")
	if r := upload(t, student, fb+"/upload?path=src", "hello.py", "x"); r.Status != 409 {
		t.Fatalf("upload without overwrite must conflict, got %d", r.Status)
	}
	list := student.Do("GET", fb+"?path=src", nil).Expect(t, 200, "list").Data()
	if len(list["entries"].([]any)) != 1 {
		t.Fatalf("expected 1 entry: %v", list)
	}
	content := student.Do("GET", fb+"/content?path=src/hello.py", nil).Expect(t, 200, "read").Data()
	if str(content, "content") != "print('halo')\n" {
		t.Fatalf("unexpected content %v", content)
	}
	student.Do("PUT", fb+"/content", map[string]any{"path": "src/hello.py", "content": "print('ITK')\n", "expected_mod_time": "2001-01-01T00:00:00Z"}).Expect(t, 409, "stale write")
	student.Do("PUT", fb+"/content", map[string]any{"path": "src/hello.py", "content": "print('ITK')\n", "expected_mod_time": str(content, "mod_time")}).Expect(t, 200, "write")
	// The file written via the file manager is visible in the container.
	terminalRun(t, student, str(term, "websocket_path"), "cat ~/src/hello.py && stat -c owner=%u ~/src/hello.py", "owner=")
	req, _ := http.NewRequest("GET", e.Base+fb+"/download?path=src/hello.py", nil)
	resp, err := student.HTTP.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	dl, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(dl) != "print('ITK')\n" || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download: %q %v", dl, resp.Header)
	}
	// Path traversal stays inside the workspace.
	student.Do("GET", fb+"/content?path=../../../../etc/resolv.conf", nil).Expect(t, 404, "traversal read")
	student.Do("GET", fb+"/download?path=%2e%2e/%2e%2e/etc/passwd", nil).Expect(t, 404, "traversal download")
	student.Do("PATCH", fb+"/rename", map[string]string{"from": "src", "to": "src/inner"}).Expect(t, 422, "move into itself")
	student.Do("PATCH", fb+"/rename", map[string]string{"from": "src/hello.py", "to": "src/main.py"}).Expect(t, 200, "rename")
	student.Do("DELETE", fb+"?path=", nil).Expect(t, 422, "delete root")
	student.Do("DELETE", fb+"?path=src", nil).Expect(t, 409, "delete non-empty")
	student.Do("DELETE", fb+"?path=src&recursive=true", nil).Expect(t, 204, "delete recursive")

	// Logs & metrics.
	student.Do("GET", "/api/v1/containers/"+str(ctr, "id")+"/logs?tail=50", nil).Expect(t, 200, "logs")
	student.Do("GET", "/api/v1/monitoring/workspaces/"+wsID, nil).Expect(t, 200, "workspace metrics")
	other.Do("GET", "/api/v1/containers/"+str(ctr, "id")+"/logs", nil).Expect(t, 404, "other user logs")

	// Workspace members: editors get access.
	mhs2ID := str(other.Do("GET", "/api/v1/auth/me", nil).Data(), "id")
	student.Do("POST", "/api/v1/workspaces/"+wsID+"/members", map[string]string{"user_id": mhs2ID, "role": "viewer"}).Expect(t, 200, "add viewer")
	other.Do("GET", "/api/v1/workspaces/"+wsID, nil).Expect(t, 200, "viewer get")
	other.Do("GET", fb, nil).Expect(t, 403, "viewer files")
	student.Do("DELETE", "/api/v1/workspaces/"+wsID+"/members/"+mhs2ID, nil).Expect(t, 204, "remove member")
	other.Do("GET", "/api/v1/workspaces/"+wsID, nil).Expect(t, 404, "removed member")

	// Emergency access requires a reason and is audited.
	admin.Do("GET", fb, nil).Expect(t, 403, "admin without reason")
	admin.Do("GET", fb, nil, "X-Access-Reason", "investigating abuse report #12").Expect(t, 200, "admin with reason")

	// Delete requires stop first; stop then delete.
	student.Do("DELETE", "/api/v1/workspaces/"+wsID, nil).Expect(t, 409, "delete running")
	student.Do("POST", "/api/v1/workspaces/"+wsID+"/stop", nil).Expect(t, 202, "stop")
	waitStatus(t, e, student, wsID, "stopped")
	if st := dockerInspect(t, rid, "{{.State.Running}}"); st != "false" {
		t.Fatal("container still running after stop")
	}
	student.Do("DELETE", "/api/v1/workspaces/"+wsID, nil).Expect(t, 202, "delete")
	e.Drain()
	student.Do("GET", "/api/v1/workspaces/"+wsID, nil).Expect(t, 404, "deleted workspace")
	if err := exec.Command("docker", "inspect", rid).Run(); err == nil {
		t.Fatal("container should be removed after workspace deletion")
	}
}

func TestQuotaEnforcement(t *testing.T) {
	e := setup(t, envOpts{})
	admin := e.AdminClient()
	student := e.CreateUser(admin, "mhs1", []string{"student"}, "basic")
	// Basic: 1 core, 1 GiB RAM, 5 GiB storage, 2 workspaces.
	student.Do("POST", "/api/v1/workspaces", map[string]any{"name": "a", "template_key": "ubuntu-dev", "cpu_millis": 2000}).Expect(t, 422, "cpu above quota")
	student.Do("POST", "/api/v1/workspaces", map[string]any{"name": "a", "template_key": "ubuntu-dev", "storage_bytes": 6 << 30}).Expect(t, 422, "storage above quota")
	student.Do("POST", "/api/v1/workspaces", map[string]any{"name": "a", "template_key": "ml-cuda"}).Expect(t, 422, "gpu template without gpu quota")
	student.Do("POST", "/api/v1/workspaces", map[string]any{"name": "one", "template_key": "ubuntu-dev", "storage_bytes": 2 << 30}).Expect(t, 202, "ws 1")
	student.Do("POST", "/api/v1/workspaces", map[string]any{"name": "two", "template_key": "ubuntu-dev", "storage_bytes": 2 << 30}).Expect(t, 202, "ws 2")
	r := student.Do("POST", "/api/v1/workspaces", map[string]any{"name": "three", "template_key": "ubuntu-dev", "storage_bytes": 512 << 20})
	if r.Status != 422 || r.ErrCode() != "quota_exceeded" {
		t.Fatalf("third workspace must exceed quota: %d %s", r.Status, r.Raw)
	}
	u := student.Do("GET", "/api/v1/me/usage", nil).Expect(t, 200, "usage").Data()
	usage := u["usage"].(map[string]any)
	if usage["workspaces"].(float64) != 2 || usage["allocated_storage_bytes"].(float64) != float64(4<<30) {
		t.Fatalf("unexpected usage %v", usage)
	}
}
