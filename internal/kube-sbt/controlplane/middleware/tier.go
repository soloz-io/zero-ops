// Package middleware provides Gin middleware for tier-based quota and feature enforcement.
package middleware

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/models"
)

// tierOrder maps tier names to a numeric rank for downgrade detection (18.9).
var tierOrder = map[string]int{
	"basic": 1, "standard": 2, "premium": 3, "enterprise": 4,
}

// TierQuotaMiddleware enforces tier quotas before the handler runs (18.1).
// It reads the current usage from the request context key "resource_usage"
// (set by the service layer) and validates against the tenant's tier quotas.
//
// Quota checks performed (18.3–18.5):
//   - POST /users          → users quota
//   - POST /files or /storage → storage_gb quota
//   - all requests          → api_requests quota (when usage is provided)
func TierQuotaMiddleware(tm interfaces.ITierManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		tier := c.GetString("tenant_tier")
		if tier == "" {
			c.Next()
			return
		}

		usage := usageFromContext(c)

		// 18.3 — user creation quota
		if c.Request.Method == http.MethodPost && c.FullPath() == "/users" {
			if err := tm.ValidateTierQuota(c.Request.Context(), tier, models.ResourceUsage{
				Users: usage.Users + 1, // +1 for the one being created
			}); err != nil {
				quotaError(c, "user", err)
				return
			}
		}

		// 18.4 — storage quota
		if c.Request.Method == http.MethodPost &&
			(matchesPrefix(c.FullPath(), "/files") || matchesPrefix(c.FullPath(), "/storage")) {
			if err := tm.ValidateTierQuota(c.Request.Context(), tier, models.ResourceUsage{
				StorageGB: usage.StorageGB,
			}); err != nil {
				quotaError(c, "storage", err)
				return
			}
		}

		// 18.5 — API request quota (checked on every request when usage is available)
		if usage.APIRequests > 0 {
			if err := tm.ValidateTierQuota(c.Request.Context(), tier, models.ResourceUsage{
				APIRequests: usage.APIRequests + 1,
			}); err != nil {
				quotaError(c, "api_requests", err)
				return
			}
		}

		c.Next()
	}
}

// TierFeatureMiddleware gates a route behind a required feature flag (18.2).
// Usage: r.POST("/webhooks", TierFeatureMiddleware(tm, "webhooks"), handler)
//
// Features checked by callers (18.6–18.8):
//   - "sso"           → SSO configuration endpoints
//   - "webhooks"      → webhook management endpoints
//   - "custom_domain" → custom domain endpoints
func TierFeatureMiddleware(tm interfaces.ITierManager, requiredFeature string) gin.HandlerFunc {
	return func(c *gin.Context) {
		tier := c.GetString("tenant_tier")
		if tier == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "tenant tier not set"})
			return
		}
		enabled, err := tm.IsTierFeatureEnabled(c.Request.Context(), tier, requiredFeature)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "feature check failed"})
			return
		}
		if !enabled {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error":            fmt.Sprintf("feature %q not available in tier %q", requiredFeature, tier),
				"tier":             tier,
				"required_feature": requiredFeature,
			})
			return
		}
		c.Next()
	}
}

// ValidateTierDowngrade returns an error if newTier is a downgrade and the
// current usage exceeds the new tier's quotas (18.9).
func ValidateTierDowngrade(ctx *gin.Context, tm interfaces.ITierManager, oldTier, newTier string, usage models.ResourceUsage) error {
	if tierOrder[newTier] >= tierOrder[oldTier] {
		return nil // upgrade or same tier — always allowed
	}
	return tm.ValidateTierQuota(ctx.Request.Context(), newTier, usage)
}

// RollbackTierChange reverts a tenant's tier to oldTier in storage (18.10).
// Called by the service layer when the Application Plane reports a tier-change failure.
func RollbackTierChange(ctx *gin.Context, storage interfaces.IStorage, tenantID, oldTier string) error {
	return storage.UpdateTenant(ctx.Request.Context(), tenantID, models.TenantUpdates{
		Tier: &oldTier,
	})
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func quotaError(c *gin.Context, resource string, err error) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"error":    err.Error(),
		"resource": resource,
	})
}

// usageFromContext reads ResourceUsage stored by the service layer under key "resource_usage".
func usageFromContext(c *gin.Context) models.ResourceUsage {
	if v, ok := c.Get("resource_usage"); ok {
		if u, ok := v.(models.ResourceUsage); ok {
			return u
		}
	}
	return models.ResourceUsage{}
}

func matchesPrefix(path, prefix string) bool {
	return len(path) >= len(prefix) && path[:len(prefix)] == prefix
}
