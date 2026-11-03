package agentproto

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCleanRelPath(t *testing.T) {
	ok := map[string]string{"": "", "/": "", "a/b": "a/b", "/a/./b/": "a/b", "a/b/../c": "a/c", "a\\b": "a/b"}
	for in, want := range ok {
		got, err := CleanRelPath(in)
		if err != nil || got != want {
			t.Errorf("CleanRelPath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	// Traversal is neutralised by rooting at "/" before cleaning, so ../ can
	// never escape; but NUL bytes and over-long names are rejected.
	if got, _ := CleanRelPath("../../etc/passwd"); got != "etc/passwd" {
		t.Errorf("traversal not contained: %q", got)
	}
	if _, err := CleanRelPath("a\x00b"); err == nil {
		t.Error("NUL byte must be rejected")
	}
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := CleanRelPath(string(long)); err == nil {
		t.Error("over-long segment must be rejected")
	}
}

func TestSignAndVerify(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	v := NewVerifier(secret)
	ts := time.Now().Unix()
	digest := Digest([]byte(`{"x":1}`))
	sig := Sign(secret, "POST", PathOps, ts, "nonce-0123456789ab", digest)
	tsS := itoa(ts)
	if err := v.Verify("POST", PathOps, tsS, "nonce-0123456789ab", sig, digest); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if err := v.Verify("POST", PathOps, tsS, "nonce-0123456789ab", sig, digest); err == nil {
		t.Fatal("replay must be rejected")
	}
	if err := v.Verify("POST", PathOps, tsS, "nonce-other-0123456", sig, digest); err == nil {
		t.Fatal("signature for a different nonce must be rejected")
	}
	if err := v.Verify("POST", PathOps, tsS, "nonce-zzzzzzzzzzzzz", Sign([]byte("wrong-secret-wrong-secret-wrong!"), "POST", PathOps, ts, "nonce-zzzzzzzzzzzzz", digest), digest); err == nil {
		t.Fatal("wrong secret must be rejected")
	}
	old := time.Now().Add(-5 * time.Minute).Unix()
	if err := v.Verify("POST", PathOps, itoa(old), "nonce-old-012345678", Sign(secret, "POST", PathOps, old, "nonce-old-012345678", digest), digest); err == nil {
		t.Fatal("stale timestamp must be rejected")
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestDecodePayloadRejectsUnknownOpsAndFields(t *testing.T) {
	if _, err := DecodePayload(&Request{Operation: "exec", Payload: []byte(`{"cmd":"rm -rf /"}`)}); err == nil {
		t.Fatal("unknown operation must be rejected")
	}
	if _, err := DecodePayload(&Request{Operation: OpFilesList, Payload: []byte(`{"workspace_id":"00000000-0000-0000-0000-000000000001","path":"x","cmd":"y"}`)}); err == nil {
		t.Fatal("unknown payload field must be rejected")
	}
	p, err := DecodePayload(&Request{Operation: OpFilesList, Payload: []byte(`{"workspace_id":"00000000-0000-0000-0000-000000000001","path":"/a/../b"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if p.(*FilePath).Path != "b" {
		t.Fatalf("path not normalised: %q", p.(*FilePath).Path)
	}
}

func TestContainerCreateValidation(t *testing.T) {
	base := ContainerCreate{
		WorkspaceID: "00000000-0000-0000-0000-000000000001", ContainerID: "00000000-0000-0000-0000-000000000002",
		OwnerID: "00000000-0000-0000-0000-000000000003", Name: "main", Image: "itk-cloud/ubuntu-dev:1.0",
		CPUMillis: 1000, MemoryBytes: 1 << 30, PidsLimit: 512, RunAsUID: 1000, RunAsGID: 1000,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	root := base
	root.RunAsUID = 0
	if root.Validate() == nil {
		t.Fatal("root containers must be rejected")
	}
	priv := base
	priv.Ports = []PortSpec{{Port: 22}}
	if priv.Validate() == nil {
		t.Fatal("privileged ports must be rejected")
	}
	env := base
	env.Env = map[string]string{"bad-key": "x"}
	if env.Validate() == nil {
		t.Fatal("invalid env key must be rejected")
	}
}

func TestEnvelopeValidation(t *testing.T) {
	r := Request{ProtocolVersion: 2, Operation: OpAgentInfo, RequestID: "r", ActorID: "a", Reason: "x"}
	if r.Validate() == nil {
		t.Fatal("protocol version mismatch must be rejected")
	}
	r.ProtocolVersion = ProtocolVersion
	r.Reason = ""
	if r.Validate() == nil {
		t.Fatal("reason is required")
	}
}
