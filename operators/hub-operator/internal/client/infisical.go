package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	opsv1alpha1 "github.com/soloz-io/zero-ops/operators/hub-operator/api/v1alpha1"
)

// InfisicalClient wraps Infisical API operations with Universal Auth
type InfisicalClient struct {
	baseURL    string
	httpClient *http.Client
	token      string
	tokenExp   time.Time
	k8sClient  client.Client
}

// NewInfisicalClient creates a new Infisical API client
// Requirement 6.3: Use controller-runtime client
// Requirement 6.9: Read infisical-auth secret from hub-platform-ops namespace
func NewInfisicalClient(ctx context.Context, k8sClient client.Client, baseURL string) (*InfisicalClient, error) {
	if baseURL == "" {
		// Use internal service URL instead of external HTTPS
		// This avoids TLS certificate verification issues during bootstrap
		// Service name follows Helm pattern: {release}-{chart}-{component}
		baseURL = "http://platform-infisical-infisical-standalone-infisical.hub-platform-security.svc:8080"
	}

	return &InfisicalClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second, // Requirement 6.8: 30 second timeout
		},
		k8sClient: k8sClient,
	}, nil
}

// authenticate performs Universal Auth login to get access token
// Requirement 6.4: Implement authenticate() using Universal Auth
func (c *InfisicalClient) authenticate(ctx context.Context) error {
	logger := log.FromContext(ctx)

	// Requirement 6.9: Read infisical-auth secret from hub-platform-ops namespace
	secret := &corev1.Secret{}
	if err := c.k8sClient.Get(ctx, client.ObjectKey{
		Name:      "infisical-auth",
		Namespace: "hub-platform-ops",
	}, secret); err != nil {
		return fmt.Errorf("failed to get infisical-auth secret: %w", err)
	}

	clientID := string(secret.Data["client-id"])
	clientSecret := string(secret.Data["client-secret"])

	if clientID == "" || clientSecret == "" {
		return fmt.Errorf("infisical-auth secret missing client-id or client-secret")
	}

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

	// Requirement 6.7: Retry logic for 5xx errors
	if resp.StatusCode >= 500 {
		return fmt.Errorf("infisical authentication failed with status %d (transient error)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("authentication failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var loginResp struct {
		AccessToken string `json:"accessToken"`
		ExpiresIn   int    `json:"expiresIn"` // seconds
	}

	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return fmt.Errorf("failed to decode login response: %w", err)
	}

	// Requirement 6.5: Token caching and refresh logic
	c.token = loginResp.AccessToken
	c.tokenExp = time.Now().Add(time.Duration(loginResp.ExpiresIn) * time.Second)

	logger.Info("Authenticated with Infisical", "expiresIn", loginResp.ExpiresIn)
	return nil
}

// ensureAuthenticated checks token validity and refreshes if needed
// Requirement 6.5: Token refresh before expiration
func (c *InfisicalClient) ensureAuthenticated(ctx context.Context) error {
	// Refresh token if expired or expiring within 5 minutes
	if c.token == "" || time.Now().Add(5*time.Minute).After(c.tokenExp) {
		return c.authenticate(ctx)
	}
	return nil
}

// CreateOrUpdateSecret creates or updates a secret in Infisical
// Requirement 6.6: Implement CreateOrUpdateSecret() for Infisical API
func (c *InfisicalClient) CreateOrUpdateSecret(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment, key, value string) error {
	logger := log.FromContext(ctx)

	// Ensure we have a valid token
	if err := c.ensureAuthenticated(ctx); err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}

	projectSlug := hubEnv.Spec.Secrets.Infisical.ProjectSlug
	environmentSlug := hubEnv.Spec.Secrets.Infisical.EnvironmentSlug
	secretPath := "/" // Root path

	// Convert projectSlug to workspaceId
	workspaceId, err := c.getWorkspaceIdFromSlug(ctx, projectSlug)
	if err != nil {
		return fmt.Errorf("failed to get workspace ID: %w", err)
	}

	// Check if secret exists
	exists, err := c.secretExists(ctx, workspaceId, environmentSlug, secretPath, key)
	if err != nil {
		return fmt.Errorf("failed to check if secret exists: %w", err)
	}

	if exists {
		logger.Info("Updating existing secret in Infisical", "key", key)
		return c.updateSecret(ctx, workspaceId, environmentSlug, secretPath, key, value)
	}

	logger.Info("Creating new secret in Infisical", "key", key)
	return c.createSecret(ctx, workspaceId, environmentSlug, secretPath, key, value)
}

// CreateOrUpdateSecretRaw creates or updates a secret using raw project/environment parameters
// Used by bootstrap to upload CLI secrets before HubEnvironment CR exists
func (c *InfisicalClient) CreateOrUpdateSecretRaw(ctx context.Context, projectSlug, environmentSlug, secretPath, key, value string) error {
	logger := log.FromContext(ctx)

	// Ensure we have a valid token
	if err := c.ensureAuthenticated(ctx); err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}

	// Convert projectSlug to workspaceId
	workspaceId, err := c.getWorkspaceIdFromSlug(ctx, projectSlug)
	if err != nil {
		return fmt.Errorf("failed to get workspace ID: %w", err)
	}

	// Check if secret exists
	exists, err := c.secretExists(ctx, workspaceId, environmentSlug, secretPath, key)
	if err != nil {
		return fmt.Errorf("failed to check if secret exists: %w", err)
	}

	if exists {
		logger.Info("Updating existing secret in Infisical", "key", key)
		return c.updateSecret(ctx, workspaceId, environmentSlug, secretPath, key, value)
	}

	logger.Info("Creating new secret in Infisical", "key", key)
	return c.createSecret(ctx, workspaceId, environmentSlug, secretPath, key, value)
}

// getWorkspaceIdFromSlug converts projectSlug to workspaceId
func (c *InfisicalClient) getWorkspaceIdFromSlug(ctx context.Context, projectSlug string) (string, error) {
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

	// Requirement 6.7: Retry logic for 5xx errors
	if resp.StatusCode >= 500 {
		return "", fmt.Errorf("failed to list workspaces with status %d (transient error)", resp.StatusCode)
	}

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

// secretExists checks if a secret already exists
func (c *InfisicalClient) secretExists(ctx context.Context, workspaceId, environmentSlug, secretPath, key string) (bool, error) {
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

	// Requirement 6.7: Retry logic for 5xx errors
	if resp.StatusCode >= 500 {
		return false, fmt.Errorf("failed to check secret existence with status %d (transient error)", resp.StatusCode)
	}

	if resp.StatusCode == http.StatusOK {
		return true, nil
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	return false, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(bodyBytes))
}

// createSecret creates a new secret in Infisical using v3 API
func (c *InfisicalClient) createSecret(ctx context.Context, workspaceId, environmentSlug, secretPath, key, value string) error {
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

	// Requirement 6.7: Retry logic for 5xx errors
	if resp.StatusCode >= 500 {
		return fmt.Errorf("failed to create secret with status %d (transient error)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create secret with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// updateSecret updates an existing secret in Infisical using v3 API
func (c *InfisicalClient) updateSecret(ctx context.Context, workspaceId, environmentSlug, secretPath, key, value string) error {
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

	// Requirement 6.7: Retry logic for 5xx errors
	if resp.StatusCode >= 500 {
		return fmt.Errorf("failed to update secret with status %d (transient error)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update secret with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}
