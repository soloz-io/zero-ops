**Agents-Core Implementation Pattern Guide**

This version properly distinguishes the **Dual Provisioning Path** (GitOps for platform infrastructure vs. Direct API for business agents) and accurately reflects the `Kagent Lifecycle` diagram you provided.

---

# Agents-Core Implementation Pattern Guide (SBT Aligned)

## 1. Architectural Positioning
Before writing any code, the team must understand the distinct boundaries between the SaaS framework, the platform services, and the provisioning layers:

* **`open-sbt`**: The generic SaaS framework (Auth, Billing, NATS, multi-tenant primitives). Contains zero agent logic.
* **`agents-core`**: Your Platform Service handling business domain logic. It exposes the MCP API, enforces tool/model authorization, and orchestrates the `AgentRegistry`.
* **The Dual Provisioning Path**:
  * **Platform Agents & Infrastructure (App Plane):** Deployed by platform engineers. Triggered via Control Plane events, committed to Git via `IProvisioner`, and synced via ArgoCD.
  * **Business Agents (Data Plane):** Deployed dynamically by tenants/users via MCP. Bypasses GitOps entirely. Provisioned instantly using a **Deployment Adapter** that applies the Kagent CRD directly to the Spoke Kubernetes API.

---

## 2. Project Structure & Boundaries
The repository must be structured to prevent domain logic from leaking into the generic `open-sbt` framework, while accommodating the Deployment Adapter for business agents.

```text
/internal
  ├── /opensbt/                 # 🔒 STRICT BOUNDARY: Do not modify for agents.
  │   ├── interfaces/           # IEventBus, IProvisioner, IAuth
  │   └── providers/            # NATS, Postgres, GitOps
  │
  ├── /agents-core/             # ✅ YOUR BUSINESS DOMAIN
  │   ├── /mcp/                 # MCP Tool Definitions (Presentation Layer)
  │   │   ├── create_agent.go   
  │   │   └── deploy_agent.go   
  │   ├── /service/             # Orchestration & Validation
  │   │   ├── agent_service.go  
  │   │   └── deployment_service.go 
  │   ├── /adapter/             # ★ NEW: Deployment Adapter for Business Agents
  │   │   └── k8s_spoke_adapter.go # Directly applies CRDs to Spoke K8s APIs
  │   ├── /client/              # External integrations (AgentRegistry OSS, Hub)
  │   └── /database/            # Platform-specific SQL & schema
  │
  └── /spoke-controller/        # Deployed to Spokes. Writes status to Hub.
```

---

## 3. The `deploy_agent` Flow (Business Agents)

Because Business Agents bypass GitOps, the `deploy_agent` MCP tool uses a synchronous-to-asynchronous handoff. It synchronously applies the CRD to the Spoke cluster (which takes milliseconds), but **does not wait for the Pod to boot**. 

### Step 1: The MCP Handler (Orchestration)
The handler extracts JWT claims, registers the intent in the DB, applies the CRD, and returns immediately.

```go
// internal/agents-core/service/deployment_service.go
func (s *DeploymentService) DeployAgent(ctx context.Context, params map[string]interface{}) (interface{}, error) {
    tenantID := ctx.Value("tenant_id").(string)
    spokeClusterID := ctx.Value("spoke_cluster_id").(string)
    agentID := params["agent_id"].(string)

    // 1. Create deployment record in AgentRegistry DB (Status: "deploying")
    // This calls POST /v0/deployments on AgentRegistry OSS
    deployResp, _ := s.agentRegistryClient.CreateDeployment(ctx, req)

    // 2. Materialize Kagent Agent CRD
    agentCRD := s.generateKagentCRD(agentID, tenantID, deployResp.ID)

    // 3. Deployment Adapter: Apply DIRECTLY to Spoke Cluster
    // Bypasses IProvisioner/GitOps completely for speed & scale
    err := s.k8sSpokeAdapter.ApplyCRD(ctx, spokeClusterID, agentCRD)
    if err != nil {
        return nil, fmt.Errorf("failed to apply CRD to spoke: %w", err)
    }

    // 4. Publish intent to NATS for billing/auditing (Fire and Forget)
    s.eventBus.PublishAsync(ctx, models.NewEvent(
        "zeroops_agentDeployRequested", // Platform event, NOT an opensbt_ event
        models.PlatformEventSource,
        map[string]interface{}{
            "tenant_id":        tenantID,
            "agent_id":         agentID,
            "deployment_id":    deployResp.ID,
        },
    ))

    // 5. Return immediately to the LLM/Client
    return map[string]interface{}{
        "status":        "deploying",
        "deployment_id": deployResp.ID,
        "message":       "Deployment initiated. Use get_agent_status to poll.",
    }, nil
}
```

---

## 4. The Status Synchronization Loop (Status Controller Pattern)

Even though the CRD application is direct, **status checking must remain strictly decoupled and asynchronous**. 

**Rule:** The `mcp-server` must NEVER use the Kubernetes API to check if an agent pod is running during a `get_agent_status` request. 

### The Implementation Flow:
1. **The Spoke:** The `Kagent` controller reconciles the applied CRD and provisions the Pod and KEDA ScaledObject.
2. **The Spoke Controller:** Watches the Pod/KEDA status. When it detects a change (e.g., `provisioning` -> `ready`), it executes an HTTP POST to the Hub's PostgREST endpoint using an Ory Hydra `client_credentials` token.
3. **The Hub DB:** The `agent_infra_status` table receives the update. A PostgreSQL trigger executes `pg_notify`.
4. **The NATS Subscriber (`cmd/nats-subscriber`):**
    * Listens to the `pg_notify` (forwarded to NATS).
    * Executes an `UPDATE agentregistry.deployments SET status = $1` directly in the Control Plane database.
5. **The MCP Tool (`get_agent_status`):**
    * Simply does a `GET /v0/deployments/{id}` to the `AgentRegistry` API.
    * Returns the database status instantly (<50ms).

---

## 5. Security, Identity & Row-Level Security (RLS)

To effectively use the SBT pattern, authorization logic must be pushed down as close to the data as possible.

### 5.1 The JWT Contract
When the MCP Client (Cursor/Goose) connects to the AgentGateway, it passes a JWT generated by Ory Hydra. Ensure your `AgentGateway` injects these exact headers for the MCP server:
* `X-Auth-Tenant-ID`
* `X-Auth-Tenant-Tier`
* `X-Auth-Spoke-Cluster-ID`

### 5.2 Enforcing RLS on Database Connections
Before `agents-core` makes *any* SQL query to platform-specific tables (e.g., `authorized_tools`), it MUST set the local context on the transaction so PostgreSQL can enforce Tenant Isolation.

```go
// Inside internal/agents-core/database/db.go
func (s *DBClient) WithTenant(ctx context.Context, tenantID string, fn func(db.Querier) error) error {
    tx, err := s.pool.Begin(ctx)
    // CRITICAL: SBT Pattern for RLS isolation
    tx.Exec(ctx, "SET LOCAL app.tenant_id = $1", tenantID)
    
    q := db.New(tx)
    err = fn(q)
    
    tx.Commit(ctx)
    return err
}
```

### 5.3 Validating Tool/Model Access
In `create_agent` and `update_agent`, the team must validate that a tenant is allowed to use a requested tool or LLM.
* **Do:** Query the `authorized_tools` database table passing the `tenantID` (which triggers the RLS filter).
* **Do Not:** Rely on the frontend or the MCP client to limit choices. The `agents-core` service is the ultimate gatekeeper.

---

## 6. Summary Checklist for Code Reviews

When reviewing PRs from the platform team, ensure they adhere to these rules:

* [ ] **Dual Provisioning Awareness:** Does the code correctly bypass GitOps (`IProvisioner`) when deploying Business Agents? (It must use the direct Deployment Adapter instead).
* [ ] **Domain Leakage:** Are there Agent structs in `/internal/opensbt/models`? *(Reject: Move to `/internal/agent-core/models`)*.
* [ ] **API Blocking:** Is the `deploy_agent` tool waiting for the K8s Pod to report "Ready"? *(Reject: It must return "deploying" immediately after the CRD is accepted by the Spoke K8s API).*
* [ ] **Status Violation:** Is `get_agent_status` importing `k8s.io/client-go` to check the Spoke? *(Reject: It must query the `AgentRegistry` database).*
* [ ] **RLS Bypass:** Is the database client executing a `SELECT` without calling `SET LOCAL app.tenant_id` first? *(Reject: Fix transaction scope).*
* [ ] **Event Naming:** Are custom platform events using the `opensbt_` prefix? *(Reject: Only `open-sbt` core events use that prefix. Agent events should use `zeroops_` or `hub.platform.` conventions).*