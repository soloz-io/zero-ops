package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2/clientcredentials"
)

// Client handles communication with Hub Centralised DB via PostgREST
type Client struct {
	baseURL    string
	httpClient *http.Client
	oauth2     *clientcredentials.Config
}

// Config holds Hub client configuration
type Config struct {
	BaseURL      string
	ClientID     string
	ClientSecret string
	TokenURL     string
	Timeout      time.Duration
}

// AgentInfraStatus represents the payload for Hub DB
type AgentInfraStatus struct {
	TenantID       string    `json:"tenant_id"`
	AgentID        string    `json:"agent_id"`
	DeploymentID   string    `json:"deployment_id"`
	SpokeClusterID string    `json:"spoke_cluster_id"`
	Status         string    `json:"status"`
	Phase          string    `json:"phase"`
	Replicas       int32     `json:"replicas"`
	Message        string    `json:"message"`
	Error          string    `json:"error,omitempty"`
	LastSyncAt     time.Time `json:"last_sync_at"`
}

// NewClient creates a new Hub PostgREST client
func NewClient(cfg Config) *Client {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}

	oauth2Config := &clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
	}

	return &Client{
		baseURL: cfg.BaseURL,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		oauth2: oauth2Config,
	}
}

// PostAgentInfraStatus writes agent infrastructure status to Hub Centralised DB
func (c *Client) PostAgentInfraStatus(ctx context.Context, status AgentInfraStatus) error {
	// Get OAuth2 token using client credentials flow
	token, err := c.oauth2.Token(ctx)
	if err != nil {
		return fmt.Errorf("failed to get OAuth2 token: %w", err)
	}

	// Marshal payload to JSON
	payload, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("failed to marshal status payload: %w", err)
	}

	// Create HTTP request
	url := fmt.Sprintf("%s/agent_infra_status", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.AccessToken))
	req.Header.Set("Prefer", "return=minimal") // PostgREST preference

	// Execute request with retry logic
	return c.executeWithRetry(ctx, req, 3)
}

// executeWithRetry executes HTTP request with exponential backoff retry
func (c *Client) executeWithRetry(ctx context.Context, req *http.Request, maxRetries int) error {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("HTTP request failed (attempt %d): %w", attempt+1, err)
			continue
		}

		// Check response status
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			resp.Body.Close()
			return nil // Success
		}

		// Read error response
		var errorBody bytes.Buffer
		errorBody.ReadFrom(resp.Body)
		resp.Body.Close()

		lastErr = fmt.Errorf("HTTP %d (attempt %d): %s", resp.StatusCode, attempt+1, errorBody.String())

		// Don't retry on client errors (4xx)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			break
		}
	}

	return fmt.Errorf("failed after %d attempts: %w", maxRetries+1, lastErr)
}