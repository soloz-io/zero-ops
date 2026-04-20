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

**CRITICAL SECURITY REQUIREMENT:** All database credentials MUST follow the secure pattern from `manifests/platform-core-services/platform-identity/databases/setup-roles-job.yaml`. NO hardcoded passwords allowed.

### Tasks

- [x] 1.1 Extend Control Plane Shared DB
  - [x] 1.1.1 Add `agentregistry` schema to existing `control_plane` database
  - [x] 1.1.2 Create agent definitions and deployments tables
  - [x] 1.1.3 Configure RLS policies for tenant isolation
  - [x] 1.1.4 **SECURITY:** Generate secure credentials via Infisical (NO hardcoded passwords)
  - [x] 1.1.5 **SECURITY:** Create Kubernetes Job to create `agentregistry_user` role using `secretKeyRef` pattern
  - [x] 1.1.6 Create `ExternalSecret` manifest to sync ONLY `username` and `password` for `agentregistry-db-credentials` from Infisical. Construct connection strings in deployment ENV vars using K8s DNS.

- [x] 1.2 Extend Hub Centralised DB
  - [x] 1.2.1 Add `agent_infra_status` table to existing `hub` database
  - [x] 1.2.2 Create pg_notify trigger for NATS integration
  - [x] 1.2.3 Configure RLS policies for cross-cluster access
  - [x] 1.2.4 **SECURITY:** Generate secure credentials via Infisical (NO hardcoded passwords)
  - [x] 1.2.5 **SECURITY:** Create Kubernetes Job to create `hub_postgrest_user` role using `secretKeyRef` pattern
  - [x] 1.2.6 Create `ExternalSecret` manifest to sync ONLY `username` and `password` for `hub-db-credentials` from Infisical. Construct connection strings in deployment ENV vars using K8s DNS.

- [x] 1.3 Database connection validation
  - [x] 1.3.1 Verify `control-plane-db-credentials` routes to `control_plane` database
  - [x] 1.3.2 Verify `hub-db-credentials` routes to `hub` database
  - [x] 1.3.3 Test RLS policies with sample tenant data
  - [x] 1.3.4 Validate pg_notify trigger functionality
  - [x] 1.3.5 **SECURITY:** Verify NO hardcoded passwords exist in any manifests or CNPG postInitSQL

- [x] 1.4 CNPG Disaster Recovery Credential Management
  - [x] 1.4.1 Create Infisical keys for `platform-db-app-username` and `platform-db-app-password`
  - [x] 1.4.2 Create `ExternalSecret` manifest for `platform-db-app` credentials (sync-wave 1)
  - [x] 1.4.3 Create placeholder secret with ArgoCD prune protection (sync-wave 0)
  - [x] 1.4.4 Update CNPG Cluster spec to use `bootstrap.initdb.secret` referencing pre-created secret
  - [x] 1.4.5 Create password rotation Job (PostSync hook, sync-wave 5)
  - [x] 1.4.6 Update sync-wave annotations: ExternalSecrets (1) → CNPG (2) → Roles (4) → Rotation (5)

**Security Enforcement Checklist:**
- [x] All passwords generated via Infisical or secure bootstrap script
- [x] All database role creation uses Kubernetes Jobs with `secretKeyRef` environment variables
- [x] NO hardcoded passwords in CNPG `postInitSQL` blocks
- [x] NO plaintext passwords committed to Git
- [x] Pattern matches `manifests/platform-core-services/platform-identity/databases/setup-roles-job.yaml`
- [x] All K8s secrets for platform infrastructure created via ESO `ExternalSecret` manifests (NOT manual kubectl create secret)
- [x] CNPG `platform-db-app` credentials pre-created from Infisical for disaster recovery
- [x] Password rotation mechanism implemented for Infisical-managed credentials

**ExternalSecret Pattern (CRITICAL):**
All platform infrastructure secrets MUST be synced from Infisical via External Secrets Operator:
1. Create secret in Infisical UI/API (e.g., `control-plane-db-credentials` with `username` and `password` properties)
2. Create `ExternalSecret` manifest referencing `infisical-backend` ClusterSecretStore
3. Commit manifest to Git, ArgoCD syncs
4. ESO creates K8s secret automatically
5. Platform service mounts secret via `secretKeyRef`

**Example ExternalSecret (CORRECTED - Secrets Only, No Config):**
```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: control-plane-db-credentials
  namespace: platform-agentregistry
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: infisical-backend
    kind: ClusterSecretStore
  target:
    name: agentregistry-db-credentials
    creationPolicy: Owner
  data:
    - secretKey: username
      remoteRef:
        key: control-plane-db-credentials
        property: username
    - secretKey: password
      remoteRef:
        key: control-plane-db-credentials
        property: password
```

**CRITICAL: Decouple Secrets from Configuration**
- Infisical stores ONLY `username` and `password` (sensitive material)
- Host, port, database name are plain-text ENV vars using K8s DNS
- Connection strings constructed in deployment manifests, NOT stored in Infisical
- NEVER store full connection URIs (`postgres://user:pass@host:port/db`) in secrets

**Manual Testing Checkpoint:**
- Connect to both databases using respective credentials
- Verify schema separation and table access
- Test RLS policies with different tenant contexts
- Confirm pg_notify trigger fires on status updates
- Audit all manifests for hardcoded credentials

**PAUSE: User must approve Phase 1 before proceeding to Phase 1.5**

---

## Phase 1.5: SPIFFE/SPIRE Workload Identity (NEW)

**Goal:** Deploy zero-trust workload identity infrastructure for service-to-service mTLS

**Purpose:** SPIFFE/SPIRE provides automatic mTLS certificate provisioning for cross-cluster service-to-service authentication, replacing static credentials with short-lived, automatically-rotated certificates.

### Tasks

- [-] 1.5.1 Deploy SPIRE Server (Hub Cluster - Highly Available)
  - [ ] 1.5.1.1 Create `manifests/platform-core-services/spire/server-deployment.yaml` with 3 replicas
  - [ ] 1.5.1.2 Configure SPIRE Server to use Kubernetes Datastore (CRDs) or Hub PostgreSQL for HA storage (NOT local SQLite)
  - [ ] 1.5.1.3 Set up SPIRE Server at `spire-server.zero-ops-system.svc.cluster.local:8081`
  - [ ] 1.5.1.4 Configure node attestation (Kubernetes PSAT) allowing spoke nodes to attest
  - [ ] 1.5.1.5 Expose Prometheus `/metrics` endpoint for SPIRE Server observability

- [ ] 1.5.2 Deploy SPIRE Kubernetes Workload Registrar (CRITICAL for Automation)
  - [ ] 1.5.2.1 Deploy SPIRE K8s Workload Registrar to Hub cluster
  - [ ] 1.5.2.2 Configure annotation-based identity mapping (`spiffe.io/spiffe-id: "true"`)
  - [ ] 1.5.2.3 Set up identity templates for dynamic tenant-based SPIFFE IDs
  - [ ] 1.5.2.4 **FORBIDDEN:** Manual `spire-server entry create` commands are strictly prohibited

- [ ] 1.5.3 Deploy SPIRE Agent & Metrics (Hub Cluster)
  - [ ] 1.5.3.1 Create `manifests/platform-core-services/spire/agent-daemonset.yaml`
  - [ ] 1.5.3.2 Mount SPIRE Agent socket to workload pods (`/run/spire/sockets/agent.sock`)
  - [ ] 1.5.3.3 Configure ServiceMonitor CRDs to scrape SPIRE Server/Agent `/metrics` endpoints
  - [ ] 1.5.3.4 Set up alerts for certificate issuance failures and rotation issues

- [ ] 1.5.4 Deploy SPIRE Agent (Spoke Clusters - via edge-catalog)
  - [ ] 1.5.4.1 Add SPIRE Agent to `edge-catalog/spire-agent.yaml`
  - [ ] 1.5.4.2 Configure upstream connection to Hub SPIRE Server (nested topology, single trust domain)
  - [ ] 1.5.4.3 Configure spoke workload attestation (Kubernetes)
  - [ ] 1.5.4.4 Ensure spoke pods auto-register via GitOps annotations when deployed

- [ ] 1.5.5 Integration validation
  - [ ] 1.5.5.1 Verify SPIRE Server issues SVIDs (X.509 certificates) automatically via K8s Registrar
  - [ ] 1.5.5.2 Test automatic certificate rotation (default: 1 hour TTL)
  - [ ] 1.5.5.3 Verify spoke agents connect upstream to Hub SPIRE Server (nested topology)
  - [ ] 1.5.5.4 Test workload identity attestation for annotated pods
  - [ ] 1.5.5.5 Verify SPIRE metrics are scraped by VictoriaMetrics
  - [ ] 1.5.5.6 Test SPIRE Server HA failover (kill one replica, verify SVIDs still issued)

**Manual Testing Checkpoint:**
- Verify SPIRE Server is healthy and issuing certificates automatically
- Test SPIRE Agent on Hub nodes can retrieve SVIDs
- Verify spoke SPIRE Agents connect upstream to Hub (nested topology, single trust domain)
- Confirm workload identities are auto-registered via K8s Registrar annotations
- Verify SPIRE metrics are exposed and scraped
- Test HA failover (kill SPIRE Server replica, verify no disruption)

**PAUSE: User must approve Phase 1.5 before proceeding to Phase 2**

---

## Phase 2: NATS Messaging Infrastructure

**Goal:** Deploy NATS cluster with JetStream for event-driven communication

### Tasks

- [x] 2.1 Deploy NATS cluster
  - [x] 2.1.1 Create `manifests/platform-core-services/nats/cluster.yaml`
  - [x] 2.1.2 Configure JetStream with persistent storage
  - [x] 2.1.3 Set up required subjects for agent lifecycle
  - [x] 2.1.4 Configure ACLs and subject permissions

- [x] 2.2 Configure event subjects
  - [x] 2.2.1 Create `hub.platform.agent.created` subject
  - [x] 2.2.2 Create `hub.platform.agent.deployed` subject
  - [x] 2.2.3 Create `hub.platform.agent.updated` subject
  - [x] 2.2.4 Create `hub.platform.agent.deleted` subject
  - [x] 2.2.5 Create `hub.platform.agent.infra_status` subject (CRITICAL for status sync)

- [ ] 2.3 Configure NATS Decentralized JWT Authentication (CRITICAL for Leaf Node security)
  - [ ] 2.3.1 Generate NATS Operator key pair (nkey) for Hub JetStream cluster
  - [ ] 2.3.2 Create NATS Account for each spoke (per-tenant isolation)
  - [ ] 2.3.3 Generate User credentials (JWT + nkey) for each spoke Leaf Node
  - [ ] 2.3.4 Configure Hub NATS to validate spoke JWTs
  - [ ] 2.3.5 Store spoke NATS credentials in Infisical (per-spoke secrets)
  - [ ] 2.3.6 **PREREQUISITE:** Verify ESO is deployed to spoke clusters via edge-catalog with Infisical ClusterSecretStore configured
  - [ ] 2.3.7 Create `ExternalSecret` manifests to sync NATS credentials to spoke clusters
  - [ ] 2.3.8 Configure Leaf Node authentication in edge-catalog

**CRITICAL PREREQUISITE - Spoke Cluster ESO:**

Before executing task 2.3.7 (syncing NATS credentials to spoke clusters), verify that:
- External Secrets Operator is deployed to ALL spoke clusters
- Each spoke has a `ClusterSecretStore` configured pointing to central Infisical API
- This is typically handled by edge-catalog bootstrapping
- Without ESO on spoke, `ExternalSecret` manifests will fail to sync

**CRITICAL ARCHITECTURE RULE: No Direct Spoke-to-DB Connections**

Spoke clusters DO NOT receive database credentials. A Spoke cluster must NEVER attempt a direct TCP connection to Hub PostgreSQL. All Spoke-to-Hub operational state updates MUST route through:

`Spoke Controller` → `mTLS` → `AgentGateway` → `JWT` → `PostgREST API` → `Hub DB`

**NATS Authentication Architecture Clarification:**

NATS Leaf Nodes use **NATS Decentralized JWT Authentication**, NOT SPIFFE/mTLS. This is intentional:

- **Why not SPIFFE for NATS?** NATS has its own mature, purpose-built authentication system (Operator/Account/User JWTs with Nkeys) that provides multi-tenant account isolation within the NATS payload. SPIFFE/mTLS is used for HTTP-based services (Spoke Controller → AgentGateway, Grafana Alloy → VictoriaMetrics).

- **NATS JWT Flow:**
  1. Hub NATS Operator generates Account JWT for each spoke
  2. Each spoke gets User JWT + Nkey pair
  3. Spoke Leaf Node presents JWT during connection
  4. Hub NATS validates JWT signature and enforces subject permissions
  5. Multi-tenant isolation enforced at NATS protocol level

- **Credential Storage:**
  - Hub: NATS Operator keys stored in Infisical
  - Spoke: User JWT + Nkey synced from Infisical via ExternalSecret
  - Leaf Node mounts secret, uses for authentication

**NOT using SPIFFE for NATS because:**
- NATS JWT provides superior multi-tenant subject isolation
- NATS Operator model is industry standard for NATS deployments
- SPIFFE adds unnecessary complexity for NATS-native auth

- [x] 2.4 Service configuration
  - [x] 2.4.1 Expose NATS at `nats.zero-ops-system.svc.cluster.local:4222`
  - [x] 2.4.2 Configure health checks and monitoring
  - [x] 2.4.3 Set up service accounts for client authentication
  - [x] 2.4.4 Configure network policies for secure access

**Manual Testing Checkpoint:**
- Test NATS connectivity from within cluster
- Publish and subscribe to test subjects
- Verify JetStream persistence across pod restarts
- Test subject permissions and ACLs
- Verify NATS JWT authentication for Leaf Nodes

**PAUSE: User must approve Phase 2 before proceeding to Phase 3**

---

## Phase 3: VictoriaMetrics Observability Stack

**Goal:** Deploy metrics storage and collection infrastructure

### Tasks

- [x] 3.1 Deploy VictoriaMetrics cluster
  - [x] 3.1.1 Create `manifests/platform-core-services/victoriametrics/cluster.yaml`
  - [x] 3.1.2 Configure persistent storage for metrics data
  - [x] 3.1.3 Set up PromQL-compatible query API
  - [x] 3.1.4 Configure retention policies and storage limits

- [x] 3.2 Deploy Prometheus Operator
  - [x] 3.2.1 Install Prometheus Operator for ServiceMonitor CRDs
  - [x] 3.2.2 Configure ServiceMonitor discovery rules
  - [x] 3.2.3 Set up metric scraping configuration
  - [x] 3.2.4 Configure alerting rules (basic platform alerts)

- [ ] 3.3 Deploy Grafana Alloy
  - [x] 3.3.1 Create `manifests/platform-core-services/grafana-alloy/deployment.yaml`
  - [x] 3.3.2 Configure metrics collection from all namespaces
  - [x] 3.3.3 Set up remote_write to VictoriaMetrics
  - [x] 3.3.4 Configure service discovery for dynamic targets

- [ ] 3.4 Cross-cluster metrics ingestion (CRITICAL for spoke observability)
  - [x] 3.4.1 Expose VictoriaMetrics externally at `victoriametrics.hub.nutgraf.in`
  - [ ] 3.4.2 Configure TLS and mTLS (SPIFFE) authentication for Grafana Alloy
  - [ ] 3.4.3 Configure VictoriaMetrics to validate SPIFFE SVIDs
  - [ ] 3.4.4 Test Grafana Alloy remote_write with mTLS from spoke to Hub

**Manual Testing Checkpoint:**
- Query metrics via PromQL API
- Verify ServiceMonitor discovery works
- Test Grafana Alloy remote_write from test spoke cluster
- Confirm metrics appear in Hub VictoriaMetrics with correct labels

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
  - [ ] 5.1.4 Configure external ingress at `postgrest.hub.nutgrat.in`

- [ ] 5.2 Configure database access
  - [ ] 5.2.1 Use `hub-db-credentials` secret (routes to hub DB)
  - [ ] 5.2.2 Expose `agent_infra_status` table with proper RLS
  - [ ] 5.2.3 Configure JWT authentication with Ory Hydra
  - [ ] 5.2.4 Set up CORS and security headers

- [ ] 5.3 Authentication integration (CORRECTED - Routes through AgentGateway)
  - [ ] 5.3.1 Configure JWT-only authentication (PostgREST accepts ONLY JWTs from AgentGateway)
  - [ ] 5.3.2 Configure JWT validation with Hydra public keys (JWKS endpoint)
  - [ ] 5.3.3 Set up RLS context from JWT claims (tenant_id extraction)
  - [ ] 5.3.4 Configure internal-only endpoint (PostgREST not exposed externally, only to AgentGateway)
  - [ ] 5.3.5 **NEW:** Store AgentGateway Hydra OAuth2 client credentials in Infisical
  - [ ] 5.3.6 **NEW:** Create `ExternalSecret` to sync AgentGateway Hydra credentials to Hub cluster
  - [ ] 5.3.7 Add AgentGateway configuration for mTLS termination and JWT issuance
  - [ ] 5.3.8 Configure AgentGateway to route Spoke Controller requests to PostgREST

**AgentGateway Hydra Integration (CRITICAL):**

For AgentGateway to issue short-lived JWTs that PostgREST will accept, it needs Hydra OAuth2 client credentials:

1. **Create Hydra OAuth2 Client:**
   ```bash
   # Via Hydra Admin API
   hydra create client \
     --endpoint http://hydra-admin.zero-ops-system.svc:4445 \
     --id agentgateway-jwt-issuer \
     --secret <generated-secret> \
     --grant-types client_credentials \
     --scope spoke.controller.write
   ```

2. **Store in Infisical:**
   - Key: `agentgateway-hydra-credentials`
   - Properties: `client_id`, `client_secret`, `token_url`

3. **Sync via ExternalSecret:**
   ```yaml
   apiVersion: external-secrets.io/v1
   kind: ExternalSecret
   metadata:
     name: agentgateway-hydra-credentials
     namespace: zero-ops-system
   spec:
     secretStoreRef:
       name: infisical-backend
       kind: ClusterSecretStore
     target:
       name: agentgateway-hydra-credentials
       creationPolicy: Owner
     data:
       - secretKey: client_id
         remoteRef:
           key: agentgateway-hydra-credentials
           property: client_id
       - secretKey: client_secret
         remoteRef:
           key: agentgateway-hydra-credentials
           property: client_secret
   ```

4. **AgentGateway uses credentials:**
   - On Spoke Controller mTLS connection
   - Extract tenant_id from SPIFFE identity
   - Call Hydra token endpoint with client_credentials grant
   - Receive JWT with tenant_id claim
   - Forward to PostgREST with JWT

- [ ] 5.4 API security and monitoring
  - [ ] 5.4.1 Configure TLS termination at AgentGateway (not PostgREST)
  - [ ] 5.4.2 Set up rate limiting and request validation at AgentGateway
  - [ ] 5.4.3 Add metrics and health endpoints
  - [ ] 5.4.4 Configure audit logging for API access

**Manual Testing Checkpoint:**
- Test Spoke Controller → AgentGateway → PostgREST flow with mTLS
- Verify AgentGateway issues short-lived JWTs correctly
- Verify RLS policies filter data by tenant_id from JWT
- Test AgentGateway mTLS termination and JWT translation
- Confirm PostgREST only accepts requests from AgentGateway (internal routing)

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
  - [ ] 10.3.3 Test Grafana Alloy metrics ingestion from spoke to Hub
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

**PAUSE: User must approve Phase 10 before proceeding to Phase 11**

---

## Phase 11: Crossplane Abstraction Layer Deployment (CRITICAL)

**Goal:** Deploy Crossplane platform APIs to enforce open-sbt patterns

### Tasks

- [ ] 11.1 Deploy Crossplane core infrastructure
  - [ ] 11.1.1 Deploy Crossplane to Hub cluster in `hub-platform-ops` namespace
  - [ ] 11.1.2 Install `provider-kubernetes` for CNPG and secret management
  - [ ] 11.1.3 Install `provider-helm` for spoke cluster deployments
  - [ ] 11.1.4 Configure Crossplane RBAC and service accounts

- [ ] 11.2 Deploy External Secrets Operator (Infisical integration)
  - [ ] 11.2.1 Deploy External Secrets Operator to Hub cluster
  - [ ] 11.2.2 Create `ClusterSecretStore` for Infisical backend
  - [ ] 11.2.3 Configure Infisical API credentials and endpoints
  - [ ] 11.2.4 Test PushSecret functionality with sample secret

- [ ] 11.3 Deploy Velero (Backup management)
  - [ ] 11.3.1 Deploy Velero to Hub cluster with Hetzner S3 backend
  - [ ] 11.3.2 Configure backup schedules for CNPG clusters
  - [ ] 11.3.3 Set up backup retention policies
  - [ ] 11.3.4 Test backup and restore functionality

- [ ] 11.4 Create Platform XRDs (CRITICAL - No K8s Import Rule)
  - [ ] 11.4.1 Create `xrds/definitions/database.opensbt.io_tenantdatabases.yaml`
  - [ ] 11.4.2 Create `xrds/definitions/cluster.opensbt.io_spokeclusters.yaml`
  - [ ] 11.4.3 Create `xrds/definitions/storage.opensbt.io_tenantbuckets.yaml`
  - [ ] 11.4.4 Apply XRDs to Hub cluster

- [ ] 11.5 Create Platform Compositions (CRITICAL - Crossplane Border Rule)
  - [ ] 11.5.1 Create `xrds/compositions/database-cnpg-velero-infisical.yaml`
  - [ ] 11.5.2 Create `xrds/compositions/cluster-hetzner-capi.yaml`
  - [ ] 11.5.3 Create `xrds/compositions/storage-hetzner-s3.yaml`
  - [ ] 11.5.4 Apply Compositions to Hub cluster

- [ ] 11.6 Update Application Plane Helm Chart (CRITICAL - Invisible Secrets Rule)
  - [ ] 11.6.1 Replace raw CNPG YAMLs with TenantDatabase Claims
  - [ ] 11.6.2 Remove all direct Kubernetes resource templates
  - [ ] 11.6.3 Ensure only Namespaces, RBAC, and Claims are generated
  - [ ] 11.6.4 Update Go code to use ISecretManager (Infisical) instead of K8s secrets

- [ ] 11.7 Enforce architectural rules
  - [ ] 11.7.1 Audit `internal/opensbt/` for `k8s.io/client-go` imports (FORBIDDEN)
  - [ ] 11.7.2 Verify Helm charts only emit Claims, not raw resources
  - [ ] 11.7.3 Confirm Go code uses Infisical SDK for secret access
  - [ ] 11.7.4 Test tenant provisioning via Crossplane Claims

**Manual Testing Checkpoint:**
- Verify Crossplane providers are healthy and ready
- Test XRD and Composition deployment
- Create test TenantDatabase Claim and verify CNPG cluster creation
- Confirm Infisical PushSecret pushes credentials correctly
- Verify Velero backup annotations work on CNPG clusters
- Test complete tenant provisioning flow via Claims

**CRITICAL VALIDATION:**
- NO Go code in `internal/opensbt/` imports `k8s.io/client-go`
- Helm charts emit ONLY Claims, Namespaces, and RBAC
- All database passwords retrieved via Infisical API, not K8s secrets

**PAUSE: User must approve Phase 11 before production deployment**

---

## Notes

- No automated test suites required (manual testing only)
- Each phase must be tested and approved before proceeding
- Focus on service integration and data flow validation
- Verify architectural corrections are properly implemented
- Test with real cluster environments, not mocks
- Ensure all services are ready for agent-core dependency requirements
