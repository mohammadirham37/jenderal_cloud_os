package agentproto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"sync"
	"time"
)

// Header names used for request authentication.
const (
	HeaderTimestamp = "X-ITK-Agent-Timestamp"
	HeaderNonce     = "X-ITK-Agent-Nonce"
	HeaderSignature = "X-ITK-Agent-Signature"
	// HeaderOp carries the base64url JSON Request envelope for streaming and
	// WebSocket endpoints whose body is not a JSON envelope.
	HeaderOp = "X-ITK-Agent-Op"
)

// MaxClockSkew is the accepted timestamp window.
const MaxClockSkew = 60 * time.Second

// Digest returns the hex SHA-256 of data.
func Digest(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// Sign computes the request signature.
func Sign(secret []byte, method, path string, ts int64, nonce, digest string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(method + "\n" + path + "\n" + strconv.FormatInt(ts, 10) + "\n" + nonce + "\n" + digest))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verifier checks signatures and rejects replays.
type Verifier struct {
	secret []byte
	mu     sync.Mutex
	seen   map[string]time.Time
	now    func() time.Time
}

// NewVerifier creates a verifier for the shared secret.
func NewVerifier(secret []byte) *Verifier {
	return &Verifier{secret: secret, seen: map[string]time.Time{}, now: time.Now}
}

// Verify validates a signature. digest must be computed by the caller from
// the body (or op header for streaming endpoints).
func (v *Verifier) Verify(method, path, tsHeader, nonce, sig, digest string) error {
	ts, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil {
		return errors.New("invalid timestamp")
	}
	now := v.now()
	if d := now.Sub(time.Unix(ts, 0)); d > MaxClockSkew || d < -MaxClockSkew {
		return errors.New("timestamp outside allowed window")
	}
	if len(nonce) < 16 || len(nonce) > 64 {
		return errors.New("invalid nonce")
	}
	want := Sign(v.secret, method, path, ts, nonce, digest)
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return errors.New("invalid signature")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	for k, exp := range v.seen {
		if now.After(exp) {
			delete(v.seen, k)
		}
	}
	if _, dup := v.seen[nonce]; dup {
		return errors.New("replayed nonce")
	}
	v.seen[nonce] = now.Add(2 * MaxClockSkew)
	return nil
}
