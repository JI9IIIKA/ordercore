package main

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"ordercore/internal/platform"
	"ordercore/migrations"
	"ordercore/projects/ordercore"
)

func main() {
	platform.Serve("ordercore", ordercore.Register, func(ctx context.Context, pool *pgxpool.Pool) error { return migrations.Apply(ctx, pool, "ordercore") })
}
