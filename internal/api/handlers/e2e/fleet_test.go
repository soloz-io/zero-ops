package e2e

import (
	"bytes"
	"encoding/json"
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

func TestFleetManagement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	cfg := &config.Config{Environment: "test"}
	srv := api.NewServer(cfg, pool, zap.NewNop())
	router := srv.Router()

	// Seed 3 tenants
	tenants := []struct {
		name   string
		email  string
		plan   string
		status string
	}{
		{"tenant-a", "a@test.com", "free", "active"},
		{"tenant-b", "b@test.com", "professional", "active"},
		{"tenant-c", "c@test.com", "professional", "suspended"},
	}

	for _, tenant := range tenants {
		createReq := map[string]interface{}{
			"name":  tenant.name,
			"email": tenant.email,
			"plan":  tenant.plan,
		}
		body, _ := json.Marshal(createReq)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tenants", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusCreated, w.Code)

		if tenant.status == "suspended" {
			var resp map[string]interface{}
			json.Unmarshal(w.Body.Bytes(), &resp)
			tenantID := resp["id"].(string)

			updateReq := map[string]interface{}{"status": "suspended"}
			body, _ = json.Marshal(updateReq)
			req = httptest.NewRequest(http.MethodPatch, "/api/v1/tenants/"+tenantID, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w = httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)
		}
	}

	// DB Assertion: Verify 3 tenants exist with correct attributes
	assertTenantCount(t, pool, "", "", 3)
	assertTenantCount(t, pool, "active", "", 2)
	assertTenantCount(t, pool, "suspended", "", 1)
	assertTenantCount(t, pool, "", "professional", 2)
	assertTenantCount(t, pool, "", "free", 1)

	// Step 1: Default list
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tenants", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var listResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listResp))
	assert.Len(t, listResp["tenants"], 3)
	assert.Equal(t, float64(3), listResp["pagination"].(map[string]interface{})["total"])

	// Step 2: Filter by plan
	req = httptest.NewRequest(http.MethodGet, "/api/v1/tenants?plan=professional", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listResp))
	assert.Len(t, listResp["tenants"], 2)

	// DB Assertion: Verify filter matches database query
	assertTenantCount(t, pool, "", "professional", 2)

	// Step 3: Filter by status
	req = httptest.NewRequest(http.MethodGet, "/api/v1/tenants?status=active", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listResp))
	assert.Len(t, listResp["tenants"], 2)

	// DB Assertion: Verify filter matches database query
	assertTenantCount(t, pool, "active", "", 2)

	// Step 4: Pagination boundaries
	req = httptest.NewRequest(http.MethodGet, "/api/v1/tenants?limit=2&page=1", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listResp))
	assert.Len(t, listResp["tenants"], 2)
	pagination := listResp["pagination"].(map[string]interface{})
	assert.NotNil(t, pagination["nextPage"])

	// Step 5: Follow pagination
	req = httptest.NewRequest(http.MethodGet, "/api/v1/tenants?limit=2&page=2", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listResp))
	assert.Len(t, listResp["tenants"], 1)
	pagination = listResp["pagination"].(map[string]interface{})
	assert.Nil(t, pagination["nextPage"])
}
