package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
)

// CatalogHandler handles read-only catalog endpoints
type CatalogHandler struct {
	metering interfaces.IMetering
}

// NewCatalogHandler creates a new catalog handler
func NewCatalogHandler(metering interfaces.IMetering) *CatalogHandler {
	return &CatalogHandler{
		metering: metering,
	}
}

// ListMeters handles GET /api/v1/meters
func (h *CatalogHandler) ListMeters(c *gin.Context) {
	namespace, _ := c.Get("namespace")

	meters, err := h.metering.ListMeters(c.Request.Context(), namespace.(string))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"type":        "https://kube-sbt.io/problems/service-unavailable",
			"title":       "Service Unavailable",
			"status":      http.StatusServiceUnavailable,
			"detail":      "Catalog temporarily unavailable",
			"retry-after": "60",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"meters": meters,
	})
}

// ListFeatures handles GET /api/v1/features
func (h *CatalogHandler) ListFeatures(c *gin.Context) {
	namespace, _ := c.Get("namespace")

	features, err := h.metering.ListFeatures(c.Request.Context(), namespace.(string))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"type":        "https://kube-sbt.io/problems/service-unavailable",
			"title":       "Service Unavailable",
			"status":      http.StatusServiceUnavailable,
			"detail":      "Catalog temporarily unavailable",
			"retry-after": "60",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"features": features,
	})
}

// ListPlans handles GET /api/v1/plans
func (h *CatalogHandler) ListPlans(c *gin.Context) {
	namespace, _ := c.Get("namespace")

	plans, err := h.metering.ListPlans(c.Request.Context(), namespace.(string))
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"type":        "https://kube-sbt.io/problems/service-unavailable",
			"title":       "Service Unavailable",
			"status":      http.StatusServiceUnavailable,
			"detail":      "Catalog temporarily unavailable",
			"retry-after": "60",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"plans": plans,
	})
}
