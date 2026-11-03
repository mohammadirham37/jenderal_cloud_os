package integration

import (
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestJupyterLabThroughAppProxy starts the JupyterLab template and verifies:
// the app is reachable only through the separate proxy origin, the platform
// session cookie is not forwarded, and the server rejects requests that do not
// carry the per-container token (direct loopback access).
func TestJupyterLabThroughAppProxy(t *testing.T) {
	e := setup(t, envOpts{})
	if !e.DockerOK || exec.Command("docker", "image", "inspect", "jenderal/jupyterlab:1.0").Run() != nil {
		t.Skip("docker or jenderal/jupyterlab:1.0 not available")
	}
	admin := e.AdminClient()
	u := e.CreateUser(admin, "peneliti", []string{"lecturer"}, "standard")
	other := e.CreateUser(admin, "mhs2", []string{"student"}, "")
	ws := str(u.Do("POST", "/api/v1/workspaces", map[string]any{"name": "notebook", "template_key": "jupyterlab", "cpu_millis": 1000, "memory_bytes": 1 << 30}).
		Expect(t, 202, "create").Data(), "id")
	e.Drain()
	u.Do("POST", "/api/v1/workspaces/"+ws+"/start", nil).Expect(t, 202, "start")
	e.Drain()
	d := u.Do("GET", "/api/v1/workspaces/"+ws, nil).Expect(t, 200, "get").Data()
	if str(d, "status") != "running" {
		t.Fatalf("workspace not running: %v", d["status_reason"])
	}
	ctr := d["containers"].([]any)[0].(map[string]any)
	ports := ctr["ports"].([]any)
	hostPort := int(ports[0].(map[string]any)["host_port"].(float64))
	if hostPort == 0 {
		t.Fatal("port 8888 was not published")
	}
	path := "/api/v1/workspaces/" + ws + "/proxy/8888/api/status"
	proxyGet := func(c *Client, host string) (int, string) {
		req, _ := http.NewRequest("GET", e.Base+path, nil)
		req.Host = host
		// Browsers send host-only cookies to every port of the host; Go's
		// client matches cookies against the overridden Host, so add them.
		for _, ck := range c.HTTP.Jar.Cookies(req.URL) {
			req.Header.Add("Cookie", ck.Name+"="+ck.Value)
		}
		resp, err := (&http.Client{}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	// JupyterLab needs a few seconds to start.
	var code int
	var body string
	for i := 0; i < 60; i++ {
		code, body = proxyGet(u, "127.0.0.2:1")
		if code == 200 {
			break
		}
		time.Sleep(time.Second)
	}
	if code != 200 || !strings.Contains(body, "started") {
		t.Fatalf("JupyterLab via proxy: %d %s", code, body)
	}
	// Not served on the main origin.
	if c, _ := proxyGet(u, strings.TrimPrefix(e.Base, "http://")); c != 404 {
		t.Fatalf("proxy must not be served on the main origin, got %d", c)
	}
	// The console API is not reachable on the proxy origin.
	req, _ := http.NewRequest("GET", e.Base+"/api/v1/auth/me", nil)
	req.Host = "127.0.0.2:1"
	for _, ck := range u.HTTP.Jar.Cookies(req.URL) {
		req.Header.Add("Cookie", ck.Name+"="+ck.Value)
	}
	if r, err := (&http.Client{}).Do(req); err != nil || r.StatusCode != 404 {
		t.Fatalf("API must not be served on the proxy origin (err=%v)", err)
	}
	// Other users cannot open it.
	if c, _ := proxyGet(other, "127.0.0.2:1"); c != 404 {
		t.Fatalf("another user must not reach the app, got %d", c)
	}
	// Direct loopback access without the token is rejected by JupyterLab.
	resp, err := http.Get("http://127.0.0.1:" + itoa(hostPort) + "/api/v1/workspaces/" + ws + "/proxy/8888/api/contents")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 && resp.StatusCode != 401 {
		t.Fatalf("direct access without token must be refused, got %d", resp.StatusCode)
	}
	u.Do("POST", "/api/v1/workspaces/"+ws+"/stop", nil).Expect(t, 202, "stop")
	e.Drain()
}

func itoa(n int) string {
	return func() string {
		b := []byte{}
		if n == 0 {
			return "0"
		}
		for n > 0 {
			b = append([]byte{byte('0' + n%10)}, b...)
			n /= 10
		}
		return string(b)
	}()
}
