# Agents Core - Technical Design

**Spec ID:** agents-core  
**Status:** Draft  
**Created:** 2026-03-25  
**Last Updated:** 2026-03-25

## 1. Overview

This design implements the Agents Core MCP server that exposes agent lifecycle management capabilities to MCP clients (Cursor, Goose, Claude Desktop). The implementation follows the Hub-Spoke architecture with distributed identity and PostgREST-based status synchronization.

**IMPORTANT - Dual Provisioning Paths:**
- **Business Agents (tenant-created via MCP)**: AgentRegistry → Deployment Adapter → Direct Kubernetes API apply (NO GitOps, NO ArgoCD)
- **Infrastructure Agents (platform team)**: Direct Git commits → ArgoCD → Spoke cluster (GitOps only)

**IMPORTANT - opensbt Architectural Alignment:**
This design assumes the opensbt Control Plane follows the correct SBT patterns:
- ✅ Tenant creation uses TenantRegistration → Event → Provisioning flow (not synchronous)
- ✅ Tenant status reads from PostgreSQL cache (not direct ArgoCD API queries)
- ✅ opensbt is a generic SaaS toolkit (agents-core is a separate platform service)
- ✅ Event-driven provisioning with `opensbt_onboardingRequest` NATS events

**Reference:** `memory/opensbt-architecture-guide.md` - Definitive opensbt patterns

### 1.1 Design Principles

- **MCP-First**: All capabilities exposed via MCP tools (no web UI)
- **Direct Kubernetes API for Business Agents**: AgentRegistry uses Deployment Adapter to apply CRDs directly (no GitOps)
- **Status via AgentRegistry**: AgentRegistry API is single source of truth for deployment status
- **NATS for Side Effects**: Billing/notifications only (not in provisioning path)
- **Single MCP Server**: Unified `cmd/mcp-server/` with all tools
- **Interface-Based**: Uses `internal/opensbt/` abstractions
- **Synchronous Deployment**: Agent deployment applies CRD directly, returns immediately with deployment_id

### 1.2 Architecture Context

```
MCP Client (Cursor/Goose)
    ↓ HTTPS + JWT (tenant_id, tenant_tier, spoke_cluster_id)
AgentGateway (validates JWT via auth-proxy)
    ↓ HTTP + X-Auth-* headers (X-Auth-Tenant-ID, X-Auth-Spoke-Cluster-ID)
cmd/mcp-server/ (agents-core tools)
    ↓ Extracts spoke_cluster_id from context
AgentRegistry API (POST /v0/deployments)
    ↓ Creates deployment record in DB
AgentRegistry Deployment Adapter
    ↓ Generates Kagent Agent CRD
    ↓ Applies CRD directly to Spoke cluster via Kubernetes API (client-go)
Kagent Controller (Spoke)
    ↓ Reconciles Agent CRD → Creates Deployment + KEDA ScaledObject
Spoke Controller watches Agent CRD
    ↓ Updates AgentRegistry deployment status via API
AgentRegistry (Control Plane Shared DB - agentregistry schema)
```

**JWT Claims Structure (from SBT patterns):**
```go
// Reference: archived/sbt-patterns/docs/sbt-design-principles.md
type TenantClaims struct {
    TenantID       string `json:"tenant_id"`
    TenantTier     string `json:"tenant_tier"`
    SpokeClusterID string `json:"spoke_cluster_id"`  // Set during environment_create
    UserID         string `json:"sub"`
    Email          string `json:"email"`
    jwt.RegisteredClaims
}
```

**spoke_cluster_id Assignment (B-03 Resolution):**
1. During tenant onboarding, `environment_create` provisions spoke cluster via Crossplane
2. Spoke cluster ID written to Control Plane Shared DB: `tenants.spoke_cluster_id`
3. Ory Hydra enriches JWT with `spoke_cluster_id` from tenant record during token generation
4. If tenant has multiple environments, JWT contains primary spoke cluster ID
5. Token refresh after spoke migration handled by Hydra reading updated tenant record

**Reference:** `archived/sbt-patterns/docs/microservice-utils.md` - Identity Token Manager pattern

## 2. Component Architecture

### 2.1 MCP Server Integration

**Location:** `cmd/mcp-server/main.go` (existing unified server)

**Agent-Specific Package:** `internal/agent-core/` (NEW - orchestration layer)

**Package Structure:**
```
internal/agent-core/
├── service/
│   ├── agent_service.go          # Orchestrates agent operations
│   ├── deployment_service.go     # Orchestrates deployment operations
│   └── status_service.go         # Orchestrates status queries
├── client/
│   ├── agentregistry_client.go   # HTTP client for AgentRegistry OSS API
│   └── hub_client.go             # HTTP client for Hub PostgREST
├── validators/
│   ├── model_validator.go        # Platform-specific model authorization
│   └── tool_validator.go         # Platform-specific tool authorization
└── models/
    └── mcp_models.go              # MCP request/response models
```

**Design Philosophy:**
- **AgentRegistry is OSS (read-only)**: Use AgentRegistry API as-is, cannot modify
- **Orchestration Layer**: `internal/agent-core/` coordinates AgentRegistry + Platform services
- **Platform-Specific Logic**: Model authorization, tool authorization, JWT routing
- **Service Coordination**: Calls AgentRegistry API + Hub PostgREST + NATS

**What AgentRegistry OSS Provides (CRUD Only - Cannot Modify):**
```
POST   /v0/agents                              # Create/update agent record in DB
GET    /v0/agents                              # List agent records from DB
GET    /v0/agents/{name}/versions/{version}   # Get agent record from DB
DELETE /v0/agents/{name}/versions/{version}   # Delete agent record from DB
POST   /v0/deployments                         # Create deployment record in DB
GET    /v0/deployments/{id}                    # Get deployment record from DB
DELETE /v0/deployments/{id}                    # Delete deployment record from DB
```

**CRITICAL: AgentRegistry does NOT provide:**
- ❌ Git commits or GitOps integration
- ❌ CRD generation
- ❌ Deployment adapters
- ❌ Provisioning orchestration

**What internal/agent-core/ Orchestrates (Platform-Specific):**
- Tenant context extraction from JWT (tenant_id, tenant_tier, spoke_cluster_id)
- Model authorization validation (tier-based access control)
- Tool authorization validation (tenant-specific permissions)
- MCP request/response formatting
- NATS event publishing for side effects (billing, notifications)

**Integration with AgentRegistry:**
- agent-core calls AgentRegistry API for CRUD operations
- AgentRegistry handles CRD generation and Kubernetes API calls via Deployment Adapter
- AgentRegistry manages deployment lifecycle and status updates

**Integration with opensbt:**
```go
// cmd/mcp-server/main.go
import (
    "internal/opensbt/controlplane"      // Generic SaaS patterns
    "internal/agent-core/service"        // Agent orchestration
    "internal/agent-core/client"         // AgentRegistry + Hub clients
)

func main() {
    // Initialize opensbt control plane (for NATS, auth, etc.)
    cp := controlplane.New(controlplane.Config{
        EventBus:   natsClient,
        AuthClient: oryClient,
    })
    
    // Initialize AgentRegistry HTTP client (OSS, read-only)
    agentRegistryClient := client.NewAgentRegistryClient(client.Config{
        BaseURL: "http://agentregistry.platform-agentregistry.svc.cluster.local:8080",
        Timeout: 30 * time.Second,
    })
    
    // Initialize Hub PostgREST client (for deployment status)
    hubClient := client.NewHubClient(client.Config{
        BaseURL: "https://postgrest.hub.zero-ops.io",
        Timeout: 10 * time.Second,
    })
    
    // Initialize agent service (orchestration layer)
    agentService := service.NewAgentService(service.Config{
        AgentRegistryClient: agentRegistryClient,
        HubClient:           hubClient,
        ControlPlane:        cp,
        DB:                  queries,  // For platform-specific tables (agents schema)
    })
    
    // Register MCP tools (thin wrappers)
    mcpServer.RegisterTool("create_agent", agentService.CreateAgent)
    mcpServer.RegisterTool("deploy_agent", agentService.DeployAgent)
    mcpServer.RegisterTool("get_agent_status", agentService.GetAgentStatus)
    mcpServer.RegisterTool("list_agents", agentService.ListAgents)
    mcpServer.RegisterTool("update_agent", agentService.UpdateAgent)
    mcpServer.RegisterTool("delete_agent", agentService.DeleteAgent)
    mcpServer.RegisterTool("list_authorized_tools", agentService.ListAuthorizedTools)
}
```

**New Tools Added:**
- `create_agent` - Orchestrates: validate model/tools → call AgentRegistry API → return response
- `deploy_agent` - Orchestrates: call AgentRegistry deployment API → publish NATS event
- `get_agent_status` - Orchestrates: query Hub PostgREST for deployment status
- `list_agents` - Orchestrates: call AgentRegistry API → enrich with Hub status
- `update_agent` - Orchestrates: call AgentRegistry API → check deployment → trigger redeploy if needed
- `delete_agent` - Orchestrates: call AgentRegistry delete APIs (agent + deployment)
- `list_authorized_tools` - Platform-specific: query agents schema for tenant tools

**Removed Tools (vs requirements.md):**
- `list_providers` - REMOVED: spoke_cluster_id derived from JWT, no need to expose topology

**Tool Organization:**
```go
// cmd/mcp-server/tools/agents/
├── create_agent.go              # Thin wrapper → internal/agent-core/service
├── deploy_agent.go              # Thin wrapper → internal/agent-core/service
├── get_agent_status.go          # Thin wrapper → internal/agent-core/service
├── list_agents.go               # Thin wrapper → internal/agent-core/service
├── update_agent.go              # Thin wrapper → internal/agent-core/service
├── delete_agent.go              # Thin wrapper → internal/agent-core/service
└── list_authorized_tools.go     # Thin wrapper → internal/agent-core/service

// MCP tool handlers delegate to internal/agent-core/service
// Example:
func handleCreateAgent(ctx context.Context, params map[string]interface{}) (interface{}, error) {
    return agentService.CreateAgent(ctx, params)
}
```

**Middleware - Tenant Context Extraction:**
```go
// Reference: archived/sbt-patterns/docs/sbt-design-principles.md
func TenantContextMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        // AgentGateway injects headers after JWT validation
        tenantID := c.GetHeader("X-Auth-Tenant-ID")
        tenantTier := c.GetHeader("X-Auth-Tenant-Tier")
        spokeClusterID := c.GetHeader("X-Auth-Spoke-Cluster-ID")
        
        // Add to context for downstream handlers
        ctx := context.WithValue(c.Request.Context(), "tenant_id", tenantID)
        ctx = context.WithValue(ctx, "tenant_tier", tenantTier)
        ctx = context.WithValue(ctx, "spoke_cluster_id", spokeClusterID)
        c.Request = c.Request.WithContext(ctx)
        
        c.Next()
    }
}
```

### 2.2 Database Schema

**Control Plane Shared DB (PostgreSQL):**

**Schema: `agentregistry`** (managed by agentregistry OSS project)
```sql
-- AgentRegistry manages its own schema with tenant isolation
-- Reference: archived/agentic-ai/solo/agentregistry/internal/registry/database/postgres.go
-- Reference: docs/prds/v9/agentic/agentic-foundations/agent-lifecycle.md

CREATE TABLE agent_definitions (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,  -- ADDED: Tenant isolation column
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    description TEXT,
    system_message TEXT,
    dependencies JSONB,
    env JSONB,
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ,
    UNIQUE(tenant_id, name, version)  -- UPDATED: Tenant-scoped uniqueness
);

-- RLS policy for tenant isolation (SBT pattern)
ALTER TABLE agent_definitions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON agent_definitions
    USING (tenant_id = current_setting('app.tenant_id')::UUID);

CREATE TABLE deployments (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,  -- ADDED: Tenant isolation column
    server_name TEXT NOT NULL,
    version TEXT NOT NULL,
    status TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    provider_metadata JSONB,
    deployed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ
);

-- RLS policy for tenant isolation
ALTER TABLE deployments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON deployments
    USING (tenant_id = current_setting('app.tenant_id')::UUID);
```

**Tenant Context Setting (agent-core responsibility):**
```go
// Before calling AgentRegistry API, set tenant context
// Reference: archived/sbt-patterns/docs/sbt-design-principles.md
func (s *AgentService) setTenantContext(ctx context.Context, tx pgx.Tx) error {
    tenantID := ctx.Value("tenant_id").(string)
    _, err := tx.Exec(ctx, "SET app.tenant_id = $1", tenantID)
    return err
}
```

**Schema: `agents`** (Zero-Ops platform-specific extensions)
```sql
-- Tenant-specific tool authorization
CREATE TABLE authorized_tools (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(tenant_id),
    tool_name VARCHAR(255) NOT NULL,
    category VARCHAR(100),  -- infrastructure, business, integration
    enabled BOOLEAN DEFAULT true,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(tenant_id, tool_name)
);

-- Note: Model authorization is managed via Kagent ModelConfig CRDs, not database tables.
-- ModelConfigs are namespace-scoped K8s resources that define available models per tenant.
-- Validation queries the K8s API server, not SQL.
```CREATE INDEX idx_authorized_tools_tenant ON authorized_tools(tenant_id);

-- RLS Policy
ALTER TABLE authorized_tools ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON authorized_tools
    USING (tenant_id = current_setting('app.tenant_id')::uuid);
```

**Database Connection Configuration:**
```go
// AgentRegistry connects to Control Plane Shared DB with schema: agentregistry
agentRegistryDB := &postgres.Config{
    Host:     "postgres-control-plane.platform.svc.cluster.local",
    Port:     5432,
    Database: "control_plane",
    Schema:   "agentregistry",  // AgentRegistry schema
    User:     "agentregistry",
    SSLMode:  "require",
}

// MCP server connects to Control Plane Shared DB with schema: agents
mcpServerDB := &postgres.Config{
    Host:     "postgres-control-plane.platform.svc.cluster.local",
    Port:     5432,
    Database: "control_plane",
    Schema:   "agents",  // Platform-specific extensions
    User:     "mcp_server",
    SSLMode:  "require",
}
```

**CRITICAL - Database Architecture:**

**Control Plane Shared DB (agentregistry schema):**
- App-layer deployment status: `deploying`, `deployed`, `failed`, `cancelled`
- Single source of truth for agent deployment lifecycle
- Updated by: AgentRegistry API + NATS event subscriber

**Hub Centralised DB:**
- Infra-layer resource status: `provisioning`, `ready` (pod-level)
- Phase tracking: `Running`, `Idle`, `Failed` (KEDA scale state)
- Replica counts: 0, 1, 2, etc.
- Updated by: Spoke Controller writes directly via PostgREST

**Event Flow (Infra → App Layer):**
```
Spoke Controller watches K8s pods
  ↓
Writes status to Hub Centralised DB (PostgREST)
  ↓
Hub DB trigger publishes NATS event
  ↓
Control Plane NATS Subscriber
  ↓
Updates AgentRegistry deployments.status (deploying → deployed)
```

### 2.3 Deployment Adapter Pattern

**Business Agent Deployment (Direct Kubernetes API):**

AgentRegistry uses a Deployment Adapter to apply Kagent CRDs directly to Spoke clusters without GitOps:

```go
// AgentRegistry internal component (not in agent-core)
type DeploymentAdapter struct {
    k8sClients map[string]*kubernetes.Clientset  // spoke_cluster_id → client
}

func (a *DeploymentAdapter) Deploy(ctx context.Context, agent *Agent, spokeClusterID string) error {
    // 1. Generate Kagent Agent CRD
    crd := a.generateAgentCRD(agent)
    
    // 2. Get Kubernetes client for target spoke
    client := a.k8sClients[spokeClusterID]
    
    // 3. Apply CRD directly (kubectl apply equivalent)
    _, err := client.Resource(agentGVR).Namespace(agent.Namespace).
        Create(ctx, crd, metav1.CreateOptions{})
    
    return err
}
```

**Infrastructure Agent Deployment (GitOps):**

Platform team commits Agent CRDs directly to Git → ArgoCD syncs to clusters.


### 2.4 Spoke Controller Integration

**Reference Implementation:** `archived/agentic-ai/solo/kagent/go/core/internal/controller/agent_controller.go`

**Location:** `operators/spoke-controller/internal/controller/agent_controller.go`

**Responsibilities:**
1. Watch Agent CRDs in spoke cluster (controller-runtime pattern from kagent)
2. Derive infra status from Deployment.status.availableReplicas
3. Map KEDA scale-to-zero to phase field
4. Write infra status to Hub Centralised DB via PostgREST (triggers NATS event)

**Controller Pattern (from kagent AgentController):**
```go
// Reference: archived/agentic-ai/solo/kagent/go/core/internal/controller/agent_controller.go
type AgentStatusController struct {
    client.Client
    Scheme     *runtime.Scheme
    HubClient  *PostgRESTClient
    ClusterID  string
}

// +kubebuilder:rbac:groups=kagent.dev,resources=agents,verbs=get;list;watch
// +kubebuilder:rbac:groups=kagent.dev,resources=agents/status,verbs=get
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch

func (r *AgentStatusController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // Fetch Agent CRD
    var agent v1alpha2.Agent
    if err := r.Get(ctx, req.NamespacedName, &agent); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // Derive infra status from underlying Deployment
    infraStatus := r.deriveInfraStatus(ctx, &agent)
    
    // Write to Hub Centralised DB (triggers NATS event to update AgentRegistry)
    if err := r.syncToHub(ctx, &agent, infraStatus); err != nil {
        return ctrl.Result{RequeueAfter: 30 * time.Second}, err
    }
    
    return ctrl.Result{}, nil
}

func (r *AgentStatusController) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&v1alpha2.Agent{}).
        Watches(
            &appsv1.Deployment{},
            handler.EnqueueRequestsFromMapFunc(r.findAgentsForDeployment),
        ).
        Named("agent-status-sync").
        Complete(r)
}
```

**Infra Status Mapping Logic:**
```go
func deriveAgentPhase(deployment *appsv1.Deployment) string {
    if deployment.Status.AvailableReplicas == 0 {
        return "Idle"  // KEDA scaled to zero
    }
    if deployment.Status.AvailableReplicas > 0 {
        return "Running"
    }
    if deployment.Status.Conditions has Failed {
        return "Failed"
    }
    return "Unknown"
}

func deriveInfraStatus(agent *v1alpha2.Agent, deployment *appsv1.Deployment) string {
    // Map from kagent Agent CRD conditions to infra status
    if agent.Status.Conditions has Ready=True {
        return "ready"
    }
    if agent.Status.Conditions has Ready=False {
        return "failed"
    }
    return "provisioning"
}
```

**Hub Centralised DB Write Pattern:**
```go
// Spoke Controller uses per-spoke JWT (Hydra client_credentials)
// Writes infra status to Hub Centralised DB (NOT AgentRegistry)
func (c *AgentStatusController) syncToHub(ctx context.Context, agent *v1alpha2.Agent, infraStatus InfraStatus) error {
    // Validate required labels before syncing (skip unlabelled CRDs)
    tenantID := agent.Labels["tenant-id"]
    agentID := agent.Labels["agent-id"]
    deploymentID := agent.Labels["deployment-id"]
    
    if tenantID == "" || agentID == "" || deploymentID == "" {
        // Skip platform agents or unlabelled CRDs - do not sync to Hub DB
        c.Log.Info("Skipping agent without required labels", 
            "name", agent.Name, 
            "namespace", agent.Namespace,
            "has_tenant_id", tenantID != "",
            "has_agent_id", agentID != "",
            "has_deployment_id", deploymentID != "")
        return nil
    }
    
    payload := AgentInfraStatus{
        TenantID:       tenantID,
        AgentID:        agentID,
        DeploymentID:   deploymentID,
        SpokeClusterID: c.ClusterID,
        Status:         infraStatus.Status,  // provisioning, ready, failed
        Phase:          infraStatus.Phase,   // Running, Idle, Failed
        Replicas:       infraStatus.Replicas,
        Message:        infraStatus.Message,
        LastSyncAt:     time.Now(),
    }
    
    // POST to Hub-side PostgREST with Bearer JWT
    // Hub DB trigger publishes NATS event → Control Plane subscriber updates AgentRegistry via API
    return c.HubClient.Post(ctx, "/agent_infra_status", payload)
}
```

### 2.5 NATS Event Subscriber (Control Plane)

**Purpose:** Subscribe to Hub NATS events and update AgentRegistry deployment status

**Location:** `cmd/nats-subscriber/main.go` (NEW component)

**Responsibilities:**
1. Subscribe to Hub NATS subject: `hub.platform.agent.infra_status`
2. Map infra status (`provisioning`, `ready`) to app status (`deploying`, `deployed`)
3. Update AgentRegistry deployments table via AgentRegistry API

**CRITICAL - API Boundary Pattern:**
The NATS subscriber uses the AgentRegistry API (not direct DB access or PostgREST) because:
- The `agentregistry` schema is NOT exposed via PostgREST (only `dashboard` schema is exposed)
- AgentRegistry API provides proper tenant isolation and validation
- Maintains clean API boundaries between components

**Implementation:**
```go
// cmd/nats-subscriber/main.go
type StatusSubscriber struct {
    db                  *sql.DB
    agentRegistryClient *client.AgentRegistryClient
}

func (s *StatusSubscriber) Listen(ctx context.Context) error {
    // Listen for pg_notify events from Hub Centralised DB
    listener := pq.NewListener(s.db.Driver().(*pq.Driver).Open, 10*time.Second, time.Minute, nil)
    defer listener.Close()

    if err := listener.Listen("agent_infra_status_updates"); err != nil {
        return fmt.Errorf("failed to listen to pg_notify channel: %w", err)
    }

    for {
        select {
        case <-ctx.Done():
            return nil
        case notification := <-listener.Notify:
            if notification != nil {
                if err := s.handleStatusUpdate(ctx, notification.Extra); err != nil {
                    log.Printf("Failed to handle status update: %v", err)
                }
            }
        }
    }
}

func (s *StatusSubscriber) handleStatusUpdate(ctx context.Context, payload string) error {
    var update InfraStatusUpdate
    if err := json.Unmarshal([]byte(payload), &update); err != nil {
        return fmt.Errorf("failed to unmarshal status update: %w", err)
    }

    // Map infra status to app status
    deploymentStatus := mapInfraStatusToDeploymentStatus(update.Status)

    // Update via AgentRegistry API (PATCH /v0/deployments/{id})
    return s.updateAgentRegistryDeployment(ctx, update.DeploymentID, deploymentStatus)
}

func mapInfraStatusToDeploymentStatus(infraStatus string) string {
    switch infraStatus {
    case "provisioning":
        return "deploying"
    case "ready":
        return "deployed"
    case "failed":
        return "failed"
    default:
        return "deploying"
    }
}

func (s *StatusSubscriber) updateAgentRegistryDeployment(ctx context.Context, deploymentID, status string) error {
    // Update via AgentRegistry API (PATCH /v0/deployments/{id})
    updateReq := &client.DeploymentUpdateRequest{
        Status: status,
    }
    
    err := s.agentRegistryClient.UpdateDeployment(ctx, deploymentID, updateReq)
    if err != nil {
        return fmt.Errorf("failed to update deployment via AgentRegistry API: %w", err)
    }
    
    return nil
}
```

**Hub Centralised DB Schema (Infra Status):**
```sql
-- Hub Centralised DB
-- Agent infra status (written by Spoke Controller)
CREATE TABLE agent_infra_status (
    deployment_id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL,
    spoke_cluster_id VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL,  -- provisioning, ready, failed
    phase VARCHAR(50),             -- Running, Idle, Failed (from KEDA)
    replicas INT DEFAULT 0,
    message TEXT,
    error TEXT,
    provider_metadata JSONB,
    last_sync_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_agent_infra_status_tenant ON agent_infra_status(tenant_id);
CREATE INDEX idx_agent_infra_status_deployment ON agent_infra_status(deployment_id);

-- DB trigger to publish NATS event on status change
CREATE OR REPLACE FUNCTION notify_agent_infra_status_change()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM pg_notify(
        'hub.platform.agent.infra_status',
        json_build_object(
            'deployment_id', NEW.deployment_id,
            'status', NEW.status,
            'phase', NEW.phase,
            'replicas', NEW.replicas
        )::text
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER agent_infra_status_change
AFTER INSERT OR UPDATE ON agent_infra_status
FOR EACH ROW
EXECUTE FUNCTION notify_agent_infra_status_change();
```

## 3. MCP Tool Implementations

### 3.1 create_agent Tool

**Purpose:** Create agent definition via AgentRegistry OSS API with platform-specific validation

**Implementation Location:** `internal/agent-core/service/agent_service.go`

**Input Schema:**
```json
{
  "type": "object",
  "properties": {
    "name": {"type": "string", "pattern": "^[a-z0-9-]+$"},
    "version": {"type": "string", "default": "v1"},
    "provider": {"type": "string", "enum": ["openai", "anthropic", "google"]},
    "model": {"type": "string"},
    "system_prompt": {"type": "string"},
    "tools": {"type": "array", "items": {"type": "string"}},
    "config": {"type": "object"}
  },
  "required": ["name", "provider", "model"]
}
```

**Implementation (Orchestration Pattern):**
```go
// internal/agent-core/service/agent_service.go
func (s *AgentService) CreateAgent(ctx context.Context, params map[string]interface{}) (interface{}, error) {
    // 1. Extract tenant context from JWT (set by middleware)
    tenantID := ctx.Value("tenant_id").(string)
    tenantNamespace := ctx.Value("tenant_namespace").(string)  // K8s namespace for tenant
    
    // 2. Platform-specific validation (NOT in AgentRegistry OSS)
    // Validate model authorization via Kagent ModelConfig CRDs
    // ModelConfigs are namespace-scoped K8s resources managed by Kagent
    modelConfigRef := params["model_config"].(string)  // Format: "namespace/name" or "name" (defaults to tenant namespace)
    if err := s.validators.ValidateModelConfigExists(ctx, tenantNamespace, modelConfigRef); err != nil {
        return nil, err  // e.g., "ModelConfig default/gpt-4 not found or not accessible"
    }
    
    // Validate tool authorization (tenant-specific permissions)
    if err := s.validators.ValidateToolAuthorization(ctx, tenantID, params["tools"]); err != nil {
        return nil, err  // e.g., "Tool stripe_api not authorized for tenant"
    }
    
    // 3. Call AgentRegistry OSS API (POST /v0/agents)
    // Reference: archived/agentic-ai/solo/agentregistry/openapi.yaml
    agentReq := &client.AgentRequest{
        Name:          params["name"].(string),
        Version:       params["version"].(string),
        ModelProvider: params["provider"].(string),
        Model:         params["model"].(string),
        SystemMessage: params["system_prompt"].(string),
        // Map MCP params to AgentRegistry schema
    }
    
    agentResp, err := s.agentRegistryClient.CreateAgent(ctx, agentReq)
    if err != nil {
        return nil, fmt.Errorf("agentregistry API error: %w", err)
    }
    
    // 4. Publish NATS event (platform-specific side effect)
    // Reference: archived/sbt-patterns/docs/saas-architecture-principles.md
    s.controlPlane.EventBus().Publish(ctx, opensbt.Event{
        Subject:    "hub.platform.agent.created",  // Hub NATS subject namespace
        TenantID:   tenantID,
        Detail: map[string]interface{}{
            "agent_name":    agentResp.Agent.Name,
            "agent_version": agentResp.Agent.Version,
        },
    })
    
    // 5. Return MCP-formatted response
    return map[string]interface{}{
        "agent_id":            agentResp.Agent.Name,  // AgentRegistry uses name as ID
        "name":                agentResp.Agent.Name,
        "version":             agentResp.Agent.Version,
        "provider":            agentResp.Agent.ModelProvider,
        "model":               agentResp.Agent.Model,
        "status":              "registered",
        "deployment_required": true,
        "message":             "Agent created successfully. Use deploy_agent to provision runtime.",
    }, nil
}
```

**What AgentRegistry OSS Does (Cannot Modify):**
```go
// Reference: archived/agentic-ai/solo/agentregistry/internal/registry/service/registry_service.go
// This is what AgentRegistry does internally when it receives POST /v0/agents

func (s *registryServiceImpl) CreateAgent(ctx context.Context, req *AgentJSON) (*AgentResponse, error) {
    // 1. Validate agent JSON structure
    if err := validators.ValidateAgentJSON(req); err != nil {
        return nil, err
    }
    
    // 2. Insert into agentregistry schema (Control Plane Shared DB)
    agent, err := s.db.CreateAgent(ctx, nil, req, officialMeta)
    if err != nil {
        return nil, err
    }
    
    // 3. Generate embeddings (if enabled)
    if s.shouldGenerateEmbeddingsOnPublish() {
        go s.generateEmbeddings(ctx, agent)
    }
    
    return &AgentResponse{Agent: agent}, nil
}
```

### 3.2 deploy_agent Tool

**Purpose:** Deploy agent to Spoke cluster via async event-driven pattern

**Reference Implementation:** `archived/agentic-ai/solo/agentregistry/openapi.yaml` - `POST /v0/deployments`

**Implementation Location:** `internal/agent-core/service/deployment_service.go`

**Input Schema:**
```json
{
  "type": "object",
  "properties": {
    "agent_id": {"type": "string", "format": "uuid"}
  },
  "required": ["agent_id"]
}
```

**Note:** `provider_id` removed - spoke_cluster_id derived from JWT claims (set during tenant onboarding)

**CRITICAL - Async Pattern (SBT Aligned):**
The MCP tool MUST NOT synchronously commit to Git. It creates the deployment record and emits an event. A background worker handles the Git commit asynchronously.

**Implementation Pattern (Synchronous - AgentRegistry Handles Deployment):**
```go
// internal/agent-core/service/deployment_service.go
func (s *DeploymentService) DeployAgent(ctx context.Context, params map[string]interface{}) (interface{}, error) {
    // 1. Extract tenant context from JWT (set by middleware)
    tenantID := ctx.Value("tenant_id").(string)
    spokeClusterID := ctx.Value("spoke_cluster_id").(string)
    agentID := params["agent_id"].(string)
    
    // 2. Fetch agent from AgentRegistry (CRUD only)
    agent, err := s.agentRegistryClient.GetAgent(ctx, agentID)
    if err != nil {
        return nil, fmt.Errorf("failed to fetch agent: %w", err)
    }
    
    // 3. Create deployment via AgentRegistry (handles CRD generation and Kubernetes apply)
    deploymentReq := &client.DeploymentRequest{
        AgentID:    agentID,
        ProviderID: spokeClusterID,
    }
    
    deploymentResp, err := s.agentRegistryClient.CreateDeployment(ctx, deploymentReq)
    if err != nil {
        return nil, fmt.Errorf("failed to create deployment: %w", err)
    }
    
    // 4. Publish NATS event for side effects (billing, audit)
    s.controlPlane.EventBus.Publish(ctx, models.NewEvent(
        "hub.platform.agent.deployed",
        models.PlatformEventSource,
        map[string]interface{}{
            "tenant_id":        tenantID,
            "agent_id":         agentID,
            "deployment_id":    deploymentResp.ID,
            "spoke_cluster_id": spokeClusterID,
        },
    ))
    
    // 5. Return immediately with deployment_id
    return map[string]interface{}{
        "agent_id":         agentID,
        "deployment_id":    deploymentResp.ID,
        "status":           deploymentResp.Status,  // "deploying"
        "spoke_cluster_id": spokeClusterID,
        "message":          "Deployment initiated. Poll get_agent_status for completion.",
    }, nil
}
```
            "tools":            agent.Tools,
        },
    ))
    
    // 5. Return immediately (async pattern)
    return map[string]interface{}{
        "agent_id":         agentID,
        "deployment_id":    deploymentResp.ID,
        "status":           "deploying",
        "spoke_cluster_id": spokeClusterID,
        "message":          "Deployment initiated. Poll get_agent_status for completion.",
    }, nil
}
```

**What AgentRegistry OSS Does (Cannot Modify):**
```go
// Reference: archived/agentic-ai/solo/agentregistry/internal/registry/api/handlers/v0/deployments.go
// This is what AgentRegistry does internally when it receives POST /v0/deployments

func (h *DeploymentHandler) CreateDeployment(ctx context.Context, req *DeploymentRequest) (*Deployment, error) {
    // 1. Validate request
    if req.AgentID == "" || req.ProviderID == "" {
        return nil, errors.New("agentId and providerId required")
    }
    
    // 2. Create deployment record in agentregistry schema (DB only)
    deployment := &models.Deployment{
        ID:         uuid.New().String(),
        AgentID:    req.AgentID,
        ProviderID: req.ProviderID,
        Status:     "deploying",  // Initial status
        CreatedAt:  time.Now(),
    }
    
    if err := h.db.CreateDeployment(ctx, deployment); err != nil {
        return nil, err
    }
    
    // 3. Invoke Deployment Adapter to apply CRD to Spoke cluster
    if err := h.deploymentAdapter.Deploy(ctx, deployment); err != nil {
        deployment.Status = "failed"
        h.db.UpdateDeployment(ctx, deployment)
        return nil, err
    }
    
    // 4. Return deployment record
    return deployment, nil
}
```

**CRITICAL: AgentRegistry Deployment Adapter handles:**
- CRD generation
- Direct Kubernetes API calls (client-go)
- Spoke cluster authentication

**agent-core does NOT:**
- Generate CRDs
- Call Kubernetes APIs
- Manage GitOps repos

### 3.3 get_agent_status Tool

**Purpose:** Read agent deployment status from AgentRegistry API

**Reference Implementation:** `archived/agentic-ai/solo/agentregistry/openapi.yaml` - `GET /v0/deployments/{id}`

**Implementation Location:** `internal/agent-core/service/status_service.go`

**Input Schema:**
```json
{
  "type": "object",
  "properties": {
    "deployment_id": {"type": "string", "format": "uuid"}
  },
  "required": ["deployment_id"]
}
```

**Implementation:**
```go
// internal/agent-core/service/status_service.go
// Query AgentRegistry API (single source of truth for app-layer status)
func (s *StatusService) GetAgentStatus(ctx context.Context, params map[string]interface{}) (interface{}, error) {
    tenantID := ctx.Value("tenant_id").(string)
    deploymentID := params["deployment_id"].(string)
    
    // Query AgentRegistry GET /v0/deployments/{id}
    deployment, err := s.agentRegistryClient.GetDeployment(ctx, deploymentID)
    if err != nil {
        return nil, fmt.Errorf("failed to get deployment status: %w", err)
    }
    
    // Return status matching AgentRegistry Deployment model
    return map[string]interface{}{
        "deployment_id":    deployment.ID,
        "agent_id":         deployment.ServerName,
        "version":          deployment.Version,
        "status":           deployment.Status,  // deploying, deployed, failed, cancelled
        "provider_id":      deployment.ProviderID,
        "error":            deployment.Error,
        "deployed_at":      deployment.DeployedAt,
        "updated_at":       deployment.UpdatedAt,
    }, nil
}
```

**AgentRegistry Deployment Status Values:**
- `deploying` - Deployment initiated, waiting for infra provisioning
- `deployed` - Agent pod is provisioned and ready (updated via NATS from Hub)
- `failed` - Deployment failed at any stage
- `cancelled` - Deployment cancelled by user

**Note:** Infra-layer details (phase, replicas) are NOT exposed via this API. AgentRegistry tracks app-layer lifecycle only.

### 3.4 list_authorized_tools Tool

**Purpose:** List MCP tools tenant is authorized to use

**Input Schema:**
```json
{
  "type": "object",
  "properties": {
    "category": {"type": "string"}
  }
}
```

**Implementation:**
```go
func (s *AgentsMCPServer) handleListAuthorizedTools(ctx context.Context, params map[string]interface{}) (interface{}, error) {
    tenantID := ctx.Value("tenant_id").(string)
    tenantTier := ctx.Value("tenant_tier").(string)
    
    // Query tenant's authorized tools from Control Plane Shared DB
    tools, err := s.queries.GetAuthorizedTools(ctx, GetAuthorizedToolsParams{
        TenantID: uuid.MustParse(tenantID),
        Tier:     tenantTier,
    })
    if err != nil {
        return nil, fmt.Errorf("failed to get authorized tools: %w", err)
    }
    
    // Filter by category if specified
    if category, ok := params["category"].(string); ok {
        tools = filterByCategory(tools, category)
    }
    
    return map[string]interface{}{
        "tools": tools,
        "tier":  tenantTier,
    }, nil
}
```

### 3.5 update_agent Tool

**Purpose:** Update agent configuration via agentregistry API (triggers redeployment only if already deployed)

**Reference Implementation:** `archived/agentic-ai/solo/agentregistry/openapi.yaml` - `POST /v0/agents` (upsert pattern)

**Input Schema:**
```json
{
  "type": "object",
  "properties": {
    "agent_id": {"type": "string", "format": "uuid"},
    "system_prompt": {"type": "string"},
    "tools": {"type": "array", "items": {"type": "string"}},
    "config": {"type": "object"}
  },
  "required": ["agent_id"]
}
```

**Implementation (Pure Delegation to AgentRegistry):**
```go
// internal/agent-core/adapters/mcp_adapter.go
func (a *MCPAdapter) UpdateAgent(ctx context.Context, params map[string]interface{}) (interface{}, error) {
    tenantID := ctx.Value("tenant_id").(string)
    spokeClusterID := ctx.Value("spoke_cluster_id").(string)
    agentID := params["agent_id"].(string)
    
    // 1. Update agent in AgentRegistry (CRUD only)
    // Reference: archived/agentic-ai/solo/agentregistry/openapi.yaml
    agentReq := &client.AgentRequest{
        Name:          agentID,
        Version:       "latest",
        SystemMessage: params["system_prompt"].(string),
        // ... map other params
    }
    
    agentResp, err := a.agentRegistryClient.CreateAgent(ctx, agentReq)  // POST is upsert
    if err != nil {
        return nil, fmt.Errorf("agentregistry update failed: %w", err)
    }
    
    // 2. Check deployment status via AgentRegistry GET /v0/deployments
    deployments, err := a.agentRegistryClient.ListDeployments(ctx, &client.DeploymentFilter{
        ResourceType: "agent",
        ResourceName: agentID,
        ProviderID:   spokeClusterID,
    })
    if err != nil {
        return nil, err
    }
    
    // 3. Determine if agent is deployed
    isDeployed := len(deployments) > 0 && 
                  (deployments[0].Status == "ready" || deployments[0].Status == "provisioning")
    
    // 4. If deployed, trigger redeployment (agent-core orchestration)
    var deploymentID string
    if isDeployed {
        deploymentID = deployments[0].ID
        
        // Call AgentRegistry to update deployment (triggers Deployment Adapter)
        _, err := a.agentRegistryClient.UpdateDeployment(ctx, deploymentID, agentResp.Agent)
        if err != nil {
            return nil, fmt.Errorf("failed to update deployment: %w", err)
        }
        
        // Publish NATS event (platform-specific side effect)
        a.controlPlane.EventBus().Publish(ctx, opensbt.Event{
            Subject:    "hub.platform.agent.updated",
            TenantID:   tenantID,
            Detail: map[string]interface{}{
                "tenant_id":        tenantID,
                "agent_id":         agentID,
                "deployment_id":    deploymentID,
                "spoke_cluster_id": spokeClusterID,
            },
        })
    }
    
    // 5. Return MCP-formatted response
    return map[string]interface{}{
        "agent_id":      agentID,
        "status":        "updated",
        "deployed":      isDeployed,
        "deployment_id": deploymentID,
        "message": func() string {
            if isDeployed {
                return "Agent updated. Redeployment initiated."
            }
            return "Agent updated. Changes will apply when agent is deployed."
        }(),
    }, nil
}
```

**What AgentRegistry OSS Does (CRUD Only - Cannot Modify):**
```go
// Reference: archived/agentic-ai/solo/agentregistry/internal/registry/service/registry_service.go
// This is what AgentRegistry does internally when it receives POST /v0/agents (update)

func (s *registryServiceImpl) CreateAgent(ctx context.Context, req *AgentJSON) (*AgentResponse, error) {
    // 1. Upsert agent in agentregistry schema (DB only)
    agent, err := s.db.UpsertAgent(ctx, nil, req)
    if err != nil {
        return nil, err
    }
    
    // 2. Return agent record (NO redeployment logic, NO Git commits)
    return &AgentResponse{Agent: agent}, nil
}
```

**CRITICAL: AgentRegistry does NOT:**
- Check deployment status
- Trigger redeployment
- Generate CRDs
- Commit to Git

All redeployment orchestration is in agent-core.


### 3.7 list_agents Tool

**Purpose:** List all agents for tenant via AgentRegistry API

**Reference Implementation:** `archived/agentic-ai/solo/agentregistry/openapi.yaml` - `GET /v0/agents`

**Implementation Location:** `internal/agent-core/service/agent_service.go`

**Input Schema:**
```json
{
  "type": "object",
  "properties": {
    "status": {"type": "string", "enum": ["all", "deployed", "not_deployed"]},
    "limit": {"type": "integer", "default": 30}
  }
}
```

**Implementation (Pure AgentRegistry Delegation):**
```go
// internal/agent-core/service/agent_service.go
func (s *AgentService) ListAgents(ctx context.Context, params map[string]interface{}) (interface{}, error) {
    // 1. Extract tenant context from JWT
    tenantID := ctx.Value("tenant_id").(string)
    
    // 2. Call AgentRegistry GET /v0/agents (pure delegation)
    // Reference: archived/agentic-ai/solo/agentregistry/openapi.yaml
    // Response includes _meta['aregistry.ai/deployments'] with deployment count and summaries
    agentsResp, err := s.agentRegistryClient.ListAgents(ctx, &client.ListAgentsRequest{
        // AgentRegistry filters by tenant context (from middleware)
        Limit: params["limit"].(int),
    })
    if err != nil {
        return nil, fmt.Errorf("agentregistry API error: %w", err)
    }
    
    // 3. Transform AgentRegistry response to MCP format
    // AgentRegistry already includes deployment metadata in _meta field - no additional queries needed
    enrichedAgents := make([]map[string]interface{}, 0, len(agentsResp.Agents))
    for _, agentResp := range agentsResp.Agents {
        agent := agentResp.Agent
        meta := agentResp.Meta
        
        enrichedAgent := map[string]interface{}{
            "agent_id":      agent.Name,
            "name":          agent.Name,
            "version":       agent.Version,
            "created_at":    meta.CreatedAt,
            "updated_at":    meta.UpdatedAt,
        }
        
        // Extract deployment info from _meta['aregistry.ai/deployments']
        if deploymentsMeta := meta.Deployments; deploymentsMeta != nil {
            enrichedAgent["deployment_count"] = deploymentsMeta.Count
            if deploymentsMeta.Count > 0 && len(deploymentsMeta.Deployments) > 0 {
                // Use first deployment as primary status
                deployment := deploymentsMeta.Deployments[0]
                enrichedAgent["deployment_status"] = deployment.Status  // deploying, deployed, failed
                enrichedAgent["deployment_id"] = deployment.ID
            } else {
                enrichedAgent["deployment_status"] = "not_deployed"
            }
        } else {
            enrichedAgent["deployment_status"] = "not_deployed"
            enrichedAgent["deployment_count"] = 0
        }
        
        enrichedAgents = append(enrichedAgents, enrichedAgent)
    }
    
    // 4. Filter by status if specified
    if statusFilter, ok := params["status"].(string); ok && statusFilter != "all" {
        filtered := make([]map[string]interface{}, 0)
        for _, agent := range enrichedAgents {
            if statusFilter == "deployed" && agent["deployment_status"] == "deployed" {
                filtered = append(filtered, agent)
            } else if statusFilter == "not_deployed" && agent["deployment_status"] == "not_deployed" {
                filtered = append(filtered, agent)
            }
        }
        enrichedAgents = filtered
    }
    
    // 5. Return MCP-formatted response
    return map[string]interface{}{
        "agents": enrichedAgents,
        "total":  len(enrichedAgents),
    }, nil
}
```

**What AgentRegistry OSS Does (Cannot Modify):**
```go
// Reference: archived/agentic-ai/solo/agentregistry/internal/registry/service/registry_service.go
// This is what AgentRegistry does internally when it receives GET /v0/agents

func (s *registryServiceImpl) ListAgents(ctx context.Context, filter *AgentFilter) (*AgentListResponse, error) {
    // 1. Query agentregistry schema (Control Plane Shared DB)
    agents, err := s.db.ListAgents(ctx, nil, filter)
    if err != nil {
        return nil, err
    }
    
    // 2. Return agent list (no deployment status - separate API)
    return &AgentListResponse{
        Agents: agents,
        Total:  len(agents),
    }, nil
}
```

### 3.8 delete_agent Tool

**Purpose:** Remove agent and deployment via AgentRegistry API

**Reference Implementation:** `archived/agentic-ai/solo/agentregistry/openapi.yaml` - `DELETE /v0/agents/{name}/versions/{version}` and `DELETE /v0/deployments/{id}`

**Implementation Location:** `internal/agent-core/service/agent_service.go`

**Input Schema:**
```json
{
  "type": "object",
  "properties": {
    "agent_id": {"type": "string"},
    "version": {"type": "string", "default": "latest"}
  },
  "required": ["agent_id"]
}
```

**Implementation (AgentRegistry Delegation):**
```go
// internal/agent-core/service/agent_service.go
func (s *AgentService) DeleteAgent(ctx context.Context, params map[string]interface{}) (interface{}, error) {
    // 1. Extract tenant context from JWT
    tenantID := ctx.Value("tenant_id").(string)
    spokeClusterID := ctx.Value("spoke_cluster_id").(string)
    agentID := params["agent_id"].(string)
    version := params["version"].(string)
    if version == "" {
        version = "latest"
    }
    
    // 2. Query deployments via AgentRegistry
    deployments, err := s.agentRegistryClient.ListDeployments(ctx, &client.DeploymentFilter{
        ResourceType: "agent",
        ResourceName: agentID,
        ProviderID:   spokeClusterID,
    })
    if err != nil {
        return nil, fmt.Errorf("failed to check deployments: %w", err)
    }
    
    // 3. For each deployment: delete via AgentRegistry
    var deletedDeployments []string
    for _, deployment := range deployments {
        // AgentRegistry handles CRD deletion via Deployment Adapter
        if err := s.agentRegistryClient.DeleteDeployment(ctx, deployment.ID); err != nil {
            return nil, fmt.Errorf("failed to delete deployment %s: %w", deployment.ID, err)
        }
        
        deletedDeployments = append(deletedDeployments, deployment.ID)
    }
    
    // 4. Delete agent definition from AgentRegistry
    if err := s.agentRegistryClient.DeleteAgent(ctx, agentID, version); err != nil {
        return nil, fmt.Errorf("agentregistry delete failed: %w", err)
    }
    
    // 5. Publish NATS event (audit/billing)
    s.controlPlane.EventBus().Publish(ctx, opensbt.Event{
        Subject:    "hub.platform.agent.deleted",
        TenantID:   tenantID,
        Detail: map[string]interface{}{
            "tenant_id":            tenantID,
            "agent_id":             agentID,
            "version":              version,
            "deleted_deployments":  deletedDeployments,
            "spoke_cluster_id":     spokeClusterID,
        },
    })
    
    // 6. Return MCP-formatted response
    return map[string]interface{}{
        "agent_id":            agentID,
        "version":             version,
        "status":              "deleted",
        "deleted_deployments": deletedDeployments,
        "message":             fmt.Sprintf("Agent deleted. %d deployment(s) removed.", len(deletedDeployments)),
    }, nil
}
```

**Delete Flow (Synchronous via AgentRegistry):**
```
1. User calls delete_agent
   ↓
2. agent-core calls AgentRegistry DELETE /v0/deployments/{id}
   ↓
3. AgentRegistry invokes Deployment Adapter
   ↓
4. Deployment Adapter deletes CRD from Spoke cluster via Kubernetes API
   ↓
5. Kagent Controller detects deletion → removes agent pod
   ↓
6. agent-core deletes agent definition from AgentRegistry
   ↓
7. agent-core publishes NATS event (audit/billing)
```

**Note on Memory Cleanup:**
Kagent manages agent memory lifecycle automatically. Memory is tied to the agent pod and namespace. When the agent CRD is deleted, Kagent Controller cleans up associated memory resources. No custom cleanup logic needed in delete_agent.
   ↓
7. Return: {status: "deleted"}
   ↓
8. Kagent Controller detects CRD deletion (async)
   ↓
9. Deletes Deployment + Service + KEDA ScaledObject
   ↓
10. Spoke Controller detects pod termination
    ↓
11. Writes to Hub Centralised DB (status: "deleted")
    ↓
12. Hub DB trigger publishes NATS event (confirmation for audit)
```

// DELETE /v0/agents/{name}/versions/{version}
func (h *AgentHandler) DeleteAgent(ctx context.Context, name, version string) error {
    // 1. Check for active deployments
    deployments, _ := h.db.ListDeployments(ctx, &DeploymentFilter{
        ResourceType: "agent",
        ResourceName: name,
        Status:       "ready",
    })
    
    if len(deployments) > 0 {
        return fmt.Errorf("cannot delete agent with active deployments")
    }
    
    // 2. Soft delete from agentregistry schema (DB only)
    return h.db.DeleteAgent(ctx, name, version)
}
```

**CRITICAL: AgentRegistry does NOT:**
- Remove CRDs from clusters
- Delete from GitOps repos
- Call deployment adapters

All deletion orchestration is in agent-core.

## 4. Data Flow Patterns

### 4.1 Agent Creation Flow

```
1. MCP Client → create_agent
   ↓
2. mcp-server validates provider/model/tools
   ↓
3. sqlc INSERT into Control Plane Shared DB (agents table)
   ↓
4. Return agent_id + "deployment_required: true"
```

### 4.2 Agent Deployment Flow (Async Pattern - agent-core Orchestration)

```
1. MCP Client → deploy_agent(agent_id)
   ↓
2. agent-core fetches agent from AgentRegistry (GET /v0/agents)
   ↓
3. agent-core creates deployment record in AgentRegistry (POST /v0/deployments - DB only)
   ↓
4. AgentRegistry calls KubernetesDeploymentAdapter.Deploy()
   ↓
5. Adapter materializes Kagent Agent CRD YAML
   ↓
6. Adapter applies CRD to Spoke cluster
   ↓
7. AgentRegistry updates deployment status to "deployed" (app layer)
   ↓
8. Return immediately: {status: "deployed", deployment_id}
   ↓
9. Kagent Controller reconciles Agent CRD (async)
   (Reference: archived/agentic-ai/solo/kagent/go/core/internal/controller/agent_controller.go)
   ↓
10. Creates Deployment + Service + KEDA ScaledObject
    ↓
11. Spoke Controller watches Agent CRD status
    ↓
12. Writes infra status to Hub Centralised DB (status: "provisioning")
    ↓
13. Hub DB trigger publishes NATS event
    ↓
14. Control Plane NATS Subscriber receives event
    ↓
15. Updates AgentRegistry deployments.status (deploying → deployed)
    ↓
16. When Deployment ready → Spoke Controller writes (status: "ready")
    ↓
17. Hub NATS event → AgentRegistry updated (status: "deployed")
    ↓
18. MCP Client polls get_agent_status → reads from AgentRegistry
    ↓
19. Returns: {status: "deployed"}
```

**Status Lifecycle (App Layer - AgentRegistry):**
- `deploying` - Git commit succeeded, waiting for infra provisioning
- `deployed` - Agent pod is provisioned and ready (updated via NATS from Hub)
- `failed` - Deployment failed at any stage
- `cancelled` - Deployment cancelled by user

**Infra Status (Hub Centralised DB - Not exposed via AgentRegistry API):**
- `provisioning` - Agent CRD created, Kagent reconciling
- `ready` - Deployment available, agent operational
- `failed` - Deployment failed (Git commit, ArgoCD, or Kagent error)


### 4.3 Status Synchronization Flow

```
Spoke Cluster:
  Agent CRD (kagent.dev/v1alpha2)
    ↓ reconciled by
  Kagent Controller
    ↓ creates
  Deployment (apps/v1)
    ↓ scaled by
  KEDA ScaledObject
    ↓ watched by
  Spoke Controller (controller-runtime)
    ↓ derives infra status
  {status: ready, phase: Running, replicas: 2}
    ↓ POST with Bearer JWT
  Hub-side PostgREST (/agent_infra_status)
    ↓ writes to
  Hub Centralised DB (agent_infra_status table)
    ↓ DB trigger publishes
  Hub NATS (hub.platform.agent.infra_status)
    ↓ consumed by
  Control Plane NATS Subscriber
    ↓ maps infra → app status
  {provisioning → deploying, ready → deployed}
    ↓ updates
  AgentRegistry deployments table (Control Plane Shared DB)
    ↓ queried by
  mcp-server (get_agent_status tool)
    ↓ returns to
  MCP Client
```

### 4.4 KEDA Scale-to-Zero Behavior

**Idle Detection:**
- KEDA HTTP Add-on monitors agent A2A request traffic via HTTP interceptor
- If no HTTP requests for 5 minutes → scale Deployment to 0 replicas
- Deployment.status.availableReplicas = 0

**KEDA Configuration:**
```yaml
apiVersion: keda.sh/v1alpha1
kind: HTTPScaledObject
metadata:
  name: agent-{{ .Values.agentId }}
  namespace: {{ .Values.tenantNamespace }}
spec:
  scaleTargetRef:
    name: agent-{{ .Values.agentId }}
    kind: Deployment
  hosts:
    - {{ .Values.agentId }}.{{ .Values.tenantNamespace }}.svc.cluster.local
  targetPendingRequests: 1
  scaledownPeriod: 300  # 5 minutes idle before scale to zero
```

**Status Mapping:**
```go
// Spoke Controller logic
if deployment.Status.AvailableReplicas == 0 {
    phase = "Idle"
    status = "ready"  // Still ready, just scaled down
}
```

**Scale-Up:**
- New A2A request arrives → KEDA HTTP Add-on intercepts
- Detects 0 replicas → scales Deployment to 1
- Buffers request until pod is ready
- Forwards request to agent pod
- Spoke Controller detects replicas > 0 → phase = "Running"

## 5. Error Handling

### 5.1 Error Codes

**Control Plane Errors:**
- `agent_not_found` - Agent ID doesn't exist
- `invalid_provider` - Provider not supported
- `invalid_model` - Model not available for provider
- `unauthorized_tool` - Tool not authorized for tenant tier
- `duplicate_agent` - Agent name+version already exists
- `git_commit_failed` - Failed to commit to GitOps repo
- `deployment_not_found` - Deployment ID doesn't exist

**Deployment Status Values (from agentregistry):**
- `deploying` - Git commit succeeded, ArgoCD sync pending
- `provisioning` - Agent CRD created, Kagent reconciling
- `ready` - Deployment available and operational
- `failed` - Deployment failed at any stage
- `cancelled` - Deployment cancelled by user

**Spoke Errors (from Spoke Controller):**
- `crd_condition_failed` - Agent CRD condition Failed=True
- `deployment_failed` - Kubernetes Deployment failed
- `spoke_controller_sync_failed` - PostgREST write failed


### 5.2 Idempotency Handling

**Scenario: Agent Already Deployed**

```go
func (s *AgentsMCPServer) handleDeployAgent(ctx context.Context, params map[string]interface{}) (interface{}, error) {
    // Check if agent already deployed
    deployment, err := s.hubClient.GetAgentDeployment(ctx, GetAgentDeploymentParams{
        TenantID: tenantID,
        AgentID:  agentID,
    })
    
    if err == nil && deployment.Status == "ready" {
        // Already deployed and ready
        return map[string]interface{}{
            "agent_id": agentID,
            "status":   "ready",
            "message":  "Agent already deployed",
            "idempotent": true,
        }, nil
    }
    
    // Proceed with deployment...
}
```

### 5.3 Retry Strategy

**Spoke Controller Retry:**
- Uses controller-runtime native retry with exponential backoff
- Max retries: 10
- Initial delay: 1s
- Max delay: 5m
- Jitter: 0.2

**PostgREST Write Retry:**
```go
func (c *SpokeController) syncWithRetry(ctx context.Context, status AgentDeploymentStatus) error {
    return retry.Do(
        func() error {
            return c.hubClient.Post(ctx, "/resource-status", status)
        },
        retry.Attempts(10),
        retry.Delay(1*time.Second),
        retry.MaxDelay(5*time.Minute),
        retry.DelayType(retry.BackOffDelay),
    )
}
```

## 6. Security Considerations

### 6.1 Authentication Flow

```
1. MCP Client sends JWT (Ory Hydra issued)
   ↓
2. AgentGateway validates JWT signature via auth-proxy
   ↓
3. auth-proxy fetches JWKS from Ory Hydra
   ↓
4. AgentGateway injects X-Auth-User-ID, X-Auth-Tenant-ID headers
   ↓
5. mcp-server extracts tenant context from headers
   ↓
6. All DB queries include tenant_id filter (RLS enforced)
```

### 6.2 Authorization Checks

**Tool-Level Authorization:**
```go
func (s *AgentsMCPServer) validateTools(ctx context.Context, tenantID string, tools []string) error {
    authorizedTools, err := s.queries.GetAuthorizedTools(ctx, GetAuthorizedToolsParams{
        TenantID: uuid.MustParse(tenantID),
    })
    if err != nil {
        return err
    }
    
    for _, tool := range tools {
        if !contains(authorizedTools, tool) {
            return fmt.Errorf("tool %s not authorized for tenant", tool)
        }
    }
    return nil
}
```


### 6.3 Database RLS Policies

**Control Plane Shared DB:**
```sql
-- Tenant isolation on agents table
CREATE POLICY tenant_isolation ON agents
    USING (tenant_id = current_setting('app.tenant_id')::uuid);

-- Set context before queries (middleware)
SET app.tenant_id = '<tenant-id-from-jwt>';
```

**Hub Centralised DB:**
```sql
-- Spoke Controllers write with bypass_rls=true
-- MCP server reads with tenant_id filter
CREATE POLICY tenant_isolation ON agent_deployments
    USING (tenant_id = current_setting('app.tenant_id')::uuid);
```

## 7. Observability

### 7.1 Metrics

**MCP Tool Metrics:**
```go
var (
    agentToolDuration = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Name: "agents_mcp_tool_duration_seconds",
            Help: "Duration of agent MCP tool execution",
        },
        []string{"tool", "tenant_id", "status"},
    )
    
    agentDeployments = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Name: "agents_deployments_total",
            Help: "Total number of agent deployments",
        },
        []string{"tenant_id", "status", "phase"},
    )
)
```

### 7.2 Logging

**Structured Logging Pattern:**
```go
log.WithFields(log.Fields{
    "tenant_id":   tenantID,
    "agent_id":    agentID,
    "tool":        "deploy_agent",
    "commit_sha":  commitSHA,
}).Info("Agent deployment initiated")
```

### 7.3 Tracing

**OpenTelemetry Integration:**
```go
func (s *AgentsMCPServer) handleDeployAgent(ctx context.Context, params map[string]interface{}) (interface{}, error) {
    ctx, span := tracer.Start(ctx, "deploy_agent")
    defer span.End()
    
    span.SetAttributes(
        attribute.String("tenant_id", tenantID),
        attribute.String("agent_id", agentID),
    )
    
    // Implementation...
}
```

## 8. Testing Strategy

### 8.1 E2E Test Pattern (Testcontainers-Go)

```go
func TestAgentLifecycle(t *testing.T) {
    // Start PostgreSQL
    pgContainer, _ := postgres.RunContainer(ctx)
    defer pgContainer.Terminate(ctx)
    
    // Start NATS
    natsContainer, _ := nats.RunContainer(ctx)
    defer natsContainer.Terminate(ctx)
    
    // Initialize MCP server
    server := setupMCPServer(t, pgContainer, natsContainer)
    
    // Test: Create agent
    result := server.CallTool(ctx, "create_agent", map[string]interface{}{
        "name":     "test-agent",
        "provider": "openai",
        "model":    "gpt-4",
    })
    assert.NotEmpty(t, result["agent_id"])
    
    // Test: Deploy agent
    deployResult := server.CallTool(ctx, "deploy_agent", map[string]interface{}{
        "agent_id": result["agent_id"],
    })
    assert.Equal(t, "pending", deployResult["status"])
    
    // Test: Get status
    statusResult := server.CallTool(ctx, "get_agent_status", map[string]interface{}{
        "agent_id": result["agent_id"],
    })
    assert.Contains(t, []string{"pending", "provisioning", "ready"}, statusResult["status"])
}
```


## 9. Implementation Phases

### Phase 1: Core MCP Tools (Week 1)
- Implement `create_agent` tool
- Implement `list_providers` tool
- Implement `list_authorized_tools` tool
- Database schema setup (Control Plane Shared DB)
- sqlc query generation

### Phase 2: GitOps Integration (Week 2)
- Implement `deploy_agent` tool
- IProvisioner integration (Git commit logic)
- Agent CRD template generation
- ArgoCD ApplicationSet configuration

### Phase 3: Status Synchronization (Week 3)
- Implement Spoke Controller (controller-runtime)
- PostgREST client for Hub Centralised DB writes
- Implement `get_agent_status` tool
- KEDA scale-to-zero status mapping

### Phase 4: Management Tools (Week 4)
- Implement `update_agent` tool
- Implement `list_agents` tool
- Implement `delete_agent` tool
- Memory preservation logic

### Phase 5: Testing & Observability (Week 5)
- E2E tests with Testcontainers-Go
- Metrics instrumentation
- Logging standardization
- OpenTelemetry tracing

## 10. Configuration

### 10.1 MCP Server Configuration

```yaml
# config/mcp-server.yaml
mcp:
  server:
    port: 8080
    path: /mcp
  
agents:
  providers:
    - name: openai
      models: [gpt-4, gpt-4-turbo, gpt-3.5-turbo]
    - name: anthropic
      models: [claude-3-opus, claude-3-sonnet, claude-3-haiku]
    - name: google
      models: [gemini-pro, gemini-ultra]
  
  default_tools:
    - tenant_create
    - tenant_list
    - environment_create
  
  tier_tools:
    basic: []
    standard: [cluster_create]
    premium: [cluster_create, database_create]
    enterprise: [cluster_create, database_create, custom_tools]

database:
  control_plane:
    host: postgres-control-plane
    port: 5432
    database: control_plane
    pool_size: 30
  
  hub_centralised:
    postgrest_url: https://postgrest.hub.zero-ops.io
    timeout: 10s

gitops:
  repo_url: https://github.com/zero-ops/tenant-gitops
  branch: main
  commit_author: zero-ops-bot
  commit_email: bot@zero-ops.io

nats:
  urls: nats://nats-0:4222,nats://nats-1:4222,nats://nats-2:4222
  max_reconnects: 10
```


### 10.2 Spoke Controller Configuration

```yaml
# config/spoke-controller.yaml
spoke:
  cluster_id: spoke-pool-01
  cluster_type: pool  # pool or silo
  
hub:
  postgrest_url: https://postgrest.hub.zero-ops.io
  auth:
    client_id: spoke-pool-01
    client_secret_ref: hub-api-credentials
    token_url: https://hydra.hub.zero-ops.io/oauth2/token
  
controller:
  sync_interval: 30s
  retry_max_attempts: 10
  retry_initial_delay: 1s
  retry_max_delay: 5m
  
store:
  type: hub  # hub or local
  hub:
    connection_string: postgres://control-plane-shared-db:5432/control_plane
  local:
    connection_string: postgres://tenant-control-plane-db:5432/tenant_control_plane
    nats_sync: true
```

## 11. Deployment Manifests

### 11.1 AgentRegistry Deployment

**Location:** Hub Management Cluster (alongside Control Plane services)

**Reference:** `archived/agentic-ai/solo/agentregistry/` deployment patterns

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: agentregistry
  namespace: platform-agentregistry
spec:
  replicas: 3
  selector:
    matchLabels:
      app: agentregistry
  template:
    metadata:
      labels:
        app: agentregistry
    spec:
      serviceAccountName: agentregistry
      containers:
      - name: agentregistry
        image: ghcr.io/agentregistry-dev/agentregistry:latest
        ports:
        - containerPort: 8080
          name: http
        env:
        - name: DATABASE_URL
          value: postgres://agentregistry:$(DB_PASSWORD)@postgres-control-plane.platform.svc.cluster.local:5432/control_plane?search_path=agentregistry
        - name: DB_PASSWORD
          valueFrom:
            secretKeyRef:
              name: agentregistry-db-credentials
              key: password
        - name: ENABLE_REGISTRY_VALIDATION
          value: "false"  # Skip package registry validation for internal use
        - name: EMBEDDINGS_ENABLED
          value: "true"
        - name: EMBEDDINGS_ON_PUBLISH
          value: "true"
        - name: EMBEDDINGS_DIMENSIONS
          value: "1536"
        resources:
          requests:
            cpu: 500m
            memory: 1Gi
          limits:
            cpu: 2000m
            memory: 4Gi
        livenessProbe:
          httpGet:
            path: /v0/health
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /v0/health
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
---
apiVersion: v1
kind: Service
metadata:
  name: agentregistry
  namespace: platform-agentregistry
spec:
  selector:
    app: agentregistry
  ports:
  - port: 8080
    targetPort: 8080
    name: http
  type: ClusterIP
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: agentregistry
  namespace: platform-agentregistry
```

**Database Initialization:**
```sql
-- Run during platform bootstrap
CREATE SCHEMA IF NOT EXISTS agentregistry;
CREATE USER agentregistry WITH PASSWORD '<generated>';
GRANT ALL PRIVILEGES ON SCHEMA agentregistry TO agentregistry;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA agentregistry TO agentregistry;
ALTER DEFAULT PRIVILEGES IN SCHEMA agentregistry GRANT ALL ON TABLES TO agentregistry;
```

### 11.2 Kagent Deployment (Spoke Clusters)

**Location:** Each Spoke cluster (Pool or Silo)

**Reference:** `archived/agentic-ai/solo/kagent/` deployment patterns

**Deployed via ArgoCD ApplicationSet:**
```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: kagent-controllers
  namespace: argocd
spec:
  generators:
  - list:
      elements:
      - cluster: spoke-pool-01
        url: https://spoke-pool-01.k8s.local
      - cluster: spoke-silo-acme
        url: https://spoke-silo-acme.k8s.local
  template:
    metadata:
      name: 'kagent-{{cluster}}'
    spec:
      project: platform
      source:
        repoURL: https://github.com/kagent-dev/kagent
        targetRevision: v0.1.0
        path: deploy/helm/kagent
        helm:
          values: |
            controller:
              replicas: 1
              resources:
                requests:
                  cpu: 200m
                  memory: 256Mi
                limits:
                  cpu: 1000m
                  memory: 1Gi
      destination:
        server: '{{url}}'
        namespace: kagent-system
      syncPolicy:
        automated:
          prune: true
          selfHeal: true
```

**Kagent CRDs (installed in each Spoke):**
```yaml
# Reference: archived/agentic-ai/solo/kagent/go/api/v1alpha2/
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: agents.kagent.dev
spec:
  group: kagent.dev
  versions:
  - name: v1alpha2
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              type:
                type: string
                enum: [Declarative, Agentic]
              declarative:
                type: object
                properties:
                  modelConfig:
                    type: string
                  systemMessage:
                    type: string
                  tools:
                    type: array
                    items:
                      type: object
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: modelconfigs.kagent.dev
spec:
  group: kagent.dev
  versions:
  - name: v1alpha2
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              model:
                type: string
              provider:
                type: string
                enum: [Anthropic, OpenAI, AzureOpenAI, Ollama, Gemini, GeminiVertexAI, AnthropicVertexAI, Bedrock]
```

### 11.3 MCP Server Deployment

**Location:** Hub Management Cluster (alongside AgentRegistry)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mcp-server
  namespace: platform-mcp
spec:
  replicas: 3
  selector:
    matchLabels:
      app: mcp-server
  template:
    metadata:
      labels:
        app: mcp-server
    spec:
      serviceAccountName: mcp-server
      containers:
      - name: mcp-server
        image: ghcr.io/zero-ops/mcp-server:v1.0.0
        ports:
        - containerPort: 8080
          name: http
        env:
        # AgentRegistry API endpoint (internal service)
        - name: AGENTREGISTRY_URL
          value: http://agentregistry.platform-agentregistry.svc.cluster.local:8080
        - name: AGENTREGISTRY_TIMEOUT
          value: 30s
        # Control Plane Shared DB (for platform-specific extensions)
        - name: DATABASE_URL
          value: postgres://mcp_server:$(DB_PASSWORD)@postgres-control-plane.platform.svc.cluster.local:5432/control_plane?search_path=agents
        - name: DB_PASSWORD
          valueFrom:
            secretKeyRef:
              name: mcp-server-db-credentials
              key: password
        # Hub Centralised DB (PostgREST for status queries)
        - name: HUB_POSTGREST_URL
          value: https://postgrest.hub.zero-ops.io
        # NATS for side effects
        - name: NATS_URLS
          value: nats://nats-0.platform.svc.cluster.local:4222,nats://nats-1.platform.svc.cluster.local:4222
        resources:
          requests:
            cpu: 500m
            memory: 512Mi
          limits:
            cpu: 2000m
            memory: 2Gi
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /readyz
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 10
---
apiVersion: v1
kind: Service
metadata:
  name: mcp-server
  namespace: platform-mcp
spec:
  selector:
    app: mcp-server
  ports:
  - port: 8080
    targetPort: 8080
    name: http
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: mcp-server
  namespace: platform-mcp
```

**MCP Server HTTP Client Configuration:**
```go
// internal/agent-core/client/agentregistry_client.go
type AgentRegistryClient struct {
    baseURL    string
    httpClient *http.Client
}

func NewAgentRegistryClient(config Config) *AgentRegistryClient {
    return &AgentRegistryClient{
        baseURL: config.BaseURL,  // http://agentregistry.platform-agentregistry.svc.cluster.local:8080
        httpClient: &http.Client{
            Timeout: config.Timeout,  // 30s
            Transport: &http.Transport{
                MaxIdleConns:        100,
                MaxIdleConnsPerHost: 10,
                IdleConnTimeout:     90 * time.Second,
            },
        },
    }
}

// Example: Create agent via agentregistry API
func (c *AgentRegistryClient) CreateAgent(ctx context.Context, req *AgentRequest) (*AgentResponse, error) {
    url := fmt.Sprintf("%s/v0/agents", c.baseURL)
    body, _ := json.Marshal(req)
    
    httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
    httpReq.Header.Set("Content-Type", "application/json")
    
    resp, err := c.httpClient.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("failed to call agentregistry: %w", err)
    }
    defer resp.Body.Close()
    
    var agentResp AgentResponse
    if err := json.NewDecoder(resp.Body).Decode(&agentResp); err != nil {
        return nil, err
    }
    
    return &agentResp, nil
}
```

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: spoke-controller
  namespace: platform-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: spoke-controller
  template:
    metadata:
      labels:
        app: spoke-controller
    spec:
      serviceAccountName: spoke-controller
      containers:
      - name: controller
        image: ghcr.io/zero-ops/spoke-controller:v1.0.0
        env:
        - name: CLUSTER_ID
          valueFrom:
            configMapKeyRef:
              name: spoke-config
              key: cluster-id
        - name: HUB_POSTGREST_URL
          value: https://postgrest.hub.zero-ops.io
        - name: HUB_CLIENT_ID
          valueFrom:
            secretKeyRef:
              name: hub-api-credentials
              key: client-id
        - name: HUB_CLIENT_SECRET
          valueFrom:
            secretKeyRef:
              name: hub-api-credentials
              key: client-secret
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: spoke-controller
  namespace: platform-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: spoke-controller
rules:
- apiGroups: ["kagent.dev"]
  resources: ["agents"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["kagent.dev"]
  resources: ["agents/status"]
  verbs: ["get"]
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: spoke-controller
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: spoke-controller
subjects:
- kind: ServiceAccount
  name: spoke-controller
  namespace: platform-system
```

## 12. Migration Strategy

### 12.1 From Existing System

**Current State:** No agent management system exists

**Migration Steps:**
1. Deploy Control Plane Shared DB schema
2. Deploy Hub Centralised DB schema
3. Deploy mcp-server with agent tools
4. Deploy Spoke Controllers to all spoke clusters
5. Configure ArgoCD ApplicationSets for agent CRDs
6. Test with single tenant
7. Rollout to all tenants

### 12.2 Rollback Plan

**If deployment fails:**
1. Remove agent tools from mcp-server (feature flag)
2. Keep database schemas (no data loss)
3. Remove Spoke Controller deployments
4. Revert ArgoCD ApplicationSet changes
5. Investigate and fix issues
6. Retry deployment



### 11.4 Spoke Controller Deployment

**Location:** Each Spoke cluster (watches Agent CRDs, syncs status to Hub)

**Reference:** Kagent controller pattern from `archived/agentic-ai/solo/kagent/go/core/internal/controller/`

**Spoke Controller Status Sync Logic:**
```go
// operators/spoke-controller/internal/controller/agent_controller.go
// Reference: archived/agentic-ai/solo/kagent/go/core/internal/controller/agent_controller.go

type AgentStatusController struct {
    client.Client
    Scheme     *runtime.Scheme
    HubClient  *PostgRESTClient
    ClusterID  string
}

func (r *AgentStatusController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. Fetch Agent CRD
    var agent v1alpha2.Agent
    if err := r.Get(ctx, req.NamespacedName, &agent); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }
    
    // 2. Derive status from underlying Deployment
    status := r.deriveAgentStatus(ctx, &agent)
    
    // 3. Sync to Hub Centralised DB via PostgREST
    if err := r.syncToHub(ctx, &agent, status); err != nil {
        return ctrl.Result{RequeueAfter: 30 * time.Second}, err
    }
    
    return ctrl.Result{}, nil
}

func (r *AgentStatusController) syncToHub(ctx context.Context, agent *v1alpha2.Agent, status AgentStatus) error {
    // Authenticate with Hub using Hydra client_credentials
    token, err := r.HubClient.GetToken(ctx)
    if err != nil {
        return fmt.Errorf("failed to get Hub token: %w", err)
    }
    
    payload := AgentDeploymentStatus{
        TenantID:       agent.Labels["tenant-id"],
        AgentID:        agent.Labels["agent-id"],
        DeploymentID:   agent.Labels["deployment-id"],
        SpokeClusterID: r.ClusterID,
        Status:         status.Status,
        Phase:          status.Phase,
        Replicas:       status.Replicas,
        Message:        status.Message,
        LastSyncAt:     time.Now(),
    }
    
    // POST to Hub PostgREST with Bearer token
    return r.HubClient.Post(ctx, "/agent_deployments", payload, token)
}
```

## 12. Service Communication Architecture

### 12.1 MCP Server → AgentRegistry Communication

**Pattern:** HTTP REST API calls (internal cluster service)

```
MCP Client (Cursor/Goose)
    ↓ HTTPS + JWT
AgentGateway (auth-proxy validates JWT)
    ↓ HTTP + X-Auth-* headers
cmd/mcp-server/ (thin orchestration layer)
    ↓ HTTP REST (internal cluster)
AgentRegistry Service (agentregistry.platform-agentregistry.svc.cluster.local:8080)
    ↓ SQL queries
Control Plane Shared DB (schema: agentregistry)
```

**Example Flow - Create Agent:**
```go
// cmd/mcp-server/tools/agents/create_agent.go
func (a *MCPAdapter) CreateAgent(ctx context.Context, params map[string]interface{}) (interface{}, error) {
    // 1. Extract tenant context from JWT
    tenantID := ctx.Value("tenant_id").(string)
    tenantTier := ctx.Value("tenant_tier").(string)
    
    // 2. Validate model authorization (platform-specific logic)
    if err := a.validateModelAuthorization(ctx, params["provider"], params["model"], tenantTier); err != nil {
        return nil, err
    }
    
    // 3. Call AgentRegistry API (pure delegation)
    agentReq := &client.AgentRequest{
        Name:          params["name"].(string),
        Version:       params["version"].(string),
        ModelProvider: params["provider"].(string),
        Model:         params["model"].(string),
        SystemMessage: params["system_prompt"].(string),
        // ... map MCP params to agentregistry schema
    }
    
    agentResp, err := a.agentRegistryClient.CreateAgent(ctx, agentReq)
    if err != nil {
        return nil, fmt.Errorf("agentregistry API error: %w", err)
    }
    
    // 4. Return MCP-formatted response
    return map[string]interface{}{
        "agent_id": agentResp.Agent.Name,  // AgentRegistry uses name as ID
        "name":     agentResp.Agent.Name,
        "version":  agentResp.Agent.Version,
        "status":   "registered",
        "message":  "Agent created successfully. Use deploy_agent to provision runtime.",
    }, nil
}
```

### 12.2 agent-core → AgentRegistry → Kagent Deployment Flow

**Pattern:** agent-core orchestrates all provisioning (AgentRegistry is CRUD only)

```
MCP Client (Cursor/Goose)
    ↓ deploy_agent request
agent-core service (internal/agent-core/service/)
    ↓ GET /v0/agents (fetch agent definition)
AgentRegistry API (CRUD only)
    ↓ Returns agent JSON
agent-core service
    ↓ POST /v0/deployments (create DB record)
AgentRegistry API (CRUD only)
    ↓ Returns deployment record
agent-core service
    ↓ Generate Agent CRD YAML
    ↓ IProvisioner.CommitManifest()
Tenant GitOps Repository (tenant-{id}-gitops/agents/)
    ↓ ArgoCD watches repo
ArgoCD ApplicationSet
    ↓ Syncs to Spoke cluster
Kagent Controller (Spoke)
    ↓ Reconciles Agent CRD
Kubernetes Deployment + KEDA ScaledObject
```

**CRITICAL: AgentRegistry provides CRUD APIs only**
- POST /v0/agents - Create/update agent record in DB
- GET /v0/agents - List agent records from DB
- POST /v0/deployments - Create deployment record in DB
- GET /v0/deployments - List deployment records from DB
- DELETE /v0/deployments/{id} - Update status to "cancelled" in DB

**agent-core orchestrates all provisioning:**
- Generate Agent CRD YAML
- Commit to GitOps repo via IProvisioner
- Publish NATS events
- Query Hub PostgREST for status

### 12.3 Spoke Controller → Hub Status Sync

**Pattern:** PostgREST HTTP API with Hydra OAuth2 authentication

```
Spoke Controller (watches Agent CRDs)
    ↓ Derives status from Deployment
Hydra Token Endpoint (client_credentials grant)
    ↓ Returns Bearer JWT
Hub PostgREST (https://postgrest.hub.zero-ops.io)
    ↓ Validates JWT, enforces RLS
Hub Centralised DB (agent_deployments table)
```

**Authentication Flow:**
```go
// operators/spoke-controller/internal/client/hub_client.go
func (c *HubClient) GetToken(ctx context.Context) (string, error) {
    data := url.Values{}
    data.Set("grant_type", "client_credentials")
    data.Set("client_id", c.clientID)
    data.Set("client_secret", c.clientSecret)
    data.Set("scope", "hub:write")
    
    resp, err := http.PostForm(c.tokenURL, data)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()
    
    var tokenResp struct {
        AccessToken string `json:"access_token"`
        ExpiresIn   int    `json:"expires_in"`
    }
    json.NewDecoder(resp.Body).Decode(&tokenResp)
    
    return tokenResp.AccessToken, nil
}
```
