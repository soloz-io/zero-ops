# Agents-Core Implementation Pattern Guide (SBT Aligned)

## 1. Architectural Positioning
Before writing any code, the team must understand where `agents-core` lives in the architecture:
* **`open-sbt`** is the generic SaaS framework (Auth, Billing, NATS, GitOps client).
* **`agents-core`** is a **Platform Service** (your specific business domain). 
* **`mcp-server`** is the API gateway/presentation layer for `agents-core`.

`agents-core` *consumes* `open-sbt` interfaces (`IEventBus`, `IAuth`, `IProvisioner`), but `open-sbt` must never contain any code related to "Agents", "Prompts", or "LLMs".

---

## 2. Project Structure & Boundaries
To enforce this separation, your repository must be structured to prevent domain logic from leaking into the `open-sbt` framework.

```text
/internal
  ├── /opensbt/                 # STAYS GENERIC. Do not modify for agents.
  │   ├── interfaces/           # IEventBus, IProvisioner, IAuth
  │   └── providers/            # GitOps, NATS, Postgres
  │
  ├── /agents-core/             # YOUR BUSINESS DOMAIN
  │   ├── /mcp/                 # MCP Tool Definitions (The API)
  │   │   ├── create_agent.go   
  │   │   └── deploy_agent.go   
  │   ├── /service/             # Synchronous business logic (AuthZ, DB writes)
  │   │   └── agent_service.go  
  │   ├── /orchestrator/        # ★ NEW: Async worker for GitOps commits
  │   │   └── gitops_worker.go  
  │   ├── /client/              # External integrations (AgentRegistry OSS, Hub)
  │   └── /database/            # Platform-specific SQL & schema (authorized_tools)
  │
  └── /spoke-controller/        # Deployed to Spokes. Writes status to Hub.
```

---

## 3. CRITICAL CORRECTION: The `deploy_agent` Flow

### 🛑 The Deviation in Current `design.md`
In your `design.md` (Section 3.2) and `tasks.md` (Phase 4.3), the MCP Tool `deploy_agent` is instructed to synchronously generate the Agent CRD and commit it to Git via `IProvisioner.CommitManifest()`, *before* returning the MCP response.

**Why this breaks the SBT Pattern:** 
Git operations (pull, resolve conflicts, commit, push) are slow and prone to network timeouts. Tying an HTTP/MCP API response to a synchronous Git commit violates the SBT rule: **"APIs must never wait for infrastructure orchestration."**

### ✅ The SBT-Aligned Flow (The Right Way)
The MCP API must only update the database, emit an event, and return immediately. An asynchronous background worker must handle the Git commit.

**Step 1: The Synchronous MCP API (`internal/agents-core/mcp/deploy_agent.go`)**
```go
func (s *AgentService) DeployAgent(ctx context.Context, params map[string]interface{}) (interface{}, error) {
    tenantID := ctx.Value("tenant_id").(string)
    
    // 1. Create deployment record in AgentRegistry (Status: "deploying")
    deployResp, _ := s.agentRegistryClient.CreateDeployment(ctx, req)
    
    // 2. Publish intent to NATS
    s.eventBus.Publish(ctx, models.NewEvent(
        "zeroops_agentDeployRequested", // Platform event, NOT an opensbt event
        models.PlatformEventSource,
        map[string]interface{}{
            "tenant_id":        tenantID,
            "agent_id":         params["agent_id"],
            "deployment_id":    deployResp.ID,
            "spoke_cluster_id": ctx.Value("spoke_cluster_id").(string),
        },
    ))
    
    // 3. Return immediately! DO NOT COMMIT TO GIT HERE.
    return map[string]interface{}{
        "status": "deploying",
        "message": "Deployment initiated. Poll get_agent_status.",
    }, nil
}
```

**Step 2: The Asynchronous Worker (`internal/agents-core/orchestrator/gitops_worker.go`)**
```go
// This worker runs as a background goroutine in the agents-core daemon
func (w *GitOpsWorker) Start(ctx context.Context) {
    w.eventBus.SubscribeQueue(ctx, "zeroops_agentDeployRequested", "agents-core", w.handleDeploy)
}

func (w *GitOpsWorker) handleDeploy(ctx context.Context, event models.Event) error {
    // 1. Check idempotency (Inbox pattern via IStorage)
    if processed, _ := w.storage.IsEventProcessed(ctx, event.ID); processed { return nil }

    // 2. Generate CRD YAML
    agentCRD := w.generateAgentCRD(event.Detail)

    // 3. Commit to Git via IProvisioner
    _, err := w.provisioner.CommitManifest(ctx, opensbt.CommitManifestRequest{
        TenantID: event.Detail["tenant_id"].(string),
        Content:  agentCRD,
    })
    
    if err != nil {
        // Mark failed in DB via AgentRegistry API
        return err 
    }

    // 4. Publish success (Audit trail)
    w.eventBus.Publish(ctx, models.NewEvent("zeroops_agentGitCommitted", ...))
    return nil
}
```

---

## 4. The Status Synchronization Loop

Your design for the Spoke Controller accurately perfectly aligns with the SBT **"Status Controller"** pattern. Here is the strict implementation contract the team must follow:

1. **No Kubernetes API queries from the Hub.** The `get_agent_status` MCP tool must **only** query the `AgentRegistry` database via `agentRegistryClient.GetDeployment()`.
2. **Spoke Controller writes, Hub NATS reacts.** 
    * The Spoke Controller (using Kagent) watches the local Pod/ScaledObject.
    * It does a `POST /agent_infra_status` to the Hub PostgREST endpoint.
    * The Hub PostgreSQL database triggers `pg_notify`, which publishes the `hub.platform.agent.infra_status` NATS event.
3. **The Control Plane NATS Subscriber:**
    * You defined a `NATSSubscriber` in `cmd/nats-subscriber/main.go`. This is correct.
    * This daemon listens to `hub.platform.agent.infra_status` and executes an `UPDATE agentregistry.deployments SET status = $1` query.

**Implementation Rule:** If `get_agent_status` takes longer than 50ms to execute, you are doing something wrong. It should be a pure, indexed SQL read.

---

## 5. Security, Identity & Row-Level Security (RLS)

To effectively use the SBT pattern, authorization logic must be pushed down as close to the data as possible.

### 5.1 The JWT Contract
When the MCP Client (Cursor/Goose) connects to the AgentGateway, it passes a JWT generated by Ory Hydra. Ensure your `AgentGateway` injects these exact headers for the MCP server:
* `X-Auth-Tenant-ID`
* `X-Auth-Tenant-Tier`
* `X-Auth-Spoke-Cluster-ID`

### 5.2 Enforcing RLS on Database Connections
Before your `agents-core` makes *any* query to the `agents` schema (e.g., in `list_authorized_tools.go`), it MUST set the local context on the transaction.

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

### 5.3 Validating Tool Access
In `create_agent` and `update_agent`, the team must validate that a tenant is allowed to use a tool (e.g., `github_mcp`, `stripe_api`).
* **Do:** Query the `authorized_tools` table passing the `tenantID`.
* **Do Not:** Hardcode tool names or rely solely on frontend/MCP client validation. The MCP Server is the gatekeeper.

---

## 6. Summary of Action Items for the Platform Team

1. **Fix `deploy_agent` and `delete_agent`:** Move the `IProvisioner.CommitManifest` calls out of the MCP synchronous handlers. Create an `orchestrator` package in `agents-core` that listens to NATS events and does the Git commits asynchronously.
2. **Remove `list_providers`:** As correctly noted in your `design.md`, the `spoke_cluster_id` is determined during onboarding and baked into the JWT. The user does not need to select a provider.
3. **Isolate `agents-core`:** Ensure `AgentRegistryClient` and `HubClient` stay in `internal/agents-core/client/` and are not placed in `internal/opensbt/`. 
4. **Adhere to the Status Flow:** Ensure no developer attempts to use the Kubernetes Go client (`client-go`) inside the Hub MCP server to check if an agent pod is running. All status checks must read from the PostgreSQL database.