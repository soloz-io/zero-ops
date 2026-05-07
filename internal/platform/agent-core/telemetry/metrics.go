package telemetry

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus metrics for agent-core MCP tools
var (
	// MCP tool execution duration histogram
	MCPToolDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agents_mcp_tool_duration_seconds",
			Help:    "Duration of MCP tool executions in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"tool_name", "tenant_id", "status"},
	)

	// Agent deployments gauge by status and phase
	AgentDeployments = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "agents_deployments_total",
			Help: "Total number of agent deployments by status and phase",
		},
		[]string{"status", "phase", "tenant_tier"},
	)

	// Agent operations counter
	AgentOperations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agents_operations_total",
			Help: "Total number of agent operations",
		},
		[]string{"operation", "tenant_id", "status"},
	)

	// NATS event processing metrics
	NATSEvents = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agents_nats_events_total",
			Help: "Total number of NATS events processed",
		},
		[]string{"event_type", "status"},
	)
)

// RecordMCPToolDuration records the duration of an MCP tool execution
func RecordMCPToolDuration(toolName, tenantID, status string, duration time.Duration) {
	MCPToolDuration.WithLabelValues(toolName, tenantID, status).Observe(duration.Seconds())
}

// UpdateAgentDeploymentCount updates the agent deployment gauge
func UpdateAgentDeploymentCount(status, phase, tenantTier string, count float64) {
	AgentDeployments.WithLabelValues(status, phase, tenantTier).Set(count)
}

// IncrementAgentOperation increments the agent operation counter
func IncrementAgentOperation(operation, tenantID, status string) {
	AgentOperations.WithLabelValues(operation, tenantID, status).Inc()
}

// IncrementNATSEvent increments the NATS event counter
func IncrementNATSEvent(eventType, status string) {
	NATSEvents.WithLabelValues(eventType, status).Inc()
}