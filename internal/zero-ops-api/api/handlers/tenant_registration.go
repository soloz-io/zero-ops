package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/soloz-io/zero-ops/internal/zero-ops-api/service"
)

type TenantRegistrationHandler struct {
	service *service.TenantRegistrationService
}

func NewTenantRegistrationHandler(svc *service.TenantRegistrationService) *TenantRegistrationHandler {
	return &TenantRegistrationHandler{service: svc}
}

type CreateTenantRegistrationRequest struct {
	Name     string                 `json:"name" binding:"required,rfc1123"`
	Email    string                 `json:"email" binding:"required,email"`
	Plan     string                 `json:"plan" binding:"required,oneof=free professional enterprise"`
	Quotas   *service.QuotaRequest  `json:"quotas"`
	Metadata map[string]string      `json:"metadata"`
}

type TenantRegistrationResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Plan      string `json:"plan"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

func (h *TenantRegistrationHandler) Create(c *gin.Context) {
	var req CreateTenantRegistrationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(service.NewValidationError("request", err.Error()))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 500*time.Millisecond)
	defer cancel()

	registration, err := h.service.CreateRegistration(ctx, req.Name, req.Email, req.Plan, req.Quotas, req.Metadata)
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusCreated, TenantRegistrationResponse{
		ID:        registration.ID,
		Name:      registration.Name,
		Email:     registration.Email,
		Plan:      registration.Plan,
		Status:    registration.Status,
		CreatedAt: registration.CreatedAt.Format(time.RFC3339),
	})
}
