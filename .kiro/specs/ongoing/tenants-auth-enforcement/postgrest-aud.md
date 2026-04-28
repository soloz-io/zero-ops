You have an incredibly sharp eye. You are **100% correct**. 

Using `https://api.nutgraf.in/mcp` for the Spoke's PostgREST audience is an architectural mistake. I pulled that value because your current `auth-proxy` codebase hardcodes it during the OAuth consent flow, but applying it to the Spoke PostgREST creates a severe **Confused Deputy** security vulnerability and tightly couples the Hub Control Plane with the Spoke Application Plane.

If a token is minted for the Hub's MCP server (`/mcp`), it should **not** be valid for a tenant's database API on the Spoke cluster, and vice versa.

Here is the breakdown of why this is wrong, what the industry standard is, and exactly how to fix it.

---

### Why the previous value was wrong

In OAuth 2.0 / OIDC, the `aud` (Audience) claim identifies the **Resource Server** (the API) that the token is intended for. 
*   **Hub MCP Server:** An agent interacting with the control plane.
*   **Spoke PostgREST:** A tenant's application interacting with their specific database.

Using the same `aud` for both means a token generated for an AI agent to read Hub telemetry could technically be replayed against a Spoke database to steal tenant data.

### The Industry Standard Solution (RFC 8707)

The widely adopted enterprise standard for this is **OAuth 2.0 Token Exchange and Resource Indicators (RFC 8707)**. 

Instead of physical URLs, modern SaaS platforms use **Logical URNs (Uniform Resource Names)** or dedicated API subdomains to define audiences. 

For your architecture, you have two tiers of Resource Servers. You should define distinct logical audiences:
1.  **Hub Control Plane APIs:** `urn:zero-ops:hub-api` (or `https://api.nutgraf.in/mcp`)
2.  **Spoke Tenant APIs:** `urn:zero-ops:tenant-api` (or `https://{tenantId}.api.nutgraf.in`)

#### Option A: Global Spoke Audience (Recommended for MVP)
You set the PostgREST audience to a single logical identifier that represents "Tenant Data APIs". 
*   **Audience:** `urn:zero-ops:tenant-api`
*   **How it works:** Hydra issues a token with `aud: "urn:zero-ops:tenant-api"`. PostgREST validates the signature and the `aud`. The *tenant isolation* is then handled by the `tenant_id` claim inside the JWT interacting with PostgreSQL Row Level Security (RLS).

#### Option B: Per-Tenant Audience (Strict Enterprise)
You set the audience dynamically per tenant.
*   **Audience:** `https://tenant-{{.tenantId}}.api.nutgraf.in`
*   **How it works:** The tenant application explicitly requests a token for *their* specific API endpoint. This ensures a token for Tenant A can never be accidentally accepted by Tenant B's API, even if RLS is misconfigured.

---

### How to implement the Industry Standard (Option A/B)

#### 1. Update the Crossplane Composition (Spoke Side)
In `manifests/hub-core-services/crossplane/tenant-platform/compositions/ainativesaas-starter-hetzner.yaml`, update the PostgREST deployment to use a proper logical audience.

**If using Option A (Global URN):**
```yaml
                    - name: PGRST_JWT_AUD
                      value: "urn:zero-ops:tenant-api"
```

**If using Option B (Strict Per-Tenant - highly recommended for your XR setup):**
```yaml
                    - name: PGRST_JWT_AUD
                      value: "" # Patched dynamically below
```
And add this patch to the `postgrest-deployment` resource in your Crossplane Composition:
```yaml
    - type: CombineFromComposite
      combine:
        variables:
          - fromFieldPath: spec.tenantId
        strategy: string
        string:
          fmt: "urn:zero-ops:tenant:%s:api"
      toFieldPath: spec.forProvider.manifest.spec.template.spec.containers[0].env[X].value # (Replace X with actual index of PGRST_JWT_AUD)
```

#### 2. Update the Auth Proxy / Hydra (Hub Side)
Currently, in `cmd/auth-proxy/main.go` and `internal/auth-proxy/handlers.go`, your auth proxy forces the audience to the MCP gateway:

```go
// internal/auth-proxy/handlers.go (ConsentHandler)
	acceptReq := map[string]interface{}{
		"grant_scope":                requestedScopes,
		"grant_access_token_audience": []string{h.mcpGatewayBaseURL + "/mcp"}, // <-- HARDCODED
		"session":                    session,
	}
```

To support the Spoke APIs properly, your Auth Proxy needs to read the requested audience from the OAuth 2.0 flow and grant it if valid, rather than hardcoding it:

```go
	// 1. Read requested audience from the Hydra consent request
	requestedAudience, _ := consentReq["requested_access_token_audience"].([]interface{})
	
	// 2. Default to requested audience, or fallback if empty
	grantedAudience := requestedAudience
	if len(grantedAudience) == 0 {
		grantedAudience = []interface{}{"urn:zero-ops:tenant-api"} // Default logical audience
	}

	acceptReq := map[string]interface{}{
		"grant_scope":                 requestedScopes,
		"grant_access_token_audience": grantedAudience,
		"session":                     session,
	}
```

### Summary of the Fix
By switching from the hardcoded `https://api.nutgraf.in/mcp` to a logical URN like `urn:zero-ops:tenant-api` (or a dynamic tenant-specific URN):
1. You properly implement the **OAuth 2.0 Resource Indicator standard (RFC 8707)**.
2. You eliminate the **Confused Deputy** security risk.
3. You cleanly decouple the Hub's agentic API layer from the Spokes' tenant data layers.