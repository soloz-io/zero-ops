# Agent Core Integration - Implementation Tasks

**Spec ID:** agent-core-integration  
**Status:** Ready for Implementation  
**Created:** 2026-03-26

---

## Implementation Phases

This implementation addresses the missing components identified in the agents-core pending review. Each phase must be completed, tested, and approved before proceeding to the next phase.

---

## Phase 1: MCP Server Entrypoint

**Goal:** Create the missing MCP JSON-RPC server executable

### Tasks

- [ ] 1.1 Create MCP server main entrypoint
  - [ ] 1.1.1 Create `cmd/mcp-server/main.go`
  - [ ] 1.1.2 Initialize opensbt telemetry libraries (tracing, metrics, logging)
  - [ ] 1.1.3 Initialize opensbt control plane with telemetry integration
  - [ ] 1.1.4 Initialize agent-core services with all dependencies
  - [ ] 1.1.5 Create JSON-RPC server instance
  - [ ] 1.1.6 Register all MCP tools with telemetry middleware

- [ ] 1.2 Implement telemetry middleware
  - [ ] 1.2.1 Create `withTelemetry` wrapper function
  - [ ] 1.2.2 Add trace span creation for each MCP tool call
  - [ ] 1.2.3 Add metrics recording (duration, success/error counts)
  - [ ] 1.2.4 Add structured logging (start, completion, errors)
  - [ ] 1.2.5 Include tenant context in all telemetry data

- [ ] 1.3 Implement tenant context middleware
  - [ ] 1.3.1 Create `tenantContextMiddleware` function
  - [ ] 1.3.2 Extract X-Auth-* headers from requests
  - [ ] 1.3.3 Add tenant context to request context
  - [ ] 1.3.4 Validate required headers are present

- [ ] 1.4 Create HTTP server configuration
  - [ ] 1.4.1 Configure HTTP server with proper timeouts
  - [ ] 1.4.2 Add health check endpoint (`/health`)
  - [ ] 1.4.3 Add metrics endpoint (`/metrics`) for VictoriaMetrics
  - [ ] 1.4.4 Add graceful shutdown handling
  - [ ] 1.4.5 Configure OTEL_EXPORTER_OTLP_ENDPOINT environment variable

**Manual Testing Checkpoint:**
- Build and run mcp-server binary
- Verify health check endpoint responds
- Verify metrics endpoint exposes Prometheus format
- Test MCP tool registration without errors
- Verify telemetry initialization

**PAUSE: User must approve Phase 1 before proceeding to Phase 2**

---

## Phase 2: Kubernetes Deployment Adapter

**Goal:** Implement direct Kubernetes API integration for Spoke cluster CRD application

### Tasks

- [ ] 2.1 Create Deployment Adapter structure
  - [ ] 2.1.1 Create `internal/agent-core/adapter/k8s_spoke_adapter.go`
  - [ ] 2.1.2 Define K8sSpokeAdapter struct with client cache
  - [ ] 2.1.3 Add telemetry integration (tracer, metrics, logger)
  - [ ] 2.1.4 Implement thread-safe client management with mutex

- [ ] 2.2 Implement Spoke client management (CRITICAL - CAPI Integration)
  - [ ] 2.2.1 Create `getSpokeClient` method with CAPI kubeconfig loading
  - [ ] 2.2.2 Read `<cluster-name>-kubeconfig` secrets from zero-ops-system namespace
  - [ ] 2.2.3 Implement client caching by spokeClusterID
  - [ ] 2.2.4 Add connection health checking and retry logic
  - [ ] 2.2.5 Handle kubeconfig rotation and credential refresh

- [ ] 2.3 Implement CRD generation
  - [ ] 2.3.1 Create `generateAgentCRD` method
  - [ ] 2.3.2 Map agent definition to Kagent Agent CRD spec
  - [ ] 2.3.3 Add required labels (tenant-id, agent-id, deployment-id)
  - [ ] 2.3.4 Include managed-by label for identification

- [ ] 2.4 Implement CRD application with auth error handling
  - [ ] 2.4.1 Create `ApplyCRD` method with Server-Side Apply
  - [ ] 2.4.2 Use dynamic Kubernetes client for CRD operations
  - [ ] 2.4.3 Handle authentication errors and client cache invalidation (CRITICAL)
  - [ ] 2.4.4 Implement retry logic for kubeconfig rotation scenarios
  - [ ] 2.4.5 Add comprehensive error logging and metrics

- [ ] 2.5 Add kubeconfig rotation handling (CRITICAL)
  - [ ] 2.5.1 Implement `isAuthError` helper function to detect auth failures
  - [ ] 2.5.2 Create `invalidateClient` method to flush cache on auth errors
  - [ ] 2.5.3 Add retry logic with fresh client after cache invalidation
  - [ ] 2.5.4 Handle auth errors in both ApplyCRD and DeleteCRD methods

- [ ] 2.6 Add custom metrics for Deployment Adapter
  - [ ] 2.6.1 Create spoke connection metrics
  - [ ] 2.6.2 Create CRD operation metrics (apply success/failure)
  - [ ] 2.6.3 Create client cache metrics
  - [ ] 2.6.4 Instrument all operations with metrics

**Manual Testing Checkpoint:**
- Test CRD generation with sample agent definition
- Test Spoke client creation with mock kubeconfig
- Verify Server-Side Apply logic with test cluster
- Test error handling and retry mechanisms
- Verify metrics are recorded correctly

**PAUSE: User must approve Phase 2 before proceeding to Phase 3**

---

## Phase 3: Spoke Controller Implementation

**Goal:** Create controller to watch Agent CRDs and sync status to Hub

### Tasks

- [ ] 3.1 Create Spoke Controller structure
  - [ ] 3.1.1 Create `operators/spoke-controller/internal/controller/agent_controller.go`
  - [ ] 3.1.2 Define AgentStatusController struct with controller-runtime
  - [ ] 3.1.3 Add Hub PostgREST client dependency
  - [ ] 3.1.4 Add telemetry integration (tracer, metrics, logger)

- [ ] 3.2 Implement controller-runtime pattern
  - [ ] 3.2.1 Create `Reconcile` method with proper error handling
  - [ ] 3.2.2 Implement `SetupWithManager` for controller registration
  - [ ] 3.2.3 Add RBAC markers for Agent CRDs and Deployments
  - [ ] 3.2.4 Configure watch for Deployment changes

- [ ] 3.3 Implement status derivation logic
  - [ ] 3.3.1 Create `deriveInfraStatus` method
  - [ ] 3.3.2 Map Deployment.status.availableReplicas to infrastructure status
  - [ ] 3.3.3 Map KEDA scale-to-zero states to phase field
  - [ ] 3.3.4 Handle deployment failure conditions

- [ ] 3.4 Implement Hub synchronization
  - [ ] 3.4.1 Create `syncToHub` method
  - [ ] 3.4.2 Validate required labels before syncing
  - [ ] 3.4.3 Skip unlabelled CRDs (platform agents)
  - [ ] 3.4.4 POST status to Hub PostgREST with authentication

- [ ] 3.5 Create Hub PostgREST client
  - [ ] 3.5.1 Create `operators/spoke-controller/internal/client/hub_client.go`
  - [ ] 3.5.2 Implement Hydra OAuth2 client_credentials flow
  - [ ] 3.5.3 Implement `UpsertAgentInfraStatus` method
  - [ ] 3.5.4 Add retry logic with exponential backoff

**Manual Testing Checkpoint:**
- Deploy Spoke Controller to test cluster
- Create test Agent CRD with required labels
- Verify controller watches and reconciles changes
- Test status derivation with different deployment states
- Verify Hub PostgREST integration works

**PAUSE: User must approve Phase 3 before proceeding to Phase 4**

---

## Phase 4: Hub Event Router (CRITICAL - pg_notify Bridge)

**Goal:** Create bridge service between PostgreSQL pg_notify and NATS

### Tasks

- [ ] 4.1 Create Hub Event Router structure
  - [ ] 4.1.1 Create `cmd/hub-event-router/main.go`
  - [ ] 4.1.2 Define HubEventRouter struct with pg_notify listener
  - [ ] 4.1.3 Add NATS connection for event publishing
  - [ ] 4.1.4 Add telemetry integration (tracer, metrics, logger)

- [ ] 4.2 Implement PostgreSQL pg_notify listener
  - [ ] 4.2.1 Create `pq.NewListener` connection to Hub Centralised DB
  - [ ] 4.2.2 Listen to `hub.platform.agent.infra_status` channel
  - [ ] 4.2.3 Handle connection failures and reconnection
  - [ ] 4.2.4 Implement graceful shutdown on context cancellation

- [ ] 4.3 Implement NATS event publishing
  - [ ] 4.3.1 Create `handleNotification` method for pg_notify events
  - [ ] 4.3.2 Parse JSON payload from pg_notify extra field
  - [ ] 4.3.3 Publish events to NATS `hub.platform.agent.infra_status` subject
  - [ ] 4.3.4 Add comprehensive error handling and logging

- [ ] 4.4 Service reliability and monitoring
  - [ ] 4.4.1 Configure health checks and readiness probes
  - [ ] 4.4.2 Add metrics for event processing success/failure
  - [ ] 4.4.3 Configure service account and RBAC permissions
  - [ ] 4.4.4 Handle both database and NATS connection failures

**Manual Testing Checkpoint:**
- Test pg_notify listener with database trigger events
- Verify NATS publishing works correctly
- Test connection recovery for both DB and NATS
- Verify event payload parsing and forwarding
- Test graceful shutdown behavior

**PAUSE: User must approve Phase 4 before proceeding to Phase 5**

---

## Phase 5: NATS Status Subscriber

**Goal:** Subscribe to NATS events and update AgentRegistry

### Tasks

- [ ] 5.1 Create NATS Subscriber structure
  - [ ] 5.1.1 Create `cmd/nats-subscriber/main.go`
  - [ ] 5.1.2 Define NATSStatusSubscriber struct
  - [ ] 5.1.3 Add AgentRegistry client dependency
  - [ ] 5.1.4 Add telemetry integration (tracer, metrics, logger)

- [ ] 5.2 Implement NATS subscription
  - [ ] 5.2.1 Create `Start` method with NATS connection
  - [ ] 5.2.2 Subscribe to `hub.platform.agent.infra_status` subject (from Hub Event Router)
  - [ ] 5.2.3 Handle connection failures and reconnection
  - [ ] 5.2.4 Implement graceful shutdown on context cancellation

- [ ] 5.3 Implement status update handling
  - [ ] 5.3.1 Create `handleStatusUpdate` method
  - [ ] 5.3.2 Unmarshal NATS message to InfraStatusUpdate struct
  - [ ] 5.3.3 Map infrastructure status to deployment status
  - [ ] 5.3.4 Add comprehensive error handling and logging

- [ ] 5.4 Implement AgentRegistry integration
  - [ ] 5.4.1 Create `updateAgentRegistryDeployment` method
  - [ ] 5.4.2 Use AgentRegistry API (NOT direct DB access)
  - [ ] 5.4.3 Handle API errors and retries
  - [ ] 5.4.4 Add metrics for processing success/failure

- [ ] 5.5 Add status mapping logic
  - [ ] 5.5.1 Create `mapInfraStatusToDeploymentStatus` function
  - [ ] 5.5.2 Map "provisioning" → "deploying"
  - [ ] 5.5.3 Map "ready" → "deployed"
  - [ ] 5.5.4 Map "failed" → "failed"

**Manual Testing Checkpoint:**
- Test NATS subscription with events from Hub Event Router
- Verify status mapping logic with different inputs
- Test AgentRegistry API integration
- Verify error handling and retry mechanisms
- Test graceful shutdown behavior

**PAUSE: User must approve Phase 5 before proceeding to Phase 6**

---

## Phase 6: Telemetry Integration

**Goal:** Integrate with opensbt telemetry libraries and add custom metrics

### Tasks

- [ ] 6.1 Update agent-core telemetry
  - [ ] 6.1.1 Update `internal/agent-core/telemetry/metrics.go`
  - [ ] 6.1.2 Add Spoke connection metrics
  - [ ] 6.1.3 Add CRD operation metrics
  - [ ] 6.1.4 Add NATS processing metrics
  - [ ] 6.1.5 Add Hub Event Router metrics

- [ ] 6.2 Integrate opensbt tracing
  - [ ] 6.2.1 Update all components to use opensbt tracing libraries
  - [ ] 6.2.2 Ensure trace propagation across components
  - [ ] 6.2.3 Add tenant context to all spans
  - [ ] 6.2.4 Record errors and attributes properly

- [ ] 6.3 Integrate opensbt logging
  - [ ] 6.3.1 Update all components to use opensbt logging libraries
  - [ ] 6.3.2 Ensure structured JSON format
  - [ ] 6.3.3 Include tenant context in all log entries
  - [ ] 6.3.4 Add correlation IDs for request tracking

- [ ] 6.4 Add missing telemetry helper
  - [ ] 6.4.1 Create `getTenantIDFromContext` helper function
  - [ ] 6.4.2 Add to `internal/agent-core/telemetry/logging.go`
  - [ ] 6.4.3 Use in all telemetry components
  - [ ] 6.4.4 Handle missing context gracefully

**Manual Testing Checkpoint:**
- Verify metrics exposed at /metrics endpoint
- Test trace propagation across components
- Verify structured logs include tenant context
- Test telemetry with different tenant scenarios

**PAUSE: User must approve Phase 6 before proceeding to Phase 7**

---

## Phase 7: Cluster Deployment Manifests

**Goal:** Create Kubernetes deployment manifests for all components

### Tasks

- [ ] 7.1 Create MCP Server manifests
  - [ ] 7.1.1 Create `manifests/platform-core/mcp-server/deployment.yaml`
  - [ ] 7.1.2 Create `manifests/platform-core/mcp-server/service.yaml`
  - [ ] 7.1.3 Create `manifests/platform-core/mcp-server/servicemonitor.yaml` (VictoriaMetrics)
  - [ ] 7.1.4 Create `manifests/platform-core/mcp-server/configmap.yaml`
  - [ ] 7.1.5 Create `manifests/platform-core/mcp-server/rbac.yaml` (CAPI kubeconfig access)

- [ ] 7.2 Create Hub Event Router manifests
  - [ ] 7.2.1 Create `manifests/platform-core/hub-event-router/deployment.yaml`
  - [ ] 7.2.2 Create `manifests/platform-core/hub-event-router/configmap.yaml`
  - [ ] 7.2.3 Create `manifests/platform-core/hub-event-router/rbac.yaml`

- [ ] 7.3 Create NATS Subscriber manifests
  - [ ] 7.3.1 Create `manifests/platform-core/nats-subscriber/deployment.yaml`
  - [ ] 7.3.2 Create `manifests/platform-core/nats-subscriber/configmap.yaml`
  - [ ] 7.3.3 Create `manifests/platform-core/nats-subscriber/rbac.yaml`

- [ ] 7.4 Create Spoke Controller manifests
  - [ ] 7.4.1 Create `manifests/platform-core/spoke-controller/deployment.yaml`
  - [ ] 7.4.2 Create `manifests/platform-core/spoke-controller/service-account.yaml`
  - [ ] 7.4.3 Create `manifests/platform-core/spoke-controller/cluster-role.yaml`
  - [ ] 7.4.4 Create `manifests/platform-core/spoke-controller/cluster-role-binding.yaml`
  - [ ] 7.4.5 Create `manifests/platform-core/spoke-controller/configmap.yaml`

- [ ] 7.5 Configure environment variables
  - [ ] 7.5.1 Add OTEL_EXPORTER_OTLP_ENDPOINT for tracing
  - [ ] 7.5.2 Add AGENT_REGISTRY_URL for API access
  - [ ] 7.5.3 Add HUB_POSTGREST_URL for status queries
  - [ ] 7.5.4 Add NATS_URL for event subscription

- [ ] 7.6 Configure volume mounts
  - [ ] 7.6.1 Add spoke-kubeconfigs secret mount for MCP server
  - [ ] 7.6.2 Add database credentials for Hub Event Router
  - [ ] 7.6.3 Add Hub API credentials for Spoke controller

**Manual Testing Checkpoint:**
- Apply manifests to test cluster
- Verify all pods start successfully
- Test service discovery and networking
- Verify RBAC permissions work correctly
- Test secret and configmap mounting

**PAUSE: User must approve Phase 7 before proceeding to Phase 8**

---

## Phase 8: Integration Testing

**Goal:** End-to-end testing of complete integration with Hub Event Router

### Tasks

- [ ] 8.1 Deploy all components
  - [ ] 8.1.1 Deploy MCP server to Hub cluster
  - [ ] 8.1.2 Deploy Hub Event Router to Hub cluster
  - [ ] 8.1.3 Deploy NATS subscriber to Hub cluster
  - [ ] 8.1.4 Deploy Spoke controller to test Spoke cluster
  - [ ] 8.1.5 Verify all components are healthy

- [ ] 8.2 Test MCP tool flow
  - [ ] 8.2.1 Test `create_agent` via MCP client
  - [ ] 8.2.2 Verify agent created in AgentRegistry
  - [ ] 8.2.3 Test `deploy_agent` via MCP client
  - [ ] 8.2.4 Verify CRD applied to Spoke cluster

- [ ] 8.3 Test status synchronization (CORRECTED - Full Chain)
  - [ ] 8.3.1 Verify Kagent Controller creates Pod
  - [ ] 8.3.2 Verify Spoke Controller detects status change
  - [ ] 8.3.3 Verify status written to Hub PostgREST
  - [ ] 8.3.4 Verify Hub DB pg_notify trigger fires
  - [ ] 8.3.5 Verify Hub Event Router publishes to NATS
  - [ ] 8.3.6 Verify NATS subscriber updates AgentRegistry

- [ ] 8.4 Test complete flow
  - [ ] 8.4.1 Test `get_agent_status` returns correct status
  - [ ] 8.4.2 Test status transitions: deploying → deployed
  - [ ] 8.4.3 Test KEDA scale-to-zero (phase: Idle)
  - [ ] 8.4.4 Test error scenarios and recovery

- [ ] 8.5 Test kubeconfig rotation handling
  - [ ] 8.5.1 Simulate kubeconfig rotation in CAPI secret
  - [ ] 8.5.2 Verify Deployment Adapter detects auth errors
  - [ ] 8.5.3 Verify client cache invalidation works
  - [ ] 8.5.4 Verify retry with fresh client succeeds

- [ ] 8.6 Validate telemetry
  - [ ] 8.6.1 Verify metrics in VictoriaMetrics
  - [ ] 8.6.2 Verify traces in tracing backend
  - [ ] 8.6.3 Verify logs in OpenSearch
  - [ ] 8.6.4 Test Grafana dashboards

**Final Manual Testing:**
- End-to-end: create → deploy → status → update → delete
- Test tenant isolation with multiple tenants
- Test error scenarios (network failures, API errors, auth failures)
- Performance test with multiple concurrent deployments
- Verify observability data quality
- Test Hub Event Router failure scenarios

**PAUSE: User must approve final testing before production deployment**

---

## Notes

- No automated test suites required (manual testing only)
- Each phase must be tested and approved before proceeding
- Focus on integration points and error handling
- Verify telemetry data quality at each phase
- Test with real Kubernetes clusters, not mocks
