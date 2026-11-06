package migrations

import (
	"context"
	"embed"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.sql
var files embed.FS

func Apply(ctx context.Context, pool *pgxpool.Pool, project string) error {
	tx, e := pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(724318)`); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS portfolio_migrations(name text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now())`); e != nil {
		return e
	}
	for _, name := range []string{"common_001.sql", project + "_001.sql"} {
		var exists bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM portfolio_migrations WHERE name=$1)`, name).Scan(&exists); e != nil {
			return e
		}
		if exists {
			continue
		}
		sql, e := files.ReadFile(name)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, string(sql)); e != nil {
			return fmt.Errorf("migration %s: %w", name, e)
		}
		if _, e = tx.Exec(ctx, `INSERT INTO portfolio_migrations(name) VALUES($1)`, name); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
