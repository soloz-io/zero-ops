package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/models"
)

// SubscriptionHandler handles subscription endpoints
type SubscriptionHandler struct {
	billing interfaces.IBilling
}

// NewSubscriptionHandler creates a new subscription handler
func NewSubscriptionHandler(billing interfaces.IBilling) *SubscriptionHandler {
	return &SubscriptionHandler{
		billing: billing,
	}
}

// CreateSubscription handles POST /api/v1/tenants/{tenantID}/subscriptions
func (h *SubscriptionHandler) CreateSubscription(c *gin.Context) {
	tenantID := c.Param("tenantID")
	namespace, _ := c.Get("namespace")

	var req struct {
		SubjectID string                 `json:"subject_id" binding:"required"`
		PlanID    string                 `json:"plan_id" binding:"required"`
		Metadata  map[string]string      `json:"metadata"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type":   "https://kube-sbt.io/problems/bad-request",
			"title":  "Bad Request",
			"status": http.StatusBadRequest,
			"detail": err.Error(),
		})
		return
	}

	// Convert userID to full subject format if needed
	subjectID := req.SubjectID
	if len(subjectID) == 36 { // UUID length
		subjectID = models.GenerateSubjectID(tenantID, subjectID)
	}

	opts := models.SubscriptionOptions{
		Metadata: req.Metadata,
	}

	err := h.billing.CreateSubscription(c.Request.Context(), namespace.(string), subjectID, req.PlanID, opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"type":   "https://kube-sbt.io/problems/internal-error",
			"title":  "Internal Server Error",
			"status": http.StatusInternalServerError,
			"detail": "Failed to create subscription",
		})
		return
	}

	c.Status(http.StatusCreated)
}

// GetSubscription handles GET /api/v1/tenants/{tenantID}/subscriptions/{subscriptionID}
func (h *SubscriptionHandler) GetSubscription(c *gin.Context) {
	subscriptionID := c.Param("subscriptionID")
	namespace, _ := c.Get("namespace")

	subscription, err := h.billing.GetSubscription(c.Request.Context(), namespace.(string), subscriptionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"type":   "https://kube-sbt.io/problems/not-found",
			"title":  "Not Found",
			"status": http.StatusNotFound,
			"detail": "Subscription not found",
		})
		return
	}

	c.JSON(http.StatusOK, subscription)
}

// UpdateSubscription handles PUT /api/v1/tenants/{tenantID}/subscriptions/{subscriptionID}
func (h *SubscriptionHandler) UpdateSubscription(c *gin.Context) {
	subscriptionID := c.Param("subscriptionID")
	namespace, _ := c.Get("namespace")

	var req struct {
		PlanID   *string           `json:"plan_id"`
		Status   *string           `json:"status"`
		Metadata map[string]string `json:"metadata"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"type":   "https://kube-sbt.io/problems/bad-request",
			"title":  "Bad Request",
			"status": http.StatusBadRequest,
			"detail": err.Error(),
		})
		return
	}

	updates := models.SubscriptionUpdates{
		PlanID:   req.PlanID,
		Status:   req.Status,
		Metadata: req.Metadata,
	}

	err := h.billing.UpdateSubscription(c.Request.Context(), namespace.(string), subscriptionID, updates)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"type":   "https://kube-sbt.io/problems/internal-error",
			"title":  "Internal Server Error",
			"status": http.StatusInternalServerError,
			"detail": "Failed to update subscription",
		})
		return
	}

	c.Status(http.StatusOK)
}

// CancelSubscription handles DELETE /api/v1/tenants/{tenantID}/subscriptions/{subscriptionID}
func (h *SubscriptionHandler) CancelSubscription(c *gin.Context) {
	subscriptionID := c.Param("subscriptionID")
	namespace, _ := c.Get("namespace")

	err := h.billing.CancelSubscription(c.Request.Context(), namespace.(string), subscriptionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"type":   "https://kube-sbt.io/problems/internal-error",
			"title":  "Internal Server Error",
			"status": http.StatusInternalServerError,
			"detail": "Failed to cancel subscription",
		})
		return
	}

	c.Status(http.StatusNoContent)
}

// ListSubscriptions handles GET /api/v1/tenants/{tenantID}/subscriptions
func (h *SubscriptionHandler) ListSubscriptions(c *gin.Context) {
	namespace, _ := c.Get("namespace")

	filters := models.SubscriptionFilters{}
	
	if status := c.Query("status"); status != "" {
		filters.Status = &status
	}

	subscriptions, err := h.billing.ListSubscriptions(c.Request.Context(), namespace.(string), filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"type":   "https://kube-sbt.io/problems/internal-error",
			"title":  "Internal Server Error",
			"status": http.StatusInternalServerError,
			"detail": "Failed to list subscriptions",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"subscriptions": subscriptions,
	})
}
