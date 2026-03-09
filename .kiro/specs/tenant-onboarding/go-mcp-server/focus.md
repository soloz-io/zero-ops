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