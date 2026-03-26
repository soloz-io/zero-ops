# Platform Core Services - Implementation Tasks

**Spec ID:** platform-core-services  
**Status:** Ready for Implementation  
**Created:** 2026-03-26

---

## Implementation Phases

This implementation provides Day 0 core platform services required for agent-core functionality. Each phase must be completed, tested, and approved before proceeding to the next phase.

**CRITICAL:** These services are strictly required for agent-core integration. Without them, MCP tools will fail.

---

## Phase 1: Database Schema Extensions

**Goal:** Extend existing CNPG clusters with required schemas and routing

### Tasks

- [ ] 1.1 Extend Control Plane Shared DB
  - [ ] 1.1.1 Add `agentregistry` schema to existing `control_plane` database
  - [ ] 1.1.2 Create agent definitions and deployments tables
  - [ ] 1.1.3 Configure RLS policies for tenant isolation
  - [ ] 1.1.4 Create `control-plane-db-credentials` secret with proper routing

- [ ] 1.2 Extend Hub Centralised DB
  - [ ] 1.2.1 Add `agent_infra_status` table to existing `hub` database
  - [ ] 1.2.2 Create pg_notify trigger for NATS integration
  - [ ] 1.2.3 Configure RLS policies for cross-cluster access
  - [ ] 1.2.4 Create `hub-db-credentials` secret with proper routing

- [ ] 1.3 Database connection validation
  - [ ] 1.3.1 Verify `control-plane-db-credentials` routes to `control_plane` database
  - [ ] 1.3.2 Verify `hub-db-credentials` routes to `hub` database
  - [ ] 1.3.3 Test RLS policies with sample tenant data
  - [ ] 1.3.4 Validate pg_notify trigger functionality

**Manual Testing Checkpoint:**
- Connect to both databases using respective credentials
- Verify schema separation and table access
- Test RLS policies with different tenant contexts
- Confirm pg_notify trigger fires on status updates

**PAUSE: User must approve Phase 1 before proceeding to Phase 2**

---

## Phase 2: NATS Messaging Infrastructure

**Goal:** Deploy NATS cluster with JetStream for event-driven communication

### Tasks

- [ ] 2.1 Deploy NATS cluster
  - [ ] 2.1.1 Create `manifests/platform-core-services/nats/cluster.yaml`
  - [ ] 2.1.2 Configure JetStream with persistent storage
  - [ ] 2.1.3 Set up required subjects for agent lifecycle
  - [ ] 2.1.4 Configure ACLs and subject permissions

- [ ] 2.2 Configure event subjects
  - [ ] 2.2.1 Create `hub.platform.agent.created` subject
  - [ ] 2.2.2 Create `hub.platform.agent.deployed` subject
  - [ ] 2.2.3 Create `hub.platform.agent.updated` subject
  - [ ] 2.2.4 Create `hub.platform.agent.deleted` subject
  - [ ] 2.2.5 Create `hub.platform.agent.infra_status` subject (CRITICAL for status sync)

- [ ] 2.3 Service configuration
  - [ ] 2.3.1 Expose NATS at `nats.zero-ops-system.svc.cluster.local:4222`
  - [ ] 2.3.2 Configure health checks and monitoring
  - [ ] 2.3.3 Set up service accounts for client authentication
  - [ ] 2.3.4 Configure network policies for secure access

**Manual Testing Checkpoint:**
- Test NATS connectivity from within cluster
- Publish and subscribe to test subjects
- Verify JetStream persistence across pod restarts
- Test subject permissions and ACLs

**PAUSE: User must approve Phase 2 before proceeding to Phase 3**

---

## Phase 3: VictoriaMetrics Observability Stack

**Goal:** Deploy metrics storage and collection infrastructure

### Tasks

- [ ] 3.1 Deploy VictoriaMetrics cluster
  - [ ] 3.1.1 Create `manifests/platform-core-services/victoriametrics/cluster.yaml`
  - [ ] 3.1.2 Configure persistent storage for metrics data
  - [ ] 3.1.3 Set up PromQL-compatible query API
  - [ ] 3.1.4 Configure retention policies and storage limits

- [ ] 3.2 Deploy Prometheus Operator
  - [ ] 3.2.1 Install Prometheus Operator for ServiceMonitor CRDs
  - [ ] 3.2.2 Configure ServiceMonitor discovery rules
  - [ ] 3.2.3 Set up metric scraping configuration
  - [ ] 3.2.4 Configure alerting rules (basic platform alerts)

- [ ] 3.3 Deploy Grafana Alloy
  - [ ] 3.3.1 Create `manifests/platform-core-services/grafana-alloy/deployment.yaml`
  - [ ] 3.3.2 Configure metrics collection from all namespaces
  - [ ] 3.3.3 Set up remote_write to VictoriaMetrics
  - [ ] 3.3.4 Configure service discovery for dynamic targets

- [ ] 3.4 Cross-cluster metrics access (CRITICAL for KEDA)
  - [ ] 3.4.1 Expose VictoriaMetrics externally at `victoriametrics.hub.zero-ops.io`
  - [ ] 3.4.2 Configure TLS and authentication for spoke cluster access
  - [ ] 3.4.3 Create service accounts for cross-cluster queries
  - [ ] 3.4.4 Test KEDA integration with Hub metrics endpoint

**Manual Testing Checkpoint:**
- Query metrics via PromQL API
- Verify ServiceMonitor discovery works
- Test cross-cluster metrics access from spoke
- Confirm KEDA can query Hub VictoriaMetrics

**PAUSE: User must approve Phase 3 before proceeding to Phase 4**

---

## Phase 4: AgentRegistry OSS Service

**Goal:** Deploy CRUD-only agent registry service

### Tasks

- [ ] 4.1 Create AgentRegistry deployment
  - [ ] 4.1.1 Create `manifests/platform-core-services/agentregistry/deployment.yaml`
  - [ ] 4.1.2 Configure connection to Control Plane Shared DB (CRITICAL routing)
  - [ ] 4.1.3 Set up service at `agentregistry.platform-agentregistry.svc.cluster.local:8080`
  - [ ] 4.1.4 Configure health checks and readiness probes

- [ ] 4.2 Configure database access
  - [ ] 4.2.1 Use `control-plane-db-credentials` secret (routes to control_plane DB)
  - [ ] 4.2.2 Configure RLS context for tenant isolation
  - [ ] 4.2.3 Set up connection pooling and timeouts
  - [ ] 4.2.4 Configure proper database schema access

- [ ] 4.3 API endpoint configuration
  - [ ] 4.3.1 Implement `/v0/agents` CRUD endpoints
  - [ ] 4.3.2 Implement `/v0/deployments` CRUD endpoints
  - [ ] 4.3.3 Configure tenant-based filtering via RLS
  - [ ] 4.3.4 Set up API documentation and health endpoints

- [ ] 4.4 Security and monitoring
  - [ ] 4.4.1 Configure service account and RBAC
  - [ ] 4.4.2 Set up network policies for secure access
  - [ ] 4.4.3 Add metrics endpoint for VictoriaMetrics scraping
  - [ ] 4.4.4 Configure structured logging

**Manual Testing Checkpoint:**
- Test all CRUD operations via API
- Verify tenant isolation works correctly
- Test database connection and RLS policies
- Confirm metrics are exposed and scraped

**PAUSE: User must approve Phase 4 before proceeding to Phase 5**

---

## Phase 5: Hub PostgREST Configuration

**Goal:** Deploy REST API for Hub Centralised DB access

### Tasks

- [ ] 5.1 Deploy Hub PostgREST
  - [ ] 5.1.1 Create `manifests/platform-core-services/hub-postgrest/deployment.yaml`
  - [ ] 5.1.2 Configure connection to Hub Centralised DB (CRITICAL routing)
  - [ ] 5.1.3 Set up internal service at `hub-postgrest.zero-ops-system.svc.cluster.local:3000`
  - [ ] 5.1.4 Configure external ingress at `postgrest.hub.zero-ops.io`

- [ ] 5.2 Configure database access
  - [ ] 5.2.1 Use `hub-db-credentials` secret (routes to hub DB)
  - [ ] 5.2.2 Expose `agent_infra_status` table with proper RLS
  - [ ] 5.2.3 Configure JWT authentication with Ory Hydra
  - [ ] 5.2.4 Set up CORS and security headers

- [ ] 5.3 Authentication integration
  - [ ] 5.3.1 Configure JWT validation with Hydra public keys
  - [ ] 5.3.2 Extract tenant context from JWT claims
  - [ ] 5.3.3 Set up RLS context from JWT tenant_id
  - [ ] 5.3.4 Configure OAuth2 client_credentials flow for spoke controllers

- [ ] 5.4 API security and monitoring
  - [ ] 5.4.1 Configure TLS termination and certificates
  - [ ] 5.4.2 Set up rate limiting and request validation
  - [ ] 5.4.3 Add metrics and health endpoints
  - [ ] 5.4.4 Configure audit logging for API access

**Manual Testing Checkpoint:**
- Test authenticated API access with JWT tokens
- Verify RLS policies filter data by tenant
- Test spoke controller authentication flow
- Confirm external ingress works with TLS

**PAUSE: User must approve Phase 5 before proceeding to Phase 6**

---

## Phase 6: NATS Status Subscriber

**Goal:** Bridge NATS events to AgentRegistry API updates

### Tasks

- [ ] 6.1 Create NATS Status Subscriber
  - [ ] 6.1.1 Create `manifests/platform-core-services/nats-subscriber/deployment.yaml`
  - [ ] 6.1.2 Configure NATS connection to `nats.zero-ops-system.svc.cluster.local:4222`
  - [ ] 6.1.3 Subscribe to `hub.platform.agent.infra_status` subject
  - [ ] 6.1.4 Configure AgentRegistry API client connection

- [ ] 6.2 Implement status mapping logic
  - [ ] 6.2.1 Map "provisioning" infrastructure status to "deploying" deployment status
  - [ ] 6.2.2 Map "ready" infrastructure status to "deployed" deployment status
  - [ ] 6.2.3 Map "failed" infrastructure status to "failed" deployment status
  - [ ] 6.2.4 Handle unknown status gracefully with logging

- [ ] 6.3 Configure API integration
  - [ ] 6.3.1 Use AgentRegistry API (NOT direct database access)
  - [ ] 6.3.2 Implement retry logic with exponential backoff
  - [ ] 6.3.3 Handle API errors and connection failures
  - [ ] 6.3.4 Add comprehensive error logging and metrics

- [ ] 6.4 Service reliability
  - [ ] 6.4.1 Configure graceful shutdown on context cancellation
  - [ ] 6.4.2 Implement NATS connection recovery and reconnection
  - [ ] 6.4.3 Add health checks and readiness probes
  - [ ] 6.4.4 Configure service account and RBAC permissions

**Manual Testing Checkpoint:**
- Test NATS subscription and message processing
- Verify status mapping logic with different inputs
- Test AgentRegistry API integration and error handling
- Confirm graceful shutdown and reconnection behavior

**PAUSE: User must approve Phase 6 before proceeding to Phase 7**

---

## Phase 7: OpenSearch Log Aggregation

**Goal:** Deploy centralized log storage and search infrastructure

### Tasks

- [ ] 7.1 Deploy OpenSearch cluster
  - [ ] 7.1.1 Create `manifests/platform-core-services/opensearch/cluster.yaml`
  - [ ] 7.1.2 Configure 3-node cluster for high availability
  - [ ] 7.1.3 Set up persistent storage for log data
  - [ ] 7.1.4 Configure index templates for structured logs

- [ ] 7.2 Configure log collection
  - [ ] 7.2.1 Update Grafana Alloy for log collection from all namespaces
  - [ ] 7.2.2 Set up JSON log parsing and field extraction
  - [ ] 7.2.3 Configure log forwarding to OpenSearch
  - [ ] 7.2.4 Set up log retention policies and index rotation

- [ ] 7.3 Service configuration
  - [ ] 7.3.1 Expose OpenSearch at `opensearch.zero-ops-system.svc.cluster.local:9200`
  - [ ] 7.3.2 Deploy OpenSearch Dashboards for log visualization
  - [ ] 7.3.3 Configure authentication and access control
  - [ ] 7.3.4 Set up index patterns and basic dashboards

- [ ] 7.4 Integration and monitoring
  - [ ] 7.4.1 Configure trace ID correlation with logs
  - [ ] 7.4.2 Set up log-based alerting rules
  - [ ] 7.4.3 Add cluster health monitoring
  - [ ] 7.4.4 Configure backup and disaster recovery

**Manual Testing Checkpoint:**
- Verify logs are collected and indexed
- Test log search and filtering functionality
- Confirm trace ID correlation works
- Test OpenSearch Dashboards access

**PAUSE: User must approve Phase 7 before proceeding to Phase 8**

---

## Phase 8: Tempo Distributed Tracing

**Goal:** Deploy distributed tracing infrastructure

### Tasks

- [ ] 8.1 Deploy Tempo
  - [ ] 8.1.1 Create `manifests/platform-core-services/tempo/deployment.yaml`
  - [ ] 8.1.2 Configure S3-compatible storage (Hetzner S3) for traces
  - [ ] 8.1.3 Set up OTLP endpoint for trace ingestion
  - [ ] 8.1.4 Configure trace retention policies

- [ ] 8.2 Configure trace collection
  - [ ] 8.2.1 Expose OTLP endpoint at `tempo.zero-ops-system.svc.cluster.local:4318`
  - [ ] 8.2.2 Set up HTTP API at `tempo.zero-ops-system.svc.cluster.local:3200`
  - [ ] 8.2.3 Configure trace sampling and rate limiting
  - [ ] 8.2.4 Set up service discovery for trace sources

- [ ] 8.3 Integration configuration
  - [ ] 8.3.1 Configure Grafana integration for trace visualization
  - [ ] 8.3.2 Set up trace-to-log correlation
  - [ ] 8.3.3 Configure trace-to-metrics correlation
  - [ ] 8.3.4 Add trace-based alerting rules

- [ ] 8.4 Service reliability
  - [ ] 8.4.1 Configure high availability and load balancing
  - [ ] 8.4.2 Set up monitoring and health checks
  - [ ] 8.4.3 Configure backup and data retention
  - [ ] 8.4.4 Test trace ingestion and query performance

**Manual Testing Checkpoint:**
- Test trace ingestion via OTLP endpoint
- Verify trace storage and retrieval
- Test trace correlation with logs and metrics
- Confirm Grafana integration works

**PAUSE: User must approve Phase 8 before proceeding to Phase 9**

---

## Phase 9: GitOps Spoke Provisioning (CORRECTED)

**Goal:** Implement GitOps-based spoke cluster provisioning

### Tasks

- [ ] 9.1 Update Hub CLI for GitOps (CRITICAL CORRECTION)
  - [ ] 9.1.1 Remove direct Kubernetes API calls from `cmd/hub/spoke.go`
  - [ ] 9.1.2 Implement GitOps fleet repository integration
  - [ ] 9.1.3 Generate CAPI Cluster and ClusterClass manifests
  - [ ] 9.1.4 Commit manifests to fleet repository instead of direct K8s API

- [ ] 9.2 Configure GitOps fleet repository
  - [ ] 9.2.1 Set up dedicated Git repository for spoke cluster manifests
  - [ ] 9.2.2 Configure ArgoCD Application to sync from fleet repository
  - [ ] 9.2.3 Set up proper sync waves for cluster provisioning order
  - [ ] 9.2.4 Configure automated sync and self-healing

- [ ] 9.3 CAPI integration
  - [ ] 9.3.1 Create ClusterClass definitions for pool and silo spoke types
  - [ ] 9.3.2 Configure automatic kubeconfig secret generation
  - [ ] 9.3.3 Set up RBAC for CAPI cluster management
  - [ ] 9.3.4 Configure Hetzner provider integration

- [ ] 9.4 Spoke Controller deployment
  - [ ] 9.4.1 Add Spoke Controller manifests to GitOps fleet repository
  - [ ] 9.4.2 Configure automatic deployment to new spoke clusters
  - [ ] 9.4.3 Set up cross-cluster RBAC for Hub communication
  - [ ] 9.4.4 Configure OAuth2 credentials for Hub PostgREST access

**Manual Testing Checkpoint:**
- Test GitOps spoke cluster creation (no direct K8s API)
- Verify manifests are committed to fleet repository
- Confirm ArgoCD syncs and provisions clusters
- Test Spoke Controller deployment and Hub communication

**PAUSE: User must approve Phase 9 before proceeding to Phase 10**

---

## Phase 10: Integration Testing and Validation

**Goal:** End-to-end testing of all platform core services

### Tasks

- [ ] 10.1 Service health validation
  - [ ] 10.1.1 Verify all pods are running and healthy
  - [ ] 10.1.2 Test all service endpoints and APIs
  - [ ] 10.1.3 Validate database connections and routing
  - [ ] 10.1.4 Confirm network policies and security

- [ ] 10.2 Status synchronization testing
  - [ ] 10.2.1 Test Hub PostgREST → NATS → AgentRegistry flow
  - [ ] 10.2.2 Verify pg_notify triggers publish to NATS
  - [ ] 10.2.3 Test NATS subscriber processes events correctly
  - [ ] 10.2.4 Validate AgentRegistry receives status updates

- [ ] 10.3 Cross-cluster integration
  - [ ] 10.3.1 Test spoke cluster provisioning via GitOps
  - [ ] 10.3.2 Verify CAPI kubeconfig secret generation
  - [ ] 10.3.3 Test cross-cluster metrics access for KEDA
  - [ ] 10.3.4 Validate Spoke Controller → Hub PostgREST communication

- [ ] 10.4 Observability validation
  - [ ] 10.4.1 Verify metrics collection and storage in VictoriaMetrics
  - [ ] 10.4.2 Test log aggregation and search in OpenSearch
  - [ ] 10.4.3 Validate distributed tracing in Tempo
  - [ ] 10.4.4 Confirm Grafana dashboards display platform metrics

- [ ] 10.5 Agent-core readiness validation
  - [ ] 10.5.1 Verify AgentRegistry API is accessible at expected endpoint
  - [ ] 10.5.2 Test NATS cluster accepts agent lifecycle events
  - [ ] 10.5.3 Confirm Hub PostgREST is ready for spoke controller connections
  - [ ] 10.5.4 Validate all dependencies for agent-core integration are met

**Final Manual Testing:**
- Complete end-to-end status synchronization flow
- Test failure scenarios and recovery mechanisms
- Validate tenant isolation across all services
- Performance test with multiple concurrent operations
- Confirm all services are ready for agent-core integration

**PAUSE: User must approve final validation before agent-core integration**

---

## Notes

- No automated test suites required (manual testing only)
- Each phase must be tested and approved before proceeding
- Focus on service integration and data flow validation
- Verify architectural corrections are properly implemented
- Test with real cluster environments, not mocks
- Ensure all services are ready for agent-core dependency requirements
