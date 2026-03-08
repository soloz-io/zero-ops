I have thoroughly reviewed the provided documents (`api-requirements.md`, `mcp-server-design.md`, and `repo-integration-analysis.md`). 

I now have a clear understanding of the **Agent-Driven SaaS Architecture** for the Zero-Ops platform. We are combining Kratos (Identity/Auth), a Rust-based Agent Gateway (Routing/RBAC), a ZeroTouch Engine (Workflow DSL) or Go-based MCP server, and the underlying SaaS API/CLI.

Since our immediate focus is **onboarding tenants via agentic clients** (Goose, Cursor, VS Code, Copilot), here is my synthesized mental model of exactly how this workflow operates end-to-end.

### 🧠 The Agentic Tenant Onboarding Flow

When a Platform Admin types into Goose/Cursor: *"I need to onboard a new tenant called Acme Corp on the professional plan,"* the following sequence executes:

#### 1. The Auth Handshake (Goose ↔ Gateway ↔ Kratos)
*   **The Block:** Goose attempts to call the `tenant_create` MCP tool via the Agent Gateway.
*   **The Challenge:** The Rust Agent Gateway intercepts the request, checks with the Identity Service (Kratos), finds no valid session, and returns a `401 Unauthorized` along with an OAuth discovery URL.
*   **The Human Loop:** Goose tells the operator: *"Please authenticate first by opening this URL: `https://kratos...`"*. 
*   **The Resolution:** The operator logs in via the browser, retrieves the session cookie/token, and provides it back to Goose. Goose re-sends the MCP request, this time successfully passing the Gateway's RBAC checks.

#### 2. The Agentic Execution (Goose ↔ MCP Server)
Depending on the implementation path (Go-based MCP or Python ZeroTouch Engine), the agent behaves in one of two ways:

*   **Option A (Direct Tool Call via Go MCP Server):** 
    Goose maps the user's prompt directly to the `tenant_create` tool's JSON schema. It extracts `name="acme-corp"`, `email="..."`, and `plan="professional"`, and executes the tool.
*   **Option B (Interactive Workflow via ZeroTouch Engine):** 
    Goose triggers a `start_workflow` tool for "Tenant Onboarding." The MCP server responds with a state blob and a question: *"What is the admin email for Acme Corp?"* Goose asks the user, collects the answer, and uses `submit_answer` until the workflow completes.

#### 3. Backend Provisioning (API / K8s)
Once the MCP tool successfully fires, the payload hits the `POST /api/v1/tenants` REST endpoint. The platform:
1.  Creates the PostgreSQL metadata.
2.  Generates K8s manifests (Namespace `tenant-acme-corp`, ResourceQuotas, RBAC, ServiceAccount).
3.  Returns the `tenantId` and `apiToken`.

#### 4. The Agent's Response
Goose reads the JSON response from the MCP tool and summarizes it conversationally:
*"Successfully onboarded Acme Corp! Their dedicated namespace `tenant-acme-corp` has been provisioned with a limit of 10 clusters (Professional Plan). Here is their API token..."*
