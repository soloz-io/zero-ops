---
purpose: Document AgentRegistry OSS capabilities and what agent-core must orchestrate
scope: AgentRegistry integration, deployment orchestration
topics: AgentRegistry API, deployment adapters, Git commits, agent-core responsibilities
update_criteria: When AgentRegistry capabilities or integration patterns change
---

# AgentRegistry OSS Architecture

## What AgentRegistry OSS Actually Provides

Based on `archived/agentic-ai/solo/agentregistry/`:

### CRUD Operations Only
- **POST /v0/agents** - Create/update agent in registry (DB only)
- **GET /v0/agents** - List agents from DB
- **GET /v0/agents/{name}/versions/{version}** - Get agent details from DB
- **DELETE /v0/agents/{name}/versions/{version}** - Delete agent from DB

### Deployment Management (DB Records Only)
- **POST /v0/deployments** - Create deployment record in DB
- **GET /v0/deployments** - List deployment records from DB
- **GET /v0/deployments/{id}** - Get deployment details from DB (single source of truth)
- **DELETE /v0/deployments/{id}** - Delete deployment record from DB

### Database Storage (B-02 Resolution)

**CRITICAL:** AgentRegistry uses **Control Plane Shared DB (agentregistry schema) ONLY**

- All agent definitions stored in `agentregistry.agent_definitions`
- All deployment records stored in `agentregistry.deployments`
- `GET /v0/deployments/{id}` is the **single source of truth** for deployment status
- **Hub Centralised DB is NOT used** - that's only for Crossplane resource provisioning status

**Status Values:**
- `deploying` - Deployment initiated, GitOps in progress
- `deployed` - Agent CRD applied and reconciled
- `failed` - Deployment failed
- `cancelled` - Deployment cancelled

### What AgentRegistry Does NOT Do
- ❌ Does NOT handle Git commits
- ❌ Does NOT generate CRDs
- ❌ Does NOT interact with GitOps repos
- ❌ Does NOT call deployment adapters
- ❌ Does NOT orchestrate provisioning
- ❌ Does NOT write to Hub Centralised DB

## What agent-core Must Orchestrate

### Deployment Flow (agent-core responsibility)
1. Call AgentRegistry POST /v0/agents (CRUD only)
2. Call AgentRegistry POST /v0/deployments (creates DB record)
3. **agent-core generates Agent CRD** (NOT AgentRegistry)
4. **agent-core commits to GitOps repo** via IProvisioner (NOT AgentRegistry)
5. **agent-core publishes NATS events** (platform-specific)

### Architecture Pattern
```
MCP Client → agent-core service
    ↓
    ├─→ AgentRegistry API (CRUD only)
    ├─→ Generate CRD (agent-core)
    ├─→ IProvisioner.CommitManifest() (agent-core)
    ├─→ NATS publish (agent-core)
    └─→ Hub PostgREST queries (agent-core)
```

## Critical Correction
- AgentRegistry is a **registry service** (database + API)
- AgentRegistry does **NOT** have deployment adapters or Git integration
- All provisioning logic belongs in **agent-core orchestration layer**


## Status Query Pattern (B-02 Resolution)

**Single Source of Truth:** AgentRegistry `GET /v0/deployments/{id}`

```go
// agent-core queries deployment status from AgentRegistry ONLY
func (s *StatusService) GetAgentStatus(ctx context.Context, deploymentID string) (*DeploymentStatus, error) {
    // Query AgentRegistry API (Control Plane Shared DB, agentregistry schema)
    deployment, err := s.agentRegistryClient.GetDeployment(ctx, deploymentID)
    if err != nil {
        return nil, err
    }
    
    // AgentRegistry deployment.Status is authoritative
    return &DeploymentStatus{
        DeploymentID: deployment.ID,
        AgentID:      deployment.ServerName,
        Status:       deployment.Status,  // deploying, deployed, failed, cancelled
        UpdatedAt:    deployment.UpdatedAt,
    }, nil
}
```

**No Hub Centralised DB queries needed** - that database is only for Crossplane resource provisioning status (infrastructure layer), not agent deployments.

**Spoke Controller** writes Crossplane claim status to Hub Centralised DB, but agent deployment status remains in AgentRegistry Control Plane Shared DB only.
