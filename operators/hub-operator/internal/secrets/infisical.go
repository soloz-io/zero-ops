package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// Enterprise Alignment: ADR-003 / ADR-031 Isolation Boundaries
	InfisicalSharedPathFormat = "/spoke-pool/%s/shared"
	InfisicalTenantPathFormat = "/spoke-pool/%s/tenants/%s"
)

// InfisicalClient is a lightweight HTTP client for ADR-031 topology management.
// Uses Machine Identity credentials (client-id + client-secret) for folder/secret
// operations. EnvironmentSlug defaults to "dev" but is configurable via env var.
type InfisicalClient struct {
	BaseURL         string
	ClientID        string
	ClientSecret    string
	ProjectID       string
	EnvironmentSlug string

	httpClient  *http.Client
	token       string
	tokenExp    time.Time
}

// NewInfisicalClient creates a new InfisicalClient from env-provided credentials.
func NewInfisicalClient(baseURL, clientID, clientSecret, projectID string) *InfisicalClient {
	if baseURL == "" {
		baseURL = "http://platform-infisical-infisical-standalone-infisical.platform-security.svc:8080"
	}
	return &InfisicalClient{
		BaseURL:         baseURL,
		ClientID:        clientID,
		ClientSecret:    clientSecret,
		ProjectID:       projectID,
		EnvironmentSlug: "dev",
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// authenticate performs Universal Auth login to get an access token.
func (c *InfisicalClient) authenticate(ctx context.Context) error {
	loginReq := map[string]string{
		"clientId":     c.ClientID,
		"clientSecret": c.ClientSecret,
	}

	body, err := json.Marshal(loginReq)
	if err != nil {
		return fmt.Errorf("failed to marshal login request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/api/v1/auth/universal-auth/login", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return fmt.Errorf("infisical authentication failed with status %d (transient)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("authentication failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var loginResp struct {
		AccessToken string `json:"accessToken"`
		ExpiresIn   int    `json:"expiresIn"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return fmt.Errorf("failed to decode login response: %w", err)
	}

	c.token = loginResp.AccessToken
	c.tokenExp = time.Now().Add(time.Duration(loginResp.ExpiresIn) * time.Second)
	return nil
}

// ensureAuthenticated checks token validity and refreshes if needed.
func (c *InfisicalClient) ensureAuthenticated(ctx context.Context) error {
	if c.token == "" || time.Now().Add(5*time.Minute).After(c.tokenExp) {
		return c.authenticate(ctx)
	}
	return nil
}

// newAuthenticatedRequest creates an HTTP request with the auth bearer token set.
func (c *InfisicalClient) newAuthenticatedRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	if err := c.ensureAuthenticated(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	return req, nil
}

// ============================================================================
// FOLDER OPERATIONS
// ============================================================================

// EnsureFolder creates a folder hierarchy in Infisical.
// Multi-segment paths (e.g. /spoke-pool/<cellId>/tenants/<tenantId>) are
// created one segment at a time. Idempotent: 400 "already exists" is treated
// as success.
func (c *InfisicalClient) EnsureFolder(ctx context.Context, folderPath string) error {
	if folderPath == "" || folderPath == "/" {
		return nil
	}

	// Build the hierarchy from root to leaf
	segments := strings.Split(strings.Trim(folderPath, "/"), "/")
	accumulated := ""
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		accumulated = path.Join(accumulated, segment)
		parentPath := path.Dir(accumulated)
		if parentPath == "." {
			parentPath = "/"
		}

		folderReq := map[string]interface{}{
			"projectId":   c.ProjectID,
			"environment": c.EnvironmentSlug,
			"path":        parentPath,
			"name":        segment,
		}
		body, err := json.Marshal(folderReq)
		if err != nil {
			return fmt.Errorf("failed to marshal folder request for %s: %w", segment, err)
		}

		req, err := c.newAuthenticatedRequest(ctx, "POST", c.BaseURL+"/api/v2/folders", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("failed to create request for folder %s: %w", segment, err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("failed to execute folder request for %s: %w", segment, err)
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == http.StatusConflict || (resp.StatusCode == http.StatusBadRequest && bytes.Contains(respBody, []byte("already exists"))) {
			// Idempotent: folder already exists
			continue
		}
		if resp.StatusCode >= 500 {
			return fmt.Errorf("failed to create folder %s with status %d (transient)", segment, resp.StatusCode)
		}
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			return fmt.Errorf("failed to create folder %s with status %d: %s", segment, resp.StatusCode, string(respBody))
		}
	}

	return nil
}

// ============================================================================
// SECRET OPERATIONS
// ============================================================================

// GetSecret retrieves a secret's value from Infisical.
// Returns the secret value and nil on success.
// Returns an error if the secret doesn't exist or can't be fetched.
func (c *InfisicalClient) GetSecret(ctx context.Context, secretPath, secretName string) (string, error) {
	url := fmt.Sprintf("%s/api/v3/secrets/raw/%s?workspaceId=%s&environment=%s&secretPath=%s",
		c.BaseURL, secretName, c.ProjectID, c.EnvironmentSlug, secretPath)

	req, err := c.newAuthenticatedRequest(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s at %s: %w", secretName, secretPath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("secret %s not found at path %s", secretName, secretPath)
	}
	if resp.StatusCode >= 500 {
		return "", fmt.Errorf("get secret %s at %s returned status %d (transient)", secretName, secretPath, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("get secret %s at %s returned status %d: %s", secretName, secretPath, resp.StatusCode, string(respBody))
	}

	var secretResp struct {
		Secret struct {
			SecretValue string `json:"secretValue"`
		} `json:"secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&secretResp); err != nil {
		return "", fmt.Errorf("failed to decode secret %s at %s: %w", secretName, secretPath, err)
	}

	return secretResp.Secret.SecretValue, nil
}

// SecretExists checks if a secret exists in Infisical.
func (c *InfisicalClient) SecretExists(ctx context.Context, secretPath, secretName string) (bool, error) {
	url := fmt.Sprintf("%s/api/v3/secrets/raw/%s?workspaceId=%s&environment=%s&secretPath=%s",
		c.BaseURL, secretName, c.ProjectID, c.EnvironmentSlug, secretPath)

	req, err := c.newAuthenticatedRequest(ctx, "GET", url, nil)
	if err != nil {
		return false, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to check secret %s at %s: %w", secretName, secretPath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode >= 500 {
		return false, fmt.Errorf("check secret %s at %s returned status %d (transient)", secretName, secretPath, resp.StatusCode)
	}
	if resp.StatusCode == http.StatusOK {
		return true, nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	return false, fmt.Errorf("check secret %s at %s returned unexpected status %d: %s", secretName, secretPath, resp.StatusCode, string(respBody))
}

// CreateSecret creates a new secret in Infisical.
// Returns an error if the secret already exists (use UpdateSecret instead).
func (c *InfisicalClient) CreateSecret(ctx context.Context, secretPath, secretName, secretValue string) error {
	createReq := map[string]interface{}{
		"workspaceId": c.ProjectID,
		"environment": c.EnvironmentSlug,
		"secretPath":  secretPath,
		"secretName":  secretName,
		"secretValue": secretValue,
		"type":        "shared",
	}

	body, err := json.Marshal(createReq)
	if err != nil {
		return fmt.Errorf("failed to marshal create secret request: %w", err)
	}

	req, err := c.newAuthenticatedRequest(ctx, "POST", c.BaseURL+"/api/v3/secrets/raw/"+secretName, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create secret %s at %s: %w", secretName, secretPath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return fmt.Errorf("create secret %s at %s returned status %d (transient)", secretName, secretPath, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create secret %s at %s returned status %d: %s", secretName, secretPath, resp.StatusCode, string(respBody))
	}

	return nil
}

// ============================================================================
// MACHINE IDENTITY CREDENTIAL GENERATION
// ============================================================================

// machineIdentity holds the generated credentials for a Spoke-scoped identity.
type machineIdentity struct {
	ClientID     string
	ClientSecret string
}

// generateMachineCredentials generates cryptographically strong client-id and
// client-secret values for a Spoke-scoped Machine Identity.
//
// NOTE: These are static credential pairs stored in Infisical for ESO consumption.
// They are NOT real Infisical Machine Identities created via the API (which
// requires an org-level admin token). To provision actual Infisical Machine
// Identities, use the BootstrapAPI in internal/infisical/bootstrap_api.go with
// the infisical-admin token from the infisical-admin K8s secret.
func (c *InfisicalClient) generateMachineCredentials(ctx context.Context, identityName string) (*machineIdentity, error) {
	clientID, err := GenerateSecurePassword()
	if err != nil {
		return nil, fmt.Errorf("failed to generate client-id for %s: %w", identityName, err)
	}
	clientSecret, err := GenerateSecurePassword()
	if err != nil {
		return nil, fmt.Errorf("failed to generate client-secret for %s: %w", identityName, err)
	}
	return &machineIdentity{
		ClientID:     clientID,
		ClientSecret: clientSecret,
	}, nil
}

// ============================================================================
// ADR-031 BUSINESS LOGIC
// ============================================================================

// EnsureResult describes the outcome of an Ensure* operation for status condition reporting.
type EnsureResult int

const (
	EnsureCreated       EnsureResult = iota
	EnsureAlreadyExists
	EnsureMissing
)

// EnsureInfisicalCredentialsResult is the outcome of EnsureInfisicalCredentials.
type EnsureInfisicalCredentialsResult struct {
	Result EnsureResult
}

// EnsureTenantCredentialsResult is the outcome of EnsureTenantFolderAndCredentials.
type EnsureTenantCredentialsResult struct {
	Result                EnsureResult
	InfisicalCredsOutcome EnsureResult
}

// EnsureInfisicalCredentials creates a Spoke-scoped Machine Identity and stores it in the shared path.
// Idempotency contract (ADR-003):
//   - If infisical-credentials exist in shared path → AlreadyExists.
//   - If missing AND isFirstTime → create identity, attach policy, upload, return Created.
//   - If missing AND !isFirstTime → return Missing (manual intervention required).
func (c *InfisicalClient) EnsureInfisicalCredentials(ctx context.Context, cellId string, isFirstTime bool) (*EnsureInfisicalCredentialsResult, error) {
	logger := log.FromContext(ctx)
	sharedPath := fmt.Sprintf(InfisicalSharedPathFormat, cellId)

	logger.Info("Ensuring Infisical folder hierarchy", "path", sharedPath)
	if err := c.EnsureFolder(ctx, sharedPath); err != nil {
		logger.Error(err, "Failed to create shared folder", "path", sharedPath)
		return nil, fmt.Errorf("failed to create shared folder: %w", err)
	}

	// Idempotency check
	if _, err := c.GetSecret(ctx, sharedPath, "infisical-credentials"); err == nil {
		logger.Info("Machine Identity credentials already exist in Infisical, skipping generation", "cell", cellId, "path", sharedPath)
		return &EnsureInfisicalCredentialsResult{Result: EnsureAlreadyExists}, nil
	}

	if !isFirstTime {
		logger.Error(nil, "CRITICAL: infisical-credentials missing from Infisical but SpokePool was already provisioned. Manual intervention required.",
			"cell", cellId, "path", sharedPath)
		return &EnsureInfisicalCredentialsResult{Result: EnsureMissing},
			fmt.Errorf("infisical-credentials missing from Infisical for already-provisioned SpokePool %s — manual recovery required", cellId)
	}

	// Generate Machine Identity credentials for this Spoke Pool
	identityName := fmt.Sprintf("spoke-pool-%s", cellId)
	logger.Info("First-time creation: generating Machine Identity credentials", "cell", cellId, "identity", identityName)
	identity, err := c.generateMachineCredentials(ctx, identityName)
	if err != nil {
		logger.Error(err, "Failed to generate machine identity credentials", "cell", cellId)
		return nil, fmt.Errorf("failed to generate machine identity credentials: %w", err)
	}

	// COMPOSITE SECRET: Marshal to JSON for ESO 'property' parsing
	// Keys must be camelCase to match ESO Infisical provider remoteRef.property
	creds := map[string]string{
		"clientId":     identity.ClientID,
		"clientSecret": identity.ClientSecret,
		"projectId":    c.ProjectID,
	}
	jsonBytes, err := json.Marshal(creds)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal infisical credentials: %w", err)
	}

	logger.Info("Uploading Machine Identity credentials to Infisical", "cell", cellId, "path", sharedPath)
	if err := c.CreateSecret(ctx, sharedPath, "infisical-credentials", string(jsonBytes)); err != nil {
		logger.Error(err, "Failed to push infisical-credentials secret to Infisical", "cell", cellId, "path", sharedPath)
		return nil, fmt.Errorf("failed to push infisical-credentials secret: %w", err)
	}

	logger.Info("SpokePool Machine Identity credentials seeded in Infisical", "cell", cellId, "path", sharedPath)
	return &EnsureInfisicalCredentialsResult{Result: EnsureCreated}, nil
}

// EnsureTenantFolderAndCredentials creates the tenant folder, copies the shared
// Machine Identity credentials into the tenant's path (for SDK identity resolution
// per ADR-019), and generates DB credentials for the tenant database.
//
// Idempotency contract (ADR-003):
//   - If db-credentials exist in tenant path → AlreadyExists.
//   - If missing AND isFirstTime → copy infisical-credentials from shared path,
//     generate db-credentials, upload, return Created.
//   - If missing AND !isFirstTime → return Missing (manual intervention required).
func (c *InfisicalClient) EnsureTenantFolderAndCredentials(ctx context.Context, cellId, tenantId string, isFirstTime bool) (*EnsureTenantCredentialsResult, error) {
	logger := log.FromContext(ctx).WithValues("tenant", tenantId, "cell", cellId)
	tenantPath := fmt.Sprintf(InfisicalTenantPathFormat, cellId, tenantId)

	logger.Info("Ensuring tenant folder hierarchy in Infisical", "path", tenantPath)
	if err := c.EnsureFolder(ctx, tenantPath); err != nil {
		logger.Error(err, "Failed to create tenant folder", "path", tenantPath)
		return nil, fmt.Errorf("failed to create tenant folder: %w", err)
	}

	// Step 1: Copy shared Machine Identity credentials to tenant-specific path
	infisicalCredsOutcome := EnsureAlreadyExists
	infisicalSecretName := "infisical-credentials"
	infisicalExists, err := c.SecretExists(ctx, tenantPath, infisicalSecretName)
	if err != nil {
		logger.Error(err, "Failed to check Infisical for infisical-credentials", "path", tenantPath)
		return nil, fmt.Errorf("failed to check Infisical for %s: %w", infisicalSecretName, err)
	}

	if !infisicalExists {
		// Copy from shared path
		sharedPath := fmt.Sprintf(InfisicalSharedPathFormat, cellId)
		logger.Info("Copying shared Machine Identity credentials to tenant path", "sharedPath", sharedPath)
		sharedValue, err := c.GetSecret(ctx, sharedPath, infisicalSecretName)
		if err != nil {
			logger.Error(err, "Failed to read shared infisical-credentials", "sharedPath", sharedPath)
			return nil, fmt.Errorf("failed to read shared %s for tenant %s: %w", infisicalSecretName, tenantId, err)
		}

		if err := c.CreateSecret(ctx, tenantPath, infisicalSecretName, sharedValue); err != nil {
			logger.Error(err, "Failed to push infisical-credentials to tenant path", "path", tenantPath)
			return nil, fmt.Errorf("failed to push %s for tenant %s: %w", infisicalSecretName, tenantId, err)
		}
		infisicalCredsOutcome = EnsureCreated
		logger.Info("Tenant infisical-credentials seeded in Infisical", "path", tenantPath)
	}

	// Step 2: Ensure db-credentials for PostgreSQL
	dbSecretName := "db-credentials"
	dbExists, err := c.SecretExists(ctx, tenantPath, dbSecretName)
	if err != nil {
		logger.Error(err, "Failed to check Infisical for db-credentials", "path", tenantPath)
		return nil, fmt.Errorf("failed to check Infisical for %s: %w", dbSecretName, err)
	}

	if dbExists {
		logger.Info("DB credentials already exist in Infisical, skipping generation", "path", tenantPath, "secret", dbSecretName)
		return &EnsureTenantCredentialsResult{
			Result:                EnsureAlreadyExists,
			InfisicalCredsOutcome: infisicalCredsOutcome,
		}, nil
	}

	if !isFirstTime {
		logger.Error(nil, "CRITICAL: DB credentials missing from Infisical for already-provisioned tenant. Manual intervention required.", "path", tenantPath)
		return &EnsureTenantCredentialsResult{
			Result:                EnsureMissing,
			InfisicalCredsOutcome: infisicalCredsOutcome,
		}, fmt.Errorf("db-credentials missing from Infisical for already-provisioned tenant %s — manual recovery required", tenantId)
	}

	password, err := GenerateSecurePassword()
	if err != nil {
		logger.Error(err, "Failed to generate password for tenant")
		return nil, fmt.Errorf("failed to generate password for tenant %s: %w", tenantId, err)
	}
	username := fmt.Sprintf("tenant-%s-user", tenantId)

	logger.Info("Generated credentials for tenant", "usernameLength", len(username), "passwordLength", len(password))

	// COMPOSITE SECRET: Marshal to JSON for ESO 'property' parsing
	dbCreds := map[string]string{
		"username": username,
		"password": password,
	}
	dbJsonBytes, err := json.Marshal(dbCreds)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal db credentials: %w", err)
	}

	logger.Info("Uploading db-credentials to Infisical", "path", tenantPath)
	if err := c.CreateSecret(ctx, tenantPath, dbSecretName, string(dbJsonBytes)); err != nil {
		logger.Error(err, "Failed to upload db-credentials to Infisical", "path", tenantPath)
		return nil, fmt.Errorf("failed to push db-credentials for tenant %s: %w", tenantId, err)
	}

	logger.Info("Tenant DB credentials seeded in Infisical", "path", tenantPath, "secret", dbSecretName)

	return &EnsureTenantCredentialsResult{
		Result:                EnsureCreated,
		InfisicalCredsOutcome: infisicalCredsOutcome,
	}, nil
}
