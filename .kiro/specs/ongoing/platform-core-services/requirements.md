# Platform Core Services - Requirements

**Spec ID:** platform-core-services  
**Status:** Draft  
**Created:** 2026-03-26  
**Target:** Platform Team  
**Purpose:** Day 0 deployment of core platform services required for agent-core functionality

## 1. Overview

This specification defines the core platform services that must be deployed and operational before agent-core business logic can function. These services provide the foundational infrastructure for agent lifecycle management, observability, messaging, and multi-cluster operations.

**CRITICAL DEPENDENCY:** Agent-core integration spec assumes these platform services are deployed and healthy.

### 1.1 Scope

**In Scope:**
- AgentRegistry OSS deployment and configuration
- VictoriaMetrics observability stack
- NATS messaging cluster
- Hub PostgREST configuration
- Spoke cluster provisioning automation
- Cross-cluster RBAC and networking

**Out of Scope:**
- Agent-core business logic (separate spec)
- Tenant onboarding workflows
- Application-level services

### 1.2 Success Criteria

- All core services deployed via GitOps (ArgoCD)
- Services accessible via cluster DNS
- Health checks passing for all components
- Ready for agent-core integration testing

## 2. Functional Requirements

### FR-1: AgentRegistry OSS Service

**Description:** Deploy and configure the open-source AgentRegistry for agent CRUD operations

**Requirements:**
- Deploy AgentRegistry OSS container to `platform-agentregistry` namespace
- Connect to Control Plane Shared DB (`agentregistry` schema)
- Expose internal service at `agentregistry.platform-agentregistry.svc.cluster.local:8080`
- Configure tenant isolation via RLS policies
- Support agent definition and deployment record management

**API Endpoints Required:**
```
POST   /v0/agents                              # Create agent definition
GET    /v0/agents                              # List agents
GET    /v0/agents/{name}/versions/{version}   # Get specific agent
DELETE /v0/agents/{name}/versions/{version}   # Delete agent
POST   /v0/deployments                         # Create deployment record
GET    /v0/deployments/{id}                    # Get deployment status
PATCH  /v0/deployments/{id}                    # Update deployment status
DELETE /v0/deployments/{id}                    # Delete deployment
```

### FR-2: VictoriaMetrics Observability Stack

**Description:** Deploy metrics storage and collection infrastructure

**Requirements:**
- Deploy VictoriaMetrics cluster for metrics storage
- Configure Prometheus Operator for ServiceMonitor discovery
- Deploy Grafana Alloy for metrics collection and forwarding
- Expose VictoriaMetrics at `victoriametrics.zero-ops-system.svc.cluster.local:8428`
- Support PromQL-compatible queries
- Scrape `/metrics` endpoints from services with ServiceMonitor CRDs

**Components:**
- VictoriaMetrics cluster (vmcluster)
- Prometheus Operator (for ServiceMonitor CRDs)
- Grafana Alloy (metrics collection)
- Basic Grafana dashboards for platform monitoring

### FR-3: NATS Messaging Cluster

**Description:** Deploy NATS cluster for event-driven communication

**Requirements:**
- Deploy NATS cluster in `zero-ops-system` namespace
- Configure JetStream for persistent messaging
- Set up Hub-Spoke event routing subjects
- Expose NATS at `nats.zero-ops-system.svc.cluster.local:4222`
- Configure subject permissions and ACLs
- Support event publishing and subscription patterns

**Required Subjects:**
```
hub.platform.agent.created
hub.platform.agent.deployed
hub.platform.agent.updated
hub.platform.agent.deleted
hub.platform.agent.infra_status
```

### FR-4: Hub PostgREST Configuration

**Description:** Configure PostgREST for Hub Centralised DB access (internal service behind AgentGateway)

**Requirements:**
- Deploy PostgREST against Hub Centralised DB
- Configure JWT-only authentication (receives JWTs from AgentGateway)
- Expose internal service at `postgrest.zero-ops-system.svc.cluster.local:3000` (external access handled via AgentGateway at `agentgateway.hub.nutgraf.in`)
- Enable RLS policies for tenant isolation (tenant_id from JWT claims)
- Support agent infrastructure status writes from Spoke Controllers (via AgentGateway)
- Configure proper CORS and security headers

**Required Tables Exposed:**
```sql
agent_infra_status  -- Write for Spoke Controllers (via AgentGateway), Read for dashboards
```

**Authentication Flow:**
- Spoke Controller → AgentGateway (mTLS/SPIFFE) → PostgREST (JWT)
- AgentGateway terminates mTLS, issues short-lived JWT, forwards to PostgREST

### FR-5: OpenSearch Log Aggregation

**Description:** Deploy log aggregation and search infrastructure

**Requirements:**
- Deploy OpenSearch cluster for log storage
- Configure Grafana Alloy for log collection
- Set up JSON log parsing for structured logs
- Expose OpenSearch at `opensearch.zero-ops-system.svc.cluster.local:9200`
- Configure index templates for agent logs
- Support log correlation via trace IDs

### FR-6: Tempo Distributed Tracing

**Description:** Deploy distributed tracing infrastructure

**Requirements:**
- Deploy Tempo for trace storage
- Configure OTLP endpoint for trace ingestion
- Expose Tempo at `tempo.zero-ops-system.svc.cluster.local:4318`
- Support OpenTelemetry trace collection
- Configure trace retention policies
- Enable trace correlation with logs and metrics

### FR-7: Spoke Cluster Provisioning

**Description:** Automate spoke cluster provisioning via CAPI

**Requirements:**
- Create CAPI ClusterClass for spoke clusters
- Implement spoke cluster creation via Hub CLI
- Generate and manage kubeconfig secrets automatically
- Deploy Spoke Controller to each spoke cluster
- Configure cross-cluster RBAC for Hub → Spoke communication
- Support both pool and silo spoke cluster types

**CLI Commands Required:**
```bash
hub spoke create <cluster-name> --type=pool|silo --region=<region>
hub spoke list
hub spoke delete <cluster-name>
```

## 3. Non-Functional Requirements

### NFR-1: High Availability

- All core services deployed with multiple replicas
- Database services use CNPG with 3-instance clusters
- Load balancing for stateless services
- Persistent storage for stateful services

### NFR-2: Security

- All inter-service communication via cluster DNS
- JWT authentication for external API access
- RLS policies for tenant data isolation
- Network policies for service-to-service communication
- TLS encryption for external endpoints

### NFR-3: Observability

- All services expose `/metrics` endpoints
- Structured JSON logging to stdout
- Health check endpoints (`/health`, `/ready`)
- Service discovery via Kubernetes labels
- Distributed tracing instrumentation

### NFR-4: Scalability

- VictoriaMetrics cluster supports horizontal scaling
- NATS cluster supports clustering and federation
- OpenSearch cluster supports node scaling
- Services designed for multi-tenant workloads

### NFR-5: GitOps Integration

- All services deployed via ArgoCD Applications
- Configuration managed in Git repositories
- Automated sync and self-healing enabled
- Proper sync waves for dependency ordering

## 4. Integration Points

### 4.1 Database Dependencies

**Control Plane Shared DB:**
- AgentRegistry requires `agentregistry` schema
- Existing identity services use same database
- Proper connection pooling and isolation

**Hub Centralised DB:**
- PostgREST exposes `agent_infra_status` table
- Spoke Controllers write status updates
- Tenant isolation via RLS policies

### 4.2 Network Dependencies

**Internal Service Discovery:**
- All services accessible via cluster DNS
- Consistent naming conventions
- Proper port configurations

**External Access:**
- PostgREST exposed via ingress with TLS
- Grafana dashboards for monitoring
- ArgoCD UI for deployment management

### 4.3 Authentication Integration

**Ory Stack Integration:**
- PostgREST validates JWT tokens from Hydra
- Service-to-service authentication via service accounts
- Tenant context extraction from JWT claims

## 5. Deployment Dependencies

### 5.1 Prerequisites

**Existing Infrastructure:**
- Hub cluster with CAPI, CAPH, ArgoCD installed
- Identity services (Ory stack) operational
- CNPG operator for database management
- Ingress controller and cert-manager

**Required Secrets:**
- Database connection strings
- Service account credentials
- TLS certificates for external endpoints

### 5.2 Deployment Order

1. **Database Schemas** - Extend existing CNPG clusters
2. **NATS Cluster** - Core messaging infrastructure
3. **VictoriaMetrics** - Metrics storage and collection
4. **OpenSearch** - Log aggregation
5. **Tempo** - Distributed tracing
6. **AgentRegistry** - Agent CRUD operations
7. **PostgREST** - Hub database API access
8. **Spoke Provisioning** - Multi-cluster setup

## 6. Acceptance Criteria

### AC-1: Service Health

- [ ] All services report healthy status via health checks
- [ ] Services accessible via expected cluster DNS names
- [ ] Database connections established and functional
- [ ] No CrashLoopBackOff or ImagePullBackOff pods

### AC-2: API Functionality

- [ ] AgentRegistry API responds to CRUD operations
- [ ] PostgREST API accepts authenticated requests
- [ ] NATS cluster accepts publish/subscribe operations
- [ ] VictoriaMetrics accepts metrics ingestion

### AC-3: Observability

- [ ] Metrics scraped and stored in VictoriaMetrics
- [ ] Logs collected and indexed in OpenSearch
- [ ] Traces collected and stored in Tempo
- [ ] Grafana dashboards display platform metrics

### AC-4: Multi-Cluster

- [ ] Spoke cluster provisioning via CLI functional
- [ ] Kubeconfig secrets generated automatically
- [ ] Cross-cluster RBAC configured correctly
- [ ] Spoke Controller deployed to spoke clusters

### AC-5: Integration Readiness

- [ ] All services required by agent-core spec are operational
- [ ] Service discovery working for agent-core components
- [ ] Authentication and authorization configured
- [ ] Ready for agent-core integration testing

## 7. Manual Testing Procedures

### 7.1 Service Deployment Testing

```bash
# Verify all pods are running
kubectl get pods -n platform-agentregistry
kubectl get pods -n zero-ops-system

# Check service endpoints
kubectl get svc -n platform-agentregistry
kubectl get svc -n zero-ops-system

# Verify ArgoCD applications
kubectl get applications -n argocd
```

### 7.2 API Testing

```bash
# Test AgentRegistry API
curl -X GET http://agentregistry.platform-agentregistry.svc.cluster.local:8080/v0/agents

# Test VictoriaMetrics
curl -X GET http://victoriametrics.zero-ops-system.svc.cluster.local:8428/api/v1/query?query=up

# Test NATS connectivity
nats --server=nats://nats.zero-ops-system.svc.cluster.local:4222 pub test.subject "hello"
```

### 7.3 Integration Testing

```bash
# Test spoke cluster creation
hub spoke create test-spoke --type=pool --region=fsn1

# Verify kubeconfig secret created
kubectl get secret test-spoke-kubeconfig -n zero-ops-system

# Test cross-cluster connectivity
kubectl --kubeconfig=secrets/test-spoke.kubeconfig get nodes
```

This specification provides the foundation for agent-core functionality and should be implemented before proceeding with the agent-core integration spec.