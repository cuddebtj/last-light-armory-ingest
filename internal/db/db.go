// Package db owns every interaction with the shared last_light_armory
// Postgres database: connection pooling, schema migrations, the advisory
// lock that serializes ingest runs, and the repositories that perform
// idempotent upserts.
//
// Concurrency-safety notes:
//   - *pgxpool.Pool is safe for concurrent use; Store methods may be called
//     from multiple goroutines.
//   - A session-level Postgres advisory lock (AcquireIngestLock) guarantees
//     at most one ingest run mutates the database at a time, even across
//     hosts.
//   - golang-migrate takes its own lock during Migrate, so concurrent
//     migration attempts are also safe.
package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the pgx5:// migrate driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cuddebtj/last-light-armory-ingest/migrations"
)

// ingestLockKey is the pg_advisory_lock key serializing ingest runs.
// Arbitrary but fixed: ASCII "LLAINGST" as a 64-bit integer.
const ingestLockKey int64 = 0x4C4C41494E475354

// Connect builds a pgx connection pool with conservative defaults suitable
// for a batch job: modest pool size, connect timeout, and a startup ping so
// a bad DATABASE_URL fails fast with a clear error.
func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		// Deliberately not wrapping err: pgx echoes the URL, credentials
		// included, in its parse errors.
		return nil, errors.New("db: DATABASE_URL failed to parse (are special characters in the password percent-encoded?)")
	}
	cfg.MaxConns = 8
	cfg.MinConns = 1
	cfg.ConnConfig.ConnectTimeout = 10 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: creating pool: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: pinging database: %w", err)
	}
	return pool, nil
}

// Migrate applies all pending embedded migrations. It is a no-op when the
// schema is already current, making it safe to run at every startup.
func Migrate(databaseURL string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("db: loading embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, pgx5URL(databaseURL))
	if err != nil {
		return fmt.Errorf("db: initializing migrator: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db: applying migrations: %w", err)
	}
	return nil
}

// MigrateDown rolls back every migration. Exposed for integration tests;
// the ingest binary never calls it.
func MigrateDown(databaseURL string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("db: loading embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, pgx5URL(databaseURL))
	if err != nil {
		return fmt.Errorf("db: initializing migrator: %w", err)
	}
	defer m.Close()

	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("db: reverting migrations: %w", err)
	}
	return nil
}

// pgx5URL rewrites a postgres:// or postgresql:// URL to the pgx5:// scheme
// golang-migrate's pgx/v5 driver registers.
func pgx5URL(databaseURL string) string {
	if rest, ok := strings.CutPrefix(databaseURL, "postgresql://"); ok {
		return "pgx5://" + rest
	}
	if rest, ok := strings.CutPrefix(databaseURL, "postgres://"); ok {
		return "pgx5://" + rest
	}
	return databaseURL
}

// AcquireIngestLock takes the session-level advisory lock that serializes
// ingest runs. It returns a release function that must be called (typically
// deferred) before the process exits; the lock also dies with the session
// if the process crashes.
//
// The lock is non-blocking: if another run holds it, an error is returned
// immediately rather than queueing a second import behind the first.
func AcquireIngestLock(ctx context.Context, pool *pgxpool.Pool) (release func(), err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("db: acquiring connection for advisory lock: %w", err)
	}

	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", ingestLockKey).Scan(&locked); err != nil {
		conn.Release()
		return nil, fmt.Errorf("db: taking advisory lock: %w", err)
	}
	if !locked {
		conn.Release()
		return nil, errors.New("db: another ingest run holds the advisory lock; refusing to run concurrently")
	}

	return func() {
		// Best-effort unlock with a fresh timeout: the parent ctx may
		// already be cancelled during shutdown, and releasing the
		// connection returns the lock anyway when the session ends.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", ingestLockKey)
		conn.Release()
	}, nil
}
