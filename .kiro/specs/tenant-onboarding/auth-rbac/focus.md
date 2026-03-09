## Phase 4 — Rust Agent Gateway (Auth & RBAC)
**Duration: 1.5–2 weeks | Risk: High**

This is the hardest phase — Rust async, Ory Hydra integration, Device Flow, JWKS caching, mTLS. It's last because it adds zero feature value until Phase 1–3 are proven, and it's the most likely to have surprises.

Deliverables:
- JWKS fetch on startup, 60-minute background refresh, async refresh on unknown `kid`
- JWT validation on every request — `< 50ms` rejection for invalid/expired tokens
- `401 Unauthorized` with `WWW-Authenticate: Bearer realm=..., device_authorization_uri=...` header on missing token
- Device Flow: proxy to Hydra's `/oauth2/device/code`, return code + verification URI to agent
- RBAC: enforce `scope` claims from JWT against tool call permissions
- mTLS between Gateway → MCP Server
- Replace hardcoded dev JWT in MCP Server with real JWT forwarded from Gateway
- Full Journey 1 (Device Flow auth) passes end-to-end with a real Hydra instance

**Exit criteria:** Unauthenticated Goose triggers Device Flow, user completes browser login, agent retries and onboards tenant successfully. Expired JWT rejected in `< 50ms`. All Phase 3 journeys still pass through the Gateway.