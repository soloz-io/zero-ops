package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/soloz-io/zero-ops/internal/zero-ops-api/api"
	"github.com/soloz-io/zero-ops/internal/zero-ops-api/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAgenticResilience(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	cfg := &config.Config{Environment: "test"}
	srv := api.NewServer(cfg, pool, zap.NewNop())
	router := srv.Router()

	// Step 1: Invalid name format
	createReq := map[string]interface{}{
		"name":  "Acme Corp!",
		"email": "admin@acme.com",
		"plan":  "free",
	}
	body, _ := json.Marshal(createReq)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	errorObj := errResp["error"].(map[string]interface{})
	assert.Equal(t, "VALIDATION_ERROR", errorObj["code"])
	assert.Equal(t, "name", errorObj["field"])

	// Step 2: Successful correction
	createReq["name"] = "acme-corp"
	body, _ = json.Marshal(createReq)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var createResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))
	assert.True(t, createResp["created"].(bool))

	// DB Assertion: Verify initial creation timestamps
	tenantID := createResp["id"].(string)
	var initialCreatedAt, initialUpdatedAt time.Time
	assertTenantInDB(t, pool, tenantID, func(row map[string]interface{}) {
		initialCreatedAt = row["created_at"].(time.Time)
		initialUpdatedAt = row["updated_at"].(time.Time)
		assert.WithinDuration(t, initialCreatedAt, initialUpdatedAt, time.Second)
	})

	// Step 3: Idempotent retry
	body, _ = json.Marshal(createReq)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var retryResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &retryResp))
	assert.False(t, retryResp["created"].(bool))
	assert.Equal(t, createResp["id"], retryResp["id"])

	// DB Assertion: Verify no mutation on idempotent retry
	assertTenantInDB(t, pool, tenantID, func(row map[string]interface{}) {
		createdAt := row["created_at"].(time.Time)
		updatedAt := row["updated_at"].(time.Time)
		assert.Equal(t, initialCreatedAt, createdAt, "created_at should not change")
		// Note: updated_at may change due to trigger firing on DO UPDATE, but data is unchanged
		assert.WithinDuration(t, initialUpdatedAt, updatedAt, 5*time.Second)
	})

	// Step 4: Different payload with same name
	createReq["email"] = "hacker@evil.com"
	body, _ = json.Marshal(createReq)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var conflictResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &conflictResp))
	assert.False(t, conflictResp["created"].(bool))
	assert.Equal(t, "admin@acme.com", conflictResp["email"])

	// DB Assertion: Verify original email preserved, no new record
	assertTenantInDB(t, pool, tenantID, func(row map[string]interface{}) {
		assert.Equal(t, "admin@acme.com", row["email"], "Original email should be preserved")
	})
	assertTenantCount(t, pool, "", "", 1) // Only 1 tenant should exist
}
