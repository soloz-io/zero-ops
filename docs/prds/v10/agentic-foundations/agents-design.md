Looking at your Hub-Spoke architecture and the agent lifecycle docs, I can see the convergence gap. Let me map how they should integrate:

## Current Gap

**Agent Lifecycle (Isolated)**:
- AgentRegistry in unknown location
- Kagent per spoke
- Platform services (Memory, Guardrail, LiteLLM) location unclear
- No Hub-Spoke integration defined

**Hub-Spoke Architecture (Isolated)**:
- Hub has shared services (Keto, Kratos, Hydra, VictoriaMetrics, NATS)
- Spokes have tenant workloads
- No agent lifecycle components mapped

## Convergence Architecture

### Hub Cluster Components

**Control Plane (Hub)**:
```
Hub Cluster
├── AgentRegistry (NEW)
│   └── PostgreSQL (agent configs)
├── AgentGateway (NEW) 
│   └── A2A/MCP routing + RBAC
├── Platform Services (Shared by ALL Spokes)
│   ├── LiteLLM Gateway (multi-tenant)
│   ├── Memory Service (pgvector)
│   ├── Guardrail Engine
│   ├── Context Service
│   └── Outcome Listener (billing)
├── Existing Hub Services
│   ├── Ory Stack (Keto/Kratos/Hydra)
│   ├── VictoriaMetrics + ClickHouse
│   ├── NATS JetStream
│   └── Hub Event Router
```

### Spoke Cluster Components

**Execution Plane (Per Spoke)**:
```
Spoke Silo Cluster (Tenant C)
├── Kagent Controller
│   └── Reconciles Agent CRDs
├── Agent Pods (Tenant workloads)
│   ├── billing-agent
│   ├── support-agent
│   └── analytics-agent
├── KEDA (scale-to-zero)
├── Spoke-local services
│   ├── Tenant routing (in-memory cache)
│   └── Tenant databases
```

## Integration Flow

### Phase 1: Build (Hub-Centric)
```
Tenant → Platform Console (Hub) 
       → POST /v0/agents 
       → AgentRegistry (Hub PostgreSQL)
```

### Phase 2: Deploy (Hub→Spoke)
```
Tenant → Platform Console (Hub)
       → POST /v0/deployments
       → AgentRegistry (Hub)
       → Kubernetes Deployment Adapter
       → Target Spoke Cluster API
       → Kagent Controller (Spoke)
       → Agent Pod provisioned in Spoke
```

**Key**: AgentRegistry in Hub makes cross-cluster API call to Spoke's Kubernetes API server.

### Phase 3: Execute (Spoke→Hub Services)
```
User → AgentGateway (Hub)
     → Routes to Spoke Cluster
     → Kagent A2AHandlerMux (Spoke)
     → Agent Pod (Spoke)
     ├─→ LiteLLM Gateway (Hub) ← Cross-cluster call
     ├─→ Memory Service (Hub) ← Cross-cluster call
     ├─→ Guardrail Engine (Hub) ← Cross-cluster call
     └─→ NATS (Hub) ← Billing event
```

## Network Topology

```
┌─────────────────────────────────────────────────────┐
│ Hub Cluster (Shared Control Plane)                 │
│                                                     │
│ ┌─────────────────┐  ┌──────────────────────────┐ │
│ │ AgentRegistry   │  │ Platform Services        │ │
│ │ (PostgreSQL)    │  │ - LiteLLM (multi-tenant) │ │
│ └─────────────────┘  │ - Memory (pgvector)      │ │
│                      │ - Guardrail Engine       │ │
│ ┌─────────────────┐  │ - Context Service        │ │
│ │ AgentGateway    │  └──────────────────────────┘ │
│ │ (Rust)          │                               │
│ └────────┬────────┘  ┌──────────────────────────┐ │
│          │           │ Ory Stack + NATS         │ │
│          │           └──────────────────────────┘ │
└──────────┼──────────────────────────────────────────┘
           │
           │ HTTPS + mTLS
           │
    ┌──────┴──────┬──────────────┬──────────────┐
    │             │              │              │
┌───▼────┐  ┌────▼───┐  ┌───────▼──┐  ┌───────▼──┐
│Spoke A │  │Spoke B │  │Spoke C   │  │Spoke N   │
│        │  │        │  │          │  │          │
│Kagent  │  │Kagent  │  │Kagent    │  │Kagent    │
│Agents  │  │Agents  │  │Agents    │  │Agents    │
│KEDA    │  │KEDA    │  │KEDA      │  │KEDA      │
└────────┘  └────────┘  └──────────┘  └──────────┘
```

## Service Endpoints (Cross-Cluster)

**Hub Services Exposed to Spokes**:
```yaml
# DNS: hub-services.zero-ops.internal
litellm-gateway.hub.svc.cluster.local:8080
memory-service.hub.svc.cluster.local:8080
guardrail-engine.hub.svc.cluster.local:8080
context-service.hub.svc.cluster.local:8080
nats.hub.svc.cluster.local:4222
```

**Agent Pod Environment (Spoke)**:
```yaml
env:
  - name: LITELLM_ENDPOINT
    value: "https://litellm-gateway.hub.zero-ops.internal"
  - name: MEMORY_SERVICE_ENDPOINT
    value: "https://memory-service.hub.zero-ops.internal"
  - name: GUARDRAIL_ENDPOINT
    value: "https://guardrail-engine.hub.zero-ops.internal"
  - name: NATS_URL
    value: "nats://nats.hub.zero-ops.internal:4222"
```

## Missing Pieces to Implement

1. **Cross-cluster networking** (Cilium Cluster Mesh or VPN)
2. **Hub service discovery** from Spokes
3. **Multi-tenant LiteLLM** configuration
4. **AgentRegistry→Spoke API** authentication (service accounts)
5. **NATS topic isolation** per tenant

This converges your isolated designs into a working Hub-Spoke agent platform.