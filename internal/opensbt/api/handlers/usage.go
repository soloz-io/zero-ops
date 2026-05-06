package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// UsageHandler handles usage query endpoints
type UsageHandler struct {
	metering interfaces.IMetering
}

// NewUsageHandler creates a new usage handler
func NewUsageHandler(metering interfaces.IMetering) *UsageHandler {
	return &UsageHandler{
		metering: metering,
	}
}

// GetTenantUsage handles GET /api/v1/tenants/{tenantID}/usage
func (h *UsageHandler) GetTenantUsage(c *gin.Context) {
	namespace, _ := c.Get("namespace")

	// Parse time period parameters
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	period := c.DefaultQuery("period", "monthly")

	timePeriod, err := parseTimePeriod(startDate, endDate, period)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type":   "https://kube-sbt.io/problems/bad-request",
			"title":  "Bad Request",
			"status": http.StatusBadRequest,
			"detail": err.Error(),
		})
		return
	}

	usage, err := h.metering.GetTenantUsage(c.Request.Context(), namespace.(string), timePeriod)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"type":        "https://kube-sbt.io/problems/service-unavailable",
			"title":       "Service Unavailable",
			"status":      http.StatusServiceUnavailable,
			"detail":      "Usage data temporarily unavailable",
			"retry-after": "60",
		})
		return
	}

	c.JSON(http.StatusOK, usage)
}

// GetUserUsage handles GET /api/v1/tenants/{tenantID}/users/{userID}/usage
func (h *UsageHandler) GetUserUsage(c *gin.Context) {
	tenantID := c.Param("tenantID")
	userID := c.Param("userID")
	namespace, _ := c.Get("namespace")

	// Parse time period parameters
	startDate := c.Query("start_date")
	endDate := c.Query("end_date")
	period := c.DefaultQuery("period", "monthly")

	timePeriod, err := parseTimePeriod(startDate, endDate, period)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type":   "https://kube-sbt.io/problems/bad-request",
			"title":  "Bad Request",
			"status": http.StatusBadRequest,
			"detail": err.Error(),
		})
		return
	}

	subjectID := models.GenerateSubjectID(tenantID, userID)
	usage, err := h.metering.GetUserUsage(c.Request.Context(), namespace.(string), subjectID, timePeriod)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"type":        "https://kube-sbt.io/problems/service-unavailable",
			"title":       "Service Unavailable",
			"status":      http.StatusServiceUnavailable,
			"detail":      "Usage data temporarily unavailable",
			"retry-after": "60",
		})
		return
	}

	c.JSON(http.StatusOK, usage)
}

// CheckEntitlements handles GET /api/v1/tenants/{tenantID}/entitlements
func (h *UsageHandler) CheckEntitlements(c *gin.Context) {
	tenantID := c.Param("tenantID")
	namespace, _ := c.Get("namespace")

	// Parse query parameters
	subjectID := c.Query("subject_id")
	featureKey := c.Query("feature_key")

	if subjectID == "" || featureKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"type":   "https://kube-sbt.io/problems/bad-request",
			"title":  "Bad Request",
			"status": http.StatusBadRequest,
			"detail": "subject_id and feature_key are required",
		})
		return
	}

	// If subjectID is just userID, convert to full subject format
	if len(subjectID) == 36 { // UUID length
		subjectID = models.GenerateSubjectID(tenantID, subjectID)
	}

	entitlement, err := h.metering.CheckEntitlement(c.Request.Context(), namespace.(string), subjectID, featureKey)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"type":        "https://kube-sbt.io/problems/service-unavailable",
			"title":       "Service Unavailable",
			"status":      http.StatusServiceUnavailable,
			"detail":      "Entitlement check temporarily unavailable",
			"retry-after": "60",
		})
		return
	}

	c.JSON(http.StatusOK, entitlement)
}

// parseTimePeriod parses time period from query parameters
func parseTimePeriod(startDate, endDate, period string) (models.TimePeriod, error) {
	var timePeriod models.TimePeriod

	if startDate != "" && endDate != "" {
		// Custom range
		start, err := time.Parse(time.RFC3339, startDate)
		if err != nil {
			return timePeriod, err
		}
		end, err := time.Parse(time.RFC3339, endDate)
		if err != nil {
			return timePeriod, err
		}
		timePeriod.Start = start
		timePeriod.End = end
	} else {
		// Predefined period
		now := time.Now()
		switch period {
		case "daily":
			timePeriod.Start = now.AddDate(0, 0, -1)
			timePeriod.End = now
		case "weekly":
			timePeriod.Start = now.AddDate(0, 0, -7)
			timePeriod.End = now
		case "monthly":
			timePeriod.Start = now.AddDate(0, -1, 0)
			timePeriod.End = now
		default:
			timePeriod.Start = now.AddDate(0, -1, 0)
			timePeriod.End = now
		}
	}

	return timePeriod, nil
}
