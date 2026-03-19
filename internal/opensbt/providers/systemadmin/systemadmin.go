// Package systemadmin provides an ISystemAdmin implementation that composes
// IAuth (admin user management) and IStorage (tenant metrics) with
// platform-level capabilities (Tasks 30.1–30.13).
package systemadmin

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/sirupsen/logrus"
	"github.com/soloz-io/zero-ops/internal/opensbt/controlplane"
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// SystemAdmin implements interfaces.ISystemAdmin (30.1–30.13).
type SystemAdmin struct {
	auth    interfaces.IAuth
	storage interfaces.IStorage

	mu          sync.RWMutex
	platformCfg map[string]interface{} // in-memory platform config (30.7)
	auditLog    []auditEntry           // in-memory audit log (30.8)

	// Prometheus metrics (30.5, 30.11, 30.12)
	tenantGauge  prometheus.Gauge
	userGauge    prometheus.Gauge
	cpuGauge     prometheus.Gauge
	memGauge     prometheus.Gauge

	log *logrus.Logger
}

type auditEntry struct {
	Time    time.Time `json:"time"`
	AdminID string    `json:"admin_id"`
	Action  string    `json:"action"`
	Detail  string    `json:"detail"`
}

// New creates a SystemAdmin (30.1).
func New(auth interfaces.IAuth, storage interfaces.IStorage) *SystemAdmin {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})

	return &SystemAdmin{
		auth:        auth,
		storage:     storage,
		platformCfg: map[string]interface{}{},
		log:         log,
		tenantGauge: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "opensbt_platform_tenants_total",
			Help: "Total number of tenants",
		}),
		userGauge: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "opensbt_platform_users_total",
			Help: "Total number of users",
		}),
		cpuGauge: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "opensbt_platform_goroutines",
			Help: "Number of goroutines (capacity planning proxy)",
		}),
		memGauge: promauto.NewGauge(prometheus.GaugeOpts{
			Name: "opensbt_platform_heap_alloc_bytes",
			Help: "Heap memory allocated in bytes",
		}),
	}
}

// ─── Platform Administrator Management (30.1–30.4) ───────────────────────────

// CreateSystemAdmin creates an admin user via IAuth with the "system_admin" role (30.2).
func (s *SystemAdmin) CreateSystemAdmin(ctx context.Context, props models.CreateAdminUserProps) error {
	if err := s.auth.CreateAdminUser(ctx, props); err != nil {
		return fmt.Errorf("systemadmin: create admin: %w", err)
	}
	s.record(ctx, "create_admin", props.Email)
	return nil
}

// UpdateSystemAdmin updates an admin user (30.3).
func (s *SystemAdmin) UpdateSystemAdmin(ctx context.Context, adminID string, updates models.UserUpdates) error {
	if err := s.auth.UpdateUser(ctx, adminID, updates); err != nil {
		return fmt.Errorf("systemadmin: update admin: %w", err)
	}
	s.record(ctx, "update_admin", adminID)
	return nil
}

// ListSystemAdmins returns all users with the system_admin role (30.4).
func (s *SystemAdmin) ListSystemAdmins(ctx context.Context) ([]models.User, error) {
	users, err := s.auth.ListUsers(ctx, models.UserFilters{Limit: 1000})
	if err != nil {
		return nil, err
	}
	var admins []models.User
	for _, u := range users {
		for _, r := range u.Roles {
			if r == "system_admin" {
				admins = append(admins, u)
				break
			}
		}
	}
	return admins, nil
}

// DeleteSystemAdmin removes an admin user (30.1).
func (s *SystemAdmin) DeleteSystemAdmin(ctx context.Context, adminID string) error {
	if err := s.auth.DeleteUser(ctx, adminID); err != nil {
		return fmt.Errorf("systemadmin: delete admin: %w", err)
	}
	s.record(ctx, "delete_admin", adminID)
	return nil
}

// ─── Platform Monitoring (30.5–30.6, 30.11–30.12) ────────────────────────────

// GetSystemMetrics collects platform-wide metrics (30.5, 30.11, 30.12).
func (s *SystemAdmin) GetSystemMetrics(ctx context.Context) (*interfaces.SystemMetrics, error) {
	tenants, err := s.storage.ListTenants(ctx, models.TenantFilters{Limit: 10000})
	if err != nil {
		return nil, err
	}
	active := 0
	for _, t := range tenants {
		if t.Status == "active" {
			active++
		}
	}

	users, err := s.auth.ListUsers(ctx, models.UserFilters{Limit: 10000})
	if err != nil {
		return nil, err
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	// Update Prometheus gauges (30.11, 30.12)
	s.tenantGauge.Set(float64(len(tenants)))
	s.userGauge.Set(float64(len(users)))
	s.cpuGauge.Set(float64(runtime.NumGoroutine()))
	s.memGauge.Set(float64(mem.HeapAlloc))

	return &interfaces.SystemMetrics{
		TotalTenants:  len(tenants),
		ActiveTenants: active,
		TotalUsers:    len(users),
		ResourceUsage: map[string]interface{}{
			"goroutines":       runtime.NumGoroutine(),
			"heap_alloc_bytes": mem.HeapAlloc,
			"heap_sys_bytes":   mem.HeapSys,
			"num_cpu":          runtime.NumCPU(),
		},
		CollectedAt: time.Now().UTC(),
	}, nil
}

// GetPlatformHealth returns health status of key subsystems (30.6).
func (s *SystemAdmin) GetPlatformHealth(ctx context.Context) (map[string]interface{}, error) {
	health := map[string]interface{}{
		"status":      "healthy",
		"checked_at":  time.Now().UTC(),
		"maintenance": false,
	}

	// Check storage
	if _, err := s.storage.ListTenants(ctx, models.TenantFilters{Limit: 1}); err != nil {
		health["status"] = "degraded"
		health["storage"] = "unhealthy: " + err.Error()
	} else {
		health["storage"] = "healthy"
	}

	// Check auth
	if _, err := s.auth.ListUsers(ctx, models.UserFilters{Limit: 1}); err != nil {
		health["status"] = "degraded"
		health["auth"] = "unhealthy: " + err.Error()
	} else {
		health["auth"] = "healthy"
	}

	// Maintenance mode status
	maint, _ := s.IsMaintenanceMode(ctx)
	if maint {
		health["maintenance"] = true
		health["status"] = "maintenance"
	}

	return health, nil
}

// ─── Platform Configuration (30.7) ───────────────────────────────────────────

func (s *SystemAdmin) GetPlatformConfig(_ context.Context) (map[string]interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]interface{}, len(s.platformCfg))
	for k, v := range s.platformCfg {
		out[k] = v
	}
	return out, nil
}

func (s *SystemAdmin) UpdatePlatformConfig(ctx context.Context, cfg map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range cfg {
		s.platformCfg[k] = v
	}
	s.record(ctx, "update_platform_config", fmt.Sprintf("keys=%d", len(cfg)))
	return nil
}

// ─── Maintenance Mode (30.9–30.10) ───────────────────────────────────────────

func (s *SystemAdmin) EnableMaintenanceMode(ctx context.Context, reason string) error {
	controlplane.SetMaintenanceMode(true, reason)
	s.record(ctx, "enable_maintenance", reason)
	return nil
}

func (s *SystemAdmin) DisableMaintenanceMode(ctx context.Context) error {
	controlplane.SetMaintenanceMode(false, "")
	s.record(ctx, "disable_maintenance", "")
	return nil
}

func (s *SystemAdmin) IsMaintenanceMode(_ context.Context) (bool, error) {
	// Read the exported flag via a lightweight approach — re-use SetMaintenanceMode
	// with a no-op to avoid a separate exported getter; instead expose one.
	return controlplane.IsMaintenanceMode(), nil
}

// ─── Audit Logging (30.8) ────────────────────────────────────────────────────

// GetAuditLog returns recorded platform audit entries (30.8).
func (s *SystemAdmin) GetAuditLog(_ context.Context) []auditEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]auditEntry, len(s.auditLog))
	copy(out, s.auditLog)
	return out
}

func (s *SystemAdmin) record(ctx context.Context, action, detail string) {
	adminID, _ := ctx.Value(ctxKey("user_id")).(string)
	entry := auditEntry{Time: time.Now().UTC(), AdminID: adminID, Action: action, Detail: detail}
	s.mu.Lock()
	s.auditLog = append(s.auditLog, entry)
	s.mu.Unlock()
	s.log.WithFields(logrus.Fields{
		"admin_id": adminID,
		"action":   action,
		"detail":   detail,
	}).Info("platform audit")
}

// ─── Backup / DR (30.13) ─────────────────────────────────────────────────────

// BackupConfig exports platform config and audit log as a snapshot map (30.13).
func (s *SystemAdmin) BackupConfig(ctx context.Context) (map[string]interface{}, error) {
	cfg, err := s.GetPlatformConfig(ctx)
	if err != nil {
		return nil, err
	}
	s.record(ctx, "backup_config", "")
	return map[string]interface{}{
		"platform_config": cfg,
		"audit_log":       s.GetAuditLog(ctx),
		"backed_up_at":    time.Now().UTC(),
	}, nil
}

// RestoreConfig replaces platform config from a backup snapshot (30.13).
func (s *SystemAdmin) RestoreConfig(ctx context.Context, snapshot map[string]interface{}) error {
	cfg, ok := snapshot["platform_config"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("systemadmin: restore: missing platform_config in snapshot")
	}
	if err := s.UpdatePlatformConfig(ctx, cfg); err != nil {
		return err
	}
	s.record(ctx, "restore_config", "")
	return nil
}

type ctxKey string
