package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/models"
)

// InvoiceHandler handles invoice endpoints
type InvoiceHandler struct {
	billing interfaces.IBilling
}

// NewInvoiceHandler creates a new invoice handler
func NewInvoiceHandler(billing interfaces.IBilling) *InvoiceHandler {
	return &InvoiceHandler{
		billing: billing,
	}
}

// PreviewInvoice handles GET /api/v1/tenants/{tenantID}/invoices/preview
func (h *InvoiceHandler) PreviewInvoice(c *gin.Context) {
	tenantID := c.Param("tenantID")
	namespace, _ := c.Get("namespace")

	subjectID := c.Query("subject_id")
	if subjectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"type":   "https://kube-sbt.io/problems/bad-request",
			"title":  "Bad Request",
			"status": http.StatusBadRequest,
			"detail": "subject_id is required",
		})
		return
	}

	// Convert userID to full subject format if needed
	if len(subjectID) == 36 { // UUID length
		subjectID = models.GenerateSubjectID(tenantID, subjectID)
	}

	invoice, err := h.billing.PreviewInvoice(c.Request.Context(), namespace.(string), subjectID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"type":        "https://kube-sbt.io/problems/service-unavailable",
			"title":       "Service Unavailable",
			"status":      http.StatusServiceUnavailable,
			"detail":      "Invoice preview temporarily unavailable",
			"retry-after": "60",
		})
		return
	}

	c.JSON(http.StatusOK, invoice)
}

// GetInvoice handles GET /api/v1/tenants/{tenantID}/invoices/{invoiceID}
func (h *InvoiceHandler) GetInvoice(c *gin.Context) {
	invoiceID := c.Param("invoiceID")
	namespace, _ := c.Get("namespace")

	invoice, err := h.billing.GetInvoice(c.Request.Context(), namespace.(string), invoiceID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"type":   "https://kube-sbt.io/problems/not-found",
			"title":  "Not Found",
			"status": http.StatusNotFound,
			"detail": "Invoice not found",
		})
		return
	}

	c.JSON(http.StatusOK, invoice)
}

// ListInvoices handles GET /api/v1/tenants/{tenantID}/invoices
func (h *InvoiceHandler) ListInvoices(c *gin.Context) {
	namespace, _ := c.Get("namespace")

	filters := models.InvoiceFilters{}
	
	if status := c.Query("status"); status != "" {
		filters.Status = &status
	}
	
	if subjectID := c.Query("subject_id"); subjectID != "" {
		filters.SubjectID = &subjectID
	}

	invoices, err := h.billing.ListInvoices(c.Request.Context(), namespace.(string), filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"type":   "https://kube-sbt.io/problems/internal-error",
			"title":  "Internal Server Error",
			"status": http.StatusInternalServerError,
			"detail": "Failed to list invoices",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"invoices": invoices,
	})
}
