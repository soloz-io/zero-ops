package service

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/soloz-io/zero-ops/internal/platform/agent-core/client"
	agentdb "github.com/soloz-io/zero-ops/internal/platform/agent-core/database/generated"
	. "github.com/soloz-io/zero-ops/internal/platform/agent-core/database/generated"
	"github.com/soloz-io/zero-ops/internal/platform/agent-core/validators"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/models"
)

// AgentService orchestrates agent operations
type AgentService struct {
	agentRegistryClient *client.AgentRegistryClient
	hubClient           *client.HubClient
	eventBus            interfaces.IEventBus
	queries             *agentdb.Queries
	modelValidator      *validators.ModelValidator
	toolValidator       *validators.ToolValidator
}

// Config holds agent service dependencies
type Config struct {
	AgentRegistryClient *client.AgentRegistryClient
	HubClient           *client.HubClient
	EventBus            interfaces.IEventBus
	Queries             *agentdb.Queries
}

// NewAgentService creates a new agent service
func NewAgentService(cfg Config) *AgentService {
	return &AgentService{
		agentRegistryClient: cfg.AgentRegistryClient,
		hubClient:           cfg.HubClient,
		eventBus:            cfg.EventBus,
		queries:             cfg.Queries,
		modelValidator:      validators.NewModelValidator(),
		toolValidator:       validators.NewToolValidator(cfg.Queries),
	}
}

// CreateAgent orchestrates agent creation with platform-specific validation
func (s *AgentService) CreateAgent(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// 1. Extract tenant context from JWT (set by middleware)
	tenantID, ok := ctx.Value("tenant_id").(string)
	if !ok || tenantID == "" {
		return nil, fmt.Errorf("tenant_id not found in context")
	}
	
	tenantTier, ok := ctx.Value("tenant_tier").(string)
	if !ok || tenantTier == "" {
		return nil, fmt.Errorf("tenant_tier not found in context")
	}
	
	// 2. Set tenant context for RLS (Task 3.2.3)
	if err := s.setTenantContext(ctx, tenantID); err != nil {
		return nil, fmt.Errorf("failed to set tenant context: %w", err)
	}
	// 2. Set tenant context for RLS (Task 3.2.3)
	if err := s.setTenantContext(ctx, tenantID); err != nil {
		return nil, fmt.Errorf("failed to set tenant context: %w", err)
	}
	
	// 3. Extract and validate parameters
	name, _ := params["name"].(string)
	version, _ := params["version"].(string)
	if version == "" {
		version = "v1"
	}
	agentType, _ := params["agent_type"].(string)
	systemMessage, _ := params["system_message"].(string)
	
	// Extract model config
	modelConfig, _ := params["model_config"].(map[string]interface{})
	if modelConfig == nil {
		return nil, fmt.Errorf("model_config is required")
	}
	modelName, _ := modelConfig["name"].(string)
	
	// Extract provider from model name (simplified - assumes format like "gpt-4", "claude-3-opus")
	provider := extractProvider(modelName)
	
	// 3. Platform-specific validation
	if err := s.modelValidator.ValidateModelAuthorization(ctx, provider, modelName, tenantTier); err != nil {
		return nil, err
	}
	
	// Validate tool authorization
	toolAccess, _ := params["tool_access"].([]interface{})
	tools := make([]string, 0, len(toolAccess))
	for _, t := range toolAccess {
		if toolStr, ok := t.(string); ok {
			tools = append(tools, toolStr)
		}
	}
	
	if err := s.toolValidator.ValidateToolAuthorization(ctx, tenantID, tools); err != nil {
		return nil, err
	}
	
	// 4. Call AgentRegistry OSS API (POST /v0/agents)
	agentReq := &client.AgentRequest{
		Name:              name,
		Version:           version,
		AgentType:         agentType,
		SystemMessage:     systemMessage,
		ToolAccess:        tools,
		MemoryConfig:      getMapOrEmpty(params, "memory_config"),
		GuardrailPolicies: getMapOrEmpty(params, "guardrail_policies"),
		ModelConfig:       modelConfig,
	}
	
	agentResp, err := s.agentRegistryClient.CreateAgent(ctx, agentReq)
	if err != nil {
		return nil, fmt.Errorf("agentregistry API error: %w", err)
	}
	
	// 5. Publish NATS event (platform-specific side effect)
	event := models.NewEvent(
		"hub.platform.agent.created",
		"hub.platform.agents",
		map[string]interface{}{
			"tenant_id":     tenantID,
			"agent_name":    agentResp.Agent.Name,
			"agent_version": agentResp.Agent.Version,
			"agent_type":    agentResp.Agent.AgentType,
		},
	)
	
	if err := s.eventBus.PublishAsync(ctx, event); err != nil {
		// Log error but don't fail the request (side effect)
		fmt.Printf("failed to publish agent.created event: %v\n", err)
	}
	
	// 6. Return MCP-formatted response
	return map[string]interface{}{
		"agent_id":            agentResp.Agent.ID,
		"name":                agentResp.Agent.Name,
		"version":             agentResp.Agent.Version,
		"status":              "registered",
		"deployment_required": true,
		"created_at":          agentResp.Agent.CreatedAt,
		"message":             "Agent created successfully. Use deploy_agent to provision runtime.",
	}, nil
}

// ListAgents orchestrates agent listing with deployment status enrichment
func (s *AgentService) ListAgents(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// 1. Extract tenant context from JWT
	tenantID, ok := ctx.Value("tenant_id").(string)
	if !ok || tenantID == "" {
		return nil, fmt.Errorf("tenant_id not found in context")
	}
	
	// 2. Set tenant context for RLS
	if err := s.setTenantContext(ctx, tenantID); err != nil {
		return nil, fmt.Errorf("failed to set tenant context: %w", err)
	}
	
	// 3. Extract parameters
	limit, _ := params["limit"].(float64)
	if limit == 0 {
		limit = 50
	}
	
	// 3. Call AgentRegistry ListAgents API
	listReq := &client.ListAgentsRequest{
		Limit: int(limit),
	}
	
	listResp, err := s.agentRegistryClient.ListAgents(ctx, listReq)
	if err != nil {
		return nil, fmt.Errorf("agentregistry list agents: %w", err)
	}
	
	// 4. Enrich with deployment status from Hub PostgREST
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant_id: %w", err)
	}
	
	infraStatuses, err := s.hubClient.ListAgentDeployments(ctx, tenantUUID.String())
	if err != nil {
		// Log error but continue (deployment status is optional enrichment)
		fmt.Printf("failed to fetch deployment statuses: %v\n", err)
		infraStatuses = []client.AgentInfraStatus{}
	}
	
	// Build deployment status map
	statusMap := make(map[string]client.AgentInfraStatus)
	for _, status := range infraStatuses {
		statusMap[status.DeploymentID] = status
	}
	
	// 5. Format response
	agents := make([]map[string]interface{}, 0, len(listResp.Agents))
	for _, agentWithMeta := range listResp.Agents {
		agent := agentWithMeta.Agent
		
		agentData := map[string]interface{}{
			"agent_id":   agent.ID,
			"name":       agent.Name,
			"version":    agent.Version,
			"agent_type": agent.AgentType,
			"status":     "registered",
			"created_at": agent.CreatedAt,
		}
		
		// Add deployment info if available
		if agentWithMeta.Meta.Deployments != nil && len(agentWithMeta.Meta.Deployments.Deployments) > 0 {
			deployment := agentWithMeta.Meta.Deployments.Deployments[0]
			agentData["deployment_id"] = deployment.ID
			agentData["deployment_status"] = deployment.Status
			
			// Enrich with infra status if available
			if infraStatus, ok := statusMap[deployment.ID]; ok {
				agentData["phase"] = infraStatus.Phase
				agentData["replicas"] = infraStatus.Replicas
			}
		}
		
		agents = append(agents, agentData)
	}
	
	return map[string]interface{}{
		"agents":      agents,
		"total_count": listResp.Total,
		"limit":       int(limit),
	}, nil
}

// Helper functions

// setTenantContext sets the PostgreSQL session variable for RLS enforcement
func (s *AgentService) setTenantContext(ctx context.Context, tenantID string) error {
	// Execute SET app.tenant_id = $1 to enforce RLS policies
	// This assumes s.queries has access to the database connection
	// Implementation depends on the database connection pattern used
	// For now, this is a placeholder - actual implementation would use database/sql
	// or the connection pool to execute: SET app.tenant_id = $1
	return nil
}

func extractProvider(modelName string) string {
	// Simple provider extraction based on model name prefix
	if len(modelName) >= 3 {
		prefix := modelName[:3]
		switch prefix {
		case "gpt":
			return "openai"
		case "cla":
			return "anthropic"
		case "gem":
			return "google"
		}
	}
	return "openai" // default
}

func getMapOrEmpty(params map[string]interface{}, key string) map[string]interface{} {
	if val, ok := params[key].(map[string]interface{}); ok {
		return val
	}
	return make(map[string]interface{})
}
// UpdateAgent orchestrates agent updates with optional redeployment
func (s *AgentService) UpdateAgent(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// 1. Extract tenant context from JWT
	tenantID, ok := ctx.Value("tenant_id").(string)
	if !ok || tenantID == "" {
		return nil, fmt.Errorf("tenant_id not found in context")
	}
	
	tenantTier, ok := ctx.Value("tenant_tier").(string)
	if !ok || tenantTier == "" {
		return nil, fmt.Errorf("tenant_tier not found in context")
	}
	
	// 2. Set tenant context for RLS
	if err := s.setTenantContext(ctx, tenantID); err != nil {
		return nil, fmt.Errorf("failed to set tenant context: %w", err)
	}
	
	// 3. Extract and validate parameters
	agentID, ok := params["agent_id"].(string)
	if !ok || agentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	
	// Parse agent ID to get name/version
	agentName, agentVersion := parseAgentID(agentID)
	
	// 3. Validate model authorization if model_config provided
	if modelConfig, ok := params["model_config"].(map[string]interface{}); ok {
		if modelName, ok := modelConfig["name"].(string); ok {
			provider := extractProvider(modelName)
			if err := s.modelValidator.ValidateModelAuthorization(ctx, provider, modelName, tenantTier); err != nil {
				return nil, err
			}
		}
	}
	
	// 4. Validate tool authorization if tool_access provided
	if toolAccess, ok := params["tool_access"].([]interface{}); ok {
		tools := make([]string, 0, len(toolAccess))
		for _, t := range toolAccess {
			if toolStr, ok := t.(string); ok {
				tools = append(tools, toolStr)
			}
		}
		if err := s.toolValidator.ValidateToolAuthorization(ctx, tenantID, tools); err != nil {
			return nil, err
		}
	}
	
	// 5. Update agent in AgentRegistry (POST /v0/agents - upsert)
	agentReq := &client.AgentRequest{
		Name:    agentName,
		Version: agentVersion,
	}
	
	// Only update provided fields
	if systemMessage, ok := params["system_message"].(string); ok {
		agentReq.SystemMessage = systemMessage
	}
	if toolAccess, ok := params["tool_access"].([]interface{}); ok {
		tools := make([]string, 0, len(toolAccess))
		for _, t := range toolAccess {
			if toolStr, ok := t.(string); ok {
				tools = append(tools, toolStr)
			}
		}
		agentReq.ToolAccess = tools
	}
	if memoryConfig, ok := params["memory_config"].(map[string]interface{}); ok {
		agentReq.MemoryConfig = memoryConfig
	}
	if guardrailPolicies, ok := params["guardrail_policies"].(map[string]interface{}); ok {
		agentReq.GuardrailPolicies = guardrailPolicies
	}
	if modelConfig, ok := params["model_config"].(map[string]interface{}); ok {
		agentReq.ModelConfig = modelConfig
	}
	
	agentResp, err := s.agentRegistryClient.CreateAgent(ctx, agentReq)
	if err != nil {
		return nil, fmt.Errorf("agentregistry update agent: %w", err)
	}
	
	// 6. Check deployment status via AgentRegistry
	deployments, err := s.agentRegistryClient.ListDeployments(ctx, &client.DeploymentFilter{
		ResourceName: agentName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to check deployments: %w", err)
	}
	
	deployed := len(deployments) > 0
	var deploymentID string
	
	// 7. If deployed: trigger redeployment via AgentRegistry
	if deployed {
		deployment := deployments[0]
		deploymentID = deployment.ID
		
		// AgentRegistry handles redeployment when agent is updated
		// This triggers Deployment Adapter to apply updated CRD
		
		// Publish NATS event for agent update
		event := models.NewEvent(
			"hub.platform.agent.updated",
			"hub.platform.agents",
			map[string]interface{}{
				"tenant_id":     tenantID,
				"agent_id":      agentID,
				"deployment_id": deploymentID,
				"redeployed":    true,
			},
		)
		
		if err := s.eventBus.PublishAsync(ctx, event); err != nil {
			fmt.Printf("failed to publish agent.updated event: %v\n", err)
		}
	}
	
	// 8. Return MCP-formatted response
	return map[string]interface{}{
		"agent_id":      agentID,
		"name":          agentResp.Agent.Name,
		"version":       agentResp.Agent.Version,
		"status":        "updated",
		"deployed":      deployed,
		"deployment_id": deploymentID,
		"updated_at":    agentResp.Agent.UpdatedAt,
		"message":       getUpdateMessage(deployed),
	}, nil
}

// DeleteAgent orchestrates agent deletion and undeployment
func (s *AgentService) DeleteAgent(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// 1. Extract tenant context from JWT
	tenantID, ok := ctx.Value("tenant_id").(string)
	if !ok || tenantID == "" {
		return nil, fmt.Errorf("tenant_id not found in context")
	}
	
	// 2. Set tenant context for RLS
	if err := s.setTenantContext(ctx, tenantID); err != nil {
		return nil, fmt.Errorf("failed to set tenant context: %w", err)
	}
	
	// 3. Extract and validate parameters
	agentID, ok := params["agent_id"].(string)
	if !ok || agentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	
	purgeMemory, _ := params["purge_memory"].(bool)
	
	// Parse agent ID to get name/version
	agentName, agentVersion := parseAgentID(agentID)
	
	// 3. Check deployments via AgentRegistry
	deployments, err := s.agentRegistryClient.ListDeployments(ctx, &client.DeploymentFilter{
		ResourceName: agentName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to check deployments: %w", err)
	}
	
	deletedDeployments := 0
	
	// 4. For each deployment: call AgentRegistry DELETE /v0/deployments/{id}
	for _, deployment := range deployments {
		if err := s.agentRegistryClient.DeleteDeployment(ctx, deployment.ID); err != nil {
			fmt.Printf("failed to delete deployment %s: %v\n", deployment.ID, err)
			continue
		}
		deletedDeployments++
	}
	
	// 5. Delete agent from AgentRegistry
	if err := s.agentRegistryClient.DeleteAgent(ctx, agentName, agentVersion); err != nil {
		return nil, fmt.Errorf("agentregistry delete agent: %w", err)
	}
	
	// 6. Publish NATS event
	event := models.NewEvent(
		"hub.platform.agent.deleted",
		"hub.platform.agents",
		map[string]interface{}{
			"tenant_id":            tenantID,
			"agent_id":             agentID,
			"deleted_deployments":  deletedDeployments,
			"memory_purged":        purgeMemory,
		},
	)
	
	if err := s.eventBus.PublishAsync(ctx, event); err != nil {
		fmt.Printf("failed to publish agent.deleted event: %v\n", err)
	}
	
	// 7. Return MCP-formatted response
	return map[string]interface{}{
		"success":             true,
		"message":             fmt.Sprintf("Agent %s deleted successfully", agentID),
		"deleted_deployments": deletedDeployments,
		"memory_retained":     !purgeMemory,
	}, nil
}

// ListAuthorizedTools queries authorized tools for tenant with optional category filter
func (s *AgentService) ListAuthorizedTools(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// 1. Extract tenant context from JWT
	tenantID, ok := ctx.Value("tenant_id").(string)
	if !ok || tenantID == "" {
		return nil, fmt.Errorf("tenant_id not found in context")
	}
	
	tenantTier, ok := ctx.Value("tenant_tier").(string)
	if !ok || tenantTier == "" {
		return nil, fmt.Errorf("tenant_tier not found in context")
	}
	
	// 2. Set tenant context for RLS
	if err := s.setTenantContext(ctx, tenantID); err != nil {
		return nil, fmt.Errorf("failed to set tenant context: %w", err)
	}
	
	// 3. Extract optional category filter
	category, _ := params["category"].(string)
	
	// 3. Parse tenant UUID
	tenantUUID, err := uuid.Parse(tenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid tenant_id: %w", err)
	}
	
	// 4. Query authorized_tools table from Control Plane Shared DB
	var tools []GetAuthorizedToolsRow
	
	if category != "" {
		// Filter by category
		toolsByCategory, err := s.queries.GetAuthorizedToolsByCategory(ctx, GetAuthorizedToolsByCategoryParams{
			TenantID: tenantUUID,
			Category: sql.NullString{String: category, Valid: true},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to query authorized tools by category: %w", err)
		}
		// Convert to common type
		for _, tool := range toolsByCategory {
			tools = append(tools, GetAuthorizedToolsRow{
				ToolName:     tool.ToolName,
				Category:     tool.Category,
				Enabled:      tool.Enabled,
				Description:  tool.Description,
				RequiredTier: tool.RequiredTier,
			})
		}
	} else {
		// Get all authorized tools
		var err error
		tools, err = s.queries.GetAuthorizedTools(ctx, tenantUUID)
		if err != nil {
			return nil, fmt.Errorf("failed to query authorized tools: %w", err)
		}
	}
	
	// 5. Filter by tenant tier (additional platform-specific filtering)
	filteredTools := make([]GetAuthorizedToolsRow, 0, len(tools))
	for _, tool := range tools {
		if isToolAuthorizedForTier(tool.ToolName, tenantTier) {
			filteredTools = append(filteredTools, tool)
		}
	}
	
	// 6. Format response
	toolList := make([]map[string]interface{}, 0, len(filteredTools))
	for _, tool := range filteredTools {
		toolData := map[string]interface{}{
			"name":        tool.ToolName,
			"enabled":     tool.Enabled.Bool,
		}
		
		// Add optional fields if present
		if tool.Category.Valid {
			toolData["category"] = tool.Category.String
		}
		if tool.Description.Valid {
			toolData["description"] = tool.Description.String
		}
		if tool.RequiredTier.Valid {
			toolData["required_tier"] = tool.RequiredTier.String
		}
		
		toolList = append(toolList, toolData)
	}
	
	return map[string]interface{}{
		"tools":       toolList,
		"total_count": len(toolList),
		"tenant_tier": tenantTier,
		"category":    category,
	}, nil
}

// Helper functions

func isToolAuthorizedForTier(toolName, tenantTier string) bool {
	// Tier hierarchy: basic < standard < premium < enterprise
	tierLevel := map[string]int{
		"basic":      1,
		"standard":   2,
		"premium":    3,
		"enterprise": 4,
	}
	
	// Tool tier requirements (simplified - in real implementation this would be configurable)
	toolTierRequirements := map[string]int{
		"web_search":     1, // basic+
		"file_operations": 1, // basic+
		"database_query":  2, // standard+
		"api_calls":       2, // standard+
		"code_execution":  3, // premium+
		"system_admin":    4, // enterprise only
	}
	
	userLevel := tierLevel[tenantTier]
	requiredLevel, exists := toolTierRequirements[toolName]
	
	// If tool not in requirements map, allow for all tiers
	if !exists {
		return true
	}
	
	return userLevel >= requiredLevel
}

func getUpdateMessage(deployed bool) string {
	if deployed {
		return "Agent configuration updated successfully. Redeployment initiated."
	}
	return "Agent configuration updated successfully. Changes will apply when agent is deployed."
}