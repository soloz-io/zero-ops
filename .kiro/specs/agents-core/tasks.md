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

- [ ] 1.1 Create database migration for Control Plane Shared DB
  - [ ] 1.1.1 Create `agentregistry` schema with tenant isolation (B-01 resolution)
  - [ ] 1.1.2 Add `tenant_id` column to agent_definitions table
  - [ ] 1.1.3 Update UNIQUE constraint to (tenant_id, name, version)
  - [ ] 1.1.4 Create RLS policy: `tenant_isolation` using `app.tenant_id`
  - [ ] 1.1.5 Create `agents` schema for platform-specific extensions
  - [ ] 1.1.6 Create `available_models` table with tier-based authorization
  - [ ] 1.1.7 Create `authorized_tools` table with RLS policies
  - [ ] 1.1.8 Seed `available_models` with initial data (OpenAI, Anthropic models)

- [ ] 1.2 Create database migration for Hub Centralised DB
  - [ ] 1.2.1 Create `agent_deployments` table
  - [ ] 1.2.2 Add indexes for tenant_id, agent_id, deployment_id, status
  - [ ] 1.2.3 Create RLS policy for tenant isolation

- [ ] 1.3 Generate sqlc queries for platform-specific tables
  - [ ] 1.3.1 Create `internal/agent-core/database/queries.sql`
  - [ ] 1.3.2 Add queries for available_models (GetModelsByTier, ValidateModelAccess)
  - [ ] 1.3.3 Add queries for authorized_tools (GetAuthorizedTools, ValidateToolAccess)
  - [ ] 1.3.4 Run `sqlc generate`

- [ ] 1.4 Implement AgentRegistry HTTP client
  - [ ] 1.4.1 Create `internal/agent-core/client/agentregistry_client.go`
  - [ ] 1.4.2 Implement CreateAgent (POST /v0/agents)
  - [ ] 1.4.3 Implement GetAgent (GET /v0/agents/{name}/versions/{version})
  - [ ] 1.4.4 Implement ListAgents (GET /v0/agents)
  - [ ] 1.4.5 Implement DeleteAgent (DELETE /v0/agents/{name}/versions/{version})
  - [ ] 1.4.6 Implement CreateDeployment (POST /v0/deployments)
  - [ ] 1.4.7 Implement ListDeployments (GET /v0/deployments)
  - [ ] 1.4.8 Implement DeleteDeployment (DELETE /v0/deployments/{id})

- [ ] 1.5 Implement Hub PostgREST client
  - [ ] 1.5.1 Create `internal/agent-core/client/hub_client.go`
  - [ ] 1.5.2 Implement GetAgentDeployment (query agent_deployments table)
  - [ ] 1.5.3 Implement ListAgentDeployments (query with filters)

**Manual Testing Checkpoint:**
- Verify database migrations applied successfully
- Verify sqlc generated code compiles
- Test AgentRegistry client against local AgentRegistry instance
- Test Hub PostgREST client against Hub Centralised DB

**PAUSE: User must approve Phase 1 before proceeding to Phase 2**

---

## Phase 2: Platform-Specific Validators

**Goal:** Implement model and tool authorization validators

### Tasks

- [ ] 2.1 Implement model authorization validator
  - [ ] 2.1.1 Create `internal/agent-core/validators/model_validator.go`
  - [ ] 2.1.2 Implement ValidateModelAuthorization (check tier-based access)
  - [ ] 2.1.3 Return error codes: `unauthorized_model`, `invalid_provider`, `invalid_model`

- [ ] 2.2 Implement tool authorization validator
  - [ ] 2.2.1 Create `internal/agent-core/validators/tool_validator.go`
  - [ ] 2.2.2 Implement ValidateToolAuthorization (check tenant-specific permissions)
  - [ ] 2.2.3 Return error code: `unauthorized_tool`

**Manual Testing Checkpoint:**
- Test model validator with different tenant tiers (basic, standard, premium, enterprise)
- Test tool validator with authorized and unauthorized tools
- Verify error codes returned correctly

**PAUSE: User must approve Phase 2 before proceeding to Phase 3**

---

## Phase 3: Agent Service - Create & List Operations

**Goal:** Implement create_agent and list_agents MCP tools

### Tasks

- [ ] 3.1 Implement agent service
  - [ ] 3.1.1 Create `internal/agent-core/service/agent_service.go`
  - [ ] 3.1.2 Add service struct with dependencies (AgentRegistryClient, HubClient, ControlPlane, DB)
  - [ ] 3.1.3 Implement CreateAgent method (orchestration pattern)
  - [ ] 3.1.4 Implement ListAgents method (orchestration pattern)

- [ ] 3.2 Implement create_agent MCP tool
  - [ ] 3.2.1 Create `cmd/mcp-server/tools/agents/create_agent.go`
  - [ ] 3.2.2 Extract tenant context from JWT (tenant_id, tenant_tier)
  - [ ] 3.2.3 Set tenant context: `SET app.tenant_id = $1` before AgentRegistry calls
  - [ ] 3.2.4 Call model validator
  - [ ] 3.2.5 Call tool validator
  - [ ] 3.2.6 Call AgentRegistry CreateAgent API
  - [ ] 3.2.7 Publish NATS event: `hub.platform.agent.created` (H-08 resolution)
  - [ ] 3.2.8 Return MCP-formatted response

- [ ] 3.3 Implement list_agents MCP tool
  - [ ] 3.3.1 Create `cmd/mcp-server/tools/agents/list_agents.go`
  - [ ] 3.3.2 Call AgentRegistry ListAgents API
  - [ ] 3.3.3 Enrich with deployment status from Hub PostgREST
  - [ ] 3.3.4 Filter by status parameter (all, deployed, not_deployed)
  - [ ] 3.3.5 Return MCP-formatted response

- [ ] 3.4 Register MCP tools in server
  - [ ] 3.4.1 Update `cmd/mcp-server/main.go` to initialize agent service
  - [ ] 3.4.2 Register create_agent tool
  - [ ] 3.4.3 Register list_agents tool

**Manual Testing Checkpoint:**
- Test create_agent via MCP client (Cursor/Goose)
- Verify agent created in AgentRegistry
- Test list_agents via MCP client
- Verify deployment status enrichment works

**PAUSE: User must approve Phase 3 before proceeding to Phase 4**

---

## Phase 4: Deployment Service - CRD Generation & GitOps

**Goal:** Implement deploy_agent MCP tool with CRD generation and GitOps commit

### Tasks

- [ ] 4.1 Implement CRD generation
  - [ ] 4.1.1 Create `internal/agent-core/service/crd_generator.go`
  - [ ] 4.1.2 Implement GenerateAgentCRD (Kagent Agent CRD template)
  - [ ] 4.1.3 Add labels: tenant-id, agent-id, deployment-id
  - [ ] 4.1.4 Map agent config to Kagent spec (modelConfig, systemMessage, tools)

- [ ] 4.2 Implement deployment service
  - [ ] 4.2.1 Create `internal/agent-core/service/deployment_service.go`
  - [ ] 4.2.2 Add service struct with dependencies
  - [ ] 4.2.3 Implement DeployAgent method (orchestration pattern)

- [ ] 4.3 Implement deploy_agent MCP tool
  - [ ] 4.3.1 Create `cmd/mcp-server/tools/agents/deploy_agent.go`
  - [ ] 4.3.2 Extract tenant context from JWT (tenant_id, spoke_cluster_id) - B-03 resolution
  - [ ] 4.3.3 Fetch agent from AgentRegistry
  - [ ] 4.3.4 Create deployment record in AgentRegistry (status: "deploying")
  - [ ] 4.3.5 Generate Agent CRD with labels (tenant-id, agent-id, deployment-id)
  - [ ] 4.3.6 Commit CRD to GitOps repo via IProvisioner.CommitManifest
  - [ ] 4.3.7 Publish NATS event: `hub.platform.agent.deployed` (H-08 resolution)
  - [ ] 4.3.8 Return MCP response with status "deploying" (async pattern)
  - [ ] 4.3.5 Generate Agent CRD YAML
  - [ ] 4.3.6 Commit to GitOps repo via IProvisioner.CommitManifest()
  - [ ] 4.3.7 Publish NATS event (opensbt_agentDeployed)
  - [ ] 4.3.8 Return MCP-formatted response (status: "deploying", deployment_id, commit_sha)

- [ ] 4.4 Register deploy_agent tool
  - [ ] 4.4.1 Update `cmd/mcp-server/main.go` to initialize deployment service
  - [ ] 4.4.2 Register deploy_agent tool

**Manual Testing Checkpoint:**
- Test deploy_agent via MCP client
- Verify deployment record created in AgentRegistry
- Verify Agent CRD committed to GitOps repo
- Verify ArgoCD syncs CRD to Spoke cluster
- Verify Kagent Controller reconciles Agent CRD

**PAUSE: User must approve Phase 4 before proceeding to Phase 5**

---

## Phase 5: Status Service - Polling & Monitoring

**Goal:** Implement get_agent_status MCP tool for deployment status polling

### Tasks

- [ ] 5.1 Implement status service
  - [ ] 5.1.1 Create `internal/agent-core/service/status_service.go`
  - [ ] 5.1.2 Add service struct with dependencies
  - [ ] 5.1.3 Implement GetAgentStatus method (query Hub PostgREST)

- [ ] 5.2 Implement get_agent_status MCP tool
  - [ ] 5.2.1 Create `cmd/mcp-server/tools/agents/get_agent_status.go`
  - [ ] 5.2.2 Extract tenant context from JWT
  - [ ] 5.2.3 Query Hub Centralised DB via PostgREST
  - [ ] 5.2.4 Return deployment status (deploying, provisioning, ready, failed, cancelled)
  - [ ] 5.2.5 Return phase (Running, Idle, Failed)
  - [ ] 5.2.6 Return replicas, message, error details

- [ ] 5.3 Register get_agent_status tool
  - [ ] 5.3.1 Update `cmd/mcp-server/main.go` to initialize status service
  - [ ] 5.3.2 Register get_agent_status tool

**Manual Testing Checkpoint:**
- Deploy an agent via deploy_agent
- Poll get_agent_status until status reaches "ready"
- Verify status transitions: deploying → provisioning → ready
- Test with KEDA scale-to-zero (verify phase: "Idle")

**PAUSE: User must approve Phase 5 before proceeding to Phase 6**

---

## Phase 6: Update & Delete Operations

**Goal:** Implement update_agent and delete_agent MCP tools

### Tasks

- [ ] 6.1 Implement update_agent method
  - [ ] 6.1.1 Add UpdateAgent method to agent_service.go
  - [ ] 6.1.2 Update agent in AgentRegistry (POST /v0/agents - upsert)
  - [ ] 6.1.3 Check deployment status via AgentRegistry
  - [ ] 6.1.4 If deployed: generate updated CRD and commit to GitOps
  - [ ] 6.1.5 If not deployed: return success immediately
  - [ ] 6.1.6 Publish NATS event (opensbt_agentUpdated)

- [ ] 6.2 Implement update_agent MCP tool
  - [ ] 6.2.1 Create `cmd/mcp-server/tools/agents/update_agent.go`
  - [ ] 6.2.2 Extract tenant context from JWT
  - [ ] 6.2.3 Call agent service UpdateAgent method
  - [ ] 6.2.4 Return MCP-formatted response (deployed: true/false)

- [ ] 6.3 Implement delete_agent method
  - [ ] 6.3.1 Add DeleteAgent method to agent_service.go
  - [ ] 6.3.2 Check deployments via AgentRegistry
  - [ ] 6.3.3 For each deployment: commit CRD deletion to Git via IProvisioner.UpdateTenantResources(operation: "delete")
  - [ ] 6.3.4 Delete deployment records from AgentRegistry
  - [ ] 6.3.5 Delete agent from AgentRegistry
  - [ ] 6.3.6 Publish NATS event (hub.platform.agent.deleted)

- [ ] 6.4 Implement delete_agent MCP tool
  - [ ] 6.4.1 Create `cmd/mcp-server/tools/agents/delete_agent.go`
  - [ ] 6.4.2 Extract tenant context from JWT
  - [ ] 6.4.3 Call agent service DeleteAgent method
  - [ ] 6.4.4 Return MCP-formatted response (deleted_deployments count)

- [ ] 6.5 Register update and delete tools
  - [ ] 6.5.1 Register update_agent tool in main.go
  - [ ] 6.5.2 Register delete_agent tool in main.go

**Manual Testing Checkpoint:**
- Test update_agent on non-deployed agent (verify no redeployment)
- Test update_agent on deployed agent (verify redeployment triggered)
- Test delete_agent (verify CRD removed from GitOps, agent deleted from AgentRegistry)

**PAUSE: User must approve Phase 6 before proceeding to Phase 7**

---

## Phase 7: List Authorized Tools

**Goal:** Implement list_authorized_tools MCP tool

### Tasks

- [ ] 7.1 Implement list_authorized_tools method
  - [ ] 7.1.1 Add ListAuthorizedTools method to agent_service.go
  - [ ] 7.1.2 Query authorized_tools table from Control Plane Shared DB
  - [ ] 7.1.3 Filter by tenant_id and tier
  - [ ] 7.1.4 Filter by category parameter (if provided)

- [ ] 7.2 Implement list_authorized_tools MCP tool
  - [ ] 7.2.1 Create `cmd/mcp-server/tools/agents/list_authorized_tools.go`
  - [ ] 7.2.2 Extract tenant context from JWT
  - [ ] 7.2.3 Call agent service ListAuthorizedTools method
  - [ ] 7.2.4 Return MCP-formatted response

- [ ] 7.3 Register list_authorized_tools tool
  - [ ] 7.3.1 Register tool in main.go

**Manual Testing Checkpoint:**
- Test list_authorized_tools via MCP client
- Verify tools filtered by tenant tier
- Test category filter parameter

**PAUSE: User must approve Phase 7 before proceeding to Phase 8**

---

## Phase 8: Spoke Controller Integration

**Goal:** Implement Spoke Controller for status synchronization

### Tasks

- [ ] 8.1 Implement Spoke Controller
  - [ ] 8.1.1 Create `operators/spoke-controller/internal/controller/agent_controller.go`
  - [ ] 8.1.2 Implement controller-runtime pattern (watch Agent CRDs)
  - [ ] 8.1.3 Implement Reconcile method
  - [ ] 8.1.4 Derive status from Deployment.status.availableReplicas
  - [ ] 8.1.5 Map KEDA HTTP Add-on scale-to-zero to phase field (Idle/Running/Failed)
  - [ ] 8.1.6 Add label validation (skip CRDs without tenant-id/agent-id/deployment-id labels)

- [ ] 8.2 Implement Hub PostgREST client for Spoke Controller
  - [ ] 8.2.1 Create `operators/spoke-controller/internal/client/hub_client.go`
  - [ ] 8.2.2 Implement Hydra OAuth2 client_credentials flow
  - [ ] 8.2.3 Implement POST /agent_deployments (write status to Hub)
  - [ ] 8.2.4 Add retry logic with exponential backoff

- [ ] 8.3 Implement status sync logic
  - [ ] 8.3.1 Add syncToHub method to agent_controller.go
  - [ ] 8.3.2 Authenticate with Hub using Hydra client_credentials
  - [ ] 8.3.3 POST status to Hub PostgREST with Bearer token
  - [ ] 8.3.4 Handle sync failures with retry

- [ ] 8.4 Deploy Spoke Controller
  - [ ] 8.4.1 Create Kubernetes manifests (Deployment, ServiceAccount, RBAC)
  - [ ] 8.4.2 Create ConfigMap for spoke_cluster_id
  - [ ] 8.4.3 Create Secret for Hub API credentials (Hydra client_id/secret)

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

- [ ] 9.1 Add Prometheus metrics
  - [ ] 9.1.1 Create `internal/agent-core/telemetry/metrics.go`
  - [ ] 9.1.2 Add metric: agents_mcp_tool_duration_seconds (histogram)
  - [ ] 9.1.3 Add metric: agents_deployments_total (gauge by status, phase)
  - [ ] 9.1.4 Instrument all MCP tool handlers

- [ ] 9.2 Add structured logging
  - [ ] 9.2.1 Add log statements to all service methods
  - [ ] 9.2.2 Include fields: tenant_id, agent_id, deployment_id, tool
  - [ ] 9.2.3 Log errors with stack traces

- [ ] 9.3 Add OpenTelemetry tracing
  - [ ] 9.3.1 Add tracing to all MCP tool handlers
  - [ ] 9.3.2 Add spans for AgentRegistry API calls
  - [ ] 9.3.3 Add spans for Hub PostgREST queries
  - [ ] 9.3.4 Add spans for GitOps commits

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
