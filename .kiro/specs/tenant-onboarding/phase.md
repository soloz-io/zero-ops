Let me read the PRD first before answering.Good, I've read the full PRD. Here's my honest take on how to phase this — not just a mechanical split, but with the *reasoning* for each boundary.

---

## The Core Principle for Splitting

Each phase should be **independently deployable, independently testable, and produce a working vertical slice** — not a horizontal layer. The trap most teams fall into is building all infrastructure first (DB, K8s, auth) and only integrating at the end. That defers risk.

The right question for each phase boundary is: **"Can I demo this to a skeptic without mocking the next phase?"**

---

## Phase 0 — Contracts & Local Dev Foundation
**Duration: 1–2 days | Risk: Low**

Before writing any handler code, lock the things that are expensive to change later:

- **JWT claims schema** — exactly which claims flow from Kratos → Hydra → Gateway → API (`sub`, `org_id`, `email`, `scope`). Write this as a markdown spec, not code yet.
- **OpenAPI spec for `POST /api/v1/tenants`** — request body, response envelope, error shapes. This is your contract between Go API and Go MCP Server.
- **MCP Tool JSON Schema** — the exact `tenant_create` and `tenant_get` tool definitions the LLM will see.
- **DB migration file** — `tenants` table with `id`, `org_id`, `name`, `plan`, `status`, `created_at`. Don't run it yet, just define it.
- **Docker Compose skeleton** — Postgres + a mock Hydra/Kratos stub so every developer can run locally without cloud dependencies.

**Why this phase exists:** The MCP tool schema, API contract, and JWT claims shape are load-bearing. Getting them wrong after Phase 1 is built means refactoring handler signatures, MCP wrappers, and test fixtures simultaneously. One afternoon of contract work now saves a week later.

**Exit criteria:** OpenAPI spec reviewed, MCP tool schema reviewed, JWT claims doc signed off. No running code required.

---

## Phase 1 — `zero-ops-api`: Core Tenant CRUD (No K8s, No Auth)
**Duration: 1 week | Risk: Low**

Build the REST API in complete isolation — pure Go/Gin with Postgres only. No Kubernetes. No auth middleware. Hardcoded test JWT in a middleware stub that injects a fake `org_id` into context.

Deliverables:
- `POST /api/v1/tenants` — validates RFC 1123, writes to Postgres, returns `201` with `{id, name, org_id, plan, status}`
- `GET /api/v1/tenants` and `GET /api/v1/tenants/:id`
- `PATCH /api/v1/tenants/:id` — plan/quota updates
- `DELETE /api/v1/tenants/:id` — soft delete only (sets `status=deleting`)
- Idempotency: duplicate `POST` with same name returns `200` with existing record + `"created": false` in body
- Name normalization: `400` with descriptive RFC 1123 error message (no silent mutation)
- Integration tests hitting a real Postgres (Docker Compose) — not mocks

**What's deliberately excluded:** K8s calls, real auth, token generation. The K8s provisioning and auth are the two highest-risk integrations. Excluding them here means your API logic, DB schema, and error handling are battle-tested before you add complexity.

**Exit criteria:** `curl -X POST /api/v1/tenants` with valid and invalid payloads behaves exactly per spec. CI green. You can demo tenant CRUD to a skeptic with no mocks except auth.

---

## Phase 2 — K8s Provisioning Integration
**Duration: 1 week | Risk: Medium**

Add the Kubernetes side-effects to the API handlers you built in Phase 1. This is the highest-risk integration because K8s SSA, Informer caches, and RBAC are all non-trivial.

Deliverables:
- `SharedInformerFactory` setup — namespace cache, never sync `Get()` on request path
- On `POST /api/v1/tenants` success: SSA apply of `Namespace`, `ResourceQuota`, `RoleBinding`, `ServiceAccount` — in that order, each idempotent
- Plan → quota mapping (Free/Pro/Enterprise hardcoded defaults, overridable via `quotas` object in payload)
- On `DELETE`: add finalizer to namespace, set `status=deleting` in DB
- K8s integration tests using `envtest` (controller-runtime's local API server) — no real cluster needed in CI
- The `kubectl` verification commands from PRD section 7 pass against a local `kind` cluster

**Critical note:** The `ServiceAccount` token is **not** returned in this response. You provision the SA here. Token issuance is Phase 3. This keeps provisioning idempotent and secrets out of logs.

**Exit criteria:** Full agentic onboarding works end-to-end (minus real auth and minus token in response) against a local `kind` cluster. The three `kubectl` verification commands from the PRD pass.

---

## Phase 3 — Go MCP Server (Stateless Translation Layer)
**Duration: 3–4 days | Risk: Low**

Now that the API is solid and tested, build the thin MCP wrapper. This should be the easiest phase because the hard decisions are already made.

Deliverables:
- `tenant_create` tool — maps to `POST /api/v1/tenants`, passes hardcoded dev JWT
- `tenant_get` tool — maps to `GET /api/v1/tenants/:id`
- `tenant_update` tool — maps to `PATCH /api/v1/tenants/:id`
- `tenant_delete` tool — maps to `DELETE /api/v1/tenants/:id` with `confirm: true` required in schema
- `auth_token_create` tool — calls `POST /api/v1/tenants/:id/tokens` (new endpoint), returns SA token. This is the only place a secret touches the MCP layer.
- Error passthrough: MCP server forwards `400`/`404`/`409` HTTP errors as MCP error responses with the full description — this is what lets the LLM self-correct
- End-to-end test: point a local Goose/Claude Desktop at the MCP server, run Journey 2 manually

**Why this is late in the order:** MCP is a thin adapter. Building it before the API is stable means you're testing the adapter and the API simultaneously, making failures hard to diagnose.

**Exit criteria:** A real LLM agent (Goose or Claude Desktop) can onboard a tenant via natural language against your local stack. Journey 2 from the PRD works. Journey 4 (delete with `confirm: true`) works.

---

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

---

## Phase 5 — Hardening, Observability & SLO Verification
**Duration: 3–4 days | Risk: Low**

This isn't "nice to have" — it's what makes the PRD's SLO (`< 500ms p99`) verifiable.

Deliverables:
- Structured logging (request ID, `org_id`, `tenant_id`, duration) on all three Go services
- Prometheus metrics: request latency histograms, K8s SSA duration, DB query duration
- Load test: `POST /api/v1/tenants` at 50 RPS, verify p99 `< 500ms` with Informer cache active
- Runbook: what happens when Hydra is down? When K8s API server is slow? Document the degraded behavior.
- All PRD acceptance tests from section 7 automated in CI

---

## Phase Map Summary

```
Phase 0  │ Contracts        │ JWT schema, OpenAPI, MCP tool schema, DB migration
Phase 1  │ API Core         │ CRUD + Postgres + validation + idempotency (no K8s, no auth)
Phase 2  │ K8s Integration  │ SSA provisioning + Informer cache + envtest CI
Phase 3  │ MCP Server       │ Thin translation layer + LLM agent E2E test
Phase 4  │ Rust Gateway     │ Device Flow + JWT validation + RBAC + mTLS
Phase 5  │ Hardening        │ Observability + SLO verification + load testing
```

Each phase gate is a demo, not a document. If you can't show it running, it's not done.

The highest-leverage decision in this ordering is **Phase 0** — the contracts. Everything else flows from those definitions, and changing them mid-stream is the most common cause of multi-week delays in platform projects like this.