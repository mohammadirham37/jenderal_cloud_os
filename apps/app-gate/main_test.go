package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseArgs(t *testing.T) {
	apps, cmd, err := parseArgs([]string{"--app", "vscode:13337:13338:code-server --x", "--app=desktop:6080:6081:start", "--", "sleep", "infinity"})
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 || apps[0].name != "vscode" || apps[0].listen != 13337 || apps[0].upstream != 13338 || apps[0].command != "code-server --x" {
		t.Fatalf("apps = %+v", apps)
	}
	if len(cmd) != 2 || cmd[0] != "sleep" {
		t.Fatalf("cmd = %v", cmd)
	}
	for _, bad := range [][]string{{"--app", "x:80:13338:c"}, {"--app", "x:13337:13337:c"}, {"--app", "x:1:2"}, {"--bogus"}} {
		if _, _, err := parseArgs(bad); err == nil {
			t.Errorf("parseArgs(%v) accepted", bad)
		}
	}
}

func TestTokenRequired(t *testing.T) {
	a := &app{spec: appSpec{name: "t", listen: 20000, upstream: 20001, command: "true"}, token: "secret"}
	h := a.handler()
	for _, auth := range []string{"", "token wrong", "Bearer secret"} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("auth %q: status %d, want 403", auth, w.Code)
		}
	}
	empty := (&app{spec: a.spec}).handler()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "token ")
	w := httptest.NewRecorder()
	empty.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("empty token accepted: %d", w.Code)
	}
}
