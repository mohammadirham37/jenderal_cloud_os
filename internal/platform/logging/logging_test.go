package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestRedactsSensitiveKeys(t *testing.T) {
	var buf bytes.Buffer
	l := New("info", &buf)
	l.Info("login", "password", "hunter2", "token", "abc", "user", "alice")
	out := buf.String()
	if strings.Contains(out, "hunter2") || strings.Contains(out, "\"abc\"") {
		t.Fatalf("secret leaked into log: %s", out)
	}
	if !strings.Contains(out, "alice") {
		t.Fatalf("expected non-sensitive field in log: %s", out)
	}
}
