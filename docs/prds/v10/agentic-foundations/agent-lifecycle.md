# Agent Build, Deploy, and Execution Lifecycle

**Purpose**: Technical reference for implementing agent lifecycle management in Zero-Ops v10  
**Audience**: Engineering team  
**Status**: DRAFT  
**References**: AgentRegistry + Kagent codebases

---

## Overview

Agent lifecycle has three distinct phases:
1. **Build** - Define agent configuration (stored in DB)
2. **Deploy** - Provision runtime infrastructure (Kubernetes pods)
3. **Execute** - Invoke agent on-demand (HTTP/A2A calls)

This document maps each phase to source code implementations.

---

## Phase 1: Build (Agent Registration)

### What Happens
Tenant defines agent configuration via API. Config stored in PostgreSQL. **No runtime deployed yet.**

### API Endpoint
```
POST /v0/agents
```

**Source**: `archived/agentic-ai/solo/agentregistry/openapi.yaml:95-115`

### Request Payload
```json
{
  "name": "com.example/billing-agent",
  "version": "1.0.0",
  "description": "Automate invoice generation",
  "systemMessage": "Generate invoices from Stripe data",
  "dependencies": {
    "agents": ["stripe-worker-agent"],
    "mcpServers": ["browser-tool-mcp"],
    "skills": ["billing-knowledge"]
  },
  "env": {
    "STRIPE_API_VERSION": "2023-10-16"
  }
}
```

**Schema**: `archived/agentic-ai/solo/agentregistry/openapi.yaml` (AgentJSON component)

### Backend Flow

#### 1. API Handler Receives Request
**Source**: `archived/agentic-ai/solo/agentregistry/internal/registry/api/handlers/v0/agents.go`

```go
// Handler validates and delegates to service layer
func HandleCreateAgent(ctx context.Context, input *CreateAgentInput) (*AgentResponse, error) {
    // Validation happens here
    agent, err := registryService.CreateAgent(ctx, input.Body)
    // ...
}
```

#### 2. Service Layer Stores Config
**Source**: `archived/agentic-ai/solo/agentregistry/internal/registry/service/registry_service.go`

```go
func (s *registryServiceImpl) CreateAgent(ctx context.Context, agent *models.AgentJSON) (*models.Agent, error) {
    // Stores agent JSON in PostgreSQL
    // Does NOT deploy runtime
    return s.db.CreateAgent(ctx, nil, agent)
}
```

#### 3. Database Layer Persists
**Source**: `archived/agentic-ai/solo/agentregistry/internal/registry/database/postgres.go`

```go
func (db *PostgreSQL) CreateAgent(ctx context.Context, tx pgx.Tx, agent *models.AgentJSON) (*models.Agent, error) {
    // INSERT INTO agents (name, version, config, ...) VALUES (...)
    // Agent config stored as JSONB
}
```

### Database Schema
```sql
CREATE TABLE agents (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    description TEXT,
    system_message TEXT,
    dependencies JSONB,  -- {agents: [], mcpServers: [], skills: []}
    env JSONB,           -- Environment variables
    created_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ,
    UNIQUE(name, version)
);
```

**Reference**: `archived/agentic-ai/solo/agentregistry/internal/registry/database/` (schema migrations)

### Key Takeaway
**Agent config is stored, but NO Kubernetes resources created yet.**

---

## Phase 2: Deploy (Runtime Provisioning)

### What Happens
Tenant triggers deployment. AgentRegistry provisions Kagent Agent CRD in Kubernetes. Kagent Controller creates Pod.

### API Endpoint
```
POST /v0/deployments
```

**Source**: `archived/agentic-ai/solo/agentregistry/openapi.yaml:398-425`

### Request Payload
```json
{
  "resourceType": "agent",
  "serverName": "com.example/billing-agent",
  "version": "1.0.0",
  "providerId": "spoke-pool-cluster-1",
  "env": {
    "TENANT_ID": "acme-corp"
  },
  "providerConfig": {
    "namespace": "tenant-acme",
    "replicas": 1
  }
}
```

### Backend Flow

#### 1. API Handler Receives Deployment Request
**Source**: `archived/agentic-ai/solo/agentregistry/internal/registry/api/handlers/v0/deployments.go:97-134`

```go
func HandleDeployServer(ctx context.Context, input *DeployServerInput) (*DeploymentResponse, error) {
    deployment, err := registryService.CreateDeployment(ctx, input.Body)
    // Returns deployment record with status "deploying"
}
```

#### 2. Service Layer Resolves Deployment Adapter
**Source**: `archived/agentic-ai/solo/agentregistry/internal/registry/service/registry_service.go:1100-1145`

```go
func (s *registryServiceImpl) CreateDeployment(ctx context.Context, req *models.Deployment) (*models.Deployment, error) {
    // 1. Resolve adapter by providerId (local or kubernetes)
    adapter, err := s.resolveDeploymentAdapterByProviderID(ctx, providerID)
    
    // 2. Create deployment record in DB (status: "deploying")
    created, err := s.createManagedDeploymentRecord(ctx, &deploymentReq)
    
    // 3. Call adapter.Deploy() - THIS PROVISIONS RUNTIME
    actionResult, deployErr := adapter.Deploy(ctx, created)
    
    // 4. Update deployment status based on result
    if err := s.applyDeploymentActionResult(ctx, created.ID, actionResult); err != nil {
        return nil, err
    }
    
    return updated, nil
}
```

#### 3. Kubernetes Deployment Adapter Provisions Agent CRD
**Source**: `archived/agentic-ai/solo/agentregistry/internal/registry/platforms/kubernetes/deployment_adapter_kubernetes.go:30-48`

```go
func (a *kubernetesDeploymentAdapter) Deploy(ctx context.Context, req *models.Deployment) (*models.DeploymentActionResult, error) {
    // Validates request
    if err := utils.ValidateDeploymentRequest(req, false); err != nil {
        return nil, err
    }
    
    // Materializes Kagent Agent CRD YAML
    manifests, err := a.materializer.Materialize(ctx, req)
    
    // Applies to Kubernetes cluster
    if err := a.applier.Apply(ctx, req, manifests); err != nil {
        return nil, err
    }
    
    return &models.DeploymentActionResult{
        Status: "deployed",
        Metadata: map[string]string{
            "namespace": req.ProviderConfig["namespace"],
            "agentName": req.ServerName,
        },
    }, nil
}
```

#### 4. Materializer Creates Kagent Agent CRD
**Source**: `archived/agentic-ai/solo/agentregistry/internal/registry/platforms/kubernetes/` (materializer logic)

```yaml
# Generated Kagent Agent CRD
apiVersion: kagent.dev/v1alpha2
kind: Agent
metadata:
  name: billing-agent
  namespace: tenant-acme
spec:
  type: Declarative
  declarative:
    systemMessage: "Generate invoices from Stripe data"
    modelConfig:
      name: gpt-4-turbo
    tools:
      - type: Agent
        agent:
          name: stripe-worker-agent
      - type: McpServer
        mcpServer:
          name: browser-tool-mcp
    memory:
      type: kagent
      config:
        namespace: billing-agent
```

#### 5. Kagent Controller Reconciles Agent CRD
**Source**: `archived/agentic-ai/solo/kagent/go/core/internal/controller/agent_controller.go`

```go
func (r *AgentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // 1. Fetch Agent CRD
    agent := &v1alpha2.Agent{}
    if err := r.Get(ctx, req.NamespacedName, agent); err != nil {
        return ctrl.Result{}, err
    }
    
    // 2. Translate Agent CRD to Kubernetes resources
    deployment, configMap, service := r.translator.Translate(agent)
    
    // 3. Create/Update Deployment (Agent Pod)
    if err := r.Client.Create(ctx, deployment); err != nil {
        return ctrl.Result{}, err
    }
    
    // 4. Register A2A handler for this agent
    if err := r.a2aRegistrar.RegisterAgent(agent); err != nil {
        return ctrl.Result{}, err
    }
    
    return ctrl.Result{}, nil
}
```

**Reference**: `archived/agentic-ai/solo/kagent/go/core/internal/controller/agent_controller.go:38-150`

#### 6. Translator Creates Kubernetes Deployment
**Source**: `archived/agentic-ai/solo/kagent/go/core/internal/controller/translator/agent/deployments.go:121`

```go
func (t *Translator) TranslateToDeployment(agent *v1alpha2.Agent) *appsv1.Deployment {
    return &appsv1.Deployment{
        ObjectMeta: metav1.ObjectMeta{
            Name:      agent.Name,
            Namespace: agent.Namespace,
        },
        Spec: appsv1.DeploymentSpec{
            Replicas: ptr.To(int32(1)), // Default replicas
            Template: corev1.PodTemplateSpec{
                Spec: corev1.PodSpec{
                    RuntimeClassName: ptr.To("gvisor"), // Platform-injected
                    Containers: []corev1.Container{
                        {
                            Name:  "agent",
                            Image: "kagent-adk:latest",
                            Env: []corev1.EnvVar{
                                {Name: "LITELLM_ENDPOINT", Value: "http://litellm-gateway:8080"},
                                {Name: "GUARDRAIL_ENDPOINT", Value: "http://guardrail-engine:8080"},
                                {Name: "MEMORY_SERVICE_ENDPOINT", Value: "http://memory-service:8080"},
                            },
                        },
                    },
                },
            },
        },
    }
}
```

**Reference**: `archived/agentic-ai/solo/kagent/go/core/internal/controller/translator/agent/deployments.go`

#### 7. KEDA ScaledObject Created (Scale-to-Zero)
**Source**: Platform injects KEDA ScaledObject alongside Deployment

```yaml
apiVersion: keda.sh/v1alpha1
kind: ScaledObject
metadata:
  name: billing-agent-scaler
  namespace: tenant-acme
spec:
  scaleTargetRef:
    name: billing-agent
  minReplicaCount: 0  # Scale to zero when idle
  maxReplicaCount: 10
  triggers:
    - type: http
      metadata:
        targetPendingRequests: "1"
```

**Reference**: KEDA HTTP Add-on documentation (external dependency)

#### 8. A2A Handler Registered
**Source**: `archived/agentic-ai/solo/kagent/go/core/internal/a2a/a2a_registrar.go:111-126`

```go
func (r *A2ARegistrar) RegisterAgent(agent *v1alpha2.Agent) error {
    agentRef := fmt.Sprintf("%s/%s", agent.Namespace, agent.Name)
    
    // Create A2A client pointing to agent pod
    client, err := a2aclient.NewA2AClient(
        fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", agent.Name, agent.Namespace),
    )
    
    // Register handler in A2AHandlerMux
    return r.handlerMux.SetAgentHandler(agentRef, client, agentCard, tracing)
}
```

**Reference**: `archived/agentic-ai/solo/kagent/go/core/internal/a2a/a2a_registrar.go`

### Deployment State Tracking
**Source**: `archived/agentic-ai/solo/agentregistry/internal/registry/database/postgres.go:2871-2900`

```sql
CREATE TABLE deployments (
    id UUID PRIMARY KEY,
    server_name TEXT NOT NULL,      -- Agent name
    version TEXT NOT NULL,
    status TEXT NOT NULL,            -- "deploying", "deployed", "failed"
    provider_id TEXT NOT NULL,       -- Which cluster
    provider_metadata JSONB,         -- K8s namespace, pod name, etc.
    deployed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ
);
```

### Key Takeaway
**Agent Pod is now running (or scaled to 0). Ready to receive invocations.**

---

## Phase 3: Execute (On-Demand Invocation)

### What Happens
User sends request to agent. Kagent Controller routes to agent pod. If pod sleeping, KEDA scales 0→1. Agent executes task.

### API Endpoint
```
POST /api/a2a/{namespace}/{agent-name}
```

**Source**: `archived/agentic-ai/solo/kagent/go/core/internal/httpserver/server.go:41-284`

### Request Payload (A2A Protocol)
```json
{
  "jsonrpc": "2.0",
  "method": "message/stream",
  "params": {
    "message": "Generate invoice for customer acme-123",
    "context": {
      "tenant_id": "acme-corp",
      "user_id": "user-456"
    }
  },
  "id": "req-789"
}
```

**Reference**: A2A Protocol spec (external: `trpc.group/trpc-go/trpc-a2a-go/protocol`)

### Execution Flow

#### 1. HTTP Request Arrives at Kagent Controller
**Source**: `archived/agentic-ai/solo/kagent/go/core/internal/httpserver/server.go:284`

```go
// Router configuration
s.router.PathPrefix(APIPathA2A + "/{namespace}/{name}").Handler(s.config.A2AHandler)
```

**Path**: `/api/a2a/tenant-acme/billing-agent`

#### 2. A2AHandlerMux Routes to Agent Handler
**Source**: `archived/agentic-ai/solo/kagent/go/core/internal/a2a/a2a_handler_mux.go:82-115`

```go
func (a *handlerMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Extract namespace and agent name from URL
    vars := mux.Vars(r)
    agentNamespace := vars["namespace"]  // "tenant-acme"
    agentName := vars["name"]            // "billing-agent"
    
    handlerName := common.ResourceRefString(agentNamespace, agentName)
    
    // Get registered handler for this agent
    handlerHandler, ok := a.getHandler(handlerName)
    if !ok {
        http.Error(w, fmt.Sprintf("Agent %s not found", handlerName), http.StatusNotFound)
        return
    }
    
    // Forward request to agent pod
    handlerHandler.ServeHTTP(w, r)
}
```

#### 3. KEDA Intercepts Request (If Pod Sleeping)
**Flow**:
```
HTTP Request → KEDA HTTP Interceptor
                     ↓
            Check: Is pod running?
                     ↓
            NO → Scale Deployment 0→1
                     ↓
            Wait for pod ready (2-5 seconds)
                     ↓
            Forward request to pod
```

**Reference**: KEDA HTTP Add-on (external dependency)

#### 4. Request Reaches Agent Pod
**Source**: Agent Pod runs Kagent ADK runtime

```go
// Agent Pod HTTP server (inside container)
func (a *Agent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // 1. Parse A2A request
    var req protocol.Message
    json.NewDecoder(r.Body).Decode(&req)
    
    // 2. Load agent config from ConfigMap
    config := a.loadConfig()
    
    // 3. Apply guardrails (pre-LLM)
    if err := a.guardrailEngine.PreFilter(req.Params.Message); err != nil {
        http.Error(w, "Guardrail violation", http.StatusForbidden)
        return
    }
    
    // 4. Query memory service for context
    context := a.memoryService.SemanticSearch(req.Params.Message)
    
    // 5. Call LLM via LiteLLM Gateway
    response := a.llmGateway.Generate(req.Params.Message, context)
    
    // 6. Apply guardrails (post-LLM)
    filtered := a.guardrailEngine.PostFilter(response)
    
    // 7. Execute tools if needed
    if toolCall := parseToolCall(filtered); toolCall != nil {
        result := a.executeTool(toolCall)
        filtered = appendToolResult(filtered, result)
    }
    
    // 8. Return response
    json.NewEncoder(w).Encode(protocol.Response{Result: filtered})
}
```

**Reference**: `archived/agentic-ai/solo/kagent/go/adk/pkg/agent/agent.go` (ADK runtime)

#### 5. Platform Services Invoked

##### Memory Service (Context Retrieval)
```go
// Agent calls Memory Service
resp, err := http.Post(
    "http://memory-service:8080/search",
    "application/json",
    bytes.NewBuffer([]byte(`{"query": "invoice generation", "namespace": "billing-agent"}`)),
)
// Returns: Top-K relevant chunks from pgvector
```

**Reference**: Platform service (to be implemented in Zero-Ops)

##### Guardrail Engine (Pre/Post LLM Filtering)
```go
// Pre-LLM check
resp, err := http.Post(
    "http://guardrail-engine:8080/pre-filter",
    "application/json",
    bytes.NewBuffer([]byte(`{"prompt": "...", "policies": ["pii_filter", "topic_denial"]}`)),
)
// Returns: {allowed: true} or {allowed: false, reason: "PII detected"}

// Post-LLM check
resp, err := http.Post(
    "http://guardrail-engine:8080/post-filter",
    "application/json",
    bytes.NewBuffer([]byte(`{"response": "...", "policies": ["data_access_restriction"]}`)),
)
```

**Reference**: Platform service (to be implemented in Zero-Ops)

##### LiteLLM Gateway (Model Routing)
```go
// Agent calls LiteLLM
resp, err := http.Post(
    "http://litellm-gateway:8080/chat/completions",
    "application/json",
    bytes.NewBuffer([]byte(`{
        "model": "gpt-4-turbo",
        "messages": [{"role": "user", "content": "..."}]
    }`)),
)
// LiteLLM routes to OpenAI, tracks token usage
```

**Reference**: LiteLLM (external dependency, already in tech stack)

#### 6. Task Completion Event Emitted
**Source**: Agent Pod emits event to NATS

```go
// After successful task completion
event := models.Event{
    Type:      "agent.task_completed",
    AgentID:   "billing-agent",
    TenantID:  "acme-corp",
    TaskID:    "task-123",
    Outcome:   "invoice_generated",
    Metadata: map[string]interface{}{
        "invoice_id": "inv-456",
        "amount":     "$1,234.56",
    },
}
natsClient.Publish("spoke.acme-corp.agent.task_completed", event)
```

**Reference**: NATS event pattern from v9 PRD

#### 7. Outcome Listener Processes Billing Event
**Source**: Hub Event Router consumes NATS event

```go
// Hub Event Router (Hub Cluster)
func (r *EventRouter) HandleTaskCompleted(event models.Event) {
    // 1. Validate task completion
    if event.Outcome == "" {
        return
    }
    
    // 2. Write billing record
    billingRecord := models.BillingRecord{
        TenantID:    event.TenantID,
        AgentID:     event.AgentID,
        OutcomeType: event.Outcome,
        UnitPrice:   0.50, // $0.50 per custom agent task
        Timestamp:   time.Now(),
    }
    db.InsertBillingRecord(billingRecord)
}
```

**Reference**: Hub Event Router pattern from v9 PRD

### Execution Trace (OpenTelemetry)
**Source**: `archived/agentic-ai/solo/kagent/go/core/internal/telemetry/tracing.go`

```go
// Spans created during execution
span1 := tracer.Start(ctx, "agent.invoke")
span2 := tracer.Start(ctx, "memory.search")
span3 := tracer.Start(ctx, "guardrail.pre_filter")
span4 := tracer.Start(ctx, "llm.generate")
span5 := tracer.Start(ctx, "guardrail.post_filter")
span6 := tracer.Start(ctx, "tool.execute")
```

**Reference**: `archived/agentic-ai/solo/kagent/go/core/internal/telemetry/tracing.go`

### Key Takeaway
**Agent executes task using platform services. Billing event emitted on completion.**

---

## Complete Lifecycle Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│ PHASE 1: BUILD (Agent Registration)                            │
│                                                                 │
│ Tenant → POST /v0/agents → AgentRegistry API                   │
│                                  ↓                              │
│                            PostgreSQL                           │
│                            (agent config stored)                │
│                                                                 │
│ Source: agentregistry/internal/registry/service/                │
│         registry_service.go:CreateAgent()                       │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│ PHASE 2: DEPLOY (Runtime Provisioning)                         │
│                                                                 │
│ Tenant → POST /v0/deployments → AgentRegistry API              │
│                                       ↓                         │
│                                 Deployment Adapter              │
│                                       ↓                         │
│                                 Kagent Agent CRD                │
│                                       ↓                         │
│                                 Kagent Controller               │
│                                       ↓                         │
│                    ┌──────────────────┴──────────────────┐     │
│                    ↓                                      ↓     │
│              Agent Pod                              KEDA Scaler │
│              (replicas: 1)                          (min: 0)    │
│                    ↓                                            │
│              A2A Handler Registered                             │
│                                                                 │
│ Source: agentregistry/internal/registry/platforms/kubernetes/   │
│         deployment_adapter_kubernetes.go:Deploy()               │
│         kagent/go/core/internal/controller/agent_controller.go  │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│ PHASE 3: EXECUTE (On-Demand Invocation)                        │
│                                                                 │
│ User → POST /api/a2a/{namespace}/{agent} → Kagent Controller   │
│                                                  ↓              │
│                                            A2AHandlerMux        │
│                                                  ↓              │
│                                            KEDA Interceptor     │
│                                                  ↓              │
│                                            Scale 0→1 (if needed)│
│                                                  ↓              │
│                                            Agent Pod            │
│                                                  ↓              │
│                    ┌─────────────────────────────┴─────────┐   │
│                    ↓                 ↓                 ↓        │
│              Memory Service    Guardrail Engine   LiteLLM      │
│                    ↓                 ↓                 ↓        │
│                    └─────────────────┴─────────────────┘        │
│                                      ↓                          │
│                                Response + Billing Event         │
│                                                                 │
│ Source: kagent/go/core/internal/a2a/a2a_handler_mux.go         │
│         kagent/go/adk/pkg/agent/agent.go                       │
└─────────────────────────────────────────────────────────────────┘
```

---

## Implementation Checklist for Zero-Ops Team

### Phase 1: Build (AgentRegistry Integration)
- [ ] Deploy AgentRegistry in Hub Cluster
- [ ] Configure PostgreSQL for agent storage
- [ ] Expose `/v0/agents` API via Platform Console
- [ ] Implement UI form for agent creation

**Reference Code**:
- `archived/agentic-ai/solo/agentregistry/internal/registry/api/handlers/v0/agents.go`
- `archived/agentic-ai/solo/agentregistry/internal/registry/service/registry_service.go`

### Phase 2: Deploy (Kagent Integration)
- [ ] Deploy Kagent Controller in Spoke Clusters
- [ ] Implement Kubernetes Deployment Adapter
- [ ] Configure KEDA HTTP Add-on for scale-to-zero
- [ ] Implement A2A handler registration

**Reference Code**:
- `archived/agentic-ai/solo/agentregistry/internal/registry/platforms/kubernetes/deployment_adapter_kubernetes.go`
- `archived/agentic-ai/solo/kagent/go/core/internal/controller/agent_controller.go`
- `archived/agentic-ai/solo/kagent/go/core/internal/a2a/a2a_registrar.go`

### Phase 3: Execute (Platform Services)
- [ ] Implement Memory Service (document ingestion, semantic search)
- [ ] Implement Guardrail Engine (pre/post LLM filtering)
- [ ] Deploy LiteLLM Gateway per tenant
- [ ] Implement Outcome Listener (billing events)
- [ ] Configure OpenTelemetry tracing

**Reference Code**:
- `archived/agentic-ai/solo/kagent/go/adk/pkg/agent/agent.go` (ADK runtime patterns)
- `archived/agentic-ai/solo/kagent/go/core/internal/telemetry/tracing.go`

### Cross-Cutting Concerns
- [ ] Implement AgentGateway JWT validation
- [ ] Configure Ory stack for authentication
- [ ] Set up NATS JetStream for event streaming
- [ ] Implement Hub Event Router for billing
- [ ] Configure VictoriaMetrics for agent metrics

---

## Key Source Code References

| Component | Source File | Purpose |
|---|---|---|
| Agent Registration API | `agentregistry/internal/registry/api/handlers/v0/agents.go` | POST /v0/agents endpoint |
| Agent Storage | `agentregistry/internal/registry/database/postgres.go` | PostgreSQL persistence |
| Deployment API | `agentregistry/internal/registry/api/handlers/v0/deployments.go` | POST /v0/deployments endpoint |
| K8s Deployment Adapter | `agentregistry/internal/registry/platforms/kubernetes/deployment_adapter_kubernetes.go` | Provisions Kagent CRD |
| Kagent Controller | `kagent/go/core/internal/controller/agent_controller.go` | Reconciles Agent CRD |
| Deployment Translator | `kagent/go/core/internal/controller/translator/agent/deployments.go` | Creates K8s Deployment |
| A2A Handler Mux | `kagent/go/core/internal/a2a/a2a_handler_mux.go` | Routes invocations to pods |
| A2A Registrar | `kagent/go/core/internal/a2a/a2a_registrar.go` | Registers agent handlers |
| ADK Runtime | `kagent/go/adk/pkg/agent/agent.go` | Agent execution logic |
| Tracing | `kagent/go/core/internal/telemetry/tracing.go` | OpenTelemetry spans |

---

## Next Steps

1. Review this document with engineering team
2. Validate source code references against latest codebase versions
3. Create implementation tasks for each phase
4. Define integration tests for end-to-end lifecycle
5. Document platform service APIs (Memory, Guardrail, etc.)
