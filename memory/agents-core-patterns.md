---
purpose: Agents-Core implementation pattern guide
scope: agents-core spec implementation
topics: deployment pattern, provisioning, RLS, status sync, event naming
update_criteria: When architectural decisions change for agents-core
---

# Agents-Core Implementation Pattern

## Dual Provisioning Path
- **Platform Agents (App Plane):** GitOps via IProvisioner → ArgoCD
- **Business Agents (Data Plane):** Direct K8s API via Deployment Adapter → Spoke cluster (bypasses GitOps)

## deploy_agent Flow (Business Agents)
1. Create deployment record in AgentRegistry DB (status: "deploying")
2. Generate Kagent Agent CRD
3. Apply CRD DIRECTLY to Spoke K8s API via k8s_spoke_adapter (NOT IProvisioner/GitOps)
4. Publish NATS event (fire and forget, for billing/audit)
5. Return immediately with status "deploying"

## Status Sync (Status Controller Pattern)
- mcp-server NEVER queries K8s API for pod status
- Spoke Controller watches pod/KEDA → POST to Hub PostgREST
- Hub DB pg_notify → NATS subscriber → UPDATE agentregistry.deployments.status
- get_agent_status: GET /v0/deployments/{id} from AgentRegistry only

## Project Structure
- /internal/agents-core/adapter/k8s_spoke_adapter.go  ← NEW: direct CRD apply to Spoke
- /internal/agents-core/mcp/  ← MCP tool definitions
- /internal/agents-core/service/  ← orchestration
- /internal/agents-core/client/  ← AgentRegistry + Hub clients
- /internal/agents-core/database/  ← platform SQL

## RLS Pattern
```go
func (s *DBClient) WithTenant(ctx context.Context, tenantID string, fn func(db.Querier) error) error {
    tx, _ := s.pool.Begin(ctx)
    tx.Exec(ctx, "SET LOCAL app.tenant_id = $1", tenantID)
    err = fn(db.New(tx))
    tx.Commit(ctx)
    return err
}
```
- MUST call SET LOCAL app.tenant_id before ANY SQL query on platform tables

## JWT Headers (from AgentGateway)
- X-Auth-Tenant-ID
- X-Auth-Tenant-Tier
- X-Auth-Spoke-Cluster-ID

## Event Naming
- opensbt_ prefix: ONLY open-sbt core events (DO NOT USE for agent events)
- Agent events: zeroops_ prefix (e.g., zeroops_agentDeployRequested)
- Hub NATS subjects: hub.platform.agent.* (e.g., hub.platform.agent.infra_status)

## Code Review Rules
- Business agents MUST use Deployment Adapter, NOT IProvisioner
- Agent structs MUST be in /internal/agent-core/models, NOT /internal/opensbt/models
- deploy_agent MUST return "deploying" immediately after CRD accepted
- get_agent_status MUST NOT import k8s client-go
- All SQL MUST use SET LOCAL app.tenant_id (RLS)
- No opensbt_ prefix on agent events
