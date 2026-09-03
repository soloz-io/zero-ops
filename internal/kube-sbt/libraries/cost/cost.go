// Package cost provides tenant-aware cost attribution metrics (Tasks 24.1–24.6).
package cost

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// tierCostPerRequest defines the base cost per API request by tier (24.5).
var tierCostPerRequest = map[string]float64{
	"basic":      0.001,
	"standard":   0.0008,
	"premium":    0.0006,
	"enterprise": 0.0004,
}

// Manager tracks resource usage and cost attribution per tenant (24.1, 24.2).
type Manager struct {
	resourceUsage *prometheus.GaugeVec   // 24.1
	costAttrib    *prometheus.GaugeVec   // 24.2
	requestCost   *prometheus.CounterVec // 24.3
}

// New creates a Manager registered under namespace.
func New(namespace string, reg prometheus.Registerer) *Manager {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	f := promauto.With(reg)
	return &Manager{
		resourceUsage: f.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Name: "tenant_resource_usage",
			Help: "Resource usage per tenant",
		}, []string{"tenant_id", "tenant_tier", "resource_type", "unit"}),

		costAttrib: f.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: namespace, Name: "tenant_cost_attribution",
			Help: "Cost attribution per tenant",
		}, []string{"tenant_id", "tenant_tier", "cost_type", "currency"}),

		requestCost: f.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace, Name: "tenant_request_cost_total",
			Help: "Cumulative request cost per tenant",
		}, []string{"tenant_id", "tenant_tier", "service", "operation"}),
	}
}

// RecordResourceUsage records a resource consumption gauge (24.1).
func (m *Manager) RecordResourceUsage(ctx context.Context, resourceType string, usage float64, unit string) {
	tid, tier := fromCtx(ctx)
	m.resourceUsage.With(prometheus.Labels{
		"tenant_id": tid, "tenant_tier": tier,
		"resource_type": resourceType, "unit": unit,
	}).Set(usage)
}

// RecordCost records a cost attribution gauge (24.2).
func (m *Manager) RecordCost(ctx context.Context, costType string, amount float64, currency string) {
	tid, tier := fromCtx(ctx)
	m.costAttrib.With(prometheus.Labels{
		"tenant_id": tid, "tenant_tier": tier,
		"cost_type": costType, "currency": currency,
	}).Set(amount)
}

// GinMiddleware records per-request cost based on tier (24.4, 24.5).
func (m *Manager) GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		duration := time.Since(start)
		ctx := c.Request.Context()
		tid, tier := fromCtx(ctx)
		cost := tierCostPerRequest[tier]
		if cost == 0 {
			cost = 0.001
		}
		// Scale cost by duration (ms) for long-running requests
		cost += duration.Seconds() * 0.0001
		m.requestCost.With(prometheus.Labels{
			"tenant_id": tid, "tenant_tier": tier,
			"service":   "api",
			"operation": c.Request.Method + " " + c.FullPath(),
		}).Add(cost)
	}
}

// GetTenantCost returns the cumulative cost counter value for a tenant (24.6).
// In production this is queried from VictoriaMetrics/Prometheus.
func (m *Manager) GetTenantCost(ctx context.Context, service, operation string) float64 {
	// Prometheus counters are read via the metrics endpoint; this is a stub
	// for direct programmatic access when needed.
	return 0
}

type ctxKey string

func fromCtx(ctx context.Context) (tenantID, tier string) {
	if v, ok := ctx.Value(ctxKey("tenant_id")).(string); ok {
		tenantID = v
	}
	if v, ok := ctx.Value(ctxKey("tenant_tier")).(string); ok {
		tier = v
	}
	if tenantID == "" {
		tenantID = "unknown"
	}
	if tier == "" {
		tier = "unknown"
	}
	return
}
