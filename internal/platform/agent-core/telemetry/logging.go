package telemetry

import (
	"context"
	"log/slog"
	"os"
)

// Logger provides structured logging for agent-core
var Logger *slog.Logger

func init() {
	// Initialize structured logger with JSON format
	Logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// LogMCPToolStart logs the start of an MCP tool execution
func LogMCPToolStart(ctx context.Context, toolName string, params map[string]interface{}) {
	Logger.InfoContext(ctx, "MCP tool started",
		slog.String("tool", toolName),
		slog.String("tenant_id", getTenantIDFromContext(ctx)),
		slog.Any("params", params),
	)
}

// LogMCPToolComplete logs the completion of an MCP tool execution
func LogMCPToolComplete(ctx context.Context, toolName string, err error) {
	if err != nil {
		Logger.ErrorContext(ctx, "MCP tool failed",
			slog.String("tool", toolName),
			slog.String("tenant_id", getTenantIDFromContext(ctx)),
			slog.String("error", err.Error()),
		)
	} else {
		Logger.InfoContext(ctx, "MCP tool completed",
			slog.String("tool", toolName),
			slog.String("tenant_id", getTenantIDFromContext(ctx)),
		)
	}
}

// LogAgentOperation logs agent operations (create, deploy, update, delete)
func LogAgentOperation(ctx context.Context, operation, agentID string, err error) {
	if err != nil {
		Logger.ErrorContext(ctx, "Agent operation failed",
			slog.String("operation", operation),
			slog.String("agent_id", agentID),
			slog.String("tenant_id", getTenantIDFromContext(ctx)),
			slog.String("error", err.Error()),
		)
	} else {
		Logger.InfoContext(ctx, "Agent operation completed",
			slog.String("operation", operation),
			slog.String("agent_id", agentID),
			slog.String("tenant_id", getTenantIDFromContext(ctx)),
		)
	}
}

// LogDeploymentStatusUpdate logs deployment status updates
func LogDeploymentStatusUpdate(ctx context.Context, deploymentID, oldStatus, newStatus string) {
	Logger.InfoContext(ctx, "Deployment status updated",
		slog.String("deployment_id", deploymentID),
		slog.String("tenant_id", getTenantIDFromContext(ctx)),
		slog.String("old_status", oldStatus),
		slog.String("new_status", newStatus),
	)
}

// LogNATSEvent logs NATS event processing
func LogNATSEvent(ctx context.Context, eventType string, payload interface{}, err error) {
	if err != nil {
		Logger.ErrorContext(ctx, "NATS event processing failed",
			slog.String("event_type", eventType),
			slog.Any("payload", payload),
			slog.String("error", err.Error()),
		)
	} else {
		Logger.InfoContext(ctx, "NATS event processed",
			slog.String("event_type", eventType),
			slog.Any("payload", payload),
		)
	}
}