// Package db provides the PostgreSQL connection pool, migrations and
// transaction helpers.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/itk-cloud/itk-cloud/migrations"
)

// Querier is satisfied by *pgxpool.Pool and pgx.Tx so repositories can run
// inside or outside a transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Connect opens a pgx pool and verifies connectivity.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	// Default to 20 connections unless the URL sets pool_max_conns (small
	// servers lower it).
	if !strings.Contains(url, "pool_max_conns") {
		cfg.MaxConns = 20
	}
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.ConnConfig.RuntimeParams["application_name"] = "itk-cloud"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

func gooseDB(pool *pgxpool.Pool) (*sql.DB, error) {
	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return nil, err
	}
	return stdlib.OpenDBFromPool(pool), nil
}

// migrationLockKey is the advisory lock that serialises concurrent migration
// runs (e.g. installer and API starting at the same time).
const migrationLockKey = 7_406_163_201

// Migrate applies all pending migrations while holding an advisory lock.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(migrationLockKey)); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, int64(migrationLockKey))
	}()
	sqlDB, err := gooseDB(pool)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	return goose.UpContext(ctx, sqlDB, ".")
}

// MigrationStatus returns (current version, latest available version).
func MigrationStatus(ctx context.Context, pool *pgxpool.Pool) (int64, int64, error) {
	sqlDB, err := gooseDB(pool)
	if err != nil {
		return 0, 0, err
	}
	defer sqlDB.Close()
	cur, err := goose.GetDBVersionContext(ctx, sqlDB)
	if err != nil {
		return 0, 0, err
	}
	migs, err := goose.CollectMigrations(".", 0, goose.MaxVersion)
	if err != nil {
		return cur, 0, err
	}
	last, err := migs.Last()
	if err != nil {
		return cur, 0, err
	}
	return cur, last.Version, nil
}

// WithTx runs fn in a transaction, committing on success.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IsNotFound reports whether err is pgx.ErrNoRows.
func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// IsUniqueViolation reports whether err is a unique constraint violation,
// optionally on a specific constraint.
func IsUniqueViolation(err error, constraint string) bool {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return constraint == "" || pe.ConstraintName == constraint
	}
	return false
}

// IsForeignKeyViolation reports whether err is a foreign key violation.
func IsForeignKeyViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23503"
}

// LikePattern escapes a user search term for ILIKE '%term%'.
func LikePattern(s string) string {
	out := make([]rune, 0, len(s)+2)
	out = append(out, '%')
	for _, r := range s {
		if r == '%' || r == '_' || r == '\\' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(append(out, '%'))
}
