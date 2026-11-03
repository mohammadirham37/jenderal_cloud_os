package redisx

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/itk-cloud/itk-cloud/internal/platform/crypto"
)

func TestLimiter(t *testing.T) {
	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL not set")
	}
	ctx := context.Background()
	rdb, err := Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	l := NewLimiter(rdb, "test:"+crypto.RandomToken(6))
	for i := 1; i <= 3; i++ {
		ok, _, err := l.Allow(ctx, "k", 3, time.Minute)
		if err != nil || !ok {
			t.Fatalf("attempt %d should pass: %v", i, err)
		}
	}
	if ok, ttl, _ := l.Allow(ctx, "k", 3, time.Minute); ok || ttl <= 0 {
		t.Fatalf("4th attempt must be limited (ttl=%v)", ttl)
	}
	_ = l.Reset(ctx, "k")
	if ok, _, _ := l.Allow(ctx, "k", 3, time.Minute); !ok {
		t.Fatal("reset should clear counter")
	}
}
