# Execution Flow - Source Files for Analysis

## Tracing Infrastructure
- `archived/agentic-ai/solo/kagent/go/core/internal/telemetry/tracing.go` - OTLP setup
- `archived/agentic-ai/solo/kagent/go/core/internal/a2a/trace.go` - A2A span creation
- `archived/agentic-ai/solo/kagent/go/adk/pkg/telemetry/tracing.go` - Span attributes

## Database Schema
- `archived/agentic-ai/solo/kagent/go/api/database/models.go` - Event/Task/Session models
- Event.Data contains JSON: protocol.Message with tool calls
- LangGraphCheckpoint.ParentCheckpointID for flow hierarchy

## A2A Protocol (External Dependency)
- Uses `trpc.group/trpc-go/trpc-a2a-go/protocol` package
- protocol.Message, protocol.Task structures (not in codebase)

## Implementation Strategy

### Build Execution Flow DAG:

**From OTLP Traces:**
1. Query Jaeger/Tempo for trace_id
2. Parse span hierarchy (parent_span_id relationships)
3. Extract span attributes: operation_name, agent_name, tool_name
4. Build visual DAG from span tree

**From Database Events:**
1. Query: `SELECT * FROM event WHERE session_id = ? ORDER BY created_at`
2. Parse event.Data JSON to extract:
   - Tool invocations
   - Agent responses
   - Subagent calls (A2A protocol)
3. Build graph from event sequence

### Missing: Visual DAG Logic
- No UI component exists in `ui/src/` for trace visualization
- Must build custom React Flow / D3.js component
- Query layer over OTLP backend required

## Recommendation
Use OTLP traces (Option 1) - cleaner hierarchy, standard tooling (Jaeger UI as reference)
