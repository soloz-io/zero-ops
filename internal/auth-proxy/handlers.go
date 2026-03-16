package authproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Handler struct {
	hydraPublicURL    string
	hydraAdminURL     string
	kratosClient      *KratosClient
	hydraClient       *HydraClient
	client            *http.Client
	jwksURL           string
	expectedAudience  string
	authPublicBaseURL string
	mcpGatewayBaseURL string
	ready             bool
}

func NewHandler(hydraPublicURL, hydraAdminURL, kratosPublicURL, kratosAdminURL string, timeout time.Duration, trustedClientIDs, expectedAudience, authPublicBaseURL, mcpGatewayBaseURL string) *Handler {
	return &Handler{
		hydraPublicURL: hydraPublicURL,
		hydraAdminURL:  hydraAdminURL,
		kratosClient:   NewKratosClient(kratosPublicURL, kratosAdminURL),
		hydraClient:    NewHydraClient(hydraAdminURL),
		client: &http.Client{
			Timeout: timeout,
		},
		jwksURL:           hydraPublicURL + "/.well-known/jwks.json",
		expectedAudience:  expectedAudience,
		authPublicBaseURL: authPublicBaseURL,
		mcpGatewayBaseURL: mcpGatewayBaseURL,
	}
}

func (h *Handler) ServeAuthServerMetadata(w http.ResponseWriter, r *http.Request) {
	auth := h.authPublicBaseURL
	meta := map[string]interface{}{
		"issuer":                                auth,
		"authorization_endpoint":                auth + "/oauth2/auth",
		"token_endpoint":                        auth + "/oauth2/token",
		"registration_endpoint":                 auth + "/oauth2/register",
		"revocation_endpoint":                   auth + "/oauth2/revoke",
		"jwks_uri":                              auth + "/.well-known/jwks.json",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported":      []string{"S256"},
		"scopes_supported":                      []string{"openid", "offline_access", "tenant:read", "tenant:write", "cluster:read", "cluster:write"},
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
	json.NewEncoder(w).Encode(meta)
}

func (h *Handler) ProxyMetadata(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/.well-known/oauth-authorization-server")
}

func (h *Handler) ProxyJWKS(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, "/.well-known/jwks.json")
}

func (h *Handler) ProxyOAuth2(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/oauth2/register" && r.Method == http.MethodPost {
		h.proxyDCR(w, r)
		return
	}
	h.proxy(w, r, r.URL.Path)
}

func (h *Handler) proxyDCR(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Inject audience into DCR request body
	var reqBody map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	reqBody["audience"] = []string{h.mcpGatewayBaseURL + "/mcp"}
	injected, _ := json.Marshal(reqBody)

	req, err := http.NewRequestWithContext(ctx, r.Method, h.hydraPublicURL+r.URL.Path, bytes.NewReader(injected))
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Set("Content-Length", strconv.Itoa(len(injected)))

	resp, err := h.client.Do(req)
	if err != nil {
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
		return
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}

	// Remove null and empty fields that Cursor's Zod schema rejects
	for k, v := range body {
		if v == nil {
			delete(body, k)
			continue
		}
		if s, ok := v.(string); ok && s == "" {
			delete(body, k)
			continue
		}
		if m, ok := v.(map[string]interface{}); ok && len(m) == 0 {
			delete(body, k)
		}
	}
	if v, ok := body["contacts"]; !ok || v == nil {
		body["contacts"] = []string{}
	}

	out, _ := json.Marshal(body)
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(http.StatusCreated)
	w.Write(out)
}

func (h *Handler) HealthReady(w http.ResponseWriter, r *http.Request) {
	if !h.ready {
		http.Error(w, "Not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (h *Handler) SetReady() {
	h.ready = true
}

func (h *Handler) LoginHandler(w http.ResponseWriter, r *http.Request) {
	challenge := r.URL.Query().Get("login_challenge")
	if challenge == "" {
		http.Error(w, "Missing login_challenge", http.StatusBadRequest)
		return
	}

	// Check for existing Kratos session
	cookie := r.Header.Get("Cookie")
	if cookie != "" {
		session, err := h.kratosClient.GetSession(cookie)
		if err == nil {
			// Accept login with existing session
			acceptReq := map[string]interface{}{
				"subject": session.Identity.ID,
			}
			h.acceptLogin(w, r, challenge, acceptReq)
			return
		}
	}

	// No session exists, redirect to Kratos UI with login_challenge
	kratosURL := fmt.Sprintf("https://console.nutgraf.in/login?login_challenge=%s", challenge)
	http.Redirect(w, r, kratosURL, http.StatusFound)
}

func (h *Handler) ConsentHandler(w http.ResponseWriter, r *http.Request) {
	challenge := r.URL.Query().Get("consent_challenge")
	if challenge == "" {
		http.Error(w, "Missing consent_challenge", http.StatusBadRequest)
		return
	}

	// Fetch consent request from Hydra
	consentReq, err := h.getConsentRequest(challenge)
	if err != nil {
		log.Printf("Failed to fetch consent request: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Validate requested scopes
	requestedScopes, _ := consentReq["requested_scope"].([]interface{})
	if len(requestedScopes) == 0 {
		h.rejectConsent(w, r, challenge, "invalid_scope", "No scopes requested")
		return
	}

	// Get identity traits from Kratos
	subject, _ := consentReq["subject"].(string)
	traits, err := h.kratosClient.GetIdentityTraits(subject)
	if err != nil {
		log.Printf("Failed to fetch identity traits: %v", err)
		h.rejectConsent(w, r, challenge, "access_denied", "Failed to fetch identity")
		return
	}

	// Build session with custom claims
	session := map[string]interface{}{
		"id_token": map[string]interface{}{
			"email": traits["email"],
			"role":  traits["role"],
		},
		"access_token": map[string]interface{}{
			"email": traits["email"],
			"role":  traits["role"],
		},
	}

	// Accept consent
	acceptReq := map[string]interface{}{
		"grant_scope":                requestedScopes,
		"grant_access_token_audience": []string{h.mcpGatewayBaseURL + "/mcp"},
		"session":                    session,
	}

	h.acceptConsent(w, r, challenge, acceptReq)
}

func (h *Handler) getConsentRequest(challenge string) (map[string]interface{}, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("%s/admin/oauth2/auth/requests/consent?consent_challenge=%s", h.hydraAdminURL, challenge), nil)
	if err != nil {
		return nil, err
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to fetch consent request: %d", resp.StatusCode)
	}

	var consentReq map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&consentReq); err != nil {
		return nil, err
	}

	return consentReq, nil
}

func (h *Handler) acceptLogin(w http.ResponseWriter, r *http.Request, challenge string, body map[string]interface{}) {
	h.acceptOrReject(w, r, challenge, body, "login", "accept")
}

func (h *Handler) acceptConsent(w http.ResponseWriter, r *http.Request, challenge string, body map[string]interface{}) {
	h.acceptOrReject(w, r, challenge, body, "consent", "accept")
}

func (h *Handler) rejectConsent(w http.ResponseWriter, r *http.Request, challenge, error, errorDescription string) {
	body := map[string]interface{}{
		"error":             error,
		"error_description": errorDescription,
	}
	h.acceptOrReject(w, r, challenge, body, "consent", "reject")
}

func (h *Handler) acceptOrReject(w http.ResponseWriter, r *http.Request, challenge string, body map[string]interface{}, flow, action string) {
	jsonBody, _ := json.Marshal(body)
	req, err := http.NewRequest("PUT", fmt.Sprintf("%s/admin/oauth2/auth/requests/%s/%s?%s_challenge=%s", h.hydraAdminURL, flow, action, flow, challenge), strings.NewReader(string(jsonBody)))
	if err != nil {
		log.Printf("Failed to create %s request: %v", action, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		log.Printf("Failed to %s %s: %v", action, flow, err)
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Failed to %s %s: %d - %s", action, flow, resp.StatusCode, string(body))
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("Failed to decode %s response: %v", action, err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	redirectTo, _ := result["redirect_to"].(string)
	if redirectTo == "" {
		http.Error(w, "Missing redirect_to", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, redirectTo, http.StatusFound)
}


func (h *Handler) proxy(w http.ResponseWriter, r *http.Request, path string) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, r.Method, h.hydraPublicURL+path, r.Body)
	if err != nil {
		log.Printf("Failed to create proxy request: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()
	req.URL.RawQuery = r.URL.RawQuery

	noRedirectClient := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := noRedirectClient.Do(req)
	if err != nil {
		log.Printf("Failed to proxy request to Hydra: %v", err)
		http.Error(w, "Bad gateway", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)

	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("Failed to copy response body: %v", err)
	}
}
