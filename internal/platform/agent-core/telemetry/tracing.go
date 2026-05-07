package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	// TracerName is the name of the tracer for agent-core
	TracerName = "github.com/soloz-io/zero-ops/internal/platform/agent-core"
)

// Tracer is the OpenTelemetry tracer for agent-core
var Tracer trace.Tracer

func init() {
	Tracer = otel.Tracer(TracerName)
}

// StartMCPToolSpan starts a new span for an MCP tool execution
func StartMCPToolSpan(ctx context.Context, toolName string, params map[string]interface{}) (context.Context, trace.Span) {
	ctx, span := Tracer.Start(ctx, "mcp_tool."+toolName,
		trace.WithAttributes(
			attribute.String("tool.name", toolName),
			attribute.String("tenant.id", getTenantIDFromContext(ctx)),
		),
	)
	
	// Add relevant parameters as attributes
	if agentID, ok := params["agent_id"].(string); ok {
		span.SetAttributes(attribute.String("agent.id", agentID))
	}
	if deploymentID, ok := params["deployment_id"].(string); ok {
		span.SetAttributes(attribute.String("deployment.id", deploymentID))
	}
	
	return ctx, span
}

// StartAgentOperationSpan starts a new span for agent operations
func StartAgentOperationSpan(ctx context.Context, operation, agentID string) (context.Context, trace.Span) {
	return Tracer.Start(ctx, "agent."+operation,
		trace.WithAttributes(
			attribute.String("operation", operation),
			attribute.String("agent.id", agentID),
			attribute.String("tenant.id", getTenantIDFromContext(ctx)),
		),
	)
}

// StartNATSEventSpan starts a new span for NATS event processing
func StartNATSEventSpan(ctx context.Context, eventType string) (context.Context, trace.Span) {
	return Tracer.Start(ctx, "nats.event."+eventType,
		trace.WithAttributes(
			attribute.String("event.type", eventType),
		),
	)
}

// RecordError records an error on the span
func RecordError(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.Bool("error", true))
	}
}