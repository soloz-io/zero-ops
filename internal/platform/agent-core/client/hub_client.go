package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// HubClient queries the Hub Centralised DB via PostgREST for agent infra status.
type HubClient struct {
	baseURL    string
	httpClient *http.Client
}

// HubConfig holds Hub PostgREST client configuration.
type HubConfig struct {
	BaseURL string
	Timeout time.Duration
}

// AgentInfraStatus is the infra-layer status written by Spoke Controller.
type AgentInfraStatus struct {
	DeploymentID   string    `json:"deployment_id"`
	TenantID       string    `json:"tenant_id"`
	AgentID        string    `json:"agent_id"`
	SpokeClusterID string    `json:"spoke_cluster_id"`
	Status         string    `json:"status"`
	Phase          string    `json:"phase,omitempty"`
	Replicas       int       `json:"replicas"`
	Message        string    `json:"message,omitempty"`
	Error          string    `json:"error,omitempty"`
	LastSyncAt     time.Time `json:"last_sync_at"`
}

// NewHubClient creates a new Hub PostgREST client.
func NewHubClient(cfg HubConfig) *HubClient {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &HubClient{
		baseURL: cfg.BaseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// GetAgentInfraStatus queries Hub Centralised DB for a deployment's infra status.
func (c *HubClient) GetAgentInfraStatus(ctx context.Context, deploymentID string) (*AgentInfraStatus, error) {
	url := fmt.Sprintf("%s/agent_infra_status?deployment_id=eq.%s&limit=1", c.baseURL, deploymentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hub get agent infra status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("hub get agent infra status: status %d", resp.StatusCode)
	}

	var results []AgentInfraStatus
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return &results[0], nil
}

// ListAgentDeployments queries Hub Centralised DB for all infra statuses for a tenant.
func (c *HubClient) ListAgentDeployments(ctx context.Context, tenantID string) ([]AgentInfraStatus, error) {
	url := fmt.Sprintf("%s/agent_infra_status?tenant_id=eq.%s", c.baseURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hub list agent deployments: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("hub list agent deployments: status %d", resp.StatusCode)
	}

	var results []AgentInfraStatus
	return results, json.NewDecoder(resp.Body).Decode(&results)
}
