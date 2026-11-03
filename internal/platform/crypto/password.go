// Package crypto provides password hashing, random tokens, authenticated
// encryption for recoverable secrets and TOTP.
package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP recommended baseline: m=64MiB, t=3, p=2 is
// stronger than the minimum m=19MiB,t=2,p=1).
type Argon2Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

// DefaultArgon2 are the production parameters.
var DefaultArgon2 = Argon2Params{Memory: 64 * 1024, Time: 3, Threads: 2, KeyLen: 32, SaltLen: 16}

// params is overridable in tests to keep them fast.
var params = DefaultArgon2

// SetArgon2ParamsForTesting lowers cost parameters (tests only).
func SetArgon2ParamsForTesting(p Argon2Params) { params = p }

// ErrMismatch is returned when a password does not match.
var ErrMismatch = errors.New("password mismatch")

// HashPassword returns a PHC-formatted Argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, params.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, params.Time, params.Memory, params.Threads, params.KeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, params.Memory, params.Time, params.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword checks password against a PHC Argon2id hash in constant time.
// needsRehash is true when the stored parameters are weaker than current ones.
func VerifyPassword(encoded, password string) (needsRehash bool, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("unsupported argon2 version")
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, errors.New("invalid argon2 parameters")
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errors.New("invalid salt")
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil {
		return false, errors.New("invalid hash")
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return false, ErrMismatch
	}
	return m < params.Memory || t < params.Time, nil
}

// dummyHash is used to equalize timing when a user does not exist.
var dummyHash string

// VerifyDummy performs a password verification against a throwaway hash so
// that unknown usernames take the same time as wrong passwords.
func VerifyDummy(password string) {
	if dummyHash == "" {
		dummyHash, _ = HashPassword("itk-cloud-dummy-password")
	}
	_, _ = VerifyPassword(dummyHash, password)
}

// ValidatePasswordStrength enforces a minimal password policy.
func ValidatePasswordStrength(pw string) string {
	if len(pw) < 12 {
		return "must be at least 12 characters"
	}
	if len(pw) > 256 {
		return "must be at most 256 characters"
	}
	var lower, upper, digit, other bool
	for _, c := range pw {
		switch {
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= '0' && c <= '9':
			digit = true
		default:
			other = true
		}
	}
	classes := 0
	for _, b := range []bool{lower, upper, digit, other} {
		if b {
			classes++
		}
	}
	if classes < 3 {
		return "must contain at least three of: lowercase, uppercase, digits, symbols"
	}
	return ""
}

// RandomToken returns a URL-safe random token with n bytes of entropy.
func RandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// RandomPassword returns a random password suitable for generated credentials.
func RandomPassword(n int) string {
	const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// HashToken returns SHA-256(token) for storing bearer tokens at rest.
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
