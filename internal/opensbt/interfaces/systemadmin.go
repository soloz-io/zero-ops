package interfaces

import (
	"context"

	"github.com/soloz-io/zero-ops/internal/opensbt/models"
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
	// Platform Administrator Management
	CreateSystemAdmin(ctx context.Context, props models.CreateAdminUserProps) error
	UpdateSystemAdmin(ctx context.Context, adminID string, updates models.UserUpdates) error
	ListSystemAdmins(ctx context.Context) ([]models.User, error)
	DeleteSystemAdmin(ctx context.Context, adminID string) error

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

	// Backup and Disaster Recovery (30.13)
	BackupConfig(ctx context.Context) (map[string]interface{}, error)
	RestoreConfig(ctx context.Context, snapshot map[string]interface{}) error
}
