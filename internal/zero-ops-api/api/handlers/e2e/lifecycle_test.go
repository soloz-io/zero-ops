package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestCompleteTenantLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	cfg := &config.Config{
		ServerPort:  "8080",
		DatabaseURL: "",
		LogLevel:    "error",
		Environment: "test",
	}
	logger := zap.NewNop()
	srv := api.NewServer(cfg, pool, logger)
	router := srv.Router()

	// Step 1: Create tenant
	createReq := map[string]interface{}{
		"name":  "acme-corp",
		"email": "admin@acme.com",
		"plan":  "free",
	}
	body, _ := json.Marshal(createReq)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var createResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))
	assert.True(t, createResp["created"].(bool))
	quotas := createResp["quotas"].(map[string]interface{})
	assert.Equal(t, float64(1), quotas["maxClusters"])
	tenantID := createResp["id"].(string)

	// DB Assertion: Verify tenant in database
	assertTenantInDB(t, pool, tenantID, func(row map[string]interface{}) {
		assert.Equal(t, "acme-corp", row["name"])
		assert.Equal(t, "active", row["status"])
		assert.Nil(t, row["deleted_at"])
		// Verify created_at = updated_at (new record)
		createdAt := row["created_at"].(time.Time)
		updatedAt := row["updated_at"].(time.Time)
		assert.WithinDuration(t, createdAt, updatedAt, time.Second)
	})

	// Step 2: Retrieve tenant
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/tenants/%s", tenantID), nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var getResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &getResp))
	assert.Equal(t, "active", getResp["status"])

	// Step 3: Upgrade plan
	updateReq := map[string]interface{}{"plan": "professional"}
	body, _ = json.Marshal(updateReq)
	req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/tenants/%s", tenantID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var updateResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &updateResp))
	assert.Equal(t, "professional", updateResp["plan"])
	assert.Equal(t, float64(10), updateResp["quotas"].(map[string]interface{})["maxClusters"])

	// DB Assertion: Verify plan update and quota recalculation
	assertTenantInDB(t, pool, tenantID, func(row map[string]interface{}) {
		assert.Equal(t, "professional", row["plan"])
		assert.Equal(t, float64(10), row["quotas"].(map[string]interface{})["maxClusters"])
		// Verify updated_at > created_at
		createdAt := row["created_at"].(time.Time)
		updatedAt := row["updated_at"].(time.Time)
		assert.True(t, updatedAt.After(createdAt), "updated_at should be after created_at")
	})

	// Step 4: Safe deletion
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/tenants/%s?confirm=true", tenantID), nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	// DB Assertion: Verify soft delete in database
	assertTenantInDB(t, pool, tenantID, func(row map[string]interface{}) {
		assert.Equal(t, "deleted", row["status"])
		assert.NotNil(t, row["deleted_at"], "deleted_at should be set")
	})

	// Step 5: Verify soft delete
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/tenants/%s", tenantID), nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	// DB Assertion: Verify record still exists but is soft-deleted
	assertTenantNotInDB(t, pool, tenantID)
}
