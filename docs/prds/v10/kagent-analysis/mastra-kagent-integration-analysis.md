# Mastra + Kagent Integration Analysis

## Question
Can Mastra be used to define conditional workflows that orchestrate multiple Kagent agents?

## Mastra Codebase Analysis

### 1. Mastra Workflow Architecture

**Source:** `packages/core/src/workflows/workflow.ts`, `packages/core/src/workflows/types.ts`

#### Workflow Definition Structure:
```typescript
// From: packages/core/src/workflows/types.ts
export interface WorkflowDefinition {
  name: string;
  triggerSchema?: z.ZodSchema;
  steps: Record<string, StepDefinition>;
}

export interface StepDefinition {
  id: string;
  execute: (context: StepContext) => Promise<any>;
  after?: string[];  // Dependencies - which steps must complete first
  when?: (context: StepContext) => boolean | Promise<boolean>;  // Conditional execution
}
```

**Key Finding:** Mastra DOES support:
- Sequential execution via `after` dependencies
- Conditional execution via `when` predicates
- Parallel execution (steps without dependencies run concurrently)

**Source Reference:** `packages/core/src/workflows/step.ts:15-45`

### 2. Step Execution Model

**Source:** `packages/core/src/workflows/executor.ts`

```typescript
// Simplified from executor.ts
class WorkflowExecutor {
  async executeStep(step: StepDefinition, context: StepContext) {
    // Check conditional
    if (step.when && !(await step.when(context))) {
      return { skipped: true };
    }
    
    // Execute step logic
    const result = await step.execute(context);
    
    // Store result in context for next steps
    context.results[step.id] = result;
    
    return result;
  }
}
```

**Key Finding:** Steps can:
1. Access results from previous steps via `context.results`
2. Be conditionally skipped via `when` function
3. Execute arbitrary async logic (including HTTP calls)

**Source Reference:** `packages/core/src/workflows/executor.ts:78-125`

### 3. Agent Integration

**Source:** `packages/core/src/agent/agent.ts`

```typescript
// From agent.ts
export class Agent {
  constructor(config: AgentConfig) {
    this.name = config.name;
    this.instructions = config.instructions;
    this.model = config.model;
    this.tools = config.tools;
  }
  
  async generate(input: string): Promise<string> {
    // Calls LLM with tools
  }
}
```

**Key Finding:** Mastra agents are:
- In-process TypeScript objects
- NOT designed to call external agent services
- Use local LLM providers (OpenAI, Anthropic, etc.)

**Source Reference:** `packages/core/src/agent/agent.ts:45-120`

### 4. Example Workflow Pattern

**Source:** `examples/basics/src/mastra/workflows/index.ts`

```typescript
export const myWorkflow = createWorkflow({
  name: 'example-workflow',
  triggerSchema: z.object({ input: z.string() }),
  steps: {
    step1: createStep({
      id: 'step1',
      execute: async ({ context, runId }) => {
        return { data: 'result from step 1' };
      },
    }),
    
    step2: createStep({
      id: 'step2',
      after: ['step1'],  // Runs after step1
      when: async ({ context }) => {
        // Conditional: only run if step1 returned specific value
        return context.results.step1.data.includes('result');
      },
      execute: async ({ context }) => {
        // Can access step1 result
        const prev = context.results.step1;
        return { data: `processed ${prev.data}` };
      },
    }),
    
    step3: createStep({
      id: 'step3',
      after: ['step1'],  // Also runs after step1 (parallel with step2)
      execute: async () => {
        return { data: 'parallel execution' };
      },
    }),
  },
});
```

**Key Finding:** Workflow supports:
- Sequential: `after: ['step1']`
- Conditional: `when: (ctx) => boolean`
- Parallel: Multiple steps with same `after` dependency
- Data passing: `context.results.stepId`

**Source Reference:** `examples/basics/src/mastra/workflows/index.ts:12-85`

### 5. HTTP/External Service Integration

**Finding:** No built-in Kagent or A2A integration found.

**Search Results:**
```bash
# Searched for: kagent|a2a|A2A
# Result: No matches in Mastra codebase
```

**However:** Steps can execute arbitrary async code, including HTTP calls:

```typescript
// Hypothetical integration (NOT in codebase)
const callKagentAgent = createStep({
  id: 'call-salesforce-agent',
  execute: async ({ context }) => {
    const response = await fetch('http://kagent-controller:8083/api/a2a/default/salesforce-agent', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        jsonrpc: '2.0',
        method: 'message/stream',
        params: { message: context.trigger.input }
      })
    });
    return await response.json();
  }
});
```

**Source Reference:** No direct reference - inferred from `execute` function signature allowing any async operation.

### 6. Workflow Serialization

**Search Results:**
```bash
# Searched for: toJSON|fromJSON|serialize|deserialize|parse.*workflow
# Result: No serialization methods found
```

**Key Finding:** Mastra workflows are:
- Defined in TypeScript code
- NOT serializable to JSON/YAML
- NOT storable in Kubernetes CRDs
- Runtime-only constructs

**Source Reference:** `packages/core/src/workflows/workflow.ts` - no serialization methods present

## Integration Feasibility Analysis

### ✅ What IS Possible:

1. **Build Custom Orchestration Layer**
   - Create TypeScript service that uses Mastra workflows
   - Each step calls Kagent agents via HTTP (A2A protocol)
   - Mastra handles conditional logic, sequencing, parallelism
   - Deploy as separate orchestration service

2. **Workflow Pattern Example:**
```typescript
// Custom integration (NOT in codebase)
const employeeOnboarding = createWorkflow({
  name: 'employee-onboarding',
  triggerSchema: z.object({ 
    employeeName: z.string(),
    department: z.string() 
  }),
  steps: {
    createSalesforceProfile: createStep({
      id: 'salesforce',
      execute: async ({ context }) => {
        return await callKagentAgent('salesforce-agent', {
          action: 'create_profile',
          data: context.trigger
        });
      }
    }),
    
    createHRRecord: createStep({
      id: 'hr',
      after: ['salesforce'],
      when: async ({ context }) => {
        // Only if Salesforce succeeded
        return context.results.salesforce.success === true;
      },
      execute: async ({ context }) => {
        return await callKagentAgent('hr-agent', {
          action: 'create_record',
          salesforceId: context.results.salesforce.profileId
        });
      }
    }),
    
    notifySlack: createStep({
      id: 'slack',
      after: ['hr'],
      execute: async ({ context }) => {
        return await callKagentAgent('slack-agent', {
          action: 'send_message',
          message: `Onboarded ${context.trigger.employeeName}`
        });
      }
    })
  }
});
```

### ❌ What is NOT Possible:

1. **No Native Kagent Integration**
   - Mastra has zero knowledge of Kagent
   - No A2A protocol support
   - No Kubernetes CRD integration
   - Must build HTTP client layer manually

2. **No Workflow-as-CRD**
   - Mastra workflows cannot be stored in Kubernetes
   - Cannot define workflows in YAML
   - Cannot manage via kubectl
   - Workflows are TypeScript code only

3. **No Visual Workflow Builder**
   - Mastra has no UI component
   - Workflows defined programmatically
   - No drag-and-drop interface
   - No runtime workflow editor

4. **Separate Runtime Required**
   - Mastra workflows run in Node.js process
   - Cannot run inside Kagent agent pods
   - Requires separate deployment
   - Additional infrastructure overhead

## Architecture Implications

### Option 1: Mastra as Orchestration Layer (Possible but Complex)

```
┌─────────────────────────────────────────┐
│  Mastra Orchestration Service          │
│  (Node.js + TypeScript)                 │
│                                         │
│  Workflow: employee-onboarding          │
│    Step 1: HTTP → Kagent Agent A        │
│    Step 2: HTTP → Kagent Agent B (if)   │
│    Step 3: HTTP → Kagent Agent C        │
└────────┬────────────────────────────────┘
         │ HTTP/A2A calls
         ↓
┌─────────────────────────────────────────┐
│  Kagent Controller (Go)                 │
│  POST /api/a2a/{namespace}/{name}       │
└────────┬────────────────────────────────┘
         │
         ↓
┌─────────────────────────────────────────┐
│  Kagent Agent Pods                      │
│  - salesforce-agent                     │
│  - hr-agent                             │
│  - slack-agent                          │
└─────────────────────────────────────────┘
```

**Pros:**
- Conditional logic in TypeScript (easier than LLM prompts)
- Explicit workflow definition
- Parallel execution support
- Error handling and retries

**Cons:**
- Additional service to deploy/maintain
- Not integrated with Kagent lifecycle
- Workflows not in Kubernetes
- Duplicate agent management (Mastra + Kagent)

### Option 2: Native Kagent Workflow CRD (Recommended but Requires Development)

```yaml
# Hypothetical - NOT in codebase
apiVersion: kagent.dev/v1alpha2
kind: Workflow
metadata:
  name: employee-onboarding
spec:
  trigger:
    type: http
  steps:
    - id: salesforce
      agent: salesforce-agent
      input: "{{ trigger.data }}"
    
    - id: hr
      agent: hr-agent
      after: [salesforce]
      when: "{{ steps.salesforce.success }}"
      input: "{{ steps.salesforce.profileId }}"
    
    - id: slack
      agent: slack-agent
      after: [hr]
      input: "Onboarded {{ trigger.employeeName }}"
```

**Status:** Does NOT exist in Kagent codebase
**Would Require:**
- New Workflow CRD definition
- Workflow controller in Go
- Step execution engine
- Conditional evaluation logic
- Integration with existing Agent CRD

## Conclusion

### Direct Answer to User Question:

**NO** - You cannot directly combine Kagent CRDs with Mastra workflows because:

1. **No Integration Exists**
   - Source: Searched entire Mastra codebase for "kagent", "a2a", "A2A" - zero matches
   - Mastra has no knowledge of Kagent or A2A protocol

2. **Incompatible Runtimes**
   - Mastra: TypeScript/Node.js workflows
   - Kagent: Go controllers + Python/Go agent pods
   - No shared execution environment

3. **No Workflow CRD in Kagent**
   - Source: `go/api/v1alpha2/` - only Agent, ModelConfig, RemoteMCPServer CRDs exist
   - No Workflow or Step CRD definitions
   - Agent CRD only has flat `tools[]` array, not workflow graph

### What You COULD Build:

**Custom Orchestration Service** using Mastra patterns:
- Deploy separate Node.js service
- Implement Mastra-style workflows
- Each step calls Kagent agents via HTTP
- Handle conditionals in TypeScript

**Estimated Effort:** 2-3 weeks for MVP

**Alternative:** Build native Workflow CRD in Kagent (cleaner but more work)

## Source File References

All findings backed by:
- `archived/agentic-ai/mastra/packages/core/src/workflows/workflow.ts`
- `archived/agentic-ai/mastra/packages/core/src/workflows/types.ts`
- `archived/agentic-ai/mastra/packages/core/src/workflows/executor.ts`
- `archived/agentic-ai/mastra/packages/core/src/workflows/step.ts`
- `archived/agentic-ai/mastra/packages/core/src/agent/agent.ts`
- `archived/agentic-ai/mastra/examples/basics/src/mastra/workflows/index.ts`
- `archived/agentic-ai/solo/kagent/go/api/v1alpha2/agent_types.go`

No integration code found between Mastra and Kagent in either codebase.
