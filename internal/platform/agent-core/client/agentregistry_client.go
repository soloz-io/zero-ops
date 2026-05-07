package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// AgentRegistryClient is an HTTP client for the AgentRegistry OSS API (CRUD only).
type AgentRegistryClient struct {
	baseURL    string
	httpClient *http.Client
}

// Config holds AgentRegistry client configuration.
type Config struct {
	BaseURL string
	Timeout time.Duration
}

// AgentRequest maps to AgentRegistry POST /v0/agents body.
// Fields match agentregistry.agent_definitions columns per requirements.md FR-1.
type AgentRequest struct {
	Name              string                 `json:"name"`
	Version           string                 `json:"version"`
	AgentType         string                 `json:"agentType"`
	SystemMessage     string                 `json:"systemMessage"`
	ToolAccess        []string               `json:"toolAccess"`
	MemoryConfig      map[string]interface{} `json:"memoryConfig"`
	GuardrailPolicies map[string]interface{} `json:"guardrailPolicies"`
	ModelConfig       map[string]interface{} `json:"modelConfig"`
}

// Agent is the AgentRegistry agent record.
type Agent struct {
	ID                string                 `json:"id"`
	Name              string                 `json:"name"`
	Version           string                 `json:"version"`
	AgentType         string                 `json:"agentType"`
	SystemMessage     string                 `json:"systemMessage"`
	ToolAccess        []string               `json:"toolAccess"`
	MemoryConfig      map[string]interface{} `json:"memoryConfig"`
	GuardrailPolicies map[string]interface{} `json:"guardrailPolicies"`
	ModelConfig       map[string]interface{} `json:"modelConfig"`
	CreatedAt         time.Time              `json:"createdAt"`
	UpdatedAt         time.Time              `json:"updatedAt"`
}

// AgentResponse wraps the AgentRegistry agent response.
type AgentResponse struct {
	Agent Agent `json:"agent"`
}

// DeploymentRequest maps to AgentRegistry POST /v0/deployments body.
// Fields match agentregistry.deployments columns per requirements.md FR-2.
type DeploymentRequest struct {
	AgentID    string `json:"agentId"`
	ProviderID string `json:"providerId"`
}

// Deployment is the AgentRegistry deployment record.
// Fields match agentregistry.deployments columns per requirements.md FR-2.
type Deployment struct {
	ID               string                 `json:"id"`
	TenantID         string                 `json:"tenantId"`
	AgentID          string                 `json:"agentId"`
	ProviderID       string                 `json:"providerId"`
	Status           string                 `json:"status"`
	ProviderMetadata map[string]interface{} `json:"providerMetadata,omitempty"`
	Error            string                 `json:"error,omitempty"`
	DeployedAt       time.Time              `json:"deployedAt"`
	UpdatedAt        time.Time              `json:"updatedAt"`
}

// DeploymentsMeta is the _meta["aregistry.ai/deployments"] payload.
type DeploymentsMeta struct {
	Deployments []DeploymentSummary `json:"deployments"`
	Count       int                 `json:"count"`
}

// DeploymentSummary is a compact deployment view embedded in agent metadata.
type DeploymentSummary struct {
	ID         string    `json:"id"`
	ProviderID string    `json:"providerId,omitempty"`
	Status     string    `json:"status"`
	Version    string    `json:"version,omitempty"`
	DeployedAt time.Time `json:"deployedAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// AgentWithMeta wraps agent + deployment metadata from list response.
type AgentWithMeta struct {
	Agent Agent
	Meta  struct {
		CreatedAt   time.Time        `json:"createdAt"`
		UpdatedAt   time.Time        `json:"updatedAt"`
		Deployments *DeploymentsMeta `json:"deployments,omitempty"`
	}
}

// ListAgentsRequest holds optional filters for listing agents.
type ListAgentsRequest struct {
	Limit int
}

// ListAgentsResponse wraps the AgentRegistry list response.
type ListAgentsResponse struct {
	Agents []AgentWithMeta
	Total  int
}

// DeploymentFilter holds optional filters for listing deployments.
type DeploymentFilter struct {
	ResourceType string
	ResourceName string
	ProviderID   string
	Status       string
}

// NewAgentRegistryClient creates a new AgentRegistry HTTP client.
func NewAgentRegistryClient(cfg Config) *AgentRegistryClient {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &AgentRegistryClient{
		baseURL: cfg.BaseURL,
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

func (c *AgentRegistryClient) do(ctx context.Context, method, path string, body interface{}) (*http.Response, error) {
	var buf *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		buf = bytes.NewBuffer(b)
	} else {
		buf = bytes.NewBuffer(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, buf)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.httpClient.Do(req)
}

// CreateAgent calls POST /v0/agents (upsert).
func (c *AgentRegistryClient) CreateAgent(ctx context.Context, req *AgentRequest) (*AgentResponse, error) {
	resp, err := c.do(ctx, http.MethodPost, "/v0/agents", req)
	if err != nil {
		return nil, fmt.Errorf("agentregistry create agent: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("agentregistry create agent: status %d", resp.StatusCode)
	}
	var result AgentResponse
	return &result, json.NewDecoder(resp.Body).Decode(&result)
}

// GetAgent calls GET /v0/agents/{name}/versions/{version}.
func (c *AgentRegistryClient) GetAgent(ctx context.Context, name, version string) (*Agent, error) {
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v0/agents/%s/versions/%s", name, version), nil)
	if err != nil {
		return nil, fmt.Errorf("agentregistry get agent: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("agent_not_found")
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("agentregistry get agent: status %d", resp.StatusCode)
	}
	var result AgentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result.Agent, nil
}

// ListAgents calls GET /v0/agents.
func (c *AgentRegistryClient) ListAgents(ctx context.Context, req *ListAgentsRequest) (*ListAgentsResponse, error) {
	path := "/v0/agents"
	if req != nil && req.Limit > 0 {
		path = fmt.Sprintf("%s?limit=%d", path, req.Limit)
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("agentregistry list agents: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("agentregistry list agents: status %d", resp.StatusCode)
	}
	// AgentRegistry returns array of agents with _meta
	var raw []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	result := &ListAgentsResponse{Total: len(raw)}
	for _, r := range raw {
		var item AgentWithMeta
		if err := json.Unmarshal(r, &item.Agent); err != nil {
			continue
		}
		result.Agents = append(result.Agents, item)
	}
	return result, nil
}

// DeleteAgent calls DELETE /v0/agents/{name}/versions/{version}.
func (c *AgentRegistryClient) DeleteAgent(ctx context.Context, name, version string) error {
	resp, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/v0/agents/%s/versions/%s", name, version), nil)
	if err != nil {
		return fmt.Errorf("agentregistry delete agent: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil // idempotent
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("agentregistry delete agent: status %d", resp.StatusCode)
	}
	return nil
}

// CreateDeployment calls POST /v0/deployments.
func (c *AgentRegistryClient) CreateDeployment(ctx context.Context, req *DeploymentRequest) (*Deployment, error) {
	resp, err := c.do(ctx, http.MethodPost, "/v0/deployments", req)
	if err != nil {
		return nil, fmt.Errorf("agentregistry create deployment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("agentregistry create deployment: status %d", resp.StatusCode)
	}
	var result Deployment
	return &result, json.NewDecoder(resp.Body).Decode(&result)
}

// GetDeployment calls GET /v0/deployments/{id}.
func (c *AgentRegistryClient) GetDeployment(ctx context.Context, id string) (*Deployment, error) {
	resp, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v0/deployments/%s", id), nil)
	if err != nil {
		return nil, fmt.Errorf("agentregistry get deployment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("deployment_not_found")
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("agentregistry get deployment: status %d", resp.StatusCode)
	}
	var result Deployment
	return &result, json.NewDecoder(resp.Body).Decode(&result)
}

// ListDeployments calls GET /v0/deployments with optional filters.
func (c *AgentRegistryClient) ListDeployments(ctx context.Context, filter *DeploymentFilter) ([]Deployment, error) {
	path := "/v0/deployments?"
	if filter != nil {
		if filter.ResourceType != "" {
			path += "resourceType=" + filter.ResourceType + "&"
		}
		if filter.ResourceName != "" {
			path += "serverName=" + filter.ResourceName + "&"
		}
		if filter.ProviderID != "" {
			path += "providerId=" + filter.ProviderID + "&"
		}
		if filter.Status != "" {
			path += "status=" + filter.Status + "&"
		}
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("agentregistry list deployments: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("agentregistry list deployments: status %d", resp.StatusCode)
	}
	var result []Deployment
	return result, json.NewDecoder(resp.Body).Decode(&result)
}

// DeleteDeployment calls DELETE /v0/deployments/{id}.
func (c *AgentRegistryClient) DeleteDeployment(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/v0/deployments/%s", id), nil)
	if err != nil {
		return fmt.Errorf("agentregistry delete deployment: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil // idempotent
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("agentregistry delete deployment: status %d", resp.StatusCode)
	}
	return nil
}
// DeploymentUpdateRequest represents a deployment status update
type DeploymentUpdateRequest struct {
	Status string `json:"status"`
}

// UpdateDeployment updates deployment status via PATCH /v0/deployments/{id}
func (c *AgentRegistryClient) UpdateDeployment(ctx context.Context, deploymentID string, req *DeploymentUpdateRequest) error {
	path := fmt.Sprintf("/v0/deployments/%s", deploymentID)
	
	resp, err := c.do(ctx, "PATCH", path, req)
	if err != nil {
		return fmt.Errorf("failed to update deployment: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("AgentRegistry API error %d: %s", resp.StatusCode, string(body))
	}
	
	return nil
}