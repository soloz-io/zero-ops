// Package tenantdb wraps database/sql with automatic PostgreSQL RLS context
// setting for tenant isolation (Tasks 23.1–23.6).
package tenantdb

import (
	"context"
	"database/sql"
	"fmt"
)

// TenantDB wraps *sql.DB and sets app.tenant_id before every query (23.1).
type TenantDB struct {
	db *sql.DB
}

// New wraps an existing *sql.DB.
func New(db *sql.DB) *TenantDB {
	return &TenantDB{db: db}
}

// BeginTx starts a transaction and immediately sets app.tenant_id for RLS (23.2).
func (t *TenantDB) BeginTx(ctx context.Context) (*sql.Tx, error) {
	tenantID := tenantIDFromCtx(ctx)
	if tenantID == "" {
		return nil, fmt.Errorf("tenantdb: tenant_id not found in context")
	}
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "SET LOCAL app.tenant_id = $1", tenantID); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("tenantdb: set RLS context: %w", err)
	}
	return tx, nil
}

// QueryContext executes a query inside a tenant-scoped transaction (23.3, 23.5).
func (t *TenantDB) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	tx, err := t.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	// Commit releases the SET LOCAL; rows remain readable until closed.
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return rows, nil
}

// ExecContext executes a statement inside a tenant-scoped transaction (23.4, 23.5).
func (t *TenantDB) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	tx, err := t.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return result, tx.Commit()
}

// DB returns the underlying *sql.DB for use with sqlc-generated Queries (23.6).
// Callers pass TenantDB.DB() to sqlc's New(db) after setting tenant context manually.
func (t *TenantDB) DB() *sql.DB { return t.db }

type ctxKey string

func tenantIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey("tenant_id")).(string)
	return v
}
