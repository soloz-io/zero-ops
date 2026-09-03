// Package postgrest provides a thin Go client for the PostgREST dashboard API.
//
// PostgREST auto-generates a REST API from the `dashboard` PostgreSQL schema.
// It is used exclusively for read-only dashboard and reporting queries.
// Write operations always go through the Control Plane API (pkg/providers/postgres).
//
// # Authentication (5.2)
//
// Every request must include a JWT issued by Ory Hydra:
//
//	Authorization: Bearer <jwt>
//
// The JWT must contain:
//   - "role": "postgrest_auth"       — selects the PostgREST DB role
//   - "tenant_id": "<uuid>"          — sets app.tenant_id for RLS (tenant admin)
//   - "app.bypass_rls": "true"       — bypasses RLS (platform admin only)
//
// # Rate Limiting (5.5)
//
// Rate limiting is enforced upstream by AgentGateway (Rust reverse proxy).
// PostgREST itself does not implement rate limiting.
//
// # Available Endpoints (5.6)
//
//	GET /tenants                    — list tenants (filtered by RLS)
//	GET /tenant_registrations       — list registrations
//	GET /tenant_status_summary      — counts by status+tier (platform admin)
//	GET /stuck_tenants              — tenants stuck in transitional states
//	GET /unobserved_tenants         — tenants with no recent ArgoCD webhook
//
// All endpoints support PostgREST query parameters:
//
//	?select=id,name,tier            — column selection
//	?status=eq.READY                — equality filter
//	?order=created_at.desc          — ordering
//	?limit=50&offset=0              — pagination
package postgrest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Client is a minimal HTTP client for the PostgREST dashboard API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// Config holds PostgREST client configuration.
type Config struct {
	BaseURL string // e.g. "http://postgrest:3000"
}

// NewClient creates a new PostgREST dashboard client.
func NewClient(cfg Config) *Client {
	return &Client{
		baseURL:    cfg.BaseURL,
		httpClient: &http.Client{},
	}
}

// Query executes a GET request against a dashboard view and decodes the JSON result.
// jwt is the Bearer token. params are optional PostgREST query parameters.
//
// Example:
//
//	var tenants []map[string]any
//	err := client.Query(ctx, jwt, "tenants", url.Values{"status": {"eq.READY"}}, &tenants)
func (c *Client) Query(ctx context.Context, jwt, view string, params url.Values, out interface{}) error {
	u := fmt.Sprintf("%s/%s", c.baseURL, view)
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("postgrest: %s %s → %d: %s", http.MethodGet, view, resp.StatusCode, body)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
