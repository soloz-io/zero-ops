package e2e

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertTenantInDB queries the database directly and validates tenant fields
func assertTenantInDB(t *testing.T, pool *pgxpool.Pool, tenantID string, checks func(row map[string]interface{})) {
	t.Helper()
	
	query := `SELECT id, name, email, plan, status, quotas, metadata, created_at, updated_at, deleted_at 
	          FROM tenants WHERE id = $1`
	
	var id, name, email, plan, status string
	var quotas, metadata []byte
	var createdAt, updatedAt time.Time
	var deletedAt *time.Time
	
	err := pool.QueryRow(context.Background(), query, tenantID).Scan(
		&id, &name, &email, &plan, &status, &quotas, &metadata, &createdAt, &updatedAt, &deletedAt,
	)
	require.NoError(t, err, "Tenant should exist in database")
	
	var quotasMap map[string]interface{}
	json.Unmarshal(quotas, &quotasMap)
	
	var metadataMap map[string]interface{}
	if len(metadata) > 0 {
		json.Unmarshal(metadata, &metadataMap)
	}
	
	row := map[string]interface{}{
		"id":         id,
		"name":       name,
		"email":      email,
		"plan":       plan,
		"status":     status,
		"quotas":     quotasMap,
		"metadata":   metadataMap,
		"created_at": createdAt,
		"updated_at": updatedAt,
		"deleted_at": deletedAt,
	}
	
	checks(row)
}

// assertTenantNotInDB verifies tenant doesn't exist or is soft-deleted
func assertTenantNotInDB(t *testing.T, pool *pgxpool.Pool, tenantID string) {
	t.Helper()
	
	query := `SELECT deleted_at FROM tenants WHERE id = $1`
	var deletedAt *time.Time
	
	err := pool.QueryRow(context.Background(), query, tenantID).Scan(&deletedAt)
	if err != nil {
		// Tenant doesn't exist at all - acceptable
		return
	}
	
	// Tenant exists - must be soft-deleted
	assert.NotNil(t, deletedAt, "Tenant should be soft-deleted")
}

// assertTenantCount counts tenants matching filters
func assertTenantCount(t *testing.T, pool *pgxpool.Pool, statusFilter, planFilter string, expectedCount int) {
	t.Helper()
	
	query := `SELECT COUNT(*) FROM tenants 
	          WHERE ($1 = '' OR status = $1) 
	          AND ($2 = '' OR plan = $2) 
	          AND deleted_at IS NULL`
	
	var count int
	err := pool.QueryRow(context.Background(), query, statusFilter, planFilter).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, expectedCount, count, "Database count should match expected")
}

// assertTimestampSet verifies a timestamp field is non-null
func assertTimestampSet(t *testing.T, pool *pgxpool.Pool, tenantID, field string) {
	t.Helper()
	
	query := `SELECT ` + field + ` FROM tenants WHERE id = $1`
	var ts *time.Time
	
	err := pool.QueryRow(context.Background(), query, tenantID).Scan(&ts)
	require.NoError(t, err)
	assert.NotNil(t, ts, field+" should be set")
}

// parseUUID is a helper to convert string to UUID for queries
func parseUUID(t *testing.T, idStr string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(idStr)
	require.NoError(t, err)
	return id
}
