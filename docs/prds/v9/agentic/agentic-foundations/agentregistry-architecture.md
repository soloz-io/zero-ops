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
- **GET /v0/deployments/{id}** - Get deployment details from DB
- **DELETE /v0/deployments/{id}** - Delete deployment record from DB

### What AgentRegistry Does NOT Do
- ❌ Does NOT handle Git commits
- ❌ Does NOT generate CRDs
- ❌ Does NOT interact with GitOps repos
- ❌ Does NOT call deployment adapters
- ❌ Does NOT orchestrate provisioning

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
