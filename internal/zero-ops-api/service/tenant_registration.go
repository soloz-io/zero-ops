package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
	"go.uber.org/zap"
)

type TenantRegistrationService struct {
	storage  interfaces.IStorage
	eventBus interfaces.IEventBus
	logger   *zap.Logger
}

func NewTenantRegistrationService(storage interfaces.IStorage, eventBus interfaces.IEventBus, logger *zap.Logger) *TenantRegistrationService {
	return &TenantRegistrationService{
		storage:  storage,
		eventBus: eventBus,
		logger:   logger,
	}
}

type TenantRegistrationResult struct {
	ID        string
	Name      string
	Email     string
	Plan      string
	Status    string
	CreatedAt time.Time
}

func (s *TenantRegistrationService) CreateRegistration(ctx context.Context, name, email, plan string, quotasOverride *QuotaRequest, metadata map[string]string) (*TenantRegistrationResult, error) {
	if !rfc1123Regex.MatchString(name) || len(name) > 63 {
		return nil, NewValidationError("name", "Name must consist of lower case alphanumeric characters or '-', and must start and end with an alphanumeric character")
	}

	quotas := ResolveQuotas(plan, quotasOverride)

	regID := uuid.New().String()
	tenantID := uuid.New().String()

	helmValues := map[string]interface{}{
		"quotas": quotas,
	}
	if metadata != nil {
		helmValues["metadata"] = metadata
	}

	config := map[string]interface{}{
		"tier":       plan,
		"helmValues": helmValues,
	}

	registration := models.TenantRegistration{
		ID:        regID,
		TenantID:  tenantID,
		Name:      name,
		Email:     email,
		Status:    "pending",
		Tier:      plan,
		Config:    config,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.storage.CreateTenantRegistration(ctx, registration); err != nil {
		s.logger.Error("failed to create tenant registration", zap.Error(err), zap.String("name", name))
		return nil, NewInternalError("Failed to create tenant registration")
	}

	// Emit opensbt_onboardingRequest event
	event := models.NewEvent(
		models.EventOnboardingRequest,
		models.ControlPlaneEventSource,
		map[string]interface{}{
			"tenantId":       tenantID,
			"registrationId": regID,
			"name":           name,
			"email":          email,
			"tier":           plan,
			"quotas":         quotas,
		},
	)

	if err := s.eventBus.PublishAsync(ctx, event); err != nil {
		s.logger.Error("failed to publish onboarding event", zap.Error(err), zap.String("tenantId", tenantID))
	}

	return &TenantRegistrationResult{
		ID:        regID,
		Name:      name,
		Email:     email,
		Plan:      plan,
		Status:    "pending",
		CreatedAt: registration.CreatedAt,
	}, nil
}
