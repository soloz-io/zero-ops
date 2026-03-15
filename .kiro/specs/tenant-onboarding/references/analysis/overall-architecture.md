Here are the ASCII wireframe diagrams mapping out the architecture, user experience, and technical execution specifically for the **Agentic Tenant Onboarding Flow**.

### 1. High-Level System Architecture
This diagram shows the complete stack for the headless PaaS, moving from the user's AI assistant through the secure gateway, down to the stateless MCP server and the K8s management cluster.

```text
                               +----------------------------------+
                               |     Agent (Goose / Cursor)       |
                               |  [ LLM Semantic Router ]         |
                               +----------------------------------+
                                                |
                                                | JSON-RPC (HTTP/Stdio)
                                                v
+-----------------------+      +----------------------------------+
|   Identity Service & Auth     |      |       Agent Gateway (Rust)       |
|  [Ory Hydra] (OAuth)  |<---->| [Auth Validator] [RBAC Engine]   |
|  [Ory Kratos] (Users) |      | [Device Flow]    [MCP Router]    |
+-----------------------+      +----------------------------------+
                                                |
                                                | Authenticated Tool Call
                                                v
                               +----------------------------------+
                               |        Go MCP Server             |
                               | [ tenant_create JSON Schema ]    | < Stateless translation
                               | [ cluster_create JSON Schema]    |   layer (No local state)
                               +----------------------------------+
                                                |
                                                | REST API (HTTP POST)
                                                v
                               +----------------------------------+
                               |    zero-ops-api (Go / Gin)       | < Core SaaS Business
                               |  [Tenant Service] [K8s Client]   |   Logic & Provisioning
                               +----------------------------------+
                                         /              \
                          (SQL Insert)  /                \  (Apply Manifests)
                                       v                  v
                 +-----------------------+      +----------------------------------+
                 | PostgreSQL Database   |      | K8s Management Cluster           |
                 | - tenants table       |      | - Namespaces (tenant-acme-corp)  |
                 | - users table         |      | - ResourceQuotas & RBAC          |
                 | - billing/quotas      |      | - Cluster API (CAPI) Controllers |
                 +-----------------------+      +----------------------------------+
```

---

### 2. The Agentic User Experience (UX Wireframe)
Because this is a **Headless PaaS**, the "UI" is the chat window. This wireframe shows how the LLM seamlessly handles the OAuth Device Authorization Flow and executes the direct tool call without kicking the user into an imperative CLI wizard.

```text
=======================================================================================
|  [ Goose AI Assistant ]                                                           |
=======================================================================================
|                                                                                     |
|  USER:  Onboard a new tenant called "Acme Corp" on the professional plan.           |
|         The admin email is alice@acme.com.                                          |
|                                                                                     |
|  GOOSE: ⏳ Connecting to Zero-Ops Gateway...                                         |
|  GOOSE: 🔒 Authentication Required.                                                  |
|         Please authorize this agent to execute infrastructure commands.             |
|                                                                                     |
|         1. Open your browser: https://auth.nutgraf.in/device                       |
|         2. Enter the code:  F7K9-P2XL                                               |
|                                                                                     |
|  [... User opens browser, authenticates via Kratos, and approves access ...]        |
|                                                                                     |
|  GOOSE: ✅ Authentication successful!                                                |
|  GOOSE: ⚙️ Executing tool: `tenant_create`                                           |
|         Payload: {                                                                  |
|           "name": "acme-corp",                                                      |
|           "email": "alice@acme.com",                                                |
|           "plan": "professional"                                                    |
|         }                                                                           |
|                                                                                     |
|  GOOSE: 🎉 Success! Acme Corp has been onboarded to the Professional Plan.          |
|                                                                                     |
|         Here are their details:                                                     |
|         - Workspace ID: tenant-acme-corp                                            |
|         - Quotas Applied: Max 10 Clusters, 50 Nodes                                 |
|         - Admin API Token generated for alice@acme.com.                             |
|                                                                                     |
|         Would you like me to provision their first cluster now?                     |
=======================================================================================
```

---

### 3. Execution Sequence Diagram (Tenant Onboarding)
This sequence shows the exact API calls from the moment Goose tries to act, to the OAuth challenge, to the successful provisioning of K8s resources.

```text
   Goose Agent        Rust Gateway        Ory Hydra/Kratos        Go MCP Server        zero-ops API (K8s)
       |                   |                     |                      |                     |
       | 1. Call Tool      |                     |                      |                     |
       | `tenant_create`   |                     |                      |                     |
       |------------------>|                     |                      |                     |
       |                   | 2. Check JWT        |                      |                     |
       |                   |-------------------->|                      |                     |
       |                   |<--------------------|                      |                     |
       | 3. 401 Unauth +   |    (No Session)     |                      |                     |
       | Device Auth Code  |                     |                      |                     |
       |<------------------|                     |                      |                     |
       |                   |                     |                      |                     |
       | 4. User logs in via Browser (Out of Band OAuth Device Flow)    |                     |
       |........................................>|                      |                     |
       |                   |                     |                      |                     |
       | 5. Retry Tool     |                     |                      |                     |
       | (with valid JWT)  |                     |                      |                     |
       |------------------>| 6. Validate JWT     |                      |                     |
       |                   |-------------------->|                      |                     |
       |                   |<--------------------|                      |                     |
       |                   | 7. Route MCP Req    |                      |                     |
       |                   |------------------------------------------->|                     |
       |                   |                     |                      | 8. POST /api/v1/tenants
       |                   |                     |                      |-------------------->|
       |                   |                     |                      |                     |--- Insert DB Record
       |                   |                     |                      |                     |--- Create K8s NS
       |                   |                     |                      |                     |--- Create RBAC/Quota
       |                   |                     |                      | 9. 201 Created      |
       |                   |                     |                      |<--------------------|
       | 10. JSON Response |                     |                      |                     |
       |<---------------------------------------------------------------|                     |
       |                   |                     |                      |                     |
```

---

### 4. Data & Resource Topology (Post-Onboarding State)
This diagram illustrates the actual infrastructure state created by the API. It shows the multi-tenant isolation principles (DB vs. Kubernetes Namespace).

```text
[ SaaS Backend State Post-Onboarding ]

+-----------------------------------------------------------------+
|  PostgreSQL Database (zero-ops-db)                              |
|                                                                 |
|  [Table: tenants]                                               |
|  +------------------+-----------------+--------------+--------+ |
|  | id               | name            | plan         | status | |
|  +------------------+-----------------+--------------+--------+ |
|  | tenant-acme-corp | Acme Corp       | professional | active | |
|  +------------------+-----------------+--------------+--------+ |
|                                                                 |
|  [Table: users]                                                 |
|  +------------------+------------------+-----------+            |
|  | tenant_id        | email            | role      |            |
|  +------------------+------------------+-----------+            |
|  | tenant-acme-corp | alice@acme.com   | admin     |            |
|  +------------------+------------------+-----------+            |
+-----------------------------------------------------------------+

                              |
                          (Maps to)
                              v

+-----------------------------------------------------------------+
|  Management Cluster (Kubernetes / Cluster API)                  |
|                                                                 |
|  +-----------------------------------------------------------+  |
|  | Namespace: tenant-acme-corp                               |  |
|  | Labels: tenant=acme-corp, managed-by=zero-ops             |  |
|  |                                                           |  |
|  |  [ResourceQuota: tenant-limits]                           |  |
|  |   - count/clusters.cluster.x-k8s.io: 10                   |  |
|  |   - count/machines.cluster.x-k8s.io: 50                   |  |
|  |   - requests.cpu: 200                                     |  |
|  |   - requests.memory: 500Gi                                |  |
|  |                                                           |  |
|  |  [ServiceAccount: tenant-admin-sa]                        |  |
|  |   - Holds programmatic API tokens                         |  |
|  |                                                           |  |
|  |  [RoleBinding: tenant-admin-rb]                           |  |
|  |   - Restricts Alice/SA to 'tenant-acme-corp' ONLY         |  |
|  |   - Grants: create clusters, read clusterclasses          |  |
|  |                                                           |  |
|  |  (Future home of Acme Corp's Hetzner API Secrets &        |  |
|  |   CAPI Cluster/Machine deployments)                       |  |
|  +-----------------------------------------------------------+  |
+-----------------------------------------------------------------+
```