package service

import (
	"context"
	"fmt"

	"github.com/soloz-io/zero-ops/internal/agent-core/client"
)

// StatusService orchestrates agent status queries
type StatusService struct {
	agentRegistryClient *client.AgentRegistryClient
	hubClient           *client.HubClient
}

// StatusConfig holds status service dependencies
type StatusConfig struct {
	AgentRegistryClient *client.AgentRegistryClient
	HubClient           *client.HubClient
}

// NewStatusService creates a new status service
func NewStatusService(cfg StatusConfig) *StatusService {
	return &StatusService{
		agentRegistryClient: cfg.AgentRegistryClient,
		hubClient:           cfg.HubClient,
	}
}

// GetAgentStatus orchestrates agent status polling from Hub Centralised DB
func (s *StatusService) GetAgentStatus(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// 1. Extract tenant context from JWT
	tenantID, ok := ctx.Value("tenant_id").(string)
	if !ok || tenantID == "" {
		return nil, fmt.Errorf("tenant_id not found in context")
	}
	
	// 2. Extract and validate parameters
	agentID, ok := params["agent_id"].(string)
	if !ok || agentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}
	
	// 3. Get agent from AgentRegistry to validate existence and get basic info
	agentName, agentVersion := parseAgentID(agentID)
	agent, err := s.agentRegistryClient.GetAgent(ctx, agentName, agentVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch agent: %w", err)
	}
	
	// 4. Get deployments from AgentRegistry
	deployments, err := s.agentRegistryClient.ListDeployments(ctx, &client.DeploymentFilter{
		ResourceName: agentName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch deployments: %w", err)
	}
	
	// 5. Build response with deployment status
	response := map[string]interface{}{
		"agent_id":   agentID,
		"name":       agent.Name,
		"version":    agent.Version,
		"status":     "registered",
		"created_at": agent.CreatedAt,
	}
	
	// 6. Enrich with deployment information if available
	if len(deployments) > 0 {
		deployment := deployments[0] // Get latest deployment
		
		response["deployment_id"] = deployment.ID
		response["deployment_status"] = deployment.Status
		response["deployed_at"] = deployment.DeployedAt
		
		// 7. Query Hub Centralised DB for infra status
		infraStatus, err := s.hubClient.GetAgentInfraStatus(ctx, deployment.ID)
		if err != nil {
			// Log error but continue (infra status is optional enrichment)
			fmt.Printf("failed to fetch infra status: %v\n", err)
		} else if infraStatus != nil {
			// Map infra status to deployment status transitions
			response["infra_status"] = infraStatus.Status
			response["phase"] = infraStatus.Phase
			response["replicas"] = infraStatus.Replicas
			response["message"] = infraStatus.Message
			response["last_sync_at"] = infraStatus.LastSyncAt
			
			// Override deployment status with more detailed infra status if available
			if infraStatus.Status == "ready" && deployment.Status == "deployed" {
				response["status"] = "ready"
			} else if infraStatus.Status == "failed" {
				response["status"] = "failed"
				response["error"] = infraStatus.Error
			} else if infraStatus.Status == "provisioning" {
				response["status"] = "provisioning"
			}
		}
	} else {
		// No deployments found
		response["deployments"] = []interface{}{}
	}
	
	return response, nil
}