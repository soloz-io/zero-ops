# Agent Creation & Deployment - Requirements Specification

**Feature:** Journey 1 - First-Time Agent Creation (MCP-Based)  
**Status:** DRAFT  
**Priority:** P0 (Critical Path)  
**Source PRD:** `docs/prds/v9/agentic-infrastructure-workflow-prd.md` - Section 3.2, Journey 1  
**Related Specs:** None (foundational feature)

---

## 1. Overview

Enable tenant admins to create and deploy their first agent via MCP tools within 2 minutes. This is the foundational user journey for the Zero-Ops agentic platform, establishing the MCP-first interaction model.

**Actors:** Tenant Admin  
**Preconditions:**
- Tenant has active Zero-Ops account (Enterprise tier)
- Tenant has Spoke Silo cluster provisioned
- Tenant has tool credentials stored in Infisical (e.g., Stripe API)
- Tenant has MCP client installed (Cursor, Goose, or Claude Desktop)

**Success Criteria:**
- Agent created and registered in <30 seconds
- Agent deployed to Spoke in <2 minutes total
- Agent enters "Ready" state (or scaled to 0)
- Test invocation succeeds via MCP

---

## 2. Functional Requirements

### FR-1: MCP Tool - Create Agent

**Tool Name:** `create_agent`

**Description:** Register a new agent definition in AgentRegistry without deploying runtime.

**Input Parameters:**
```json
{
  "name": "string (required, lowercase-hyphenated, max 63 chars)",
  "version": "string (required, semver format, e.g., '1.0.0')",
  "agent_type": "enum (required, values: 'infrastructure' | 'business')",
  "system_message": "string (required, max 2000 chars)",
  "tool_access": "array<string> (required, list of authorized tool names)",
  "memory_config": {
    "enabled": "boolean (required)",
    "namespace": "string (optional, defaults to agent name)"
  },
  "guardrail_policies": {
    "global": "array<string> (required, inherited policies)",
    "task_specific": "array<object> (optional, agent-specific policies)"
  },
  "model_config": {
    "name": "string (required, e.g., 'gpt-4-turbo')",
    "temperature": "number (optional, default 0.7, range 0-2)"
  }
}
```

**Output (Success):**
```json
{
  "agent_id": "uuid",
  "name": "string",
  "status": "registered",
  "created_at": "ISO8601 timestamp",
  "message": "Agent created successfully. Use deploy_agent to provision runtime."
}
```

**Output (Error):**
```json
{
  "error": "string (error code)",
  "message": "string (human-readable error)",
  "details": "object (optional, additional context)"
}
```

**Error Codes:**
- `quota_exceeded`: Tenant has reached agent limit
- `unauthorized_tool`: Tool not authorized for tenant
- `invalid_policy_syntax`: Guardrail policy syntax error
- `invalid_name`: Agent name violates naming rules
- `duplicate_agent`: Agent with same name+version already exists

**Backend Flow:**
1. MCP server validates JWT token (tenant_id extraction)
2. MCP server → AgentRegistry API (POST /v0/agents)
3. AgentRegistry validates:
   - Tenant quota (max agents per tier)
   - Tool access permissions (check Infisical/RBAC)
   - Guardrail policy syntax
   - Agent name uniqueness (tenant_id + name + version)
4. AgentRegistry stores config in Control Plane Shared DB (schema: agents, table: agent_definitions)
5. Return agent_id and status

**Database Schema:**
```sql
-- Control Plane Shared DB, schema: agentregistry (managed by AgentRegistry OSS)
-- Reference: B-01 resolution - tenant isolation via RLS
CREATE TABLE agent_definitions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,  -- Tenant isolation column
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    agent_type TEXT NOT NULL,
    system_message TEXT NOT NULL,
    tool_access JSONB NOT NULL,
    memory_config JSONB NOT NULL,
    guardrail_policies JSONB NOT NULL,
    model_config JSONB NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(tenant_id, name, version)  -- Tenant-scoped uniqueness
);

-- RLS policy for tenant isolation (SBT pattern)
ALTER TABLE agent_definitions ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON agent_definitions
    USING (tenant_id = current_setting('app.tenant_id')::UUID);
```

**Tenant Context Setting:**
agent-core sets `app.tenant_id` before calling AgentRegistry API to enforce RLS.

---

### FR-2: MCP Tool - Deploy Agent

**Tool Name:** `deploy_agent`

**Description:** Provision agent runtime in target Spoke cluster.

**Input Parameters:**
```json
{
  "agent_id": "uuid (required, from create_agent response)"
}
```

**Output (Success):**
```json
{
  "deployment_id": "uuid",
  "agent_id": "uuid",
  "status": "deploying",
  "spoke_cluster_id": "string",
  "message": "Agent deployment initiated. Use get_agent_status to poll for completion."
}
```

**Output (Error):**
```json
{
  "error": "string (error code)",
  "message": "string (human-readable error)",
  "deployment_id": "uuid (if created)",
  "status": "failed"
}
```

**Error Codes:**
- `agent_not_found`: Agent ID does not exist
- `spoke_unreachable`: Cannot connect to Spoke cluster
- `deployment_timeout`: Deployment exceeded 2 minute timeout
- `crd_condition_failed`: Agent CRD failed to reach Ready condition
- `spoke_controller_sync_failed`: Spoke Controller failed to sync status to Hub

**Backend Flow:**
1. MCP server validates JWT token (tenant_id, spoke_cluster_id extraction from claims - B-03 resolution)
2. MCP server → AgentRegistry API (POST /v0/deployments)
3. AgentRegistry creates deployment record in Control Plane Shared DB (status: "deploying")
4. AgentRegistry retrieves agent config from its database
5. agents-core invokes Deployment Adapter (Kubernetes client-go)
6. Deployment Adapter generates Kagent Agent CRD YAML
7. Deployment Adapter applies CRD directly to Spoke cluster via Kubernetes API (kubectl apply equivalent)
8. Deployment status remains "deploying"; asynchronous status sync updates it to "deployed" (via NATS subscriber)
9. MCP server publishes NATS event: `hub.platform.agent.deployed` (H-08 resolution)
10. Return immediately with deployment_id and status "deploying"
11. Kagent Controller (Spoke) reconciles Agent CRD (async):
   - Creates Kubernetes Deployment (Agent Pod)
   - Injects environment variables (Memory, Guardrail endpoints)
   - Creates KEDA ScaledObject (scale-to-zero config)
   - Registers A2A handler in Kagent A2AHandlerMux
12. Spoke Controller watches Agent CRD status and syncs to Hub Centralised DB via PostgREST
13. Client polls get_agent_status to check deployment completion

**Note on Provisioning Paths:**
- **Business Agents (tenant-created)**: agents-core → Deployment Adapter → Direct Kubernetes API apply (NO GitOps)
- **Infrastructure Agents (platform team)**: Direct Git commits → ArgoCD → Spoke cluster (GitOps only)

**Note on spoke_cluster_id (B-03 resolution):**
- Set during environment_create, stored in `tenants.spoke_cluster_id`
- Ory Hydra enriches JWT with spoke_cluster_id from tenant record
- No `provider_id` parameter needed - derived from JWT claims

**Database Schema:**
```sql
-- Control Plane Shared DB, schema: agents
CREATE TABLE deployments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    agent_id UUID NOT NULL REFERENCES agent_definitions(id),
    provider_id TEXT NOT NULL,
    status TEXT NOT NULL, -- 'deploying', 'deployed', 'failed'
    provider_metadata JSONB,
    deployed_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
```

---

### FR-3: MCP Tool - Update Agent

**Tool Name:** `update_agent`

**Description:** Update an existing agent's configuration (system message, model, tools, guardrails) without changing name or version.

**Input Parameters:**
```json
{
  "agent_id": "uuid (required)",
  "system_message": "string (optional, max 2000 chars)",
  "tool_access": "array<string> (optional, list of authorized tool names)",
  "memory_config": {
    "enabled": "boolean (optional)",
    "namespace": "string (optional)"
  },
  "guardrail_policies": {
    "global": "array<string> (optional)",
    "task_specific": "array<object> (optional)"
  },
  "model_config": {
    "name": "string (optional, e.g., 'gpt-4-turbo')",
    "temperature": "number (optional, range 0-2)"
  }
}
```

**Output (Success - Not Deployed):**
```json
{
  "agent_id": "uuid",
  "name": "string",
  "version": "string",
  "status": "updated",
  "deployed": false,
  "updated_at": "ISO8601 timestamp",
  "message": "Agent configuration updated successfully. Changes will apply when agent is deployed."
}
```

**Output (Success - Already Deployed):**
```json
{
  "agent_id": "uuid",
  "name": "string",
  "version": "string",
  "status": "updated",
  "deployed": true,
  "deployment_id": "uuid",
  "updated_at": "ISO8601 timestamp",
  "message": "Agent configuration updated successfully. Redeployment initiated."
}
```

**Output (Error):**
```json
{
  "error": "string (error code)",
  "message": "string (human-readable error)",
  "details": "object (optional, additional context)"
}
```

**Error Codes:**
- `agent_not_found`: Agent ID does not exist
- `unauthorized_tool`: Tool not authorized for tenant
- `invalid_policy_syntax`: Guardrail policy syntax error

**Backend Flow:**
1. MCP server validates JWT token (tenant_id extraction)
2. MCP server → AgentRegistry API (PATCH /v0/agents/{agent_id})
3. AgentRegistry validates:
   - Agent exists and belongs to tenant
   - Tool access permissions (if tool_access provided)
   - Guardrail policy syntax (if guardrail_policies provided)
4. AgentRegistry updates JSONB config in Control Plane Shared DB (agents.agent_definitions table)
5. **Check if agent is deployed** (query deployments table):
   - **If NOT deployed**: Return success immediately (no redeployment needed)
   - **If deployed**: Proceed with redeployment (steps 6-8)
6. AgentRegistry invokes Deployment Adapter with updated config
7. Deployment Adapter applies updated Agent CRD directly to Spoke cluster via Kubernetes API
8. Kagent Controller performs rolling update of Agent Pod
9. Return updated agent metadata

---

### FR-4: MCP Tool - List Authorized Tools

**Tool Name:** `list_authorized_tools`

**Description:** Query Control Plane Shared DB and evaluate Keto/RBAC permissions to discover tools the tenant can attach to agents.

**Input Parameters:**
```json
{
  "category": "string (optional, filter by tool category, e.g., 'infrastructure', 'business')"
}
```

**Output:**
```json
{
  "tools": [
    {
      "tool_name": "string (e.g., 'stripe_api', 'github_mcp')",
      "category": "string (infrastructure | business | integration)",
      "description": "string",
      "requires_credentials": "boolean"
    }
  ],
  "total_count": "number"
}
```

**Backend Flow:**
1. MCP server validates JWT token (tenant_id extraction)
2. MCP server → AgentRegistry API (GET /v0/tools?tenant_id={tenant_id})
3. AgentRegistry queries Control Plane Shared DB (Tenant Config schema)
4. AgentRegistry evaluates Keto/RBAC permissions for tenant
5. Filter by category parameter (if provided)
6. Return list of authorized tools

---

### FR-5: MCP Tool - Get Agent Status

**Tool Name:** `get_agent_status`

**Description:** Retrieve current status of agent (registration + deployment).

**Input Parameters:**
```json
{
  "agent_id": "uuid (required)"
}
```

**Output:**
```json
{
  "agent_id": "uuid",
  "name": "string",
  "version": "string",
  "status": "string (registered | deployed | failed)",
  "created_at": "ISO8601 timestamp",
  "deployments": [
    {
      "deployment_id": "uuid",
      "provider_id": "string",
      "status": "string",
      "pod_name": "string",
      "replicas": "string",
      "ready": "boolean",
      "last_heartbeat": "ISO8601 timestamp"
    }
  ]
}
```

---

### FR-6: MCP Tool - List Agents

**Tool Name:** `list_agents`

**Description:** List all agents for current tenant.

**Input Parameters:**
```json
{
  "limit": "number (optional, default 50, max 100)",
  "offset": "number (optional, default 0)"
}
```

**Output:**
```json
{
  "agents": [
    {
      "agent_id": "uuid",
      "name": "string",
      "version": "string",
      "agent_type": "string",
      "status": "string",
      "created_at": "ISO8601 timestamp"
    }
  ],
  "total_count": "number",
  "limit": "number",
  "offset": "number"
}
```

---

### FR-7: MCP Tool - Delete Agent

**Tool Name:** `delete_agent`

**Description:** Soft-delete agent definition and undeploy runtime. Retains vector memory by default to prevent accidental data loss.

**Input Parameters:**
```json
{
  "agent_id": "uuid (required)",
  "purge_memory": "boolean (optional, default false, if true deletes pgvector memory namespace)"
}
```

**Output:**
```json
{
  "success": "boolean",
  "message": "string",
  "deleted_deployments": "number",
  "memory_retained": "boolean"
}
```

**Backend Flow:**
1. MCP server validates JWT token (tenant_id extraction)
2. MCP server → AgentRegistry API (DELETE /v0/agents/{agent_id})
3. AgentRegistry soft-deletes the agent record in Control Plane Shared DB (sets deleted_at timestamp)
4. AgentRegistry retains the pgvector memory namespace in Control Plane Shared DB (schema: memory) by default to prevent accidental data loss
5. If purge_memory=true, delete vector embeddings from memory schema
6. AgentRegistry queries deployments table for active deployments
7. For each deployment: AgentRegistry invokes Deployment Adapter to delete CRD
8. Deployment Adapter deletes Agent CRD from Spoke cluster via Kubernetes API (kubectl delete equivalent)
9. Kagent Controller detects deletion and removes Agent Pod and KEDA ScaledObject
10. Return deletion status

---

## 3. Non-Functional Requirements

### NFR-1: Performance
- Agent creation (FR-1): <30 seconds (P95)
- Agent deployment (FR-2): <2 minutes total (P95)
- Agent status query (FR-3): <500ms (P95)

### NFR-2: Scalability
- Support 100 concurrent agent creation requests
- Support 1,000 total agents per tenant (Enterprise tier)
- Support 10,000 total agents across platform

### NFR-3: Reliability
- Agent creation success rate: >95%
- Agent deployment success rate: >90% (excluding Spoke unavailability)
- Database writes must be atomic (agent_definitions + deployments)

### NFR-4: Security
- All MCP tool calls require valid JWT token
- Tenant isolation enforced via RLS policies in Control Plane Shared DB
- Tool access permissions validated against Infisical/RBAC
- Cross-cluster API calls use service account credentials

### NFR-5: Observability
- All MCP tool invocations logged to OpenSearch
- Agent creation/deployment metrics emitted to VictoriaMetrics
- Failed deployments trigger alerts (PagerDuty/Slack)

---

## 4. Error Handling

### Quota Exceeded (FR-1)
**Scenario:** Tenant has 10 agents (Enterprise tier limit)

**MCP Tool Response:**
```json
{
  "error": "quota_exceeded",
  "message": "Cannot create agent. Tenant has reached agent limit (10). Upgrade to Enterprise Plus tier or delete unused agents.",
  "current_count": 10,
  "limit": 10,
  "upgrade_url": "https://console.zero-ops.io/billing/upgrade"
}
```

**User Action:** Use `list_agents` to view existing agents, then `delete_agent` to remove unused ones.

---

### Unauthorized Tool (FR-1)
**Scenario:** Tenant requests Stripe API access but not authorized

**MCP Tool Response:**
```json
{
  "error": "unauthorized_tool",
  "message": "Stripe API not authorized for tenant 'acme-corp'. Contact support to enable.",
  "tool": "stripe_api",
  "support_url": "https://support.zero-ops.io/enable-tool"
}
```

**User Action:** Contact support or use `create_support_ticket` MCP tool.

---

### Invalid Policy Syntax (FR-1)
**Scenario:** Guardrail policy has syntax error

**MCP Tool Response:**
```json
{
  "error": "invalid_policy_syntax",
  "message": "Guardrail policy syntax error: 'deniedTables' must be array of strings, got object.",
  "policy_field": "task_specific[0].denied_tables",
  "expected_type": "array",
  "received_type": "object"
}
```

**User Action:** Correct policy syntax and retry `create_agent`.

---

### Spoke Unreachable (FR-2)
**Scenario:** Cannot connect to Spoke cluster during deployment

**MCP Tool Response:**
```json
{
  "error": "spoke_unreachable",
  "message": "Cannot connect to Spoke cluster 'spoke-silo-acme-corp'. Check cluster status.",
  "provider_id": "spoke-silo-acme-corp",
  "deployment_id": "uuid",
  "status": "failed"
}
```

**User Action:** Check Spoke cluster health via infrastructure MCP tools.

---

### Deployment Timeout (FR-2)
**Scenario:** Agent CRD does not reach Ready condition within 2 minutes

**MCP Tool Response:**
```json
{
  "error": "deployment_timeout",
  "message": "Agent deployment timeout after 2 minutes. CRD status: Pending. Check Spoke Controller sync status.",
  "deployment_id": "uuid",
  "crd_name": "billing-automation-agent",
  "crd_status": "Pending",
  "spoke_controller_events": [
    "Warning  SyncFailed  Unable to sync status to Hub Centralised DB"
  ]
}
```

**User Action:** Use `analyze_agent_failure` MCP tool for detailed diagnosis.

---

### Agent Already Deployed (FR-2)
**Scenario:** `deploy_agent` called for an agent that is already deployed and Ready

**MCP Tool Response:**
```json
{
  "deployment_id": "uuid",
  "status": "deployed",
  "pod_name": "billing-automation-agent-7d8f9c",
  "replicas": "0/0 (scaled to zero by KEDA)",
  "ready": true,
  "message": "Agent is already deployed and Ready."
}
```

**User Action:** No action required. API is idempotent and returns success if desired state matches actual state.

---

## 5. Data Model

### Agent Definition (Control Plane Shared DB)
```sql
-- Schema: agents
-- Table: agent_definitions
{
  "id": "uuid",
  "tenant_id": "uuid",
  "name": "billing-automation-agent",
  "version": "1.0.0",
  "agent_type": "infrastructure",
  "system_message": "You are a billing automation expert...",
  "tool_access": ["stripe_api", "email_service"],
  "memory_config": {
    "enabled": true,
    "namespace": "billing-automation-agent"
  },
  "guardrail_policies": {
    "global": ["pii_filter", "content_filter"],
    "task_specific": [
      {
        "type": "data_access_restriction",
        "denied_tables": ["customers", "internal_notes"]
      }
    ]
  },
  "model_config": {
    "name": "gpt-4-turbo",
    "temperature": 0.7
  },
  "created_at": "2026-03-25T10:00:00Z",
  "updated_at": "2026-03-25T10:00:00Z"
}
```

### Deployment Record (Control Plane Shared DB)
```sql
-- Schema: agents
-- Table: deployments
{
  "id": "uuid",
  "tenant_id": "uuid",
  "agent_id": "uuid",
  "provider_id": "spoke-silo-acme-corp",
  "status": "deployed",
  "provider_metadata": {
    "namespace": "tenant-acme",
    "pod_name": "billing-automation-agent-7d8f9c",
    "replicas": 1
  },
  "deployed_at": "2026-03-25T10:01:45Z",
  "updated_at": "2026-03-25T10:01:45Z"
}
```

---

## 6. Integration Points

### 6.1 AgentRegistry API
- **Endpoint:** POST /v0/agents
- **Authentication:** JWT token (via auth-proxy)
- **Request Body:** Agent definition JSON
- **Response:** Agent ID + status

### 6.2 Deployment Adapter (Kubernetes)
- **Input:** Agent definition + spoke cluster credentials
- **Output:** Kagent Agent CRD applied to cluster
- **Action:** Uses Kubernetes client-go to directly apply the Kagent Agent CRD to the target Spoke cluster. No Git repository or ArgoCD involved for business agents.

### 6.3 Kagent Controller (Spoke)
- **Input:** Agent CRD
- **Output:** Kubernetes Deployment + KEDA ScaledObject
- **Action:** Reconcile CRD → provision runtime

### 6.4 Control Plane Shared DB
- **Schema:** agents
- **Tables:** agent_definitions, deployments
- **Access:** PostgREST API (Hub-side)

### 6.5 Infisical (Secret Management)
- **Purpose:** Validate tool access permissions
- **Query:** Check if tenant has credentials for requested tools

---

## 7. Testing Strategy

**CRITICAL: Manual testing only via MCP tools. No automated test suites.**

### Test Case 1: Happy Path - Create and Deploy Agent
1. Open MCP client (Cursor)
2. Invoke `create_agent` with valid parameters
3. Verify response: `{"agent_id": "...", "status": "registered"}`
4. Invoke `deploy_agent` with agent_id
5. Verify response: `{"status": "deployed", "ready": true}`
6. Invoke `get_agent_status` to confirm deployment
7. Verify pod running in Spoke cluster (kubectl)

### Test Case 2: Quota Exceeded
1. Create 10 agents (Enterprise tier limit)
2. Attempt to create 11th agent
3. Verify error: `{"error": "quota_exceeded", ...}`
4. Invoke `list_agents` to view existing agents
5. Invoke `delete_agent` to remove one agent
6. Retry create_agent (should succeed)

### Test Case 3: Unauthorized Tool
1. Invoke `create_agent` with tool_access: ["stripe_api"]
2. Verify error: `{"error": "unauthorized_tool", ...}`
3. Contact support to enable Stripe API
4. Retry create_agent (should succeed)

### Test Case 4: Invalid Policy Syntax
1. Invoke `create_agent` with malformed guardrail policy
2. Verify error: `{"error": "invalid_policy_syntax", ...}`
3. Correct policy syntax
4. Retry create_agent (should succeed)

### Test Case 5: Deployment Timeout
1. Invoke `deploy_agent` to Spoke with insufficient resources
2. Wait 2 minutes
3. Verify error: `{"error": "deployment_timeout", ...}`
4. Check pod events (kubectl describe pod)
5. Fix resource constraints
6. Retry deploy_agent (should succeed)

---

## 8. Acceptance Criteria

- [ ] MCP tool `create_agent` registers agent in <30 seconds
- [ ] MCP tool `deploy_agent` provisions runtime in <2 minutes
- [ ] Agent enters "Ready" state (or scaled to 0)
- [ ] MCP tool `get_agent_status` returns accurate deployment status
- [ ] MCP tool `list_agents` returns all tenant agents
- [ ] MCP tool `delete_agent` removes agent and undeploys runtime
- [ ] Quota exceeded error prevents agent creation beyond limit
- [ ] Unauthorized tool error blocks agent creation
- [ ] Invalid policy syntax error provides clear remediation
- [ ] Deployment timeout error includes pod events for debugging
- [ ] All agent data stored in Control Plane Shared DB (schema: agents)
- [ ] RLS policies enforce tenant isolation
- [ ] All MCP tool invocations logged to OpenSearch
- [ ] Agent creation/deployment metrics emitted to VictoriaMetrics

---

## 9. Out of Scope

- Agent invocation (covered in Journey 2)
- Context document upload (covered in Journey 3)
- Agent failure debugging (covered in Journey 4)
- Guardrail policy configuration (covered in Journey 5)
- Automated testing frameworks
- Web UI dashboard
- Agent marketplace
- Custom embedding models
- Tenant MCP servers

---

## 10. Dependencies

- AgentRegistry service (Go)
- Kagent Controller (Go, deployed in Spokes)
- Control Plane Shared DB (PostgreSQL + pgvector)
- Deployment Adapter (Kubernetes client)
- Zero-Ops MCP Server (Go)
- Ory Hydra (JWT token issuer)
- Infisical (secret management)
- KEDA HTTP Add-on (scale-to-zero)

---

## 11. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Spoke cluster unreachable during deployment | High | Implement retry logic with exponential backoff. Store deployment status in DB for async reconciliation. |
| Agent pod fails to start (OOMKilled, ImagePullBackOff) | Medium | Provide detailed error messages with pod events. Implement Agent Debugging Service for automated diagnosis. |
| Quota enforcement bypass | High | Enforce quota checks at API layer (AgentRegistry) with database constraints (UNIQUE tenant_id + name + version). |
| Cross-cluster authentication failure | High | Use service account credentials with cert-manager for mTLS. Implement credential rotation. |
| Database connection pool exhaustion | Medium | Configure PgBouncer with pool_mode=transaction. Monitor connection count via VictoriaMetrics. |

---

## 12. Future Enhancements (Out of Scope for v1.0)

- Agent versioning and rollback
- Agent templates and marketplace
- Multi-region agent deployment
- Agent collaboration workflows
- Custom embedding models per tenant
- Tenant-defined MCP tool servers
- Agent performance optimization recommendations
- Agent cost optimization suggestions
