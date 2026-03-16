package authproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Handler struct {
	hydraPublicURL string
	hydraAdminURL  string
	kratosClient   *KratosClient
	hydraClient    *HydraClient
	client         *http.Client
	trustedClients map[string]bool
	jwksURL        string
	expectedAudience string
	ready          bool
}

func NewHandler(hydraPublicURL, hydraAdminURL, kratosPublicURL, kratosAdminURL string, timeout time.Duration, trustedClientIDs, expectedAudience string) *Handler {
	trustedClients := make(map[string]bool)
	for _, id := range strings.Split(trustedClientIDs, ",") {
		trustedClients[strings.TrimSpace(id)] = true
	}

	return &Handler{
		hydraPublicURL: hydraPublicURL,
		hydraAdminURL:  hydraAdminURL,
		kratosClient:   NewKratosClient(kratosPublicURL, kratosAdminURL),
		hydraClient:    NewHydraClient(hydraAdminURL),
		client: &http.Client{
			Timeout: timeout,
		},
		trustedClients:   trustedClients,
		jwksURL:          hydraPublicURL + "/.well-known/jwks.json",
		expectedAudience: expectedAudience,
	}
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

	req, err := http.NewRequestWithContext(ctx, r.Method, h.hydraPublicURL+r.URL.Path, r.Body)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()

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

	// Sanitize fields Cursor's Zod schema rejects
	if v, ok := body["client_uri"]; !ok || v == "" {
		delete(body, "client_uri")
	}
	if v, ok := body["contacts"]; !ok || v == nil {
		body["contacts"] = []string{}
	}

	out, _ := json.Marshal(body)
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
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

	// No session exists, redirect to Kratos with return_to
	returnTo := fmt.Sprintf("%s/login?login_challenge=%s", r.Host, challenge)
	encodedReturnTo := url.QueryEscape(returnTo)
	kratosURL := fmt.Sprintf("https://console.nutgraf.in/login?return_to=%s", encodedReturnTo)
	
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

	// Check if client is trusted
	clientID, _ := consentReq["client"].(map[string]interface{})["client_id"].(string)
	if !h.trustedClients[clientID] {
		h.rejectConsent(w, r, challenge, "access_denied", "Client not trusted")
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
		"grant_access_token_audience": []string{"https://api.nutgraf.in"},
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

	resp, err := h.client.Do(req)
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
