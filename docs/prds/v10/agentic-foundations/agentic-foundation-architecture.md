# Agentic Foundation Architecture
## Zero-Ops v10 - Kagent Native Integration

**Purpose**: Define how Kagent agents consume Zero-Ops platform services by default  
**Audience**: Product team, engineering leads  
**Status**: DRAFT

---

## Core Principles

1. **Three-phase agent lifecycle** (see [agent-lifecycle.md](./agent-lifecycle.md))  
   - **Build**: Define agent config via AgentRegistry API → PostgreSQL  
   - **Deploy**: Provision runtime via Kagent Controller → Agent Pod  
   - **Execute**: Invoke on-demand via A2A protocol → Platform services

2. **Tenant agents are API-driven, not GitOps**  
   Tenants create agents via Platform Console → AgentRegistry API → PostgreSQL  
   Deployment triggers Kagent CRD creation (no Git commits for tenant agents)

3. **Platform agents are GitOps-managed**  
   Platform default agents (Hub Infra Agents) committed to Git → ArgoCD  
   Infrastructure layer (AINativeSaaS XRD, baseline services) uses GitOps

4. **Every Kagent Agent automatically inherits platform services**  
   Regardless of creation method, all agents get Memory, Guardrails, LiteLLM, etc.

---

## Architecture Components

### AgentRegistry (Hub Cluster)
Central registry for agent definitions and deployment orchestration.

**Responsibilities**:
- Store agent configurations (PostgreSQL)
- Expose agent CRUD APIs (`/v0/agents`, `/v0/deployments`)
- Trigger Kagent CRD provisioning via Deployment Adapters
- Track deployment status

**Source**: `archived/agentic-ai/solo/agentregistry/`

### Kagent Controller (Spoke Clusters)
Kubernetes-native agent runtime framework.

**Responsibilities**:
- Reconcile Agent CRDs → Kubernetes Deployments
- Register A2A handlers for agent invocation
- Inject platform service endpoints (Memory, Guardrails, LiteLLM)
- Manage agent lifecycle (create, update, delete)

**Source**: `archived/agentic-ai/solo/kagent/go/core/`

---

## Service Layer Mapping

### 1. AUTOMATE LAYER → Kagent Runtime

| Platform Service | Kagent Feature | Integration Method |
|---|---|---|
| **Agent Runtime Service** | Kagent Agent Pod (ADK) | Crossplane provisions pod from Agent CRD |
| **Workflow Orchestrator** | Kagent A2A Protocol | Collaborator → Worker agent routing |
| **State Machine Service** | Kagent Task/Session API | Stores state in Control Plane DB |
| **Session Manager** | Kagent Session CRD | PostgreSQL backend via ADK |
| **Code Interpreter** | AgentSandbox (gVisor) | Mounted as sidecar to Agent Pod |
| **Browser Tool Service** | MCP Tool Server | Exposed via `spec.declarative.tools[type=McpServer]` |

**Tenant Agent Creation (API)**:
```json
POST /v0/agents
{
  "name": "billing-automation-agent",
  "version": "1.0.0",
  "description": "Automate invoice generation",
  "systemMessage": "Generate invoices from Stripe data",
  "dependencies": {
    "agents": ["stripe-worker-agent"],
    "mcpServers": ["browser-tool-mcp"]
  }
}
```
→ Stored in AgentRegistry PostgreSQL  
→ Tenant triggers: `POST /v0/deployments`  
→ Deployment Adapter provisions Kagent Agent CRD in Spoke  
→ Kagent Controller creates Pod + injects platform services

**Source**: `agentregistry/internal/registry/api/handlers/v0/agents.go`

**Platform Agent (GitOps)**:
```yaml
apiVersion: kagent.dev/v1alpha2
kind: Agent
metadata:
  name: prometheus-worker-agent
spec:
  declarative:
    systemMessage: "Query Prometheus metrics"
    tools:
      - type: McpServer
        mcpServer: {name: prometheus-mcp}
```
→ Committed to infrastructure Git repo  
→ ArgoCD syncs to Spoke  
→ Kagent Controller reconciles + injects platform services

**Source**: `kagent/go/core/internal/controller/agent_controller.go`

---

### 2. CONNECT LAYER → Kagent Memory & Context

| Platform Service | Kagent Feature | Integration Method |
|---|---|---|
| **Document Processor** | Context Service API | Tenant uploads docs via Platform Console |
| **Chunking Strategy** | Context Service | Auto-applied during ingestion |
| **Embedding Service** | Context Service | Standard model for all tenants |
| **Reranker Service** | Context Service | Applied during semantic search |
| **Knowledge Graph** | Kagent Memory CRD | Stores relationships in pgvector |
| **Memory Layer** | Kagent Memory Service | `spec.declarative.memory` config |

**Kagent Memory Integration**:
```yaml
apiVersion: kagent.dev/v1alpha2
kind: Agent
metadata:
  name: customer-support-agent
spec:
  declarative:
    memory:
      type: kagent  # Uses platform Memory Service
      config:
        namespace: customer-support  # Isolated pgvector namespace
        # Platform provides: Document ingestion, chunking, embeddings, reranking
```

**Memory Types Auto-Configured**:
- **Short-term**: Current session (PostgreSQL)
- **Long-term**: Embeddings (pgvector via Kagent Memory Service)
- **Episodic**: Past tasks (Task table)
- **Semantic**: Knowledge graph (pgvector relationships)

---

### 3. GOVERN LAYER → Kagent Security & Guardrails

| Platform Service | Kagent Feature | Integration Method |
|---|---|---|
| **Agent Identity** | Kagent Agent Metadata | `metadata.name` + `tenant_id` |
| **Credential Vault** | Infisical + ESO | Secrets mounted to Agent Pod |
| **Permission Resolver** | Kagent A2A Auth | AgentGateway validates via auth-proxy |
| **Guardrail Engine** | Kagent Tool Approval | `spec.declarative.tools[].requireApproval` |
| **Audit Logger** | Kagent Event API | All actions logged to OpenSearch |
| **Confidence Scorer** | Platform Service | Injected as pre-LLM middleware |
| **HITL Approval** | Kagent HITL | `go/adk/pkg/a2a/hitl.go` |

**Kagent Guardrail Integration**:
```yaml
apiVersion: kagent.dev/v1alpha2
kind: Agent
metadata:
  name: remediation-agent
spec:
  declarative:
    tools:
      - type: McpServer
        mcpServer:
          name: k8s-tools-mcp
          toolNames: [k8s_get_pods]  # Read-only
          # Platform enforces: Global guardrails (PII filter, topic denial)
      - type: McpServer
        mcpServer:
          name: k8s-tools-mcp
          toolNames: [k8s_delete_pod]
          requireApproval: [k8s_delete_pod]  # HITL for destructive ops
          # Platform enforces: Task-specific guardrails + approval queue
```

**Guardrail Enforcement Flow**:
```
1. Agent calls tool → AgentGateway
2. AgentGateway loads guardrails from Control Plane DB
3. Pre-LLM filter: Check global policies (PII, topic denial)
4. If requireApproval: Suspend task, fire HITL event
5. Post-LLM filter: Check task-specific policies (data access)
6. Return response or rejection
```

---

### 4. DEPLOY LAYER → Kagent Infrastructure

| Platform Service | Kagent Feature | Integration Method |
|---|---|---|
| **Rate Limiter** | AgentGateway | Per-agent quotas enforced at gateway |
| **Cost Tracker** | Kagent Metrics | Token usage in `event.Data` JSON |
| **Model Router** | LiteLLM Gateway | `spec.declarative.modelConfig` |
| **Fallback Handler** | LiteLLM | Auto-retry with fallback models |
| **KEDA Scale-to-Zero** | Kagent Deployment | Worker agents scale 0→1 on A2A call |
| **AgentSandbox** | gVisor Runtime | Kagent Pod `runtimeClassName: gvisor` |

**Kagent Deployment Auto-Configuration**:
```yaml
# Tenant creates Agent CRD
apiVersion: kagent.dev/v1alpha2
kind: Agent
metadata:
  name: prometheus-worker-agent
spec:
  declarative:
    modelConfig:
      name: gpt-4-turbo  # Routes via LiteLLM
    # Platform injects:
    # - Rate limiter (100 req/min default)
    # - Cost tracker (token usage → billing DB)
    # - KEDA ScaledObject (scale to 0 after 5min idle)
    # - gVisor runtime class
```

**Crossplane Translates to**:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: prometheus-worker-agent
spec:
  replicas: 1  # KEDA overrides to 0 when idle
  template:
    spec:
      runtimeClassName: gvisor  # Platform-injected
      containers:
        - name: agent
          env:
            - name: LITELLM_ENDPOINT
              value: "http://litellm-gateway:8080"  # Platform-injected
            - name: GUARDRAIL_ENDPOINT
              value: "http://guardrail-engine:8080"  # Platform-injected
---
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: prometheus-worker-agent-scaler
spec:
  scaleTargetRef:
    name: prometheus-worker-agent
  minReplicaCount: 0  # Platform-injected
  maxReplicaCount: 10
  triggers:
    - type: http
      metadata:
        targetPendingRequests: "1"
```

---

## Kagent Agent Lifecycle with Platform Services

**See [agent-lifecycle.md](./agent-lifecycle.md) for complete technical details with source code references.**

### Phase 1: Build (Agent Definition)
```
1. Tenant defines agent via Platform Console UI
2. Platform Console → AgentRegistry API (POST /v0/agents)
3. AgentRegistry stores agent JSON in PostgreSQL (versioned)
4. Agent config stored, NO runtime deployed yet
```

**Source**: `agentregistry/internal/registry/service/registry_service.go:CreateAgent()`

### Phase 2: Deploy (Runtime Provisioning)
```
1. Tenant triggers deployment (POST /v0/deployments)
2. AgentRegistry → Deployment Adapter (kubernetes)
3. Deployment Adapter → Kagent Agent CRD (applied to Spoke)
4. Kagent Controller reconciles Agent CRD
5. Platform injects services:
   ├─ Memory Service config (pgvector namespace)
   ├─ Guardrail policies (from Control Plane DB)
   ├─ LiteLLM endpoint (model routing)
   ├─ KEDA ScaledObject (scale-to-zero)
   ├─ gVisor runtime (sandboxing)
   └─ Credential mounts (Infisical secrets)
6. Agent Pod starts in "Ready" state (or scaled to 0)
7. A2A handler registered in Kagent Controller
```

**Source**: `agentregistry/internal/registry/platforms/kubernetes/deployment_adapter_kubernetes.go:Deploy()`  
**Source**: `kagent/go/core/internal/controller/agent_controller.go:Reconcile()`

### Phase 3: Execute (On-Demand Invocation)
```
1. User sends request: POST /api/a2a/{namespace}/{agent-name}
2. Kagent Controller → A2AHandlerMux routes to agent
3. If pod sleeping: KEDA scales 0→1 (2-5 seconds)
4. Agent Pod executes:
   ├─ Load config from ConfigMap
   ├─ Apply guardrails (pre-LLM)
   ├─ Query Memory Service (semantic search)
   ├─ Call LiteLLM Gateway (model inference)
   ├─ Apply guardrails (post-LLM)
   ├─ Execute tools (if needed)
   └─ Return response
5. Emit task completion event to NATS
6. Outcome Listener writes billing record
```

**Source**: `kagent/go/core/internal/a2a/a2a_handler_mux.go:ServeHTTP()`  
**Source**: `kagent/go/adk/pkg/agent/agent.go` (ADK runtime patterns)

**Key Distinction:**
- **Phase 1 (Build)**: Agent config stored in DB, no K8s resources
- **Phase 2 (Deploy)**: Kagent CRD + Pod created, A2A handler registered
- **Phase 3 (Execute)**: HTTP request triggers agent execution via platform services

### Execution Flow
```
1. User request → AgentGateway (JWT validation)
2. AgentGateway → Rate Limiter (check quota)
3. AgentGateway → Guardrail Engine (pre-LLM filter)
4. AgentGateway → Kagent Controller (POST /api/a2a/{namespace}/{agent})
5. Kagent A2AHandlerMux routes to agent handler
   ├─ If agent sleeping: KEDA scales 0→1 (2-5s delay)
   └─ Agent wakes, loads config from ConfigMap
6. Agent → Memory Service (semantic search for context)
7. Agent → LiteLLM Gateway (model inference)
8. LiteLLM → Guardrail Engine (post-LLM filter)
9. Agent → MCP Tool Server (execute tool if needed)
10. Agent → Event API (log action to OpenSearch)
11. Agent → Task API (update task status)
12. If task completed → NATS event → Outcome Listener → Billing DB
13. Response → User
```

**Source**: See [agent-lifecycle.md](./agent-lifecycle.md) Phase 3 for detailed code references

---

## Platform Service Defaults

Every Kagent Agent automatically gets:

| Service | Default Configuration | Override Method |
|---|---|---|
| Memory | pgvector namespace = `{agent-name}` | `spec.declarative.memory.config.namespace` |
| Guardrails | Global policies (PII, topic denial) | Add task-specific in `requireApproval` |
| Model | GPT-4-turbo via LiteLLM | `spec.declarative.modelConfig.name` |
| Rate Limit | 100 req/min per agent | Platform Admin adjusts in Control Plane DB |
| Scale-to-Zero | Idle timeout: 5 minutes | `annotations: keda.sh/idle-timeout: "10m"` |
| Sandbox | gVisor runtime | Cannot override (security requirement) |
| Audit | All actions logged | Cannot disable (compliance requirement) |

---

## Tenant Experience

### Before (Manual Setup)
```
1. Deploy PostgreSQL with pgvector
2. Build RAG pipeline (chunking, embeddings)
3. Implement guardrails (PII filter, approval logic)
4. Set up LLM gateway (rate limiting, fallback)
5. Configure observability (traces, metrics)
6. Build agent runtime
7. Deploy to Kubernetes
```

### After (AgentRegistry + Kagent on Zero-Ops)
```
1. Define agent JSON in Platform Console
2. POST to AgentRegistry API
3. Platform provisions everything automatically
4. Agent ready in 2 minutes
```

---

## Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│ TENANT DEFINES AGENT (Platform Console UI)                     │
│                                                                 │
│ {                                                               │
│   "name": "billing-agent",                                      │
│   "version": "1.0.0",                                           │
│   "systemMessage": "...",                                       │
│   "dependencies": {                                             │
│     "agents": ["stripe-worker"],                               │
│     "mcpServers": ["browser-tool-mcp"]                         │
│   }                                                             │
│ }                                                               │
└────────────────────────┬────────────────────────────────────────┘
                         │ POST /v0/agents
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ AGENTREGISTRY API (Hub Cluster)                                │
│ - Stores agent JSON in PostgreSQL (versioned)                  │
│ - Agent config stored, NO runtime yet                          │
└────────────────────────┬────────────────────────────────────────┘
                         │ Tenant triggers: POST /v0/deployments
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ DEPLOYMENT ADAPTER (Kubernetes)                                │
│ - Materializes Kagent Agent CRD                                │
│ - Applies to Spoke Cluster                                     │
└────────────────────────┬────────────────────────────────────────┘
                         │ Agent CRD created
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ KAGENT CONTROLLER (Spoke Cluster)                              │
│                                                                 │
│ Reconciles Agent CRD → Provisions:                             │
│ ┌─────────────────────────────────────────────────────────────┐ │
│ │ Agent Pod (ADK Runtime)                                     │ │
│ │ ├─ Env: LITELLM_ENDPOINT (injected)                         │ │
│ │ ├─ Env: GUARDRAIL_ENDPOINT (injected)                       │ │
│ │ ├─ Env: MEMORY_SERVICE_ENDPOINT (injected)                  │ │
│ │ ├─ RuntimeClass: gvisor (injected)                          │ │
│ │ └─ Secrets: Infisical mounts (injected)                     │ │
│ └─────────────────────────────────────────────────────────────┘ │
│ ┌─────────────────────────────────────────────────────────────┐ │
│ │ KEDA ScaledObject (injected)                                │ │
│ │ - minReplicaCount: 0                                        │ │
│ │ - HTTP trigger on A2A endpoint                              │ │
│ └─────────────────────────────────────────────────────────────┘ │
└────────────────────────┬────────────────────────────────────────┘
                         │ Agent calls platform services
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ PLATFORM SERVICES (Auto-Consumed by Kagent)                    │
│                                                                 │
│ ┌──────────────┐ ┌──────────────┐ ┌──────────────────────────┐ │
│ │Memory Service│ │Guardrail     │ │LiteLLM Gateway           │ │
│ │(pgvector)    │ │Engine        │ │(Model Router)            │ │
│ └──────────────┘ └──────────────┘ └──────────────────────────┘ │
│                                                                 │
│ ┌──────────────┐ ┌──────────────┐ ┌──────────────────────────┐ │
│ │Context       │ │Audit Logger  │ │Outcome Listener          │ │
│ │Service       │ │(OpenSearch)  │ │(Billing)                 │ │
│ └──────────────┘ └──────────────┘ └──────────────────────────┘ │
│                                                                 │
│ ┌──────────────┐ ┌──────────────┐ ┌──────────────────────────┐ │
│ │Trace         │ │Metrics       │ │HITL Approval Queue       │ │
│ │Collector     │ │Aggregator    │ │                          │ │
│ └──────────────┘ └──────────────┘ └──────────────────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

---

## Key Takeaways for Product Team

1. **Kagent is the agent runtime framework** - Zero-Ops provides the platform services
2. **Agent CRD is the only interface** - Tenants never configure services directly
3. **Platform services are injected automatically** - Memory, guardrails, LLM routing, observability
4. **Scale-to-zero by default** - Worker agents only consume compute when active
5. **Multi-tenancy enforced at platform layer** - Kagent agents inherit tenant isolation
6. **Billing tied to outcomes** - Task completion events flow to billing engine
7. **IDE MCP clients interact via AgentGateway** - Kagent agents are the execution backend

---

## Next Steps

1. Define Crossplane Composition for Agent CRD → Platform Service injection
2. Build Context Service API (document ingestion, semantic search)
3. Extend Guardrail Engine to integrate with Kagent A2A protocol
4. Configure KEDA HTTP Add-on for worker agent scale-to-zero
5. Implement Outcome Listener for billing events
