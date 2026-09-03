package service

import (
	"context"
	"fmt"

	"github.com/soloz-io/zero-ops/internal/platform/agent-core/client"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/models"
)

// DeploymentService orchestrates agent deployment operations
type DeploymentService struct {
	agentRegistryClient *client.AgentRegistryClient
	eventBus            interfaces.IEventBus
}

// DeploymentConfig holds deployment service dependencies
type DeploymentConfig struct {
	AgentRegistryClient *client.AgentRegistryClient
	EventBus            interfaces.IEventBus
}

// NewDeploymentService creates a new deployment service
func NewDeploymentService(cfg DeploymentConfig) *DeploymentService {
	return &DeploymentService{
		agentRegistryClient: cfg.AgentRegistryClient,
		eventBus:            cfg.EventBus,
	}
}

// DeployAgent orchestrates agent deployment via AgentRegistry integration
func (s *DeploymentService) DeployAgent(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// 1. Extract tenant context from JWT (set by middleware)
	tenantID, ok := ctx.Value("tenant_id").(string)
	if !ok || tenantID == "" {
		return nil, fmt.Errorf("tenant_id not found in context")
	}
	
	spokeClusterID, ok := ctx.Value("spoke_cluster_id").(string)
	if !ok || spokeClusterID == "" {
		return nil, fmt.Errorf("spoke_cluster_id not found in context")
	}
	
	// 2. Extract and validate parameters
	agentID, ok := params["agent_id"].(string)
	if !ok || agentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	
	// 3. Fetch agent from AgentRegistry (CRUD only)
	// Note: AgentRegistry uses name as ID, so we need to parse name/version from agentID
	agentName, agentVersion := parseAgentID(agentID)
	
	agent, err := s.agentRegistryClient.GetAgent(ctx, agentName, agentVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch agent: %w", err)
	}
	
	// 4. Create deployment via AgentRegistry (handles CRD generation and Kubernetes apply)
	deploymentReq := &client.DeploymentRequest{
		AgentID:    agentID,
		ProviderID: spokeClusterID,
	}
	
	deployment, err := s.agentRegistryClient.CreateDeployment(ctx, deploymentReq)
	if err != nil {
		return nil, fmt.Errorf("agentregistry create deployment: %w", err)
	}
	
	// 5. Publish NATS event (platform-specific side effect)
	event := models.NewEvent(
		"hub.platform.agent.deployed",
		"hub.platform.agents",
		map[string]interface{}{
			"tenant_id":       tenantID,
			"agent_id":        agentID,
			"deployment_id":   deployment.ID,
			"spoke_cluster_id": spokeClusterID,
			"agent_name":      agent.Name,
			"agent_version":   agent.Version,
		},
	)
	
	if err := s.eventBus.PublishAsync(ctx, event); err != nil {
		// Log error but don't fail the request (side effect)
		fmt.Printf("failed to publish agent.deployed event: %v\n", err)
	}
	
	// 6. Return MCP response with status "deploying"
	return map[string]interface{}{
		"deployment_id":    deployment.ID,
		"agent_id":         agentID,
		"status":           "deploying",
		"spoke_cluster_id": spokeClusterID,
		"deployed_at":      deployment.DeployedAt,
		"message":          "Agent deployment initiated. Use get_agent_status to poll for completion.",
	}, nil
}

