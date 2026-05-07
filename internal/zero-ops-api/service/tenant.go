package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/soloz-io/zero-ops/internal/zero-ops-api/db"
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
	"go.uber.org/zap"
)

var rfc1123Regex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type TenantService struct {
	queries  db.Querier
	logger   *zap.Logger
	eventBus interfaces.IEventBus
}

func NewTenantService(queries db.Querier, logger *zap.Logger, eventBus interfaces.IEventBus) *TenantService {
	return &TenantService{
		queries:  queries,
		logger:   logger,
		eventBus: eventBus,
	}
}

type Quotas struct {
	MaxClusters int    `json:"maxClusters"`
	MaxNodes    int    `json:"maxNodes"`
	MaxCPU      string `json:"maxCPU"`
	MaxMemory   string `json:"maxMemory"`
}

type QuotaRequest struct {
	MaxClusters *int    `json:"maxClusters,omitempty"`
	MaxNodes    *int    `json:"maxNodes,omitempty"`
	MaxCPU      *string `json:"maxCPU,omitempty"`
	MaxMemory   *string `json:"maxMemory,omitempty"`
}

var planDefaults = map[string]Quotas{
	"free": {
		MaxClusters: 1,
		MaxNodes:    5,
		MaxCPU:      "10",
		MaxMemory:   "20Gi",
	},
	"professional": {
		MaxClusters: 10,
		MaxNodes:    50,
		MaxCPU:      "200",
		MaxMemory:   "500Gi",
	},
	"enterprise": {
		MaxClusters: 100,
		MaxNodes:    500,
		MaxCPU:      "2000",
		MaxMemory:   "5000Gi",
	},
}

func (s *TenantService) CreateTenant(ctx context.Context, name, email, plan string, quotasOverride *QuotaRequest, metadata map[string]string) (*db.UpsertTenantRow, bool, error) {
	if !rfc1123Regex.MatchString(name) || len(name) > 63 {
		return nil, false, NewValidationError("name", "Name must consist of lower case alphanumeric characters or '-', and must start and end with an alphanumeric character")
	}

	quotas := ResolveQuotas(plan, quotasOverride)
	quotasJSON, _ := json.Marshal(quotas)

	var metadataJSON []byte
	if metadata != nil {
		metadataJSON, _ = json.Marshal(metadata)
	}

	tenantID := pgtype.UUID{}
	_ = tenantID.Scan(uuid.New().String())

	orgID := pgtype.UUID{Valid: false}

	params := db.UpsertTenantParams{
		ID:       tenantID,
		OrgID:    orgID,
		Name:     name,
		Email:    email,
		Plan:     plan,
		Quotas:   quotasJSON,
		Metadata: metadataJSON,
	}

	result, err := s.queries.UpsertTenant(ctx, params)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, false, NewInternalError("Database operation timed out")
		}
		s.logger.Error("failed to upsert tenant", zap.Error(err), zap.String("name", name))
		return nil, false, NewInternalError("Failed to create tenant")
	}

	// Emit opensbt_onboardingRequest event if this is a new tenant
	if result.Created && s.eventBus != nil {
		tenantIDStr, _ := uuid.FromBytes(result.ID.Bytes[:])
		event := models.NewEvent(
			models.EventOnboardingRequest,
			models.ControlPlaneEventSource,
			map[string]interface{}{
				"tenantId": tenantIDStr.String(),
				"name":     name,
				"email":    email,
				"tier":     plan,
				"quotas":   quotas,
			},
		)
		if err := s.eventBus.PublishAsync(ctx, event); err != nil {
			s.logger.Error("failed to publish onboarding event", zap.Error(err), zap.String("tenantId", tenantIDStr.String()))
		}
	}

	return &result, result.Created, nil
}

func (s *TenantService) UpdateTenant(ctx context.Context, id pgtype.UUID, plan *string, status *string, quotas *QuotaRequest) (*db.Tenant, error) {
	tenant, err := s.queries.GetTenant(ctx, id)
	if err != nil {
		return nil, NewNotFoundError("tenant", id.String())
	}

	if status != nil {
		if err := validateStatusTransition(tenant.Status, *status); err != nil {
			return nil, err
		}
	}

	var quotasJSON []byte
	if plan != nil {
		resolvedQuotas := ResolveQuotas(*plan, quotas)
		quotasJSON, _ = json.Marshal(resolvedQuotas)
	} else if quotas != nil {
		var currentQuotas Quotas
		_ = json.Unmarshal(tenant.Quotas, &currentQuotas)
		resolvedQuotas := ResolveQuotas(tenant.Plan, quotas)
		quotasJSON, _ = json.Marshal(resolvedQuotas)
	}

	params := db.UpdateTenantParams{
		ID:       id,
		Plan:     pgtype.Text{String: stringVal(plan), Valid: plan != nil},
		Status:   pgtype.Text{String: stringVal(status), Valid: status != nil},
		Quotas:   quotasJSON,
		Metadata: nil,
	}

	updated, err := s.queries.UpdateTenant(ctx, params)
	if err != nil {
		s.logger.Error("failed to update tenant", zap.Error(err))
		return nil, NewInternalError("Failed to update tenant")
	}

	return &updated, nil
}

func stringVal(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func validateStatusTransition(current, next string) error {
	if current == "deleted" {
		return NewValidationError("status", "Cannot transition from 'deleted' to any other status")
	}
	validTransitions := map[string][]string{
		"creating":  {"active", "suspended", "deleted"},
		"active":    {"suspended", "deleted"},
		"suspended": {"active", "deleted"},
	}
	allowed := validTransitions[current]
	for _, valid := range allowed {
		if next == valid {
			return nil
		}
	}
	return NewValidationError("status", fmt.Sprintf("Cannot transition from '%s' to '%s'", current, next))
}

func ResolveQuotas(plan string, overrides *QuotaRequest) Quotas {
	base := planDefaults[plan]
	if overrides == nil {
		return base
	}
	if overrides.MaxClusters != nil {
		base.MaxClusters = *overrides.MaxClusters
	}
	if overrides.MaxNodes != nil {
		base.MaxNodes = *overrides.MaxNodes
	}
	if overrides.MaxCPU != nil {
		base.MaxCPU = *overrides.MaxCPU
	}
	if overrides.MaxMemory != nil {
		base.MaxMemory = *overrides.MaxMemory
	}
	return base
}
