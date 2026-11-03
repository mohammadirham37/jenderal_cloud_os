// Package redisx wraps Redis for rate limiting, short-lived coordination and
// pub/sub event fan-out.
package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Connect parses REDIS_URL and pings the server.
func Connect(ctx context.Context, url string) (*redis.Client, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	c := redis.NewClient(opt)
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.Ping(pctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return c, nil
}

// Limiter is a fixed-window rate limiter.
type Limiter struct {
	rdb    *redis.Client
	prefix string
}

// NewLimiter creates a limiter whose keys are namespaced by prefix.
func NewLimiter(rdb *redis.Client, prefix string) *Limiter {
	return &Limiter{rdb: rdb, prefix: prefix}
}

var incrScript = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
if n == 1 then redis.call('PEXPIRE', KEYS[1], ARGV[1]) end
return {n, redis.call('PTTL', KEYS[1])}
`)

// Allow increments the counter for key and reports whether it is within
// limit per window, plus the time until reset. Fails open with an error so
// callers can decide; login endpoints fail closed.
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	res, err := incrScript.Run(ctx, l.rdb, []string{l.prefix + ":" + key}, window.Milliseconds()).Int64Slice()
	if err != nil {
		return false, 0, err
	}
	return res[0] <= int64(limit), time.Duration(res[1]) * time.Millisecond, nil
}

// Reset clears a counter (e.g. after successful login).
func (l *Limiter) Reset(ctx context.Context, key string) error {
	return l.rdb.Del(ctx, l.prefix+":"+key).Err()
}
