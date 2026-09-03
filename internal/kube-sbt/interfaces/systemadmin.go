package interfaces

import (
	"context"
)

// SystemMetrics represents platform-wide metrics
type SystemMetrics struct {
	TotalTenants   int                    `json:"total_tenants"`
	ActiveTenants  int                    `json:"active_tenants"`
	TotalUsers     int                    `json:"total_users"`
	ResourceUsage  map[string]interface{} `json:"resource_usage"`
	CollectedAt    interface{}            `json:"collected_at"`
}

// ISystemAdmin provides platform-level administration capabilities
type ISystemAdmin interface {
	// Platform Monitoring
	GetSystemMetrics(ctx context.Context) (*SystemMetrics, error)
	GetPlatformHealth(ctx context.Context) (map[string]interface{}, error)

	// Platform Configuration
	GetPlatformConfig(ctx context.Context) (map[string]interface{}, error)
	UpdatePlatformConfig(ctx context.Context, config map[string]interface{}) error

	// Maintenance
	EnableMaintenanceMode(ctx context.Context, reason string) error
	DisableMaintenanceMode(ctx context.Context) error
	IsMaintenanceMode(ctx context.Context) (bool, error)
}
