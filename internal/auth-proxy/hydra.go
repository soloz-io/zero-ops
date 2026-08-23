package authproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
)

type HydraClient struct {
	adminURL string
	client   *http.Client
}

func NewHydraClient(adminURL string) *HydraClient {
	return &HydraClient{
		adminURL: adminURL,
		client:   &http.Client{},
	}
}

type OAuth2Client struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	ClientSecret            string   `json:"client_secret,omitempty"`
	Scope                   string   `json:"scope"`
}

// RegisterClient registers the OAuth2 clients the PLATFORM owns.
//
// Tenant clients are deliberately absent. This function used to also register a
// named tenant's browser and BFF clients, with their redirect URIs and that
// tenant's client secret compiled in — which put a specific fleet inside a platform
// binary and inverted ADR-004/ADR-047: tenant runtime state lives in fleet-registry
// and is rendered per fleet at onboarding, never named here.
//
// It also created a dependency the platform could not satisfy. auth-proxy waited on
// an ExternalSecret whose key only exists once that tenant's client is registered,
// so on a fresh hub — where no tenant has onboarded — it could never resolve.
// Onboarding a second tenant would have required a second env var and a code change.
//
// Tenant OAuth2 clients are now declared as OAuth2Client resources reconciled by
// hydra-maester, rendered per fleet by the tenant provisioning path. auth-proxy
// remains Hydra's login/consent provider for them; it just does not author them.
func (h *HydraClient) RegisterClient() error {
	clientSpec := OAuth2Client{
		ClientID:      "mcp-public-client",
		ClientName:    "Zero-Ops MCP Client",
		GrantTypes:    []string{"authorization_code", "refresh_token"},
		ResponseTypes: []string{"code"},
		RedirectURIs: []string{
			"http://127.0.0.1:54321/callback",
			"http://localhost:54321/callback",
			"http://127.0.0.1:18999/callback",
			"http://localhost:18999/callback",
			"http://127.0.0.1:3000/callback",
			"http://localhost:3000/callback",
			"cursor://anysphere.cursor-mcp/oauth/callback",
		},
		TokenEndpointAuthMethod: "none",
		Scope:                   "tenant:read tenant:write cluster:read cluster:write offline_access openid",
	}
	return h.upsertClient(clientSpec)
}

func (h *HydraClient) upsertClient(clientSpec OAuth2Client) error {
	// GET to check if client exists
	getReq, err := http.NewRequest("GET", fmt.Sprintf("%s/admin/clients/%s", h.adminURL, clientSpec.ClientID), nil)
	if err != nil {
		log.Fatal(err)
	}

	getResp, err := h.client.Do(getReq)
	if err != nil {
		log.Fatal(err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode == 404 {
		// Client doesn't exist, POST to create
		body, _ := json.Marshal(clientSpec)
		postReq, err := http.NewRequest("POST", fmt.Sprintf("%s/admin/clients", h.adminURL), bytes.NewBuffer(body))
		if err != nil {
			log.Fatal(err)
		}
		postReq.Header.Set("Content-Type", "application/json")

		postResp, err := h.client.Do(postReq)
		if err != nil {
			log.Fatal(err)
		}
		defer postResp.Body.Close()

		if postResp.StatusCode == 409 {
			// Race condition - another replica created it
			log.Println("Client already exists (409 race condition), continuing")
			return nil
		}

		if postResp.StatusCode != 200 && postResp.StatusCode != 201 {
			bodyBytes, _ := io.ReadAll(postResp.Body)
			log.Fatalf("Failed to create client: %d - %s", postResp.StatusCode, string(bodyBytes))
		}

		log.Println("Client created successfully")
		return nil
	}

	if getResp.StatusCode == 200 {
		// Client exists, PUT to ensure state matches
		body, _ := json.Marshal(clientSpec)
		putReq, err := http.NewRequest("PUT", fmt.Sprintf("%s/admin/clients/%s", h.adminURL, clientSpec.ClientID), bytes.NewBuffer(body))
		if err != nil {
			log.Fatal(err)
		}
		putReq.Header.Set("Content-Type", "application/json")

		putResp, err := h.client.Do(putReq)
		if err != nil {
			log.Fatal(err)
		}
		defer putResp.Body.Close()

		if putResp.StatusCode != 200 {
			bodyBytes, _ := io.ReadAll(putResp.Body)
			log.Fatalf("Failed to update client: %d - %s", putResp.StatusCode, string(bodyBytes))
		}

		log.Println("Client updated successfully")
		return nil
	}

	// Any other error
	bodyBytes, _ := io.ReadAll(getResp.Body)
	log.Fatalf("Failed to check client existence: %d - %s", getResp.StatusCode, string(bodyBytes))
	return nil
}
