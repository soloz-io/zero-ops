package telemetry

import (
	"context"
	"time"
)

// MCPToolMiddleware wraps MCP tool handlers with metrics, logging, and tracing instrumentation
func MCPToolMiddleware(toolName string, handler func(context.Context, map[string]interface{}) (interface{}, error)) func(context.Context, map[string]interface{}) (interface{}, error) {
	return func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
		// Start tracing span
		ctx, span := StartMCPToolSpan(ctx, toolName, params)
		defer span.End()
		
		// Log tool start
		LogMCPToolStart(ctx, toolName, params)
		
		// Record metrics start time
		start := time.Now()
		tenantID := getTenantIDFromContext(ctx)
		
		// Execute the handler
		result, err := handler(ctx, params)
		
		// Record error in span if present
		RecordError(span, err)
		
		// Log completion
		LogMCPToolComplete(ctx, toolName, err)
		
		// Record metrics
		duration := time.Since(start)
		status := "success"
		if err != nil {
			status = "error"
		}
		
		RecordMCPToolDuration(toolName, tenantID, status, duration)
		IncrementAgentOperation(toolName, tenantID, status)
		
		return result, err
	}
}

// getTenantIDFromContext extracts tenant ID from context
func getTenantIDFromContext(ctx context.Context) string {
	if tenantID := ctx.Value("tenant_id"); tenantID != nil {
		if id, ok := tenantID.(string); ok {
			return id
		}
	}
	return "unknown"
}