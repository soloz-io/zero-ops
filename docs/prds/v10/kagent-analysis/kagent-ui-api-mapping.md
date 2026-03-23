---
purpose: Map Kagent Enterprise UI screens to available/missing backend APIs
scope: UI-to-API mapping for custom platform console
topics: REST APIs, K8s CRDs, missing endpoints, dashboard metrics
update_criteria: When analyzing UI replication requirements or API gaps
last_updated: 2026-03-24
validation_status: Verified against kagent codebase
---

# Kagent UI to API Mapping

> **Validation Status:** ✅ Verified against open-source Kagent codebase  
> **Last Updated:** March 24, 2026  
> **Key Corrections Applied:**
> - Fixed API path prefix (removed `/v1/`)
> - Confirmed SSE streaming support (not WebSocket)
> - Verified pagination implementation
> - Clarified token usage tracking architecture

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
   Response: { total: 106, byTime: [...] }

❌ GET /api/metrics/tokens?groupBy=model&timeRange=24h
   Response: { totalByModel: {...}, dailyUsage: [...] }
   
   NOTE: Token data EXISTS in A2A stream (genai.GenerateContentResponseUsageMetadata)
   but is NOT persisted to database. Custom backend must intercept A2A stream
   and aggregate PromptTokenCount/CandidatesTokenCount into metrics table.

❌ GET /api/clusters
   Response: [{ name, version, status, agentCount, toolCount }]
   
   NOTE: Open-source Kagent is single-cluster only. Multi-cluster requires
   custom fleet management layer.

❌ GET /api/metrics/dashboard/summary
   Response: { agentRuns, deployedAgents, models, tools }
```

### Database Support:
- Task table has created_at ✅
- Session table tracks agent_id ✅
- Token usage in A2A stream but NOT persisted ⚠️
- No cluster metadata table ❌

### Token Usage Architecture:
**Source:** `go/adk/pkg/models/openai_adk.go`, `go/adk/pkg/models/anthropic_adk.go`
- ADK maps provider responses to `genai.GenerateContentResponseUsageMetadata`
- Metadata includes `PromptTokenCount` and `CandidatesTokenCount`
- Data flows through A2A stream but is NOT stored in database
- **Custom Solution Required:** Intercept A2A stream, parse metadata, store in metrics table

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
   (Search/filter not implemented - endpoints return flat lists only)

❌ GET /api/policies?targetKind=MCPServer&targetName=resend-mcp
   (AccessPolicy CRD and API completely missing from open-source)
```

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
   Response: [{ id, name, description, systemMessage, suggestedTools }]
   
   NOTE: No template CRD or database schema exists. Must be built entirely
   by custom backend.

❌ GET /api/clusters/{cluster}/namespaces
   (Cluster-specific namespace listing - requires multi-cluster architecture)
```

---

## Screen 4: Agent Chat Interface

### UI Components:
- Chat history with messages
- Tool call display with arguments/results
- Session management
- "What is today's news?" prompt suggestions

### Available APIs:
```
GET /api/sessions - List sessions ✅
POST /api/sessions - Create session ✅
GET /api/sessions/{session_id} - Get session ✅
  Query params: ?limit=50&order=asc&after=2026-03-23T10:00:00Z ✅
DELETE /api/sessions/{session_id} - Delete session ✅
PUT /api/sessions/{session_id} - Update session ✅
POST /api/sessions/{session_id}/events - Add event ✅
GET /api/sessions/{session_id}/tasks - List tasks ✅
POST /api/tasks - Create task ✅
GET /api/tasks/{task_id} - Get task ✅

POST /api/a2a/{namespace}/{name} - A2A streaming endpoint ✅
  Content-Type: application/json
  Accept: text/event-stream
  Returns: SSE stream with JSON-RPC responses
```

### Database Support:
- Session table ✅
- Event table (stores protocol.Message as JSON) ✅
- Task table (stores protocol.Task as JSON) ✅

### Streaming Architecture:
**Implementation:** Server-Sent Events (SSE), NOT WebSocket  
**Client Code:** `ui/src/lib/a2aClient.ts`
```typescript
// UI sends JSON-RPC request to A2A endpoint
POST /api/a2a/{namespace}/{name}
Headers: { Accept: 'text/event-stream' }
Body: { jsonrpc: "2.0", method: "message/stream", params: {...} }

// Backend streams SSE events
data: {"result": {...}}\n\n
data: {"result": {...}}\n\n
data: [DONE]\n\n

// UI processes via async iterator
for await (const event of stream) {
  // Handle streaming response chunks
}
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
   Response: [{ traceId, startTime, duration, status }]

❌ GET /api/traces/{trace_id}
   Response: { spans[], events[], attributes }

❌ GET /api/traces/{trace_id}/replay
   Response: { executionFlow, timings, nodeStates }
```

### Required Backend:
- Query layer over OTLP backend (Jaeger/Tempo) ❌
- Trace aggregation service ❌
- Replay state reconstruction ❌

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
   (Advanced filtering not implemented - endpoints return flat lists only)

❌ GET /api/tools/{tool_id}/usage-stats
   Response: { callCount, lastUsed, avgDuration }
   
   NOTE: Tool usage tracking not implemented. Would require custom
   instrumentation in A2A/MCP handlers.
```

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

### Authorization Architecture:
**Current State:** `go/core/internal/httpserver/auth/authz.go`
```go
type NoopAuthorizer struct{}

func (a *NoopAuthorizer) Check(...) error {
    return nil  // Always allows access
}
```

**Interface:** `go/core/pkg/auth/auth.go`
```go
type Authorizer interface {
    Check(ctx context.Context, principal Principal, verb Verb, resource Resource) error
}
```

**To Implement Policies:**
1. Create AccessPolicy CRD (subjects, targets, actions)
2. Implement `PolicyAuthorizer` that reads CRDs
3. Inject into controller instead of `NoopAuthorizer`
4. Enforce in A2A/MCP request handlers
5. Build REST API for policy CRUD

---

## Summary: API Coverage

### ✅ Fully Available (70%):
- Agent CRUD
- Session/Chat management
- Task execution
- Tool/ToolServer management
- Model configuration
- Memory (vector search)
- Feedback
- LangGraph checkpoints
- CrewAI integration

### ⚠️ Partially Available (15%):
- Namespace listing (exists but not cluster-aware)
- Tool discovery (exists but no search/filter)
- Token usage (exists in A2A stream but not persisted)

### ❌ Missing (15%):
- Dashboard metrics aggregation (need to intercept A2A stream for tokens)
- Cluster management APIs (open-source is single-cluster only)
- AccessPolicy CRUD (CRD, controller, enforcement, REST API)
- Tracing query/replay APIs (need OTEL backend integration)
- Token usage persistence (data exists in stream, need aggregation layer)
- Agent templates (no CRD or database schema)
- Advanced search/filtering (endpoints return flat lists only)
- Tool usage statistics (no instrumentation)

---

## Implementation Priority for Custom UI:

### Phase 1 (MVP - Use existing APIs):
1. Agent management screens (CRUD via `/api/agents`)
2. Chat interface (SSE streaming via `/api/a2a/{namespace}/{name}`)
3. Tool/ToolServer management (CRUD via `/api/tools`, `/api/toolservers`)
4. Model configuration (CRUD via `/api/modelconfigs`)
5. Session management with pagination (`/api/sessions?limit=50&order=asc&after=...`)

**Key Corrections:**
- ✅ Use SSE streaming (NOT polling, NOT WebSocket)
- ✅ Pagination already exists (use query params)
- ✅ All API paths use `/api/` prefix (NOT `/api/v1/`)

### Phase 2 (Add aggregation/metrics):
1. **Token Usage Aggregation Service**
   - Intercept A2A stream responses
   - Parse `genai.GenerateContentResponseUsageMetadata`
   - Store `PromptTokenCount`/`CandidatesTokenCount` in metrics table
   - Build `/api/metrics/tokens` endpoint for dashboard

2. **Dashboard Metrics Service**
   - Aggregate task counts by time range
   - Count deployed agents, models, tools
   - Build `/api/metrics/dashboard/summary` endpoint

3. **Advanced Filtering**
   - Add search/filter layer over flat list endpoints
   - Implement `/api/tools?search=...&category=...`
   - Implement `/api/toolservers?search=...&cluster=...`

4. **Cluster Management** (if multi-cluster needed)
   - Design fleet management architecture
   - Build `/api/clusters` endpoint
   - Federate queries to multiple Kagent instances

### Phase 3 (Advanced features):
1. **AccessPolicy System**
   - Design AccessPolicy CRD schema
   - Implement `PolicyAuthorizer` (replace `NoopAuthorizer`)
   - Build policy controller
   - Add enforcement to A2A/MCP handlers
   - Build REST API (`/api/policies`)

2. **Tracing Integration**
   - Deploy OTEL backend (Jaeger/Tempo)
   - Build query layer over OTEL API
   - Implement `/api/traces` endpoints
   - Build replay/visualization service

3. **Agent Templates**
   - Design template schema (CRD or database)
   - Build template CRUD API
   - Integrate with agent creation flow

4. **Tool Usage Statistics**
   - Instrument A2A/MCP handlers
   - Track tool invocations, latency, errors
   - Build `/api/tools/{id}/usage-stats` endpoint

---

## Critical Architecture Notes

### Token Usage Flow:
```
LLM Provider → ADK (openai_adk.go/anthropic_adk.go)
  ↓ Maps to genai.GenerateContentResponseUsageMetadata
A2A Stream (JSON-RPC response)
  ↓ Contains PromptTokenCount, CandidatesTokenCount
Custom Interceptor (YOUR CODE)
  ↓ Parse metadata from stream
Metrics Database Table
  ↓ Store aggregated data
Dashboard API (/api/metrics/tokens)
```

### Multi-Cluster Architecture:
Open-source Kagent is **single-cluster only**. Enterprise multi-cluster requires:
- Centralized management plane
- Fleet controller to orchestrate multiple Kagent instances
- Federated API gateway
- Cluster registry and health monitoring

### Authorization Enforcement:
`NoopAuthorizer` is pluggable via `auth.Authorizer` interface. To enforce policies:
1. Implement `PolicyAuthorizer` that reads AccessPolicy CRDs
2. Inject into HTTP server config
3. Add authorization checks in handlers before K8s API calls
4. Enforce in A2A proxy before forwarding to agents
