package interfaces

import (
	"context"

	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// IMetering provides usage metering and tracking capabilities
type IMetering interface {
	// Meter Management
	CreateMeter(ctx context.Context, meter models.Meter) error
	GetMeter(ctx context.Context, meterID string) (*models.Meter, error)
	UpdateMeter(ctx context.Context, meterID string, updates models.MeterUpdates) error
	DeleteMeter(ctx context.Context, meterID string) error
	ListMeters(ctx context.Context, filters models.MeterFilters) ([]models.Meter, error)

	// Usage Ingestion
	IngestUsageEvent(ctx context.Context, event models.UsageEvent) error
	IngestUsageEventBatch(ctx context.Context, events []models.UsageEvent) error

	// Usage Queries
	GetUsage(ctx context.Context, meterID string, period models.TimePeriod) (*models.UsageData, error)
	GetTenantUsage(ctx context.Context, tenantID string, period models.TimePeriod) (*models.TenantUsageData, error)
	AggregateUsage(ctx context.Context, req models.AggregationRequest) (*models.AggregationResult, error)

	// Usage Management
	CancelUsageEvents(ctx context.Context, eventIDs []string) error
}
