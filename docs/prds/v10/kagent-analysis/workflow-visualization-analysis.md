# Workflow Visualization Analysis - Kagent Codebase

## Question
Can agent-to-agent composition be visualized as a workflow graph in UI before execution?

## Findings from Codebase

### 1. Agent Composition Storage

#### In Agent CRD Spec (Declarative):
```yaml
spec:
  declarative:
    tools:
      - type: Agent
        agent:
          name: salesforce-agent
          namespace: default
      - type: Agent  
        agent:
          name: hr-agent
      - type: McpServer
        mcpServer:
          name: slack-mcp
          toolNames: [send_message]
```
**Source:** `go/api/v1alpha2/agent_types.go:163-171, 395-416`

#### In Agent CRD Status:
```go
type AgentStatus struct {
    ObservedGeneration int64              `json:"observedGeneration"`
    Conditions         []metav1.Condition `json:"conditions,omitempty"`
}
```
**Source:** `go/api/v1alpha2/agent_types.go:486-489`

**Finding:** Status does NOT store resolved tool graph or agent references.

### 2. Runtime Configuration

#### Config Translation:
Agent CRD → ConfigMap with `config.json` + `agent-card.json`

**config.json structure:**
```json
{
  "name": "employee-onboarding",
  "system_message": "...",
  "model": {...},
  "tools": [
    {
      "type": "a2a",
      "url": "http://kagent-controller:8083/api/a2a/default/salesforce-agent",
      "name": "salesforce-agent"
    },
    {
      "type": "a2a", 
      "url": "http://kagent-controller:8083/api/a2a/default/hr-agent",
      "name": "hr-agent"
    },
    {
      "type": "mcp",
      "url": "http://slack-mcp:8080",
      "name": "slack-mcp"
    }
  ]
}
```
**Source:** `go/core/internal/controller/translator/agent/config.go`, `go/core/internal/controller/translator/agent/tools.go`

**Finding:** Tools array contains flat list with URLs. No hierarchical graph structure.

#### agent-card.json structure:
```json
{
  "name": "employee-onboarding",
  "description": "...",
  "skills": [
    {
      "id": "onboard-employee",
      "name": "Onboard Employee",
      "description": "..."
    }
  ]
}
```
**Source:** `go/adk/pkg/a2a/agentcard.go`, BYO example at `go/adk/examples/byo/main.go:138-144`

**Finding:** AgentCard.Skills describes WHAT the agent can do, not HOW (no workflow graph).

### 3. Programmatic Composition (BYO Agents)

#### ParallelAgent Example:
```go
parallelAgent, err := parallelagent.New(parallelagent.Config{
    AgentConfig: adkagent.Config{
        Name:        "parallel_writer",
        Description: "Runs creative and technical writers in parallel",
        SubAgents:   []adkagent.Agent{creativeWriter, technicalWriter},
    },
})
```
**Source:** `go/adk/examples/byo/main.go:99-106`

**Finding:** SubAgents array exists in Google ADK's `adkagent.Config`, but:
1. NOT exposed in Kagent Agent CRD
2. NOT stored in config.json
3. Only available in programmatic BYO agents

### 4. REST API Exposure

#### GET /api/agents/{namespace}/{name}:
```go
func (h *AgentsHandler) HandleGetAgent(w ErrorResponseWriter, r *http.Request) {
    // Returns K8s Agent CRD directly
    agent := &v1alpha2.Agent{}
    err := h.client.Get(ctx, types.NamespacedName{...}, agent)
    // Returns: agent.Spec, agent.Status
}
```
**Source:** `go/core/internal/httpserver/handlers/agents.go:82-115`

**Response structure:**
```json
{
  "metadata": {...},
  "spec": {
    "type": "Declarative",
    "declarative": {
      "tools": [
        {"type": "Agent", "agent": {"name": "salesforce-agent"}},
        {"type": "Agent", "agent": {"name": "hr-agent"}}
      ]
    }
  },
  "status": {
    "observedGeneration": 1,
    "conditions": [...]
  }
}
```

**Finding:** API returns spec.declarative.tools array. This CAN be used to build a graph.

### 5. UI Implementation

#### Current UI:
- **Location:** `ui/src/app/agents/page.tsx`
- **Displays:** Agent list with name, type, status
- **Does NOT display:** Tool graph, agent composition, workflow visualization

#### Dependencies Check:
```json
// ui/package.json
{
  "dependencies": {
    "react": "^19.0.0",
    "next": "^15.1.6",
    // NO graph libraries: no react-flow, no d3, no cytoscape
  }
}
```
**Source:** `ui/package.json`

**Finding:** No graph visualization library installed. No workflow UI exists.

### 6. A2A Protocol Discovery

#### .well-known/agent.json endpoint:
```go
mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(&agentCard))
// Serves AgentCard at: http://agent-pod:8080/.well-known/agent.json
```
**Source:** `go/adk/pkg/a2a/server/server.go:39`

**Response:**
```json
{
  "name": "parallel_writer",
  "skills": [
    {
      "id": "parallel-write",
      "name": "Parallel Write",
      "description": "Writes from both creative and technical perspectives"
    }
  ]
}
```

**Finding:** AgentCard exposes skills (capabilities), NOT internal composition (SubAgents).

## CONCLUSION

### ✅ What EXISTS:
1. **Agent CRD stores tool references** in `spec.declarative.tools[]`
2. **REST API exposes this data** via `GET /api/agents/{namespace}/{name}`
3. **Tools array includes agent-to-agent references** with type=Agent
4. **Data structure is queryable** and can be traversed

### ❌ What's MISSING:
1. **No workflow graph in CRD status** - Status only has conditions, not resolved graph
2. **No hierarchical structure** - Tools are flat array, not tree/DAG
3. **No UI visualization** - No graph library, no workflow component
4. **No execution order** - Tools array doesn't specify sequence/parallel/conditional
5. **SubAgents not exposed** - Google ADK's SubAgents field not in Kagent CRD

### ⚠️ Critical Gap:
**Execution flow is determined by LLM at runtime, not predefined in spec.**

The Agent CRD defines:
- WHICH agents can be called (tools array)
- NOT WHEN or HOW they are orchestrated

Example:
```yaml
tools:
  - type: Agent
    agent: {name: salesforce-agent}
  - type: Agent
    agent: {name: hr-agent}
```

At runtime, the LLM decides:
- Call salesforce-agent first, then hr-agent? 
- Call both in parallel?
- Call only one based on user input?

**This is NOT a workflow engine. It's tool availability declaration.**

## Answer to Original Question

### Can you build a visual workflow preview?

**YES, but with limitations:**

#### What you CAN visualize:
1. **Available agents** (from spec.declarative.tools where type=Agent)
2. **Available MCP tools** (from spec.declarative.tools where type=McpServer)
3. **Static dependency graph** (Agent A can call Agent B, C, D)

#### What you CANNOT visualize:
1. **Execution order** (not defined in spec)
2. **Conditional logic** (LLM decides at runtime)
3. **Parallel vs sequential** (not specified in declarative agents)
4. **Loop/retry patterns** (not in CRD)

#### Implementation Approach:
```typescript
// Fetch agent spec
const agent = await fetch(`/api/agents/${namespace}/${name}`);

// Extract agent tools
const agentTools = agent.spec.declarative.tools
  .filter(t => t.type === 'Agent')
  .map(t => ({
    name: t.agent.name,
    namespace: t.agent.namespace || agent.metadata.namespace
  }));

// Build graph nodes
const nodes = [
  { id: agent.metadata.name, type: 'root' },
  ...agentTools.map(t => ({ id: t.name, type: 'agent' }))
];

// Build edges (root can call all tools)
const edges = agentTools.map(t => ({
  source: agent.metadata.name,
  target: t.name,
  label: 'can call'
}));

// Render with React Flow or D3
```

### For Programmatic Composition (BYO):
**NOT POSSIBLE** - SubAgents are in-memory Go/Python objects, not exposed via API.

## Recommendation

To build workflow visualization:
1. **Use spec.declarative.tools** to show "available agents"
2. **Label it as "Capability Graph"** not "Execution Flow"
3. **Add disclaimer:** "Actual execution order determined by LLM at runtime"
4. **For true workflow:** Build custom orchestration layer (like LangGraph) with explicit graph definition
