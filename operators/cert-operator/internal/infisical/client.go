package infisical

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

type Client struct {
	BaseURL       string
	clientID      string
	clientSecret  string
	token         string
	tokenExp      time.Time
	mu            sync.RWMutex
	HTTP          *http.Client
}

func NewClient(baseURL string) *Client {
	clientID := os.Getenv("INFISICAL_CLIENT_ID")
	clientSecret := os.Getenv("INFISICAL_CLIENT_SECRET")
	return &Client{
		BaseURL:      baseURL,
		clientID:     clientID,
		clientSecret: clientSecret,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) authenticate(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	clientID := c.clientID
	if clientID == "" {
		clientID = os.Getenv("INFISICAL_CLIENT_ID")
	}
	clientSecret := c.clientSecret
	if clientSecret == "" {
		clientSecret = os.Getenv("INFISICAL_CLIENT_SECRET")
	}
	if clientID == "" || clientSecret == "" {
		return fmt.Errorf("INFISICAL_CLIENT_ID and INFISICAL_CLIENT_SECRET must be set")
	}

	loginReq := map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
	}
	body, err := json.Marshal(loginReq)
	if err != nil {
		return fmt.Errorf("marshal login request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		c.BaseURL+"/api/v1/auth/universal-auth/login", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("execute login request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("universal auth login failed: status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var loginResp struct {
		AccessToken string `json:"accessToken"`
		ExpiresIn   int    `json:"expiresIn"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return fmt.Errorf("decode login response: %w", err)
	}

	c.token = loginResp.AccessToken
	c.tokenExp = time.Now().Add(time.Duration(loginResp.ExpiresIn) * time.Second)
	return nil
}

func (c *Client) ensureAuthenticated(ctx context.Context) error {
	c.mu.RLock()
	valid := c.token != "" && time.Now().Add(5*time.Minute).Before(c.tokenExp)
	c.mu.RUnlock()
	if valid {
		return nil
	}
	return c.authenticate(ctx)
}

func (c *Client) getToken(ctx context.Context) (string, error) {
	if err := c.ensureAuthenticated(ctx); err != nil {
		return "", err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token, nil
}

// IssueCertRequest matches the verified Infisical /api/v1/cert-manager/certificates endpoint
type IssueCertRequest struct {
	ProfileId  string              `json:"profileId"`
	Attributes IssueCertAttributes `json:"attributes"`
}

type IssueCertAttributes struct {
	CommonName string `json:"commonName,omitempty"`
	TTL        string `json:"ttl,omitempty"`
}

type IssueCertResponseWrapper struct {
	Certificate          *CertData `json:"certificate"`
	CertificateRequestId string    `json:"certificateRequestId"`
	Status               string    `json:"status"`
	Message              string    `json:"message,omitempty"`
}

type CertData struct {
	Certificate          string `json:"certificate"`
	IssuingCaCertificate string `json:"issuingCaCertificate"`
	CertificateChain     string `json:"certificateChain"`
	PrivateKey           string `json:"privateKey"`
	SerialNumber         string `json:"serialNumber"`
	CertificateId        string `json:"certificateId"`
}

type Identity struct {
	ID           string
	ClientID     string
	ClientSecret string
}

type Profile struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

// IssueBootstrapCertificate mints a 72-hour cert for ArgoCD Agent bootstrap.
func (c *Client) IssueBootstrapCertificate(ctx context.Context, profileId, commonName string) (*CertData, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}

	reqBody := IssueCertRequest{
		ProfileId: profileId,
		Attributes: IssueCertAttributes{
			CommonName: commonName,
			TTL:        "72h",
		},
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal issue cert request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/api/v1/cert-manager/certificates", c.BaseURL),
		bytes.NewBuffer(payload))
	if err != nil {
		return nil, fmt.Errorf("create issue cert request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute issue cert request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("issue bootstrap cert failed: status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result IssueCertResponseWrapper
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode issue cert response: %w", err)
	}

	if result.Certificate == nil {
		return nil, fmt.Errorf("bootstrap certificate issued nil (async order flow): %s", result.Message)
	}

	return result.Certificate, nil
}

// GetProfileIdBySlug resolves a certificate profile slug to its UUID.
// The projectId query parameter is required by Infisical OSS to scope the lookup
// (source: certificate-profiles-router.ts:461).
func (c *Client) GetProfileIdBySlug(ctx context.Context, slug, projectID string) (string, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return "", fmt.Errorf("authenticate: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/api/v1/cert-manager/certificate-profiles/slug/%s?projectId=%s", c.BaseURL, slug, projectID), nil)
	if err != nil {
		return "", fmt.Errorf("create get profile request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("execute get profile request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("get profile by slug failed: status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode profile response: %w", err)
	}

	return result.ID, nil
}

// GetOrCreateMachineIdentity finds or creates a Machine Identity in Infisical.
func (c *Client) GetOrCreateMachineIdentity(ctx context.Context, name, orgID string) (*Identity, error) {
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}
	c.token = token // reuse for private helpers

	existing, err := c.findIdentityByName(ctx, name, orgID)
	if err == nil && existing != "" {
		ua, err := c.getUniversalAuth(ctx, existing)
		if err != nil {
			return nil, fmt.Errorf("get universal auth for existing identity: %w", err)
		}
		return &Identity{ID: existing, ClientID: ua.IdentityUniversalAuth.ClientID, ClientSecret: ""}, nil
	}

	id, err := c.createIdentity(ctx, name, orgID)
	if err != nil {
		return nil, fmt.Errorf("create machine identity: %w", err)
	}

	if err := c.attachUniversalAuth(ctx, id); err != nil {
		return nil, fmt.Errorf("attach universal auth: %w", err)
	}

	clientID, err := c.getClientID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get client ID: %w", err)
	}

	clientSecret, err := c.generateClientSecret(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("generate client secret: %w", err)
	}

	return &Identity{ID: id, ClientID: clientID, ClientSecret: clientSecret}, nil
}

// GrantProjectAccess grants a Machine Identity access to the Infisical project.
func (c *Client) GrantProjectAccess(ctx context.Context, identityID, projectID, role string) error {
	token, err := c.getToken(ctx)
	if err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}

	payload := map[string]string{"role": role}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal grant access: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/api/v1/projects/%s/memberships/identities/%s", c.BaseURL, projectID, identityID),
		bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("create grant access request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("execute grant access request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusConflict {
		respBody, _ := io.ReadAll(resp.Body)
		if bytes.Contains(respBody, []byte("already")) || bytes.Contains(respBody, []byte("exists")) {
			return nil
		}
		return fmt.Errorf("grant project access failed: status %d: %s", resp.StatusCode, string(respBody))
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("grant project access failed: status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// --- private helpers ---

func (c *Client) findIdentityByName(ctx context.Context, name, orgID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/api/v1/identities?limit=100&orgId=%s", c.BaseURL, orgID), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("list identities failed: status %d", resp.StatusCode)
	}

	var listResp struct {
		Identities []struct {
			Identity struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"identity"`
		} `json:"identities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResp); err != nil {
		return "", err
	}

	for _, item := range listResp.Identities {
		if item.Identity.Name == name {
			return item.Identity.ID, nil
		}
	}
	return "", nil
}

type universalAuthResponse struct {
	IdentityUniversalAuth struct {
		ClientID string `json:"clientId"`
	} `json:"identityUniversalAuth"`
}

func (c *Client) getUniversalAuth(ctx context.Context, identityID string) (*universalAuthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s/api/v1/auth/universal-auth/identities/%s", c.BaseURL, identityID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get universal auth failed: status %d", resp.StatusCode)
	}

	var ua universalAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&ua); err != nil {
		return nil, err
	}
	return &ua, nil
}

func (c *Client) createIdentity(ctx context.Context, name, orgID string) (string, error) {
	payload := map[string]string{
		"name":           name,
		"organizationId": orgID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/api/v1/identities", c.BaseURL), bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("create identity failed: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Identity struct {
			ID string `json:"id"`
		} `json:"identity"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Identity.ID, nil
}

func (c *Client) attachUniversalAuth(ctx context.Context, identityID string) error {
	payload := map[string]interface{}{
		"clientSecretTrustedIps": []map[string]string{
			{"ipAddress": "0.0.0.0/0"},
			{"ipAddress": "::/0"},
		},
		"accessTokenTrustedIps": []map[string]string{
			{"ipAddress": "0.0.0.0/0"},
			{"ipAddress": "::/0"},
		},
		"accessTokenTTL":      2592000,
		"accessTokenMaxTTL":   2592000,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/api/v1/auth/universal-auth/identities/%s", c.BaseURL, identityID),
		bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusBadRequest && bytes.Contains(respBody, []byte("already")) {
		return nil
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("attach universal auth failed: status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func (c *Client) getClientID(ctx context.Context, identityID string) (string, error) {
	ua, err := c.getUniversalAuth(ctx, identityID)
	if err != nil {
		return "", err
	}
	return ua.IdentityUniversalAuth.ClientID, nil
}

func (c *Client) generateClientSecret(ctx context.Context, identityID string) (string, error) {
	payload := map[string]interface{}{
		"numUsesLimit": 0,
		"ttl":          0,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		fmt.Sprintf("%s/api/v1/auth/universal-auth/identities/%s/client-secrets", c.BaseURL, identityID),
		bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("generate client secret failed: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ClientSecret string `json:"clientSecret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	return result.ClientSecret, nil
}
