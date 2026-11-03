package crypto

import (
	"encoding/base32"
	"errors"
	"strings"
	"testing"
	"time"
)

func init() {
	SetArgon2ParamsForTesting(Argon2Params{Memory: 1024, Time: 1, Threads: 1, KeyLen: 32, SaltLen: 16})
}

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("Correct-Horse-9")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("unexpected format %q", h)
	}
	if _, err := VerifyPassword(h, "Correct-Horse-9"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if _, err := VerifyPassword(h, "wrong"); !errors.Is(err, ErrMismatch) {
		t.Fatalf("expected mismatch, got %v", err)
	}
	if _, err := VerifyPassword("$bcrypt$x", "x"); err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

func TestPasswordStrength(t *testing.T) {
	cases := map[string]bool{"short": false, "alllowercaseletters": false, "Lowercase-and-1": true, "NoDigitsButSymbols!": true}
	for pw, ok := range cases {
		if got := ValidatePasswordStrength(pw) == ""; got != ok {
			t.Errorf("%q: got ok=%v want %v", pw, got, ok)
		}
	}
}

func TestSecretBox(t *testing.T) {
	key := make([]byte, 32)
	box, err := NewSecretBox(key)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := box.Seal([]byte("s3cret"), []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := box.Open(ct, []byte("row-1"))
	if err != nil || string(pt) != "s3cret" {
		t.Fatalf("open: %v %q", err, pt)
	}
	if _, err := box.Open(ct, []byte("row-2")); err == nil {
		t.Fatal("expected AAD mismatch to fail")
	}
	if _, err := NewSecretBox([]byte("short")); err == nil {
		t.Fatal("expected short key error")
	}
}

// RFC 6238 Appendix B test vectors (SHA1, 8 digits truncated to 6 here).
func TestTOTPRFCVectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	vectors := map[int64]string{59: "94287082", 1111111109: "07081804", 1234567890: "89005924", 2000000000: "69279037"}
	for ts, want8 := range vectors {
		got, err := TOTPCode(secret, ts/30)
		if err != nil {
			t.Fatal(err)
		}
		if got != want8[2:] {
			t.Errorf("t=%d got %s want %s", ts, got, want8[2:])
		}
	}
}

func TestVerifyTOTPRejectsReplay(t *testing.T) {
	secret := NewTOTPSecret()
	now := time.Unix(1700000000, 0)
	code, _ := TOTPCode(secret, TOTPStep(now))
	step, ok := VerifyTOTP(secret, code, now, 0)
	if !ok {
		t.Fatal("expected valid code")
	}
	if _, ok := VerifyTOTP(secret, code, now, step); ok {
		t.Fatal("replayed code must be rejected")
	}
	if _, ok := VerifyTOTP(secret, "000000x", now, 0); ok {
		t.Fatal("malformed code must be rejected")
	}
}
