package interfaces

import (
	"context"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// IMetering provides runtime metering and entitlement capabilities via OpenMeter
// ARCHITECTURAL NOTE: Static catalog (Meters, Features, Plans) managed via hub-operator CRDs
// This interface handles ONLY dynamic runtime operations
type IMetering interface {
	// Meter Management (Read-Only for UI Display)
	GetMeter(ctx context.Context, namespace, meterID string) (*models.Meter, error)
	ListMeters(ctx context.Context, namespace string) ([]models.Meter, error)

	// Feature Management (Read-Only for UI Display)
	GetFeature(ctx context.Context, namespace, featureID string) (*models.Feature, error)
	ListFeatures(ctx context.Context, namespace string) ([]models.Feature, error)

	// Plan Management (Read-Only for UI Display)
	GetPlan(ctx context.Context, namespace, planID string) (*models.Plan, error)
	ListPlans(ctx context.Context, namespace string) ([]models.Plan, error)

	// Usage Queries (Req 2, 3)
	GetUsage(ctx context.Context, namespace string, filter models.UsageFilter) (*models.UsageReport, error)
	GetTenantUsage(ctx context.Context, namespace string, period models.TimePeriod) (*models.TenantUsageData, error)
	GetUserUsage(ctx context.Context, namespace, subjectID string, period models.TimePeriod) (*models.UserUsageData, error)

	// Entitlements (Req 6)
	CheckEntitlement(ctx context.Context, namespace, subjectID, featureKey string) (*models.EntitlementStatus, error)

	// Subject Management (Req 1) - Dynamic Runtime Operations
	RegisterSubject(ctx context.Context, namespace, subjectID string, metadata map[string]string) error
	DeleteSubject(ctx context.Context, namespace, subjectID string) error
	GetSubject(ctx context.Context, namespace, subjectID string) (*models.Subject, error)
	ListSubjects(ctx context.Context, namespace string) ([]models.Subject, error)
}
