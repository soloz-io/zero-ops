package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	opsv1alpha1 "github.com/soloz-io/zero-ops/operators/hub-operator/api/v1alpha1"
	"github.com/soloz-io/zero-ops/operators/hub-operator/internal/constant"
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
// Requirement 6.9: Read infisical-auth secret from platform-ops namespace
func NewInfisicalClient(ctx context.Context, k8sClient client.Client, baseURL string) (*InfisicalClient, error) {
	if baseURL == "" {
		// Use internal service URL instead of external HTTPS
		// This avoids TLS certificate verification issues during bootstrap
		// Service name follows Helm pattern: {release}-{chart}-{component}
		baseURL = "http://infisical-standalone-infisical.platform-security.svc:8080"
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

	// Requirement 6.9: Read infisical-auth secret from platform-ops namespace
	secret := &corev1.Secret{}
	if err := c.k8sClient.Get(ctx, client.ObjectKey{
		Name:      "infisical-auth",
		Namespace: constant.NamespaceOps,
	}, secret); err != nil {
		return fmt.Errorf("failed to get infisical-auth secret: %w", err)
	}

	clientID := string(secret.Data[constant.KeyClientID])
	clientSecret := string(secret.Data[constant.KeyClientSecret])

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

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+constant.APIEndpointUniversalAuthLogin, bytes.NewReader(body))
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
	url := fmt.Sprintf("%s%s/%s?workspaceId=%s&environment=%s&secretPath=%s",
		c.baseURL, constant.APIEndpointSecretsRaw, key, workspaceId, environmentSlug, secretPath)

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

// SecretExists checks if a secret exists in Infisical (public method for controllers)
func (c *InfisicalClient) SecretExists(ctx context.Context, projectSlug, environmentSlug, secretPath, key string) (bool, error) {
	// Ensure we have a valid token
	if err := c.ensureAuthenticated(ctx); err != nil {
		return false, fmt.Errorf("failed to authenticate: %w", err)
	}

	// Convert projectSlug to workspaceId
	workspaceId, err := c.getWorkspaceIdFromSlug(ctx, projectSlug)
	if err != nil {
		return false, fmt.Errorf("failed to get workspace ID: %w", err)
	}

	return c.secretExists(ctx, workspaceId, environmentSlug, secretPath, key)
}

// createSecret creates a new secret in Infisical using v3 API
func (c *InfisicalClient) createSecret(ctx context.Context, workspaceId, environmentSlug, secretPath, key, value string) error {
	logger := log.FromContext(ctx)

	createReq := map[string]interface{}{
		"workspaceId": workspaceId,
		"environment": environmentSlug,
		"secretPath":  secretPath,
		"secretName":  key,
		"secretValue": value,
		"type":        "shared",
	}

	logger.Info("Creating secret in Infisical",
		"workspaceId", workspaceId,
		"environment", environmentSlug,
		"secretPath", secretPath,
		"key", key,
		"valueLength", len(value))

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

	logger.Info("Secret created successfully in Infisical", "key", key, "status", resp.StatusCode)

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

// createFolder creates a folder hierarchy in Infisical to prevent 404 errors
// when later creating secrets inside non-existent paths.
func (c *InfisicalClient) createFolder(ctx context.Context, workspaceId, environmentSlug, folderPath string) error {
	logger := log.FromContext(ctx)

	parentPath := path.Dir(folderPath)
	if parentPath == "." {
		parentPath = "/"
	}
	folderName := path.Base(folderPath)
	if folderName == "." || folderName == "/" {
		return fmt.Errorf("invalid Infisical folder path: %s", folderPath)
	}

	folderReq := map[string]interface{}{
		"projectId":   workspaceId,
		"environment": environmentSlug,
		"path":        parentPath,
		"name":        folderName,
	}

	body, err := json.Marshal(folderReq)
	if err != nil {
		return fmt.Errorf("failed to marshal folder request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/v2/folders", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create folder request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute folder request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)

	// Requirement 6.7: Retry logic for 5xx errors
	if resp.StatusCode >= 500 {
		return fmt.Errorf("failed to create folder with status %d (transient error)", resp.StatusCode)
	}

	// 409 Conflict or 400 "already exists" — Infisical returns 400 (not 409) when a folder
	// with the same name already exists at the given path. Both are idempotent success.
	if resp.StatusCode == http.StatusConflict {
		logger.Info("Folder already exists in Infisical (409)", "folderPath", folderPath)
		return nil
	}

	if resp.StatusCode == http.StatusBadRequest {
		bodyStr := string(bodyBytes)
		if contains(bodyStr, "already exists") {
			logger.Info("Folder already exists in Infisical (400)", "folderPath", folderPath)
			return nil
		}
		return fmt.Errorf("failed to create folder with status 400: %s", bodyStr)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to create folder with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	logger.Info("Folder created successfully in Infisical", "folderPath", folderPath)
	return nil
}

// EnsureTenantFolder ensures the directory structure exists in Infisical before writing secrets.
// This prevents the "Folder ... not found" API rejection from Infisical V3 API.
// ADR-003: The folder hierarchy /spoke-pool/<cellId>/tenants/<tenantId> must exist before
// secrets can be placed inside it. The ESO Infisical provider splits remoteRef.key on the
// last '/' so the secret name is the final segment and the folder path is the prefix.
func (c *InfisicalClient) EnsureTenantFolder(ctx context.Context, projectSlug, environmentSlug, cellID, tenantID string) error {
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

	// Build folder hierarchy from root to leaf. ESO remoteRef.key uses the last path
	// segment as the secret name and the prefix as the folder path, so the folder
	// hierarchy need only go to /spoke-pool/<cellId>/tenants/<tenantId>.
	// ADR-003: path pattern /spoke-pool/<cellId>/tenants/<tenantId>
	foldersToEnsure := []string{
		"/spoke-pool",
		fmt.Sprintf("/spoke-pool/%s", cellID),
		fmt.Sprintf("/spoke-pool/%s/tenants", cellID),
		fmt.Sprintf("/spoke-pool/%s/tenants/%s", cellID, tenantID),
	}

	for _, folder := range foldersToEnsure {
		if err := c.createFolder(ctx, workspaceId, environmentSlug, folder); err != nil {
			logger.Error(err, "Failed to ensure Infisical folder", "folder", folder)
			return fmt.Errorf("failed to ensure Infisical folder %s: %w", folder, err)
		}
	}

	logger.Info("Tenant folder hierarchy ensured in Infisical", "cellID", cellID, "tenantID", tenantID)
	return nil
}

// EnsurePKITemplate idempotently ensures a PKI template exists in Infisical.
func (c *InfisicalClient) EnsurePKITemplate(ctx context.Context, projectSlug, caName, templateName string, ttlDays int) error {
	logger := log.FromContext(ctx)

	var lastErr error
	err := wait.PollImmediateWithContext(ctx, 5*time.Second, 2*time.Minute, func(ctx context.Context) (bool, error) {
		if err := c.ensureAuthenticated(ctx); err != nil {
			lastErr = fmt.Errorf("failed to authenticate: %w", err)
			return false, nil // retriable
		}

		workspaceId, err := c.getWorkspaceIdFromSlug(ctx, projectSlug)
		if err != nil {
			lastErr = fmt.Errorf("failed to get workspace ID: %w", err)
			return false, nil // retriable
		}

		exists, err := c.verifyPKITemplateExists(ctx, workspaceId, templateName)
		if err != nil {
			lastErr = fmt.Errorf("failed to check if PKI template exists: %w", err)
			return false, nil // retriable
		}
		if exists {
			logger.Info("PKI template already exists", "templateName", templateName)
			return true, nil
		}

		if err := c.createPKITemplate(ctx, workspaceId, caName, templateName, ttlDays); err != nil {
			lastErr = fmt.Errorf("failed to create PKI template: %w", err)
			return false, nil // retriable
		}

		logger.Info("Successfully created PKI template", "templateName", templateName)
		return true, nil
	})

	if err != nil {
		if err == context.DeadlineExceeded || err == wait.ErrWaitTimeout {
			return fmt.Errorf("timeout ensuring PKI template %s: %w", templateName, lastErr)
		}
		return err
	}
	return nil
}

func (c *InfisicalClient) verifyPKITemplateExists(ctx context.Context, workspaceId, templateName string) (bool, error) {
	url := fmt.Sprintf("%s%s/%s?projectId=%s", c.baseURL, constant.APIEndpointPKITemplates, templateName, workspaceId)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result struct {
		CertificateTemplate struct {
			ID string `json:"id"`
		} `json:"certificateTemplate"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}
	return result.CertificateTemplate.ID != "", nil
}

func (c *InfisicalClient) createPKITemplate(ctx context.Context, workspaceId, caName, templateName string, ttlDays int) error {
	createReq := map[string]interface{}{
		"projectId":              workspaceId,
		"caName":                 caName,
		"name":                   templateName,
		"commonName":             ".*",
		"subjectAlternativeName": ".*",
		"ttl":                    fmt.Sprintf("%dh", ttlDays*24),
		"keyUsages":              []string{"digitalSignature", "keyEncipherment"},
		"extendedKeyUsages":      []string{"serverAuth", "clientAuth"},
	}
	bodyData, err := json.Marshal(createReq)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s%s", c.baseURL, constant.APIEndpointPKITemplates)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyData))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		return nil
	}

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		CertificateTemplate struct {
			ID string `json:"id"`
		} `json:"certificateTemplate"`
	}
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return fmt.Errorf("failed to parse create template response: %w", err)
	}
	if result.CertificateTemplate.ID == "" {
		return fmt.Errorf("API response returned empty ID: %s", string(bodyBytes))
	}

	return nil
}
