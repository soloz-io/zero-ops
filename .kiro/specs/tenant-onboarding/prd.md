# Product Requirements Document: Agentic Tenant Onboarding

**Version:** 1.0  
**Status:** APPROVED  
**Project Name:** Zero-Ops Agent-Native PaaS  
**Author:** Senior Platform Architecture Team  

---

## 1. Executive Summary

This initiative delivers a **Zero-Touch, Agentic Tenant Onboarding** flow for the Zero-Ops headless PaaS. Targeted at **Platform Admins**, it replaces traditional UI wizards and imperative CLIs with an AI-agent-driven interaction model (via Goose, Cursor, or Copilot). The core promise is seamless, conversational provisioning of isolated tenant workspaces—including Kubernetes namespaces, strict RBAC, ResourceQuotas, and PostgreSQL metadata—secured by a frictionless OAuth 2.0 Device Authorization flow. The platform enforces strict constraints: the agent acts as a stateless semantic router (via Direct Tool Calls), all authentication relies on Ory Kratos/Hydra without exposing long-lived credentials in local `mcp.json` files, and the backend guarantees idempotent, BYOC-ready infrastructure isolation.

---

## 2. Problem Statement

### **Current State**
Today, onboarding a tenant requires a fragmented, manual workflow. Platform Admins must log into a web UI or use bash scripts to piece together PostgreSQL inserts and Kubernetes manifests. In early agentic experiments, users suffer a degraded UX: they must manually generate API keys, copy-paste them into an `mcp.json` file, and manage token rotation by hand. If an agent hits an error (e.g., a naming constraint), the user must manually debug the YAML/JSON output and retry.

### **Gap / Motivation**
We lack a secure, native integration between Large Language Model (LLM) agents and our control plane. To scale the headless PaaS model, the agent must be able to execute onboarding autonomously, resolving intent (extracting name, tier, and email) directly from natural language. Furthermore, the authentication layer must support headless environments securely via the OAuth Device Flow, completely eliminating manual API key management for the operator.

### **Constraints & Non-Goals**
**Constraints:**
- **Thin-Client Principle:** The MCP Server must be 100% stateless.
- **Direct Tool Calls Only:** The LLM must construct single, complete JSON payloads for the API. We will *not* use stateful, interactive question-and-answer DSLs (e.g., ZeroTouch Engine).
- **Strict Isolation:** Every tenant must be isolated to a dedicated Kubernetes namespace with rigidly enforced ResourceQuotas and RBAC.

**Non-Goals:**
- Provisioning actual Hetzner/AWS compute clusters (this initiative covers the *workspace onboarding* only; Day-1 cluster creation is a separate journey).
- Building a custom Identity Provider (we are strictly utilizing Ory Kratos and Hydra).

---

## 3. User Personas & User Journeys

### **3.1 Personas**
- **Platform Admin (Primary):** The operator chatting with the AI agent (Goose/Cursor). Responsible for creating, scaling, and managing tenant boundaries on the Zero-Ops platform. Needs high velocity and zero context-switching from their IDE/terminal.
- **Tenant Developer (Secondary):** The end-user who receives the output (API Token and namespace) to begin deploying their BYOC clusters.

### **3.2 End-to-End User Journeys**

#### **Journey 1: First-Time Setup / Headless Authentication**
- **Trigger:** Admin asks the agent to perform a privileged action (e.g., "List tenants").
- **Action:** Agent attempts MCP call. Rust Gateway intercepts and returns `401 Unauthorized` with a device code. Agent displays the device auth link to the user. User authenticates in the browser.
- **System Response:** Hydra issues a token to the Agent. Agent securely stores the token in memory/keychain and retries the MCP request.
- **Error State:** User ignores the link or it times out. Agent polls until timeout, then informs the user the auth flow expired.

#### **Journey 2: Agentic Tenant Onboarding (Creation)**
- **Trigger:** Admin types: *"Onboard Acme Corp on the professional plan, admin is alice@acme.com."*
- **Action:** Agent parses intent, maps variables to the `tenant_create` JSON schema, and sends a single, stateless request to the Go MCP server.
- **System Response:** The Go REST API inserts a record into PostgreSQL, generates a K8s Namespace (`tenant-acme-corp`), applies a ResourceQuota, creates a ServiceAccount, and generates a JWT. Agent responds conversationally with the new workspace details and token.
- **Error State:** Naming conflict (e.g., "Acme Corp" results in invalid K8s namespace). API returns `400 Bad Request`. Agent natively reads the error, corrects the format to `acme-corp`, and retries autonomously.

#### **Journey 3: Day-2 Operations (Quota Update)**
- **Trigger:** Admin types: *"Upgrade Acme Corp to the Enterprise plan and set max clusters to 50."*
- **Action:** Agent executes the `tenant_update` tool. API updates the DB and K8s `ResourceQuota`. Agent confirms the new limits.

#### **Journey 4: Decommission / Offboarding**
- **Trigger:** Admin types: *"Delete the Acme Corp tenant."*
- **Action:** Agent explicitly asks for confirmation (via tool schema requiring `confirm: true`). Once provided, the API initiates a soft-delete in Postgres, adding a finalizer to the K8s namespace for cascading deletion of CAPI resources.

---

## 4. Proposed Architecture

### **4.1 High-Level Flow**

```text
                               +----------------------------------+
                               |     Agent (Goose / Cursor)       |
                               |  [ LLM Semantic Router ]         |
                               +----------------------------------+
                                                |
                                                | JSON-RPC (HTTP/Stdio)
                                                v
+-----------------------+      +----------------------------------+
|   Identity & Auth     |      |       Agent Gateway (Rust)       |
|  [Ory Hydra] (OAuth)  |<---->| [Auth Validator] [RBAC Engine]   |
|  [Ory Kratos] (Users) |      | [Device Flow]    [MCP Router]    |
+-----------------------+      +----------------------------------+
                                                |
                                                | Authenticated Tool Call
                                                v
                               +----------------------------------+
                               |        Go MCP Server             |
                               | [ tenant_create JSON Schema ]    | < Stateless translation
                               +----------------------------------+
                                                |
                                                | REST API (HTTP POST)
                                                v
                               +----------------------------------+
                               |    zero-ops-api (Go / Gin)       | < Core SaaS Logic
                               +----------------------------------+
                                         /              \
                          (SQL Insert)  /                \  (Apply Manifests)
                                       v                  v
                 +-----------------------+      +----------------------------------+
                 | PostgreSQL Database   |      | Management K8s Cluster           |
                 | - tenants table       |      | - Namespace (tenant-acme-corp)   |
                 | - users table         |      | - ResourceQuotas & RBAC          |
                 +-----------------------+      +----------------------------------+
```

### **4.2 Components & Responsibilities**

| Component | Implementation Choice | Responsibility |
| :--- | :--- | :--- |
| **Agent / Client** | Goose / Cursor (LLM) | Semantic reasoning, prompt-to-JSON mapping, user interaction. |
| **Agent Gateway** | Rust (Tokio/Hyper) | L7 Routing, enforcing OAuth Device Flow, validating JWTs, enforcing RBAC (CEL policies). |
| **Identity/Auth** | Ory Kratos & Hydra | Minting OAuth tokens, managing user identities, managing the Device Auth web UI. |
| **MCP Server** | Go (official MCP SDK) | Pure stateless translation layer. Exposes API capabilities as LLM tool schemas (`tenant_create`). |
| **zero-ops-api** | Go (Gin + client-go) | Handles core business logic. Idempotently creates DB records and K8s resources. |
| **State Layer** | PostgreSQL & K8s etcd | Source of truth for tenant metadata (DB) and infrastructure isolation boundaries (K8s). |

### **4.3 Integration & Control Plane**
- **Auth Control Plane:** Ory Hydra manages the OAuth 2.0 Device Authorization flow. The Rust Agent Gateway validates the resulting JWT using Hydra's JSON Web Key Set (JWKS).
- **Infra Control Plane:** `zero-ops-api` utilizes Kubernetes Server-Side Apply (SSA) to declaratively create Namespaces, ServiceAccounts, RoleBindings, and ResourceQuotas. This ensures that repeated agent tool calls are safely idempotent.

---

## 5. Technical Specifications

### **5.1 Platform Changes**
- **Deployments:**
  - `agent-gateway` (Rust) configured with Ory Hydra OIDC discovery URL.
  - `zero-ops-mcp-server` (Go) exposing the `tenant_*` toolsets.
  - `zero-ops-api` (Go) exposing `/api/v1/tenants`.
- **Database Migrations:** Add `tenants` and `users` tables to PostgreSQL.

### **5.2 Core Resources & APIs**
- **Naming Conventions:** All generated namespaces must match `tenant-{normalized-name}` (e.g., `tenant-acme-corp`). Must comply with RFC 1123.
- **K8s Resources Created per Tenant:**
  1. `Namespace` (`tenant-acme-corp`) with label `tenant: acme-corp`.
  2. `ResourceQuota` (`tenant-limits`) restricting `count/clusters.cluster.x-k8s.io` and `count/machines.cluster.x-k8s.io` based on the plan (Free=1/5, Pro=10/50, Ent=Custom).
  3. `ServiceAccount` (`tenant-admin-sa`) for programmatic API access.
  4. `RoleBinding` (`tenant-admin-rb`) binding `edit` and custom CAPI roles to the SA within the namespace.

### **5.3 Defaulting & Automation Logic**
- **Name Normalization:** If the LLM sends "Acme Corp!", the API must normalize it to `acme-corp` and return a `201 Created` with the *actual* namespace name in the JSON payload, so the LLM updates its context.
- **Plan Defaults:** Missing `quotas` in the payload automatically default to the limits mapped to the `plan` enum (free, professional, enterprise).

### **5.4 Operational Semantics (Lifecycle & Frequency) [REQUIRED]**
To ensure high performance and reliability, external dependencies must be handled as follows:

- **JWKS (JSON Web Key Set) Fetching (Agent Gateway):**
  - *Startup:* Fetched once upon Gateway initialization.
  - *Runtime:* Held in memory. **Never fetched per-request.**
  - *Refresh/Rotation:* Background goroutine/task refreshes the JWKS every 60 minutes, OR triggered asynchronously on a cache-miss (if a request presents a validly formatted JWT with an unknown `kid`).
- **OAuth Device Flow Polling (Goose/Client):**
  - *Event:* Triggered only when Gateway returns `401 Unauthorized` with `WWW-Authenticate` header.
  - *Frequency:* Client polls the Hydra token endpoint strictly adhering to the `interval` returned in the device authorization response (e.g., every 5 seconds). Aggressive polling is rejected.
- **Kubernetes API Interactions (`zero-ops-api`):**
  - *Reads:* Must utilize `SharedInformerFactory` caches. The API must **never** perform synchronous `client.CoreV1().Namespaces().Get()` calls against the K8s API server on the critical request path to verify existence.
  - *Writes:* Executed on-demand per request using Server-Side Apply (SSA) for idempotency.

### **5.5 Idiomatic Behavior & Anti-Patterns [REQUIRED]**
- **Idiomatic:**
  - Tools are designed as **Direct JSON Tool Calls**. The LLM infers parameters from the user's initial prompt and fills the schema.
  - Operations are highly idempotent. Calling `tenant_create` twice with the same data returns `200 OK` (or `201 Created` with existing data) without duplicating state.
- **Anti-Patterns (Implementers MUST NOT do the following):**
  - **MUST NOT** fetch configuration, JWKS, or OIDC metadata synchronously on the request path.
  - **MUST NOT** implement stateful conversational wizards (e.g., returning a "state_blob" and asking the user a sequence of questions). The LLM is the conversational engine; the MCP server must be stateless.
  - **MUST NOT** expose raw Kubernetes credentials or Kubeconfigs directly in the LLM context unless explicitly requested by a tool designed to generate a short-lived kubeconfig file.

### **5.6 Security, Compliance & Reliability**
- **TLS:** Internal communication between the Rust Agent Gateway, Go MCP Server, and Go API runs over mTLS (e.g., via Linkerd/Cilium or native TLS).
- **Isolation:** The `RoleBinding` strictly confines the tenant's `ServiceAccount` to their specific namespace. Cross-namespace reads are cryptographically and logically impossible via the API.
- **Reliability (SLO):** The `/api/v1/tenants` endpoint must respond in < 500ms (99th percentile), enabled by using Informer caches for validation rather than synchronous K8s API reads.

---

## 6. User Journey Deep Dives (Scenario-Based)

### **Scenario 1: Agentic Headless Authentication & Onboarding**
**Actors:** Platform Admin, Goose Agent.
**Preconditions:** Zero-Ops platform deployed. User has no valid session in Goose.

**Step-by-Step Flow:**
1. **User Action:** Types "Onboard Acme Corp on the pro plan."
2. **System Action:** Goose maps this to `tenant_create` and fires the tool.
3. **Platform Action:** Rust Agent Gateway detects missing JWT. Returns `401 Unauthorized` + `WWW-Authenticate` header pointing to Hydra's device auth endpoint.
4. **Agent Action:** Goose intercepts the `401`, pauses execution, and tells the user: *"Please open `https://auth.nutgraf.in/device` and enter code `F7K9-P2XL`."* Goose begins polling Hydra.
5. **User Action:** Completes login in the browser via Ory Kratos.
6. **Platform Action:** Hydra returns the Access Token to Goose.
7. **System Action:** Goose automatically retries the `tenant_create` MCP call with the JWT.
8. **Platform Action:** `zero-ops-api` provisions DB records and K8s namespace `tenant-acme-corp`. Returns `201 Created` with a new `apiToken`.
9. **Agent Action:** Goose displays a success summary and the API token to the user.

**Variations / Edge Cases:**
- *Timeout:* If the user takes longer than 15 minutes to authorize the device code, Hydra rejects the polling. Goose reports the timeout and asks if the user wants to restart the flow.

### **Scenario 2: LLM Auto-Correction of Invalid Input**
**Actors:** Platform Admin, Goose Agent.
**Preconditions:** User is authenticated.

**Step-by-Step Flow:**
1. **User Action:** Types "Create a tenant named 'Super_Corp 123!'."
2. **System Action:** Goose passes `name: "Super_Corp 123!"` to the MCP Server.
3. **Platform Action:** `zero-ops-api` attempts validation. The name violates RFC 1123 K8s namespace constraints. API returns `400 Bad Request: Name must consist of lower case alphanumeric characters or '-', and must start and end with an alphanumeric character.`
4. **Agent Action:** Goose reads the 400 error natively, realizes the mistake without bothering the user, normalizes the string to `super-corp-123`, and seamlessly retries the MCP tool call.
5. **Platform Action:** API succeeds.
6. **Agent Action:** Goose tells the user: *"I've onboarded your tenant. Note: I adjusted the name to 'super-corp-123' to comply with Kubernetes naming rules."*

---

## 7. Success Criteria & Acceptance Tests

**Functional Behavior:**
- [ ] **Direct Tool Execution:** Agent can successfully provision a tenant from a single natural language prompt without entering an interactive question-and-answer loop.
- [ ] **LLM Self-Correction:** When intentionally passing invalid characters via the agent, the agent reads the `400 Bad Request` and auto-corrects the payload successfully.
- [ ] **Idempotency:** Running the exact same agent prompt twice results in no duplicate K8s namespaces or DB entries, and the API returns a safe `200/201` response.

**Auth & Security Verifications:**
- [ ] **Device Flow Completes:** Unauthenticated requests result in a valid Device Code prompt in the Agent UI, which successfully resolves to a JWT after browser login.
- [ ] **Gateway Rejection:** Hand-crafting an MCP request with an expired or invalid JWT is rejected by the Rust Gateway in `< 50ms` without hitting the downstream Go MCP Server.

**Infrastructure Observability (Verification Commands):**
To prove the success criteria, run the following after an agentic onboarding:
```bash
# Verify K8s Namespace & Labels
kubectl get namespace tenant-acme-corp --show-labels
# Expected: EXISTS, labels include tenant=acme-corp

# Verify Quota Enforcement
kubectl get resourcequota tenant-limits -n tenant-acme-corp -o yaml
# Expected: hard limits map exactly to the requested 'professional' plan

# Verify Database Entry
kubectl exec -it deployment/postgres -n zero-ops-system -- psql -U postgres -d zeroops -c "SELECT name, plan FROM tenants WHERE name='acme-corp';"
# Expected: 1 row returned with plan='professional'
```