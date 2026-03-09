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