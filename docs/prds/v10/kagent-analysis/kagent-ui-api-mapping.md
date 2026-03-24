---
purpose: Map Kagent Enterprise UI screens to available/missing backend APIs
scope: UI-to-API mapping for custom platform console
topics: REST APIs, K8s CRDs, missing endpoints, dashboard metrics, capability mapping
update_criteria: When analyzing UI replication requirements or API gaps
last_updated: 2026-03-24
validation_status: Verified against kagent codebase (go, ui, helm, python, docker, scripts, reports)
---

# Kagent UI to API Mapping

> **Validation Status:** ✅ Verified against open-source Kagent codebase  
> **Last Updated:** March 24, 2026  
> **Folders Verified:** go/, ui/, helm/, python/, docker/, scripts/, reports/  
> **Key Corrections Applied:**
> - Fixed API path prefix (removed `/v1/`)
> - Confirmed SSE streaming support (not WebSocket)
> - Verified pagination implementation
> - Clarified token usage: Data persisted in event.Data as JSON, NOT indexed for aggregation
> - Confirmed AccessPolicy CRD does NOT exist in open-source
> - Verified no dashboard/tracing UI components exist

---

## Capability Matrix

### ✅ Fully Available in Open-Source

| Capability | Status | Implementation | Reference |
|-----------|--------|----------------|-----------|
| **ADK (Go & Python)** | ✅ | Agent runtime frameworks | `go/adk/pkg/agent/agent.go`, `python/packages/kagent-adk/` |
| **CrewAI Integration** | ✅ | State management APIs | `go/core/internal/httpserver/handlers/crewai.go` |
| **LangGraph Integration** | ✅ | Checkpoint APIs | `go/core/internal/httpserver/handlers/checkpoints.go` |
| **BYO Agents** | ✅ | Custom container support | `go/api/v1alpha2/agent_types.go` (BYOAgentSpec) |
| **MCP Tool Servers** | ✅ | Remote MCP integration | `go/api/v1alpha2/remotemcpserver_types.go` |
| **Skills (Git/OCI)** | ✅ | Skill loading from repos | `go/api/v1alpha2/agent_types.go` (SkillForAgent) |
| **Session Management** | ✅ | Full CRUD + pagination | `go/core/internal/httpserver/handlers/sessions.go` |
| **Human-in-the-Loop** | ✅ | HITL approval flow | `go/adk/pkg/a2a/hitl.go` |
| **SSE Streaming** | ✅ | Real-time A2A streaming | `go/adk/pkg/a2a/eventqueue.go` |
| **Vector Memory** | ✅ | pgvector integration | `go/adk/pkg/memory/kagent_service.go` |
| **Context Compression** | ✅ | Event compaction | `go/api/v1alpha2/agent_types.go` (ContextCompressionConfig) |
| **OIDC/OAuth** | ✅ | Authentication middleware | `go/core/internal/httpserver/auth/authn.go` |
| **OpenTelemetry** | ✅ | OTLP span export | `go/core/internal/telemetry/tracing.go` |
| **Agent Mesh (A2A)** | ✅ | Agent-to-agent calls | `go/core/internal/a2a/a2a_handler_mux.go` |
| **Agent HA** | ✅ | K8s deployment HA | `go/core/internal/controller/translator/agent/deployments.go` |

### ⚠️ Partially Available (Needs Extension)

| Capability | Status | What Exists | What's Missing |
|-----------|--------|-------------|----------------|
| **Token Usage Tracking** | ⚠️ | Emitted in A2A stream (`go/adk/pkg/models/openai_adk.go:267-273`) | Aggregation API, persistence layer |
| **Metrics** | ⚠️ | Basic Prometheus metrics | Dashboard aggregation endpoints |
| **Tracing** | ⚠️ | OTLP export to backend | Query API, visual DAG, replay |

### ❌ Missing in Open-Source

| Capability | Status | Enterprise Feature | Implementation Required |
|-----------|--------|-------------------|------------------------|
| **Evaluation Engine** | ❌ | No native eval APIs | Custom eval framework |
| **Access Policies (RBAC)** | ❌ | Uses NoopAuthorizer | AccessPolicy CRD + PolicyAuthorizer |
| **mTLS / Token Exchange** | ❌ | Relies on service mesh | Native STS integration |
| **Multi-Cluster** | ❌ | Single-cluster only | Fleet management layer |
| **Dashboard Aggregation** | ❌ | No summary endpoints | Metrics aggregation service |

---

## Screen 1: Dashboard Overview

### UI Components:
- Total Agent Runs (106)
- Deployed Agents (17)
- Models (3)
- Tools (237)
- Total Tokens per Model (bar chart)
- Daily Token Usage per Model (area chart)
- Daily Agent Runs (line chart)
- Connected Clusters (3 clusters with health status)
- Deployed Agents (grid with cards)

### Available APIs:
```
GET /api/agents - List all agents ✅
GET /api/tools - List all tools ✅
GET /api/modelconfigs - List model configs ✅
GET /api/namespaces - List namespaces (proxy for clusters) ✅
```

### Missing APIs:
```
❌ GET /api/metrics/agent-runs?timeRange=24h
❌ GET /api/metrics/tokens?groupBy=model&timeRange=24h
❌ GET /api/clusters
❌ GET /api/metrics/dashboard/summary
```

### Database Support:
- Task table has created_at ✅
- Session table tracks agent_id ✅
- Event table stores event.Data as JSON string (includes UsageMetadata) ✅
- Token usage NOT indexed for aggregation queries ❌
- No cluster metadata table ❌
- No metrics aggregation tables ❌

**Verification Notes:**
- UI folder confirms: TokenStats component exists (`ui/src/components/chat/TokenStats.tsx`)
- No Dashboard component found in UI codebase
- Helm CRDs verified: Only Agent, ModelConfig, RemoteMCPServer, ToolServer, Memory exist
- Python packages: ADK only, no metrics/dashboard services
- Docker/scripts: Build tooling only, no backend services

*(See Critical Architecture Note 1 for token usage implementation)*

---

## Screen 2: Tool Servers

### UI Components:
- Search tool servers
- Filter by Tool Server, Category, Cluster
- Table: Tool Name, Category, Policy, Cluster
- Tool discovery status
- MCP server connection info

### Available APIs:
```
GET /api/toolservers - List all tool servers ✅
GET /api/toolservers/{namespace}/{name} - Get tool server ✅
POST /api/toolservers - Create tool server ✅
DELETE /api/toolservers/{namespace}/{name} - Delete tool server ✅
GET /api/toolservertypes - List tool server types ✅
```

### Available CRDs:
```
RemoteMCPServer CRD ✅
  - spec.url, spec.protocol, spec.headersFrom
  - status.discoveredTools[]
  - status.conditions (Accepted)
```

### Missing APIs:
```
❌ GET /api/toolservers?search=fetch&category=Argo&cluster=cluster-1
❌ GET /api/policies?targetKind=MCPServer&targetName=resend-mcp
```

*(Advanced filtering not implemented. AccessPolicy CRD missing - see Critical Architecture Note 3)*

### Database Support:
- ToolServer table ✅
- Tool table with server_name, group_kind ✅
- No policy table ❌

---

## Screen 3: Create/Update Agent

### UI Components:
- Basic Information (name, cluster, namespace, description)
- Behavior & Model Settings (model selection, instructions)
- Tools and Agents (multi-select with search)
- Agent templates (Research, DevOps, Troubleshooter)

### Available APIs:
```
POST /api/agents - Create agent ✅
PUT /api/agents - Update agent ✅
GET /api/agents/{namespace}/{name} - Get agent ✅
DELETE /api/agents/{namespace}/{name} - Delete agent ✅
GET /api/modelconfigs - List models ✅
GET /api/toolservers - List tool servers ✅
```

### Available CRDs:
```
Agent CRD ✅
  - spec.type (Declarative/BYO)
  - spec.declarative.systemMessage
  - spec.declarative.modelConfig
  - spec.declarative.tools[]
  - spec.declarative.a2aConfig
  - status.conditions (Ready, Accepted)
```

### Missing APIs:
```
❌ GET /api/agent-templates
   
❌ GET /api/clusters/{cluster}/namespaces
```

*(No template CRD or database schema exists. Multi-cluster requires custom fleet layer - see Critical Architecture Note 2)*

---

## Screen 4: Agent Chat Interface

### UI Components:
- Chat history with messages
- Tool call display with arguments/results
- Session management
- "What is today's news?" prompt suggestions

### Available APIs (UI-Facing):
```
GET /api/sessions - List sessions ✅
POST /api/sessions - Create session ✅
GET /api/sessions/{session_id} - Get session with events ✅
  Query params: ?limit=50&order=asc&after=2026-03-23T10:00:00Z ✅
DELETE /api/sessions/{session_id} - Delete session ✅
PUT /api/sessions/{session_id} - Update session ✅
GET /api/sessions/{session_id}/tasks - List tasks ✅

POST /api/a2a/{namespace}/{name} - A2A streaming endpoint ✅
  Content-Type: application/json
  Accept: text/event-stream
  Returns: SSE stream with JSON-RPC responses
```

### Available APIs (Agent-Facing):
```
POST /api/sessions/{session_id}/events - Add event (called by Agent Pod) ✅
POST /api/tasks - Create task (called by Agent Pod) ✅
GET /api/tasks/{task_id} - Get task ✅
```

### Database Support:
- Session table ✅
- Event table (stores protocol.Message as JSON, includes UsageMetadata) ✅
- Task table (stores protocol.Task as JSON) ✅

### Streaming Architecture:
**Implementation:** Server-Sent Events (SSE), NOT WebSocket  
**Client Code:** `ui/src/lib/a2aClient.ts`

**UI Flow:**
```typescript
// 1. UI sends JSON-RPC request to A2A endpoint
POST /api/a2a/{namespace}/{name}
Headers: { Accept: 'text/event-stream' }
Body: { jsonrpc: "2.0", method: "message/stream", params: {...} }

// 2. Backend streams SSE events
data: {"result": {..., "usageMetadata": {...}}}\n\n
data: {"result": {...}}\n\n
data: [DONE]\n\n

// 3. UI processes via async iterator
for await (const event of stream) {
  // Handle streaming response chunks
}
```

**Agent State Persistence Flow:**
```
Agent Pod (ADK) → POST /api/sessions/{session_id}/events
                → Stores event.Data as JSON string
                → Includes UsageMetadata in payload
```

### Missing APIs:
```
✅ CORRECTION: SSE streaming ALREADY EXISTS via A2A protocol
   Do NOT build WebSocket or polling-based implementation
   
✅ CORRECTION: Pagination ALREADY EXISTS
   GET /api/sessions/{session_id}?limit=50&order=asc&after=RFC3339_timestamp
   Implemented in: go/core/internal/httpserver/handlers/sessions.go:165-185
```

---

## Screen 5: Tracing View

### UI Components:
- Execution Flow Graph (visual DAG)
- Trace Tree (hierarchical timing)
- Agent Trace Details (input/output, metadata)
- Replay functionality
- Time range filter

### Available APIs:
```
❌ NONE - No tracing query APIs exist
```

### Available Infrastructure:
```
OpenTelemetry tracing configured ✅
  - internal/telemetry/tracing.go
  - Exports to OTLP endpoint
```

### Missing APIs:
```
❌ GET /api/traces?agentName=newsheadline&timeRange=24h
❌ GET /api/traces/{trace_id}
❌ GET /api/traces/{trace_id}/replay
```

*(See Critical Architecture Note 4 for implementation details)*

### Required Backend:
- Query layer over OTLP backend (Jaeger/Tempo) ❌
- Trace aggregation service ❌
- Replay state reconstruction ❌

*(See Critical Architecture Note 4)*

---

## Screen 6: Tools Page (Global View)

### UI Components:
- Search tools
- Filter by Tool Server, Category, Cluster
- Table with tool details
- Policy indicators

### Available APIs:
```
GET /api/tools - List all tools ✅
GET /api/toolservers - List tool servers ✅
```

### Missing APIs:
```
❌ GET /api/tools?search=incident&category=Cilium&cluster=mgmt-cluster
❌ GET /api/tools/{tool_id}/usage-stats
```

*(Advanced filtering and usage tracking not implemented)*

---

## Screen 7: Access Policies

### UI Components:
- Policy YAML editor
- Policy list/CRUD
- Subject/target selection
- Action configuration (ALLOW/DENY)

### Available CRDs:
```
❌ AccessPolicy CRD not found in v1alpha2
   (Mentioned in video but CRD not in codebase)
```

### Missing APIs:
```
❌ GET /api/policies
❌ POST /api/policies
❌ GET /api/policies/{namespace}/{name}
❌ PUT /api/policies/{namespace}/{name}
❌ DELETE /api/policies/{namespace}/{name}
```

### Required Implementation:
- AccessPolicy CRD definition ❌
- Policy controller ❌
- Policy enforcement in A2A/MCP handlers ❌
- REST API layer ❌

*(See Critical Architecture Note 3 for implementation details)*

---

## Product Team Implementation Roadmap

### Epic 1: Core UI (Use Existing APIs)
**Effort:** 2-3 sprints | **Risk:** Low

Build UI screens using available Kagent REST APIs:
- Agent CRUD, Chat (SSE), Tool/ToolServer management, Model configs, Session history

**Key Implementation Notes:**
- Use `/api/` prefix (NOT `/api/v1/`)
- SSE streaming via `POST /api/a2a/{namespace}/{name}` (NOT WebSocket)
- Pagination: `?limit=50&order=asc&after=RFC3339_timestamp`

### Epic 2: Metrics Aggregation Service
**Effort:** 1-2 sprints | **Risk:** Medium

Build custom service to power dashboard charts:
- Intercept A2A stream, parse UsageMetadata from event.Data JSON
- Store in time-series metrics table (agent_id, model, timestamp, tokens)
- Build `/api/metrics/*` endpoints for dashboard queries

**Why Needed:** Token data exists in event.Data but NOT indexed for aggregation

### Epic 3: AccessPolicy System
**Effort:** 2-3 sprints | **Risk:** High

Build RBAC enforcement (currently NoopAuthorizer):
- Design AccessPolicy CRD, implement PolicyAuthorizer, build controller
- Enforce in A2A/MCP handlers, build REST API

**Why Needed:** CRD does NOT exist in open-source codebase

### Epic 4: Tracing Query Layer
**Effort:** 2 sprints | **Risk:** Medium

Build query service over OTLP backend:
- Deploy Jaeger/Tempo, build query API, implement visual DAG/replay

**Why Needed:** OTLP export exists, but no query/visualization APIs

### Epic 5: Multi-Cluster Federation (Optional)
**Effort:** 3-4 sprints | **Risk:** High

Build fleet management layer:
- Cluster registry, federated API gateway, health monitoring

**Why Needed:** Open-source is single-cluster only

---

## Critical Architecture Notes

### 1. Token Usage Flow
```
┌─────────────────┐
│  LLM Provider   │ (OpenAI, Anthropic, etc.)
└────────┬────────┘
         │ Returns usage: {prompt_tokens, completion_tokens}
         ↓
┌─────────────────────────────────────────────────────────┐
│  ADK Layer (go/adk/pkg/models/openai_adk.go:267-273)   │
│  Maps to: genai.GenerateContentResponseUsageMetadata   │
│  - PromptTokenCount                                     │
│  - CandidatesTokenCount                                 │
│  - TotalTokenCount                                      │
└────────┬────────────────────────────────────────────────┘
         │ Included in JSON-RPC response
         ↓
┌─────────────────────────────────────────────────────────┐
│  A2A Stream (SSE)                                       │
│  data: {"result": {..., "usageMetadata": {...}}}       │
└────────┬────────────────────────────────────────────────┘
         │ Real-time to UI + Persisted to DB
         ↓
┌─────────────────────────────────────────────────────────┐
│  ✅ PERSISTED as JSON in event.Data column              │
│  Agent Pod → POST /api/sessions/{id}/events             │
│  Stores: event.Data = JSON string with UsageMetadata    │
│  ❌ NOT INDEXED - Cannot query SUM(tokens) efficiently  │
└────────┬────────────────────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────────────────────────────┐
│  ⚠️ Custom Metrics Aggregator Required                  │
│  YOUR CODE: Parse event.Data JSON → Extract tokens      │
│             → Store in dedicated metrics table          │
└────────┬────────────────────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────────────────────────────┐
│  Metrics Database Table (Custom)                        │
│  Schema: agent_id, model, timestamp, prompt_tokens,     │
│          completion_tokens, total_tokens                │
└────────┬────────────────────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────────────────────────────┐
│  Dashboard API (Custom)                                 │
│  GET /api/metrics/tokens?groupBy=model&timeRange=24h    │
└─────────────────────────────────────────────────────────┘
```

**Clarification:** Token data IS persisted in the event table's Data column as a JSON string, but querying thousands of JSON blobs for aggregation will crash your database. Build a dedicated metrics table.

### 2. Multi-Cluster Architecture
**Current State:** Open-source Kagent is **single-cluster only**

**Enterprise Multi-Cluster Requirements:**
```
┌──────────────────────────────────────────────────────┐
│  Management Plane (Custom)                           │
│  - Cluster registry                                  │
│  - Health monitoring                                 │
│  - Federated API gateway                             │
└────────┬─────────────────────────────────────────────┘
         │
         ├─────────────┬─────────────┬─────────────┐
         ↓             ↓             ↓             ↓
    ┌────────┐   ┌────────┐   ┌────────┐   ┌────────┐
    │Cluster1│   │Cluster2│   │Cluster3│   │ClusterN│
    │ Kagent │   │ Kagent │   │ Kagent │   │ Kagent │
    └────────┘   └────────┘   └────────┘   └────────┘
```

**Implementation Approach:**
1. Deploy Kagent controller per cluster
2. Build centralized management API
3. Federate queries across clusters
4. Aggregate metrics/status
5. Route agent requests to target cluster

### 3. Authorization Enforcement
**Current State:** `NoopAuthorizer` (always allows)

```go
// go/core/internal/httpserver/auth/authz.go
type NoopAuthorizer struct{}

func (a *NoopAuthorizer) Check(...) error {
    return nil  // ← Always allows
}
```

**Interface:** `go/core/pkg/auth/auth.go`
```go
type Authorizer interface {
    Check(ctx context.Context, principal Principal, verb Verb, resource Resource) error
}
```

**To Implement AccessPolicy:**
```
┌─────────────────────────────────────────────────────┐
│  1. Create AccessPolicy CRD                         │
│     apiVersion: policy.kagent.io/v1alpha1           │
│     kind: AccessPolicy                              │
│     spec:                                           │
│       subjects: [agents, users]                     │
│       targets: [tools, agents]                      │
│       actions: [ALLOW, DENY]                        │
└────────┬────────────────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────────────────────────┐
│  2. Implement PolicyAuthorizer                      │
│     - Reads AccessPolicy CRDs from K8s              │
│     - Evaluates rules against principal/resource    │
│     - Returns error if denied                       │
└────────┬────────────────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────────────────────────┐
│  3. Inject into HTTP Server                         │
│     Replace NoopAuthorizer with PolicyAuthorizer    │
│     in ServerConfig                                 │
└────────┬────────────────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────────────────────────┐
│  4. Enforce in Handlers                             │
│     - Check before K8s API calls                    │
│     - Check in A2A proxy before forwarding          │
│     - Check in MCP handler before tool execution    │
└─────────────────────────────────────────────────────┘
```

### 4. Tracing Architecture
**Current State:** Exports OTLP spans, no query API

```
┌─────────────────┐
│  Kagent Agent   │
│  Execution      │
└────────┬────────┘
         │ Emits OTLP spans
         ↓
┌─────────────────────────────────────────────────────┐
│  OpenTelemetry Collector                            │
│  (go/core/internal/telemetry/tracing.go)            │
└────────┬────────────────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────────────────────────┐
│  OTLP Backend (Jaeger / Tempo)                      │
│  Stores spans, builds trace trees                   │
└────────┬────────────────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────────────────────────┐
│  ❌ Custom Query Layer Required                      │
│  - Query Jaeger/Tempo API                           │
│  - Build execution flow DAG                         │
│  - Format for UI visualization                      │
│  - Implement replay logic                           │
└────────┬────────────────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────────────────────────┐
│  Tracing UI Endpoints (Custom)                      │
│  GET /api/traces?agent=...&timeRange=...            │
│  GET /api/traces/{trace_id}                         │
│  GET /api/traces/{trace_id}/replay                  │
└─────────────────────────────────────────────────────┘
```

### 5. Evaluation Engine
**Status:** ❌ Not implemented in open-source

**Required Components:**
- Evaluation framework (accuracy, latency, cost metrics)
- Test dataset management
- Baseline comparison
- A/B testing infrastructure
- Evaluation result storage
- Reporting API

**Reference Implementations:**
- LangSmith evaluation
- Weights & Biases for LLMs
- Custom eval harness

