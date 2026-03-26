# Agents Core - Implementation Tasks

**Spec ID:** agents-core  
**Status:** Ready for Implementation  
**Created:** 2026-03-25

---

## Implementation Phases

This implementation follows a phased approach with manual testing gates between phases. Each phase must be completed, tested, and approved before proceeding to the next phase.

---

## Phase 1: Database Schema & AgentRegistry Integration

**Goal:** Set up database schemas and HTTP client for AgentRegistry OSS API

### Tasks

- [x] 1.1 Create database migration for Control Plane Shared DB
  - [x] 1.1.1 Create `agentregistry` schema with tenant isolation (B-01 resolution)
  - [x] 1.1.2 Add `tenant_id` column to agent_definitions table
  - [x] 1.1.3 Update UNIQUE constraint to (tenant_id, name, version)
  - [x] 1.1.4 Create RLS policy: `tenant_isolation` using `app.tenant_id`
  - [x] 1.1.5 Create `agents` schema for platform-specific extensions
  - [x] 1.1.6 Create `authorized_tools` table with RLS policies

- [x] 1.2 Create database migration for Hub Centralised DB
  - [x] 1.2.1 Create `agent_infra_status` table
  - [x] 1.2.2 Add indexes for tenant_id, agent_id, deployment_id
  - [x] 1.2.3 Create pg_notify trigger for NATS event publishing

- [ ] 1.3 Create sqlc queries for platform-specific tables
  - [x] 1.3.1 Create `internal/agent-core/database/queries.sql`
  - [x] 1.3.2 Add queries for authorized_tools (GetAuthorizedTools, GetAuthorizedToolsByCategory, ValidateToolAccess)
  - [x] 1.3.3 Create sqlc.yaml config

- [x] 1.4 Implement AgentRegistry HTTP client
  - [x] 1.4.1 Create `internal/agent-core/client/agentregistry_client.go`
  - [x] 1.4.2 Implement CreateAgent (POST /v0/agents)
  - [x] 1.4.3 Implement GetAgent (GET /v0/agents/{name}/versions/{version})
  - [x] 1.4.4 Implement ListAgents (GET /v0/agents)
  - [x] 1.4.5 Implement DeleteAgent (DELETE /v0/agents/{name}/versions/{version})
  - [x] 1.4.6 Implement CreateDeployment (POST /v0/deployments)
  - [x] 1.4.7 Implement ListDeployments (GET /v0/deployments)
  - [x] 1.4.8 Implement DeleteDeployment (DELETE /v0/deployments/{id})

- [x] 1.5 Implement Hub PostgREST client
  - [x] 1.5.1 Create `internal/agent-core/client/hub_client.go`
  - [x] 1.5.2 Implement GetAgentInfraStatus (query agent_infra_status by deployment_id)
  - [x] 1.5.3 Implement ListAgentDeployments (query agent_infra_status by tenant_id)

**Manual Testing Checkpoint:**
- Apply `manifests/platform-database/platform-db.yaml` → verify CNPG cluster Ready
- Apply `manifests/platform-database/migrations/control-plane-migrations.yaml` → verify Job completes
- Apply `manifests/platform-database/migrations/hub-migrations.yaml` → verify Job completes
- Test AgentRegistry client against local AgentRegistry instance
- Test Hub PostgREST client against Hub Centralised DB

**PAUSE: User must approve Phase 1 before proceeding to Phase 2**

---

## Phase 2: Platform-Specific Validators

**Goal:** Implement model and tool authorization validators

### Tasks

- [x] 2.1 Implement model authorization validator
  - [x] 2.1.1 Create `internal/agent-core/validators/model_validator.go`
  - [x] 2.1.2 Implement ValidateModelAuthorization (check tier-based access)
  - [x] 2.1.3 Return error codes: `unauthorized_model`, `invalid_provider`, `invalid_model`

- [x] 2.2 Implement tool authorization validator
  - [x] 2.2.1 Create `internal/agent-core/validators/tool_validator.go`
  - [x] 2.2.2 Implement ValidateToolAuthorization (check tenant-specific permissions)
  - [x] 2.2.3 Return error code: `unauthorized_tool`

**Manual Testing Checkpoint:**
- Test model validator with different tenant tiers (basic, standard, premium, enterprise)
- Test tool validator with authorized and unauthorized tools
- Verify error codes returned correctly

**PAUSE: User must approve Phase 2 before proceeding to Phase 3**

---

## Phase 3: Agent Service - Create & List Operations

**Goal:** Implement create_agent, list_agents, and list_authorized_tools MCP tools

### Tasks

- [x] 3.1 Implement agent service
  - [x] 3.1.1 Create `internal/agent-core/service/agent_service.go`
  - [x] 3.1.2 Add service struct with dependencies (AgentRegistryClient, HubClient, ControlPlane, DB)
  - [x] 3.1.3 Implement CreateAgent method (orchestration pattern)
  - [x] 3.1.4 Implement ListAgents method (orchestration pattern)

- [x] 3.2 Implement create_agent MCP tool
  - [x] 3.2.1 Create `cmd/mcp-server/tools/agents/create_agent.go`
  - [x] 3.2.2 Extract tenant context from JWT (tenant_id, tenant_tier)
  - [x] 3.2.3 Set tenant context: `SET app.tenant_id = $1` before AgentRegistry calls
  - [x] 3.2.4 Call model validator
  - [x] 3.2.5 Call tool validator
  - [x] 3.2.6 Call AgentRegistry CreateAgent API
  - [x] 3.2.7 Publish NATS event: `hub.platform.agent.created` (H-08 resolution)
  - [x] 3.2.8 Return MCP-formatted response

- [x] 3.3 Implement list_agents MCP tool
  - [x] 3.3.1 Create `cmd/mcp-server/tools/agents/list_agents.go`
  - [x] 3.3.2 Call AgentRegistry ListAgents API
  - [x] 3.3.3 Enrich with deployment status from Hub PostgREST
  - [x] 3.3.4 Filter by status parameter (all, deployed, not_deployed)
  - [x] 3.3.5 Return MCP-formatted response

- [x] 3.4 Register MCP tools in server
  - [x] 3.4.1 Update `cmd/mcp-server/main.go` to initialize agent service
  - [x] 3.4.2 Register create_agent tool
  - [x] 3.4.3 Register list_agents tool

- [x] 3.5 Implement list_authorized_tools method
  - [x] 3.5.1 Add ListAuthorizedTools method to agent_service.go
  - [x] 3.5.2 Query authorized_tools table from Control Plane Shared DB
  - [x] 3.5.3 Filter by tenant_id and tier
  - [x] 3.5.4 Filter by category parameter (if provided)

- [x] 3.6 Implement list_authorized_tools MCP tool
  - [x] 3.6.1 Create `cmd/mcp-server/tools/agents/list_authorized_tools.go`
  - [x] 3.6.2 Extract tenant context from JWT
  - [x] 3.6.3 Call agent service ListAuthorizedTools method
  - [x] 3.6.4 Return MCP-formatted response

- [x] 3.7 Register list_authorized_tools tool
  - [x] 3.7.1 Register tool in main.go

**Manual Testing Checkpoint:**
- Test create_agent via MCP client (Cursor/Goose)
- Verify agent created in AgentRegistry
- Test list_agents via MCP client
- Verify deployment status enrichment works
- Test list_authorized_tools via MCP client
- Verify tools filtered by tenant tier
- Test category filter parameter

**PAUSE: User must approve Phase 3 before proceeding to Phase 4**

---

## Phase 4: Deployment Service - CRD Generation & Kubernetes API Integration

**Goal:** Implement deploy_agent MCP tool with AgentRegistry integration

### Tasks

- [x] 4.1 Implement deployment service
  - [x] 4.1.1 Create `internal/agent-core/service/deployment_service.go`
  - [x] 4.1.2 Add service struct with dependencies
  - [x] 4.1.3 Implement DeployAgent method (calls AgentRegistry API)

- [x] 4.2 Implement deploy_agent MCP tool
  - [x] 4.2.1 Create `cmd/mcp-server/tools/agents/deploy_agent.go`
  - [x] 4.2.2 Extract tenant context from JWT (tenant_id, spoke_cluster_id) - B-03 resolution
  - [x] 4.2.3 Fetch agent from AgentRegistry
  - [x] 4.2.4 Create deployment via AgentRegistry API (POST /v0/deployments)
  - [x] 4.2.5 agents-core invokes Deployment Adapter to apply CRD to Spoke cluster
  - [x] 4.2.6 Publish NATS event: `hub.platform.agent.deployed` (H-08 resolution)
  - [x] 4.2.7 Return MCP response with status "deploying" (deployment_id, spoke_cluster_id)

- [x] 4.3 Register deploy_agent tool
  - [x] 4.3.1 Update `cmd/mcp-server/main.go` to initialize deployment service
  - [x] 4.3.2 Register deploy_agent tool

**Manual Testing Checkpoint:**
- Test deploy_agent via MCP client
- Verify deployment record created in AgentRegistry
- Verify Agent CRD applied to Spoke cluster (kubectl get agents)
- Verify Kagent Controller reconciles Agent CRD

**PAUSE: User must approve Phase 4 before proceeding to Phase 5**

---

## Phase 5: Status Service - Polling & Monitoring

**Goal:** Implement get_agent_status MCP tool for deployment status polling

### Tasks

- [x] 5.1 Implement status service
  - [x] 5.1.1 Create `internal/agent-core/service/status_service.go`
  - [x] 5.1.2 Add service struct with dependencies
  - [x] 5.1.3 Implement GetAgentStatus method (query Hub PostgREST)

- [x] 5.2 Implement get_agent_status MCP tool
  - [x] 5.2.1 Create `cmd/mcp-server/tools/agents/get_agent_status.go`
  - [x] 5.2.2 Extract tenant context from JWT
  - [x] 5.2.3 Query Hub Centralised DB via PostgREST
  - [x] 5.2.4 Return deployment status (deploying, provisioning, ready, failed, cancelled)
  - [x] 5.2.5 Return phase (Running, Idle, Failed)
  - [x] 5.2.6 Return replicas, message, error details

- [x] 5.3 Register get_agent_status tool
  - [x] 5.3.1 Update `cmd/mcp-server/main.go` to initialize status service
  - [x] 5.3.2 Register get_agent_status tool

**Manual Testing Checkpoint:**
- Deploy an agent via deploy_agent
- Poll get_agent_status until status reaches "ready"
- Verify status transitions: deploying → provisioning → ready
- Test with KEDA scale-to-zero (verify phase: "Idle")

---

## Phase 5b: Status Sync Subscriber (pg_notify → AgentRegistry)

**Goal:** Bridge Hub Centralised DB status updates into AgentRegistry deployment records.

### Tasks
- [x] 5b.1 Implement `cmd/nats-subscriber` component
  - [x] 5b.1.1 Listen to Hub Centralised DB `pg_notify` channel for infra status updates
  - [x] 5b.1.2 Map infra status (`provisioning`, `ready`, `failed`) → deployment status (`deploying`, `deployed`, `failed`)
  - [x] 5b.1.3 Update `agentregistry.deployments.status` via AgentRegistry API (PATCH /v0/deployments/{id})
- [x] 5b.2 Add project structure entry (new command/service)
- [x] 5b.3 Add deployment manifest for `nats-subscriber`

**CRITICAL - API Boundary Correction:**
- ❌ **NOT via PostgREST**: The `agentregistry` schema is NOT exposed via PostgREST (only `dashboard` schema is exposed)
- ✅ **Via AgentRegistry API**: Use `client.AgentRegistryClient.UpdateDeployment()` method
- ✅ **Environment Variable**: `AGENTREGISTRY_URL` (not `CONTROL_PLANE_POSTGREST_URL`)

**Manual Testing Checkpoint:**
- Deploy an agent via `deploy_agent`
- Poll `get_agent_status` until status reaches `ready`

**PAUSE: User must approve Phase 5b before proceeding to Phase 6**

---

## Phase 6: Update & Delete Operations

**Goal:** Implement update_agent and delete_agent MCP tools

### Tasks

- [x] 6.1 Implement update_agent method
  - [x] 6.1.1 Add UpdateAgent method to agent_service.go
  - [x] 6.1.2 Update agent in AgentRegistry (POST /v0/agents - upsert)
  - [x] 6.1.3 Check deployment status via AgentRegistry
  - [x] 6.1.4 If deployed: call AgentRegistry to update deployment (triggers Deployment Adapter)
  - [x] 6.1.5 If not deployed: return success immediately
  - [x] 6.1.6 Publish NATS event (`hub.platform.agent.updated`)

- [x] 6.2 Implement update_agent MCP tool
  - [x] 6.2.1 Create `cmd/mcp-server/tools/agents/update_agent.go`
  - [x] 6.2.2 Extract tenant context from JWT
  - [x] 6.2.3 Call agent service UpdateAgent method
  - [x] 6.2.4 Return MCP-formatted response (deployed: true/false)

- [x] 6.3 Implement delete_agent method
  - [x] 6.3.1 Add DeleteAgent method to agent_service.go
  - [x] 6.3.2 Check deployments via AgentRegistry
  - [x] 6.3.3 For each deployment: call AgentRegistry DELETE /v0/deployments/{id} (triggers Deployment Adapter)
  - [x] 6.3.4 Delete deployment records from AgentRegistry
  - [x] 6.3.5 Delete agent from AgentRegistry
  - [x] 6.3.6 Publish NATS event (hub.platform.agent.deleted)

- [x] 6.4 Implement delete_agent MCP tool
  - [x] 6.4.1 Create `cmd/mcp-server/tools/agents/delete_agent.go`
  - [x] 6.4.2 Extract tenant context from JWT
  - [x] 6.4.3 Call agent service DeleteAgent method
  - [x] 6.4.4 Return MCP-formatted response (deleted_deployments count)

- [x] 6.5 Register update and delete tools
  - [x] 6.5.1 Register update_agent tool in main.go
  - [x] 6.5.2 Register delete_agent tool in main.go

**Manual Testing Checkpoint:**
- Test update_agent on non-deployed agent (verify no redeployment)
- Test update_agent on deployed agent (verify redeployment triggered)
- Test delete_agent (verify CRD removed from Spoke cluster via `kubectl get agents`, agent deleted from AgentRegistry)

**PAUSE: User must approve Phase 6 before proceeding to Phase 8**

---

## Phase 8: Spoke Controller Integration

**Goal:** Implement Spoke Controller for status synchronization

### Tasks

- [x] 8.1 Implement Spoke Controller
  - [x] 8.1.1 Create `operators/spoke-controller/internal/controller/agent_controller.go`
  - [x] 8.1.2 Implement controller-runtime pattern (watch Agent CRDs)
  - [x] 8.1.3 Implement Reconcile method
  - [x] 8.1.4 Derive status from Deployment.status.availableReplicas
  - [x] 8.1.5 Map KEDA HTTP Add-on scale-to-zero to phase field (Idle/Running/Failed)
  - [x] 8.1.6 Add label validation (skip CRDs without tenant-id/agent-id/deployment-id labels)

- [x] 8.2 Implement Hub PostgREST client for Spoke Controller
  - [x] 8.2.1 Create `operators/spoke-controller/internal/client/hub_client.go`
  - [x] 8.2.2 Implement Hydra OAuth2 client_credentials flow
  - [x] 8.2.3 Implement POST /agent_deployments (write status to Hub)
  - [x] 8.2.4 Add retry logic with exponential backoff

- [x] 8.3 Implement status sync logic
  - [x] 8.3.1 Add syncToHub method to agent_controller.go
  - [x] 8.3.2 Authenticate with Hub using Hydra client_credentials
  - [x] 8.3.3 POST status to Hub PostgREST with Bearer token
  - [x] 8.3.4 Handle sync failures with retry

- [x] 8.4 Deploy Spoke Controller
  - [x] 8.4.1 Create Kubernetes manifests (Deployment, ServiceAccount, RBAC)
  - [x] 8.4.2 Create ConfigMap for spoke_cluster_id
  - [x] 8.4.3 Create Secret for Hub API credentials (Hydra client_id/secret)

**Manual Testing Checkpoint:**
- Deploy Spoke Controller to test Spoke cluster
- Deploy an agent via deploy_agent
- Verify Spoke Controller syncs status to Hub Centralised DB
- Verify Spoke Controller skips unlabelled CRDs (no errors in logs)
- Verify get_agent_status returns correct status from Hub
- Test KEDA HTTP Add-on scale-to-zero (verify phase changes to "Idle")

**PAUSE: User must approve Phase 8 before proceeding to Phase 9**

---

## Phase 9: Observability & Monitoring

**Goal:** Add metrics, logging, and tracing

### Tasks

- [x] 9.1 Add Prometheus metrics
  - [x] 9.1.1 Create `internal/agent-core/telemetry/metrics.go`
  - [x] 9.1.2 Add metric: agents_mcp_tool_duration_seconds (histogram)
  - [x] 9.1.3 Add metric: agents_deployments_total (gauge by status, phase)
  - [x] 9.1.4 Instrument all MCP tool handlers

- [x] 9.2 Add structured logging
  - [x] 9.2.1 Add log statements to all service methods
  - [x] 9.2.2 Include fields: tenant_id, agent_id, deployment_id, tool
  - [x] 9.2.3 Log errors with stack traces

- [x] 9.3 Add OpenTelemetry tracing
  - [x] 9.3.1 Add tracing to all MCP tool handlers
  - [x] 9.3.2 Add spans for AgentRegistry API calls
  - [x] 9.3.3 Add spans for Hub PostgREST queries
  - [x] 9.3.4 Add spans for GitOps commits

**Manual Testing Checkpoint:**
- Verify metrics exposed at /metrics endpoint
- Verify logs written to OpenSearch
- Verify traces exported to observability backend
- Test with Grafana dashboards

**PAUSE: User must approve Phase 9 before final deployment**

---

## Phase 10: Documentation & Deployment

**Goal:** Finalize documentation and deploy to production

### Tasks

- [ ] 10.1 Update deployment manifests
  - [ ] 10.1.1 Update `cmd/mcp-server/deployment.yaml` with agent-core config
  - [ ] 10.1.2 Add environment variables (AGENTREGISTRY_URL, HUB_POSTGREST_URL)
  - [ ] 10.1.3 Add resource limits (CPU, memory)

- [ ] 10.2 Create configuration documentation
  - [ ] 10.2.1 Document MCP server configuration
  - [ ] 10.2.2 Document Spoke Controller configuration
  - [ ] 10.2.3 Document database schema setup

- [ ] 10.3 Create user documentation
  - [ ] 10.3.1 Document MCP tool usage (create_agent, deploy_agent, etc.)
  - [ ] 10.3.2 Document error codes and troubleshooting
  - [ ] 10.3.3 Document deployment flow and status polling

**Final Manual Testing:**
- End-to-end test: create → deploy → status → update → delete
- Test all error scenarios (quota exceeded, unauthorized tool, deployment timeout)
- Test with multiple tenants (verify tenant isolation)
- Performance test (100 concurrent agent creations)

**PAUSE: User must approve final testing before production deployment**

---

## Notes

- No automated test suites required (manual testing only)
- Each phase must be tested and approved before proceeding
- Use MCP clients (Cursor, Goose, Claude Desktop) for manual testing
- Verify all changes against AgentRegistry OSS codebase (read-only)
- All provisioning logic must be in agent-core (not AgentRegistry)
