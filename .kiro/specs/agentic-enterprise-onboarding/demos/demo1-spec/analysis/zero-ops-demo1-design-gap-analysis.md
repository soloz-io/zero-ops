# Demo 1 Design — Gap Analysis

**Document reviewed:** `design.md` — OAuth2 Authorization Code Flow with PKCE  
**Date:** 2026-03-13  
**Context:** One full-stack engineer, one day to demo. Management cluster already running (Talos/CAPI/ArgoCD/CNPG). Cursor MCP client not yet started.

**Verdict: NOT YET implementation-ready.** No showstoppers, but 4 High gaps will block the demo if unresolved before a line of code is written. 7 Medium gaps will cause mid-day rework. All are actionable and closable before implementation starts.

---

## Gap Summary

| ID | Severity | Area | Title |
|---|---|---|---|
| [G-01](#g-01) | **High** | auth-proxy | Service renamed in design but requirements use `identity-service` — naming divergence throughout |
| [G-02](#g-02) | **High** | auth-proxy / Cursor | Login flow design assumes a browser-rendered form, but a headless PKCE flow from an IDE needs no form UI |
| [G-03](#g-03) | **High** | AgentGateway | JWKS fetch URL points to Hydra internal service — but AgentGateway spec says it fetches via identity-service, not Hydra directly |
| [G-04](#g-04) | **High** | Secrets | Database passwords are plain Kubernetes Secrets with no creation mechanism defined — nothing will deploy on Day 1 without them |
| [G-05](#g-05) | **Medium** | Hydra config | `dsn` in Helm values uses `${HYDRA_DB_PASSWORD}` — Helm does not perform env-var substitution; this will fail silently |
| [G-06](#g-06) | **Medium** | auth-proxy | `mcp-public-client` registration is described as "on startup" — idempotency mechanism (check-before-create vs upsert) is undefined |
| [G-07](#g-07) | **Medium** | auth-proxy | Consent flow fetches Kratos traits to inject `tenant_id` — but at Demo 1 stage, new users have no `tenant_id` trait yet (that comes in Demo 4); consent will inject a null/empty claim |
| [G-08](#g-08) | **Medium** | AgentGateway | JWKS cache uses 1-hour TTL but also triggers refresh on key-id mismatch — the two strategies are described separately; the interaction (TTL reset on mismatch-refresh?) is unspecified |
| [G-09](#g-09) | **Medium** | Cursor | Token storage spec stores `access_token`, `refresh_token`, `expires_in` — `id_token` is present in the token response but not mentioned for storage; required for Demo 1 retry? |
| [G-10](#g-10) | **Medium** | Deployment | ArgoCD sync-waves are explicitly rejected ("services retry until dependencies ready") — but CNPG must be ready before Hydra/Kratos/Keto can connect; retry loops will produce noisy failed pods for several minutes and may confuse the Day 1 demo |
| [G-11](#g-11) | **Medium** | auth-proxy | `KETO_READ_URL` is listed in auth-proxy config but Keto is out of scope for Demo 1 (no permission checks until Demo 2) — unused dependency adds startup failure risk |
| [G-12](#g-12) | **Low** | OAuth metadata | `scopes_supported` in `oauth-protected-resource` omits `offline_access` and `openid`, both of which appear in the auth request scope — response is inconsistent with the actual client registration |
| [G-13](#g-13) | **Low** | Token response | Token response example includes `openid` in scope and an `id_token` field, but `openid` is not in the requirements spec scope list (`tenant:read tenant:write cluster:read cluster:write offline_access`) — intentional addition or drift? |
| [G-14](#g-14) | **Low** | Rollout plan | Phase 3 ("Day 1 Evening") says "Configure Cursor with AgentGateway URL" — no spec for what configuration file or environment variable Cursor reads this from |
| [G-15](#g-15) | **Low** | Demo validation | Success criterion 9 says "Cursor retries MCP tool call with JWT → succeeds" — but `zero_ops_api` backend is out of scope for Demo 1; what does "succeeds" mean with no backend to receive the request? |

---

## High

> Will block the demo if unresolved before implementation starts.

---

### G-01

**Area:** Naming — auth-proxy vs identity-service

The requirements document consistently uses the term `identity-service` for the Python service that proxies the Ory stack. This design document introduces a new name, `auth-proxy`, and specifies it in Go (`cmd/auth-proxy/`). These are used interchangeably in some places (the Glossary refers to "identity-service: Python service layer") but this design makes it Go.

This is not just a naming issue — it is a language and responsibility change. If the implementation engineer reads the requirements and the design simultaneously, they will find contradictions on: language (Python vs Go), service name, and monorepo location.

**Questions:**
1. Is `auth-proxy` the final service name, replacing `identity-service` from the requirements? If yes, update the requirements glossary and all AC references before implementation starts to prevent future confusion.
2. Is Go confirmed as the implementation language? The requirements say Python. If Go is the decision, state the rationale explicitly so it is not relitigated mid-sprint.
3. Is `cmd/auth-proxy/` the agreed monorepo location, or should it be `cmd/identity-service/` to match the requirements?

---

### G-02

**Area:** auth-proxy login UI — headless PKCE vs browser form

The design specifies a full browser-rendered login flow:
- `GET /login` — "Login UI (Hydra redirect target)"
- `POST /login` — "Process login, call Kratos, accept/reject login request"
- Step 4 in the PKCE sequence: "auth-proxy displays login form / User enters email/password"

For a **browser-based PKCE flow from an IDE**, the login UI is the **Kratos self-service UI**, not a custom auth-proxy form. Kratos has its own `selfservice.flows.login.ui_url` which the design already configures as `https://console.zero-ops.io/login`. This is the correct pattern — Hydra redirects to Kratos, and Kratos handles the login UI independently.

If auth-proxy owns the login form, it is reimplementing what Kratos already does, including session cookie management, CSRF protection, and flow token handling. This is a significant scope expansion for Day 1 and introduces subtle bugs (Kratos flow tokens expire in minutes; a custom form must honour them correctly).

**Questions:**
1. Should the login flow be: Hydra → Kratos self-service UI (at `console.zero-ops.io/login`) → Kratos → auth-proxy `acceptOAuth2LoginRequest`? If yes, auth-proxy's login endpoint only accepts the Hydra login challenge, calls Kratos to validate an existing session, and accepts/rejects — it does not render a form.
2. For Demo 1 (single engineer, one day), is it acceptable to use Kratos's built-in login UI with a minimal Kratos UI container, rather than building a custom form? This removes a full UI component from Day 1 scope.
3. If auth-proxy must own the login form: what is the Kratos flow token lifecycle and how does the form submit back to Kratos' `POST /self-service/login` endpoint? This is not specified in the design.

---

### G-03

**Area:** AgentGateway JWKS fetch target

The requirements spec (Req 10) states: "AgentGateway fetches JWKS from **identity-service** (which proxies Hydra), NOT directly from Hydra."

This design specifies the opposite:
```go
JWKS_URL = "http://ory-hydra-public.ory-system.svc.cluster.local:4444/.well-known/jwks.json"
```

AgentGateway is configured to fetch JWKS directly from Hydra's internal service. The auth-proxy (`/.well-known/oauth-authorization-server`) correctly proxies Hydra's metadata, but the `jwks_uri` in that metadata response points to `https://auth.zero-ops.io/.well-known/jwks.json` — the public-facing URL, not auth-proxy's internal endpoint.

These are two different architectures. In the direct-Hydra model, AgentGateway is coupled to Hydra's internal address. In the proxy model, AgentGateway only talks to auth-proxy, and Hydra is fully internal.

**Questions:**
1. Confirm the intended JWKS fetch path: AgentGateway → auth-proxy → Hydra (proxy model), or AgentGateway → Hydra directly (direct model)?
2. If the proxy model: does auth-proxy expose `GET /.well-known/jwks.json` as a passthrough to Hydra? This endpoint is missing from the auth-proxy endpoint list in the design.
3. If the direct model: update Req 10 to remove the "NOT directly from Hydra" constraint, as the design contradicts it.

---

### G-04

**Area:** Database password Secrets — no creation mechanism

The design specifies:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: identity-postgres-passwords
  namespace: ory-system
data:
  hydra-password: <base64>
  kratos-password: <base64>
  keto-password: <base64>
```

And states Ory Helm charts reference these via `existingSecret`. But the design does not specify:
- How this Secret is created (kubectl manually? KSOPS-encrypted in Git? Helm hook?)
- Who creates it (CLI bootstrap step? Platform engineer manually before ArgoCD sync?)
- How the CNPG `Database` CRDs reference these same passwords for the PostgreSQL users

Without this Secret existing before ArgoCD syncs the Helm charts, all three Ory components will fail to start with database connection errors. This is the most likely cause of a failed Day 1 demo.

**Questions:**
1. What is the creation mechanism for `identity-postgres-passwords`? For a GitOps-first platform, the natural answer is KSOPS-encrypted in `manifests/platform-identity/` — but the Master Platform Age Key (Req 15) is not deployed until Demo 3. Is this a chicken-and-egg problem for Demo 1?
2. Are the passwords in the `Secret` the same passwords used in the CNPG `Database` CRDs for PostgreSQL role creation? If yes, specify how CNPG's `Database` CR references the Secret for password rotation.
3. For Day 1 specifically: is it acceptable to create this Secret imperatively via `kubectl create secret` as a documented bootstrap step, with a note to migrate to KSOPS in Demo 3?

---

## Medium

> Will cause mid-day rework or a confusing demo if unresolved. Fix before implementation starts.

---

### G-05

**Area:** Hydra Helm values — env-var substitution in DSN

The Hydra values.yaml contains:
```yaml
dsn: postgres://hydra:${HYDRA_DB_PASSWORD}@identity-postgres-rw...
```

Helm does not perform shell-style `${VAR}` substitution. This value will be passed to Hydra literally, causing a database connection failure on startup. The correct pattern is to use `existingSecret` in the Hydra Helm chart, which reads the password from the Kubernetes Secret and sets the `DSN_SECRET` env var at pod startup.

The same issue exists in the Kratos and Keto values (`${KRATOS_DB_PASSWORD}`, `${KETO_DB_PASSWORD}`).

**Action:** Replace DSN values with `existingSecret` references per the Ory Helm chart documentation. Example:
```yaml
hydra:
  config:
    dsn: postgres://hydra:$(HYDRA_DB_PASSWORD)@...
  extraEnv:
    - name: HYDRA_DB_PASSWORD
      valueFrom:
        secretKeyRef:
          name: identity-postgres-passwords
          key: hydra-password
```
Or use the chart's native `existingSecret` support — confirm which pattern the specific chart version uses.

---

### G-06

**Area:** auth-proxy — client pre-registration idempotency

The design states `mcp-public-client` is registered via "auth-proxy calls Hydra Admin API on startup." If auth-proxy restarts (pod eviction, rollout), it will attempt to register the client again. The design does not specify whether this is a `GET` then `PUT` (upsert), a `POST` with 409-handling, or a Hydra-idempotent call.

Hydra's `POST /admin/clients` returns HTTP 409 if the `client_id` already exists. Without explicit 409 handling, a restarted auth-proxy will log an error on every startup even when everything is working correctly.

**Questions:**
1. Should registration use: `GET /admin/clients/mcp-public-client` first, and only `POST` if 404? Or `PUT /admin/clients/mcp-public-client` (upsert)?
2. Should auth-proxy treat a 409 on client registration as a success (already registered) and continue startup without error?

---

### G-07

**Area:** Consent flow — empty `tenant_id` for new users

Step 5 of the PKCE sequence shows the consent handler injecting:
```json
"session": {
  "access_token": { "tenant_id": "acme-corp" },
  "id_token": { "tenant_id": "acme-corp", "email": "...", "role": "..." }
}
```

For Demo 1, no tenant has been created yet (Demo 4 is `tenant_create`). Every user authenticating in Demo 1 will have no `tenant_id` in their Kratos identity traits. If auth-proxy tries to read `tenant_id` from Kratos traits and it is absent, the behaviour is undefined: it might inject `null`, `""`, or crash.

The requirements spec handles this correctly — Req 2 AC13 says the JWT "contains tenant_id" but a new user legitimately has no tenant yet. The JWT must still be valid for the Demo 1 use case (proving authentication works), even with an absent `tenant_id`.

**Questions:**
1. When `tenant_id` is absent from Kratos traits, should the consent handler omit the `tenant_id` claim from the JWT entirely, or inject an empty string? The AgentGateway must tolerate a missing `tenant_id` claim for unauthenticated-but-valid tokens.
2. Should auth-proxy treat missing optional traits (tenant_id, role) as `null` and omit them from the session object, rather than injecting empty strings that downstream checks might misinterpret?
3. For Demo 1 specifically: the demo user needs a pre-seeded Kratos identity with at least `email` and `role` set. Is there a seed script or a defined admin setup step?

---

### G-08

**Area:** AgentGateway JWKS cache — TTL reset on mismatch-refresh

The design specifies two JWKS refresh triggers:
1. TTL expiry (1 hour)
2. Key-id mismatch (signature validation failure → refresh once → retry)

The interaction between these two is unspecified. Specifically: when a mismatch-triggered refresh occurs at T+30min, does the TTL clock reset to T+90min, or does the original TTL still expire at T+60min?

If the TTL does not reset, the cache may expire 30 minutes after a successful key-rotation refresh, causing an unnecessary extra fetch. If the TTL always resets, a malicious or buggy key-id could force continuous refreshes.

**Questions:**
1. When a mismatch-triggered JWKS refresh succeeds, does the cache TTL reset to now + 1h?
2. Is there a minimum interval between mismatch-triggered refreshes to prevent a thundering herd if many requests arrive with a mismatched key-id simultaneously (e.g., during key rotation)?

---

### G-09

**Area:** Token storage — `id_token` not mentioned

The token response includes an `id_token` (because `openid` scope is requested). Step 7 specifies storing `access_token`, `refresh_token`, and `expires_in` in the OS keychain. `id_token` is not mentioned.

For Demo 1, the `id_token` may not be needed (all calls use the `access_token`). But if the demo success criterion includes "show JWT in keychain," the engineer needs to know what to store and what to show.

**Questions:**
1. Should `id_token` also be stored in the keychain, or discarded after use?
2. For the demo, which token is shown to stakeholders as proof of authentication — the `access_token`, the `id_token`, or both?

---

### G-10

**Area:** ArgoCD sync-wave strategy — startup noise on Day 1

The design explicitly rejects sync-waves: "No sync-wave dependencies — services retry until dependencies ready."

In practice, Hydra, Kratos, and Keto will all attempt to run database migrations on startup. If CNPG is not ready (which takes 2–3 minutes for a 3-node cluster to initialise), the Ory pods will crash-loop with `connection refused` errors. Kubernetes will apply exponential backoff. By the time CNPG is ready, the Ory pods may be in a 5-minute backoff window.

For a single-engineer, one-day timeline, this silent crash-loop is a time sink. The engineer will see CrashLoopBackOff and spend time debugging what is actually just a startup ordering issue.

**Questions:**
1. Is an explicit ordering mechanism acceptable for Day 1, even if it is removed later? Options: (a) Helm `initContainer` that waits for CNPG to accept connections; (b) a single sync-wave (`wave: 0` for CNPG, `wave: 1` for Ory); (c) an ArgoCD `PostSync` hook on the CNPG app that signals readiness.
2. Alternatively: is the expected startup time documented so the engineer knows to wait 10 minutes and does not treat crash-loops as real failures?

---

### G-11

**Area:** auth-proxy — Keto dependency out of scope for Demo 1

auth-proxy's configuration includes `KETO_READ_URL` and the code structure includes `client/keto.go`. Keto is listed as out of scope for Demo 1 (no permission checks until Demo 2, Req 3).

If auth-proxy attempts to connect to Keto on startup (health check, connection pool), and Keto is not yet configured or has no data, auth-proxy may fail to start or log confusing errors during Demo 1.

**Questions:**
1. Should `keto.go` and `KETO_READ_URL` be deferred entirely to Demo 2, keeping auth-proxy's Demo 1 scope strictly to login/consent/metadata?
2. If the Keto client is included in the Demo 1 build, is the connection optional (lazy init on first use) so a missing Keto does not prevent startup?

---

## Low

> Editorial or minor inconsistencies. Fix before the design is baselined, but will not block the demo.

---

### G-12

**Area:** `oauth-protected-resource` — missing scopes

The `/.well-known/oauth-protected-resource` response specifies:
```json
"scopes_supported": ["tenant:read", "tenant:write", "cluster:read", "cluster:write"]
```

The actual auth request includes `offline_access` and `openid` in scope. These are absent from `scopes_supported`. A strict MCP client implementation that checks whether requested scopes are advertised will fail to include `offline_access` and `openid`, breaking the refresh token and id_token flows.

**Action:** Add `offline_access` and `openid` to the `scopes_supported` array, or confirm they are intentionally excluded (e.g., they are standard OIDC scopes not requiring advertisement).

---

### G-13

**Area:** `openid` scope — present in design, absent in requirements

The token response and auth request in this design include `openid` scope and an `id_token` field. The requirements spec's scope list is `tenant:read tenant:write cluster:read cluster:write offline_access` — `openid` is not listed.

This may be intentional (OIDC is a practical addition), but if it is undocumented, the requirements and design will diverge permanently.

**Action:** Confirm whether `openid` is intentionally added to the scope list. If yes, update Req 2 AC4 scope list and Req 2 AC15 token response fields to include it.

---

### G-14

**Area:** Cursor configuration — AgentGateway URL source undefined

Phase 3 of the rollout plan says "Configure Cursor with AgentGateway URL." For the demo, the engineer needs to know exactly what to configure and where. The design does not specify: an environment variable, a `.cursor/mcp.json` file, a Claude Desktop config, or a hardcoded constant.

**Action:** Specify the exact configuration mechanism. For Cursor MCP: the server URL is typically set in `~/.cursor/mcp.json` or equivalent. Confirm the file, the key name, and the value format expected.

---

### G-15

**Area:** Demo success criterion 9 — "succeeds" against a missing backend

Success criterion 9: "Cursor retries MCP tool call with JWT → succeeds."

`zero_ops_api` is out of scope for Demo 1. AgentGateway will validate the JWT successfully but will have no backend to route the request to. The call will return an error (likely HTTP 502 or connection refused from AgentGateway's perspective).

**Action:** Clarify criterion 9. Options: (a) deploy a stub/echo backend that returns HTTP 200 for any request, specifically for Demo 1 validation; (b) rewrite criterion 9 as "AgentGateway returns HTTP 200 to a test endpoint (not an MCP tool call)" using a health endpoint that does not require a backend; (c) accept that the demo ends at step 8 (token stored in keychain) and step 9 is deferred to Demo 2.
