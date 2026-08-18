package migrate

import (
	"context"
	_ "embed"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/001_init.sql
var migration001 string

func Run(ctx context.Context, pool *pgxpool.Pool) error {
	c, e := pool.Acquire(ctx)
	if e != nil {
		return e
	}
	defer c.Release()
	if _, e = c.Exec(ctx, "SELECT pg_advisory_lock($1)", int64(0x52434e)); e != nil {
		return e
	}
	defer c.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", int64(0x52434e))
	var exists bool
	if e = c.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name='schema_migrations')").Scan(&exists); e != nil {
		return e
	}
	if exists {
		var n int
		if e = c.QueryRow(ctx, "SELECT count(*) FROM schema_migrations WHERE version=1").Scan(&n); e != nil {
			return e
		}
		if n > 0 {
			return nil
		}
	}
	tx, e := c.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, migration001, pgx.QueryExecModeSimpleProtocol); e != nil {
		return fmt.Errorf("migration 1: %w", e)
	}
	if _, e = tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES(1) ON CONFLICT DO NOTHING"); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
