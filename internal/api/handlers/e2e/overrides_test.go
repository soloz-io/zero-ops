package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/soloz-io/zero-ops/internal/api"
	"github.com/soloz-io/zero-ops/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAdminOverrides(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	cfg := &config.Config{Environment: "test"}
	srv := api.NewServer(cfg, pool, zap.NewNop())
	router := srv.Router()

	// Step 1: Create with custom quotas
	maxClusters := 999
	createReq := map[string]interface{}{
		"name":  "custom-tenant",
		"email": "admin@custom.com",
		"plan":  "professional",
		"quotas": map[string]interface{}{
			"maxClusters": maxClusters,
		},
	}
	body, _ := json.Marshal(createReq)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenants", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var createResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))
	quotas := createResp["quotas"].(map[string]interface{})
	assert.Equal(t, float64(999), quotas["maxClusters"])
	assert.Equal(t, float64(50), quotas["maxNodes"])
	tenantID := createResp["id"].(string)

	// DB Assertion: Verify custom quotas in database
	assertTenantInDB(t, pool, tenantID, func(row map[string]interface{}) {
		dbQuotas := row["quotas"].(map[string]interface{})
		assert.Equal(t, float64(999), dbQuotas["maxClusters"], "Custom quota should be in DB")
		assert.Equal(t, float64(50), dbQuotas["maxNodes"], "Default quota should be in DB")
	})

	// Step 2: Unconfirmed deletion attempt
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/tenants/%s", tenantID), nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	errorObj := errResp["error"].(map[string]interface{})
	assert.Equal(t, "CONFIRMATION_REQUIRED", errorObj["code"])

	// Step 3: Invalid status transition
	req = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/tenants/%s?confirm=true", tenantID), nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)

	updateReq := map[string]interface{}{"status": "active"}
	body, _ = json.Marshal(updateReq)
	req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/tenants/%s", tenantID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	// DB Assertion: Verify tenant is deleted in database
	assertTenantInDB(t, pool, tenantID, func(row map[string]interface{}) {
		assert.Equal(t, "deleted", row["status"])
		assert.NotNil(t, row["deleted_at"])
	})
}
