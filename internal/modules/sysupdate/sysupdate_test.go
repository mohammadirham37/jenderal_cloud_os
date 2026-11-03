package sysupdate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/itk-cloud/itk-cloud/internal/modules/audit"
	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.6", "0.1.5", 1},
		{"v0.1.5", "0.1.5", 0},
		{"0.2.0", "0.10.0", -1},
		{"1.0.0", "0.99.99", 1},
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0-rc.2", "1.0.0-rc.10", -1},
		{"1.0.0-alpha", "1.0.0-1", 1},
		{"1.0.0-alpha.1", "1.0.0-alpha", 1},
	}
	for _, c := range cases {
		got, ok := CompareVersions(c.a, c.b)
		if !ok || got != c.want {
			t.Errorf("CompareVersions(%q,%q) = %d,%v; want %d", c.a, c.b, got, ok, c.want)
		}
	}
	for _, bad := range []string{"dev", "", "1.2", "1.2.3.4", "1.2.3+build", "v1.2.x"} {
		if _, ok := CompareVersions(bad, "1.0.0"); ok {
			t.Errorf("%q must not parse", bad)
		}
	}
}

func TestToReleaseAndAssetSelection(t *testing.T) {
	gr := ghRelease{TagName: "v0.1.6", Body: "notes", HTMLURL: "https://github.com/o/r/releases/tag/v0.1.6", Assets: []ghAsset{
		{Name: "itk-cloud-0.1.6-linux-amd64.tar.gz", Size: 10}, {Name: "itk-cloud-0.1.6-linux-amd64.tar.gz.sha256", Size: 1},
		{Name: "itk-cloud-0.1.6-linux-arm64.tar.gz", Size: 10},
	}}
	rel, err := toRelease(gr, "amd64")
	if err != nil || rel.Version != "0.1.6" || !rel.AssetAvailable || rel.Asset != "itk-cloud-0.1.6-linux-amd64.tar.gz" {
		t.Fatalf("amd64: %+v %v", rel, err)
	}
	if rel, _ := toRelease(gr, "arm64"); rel.AssetAvailable {
		t.Fatal("arm64 without checksum file must not be installable")
	}
	for _, tag := range []string{"0.1.6", "v0.1", "release-1", "v01.1.6"} {
		if _, err := toRelease(ghRelease{TagName: tag}, "amd64"); err == nil {
			t.Errorf("tag %q must be rejected", tag)
		}
	}
	if _, err := toRelease(ghRelease{TagName: "v1.0.0", Prerelease: true}, "amd64"); err == nil {
		t.Error("pre-release must be rejected")
	}
	if rel, _ := toRelease(ghRelease{TagName: "v1.0.0", HTMLURL: "javascript:alert(1)"}, "amd64"); rel.HTMLURL != "" {
		t.Error("non-GitHub html_url must be dropped")
	}
}

func TestUpdateAvailable(t *testing.T) {
	rel := &Release{Version: "0.1.6", AssetAvailable: true}
	if !updateAvailable("0.1.5", rel) || updateAvailable("0.1.6", rel) || updateAvailable("0.2.0", rel) || updateAvailable("dev", rel) {
		t.Fatal("unexpected updateAvailable result")
	}
	rel.AssetAvailable = false
	if updateAvailable("0.1.5", rel) {
		t.Fatal("release without package must not be offered")
	}
}

func githubStub(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		if r.URL.Path != "/repos/o/r/releases/latest" || r.Header.Get("User-Agent") != UserAgent {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(ghRelease{TagName: "v0.1.6", Body: "Fixes", Assets: []ghAsset{
			{Name: "itk-cloud-0.1.6-linux-amd64.tar.gz", Size: 1}, {Name: "itk-cloud-0.1.6-linux-amd64.tar.gz.sha256", Size: 1}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckerCachesAndToleratesOffline(t *testing.T) {
	var hits int32
	srv := githubStub(t, &hits)
	c := &Checker{Repo: "o/r", Arch: "amd64", APIBase: srv.URL}
	if s := c.Latest(context.Background(), false); s.Err != "" || s.Release == nil || s.Release.Version != "0.1.6" {
		t.Fatalf("first check: %+v", s)
	}
	c.Latest(context.Background(), false)
	if hits != 1 {
		t.Fatalf("cached check must not hit GitHub again (hits=%d)", hits)
	}
	c.Latest(context.Background(), true)
	if hits != 2 {
		t.Fatalf("forced check must hit GitHub (hits=%d)", hits)
	}
	srv.Close()
	s := c.Latest(context.Background(), true)
	if s.Err == "" || s.Release == nil {
		t.Fatalf("offline check must keep the last release and report an error: %+v", s)
	}
}

type fakeAgent struct {
	ops      []string
	payloads []any
}

func (f *fakeAgent) Do(_ context.Context, op, _, _ string, payload, out any) error {
	f.ops = append(f.ops, op)
	f.payloads = append(f.payloads, payload)
	if st, ok := out.(*Job); ok {
		st.State = agentproto.UpdateQueued
	}
	return nil
}

func serve(h httpx.HandlerFunc, p *authz.Principal, body string) *httptest.ResponseRecorder {
	method := http.MethodGet
	if body != "" {
		method = http.MethodPost
	}
	r := httptest.NewRequest(method, "/x", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(authz.WithPrincipal(r.Context(), p))
	w := httptest.NewRecorder()
	httpx.Handle(h).ServeHTTP(w, r)
	return w
}

func TestHandlerPermissions(t *testing.T) {
	var hits int32
	srv := githubStub(t, &hits)
	ag := &fakeAgent{}
	var audits []audit.Entry
	h := &Handler{Svc: &Service{Checker: &Checker{Repo: "o/r", Arch: "amd64", APIBase: srv.URL}, Agent: ag, Installed: "0.1.5", Enabled: true},
		Audit: func(_ context.Context, e audit.Entry) error { audits = append(audits, e); return nil }}
	student := &authz.Principal{Permissions: map[string]bool{"workspaces.create": true}}
	viewer := &authz.Principal{Permissions: map[string]bool{PermView: true}}
	super := &authz.Principal{Permissions: map[string]bool{PermView: true, PermUpdate: true}}
	good := `{"version":"0.1.6","confirm":"v0.1.6","reason":"monthly maintenance window"}`

	if w := serve(h.Get, student, ""); w.Code != http.StatusForbidden {
		t.Fatalf("student GET: %d", w.Code)
	}
	w := serve(h.Get, viewer, "")
	if w.Code != http.StatusOK {
		t.Fatalf("viewer GET: %d", w.Code)
	}
	var resp struct{ Data Status }
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || !resp.Data.UpdateAvailable || resp.Data.CanUpdate {
		t.Fatalf("viewer status: %s", w.Body.String())
	}
	if w := serve(h.Start, viewer, good); w.Code != http.StatusForbidden {
		t.Fatalf("viewer start: %d", w.Code)
	}
	if len(audits) != 1 || audits[0].Outcome != audit.Denied {
		t.Fatalf("denied start must be audited: %+v", audits)
	}
	for _, bad := range []string{
		`{"version":"0.1.7","confirm":"v0.1.7","reason":"monthly maintenance window"}`,
		`{"version":"0.1.6","confirm":"yes","reason":"monthly maintenance window"}`,
		`{"version":"0.1.6","confirm":"v0.1.6","reason":"short"}`,
		`{"version":"0.1.6","confirm":"v0.1.6","reason":"monthly maintenance window","url":"https://evil"}`,
	} {
		if w := serve(h.Start, super, bad); w.Code < 400 || w.Code >= 500 {
			t.Fatalf("invalid start %s: %d", bad, w.Code)
		}
	}
	for _, op := range ag.ops {
		if op == agentproto.OpPlatformUpdate {
			t.Fatal("invalid requests must not reach the agent")
		}
	}
	if w := serve(h.Start, super, good); w.Code != http.StatusAccepted {
		t.Fatalf("super start: %d %s", w.Code, w.Body.String())
	}
	var sent *agentproto.PlatformUpdate
	for i, op := range ag.ops {
		if op == agentproto.OpPlatformUpdate {
			pu := ag.payloads[i].(agentproto.PlatformUpdate)
			sent = &pu
		}
	}
	if sent == nil || sent.Version != "0.1.6" || sent.Repo != "o/r" || sent.Arch != "amd64" {
		t.Fatalf("agent payload: %+v", sent)
	}
	if last := audits[len(audits)-1]; last.Action != "system.update" || last.Outcome != audit.Success {
		t.Fatalf("success must be audited: %+v", last)
	}
}
