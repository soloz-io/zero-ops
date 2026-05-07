package validators

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	agentdb "github.com/soloz-io/zero-ops/internal/platform/agent-core/database/generated"
)

// ToolValidator validates tool authorization for tenants
type ToolValidator struct {
	queries *agentdb.Queries
}

// NewToolValidator creates a new tool validator
func NewToolValidator(queries *agentdb.Queries) *ToolValidator {
	return &ToolValidator{
		queries: queries,
	}
}

// ValidateToolAuthorization checks if tenant has access to requested tools
func (v *ToolValidator) ValidateToolAuthorization(ctx context.Context, tenantID string, tools []string) error {
	if len(tools) == 0 {
		return nil
	}
	
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return &ValidationError{
			Code:    "invalid_tenant_id",
			Message: fmt.Sprintf("Invalid tenant ID format: %s", tenantID),
		}
	}
	
	// Validate each tool
	for _, tool := range tools {
		authorized, err := v.queries.ValidateToolAccess(ctx, agentdb.ValidateToolAccessParams{
			TenantID: tenantUUID,
			ToolName: tool,
		})
		
		if err != nil {
			return fmt.Errorf("failed to validate tool access: %w", err)
		}
		
		if !authorized {
			return &ValidationError{
				Code:    "unauthorized_tool",
				Message: fmt.Sprintf("Tool '%s' not authorized for tenant. Contact support to enable.", tool),
				Details: map[string]interface{}{
					"tool":      tool,
					"tenant_id": tenantID,
				},
			}
		}
	}
	
	return nil
}
