package authproxy

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"
)

// Handler serves the machine surfaces that remain on auth.<zone>.
//
// The Gateway splits that hostname (manifests/hub-core-services/gateway/
// httproutes.yaml): /.well-known/ and /internal/ are SERVED here, everything else
// is redirected to Zitadel for browser login. So this type deliberately has no
// login, consent, token or userinfo handler — those were Hydra's challenge flow,
// Zitadel hosts its own login, and anything still routed to them would be
// answered by a redirect rather than by this process.
type Handler struct {
	// issuerURL is the PUBLIC Zitadel issuer. Everything advertised to a client
	// is built from this, never from authPublicBaseURL: a token minted by Zitadel
	// carries iss=https://id.<zone>, and a client that discovered a different
	// issuer rejects it as a spoofed provider.
	issuerURL string
	// internalURL is the in-cluster address documents are fetched from.
	internalURL       string
	client            *http.Client
	jwksURL           string
	expectedAudience  string
	authPublicBaseURL string
	mcpGatewayBaseURL string
	ready             bool
}

func NewHandler(issuerURL, internalURL string, timeout time.Duration, expectedAudience, authPublicBaseURL, mcpGatewayBaseURL string) *Handler {
	return &Handler{
		issuerURL:   issuerURL,
		internalURL: internalURL,
		client: &http.Client{
			Timeout: timeout,
		},
		// Zitadel publishes signing keys at /oauth/v2/keys and returns 404 for the
		// conventional /.well-known/jwks.json. Validation against the wrong path
		// fails as "Expected 200 OK from the JSON Web Key Set HTTP response",
		// which names neither the path nor the provider.
		jwksURL:           internalURL + zitadelJWKSPath,
		expectedAudience:  expectedAudience,
		authPublicBaseURL: authPublicBaseURL,
		mcpGatewayBaseURL: mcpGatewayBaseURL,
	}
}

// ServeAuthServerMetadata answers RFC 8414 for MCP clients discovering the API
// gateway's authorization server.
//
// Every endpoint names Zitadel directly rather than a path on this host. The
// previous version advertised auth.<zone>/oauth2/* because Hydra sat behind those
// paths here; the Gateway now redirects them, and a 302 on a token endpoint is
// not something an OAuth client recovers from.
//
// registration_endpoint is deliberately ABSENT. Zitadel does not implement
// dynamic client registration (RFC 7591), which Hydra did, so a client that
// self-registered must now be given a pre-provisioned client id. Advertising an
// endpoint that 404s would turn that into a confusing runtime failure instead of
// a clean absence the client can detect.
func (h *Handler) ServeAuthServerMetadata(w http.ResponseWriter, r *http.Request) {
	iss := h.issuerURL
	meta := map[string]interface{}{
		"issuer":                                iss,
		"authorization_endpoint":                iss + zitadelAuthorizePath,
		"token_endpoint":                        iss + zitadelTokenPath,
		"userinfo_endpoint":                     iss + zitadelUserinfoPath,
		"revocation_endpoint":                   iss + zitadelRevocationPath,
		"introspection_endpoint":                iss + zitadelIntrospectionPath,
		"end_session_endpoint":                  iss + zitadelEndSessionPath,
		"jwks_uri":                              iss + zitadelJWKSPath,
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported":      []string{"S256"},
		"scopes_supported":                      []string{"openid", "profile", "email", "offline_access"},
		// The resource this authorization server mints tokens for.
		"resource": h.mcpGatewayBaseURL + "/mcp",
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, MCP-Protocol-Version")
	if err := json.NewEncoder(w).Encode(meta); err != nil {
		log.Printf("Failed to encode authorization server metadata: %v", err)
	}
}

// ProxyOpenIDConfiguration serves Zitadel's discovery document.
//
// Served, not redirected, and passed through UNMODIFIED. The document states
// "issuer": "https://id.<zone>" and must keep saying so even though it is being
// read from auth.<zone> — rewriting it to name this host would produce a document
// no Zitadel-issued token can be validated against.
func (h *Handler) ProxyOpenIDConfiguration(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, zitadelDiscoveryPath)
}

// ProxyJWKS serves Zitadel's signing keys, translating the conventional path to
// the one Zitadel actually publishes on.
func (h *Handler) ProxyJWKS(w http.ResponseWriter, r *http.Request) {
	h.proxy(w, r, zitadelJWKSPath)
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

// JWKSURL is the in-cluster URL signing keys are fetched from.
func (h *Handler) JWKSURL() string { return h.jwksURL }

func (h *Handler) proxy(w http.ResponseWriter, r *http.Request, path string) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, r.Method, h.internalURL+path, r.Body)
	if err != nil {
		log.Printf("Failed to create proxy request: %v", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()
	// The in-cluster Service address is not the issuer. Zitadel builds URLs from
	// its configured ExternalDomain rather than from this header, but the header
	// is corrected anyway so a request never carries a host the origin does not
	// serve.
	req.Host = ""
	req.URL.RawQuery = r.URL.RawQuery

	noRedirectClient := &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := noRedirectClient.Do(req)
	if err != nil {
		log.Printf("Failed to proxy request to Zitadel: %v", err)
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
