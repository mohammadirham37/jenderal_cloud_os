// Package app wires configuration, infrastructure and modules into the API
// server and worker processes.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/itk-cloud/itk-cloud/internal/modules/mail"
	"github.com/itk-cloud/itk-cloud/internal/modules/settings"
	"github.com/itk-cloud/itk-cloud/internal/platform/config"
	"github.com/itk-cloud/itk-cloud/internal/platform/crypto"
	"github.com/itk-cloud/itk-cloud/internal/platform/db"
	"github.com/itk-cloud/itk-cloud/internal/platform/redisx"
	"github.com/itk-cloud/itk-cloud/packages/agentproto"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// Deps holds shared infrastructure.
type Deps struct {
	Cfg      *config.Config
	Log      *slog.Logger
	Pool     *pgxpool.Pool
	Redis    *redis.Client
	Box      *crypto.SecretBox
	Settings *settings.Store
	Mail     mail.Sender
	Agent    *agentproto.Client
}

// NewDeps connects to PostgreSQL and Redis.
func NewDeps(ctx context.Context, cfg *config.Config, log *slog.Logger) (*Deps, error) {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	rdb, err := redisx.Connect(ctx, cfg.RedisURL)
	if err != nil {
		pool.Close()
		return nil, err
	}
	box, err := crypto.NewSecretBox(cfg.EncryptionKey)
	if err != nil {
		return nil, err
	}
	return &Deps{
		Cfg: cfg, Log: log, Pool: pool, Redis: rdb, Box: box,
		Settings: &settings.Store{Pool: pool},
		Mail: &mail.SMTP{Host: cfg.SMTPHost, Port: cfg.SMTPPort, Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword, From: cfg.SMTPFrom},
		Agent: agentproto.NewClient(cfg.AgentSocketPath, cfg.AgentSharedSecret),
	}, nil
}

// Close releases connections.
func (d *Deps) Close() {
	if d.Redis != nil {
		_ = d.Redis.Close()
	}
	if d.Pool != nil {
		d.Pool.Close()
	}
}

func baseOrigin(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s://%s", u.Scheme, u.Host)
}
