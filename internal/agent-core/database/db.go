package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DBClient wraps the connection pool with tenant-scoped transaction support.
type DBClient struct {
	pool *pgxpool.Pool
}

// NewDBClient creates a new DBClient.
func NewDBClient(pool *pgxpool.Pool) *DBClient {
	return &DBClient{pool: pool}
}

// WithTenant executes fn inside a transaction with RLS tenant context set.
// MUST be called before any SQL query on platform-specific tables (agents schema).
// Implements the SBT pattern: SET LOCAL app.tenant_id enforces RLS isolation.
func (c *DBClient) WithTenant(ctx context.Context, tenantID string, fn func(*pgxpool.Pool) error) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "SET LOCAL app.tenant_id = $1", tenantID); err != nil {
		return fmt.Errorf("set tenant context: %w", err)
	}

	if err := fn(c.pool); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
