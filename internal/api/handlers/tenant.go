package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/soloz-io/zero-ops/internal/db"
	"github.com/soloz-io/zero-ops/internal/service"
)

type TenantHandler struct {
	service *service.TenantService
	queries db.Querier
}

func NewTenantHandler(svc *service.TenantService, queries db.Querier) *TenantHandler {
	return &TenantHandler{service: svc, queries: queries}
}

type CreateTenantRequest struct {
	Name     string                 `json:"name" binding:"required,rfc1123"`
	Email    string                 `json:"email" binding:"required,email"`
	Plan     string                 `json:"plan" binding:"required,oneof=free professional enterprise"`
	Quotas   *service.QuotaRequest  `json:"quotas"`
	Metadata map[string]string      `json:"metadata"`
}

type TenantResponse struct {
	ID        string                 `json:"id"`
	OrgID     *string                `json:"org_id,omitempty"`
	Name      string                 `json:"name"`
	Email     string                 `json:"email"`
	Plan      string                 `json:"plan"`
	Status    string                 `json:"status"`
	Quotas    map[string]interface{} `json:"quotas"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt string                 `json:"created_at"`
	UpdatedAt string                 `json:"updated_at"`
	DeletedAt *string                `json:"deleted_at,omitempty"`
	Created   *bool                  `json:"created,omitempty"`
}

func toTenantResponse(row *db.UpsertTenantRow) TenantResponse {
	var quotas map[string]interface{}
	json.Unmarshal(row.Quotas, &quotas)

	var metadata map[string]interface{}
	if len(row.Metadata) > 0 {
		json.Unmarshal(row.Metadata, &metadata)
	}

	id, _ := uuid.FromBytes(row.ID.Bytes[:])

	resp := TenantResponse{
		ID:        id.String(),
		Name:      row.Name,
		Email:     row.Email,
		Plan:      row.Plan,
		Status:    row.Status,
		Quotas:    quotas,
		Metadata:  metadata,
		CreatedAt: row.CreatedAt.Time.Format(time.RFC3339),
		UpdatedAt: row.UpdatedAt.Time.Format(time.RFC3339),
		Created:   &row.Created,
	}

	if row.OrgID.Valid {
		orgID, _ := uuid.FromBytes(row.OrgID.Bytes[:])
		orgIDStr := orgID.String()
		resp.OrgID = &orgIDStr
	}

	if row.DeletedAt.Valid {
		deletedAt := row.DeletedAt.Time.Format(time.RFC3339)
		resp.DeletedAt = &deletedAt
	}

	return resp
}

func (h *TenantHandler) Create(c *gin.Context) {
	var req CreateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(service.NewValidationError("name", err.Error()))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 500*time.Millisecond)
	defer cancel()

	tenant, created, err := h.service.CreateTenant(ctx, req.Name, req.Email, req.Plan, req.Quotas, req.Metadata)
	if err != nil {
		c.Error(err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	c.JSON(status, toTenantResponse(tenant))
}

func (h *TenantHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(service.NewValidationError("id", "Invalid UUID format"))
		return
	}

	pgID := pgtype.UUID{}
	_ = pgID.Scan(id.String())

	ctx, cancel := context.WithTimeout(c.Request.Context(), 200*time.Millisecond)
	defer cancel()

	tenant, err := h.queries.GetTenant(ctx, pgID)
	if err != nil {
		c.Error(service.NewNotFoundError("tenant", id.String()))
		return
	}

	c.JSON(http.StatusOK, toGetTenantResponse(&tenant))
}

func toGetTenantResponse(row *db.Tenant) TenantResponse {
	var quotas map[string]interface{}
	json.Unmarshal(row.Quotas, &quotas)

	var metadata map[string]interface{}
	if len(row.Metadata) > 0 {
		json.Unmarshal(row.Metadata, &metadata)
	}

	id, _ := uuid.FromBytes(row.ID.Bytes[:])

	resp := TenantResponse{
		ID:        id.String(),
		Name:      row.Name,
		Email:     row.Email,
		Plan:      row.Plan,
		Status:    row.Status,
		Quotas:    quotas,
		Metadata:  metadata,
		CreatedAt: row.CreatedAt.Time.Format(time.RFC3339),
		UpdatedAt: row.UpdatedAt.Time.Format(time.RFC3339),
	}

	if row.OrgID.Valid {
		orgID, _ := uuid.FromBytes(row.OrgID.Bytes[:])
		orgIDStr := orgID.String()
		resp.OrgID = &orgIDStr
	}

	if row.DeletedAt.Valid {
		deletedAt := row.DeletedAt.Time.Format(time.RFC3339)
		resp.DeletedAt = &deletedAt
	}

	return resp
}

func (h *TenantHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit > 100 {
		limit = 100
	}

	offset := (page - 1) * limit

	ctx, cancel := context.WithTimeout(c.Request.Context(), 200*time.Millisecond)
	defer cancel()

	statusFilter := c.Query("status")
	planFilter := c.Query("plan")

	params := db.ListTenantsParams{
		Column1: statusFilter,
		Column2: planFilter,
		Limit:   int32(limit),
		Offset:  int32(offset),
	}

	tenants, err := h.queries.ListTenants(ctx, params)
	if err != nil {
		c.Error(service.NewInternalError("Failed to list tenants"))
		return
	}

	countParams := db.CountTenantsParams{
		Column1: statusFilter,
		Column2: planFilter,
	}
	total, err := h.queries.CountTenants(ctx, countParams)
	if err != nil {
		c.Error(service.NewInternalError("Failed to count tenants"))
		return
	}

	var nextPage *int
	if len(tenants) == limit && page*limit < int(total) {
		next := page + 1
		nextPage = &next
	}

	tenantResponses := make([]TenantResponse, len(tenants))
	for i, t := range tenants {
		tenantResponses[i] = toGetTenantResponse(&t)
	}

	c.JSON(http.StatusOK, gin.H{
		"tenants": tenantResponses,
		"pagination": gin.H{
			"page":       page,
			"limit":      limit,
			"total":      total,
			"totalPages": (int(total) + limit - 1) / limit,
			"nextPage":   nextPage,
		},
	})
}

type UpdateTenantRequest struct {
	Plan   *string              `json:"plan,omitempty" binding:"omitempty,oneof=free professional enterprise"`
	Status *string              `json:"status,omitempty" binding:"omitempty,oneof=active suspended deleted"`
	Quotas *service.QuotaRequest `json:"quotas,omitempty"`
}

func (h *TenantHandler) Update(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(service.NewValidationError("id", "Invalid UUID format"))
		return
	}

	var req UpdateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(service.NewValidationError("", err.Error()))
		return
	}

	pgID := pgtype.UUID{}
	_ = pgID.Scan(id.String())

	ctx, cancel := context.WithTimeout(c.Request.Context(), 200*time.Millisecond)
	defer cancel()

	tenant, err := h.service.UpdateTenant(ctx, pgID, req.Plan, req.Status, req.Quotas)
	if err != nil {
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, toGetTenantResponse(tenant))
}

func (h *TenantHandler) Delete(c *gin.Context) {
	if c.Query("confirm") != "true" {
		c.Error(service.NewConfirmationRequiredError("Must provide 'confirm=true' query parameter to delete tenant"))
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.Error(service.NewValidationError("id", "Invalid UUID format"))
		return
	}

	pgID := pgtype.UUID{}
	_ = pgID.Scan(id.String())

	ctx, cancel := context.WithTimeout(c.Request.Context(), 200*time.Millisecond)
	defer cancel()

	err = h.queries.SoftDeleteTenant(ctx, pgID)
	if err != nil {
		c.Error(service.NewNotFoundError("tenant", id.String()))
		return
	}

	c.Status(http.StatusNoContent)
}
