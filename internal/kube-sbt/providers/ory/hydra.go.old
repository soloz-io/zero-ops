package ory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

type hydraClient struct {
	publicURL string
	adminURL  string
	client    *http.Client
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// authenticate exchanges credentials for tokens via Hydra's token endpoint
// using the Resource Owner Password Credentials grant (for admin/service use).
func (h *hydraClient) authenticate(ctx context.Context, creds models.Credentials, clientID, audience string) (*models.Token, error) {
	form := url.Values{
		"grant_type": {"password"},
		"username":   {creds.Email},
		"password":   {creds.Password},
		"client_id":  {clientID},
		"audience":   {audience},
		"scope":      {"openid offline_access"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.publicURL+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("hydra authenticate: %d %s", resp.StatusCode, b)
	}
	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, err
	}
	return &models.Token{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		TokenType:    tr.TokenType,
		ExpiresIn:    tr.ExpiresIn,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}, nil
}

// refreshToken exchanges a refresh token for a new token pair.
func (h *hydraClient) refreshToken(ctx context.Context, refreshToken, clientID string) (*models.Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.publicURL+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("hydra refreshToken: %d %s", resp.StatusCode, b)
	}
	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return nil, err
	}
	return &models.Token{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		TokenType:    tr.TokenType,
		ExpiresIn:    tr.ExpiresIn,
		ExpiresAt:    time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}, nil
}

// ensureClient creates or updates an OAuth2 client in Hydra (idempotent).
func (h *hydraClient) ensureClient(ctx context.Context, clientID, clientName string, redirectURIs []string, scopes string) error {
	spec := map[string]interface{}{
		"client_id":                  clientID,
		"client_name":                clientName,
		"grant_types":                []string{"authorization_code", "refresh_token", "password"},
		"response_types":             []string{"code"},
		"redirect_uris":              redirectURIs,
		"token_endpoint_auth_method": "none",
		"scope":                      scopes,
	}
	body, _ := json.Marshal(spec)

	// Check existence
	getResp, err := h.client.Do(mustRequest(ctx, http.MethodGet,
		fmt.Sprintf("%s/admin/clients/%s", h.adminURL, clientID), nil))
	if err != nil {
		return err
	}
	defer getResp.Body.Close()

	method := http.MethodPost
	path := h.adminURL + "/admin/clients"
	if getResp.StatusCode == http.StatusOK {
		method = http.MethodPut
		path = fmt.Sprintf("%s/admin/clients/%s", h.adminURL, clientID)
	}

	resp, err := h.client.Do(mustRequest(ctx, method, path, bytes.NewReader(body)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("hydra ensureClient: %d %s", resp.StatusCode, b)
	}
	return nil
}

func mustRequest(ctx context.Context, method, url string, body io.Reader) *http.Request {
	req, _ := http.NewRequestWithContext(ctx, method, url, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}
