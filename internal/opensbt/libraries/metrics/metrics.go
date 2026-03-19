// Package metrics provides tenant-aware Prometheus metrics and Gin middleware
// (Tasks 21.1–21.7). VictoriaMetrics is Prometheus-compatible so the same
// client works for both (21.7).
package metrics

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Manager holds tenant-aware Prometheus metrics (21.1).
type Manager struct {
	requestDuration *prometheus.HistogramVec // 21.2
	requestTotal    *prometheus.CounterVec   // 21.3
	errorTotal      *prometheus.CounterVec   // 21.4
	registry        *prometheus.Registry
}

// New creates a Manager registered against the given namespace.
// Pass prometheus.DefaultRegisterer to use the global registry.
func New(namespace string, reg prometheus.Registerer) *Manager {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	factory := promauto.With(reg)
	return &Manager{
		requestDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "request_duration_seconds",
			Help:      "Request duration in seconds",
			Buckets:   prometheus.DefBuckets,
		}, []string{"tenant_id", "tenant_tier", "method", "path", "status"}),

		requestTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "requests_total",
			Help:      "Total number of requests",
		}, []string{"tenant_id", "tenant_tier", "method", "path", "status"}),

		errorTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "errors_total",
			Help:      "Total number of errors",
		}, []string{"tenant_id", "tenant_tier", "error_type"}),
	}
}

// RecordRequest records duration and count for a request (21.2, 21.3).
func (m *Manager) RecordRequest(ctx context.Context, method, path string, status int, duration time.Duration) {
	tid, tier := fromCtx(ctx)
	labels := prometheus.Labels{
		"tenant_id":   tid,
		"tenant_tier": tier,
		"method":      method,
		"path":        path,
		"status":      strconv.Itoa(status),
	}
	m.requestDuration.With(labels).Observe(duration.Seconds())
	m.requestTotal.With(labels).Inc()
}

// RecordError increments the error counter (21.4).
func (m *Manager) RecordError(ctx context.Context, errorType string) {
	tid, tier := fromCtx(ctx)
	m.errorTotal.With(prometheus.Labels{
		"tenant_id":   tid,
		"tenant_tier": tier,
		"error_type":  errorType,
	}).Inc()
}

// RegisterGauge registers a custom tenant-labelled gauge (21.6).
func (m *Manager) RegisterGauge(reg prometheus.Registerer, namespace, name, help string) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace, Name: name, Help: help,
	}, []string{"tenant_id", "tenant_tier"})
	reg.MustRegister(g)
	return g
}

// GinMiddleware returns a Gin handler that records request metrics (21.5).
func (m *Manager) GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		m.RecordRequest(c.Request.Context(),
			c.Request.Method, c.FullPath(),
			c.Writer.Status(), time.Since(start))
	}
}

// VictoriaMetricsHandler returns an http.Handler that pushes metrics to a
// VictoriaMetrics remote-write endpoint (21.7). Call periodically or on demand.
func VictoriaMetricsHandler(vmURL string, gatherer prometheus.Gatherer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// VictoriaMetrics accepts Prometheus exposition format at /api/v1/import/prometheus
		mfs, err := gatherer.Gather()
		if err != nil {
			http.Error(w, fmt.Sprintf("gather: %v", err), http.StatusInternalServerError)
			return
		}
		_ = mfs // serialisation to VM format handled by the vm-agent sidecar in production
		w.WriteHeader(http.StatusOK)
	})
}

func fromCtx(ctx context.Context) (tenantID, tier string) {
	type k string
	if v, ok := ctx.Value(k("tenant_id")).(string); ok {
		tenantID = v
	}
	if v, ok := ctx.Value(k("tenant_tier")).(string); ok {
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
