package infisical

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// BootstrapAPI handles low-level Infisical API calls for bootstrap
type BootstrapAPI struct {
	baseURL    string
	httpClient *http.Client
}

// NewBootstrapAPI creates a new bootstrap API client
func NewBootstrapAPI() *BootstrapAPI {
	return &BootstrapAPI{
		baseURL: InfisicalBaseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// BootstrapResponse represents the response from /api/v1/admin/bootstrap
type BootstrapResponse struct {
	Identity struct {
		Credentials struct {
			Token string `json:"token"`
		} `json:"credentials"`
	} `json:"identity"`
	Organization struct {
		ID string `json:"id"`
	} `json:"organization"`
}

// Bootstrap calls /api/v1/admin/bootstrap to create admin user
func (api *BootstrapAPI) Bootstrap(ctx context.Context, email, password, orgName string) (*BootstrapResponse, error) {
	payload := map[string]string{
		"email":        email,
		"password":     password,
		"organization": orgName,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal bootstrap request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", api.baseURL+"/api/v1/admin/bootstrap", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create bootstrap request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := api.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute bootstrap request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bootstrap failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var bootstrapResp BootstrapResponse
	if err := json.NewDecoder(resp.Body).Decode(&bootstrapResp); err != nil {
		return nil, fmt.Errorf("failed to decode bootstrap response: %w", err)
	}

	return &bootstrapResp, nil
}

// OrganizationResponse represents organization list response
type OrganizationResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListOrganizations lists all organizations
func (api *BootstrapAPI) ListOrganizations(ctx context.Context, adminToken string) ([]OrganizationResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", api.baseURL+"/api/v1/organizations", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := api.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list organizations failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var orgs []OrganizationResponse
	if err := json.NewDecoder(resp.Body).Decode(&orgs); err != nil {
		return nil, fmt.Errorf("failed to decode organizations response: %w", err)
	}

	return orgs, nil
}

// ProjectResponse represents project creation response
type ProjectResponse struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// CreateProject creates a new project
func (api *BootstrapAPI) CreateProject(ctx context.Context, adminToken, projectName, orgID string) (*ProjectResponse, error) {
	payload := map[string]string{
		"projectName":    projectName,
		"organizationId": orgID,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal project request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", api.baseURL+"/api/v1/projects", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create project request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := api.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute project request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create project failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var projectResp ProjectResponse
	if err := json.NewDecoder(resp.Body).Decode(&projectResp); err != nil {
		return nil, fmt.Errorf("failed to decode project response: %w", err)
	}

	return &projectResp, nil
}

// IdentityResponse represents machine identity creation response
type IdentityResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CreateIdentity creates a machine identity
func (api *BootstrapAPI) CreateIdentity(ctx context.Context, adminToken, identityName, orgID string) (*IdentityResponse, error) {
	payload := map[string]string{
		"name":           identityName,
		"organizationId": orgID,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal identity request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", api.baseURL+"/api/v1/identities", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create identity request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := api.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute identity request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create identity failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var identityResp IdentityResponse
	if err := json.NewDecoder(resp.Body).Decode(&identityResp); err != nil {
		return nil, fmt.Errorf("failed to decode identity response: %w", err)
	}

	return &identityResp, nil
}

// AttachUniversalAuth attaches Universal Auth to machine identity
func (api *BootstrapAPI) AttachUniversalAuth(ctx context.Context, adminToken, identityID string) error {
	req, err := http.NewRequestWithContext(ctx, "POST", api.baseURL+"/api/v1/auth/universal-auth/identities/"+identityID, nil)
	if err != nil {
		return fmt.Errorf("failed to create attach auth request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := api.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute attach auth request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("attach universal auth failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// ClientCredentialsResponse represents client credentials generation response
type ClientCredentialsResponse struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

// GenerateClientCredentials generates client credentials for machine identity
func (api *BootstrapAPI) GenerateClientCredentials(ctx context.Context, adminToken, identityID string) (*ClientCredentialsResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", api.baseURL+"/api/v1/auth/universal-auth/identities/"+identityID+"/client-secrets", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create client credentials request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := api.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute client credentials request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("generate client credentials failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var credsResp ClientCredentialsResponse
	if err := json.NewDecoder(resp.Body).Decode(&credsResp); err != nil {
		return nil, fmt.Errorf("failed to decode client credentials response: %w", err)
	}

	return &credsResp, nil
}

// GrantProjectAccess grants machine identity access to project with role
func (api *BootstrapAPI) GrantProjectAccess(ctx context.Context, adminToken, projectID, identityID, role string) error {
	payload := map[string]string{
		"role": role,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal grant access request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/projects/%s/memberships/identities/%s", api.baseURL, projectID, identityID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create grant access request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := api.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute grant access request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("grant project access failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}
