package infisical

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	
	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
)

// Client wraps Infisical API operations
type Client struct {
	baseURL    string
	httpClient *http.Client
	token      string
}

// Config holds Infisical connection configuration
type Config struct {
	BaseURL        string
	ProjectSlug    string
	EnvironmentSlug string
}

// NewClient creates a new Infisical API client
// It retrieves the Infisical URL and authenticates using Universal Auth
// Reuses the same credentials that ESO uses (infisical-auth in external-secrets-system)
func NewClient(ctx context.Context, clientset *kubernetes.Clientset) (*Client, error) {
	// Allow override for local port-forwarding or custom DNS
	baseURL := os.Getenv("INFISICAL_API_URL")
	if baseURL == "" {
		baseURL = "https://infisical.nutgraf.in"
	}
	
	// Get Universal Auth credentials from the same secret ESO uses
	esoNamespace := constants.NamespaceOps
	secret, err := clientset.CoreV1().Secrets(esoNamespace).Get(ctx, "infisical-auth", metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get infisical-auth secret: %w (run 'hub init-secrets' first)", err)
	}

	clientID := string(secret.Data["client-id"])
	clientSecret := string(secret.Data["client-secret"])

	if clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("infisical-auth secret missing client-id or client-secret (run 'hub init-secrets' first)")
	}

	client := &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				// Ignore TLS verification for Day-0 bootstrap when certificates are still provisioning
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}

	// Authenticate and get access token
	if err := client.authenticate(ctx, clientID, clientSecret); err != nil {
		return nil, fmt.Errorf("failed to authenticate with Infisical: %w", err)
	}

	return client, nil
}

// getWorkspaceIdFromSlug converts projectSlug to workspaceId by querying Infisical API
func (c *Client) getWorkspaceIdFromSlug(ctx context.Context, projectSlug string) (string, error) {
	// List all workspaces and find the one matching the slug
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/v1/workspace", nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to list workspaces with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var workspacesResp struct {
		Workspaces []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"workspaces"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&workspacesResp); err != nil {
		return "", fmt.Errorf("failed to decode workspaces response: %w", err)
	}

	for _, ws := range workspacesResp.Workspaces {
		if ws.Slug == projectSlug {
			return ws.ID, nil
		}
	}

	return "", fmt.Errorf("workspace with slug '%s' not found", projectSlug)
}

// authenticate performs Universal Auth login to get access token
func (c *Client) authenticate(ctx context.Context, clientID, clientSecret string) error {
	loginReq := map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
	}

	body, err := json.Marshal(loginReq)
	if err != nil {
		return fmt.Errorf("failed to marshal login request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/v1/auth/universal-auth/login", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("authentication failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var loginResp struct {
		AccessToken string `json:"accessToken"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return fmt.Errorf("failed to decode login response: %w", err)
	}

	c.token = loginResp.AccessToken
	return nil
}

// CreateOrUpdateSecret creates or updates a secret in Infisical
func (c *Client) CreateOrUpdateSecret(ctx context.Context, projectSlug, environmentSlug, secretPath, key, value string) error {
	// Convert projectSlug to workspaceId (required for v3 write operations)
	workspaceId, err := c.getWorkspaceIdFromSlug(ctx, projectSlug)
	if err != nil {
		return fmt.Errorf("failed to get workspace ID: %w", err)
	}

	// First, try to get the secret to see if it exists
	exists, err := c.secretExists(ctx, workspaceId, environmentSlug, secretPath, key)
	if err != nil {
		return fmt.Errorf("failed to check if secret exists: %w", err)
	}

	if exists {
		return c.updateSecret(ctx, workspaceId, environmentSlug, secretPath, key, value)
	}

	return c.createSecret(ctx, workspaceId, environmentSlug, secretPath, key, value)
}

// secretExists checks if a secret already exists
func (c *Client) secretExists(ctx context.Context, workspaceId, environmentSlug, secretPath, key string) (bool, error) {
	url := fmt.Sprintf("%s/api/v3/secrets/raw/%s?workspaceId=%s&environment=%s&secretPath=%s",
		c.baseURL, key, workspaceId, environmentSlug, secretPath)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}

	if resp.StatusCode == http.StatusOK {
		return true, nil
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	return false, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(bodyBytes))
}

// createSecret creates a new secret in Infisical using v3 API with workspaceId
func (c *Client) createSecret(ctx context.Context, workspaceId, environmentSlug, secretPath, key, value string) error {
	createReq := map[string]interface{}{
		"workspaceId": workspaceId,
		"environment": environmentSlug,
		"secretPath":  secretPath,
		"secretName":  key,
		"secretValue": value,
		"type":        "shared",
	}

	body, err := json.Marshal(createReq)
	if err != nil {
		return fmt.Errorf("failed to marshal create request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/v3/secrets/raw/"+key, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create secret with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// updateSecret updates an existing secret in Infisical using v3 API with workspaceId
func (c *Client) updateSecret(ctx context.Context, workspaceId, environmentSlug, secretPath, key, value string) error {
	updateReq := map[string]interface{}{
		"workspaceId": workspaceId,
		"environment": environmentSlug,
		"secretPath":  secretPath,
		"secretValue": value,
		"type":        "shared",
	}

	body, err := json.Marshal(updateReq)
	if err != nil {
		return fmt.Errorf("failed to marshal update request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PATCH", c.baseURL+"/api/v3/secrets/raw/"+key, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update secret with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// GetInfisicalConfig retrieves Infisical configuration from ClusterSecretStore
func GetInfisicalConfig(ctx context.Context, clientset *kubernetes.Clientset) (*Config, error) {
	// Look for the correct ConfigMap in the correct namespace
	cm, err := clientset.CoreV1().ConfigMaps(constants.NamespaceOps).Get(ctx, "hub-bootstrap-config", metav1.GetOptions{})
	if err != nil {
		// Fallback to the correct hardcoded values from cluster-secret-store.yaml
		return &Config{
			ProjectSlug:     "hub-platform",
			EnvironmentSlug: "dev",
		}, nil
	}

	// Use the correct keys from hub-bootstrap-config
	projectSlug := cm.Data["INFISICAL_PROJECT_SLUG"]
	environmentSlug := cm.Data["INFISICAL_ENVIRONMENT_SLUG"]

	if projectSlug == "" || environmentSlug == "" {
		return &Config{
			ProjectSlug:     "hub-platform",
			EnvironmentSlug: "dev",
		}, nil
	}

	return &Config{
		ProjectSlug:     projectSlug,
		EnvironmentSlug: environmentSlug,
	}, nil
}
