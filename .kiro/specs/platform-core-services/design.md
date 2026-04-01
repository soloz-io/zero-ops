# Platform Core Services - Technical Design

**Spec ID:** platform-core-services  
**Status:** Draft  
**Created:** 2026-03-26  
**Last Updated:** 2026-03-26

## 1. Overview

This design implements the Day 0 core platform services required for agent-core functionality. These services bridge the gap between foundational infrastructure (identity, databases) and business domain services (agents-core, mcp-server).

**CRITICAL DEPENDENCY CHAIN:**
- `cmd/mcp-server/main.go` → AgentRegistry OSS → Control Plane DB
- `operators/spoke-controller` → Hub PostgREST → Hub Centralised DB  
- `internal/agent-core/service` → NATS → Event-driven orchestration
- `deploy_agent` MCP tool → Spoke clusters → CAPI kubeconfig secrets

### 1.1 Architecture Corrections

**CORRECTED - GitOps First Constraint:**
- Spoke cluster creation uses GitOps fleet repository commits, NOT direct Kubernetes API
- Hub CLI generates manifests and commits to Git, ArgoCD syncs to cluster
- No direct `k8sClient.Create()` calls except during bootstrap

**CORRECTED - Database Routing:**
- AgentRegistry connects to `control_plane` database (agentregistry schema)
- Hub PostgREST connects to `hub` database (agent_infra_status table)
- Clear separation between Control Plane Shared DB and Hub Centralised DB

**CORRECTED - Status Sync Architecture:**
- Hub DB pg_notify triggers publish to NATS `hub.platform.agent.infra_status`
- NATS subscriber consumes events and updates AgentRegistry via API
- No direct PostgreSQL listeners bypassing NATS

### 1.2 Service Architecture

```
┌─────────────────────────────────────────────────────────┐
│ PLATFORM CORE SERVICES (Day 0)                         │
│                                                         │
│ ├── AgentRegistry OSS → Control Plane DB               │
│ ├── VictoriaMetrics → Metrics storage & collection     │
│ ├── NATS Cluster → Event-driven messaging              │
│ ├── Hub PostgREST → Hub Centralised DB API             │
│ ├── OpenSearch → Log aggregation                       │
│ ├── Tempo → Distributed tracing                        │
│ └── Spoke Provisioning → GitOps fleet repository       │
└─────────────────────────────────────────────────────────┘
```

## 2. Component Implementations

### 2.1 AgentRegistry OSS Service

**Location:** `platform-agentregistry` namespace  
**Purpose:** CRUD-only database wrapper for agent definitions and deployments

**Database Connection (CORRECTED):**
```yaml
# manifests/platform-agentregistry/deployment.yaml
env:
- name: DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: control-plane-db-credentials  # CRITICAL: Control Plane DB
      key: url
```

**Key Requirements:**
- Connect to Control Plane Shared DB (`control_plane` database)
- Access `agentregistry` schema with proper RLS policies
- Expose REST API at `agentregistry.platform-agentregistry.svc.cluster.local:8080`
- NO Kubernetes operations (CRUD-only)

**API Endpoints:**
```
POST   /v0/agents                              # Create agent definition
GET    /v0/agents                              # List agents (RLS filtered)
GET    /v0/agents/{name}/versions/{version}   # Get specific agent
DELETE /v0/agents/{name}/versions/{version}   # Delete agent
POST   /v0/deployments                         # Create deployment record
GET    /v0/deployments/{id}                    # Get deployment status
PATCH  /v0/deployments/{id}                    # Update deployment status
DELETE /v0/deployments/{id}                    # Delete deployment
```

### 2.2 VictoriaMetrics Observability Stack

**Location:** `observability` namespace  
**Purpose:** Centralized metrics storage for Hub and all spoke clusters

**Components:**
- VictoriaMetrics cluster (vmcluster) for metrics storage
- Prometheus Operator for ServiceMonitor CRD support
- Grafana Alloy for metrics collection and forwarding
- Grafana dashboards for platform monitoring

**Cross-Cluster Metrics Ingestion:**

Spoke clusters push metrics to Hub VictoriaMetrics via Grafana Alloy `remote_write` with **mTLS (SPIFFE/SPIRE)**:

```
Spoke Cluster (Grafana Alloy with SPIRE Agent)
  → Obtains SVID: spiffe://zero-ops.nutgraf.in/grafana-alloy/{tenant-id}
  → remote_write (HTTPS + mTLS)
  → Hub VictoriaMetrics (validates SPIFFE SVID)
  → Centralized metrics storage
  → Platform Admin dashboards
```

**Authentication Model:**
- **Protocol**: mTLS with SPIFFE/SPIRE workload identity
- **Identity**: Each Alloy instance gets unique SPIFFE ID
- **Certificate Rotation**: Automatic (default 1-hour TTL)
- **Trust**: Hub SPIRE Server issues and validates SVIDs

**Why mTLS (SPIFFE) instead of Basic Auth:**
- Zero credential management (automatic certificate issuance/rotation)
- Workload identity (cryptographically verifiable)
- Zero-trust architecture (mutual authentication)
- Industry standard for service-to-service auth

**Service Endpoints:**
```
victoriametrics.zero-ops-system.svc.cluster.local:8428  # PromQL API
grafana-alloy.zero-ops-system.svc.cluster.local:9090   # Metrics collection
```

### 2.3 NATS Messaging Cluster

**Location:** `zero-ops-system` namespace  
**Purpose:** Event-driven communication for status synchronization

**JetStream Configuration:**
```yaml
# Required subjects for agent lifecycle
subjects:
  - hub.platform.agent.created
  - hub.platform.agent.deployed  
  - hub.platform.agent.updated
  - hub.platform.agent.deleted
  - hub.platform.agent.infra_status  # CRITICAL: Status sync subject
```

**Status Sync Flow (CORRECTED):**
1. Spoke Controller writes to Hub PostgREST
2. Hub DB pg_notify trigger publishes to NATS `hub.platform.agent.infra_status`
3. NATS subscriber consumes events and updates AgentRegistry via API

### 2.4 Hub PostgREST Configuration

**Location:** `zero-ops-system` namespace  
**Purpose:** REST API for Hub Centralised DB access

**Database Connection (CORRECTED):**
```yaml
# manifests/hub-postgrest/deployment.yaml
env:
- name: PGRST_DB_URI
  valueFrom:
    secretKeyRef:
      name: hub-db-credentials  # CRITICAL: Hub Centralised DB
      key: url
```

**Key Requirements:**
- Connect to Hub Centralised DB (`hub` database)
- Expose `agent_infra_status` table with RLS policies
- JWT authentication with Ory Hydra integration
- External endpoint: `postgrest.hub.nutgrat.in`
- Internal endpoint: `hub-postgrest.zero-ops-system.svc.cluster.local:3000`

**Database Schema:**
```sql
-- Hub Centralised DB (hub database)
CREATE TABLE agent_infra_status (
    tenant_id UUID NOT NULL,
    agent_id VARCHAR NOT NULL,
    deployment_id UUID NOT NULL,
    spoke_cluster_id VARCHAR NOT NULL,
    status VARCHAR NOT NULL,  -- provisioning, ready, failed
    phase VARCHAR NOT NULL,   -- Unknown, Idle, Running, Failed
    replicas INTEGER NOT NULL DEFAULT 0,
    message TEXT,
    last_sync_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    PRIMARY KEY (deployment_id)
);

-- pg_notify trigger for NATS integration
CREATE OR REPLACE FUNCTION notify_agent_status_change()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM pg_notify('hub.platform.agent.infra_status', 
        json_build_object(
            'deployment_id', NEW.deployment_id,
            'status', NEW.status,
            'phase', NEW.phase,
            'replicas', NEW.replicas
        )::text
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER agent_status_notify
    AFTER INSERT OR UPDATE ON agent_infra_status
    FOR EACH ROW EXECUTE FUNCTION notify_agent_status_change();
```

### 2.5 OpenSearch Log Aggregation

**Location:** `zero-ops-system` namespace  
**Purpose:** Centralized log storage and search

**Components:**
- OpenSearch cluster (3 nodes for HA)
- Grafana Alloy for log collection from all namespaces
- Index templates for structured agent logs
- Log correlation via trace IDs

**Service Endpoints:**
```
opensearch.zero-ops-system.svc.cluster.local:9200  # REST API
opensearch-dashboards.zero-ops-system.svc.cluster.local:5601  # UI
```

### 2.6 Tempo Distributed Tracing

**Location:** `zero-ops-system` namespace  
**Purpose:** Distributed tracing for request correlation

**Configuration:**
- OTLP endpoint for trace ingestion
- S3-compatible storage (Hetzner S3) for trace data
- Integration with OpenSearch for log correlation
- Grafana integration for trace visualization

**Service Endpoints:**
```
tempo.zero-ops-system.svc.cluster.local:4318  # OTLP gRPC
tempo.zero-ops-system.svc.cluster.local:3200  # HTTP API
```

### 2.7 Spoke Cluster Provisioning (CORRECTED - GitOps)

**Purpose:** Automate spoke cluster lifecycle via GitOps

**GitOps Flow (CORRECTED):**
```go
// cmd/hub/spoke.go - CORRECTED implementation
func createSpokeCluster(clusterName, clusterType, region string) error {
    // 1. Generate CAPI Cluster and ClusterClass manifests
    manifests := generateCAPIManifests(clusterName, clusterType, region)
    
    // 2. Commit to GitOps fleet repository (NOT direct K8s API)
    err := gitops.CommitToFleetRepo(manifests, fmt.Sprintf("Add spoke cluster %s", clusterName))
    if err != nil {
        return fmt.Errorf("failed to commit to fleet repo: %w", err)
    }
    
    // 3. ArgoCD will sync and provision the cluster
    fmt.Printf("Spoke cluster %s queued for provisioning via GitOps\n", clusterName)
    return nil
}
```

**CLI Commands (CORRECTED):**
```bash
# GitOps-based spoke management
hub spoke create <cluster-name> --type=pool|silo --region=<region>  # Commits to Git
hub spoke list                                                      # Reads from Git
hub spoke delete <cluster-name>                                     # Commits deletion to Git
```

**CAPI Integration:**
- ClusterClass definitions for pool and silo spoke types
- Automatic kubeconfig secret generation in `zero-ops-system` namespace
- Spoke Controller deployment via GitOps fleet repository

### 2.8 Crossplane Platform APIs (XRDs) - CRITICAL

**Purpose:** Enforce open-sbt abstraction layer to prevent custom infrastructure code

**CRITICAL RULE ENFORCEMENT:**
- Hub cluster hosts Crossplane and platform XRDs
- Application Plane NEVER writes raw Kubernetes resources
- All infrastructure provisioned via Crossplane Claims
- Secrets managed via Infisical integration
- Backups handled via Velero annotations

**Directory Structure (MANDATORY):**
```
zero-ops/
├── xrds/                                  # Platform Definitions (Hub Day 0)
│   ├── definitions/                       # APIs exposed to Application Plane
│   │   ├── database.opensbt.io_tenantdatabases.yaml
│   │   ├── cluster.opensbt.io_spokeclusters.yaml
│   │   └── storage.opensbt.io_tenantbuckets.yaml
│   │
│   └── compositions/                      # Implementation of APIs
│       ├── database-cnpg-velero-infisical.yaml  # CNPG + Infisical + Velero
│       ├── cluster-hetzner-capi.yaml
│       └── storage-hetzner-s3.yaml
│
├── internal/opensbt/providers/gitops/
│   └── helm-chart/                        # Tenant Instances (Spoke Day 1+)
│       ├── templates/
│       │   ├── database-claim.yaml        # XRC Claims only
│       │   ├── namespace.yaml
│       │   └── rbac.yaml
│       └── values.yaml
```

**Crossplane Provider Requirements:**
- `provider-kubernetes`: Deploy CNPG clusters and secrets
- `provider-helm`: Deploy Helm charts to spoke clusters
- External Secrets Operator: Infisical integration
- Velero: Automated backup management

**XRD Example - Tenant Database:**
```yaml
# xrds/definitions/database.opensbt.io_tenantdatabases.yaml
apiVersion: apiextensions.crossplane.io/v1
kind: CompositeResourceDefinition
metadata:
  name: xtenantdatabases.platform.opensbt.io
spec:
  group: platform.opensbt.io
  names:
    kind: XTenantDatabase
    plural: xtenantdatabases
  claimNames:
    kind: TenantDatabase
    plural: tenantdatabases
  versions:
  - name: v1alpha1
    served: true
    referenceable: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              size:
                type: string
                enum: ["small", "medium", "large"]
              backup:
                type: boolean
                default: true
          status:
            type: object
            properties:
              connectionSecret:
                type: string
```

**Composition Example - Database with Infisical + Velero:**
```yaml
# xrds/compositions/database-cnpg-velero-infisical.yaml
apiVersion: apiextensions.crossplane.io/v1
kind: Composition
metadata:
  name: tenant-database-standard
  labels:
    crossplane.io/xrd: xtenantdatabases.platform.opensbt.io
spec:
  mode: Resources
  compositeTypeRef:
    apiVersion: platform.opensbt.io/v1alpha1
    kind: XTenantDatabase
  resources:
  # 1. CNPG Cluster with Velero backup annotations
  - name: cnpg-cluster
    base:
      apiVersion: kubernetes.crossplane.io/v1alpha2
      kind: Object
      spec:
        forProvider:
          manifest:
            apiVersion: postgresql.cnpg.io/v1
            kind: Cluster
            metadata:
              annotations:
                backup.velero.io/backup-volumes: "pgdata"
            spec:
              instances: 2
              storage:
                size: 20Gi
              backup:
                barmanObjectStore:
                  destinationPath: s3://zero-ops-backups/tenants/
                  endpointURL: https://fsn1.your-objectstorage.com
    patches:
    - type: FromCompositeFieldPath
      fromFieldPath: spec.claimRef.namespace
      toFieldPath: spec.forProvider.manifest.metadata.namespace
    
  # 2. Infisical PushSecret for zero-touch credential management
  - name: infisical-push-secret
    base:
      apiVersion: kubernetes.crossplane.io/v1alpha2
      kind: Object
      spec:
        forProvider:
          manifest:
            apiVersion: external-secrets.io/v1alpha1
            kind: PushSecret
            spec:
              refreshInterval: "1h"
              secretStoreRefs:
              - name: infisical-backend
                kind: ClusterSecretStore
              data:
              - match:
                  secretKey: password
                  remoteRef:
                    remoteKey: DB_PASSWORD
    patches:
    - type: CombineFromComposite
      combine:
        variables:
        - fromFieldPath: spec.claimRef.namespace
        strategy: string
        string:
          fmt: "/tenants/%s/DB_PASSWORD"
      toFieldPath: spec.forProvider.manifest.spec.data[0].match.remoteRef.remoteKey
```

**Application Plane Helm Chart (CORRECTED):**
```yaml
# internal/opensbt/providers/gitops/helm-chart/templates/database-claim.yaml
apiVersion: platform.opensbt.io/v1alpha1
kind: TenantDatabase  # Crossplane Claim - NOT raw CNPG
metadata:
  name: {{ .Values.tenantId }}-db
  namespace: {{ .Values.tenantId }}
spec:
  size: {{ .Values.database.size | default "small" }}
  backup: {{ .Values.database.backup | default true }}
  compositionRef:
    name: tenant-database-standard
```

**CRITICAL ENFORCEMENT RULES:**

1. **No K8s Import Rule:** No Go file in `internal/opensbt/` may import `k8s.io/client-go`
2. **Crossplane Border Rule:** Helm charts emit ONLY Namespaces, RBAC, and Crossplane Claims
3. **Invisible Secrets Rule:** Go code uses `ISecretManager` (Infisical), never K8s secrets

## 3. Database Architecture (CORRECTED)

### 3.1 Database Separation

**Control Plane Shared DB (`control_plane` database):**
- AgentRegistry schema and tables
- Identity services (Ory stack) data
- Tenant management data
- Connection: `control-plane-db-credentials` secret

**Hub Centralised DB (`hub` database):**
- `agent_infra_status` table for status synchronization
- Cross-cluster status aggregation
- PostgREST API exposure
- Connection: `hub-db-credentials` secret

### 3.2 Secure Credential Management (CRITICAL)

**Zero-Touch Credential Pattern (from Identity Setup):**

All database credentials MUST follow the secure pattern established in `manifests/platform-identity/databases/`:

1. **Secret Generation:** Credentials generated via Infisical or secure bootstrap script
2. **Role Creation Job:** Kubernetes Job reads secrets via `secretKeyRef` and creates database roles
3. **No Hardcoded Passwords:** NEVER use hardcoded passwords in CNPG `postInitSQL` or manifests

**Example (Correct Pattern):**
```yaml
# Step 1: Generate secure credentials (via Infisical or bootstrap)
apiVersion: v1
kind: Secret
metadata:
  name: agentregistry-db-credentials
  namespace: platform-agentregistry
type: Opaque
data:
  password: <base64-encoded-secure-password>  # Generated, NOT hardcoded
  url: <base64-encoded-connection-string>

---
# Step 2: Create database roles via Job (NOT postInitSQL)
apiVersion: batch/v1
kind: Job
metadata:
  name: setup-agentregistry-role
  namespace: zero-ops-system
spec:
  template:
    spec:
      containers:
      - name: setup-role
        image: postgres:16-alpine
        env:
        - name: AGENTREGISTRY_PASSWORD
          valueFrom:
            secretKeyRef:
              name: agentregistry-db-credentials
              key: password
        - name: PGHOST
          value: "platform-db-rw.zero-ops-system.svc.cluster.local"
        - name: PGDATABASE
          value: "control_plane"
        - name: PGUSER
          value: "postgres"
        - name: PGPASSWORD
          valueFrom:
            secretKeyRef:
              name: platform-db-superuser
              key: password
        command:
        - /bin/sh
        - -c
        - |
          psql -c "CREATE ROLE agentregistry_user WITH LOGIN PASSWORD '${AGENTREGISTRY_PASSWORD}';"
          psql -c "GRANT ALL PRIVILEGES ON SCHEMA agentregistry TO agentregistry_user;"
          psql -c "GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA agentregistry TO agentregistry_user;"
      restartPolicy: OnFailure
```

**FORBIDDEN Pattern:**
```yaml
# NEVER DO THIS - Hardcoded passwords
bootstrap:
  initdb:
    postInitSQL:
    - CREATE USER agentregistry WITH PASSWORD 'changeme';  # FORBIDDEN
```

### 3.3 Connection String Routing (CORRECTED)

```yaml
# AgentRegistry deployment
env:
- name: DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: control-plane-db-credentials  # → control_plane database
      key: url

---
# Hub PostgREST deployment  
env:
- name: PGRST_DB_URI
  valueFrom:
    secretKeyRef:
      name: hub-db-credentials  # → hub database
      key: url
```

## 4. Status Synchronization Architecture (CORRECTED)

### 4.1 Event Flow

```
Spoke Controller (Spoke Cluster)
    ↓ HTTPS POST with mTLS (SPIFFE)
Hub AgentGateway (Hub Cluster)
    ↓ Terminates mTLS, issues short-lived JWT
Hub PostgREST (Hub Cluster)
    ↓ INSERT/UPDATE agent_infra_status (JWT auth)
Hub Centralised DB
    ↓ pg_notify trigger
NATS hub.platform.agent.infra_status subject
    ↓ NATS subscription
NATS Status Subscriber (Hub Cluster)
    ↓ HTTP PATCH
AgentRegistry API
    ↓ UPDATE deployments table
Control Plane Shared DB
```

### 4.2 NATS Status Subscriber (CORRECTED)

**Location:** `cmd/nats-subscriber/main.go`  
**Purpose:** Bridge NATS events to AgentRegistry API

```go
// CORRECTED: Uses NATS, not direct PostgreSQL
func (s *NATSStatusSubscriber) Start(ctx context.Context) error {
    // Subscribe to NATS subject (NOT pg_notify)
    _, err := s.natsConn.Subscribe("hub.platform.agent.infra_status", s.handleStatusUpdate)
    if err != nil {
        return fmt.Errorf("failed to subscribe to NATS: %w", err)
    }
    
    s.logger.InfoContext(ctx, "NATS status subscriber started")
    <-ctx.Done()
    return nil
}

func (s *NATSStatusSubscriber) handleStatusUpdate(msg *nats.Msg) {
    var update models.InfraStatusUpdate
    json.Unmarshal(msg.Data, &update)
    
    // Map infrastructure status to deployment status
    deploymentStatus := s.mapInfraStatusToDeploymentStatus(update.Status)
    
    // Update AgentRegistry via API (NOT direct DB)
    s.agentRegistryClient.UpdateDeployment(ctx, update.DeploymentID, deploymentStatus)
}
```

## 5. Observability Integration

### 5.1 Cross-Cluster Metrics Ingestion

**Hub VictoriaMetrics Configuration:**
- External ingress at `victoriametrics.hub.nutgraf.in` for spoke metrics ingestion
- **mTLS authentication via SPIFFE/SPIRE** (replaces basic auth)
- Each spoke Grafana Alloy instance obtains unique SPIFFE identity
- Hub VictoriaMetrics validates SPIFFE SVIDs (X.509 certificates)

**Spoke Cluster Grafana Alloy Configuration:**
```yaml
# Grafana Alloy remote_write config (spoke cluster)
prometheus.remote_write "hub" {
  endpoint {
    url = "https://victoriametrics.hub.nutgraf.in/api/v1/write"
    
    tls_config {
      # SPIFFE Workload API provides automatic certificate rotation
      cert_file = "/run/spire/sockets/agent.sock"  # SPIRE Agent socket
      key_file  = "/run/spire/sockets/agent.sock"
      ca_file   = "/run/spire/bundle.crt"          # Trust bundle
      
      # SPIFFE identity for this Alloy instance
      # Format: spiffe://zero-ops.nutgraf.in/grafana-alloy/{tenant-id}
      server_name = "victoriametrics.hub.nutgraf.in"
    }
  }
  
  external_labels = {
    cluster_id = "spoke-acme-prod"
    tenant_id  = "acme-corp"
    tier       = "enterprise"
    region     = "eu-central-1"
  }
}
```

**mTLS Authentication Flow:**
1. Spoke bootstrap provisions SPIRE Agent on all nodes
2. SPIRE Agent federates with Hub SPIRE Server
3. Grafana Alloy pod attests to SPIRE Agent via Kubernetes workload attestation
4. SPIRE Agent issues SVID: `spiffe://zero-ops.nutgraf.in/grafana-alloy/{tenant-id}`
5. Alloy uses SVID for mTLS connection to Hub VictoriaMetrics
6. Hub VictoriaMetrics validates SVID against SPIRE trust bundle
7. Certificates automatically rotate (default: 1-hour TTL)

**SPIFFE Identity Registration:**
```bash
# Hub SPIRE Server registration (per spoke)
spire-server entry create \
  -spiffeID spiffe://zero-ops.nutgraf.in/grafana-alloy/acme-corp \
  -parentID spiffe://zero-ops.nutgraf.in/spoke-agent/acme-prod \
  -selector k8s:ns:observability \
  -selector k8s:sa:grafana-alloy \
  -dns victoriametrics.hub.nutgraf.in
```

**Why mTLS (SPIFFE) instead of Basic Auth:**
- **Zero credential management:** No passwords to generate, store, or rotate
- **Automatic rotation:** Certificates rotate every hour by default
- **Workload identity:** Cryptographically verifiable service identity
- **Zero-trust architecture:** Mutual authentication (both sides verify)
- **Industry standard:** SPIFFE is CNCF graduated project for service identity

**NOT used for:**
- KEDA autoscaling (KEDA queries local spoke metrics)
- Real-time operational decisions (uses local metrics)
- Spoke-to-spoke communication (no cross-spoke queries)

**Used for:**
- Platform Admin fleet-wide dashboards
- Centralized alerting (vmalert on Hub)
- Historical analysis and capacity planning
- Cross-tenant correlation (Platform Admin only)

### 5.2 Telemetry Stack

**Metrics:** VictoriaMetrics with Prometheus client libraries  
**Tracing:** Tempo with OpenTelemetry instrumentation  
**Logging:** OpenSearch with structured JSON logs  
**Dashboards:** Grafana with pre-built platform dashboards

## 6. Deployment Architecture

### 6.1 ArgoCD Applications

```yaml
# manifests/argocd/apps/platform-core-services.yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: platform-core-services
  namespace: argocd
  annotations:
    argocd.argoproj.io/sync-wave: "4"  # After identity and database
spec:
  project: default
  source:
    repoURL: https://github.com/soloz-io/zero-ops
    targetRevision: HEAD
    path: manifests/platform-core-services
  destination:
    server: https://kubernetes.default.svc
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
```

### 6.2 Deployment Order

1. **Database Extensions** (sync-wave: 4)
   - Extend existing CNPG clusters with new schemas
   - Create connection secrets for service routing

2. **Core Infrastructure** (sync-wave: 5)
   - NATS cluster with JetStream
   - VictoriaMetrics cluster
   - OpenSearch cluster
   - Tempo deployment

3. **Platform Services** (sync-wave: 6)
   - AgentRegistry OSS
   - Hub PostgREST
   - NATS Status Subscriber

4. **Observability** (sync-wave: 7)
   - Grafana Alloy configuration
   - ServiceMonitor CRDs
   - Grafana dashboards

### 6.3 Network Security

**Internal Service Communication:**
- All services use cluster DNS for discovery
- Network policies restrict cross-namespace access
- Service accounts for authentication

**External Access:**
- Hub PostgREST via ingress with TLS
- VictoriaMetrics via ingress for spoke cluster access
- Grafana dashboards for monitoring

## 7. Integration Points

### 7.1 Agent-Core Dependencies

**Required Services:**
- AgentRegistry OSS at `agentregistry.platform-agentregistry.svc.cluster.local:8080`
- NATS cluster at `nats.zero-ops-system.svc.cluster.local:4222`
- Hub PostgREST at `postgrest.hub.nutgrat.in` (external)
- VictoriaMetrics at `victoriametrics.zero-ops-system.svc.cluster.local:8428`

**Crossplane Platform APIs (CRITICAL):**
- XRDs deployed to Hub cluster for tenant abstraction
- Compositions handle CNPG + Infisical + Velero integration
- External Secrets Operator for zero-touch credential management
- Velero for automated backup management

**Service Discovery:**
- All services accessible via consistent cluster DNS naming
- Health checks and readiness probes configured
- Service mesh integration (if applicable)

### 7.2 Tenant Onboarding Integration (UPDATED)

**Spoke Cluster Assignment:**
- Tenant tier determines spoke cluster type (pool vs silo)
- JWT tokens include `spoke_cluster_id` claim
- AgentGateway routes based on spoke assignment

**Crossplane Provisioning (CORRECTED):**
- Tenant databases provisioned via `TenantDatabase` Claims
- Crossplane Compositions handle CNPG, Infisical, and Velero
- Application Plane Helm charts emit ONLY Claims, not raw resources
- Secrets managed via Infisical, never direct K8s secret access

**Cross-Cluster RBAC:**
- Spoke Controllers authenticate to Hub PostgREST via OAuth2
- CAPI kubeconfig secrets managed automatically
- Service account rotation and credential refresh

**CRITICAL ENFORCEMENT RULES:**
1. **No K8s Import Rule:** No Go file in `internal/opensbt/` may import `k8s.io/client-go`
2. **Crossplane Border Rule:** Helm charts emit ONLY Namespaces, RBAC, and Crossplane Claims
3. **Invisible Secrets Rule:** Go code uses `ISecretManager` (Infisical), never K8s secrets

## 8. Testing Strategy

### 8.1 Service Health Validation

```bash
# Verify all services are running
kubectl get pods -n platform-agentregistry
kubectl get pods -n zero-ops-system

# Test API endpoints
curl -X GET http://agentregistry.platform-agentregistry.svc.cluster.local:8080/v0/agents
curl -X GET http://victoriametrics.zero-ops-system.svc.cluster.local:8428/api/v1/query?query=up
```

### 8.2 Status Sync Validation

```bash
# Test NATS connectivity
nats --server=nats://nats.zero-ops-system.svc.cluster.local:4222 pub hub.platform.agent.infra_status '{"deployment_id":"test","status":"ready"}'

# Verify AgentRegistry receives update
curl -X GET http://agentregistry.platform-agentregistry.svc.cluster.local:8080/v0/deployments/test
```

### 8.3 GitOps Spoke Provisioning

```bash
# Test spoke cluster creation (GitOps)
hub spoke create test-spoke --type=pool --region=fsn1

# Verify manifest committed to fleet repository
git log --oneline fleet-repository/

# Verify ArgoCD syncs the cluster
kubectl get clusters -n zero-ops-system
```

This design addresses all architectural deviations and provides a solid foundation for agent-core integration.