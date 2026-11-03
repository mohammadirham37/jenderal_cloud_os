// Package idempotency replays responses of mutating requests that carry an
// Idempotency-Key header, so clients can safely retry provisioning calls.
package idempotency

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/itk-cloud/itk-cloud/internal/platform/authz"
	"github.com/itk-cloud/itk-cloud/internal/platform/httpx"
)

// Header is the idempotency request header.
const Header = "Idempotency-Key"

type recorder struct {
	http.ResponseWriter
	status int
	buf    bytes.Buffer
}

func (r *recorder) WriteHeader(s int) { r.status = s; r.ResponseWriter.WriteHeader(s) }
func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.buf.Write(b)
	return r.ResponseWriter.Write(b)
}

// Wrap makes fn idempotent per (user, Idempotency-Key).
func Wrap(pool *pgxpool.Pool, fn httpx.HandlerFunc) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		key := r.Header.Get(Header)
		p := authz.FromContext(r.Context())
		if key == "" || p == nil {
			return fn(w, r)
		}
		if len(key) < 8 || len(key) > 100 {
			return httpx.Validation(map[string]string{Header: "must be 8-100 characters"})
		}
		ctx := r.Context()
		var method, path string
		var status int
		var body []byte
		err := pool.QueryRow(ctx, `SELECT method, path, status_code, response FROM api_idempotency_keys
			WHERE user_id=$1 AND key=$2 AND created_at > now() - interval '24 hours'`, p.UserID, key).Scan(&method, &path, &status, &body)
		if err == nil {
			if method != r.Method || path != r.URL.Path {
				return httpx.Conflict("Idempotency-Key was already used for a different request.")
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Idempotent-Replay", "true")
			w.WriteHeader(status)
			_, _ = w.Write(body)
			return nil
		}
		rec := &recorder{ResponseWriter: w}
		if err := fn(rec, r); err != nil {
			return err
		}
		if rec.status >= 200 && rec.status < 300 && json.Valid(rec.buf.Bytes()) {
			_, _ = pool.Exec(ctx, `INSERT INTO api_idempotency_keys (key, user_id, method, path, status_code, response)
				VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (user_id, key) DO NOTHING`, key, p.UserID, r.Method, r.URL.Path, rec.status, rec.buf.Bytes())
		}
		return nil
	}
}
