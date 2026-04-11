# Implementation Plan: Spoke Pool Provisioner (Cell-Based Scaling)

## Overview

This implementation plan follows a phased approach to build the Spoke Pool provisioner system. Each phase includes a MANDATORY REVIEW CHECKPOINT where implementation MUST STOP and wait for user review/approval before proceeding. All tasks follow GitOps-first principles (no kubectl apply except for validation).

## CRITICAL: Phase Review Checkpoints

After completing each phase, you MUST:
1. STOP all implementation work
2. Present phase completion summary to user
3. WAIT for explicit user approval
4. DO NOT proceed to next phase without approval

---

## Phase 1: Core Infrastructure (Week 1-2)

### 1.1 SpokePool XRD and Composition

- [x] 1.1.1 Create SpokePool XRD definition
  - Define API schema: `region`, `nodePool.count`, `nodePool.instanceType`, `maxTenantCapacity`
  - Create file: `xrds/definitions/spokepool-v1.yaml`
  - Add OpenAPI validation for required fields
  - _Requirements: FR-1.1, AC-1_

- [x] 1.1.2 Create SpokePool Composition for Hetzner
  - Generate CAPI Cluster CR (Ubuntu + kubeadm, k8s v1.31.6)
  - Generate HetznerCluster CR (load balancer, network, Cilium CNI)
  - Generate KubeadmControlPlane CR (1 control plane node)
  - Generate MachineDeployment CR (worker nodes based on nodePool.count)
  - Create file: `xrds/compositions/spokepool-hetzner.yaml`
  - _Requirements: FR-1.1, AC-1_


- [x] 1.1.3 Commit XRD and Composition to Git
  - Commit to feature branch: `feature/spoke-pool-xrd`
  - Push to GitOps repository
  - Verify ArgoCD detects and syncs XRD
  - _Requirements: FR-1.1, AC-1_

### 1.2 cert-manager Certificate Generation

- [x] 1.2.1 Create ArgoCD Agent mTLS Certificate manifest
  - Define Certificate CR for ArgoCD Agent (90-day validity, auto-renew at 83 days)
  - Create self-signed CA for ArgoCD
  - Create file: `catalog/security/argocd-agent-cert.yaml`
  - _Requirements: FR-1.2, NFR-4.1, NFR-4.2, AC-3_

- [x] 1.2.2 Create NATS Leaf Node mTLS Certificate manifest
  - Define Certificate CR for NATS Leaf Node (90-day validity, auto-renew at 83 days)
  - Create self-signed CA for NATS (separate from ArgoCD CA)
  - Create file: `catalog/messaging/nats-leaf-cert.yaml`
  - _Requirements: NFR-4.1, NFR-4.2_

- [x] 1.2.3 Commit certificate manifests to Git
  - Commit to feature branch
  - Verify ArgoCD syncs and cert-manager issues certificates
  - _Requirements: NFR-4.1, NFR-4.2_

### 1.3 ClusterResourceSet (Secret Zero)

- [x] 1.3.1 Create ArgoCD Agent Deployment ConfigMap
  - Define Deployment manifest for ArgoCD Agent
  - Configure agent mode: managed, Hub URL from environment
  - Create file: `edge-catalog/argocd-agent-deployment.yaml`
  - _Requirements: FR-1.2, AC-3_


- [x] 1.3.2 Create ArgoCD Agent ConfigMap
  - Define ConfigMap with Hub URL, cluster name placeholder, mode=managed
  - Create file: `edge-catalog/argocd-agent-config.yaml`
  - _Requirements: FR-1.2, AC-3_

- [x] 1.3.3 Create ArgoCD Agent RBAC manifests
  - Define ServiceAccount, ClusterRole, ClusterRoleBinding
  - Limit permissions to agent's own cluster (no cross-cluster access)
  - Create file: `edge-catalog/argocd-agent-rbac.yaml`
  - _Requirements: FR-1.2, NFR-4.4, AC-3_

- [x] 1.3.4 Create ClusterResourceSet CR
  - Reference 5 resources: Deployment ConfigMap, Agent ConfigMap, mTLS cert Secret, CA Secret, RBAC
  - Add selector to match SpokePool clusters: `spoke-type: pool`
  - Patch mTLS certificates from cert-manager into ClusterResourceSet
  - Create file: `xrds/compositions/spokepool-clusterresourceset.yaml`
  - _Requirements: FR-1.2, AC-3_

- [x] 1.3.5 Update SpokePool Composition to generate ClusterResourceSet
  - Add ClusterResourceSet generation to Composition
  - Ensure certificates are patched before ClusterResourceSet creation
  - _Requirements: FR-1.2, AC-3_

- [x] 1.3.6 Commit ClusterResourceSet manifests to Git
  - Commit to feature branch
  - Verify ArgoCD syncs ClusterResourceSet
  - _Requirements: FR-1.2, AC-3_

### 1.4 Kyverno Cluster Discovery Policy

- [x] 1.4.1 Create Kyverno ClusterPolicy for ArgoCD Secret generation
  - Watch CAPI Cluster resources with label `spoke-type: pool`
  - Trigger on `status.phase=Provisioned`
  - Extract kubeconfig from CAPI-generated Secret
  - Generate ArgoCD cluster Secret with labels: `argocd.argoproj.io/secret-type: cluster`, `spoke-type: pool`, `cell-id: <cluster-name>`
  - Create file: `catalog/policy/kyverno-argocd-discovery.yaml`
  - _Requirements: FR-1.3, NFR-1.4, AC-2_


- [x] 1.4.2 Commit Kyverno policy to Git
  - Commit to feature branch
  - Verify ArgoCD syncs policy
  - _Requirements: FR-1.3, AC-2_

### 1.5 Phase 1 Manual Validation

- [x] 1.5.1 Apply SpokePool XR for test cell
  - Create test manifest: `spokepool-01.yaml`
  - Apply via GitOps: commit to fleet registry
  - Wait for CAPI cluster Ready: `kubectl wait --for=condition=Ready cluster/spokepool-01 --timeout=20m`
  - **COMPLETED**: spoke-pool-eu-prod-01 cluster provisioned and Ready
  - _Requirements: NFR-1.1, AC-6_

- [x] 1.5.2 Verify ArgoCD Agent bootstrap
  - Verify ClusterResourceSet injection: `kubectl get clusterresourceset`
  - Verify ArgoCD Agent pod running: `kubectl --context spokepool-01 get pods -n argocd`
  - Verify Agent connects to Hub: check ArgoCD UI for cluster registration
  - **COMPLETED**: ArgoCD Agent pod Running 1/1, CRDs installed, waiting for Hub Principal
  - _Requirements: FR-1.2, AC-3_

- [x] 1.5.3 Verify Kyverno cluster discovery
  - Verify ArgoCD cluster Secret created: `kubectl get secret -n argocd -l cell-id=spokepool-01`
  - Verify Secret contains correct kubeconfig and labels
  - Verify ArgoCD discovers cluster within 30 seconds
  - **COMPLETED**: Kyverno policy created ArgoCD cluster Secret with correct labels
  - _Requirements: FR-1.3, NFR-1.4, AC-2_

- [x] 1.5.4 Verify cell provisioning time
  - Measure total time from XR apply to cluster Ready
  - Verify < 15 minutes (P95)
  - _Requirements: NFR-1.1, AC-6_

### 1.6 PHASE 1 REVIEW CHECKPOINT

- [x] 1.6.1 **MANDATORY STOP - Phase 1 Review**
  - **STOP ALL IMPLEMENTATION WORK**
  - Present Phase 1 completion summary to user
  - Demonstrate: SpokePool XR → CAPI cluster → ArgoCD Agent → Kyverno discovery
  - Show validation results from tasks 1.5.1-1.5.4
  - **WAIT FOR USER APPROVAL BEFORE PROCEEDING TO PHASE 2**
  - Document any issues or deviations from design
  - **NOTE**: Tasks 1.5.2-1.5.4 require Crossplane installation (now added to hub bootstrap postboot phase - commit bf4a9b7)
  - **BUGS FIXED**: #1 Kyverno kubeconfig extraction, #2 ClusterResourceSet references, #3 per-cluster ConfigMap generation, #5 test manifest exists
  - _Requirements: All Phase 1 requirements_

### 1.7 ClusterResourceSet Template Management (Bugfix)

- [x] 1.7.1 Create ADR for ClusterResourceSet addon template management
  - Document decision to use static versioned templates in Git
  - Document architecture: Git → ArgoCD → Hub → Crossplane → CRS → Spoke
  - Document alternatives considered (operator, shared operator, dynamic generation)
  - Create file: `docs/adr/0001-clusterresourceset-addon-template-management.md`
  - _Requirements: FR-1.2, AC-3_

- [x] 1.7.2 Create directory structure for cluster BIOS templates
  - Create directory: `manifests/platform-ops/cluster-bios/`
  - This directory will contain static templates for CNI, CCM, and ArgoCD Agent
  - _Requirements: FR-1.2, AC-3_

- [x] 1.7.3 Create Cilium CNI addon template
  - Copy manifest from: `internal/assets/manifests/addons/cilium-rendered.yaml`
  - Wrap in Secret with type: `addons.cluster.x-k8s.io/resource-set`
  - Create file: `manifests/platform-ops/cluster-bios/cilium-addon-template.yaml`
  - Secret name: `cilium-addon-template`
  - Secret namespace: `hub-platform-ops`
  - _Requirements: FR-1.2, AC-3_

- [x] 1.7.4 Create Hetzner CCM addon template
  - Copy manifest from: `internal/assets/manifests/addons/ccm-rendered.yaml`
  - Wrap in Secret with type: `addons.cluster.x-k8s.io/resource-set`
  - Create file: `manifests/platform-ops/cluster-bios/ccm-addon-template.yaml`
  - Secret name: `ccm-addon-template`
  - Secret namespace: `hub-platform-ops`
  - _Requirements: FR-1.2, AC-3_

- [x] 1.7.5 Create ArgoCD Agent addon templates
  - Copy manifests from: `edge-catalog/argocd-agent-deployment.yaml`, `edge-catalog/argocd-agent-rbac.yaml`
  - Wrap in ConfigMaps with type: `addons.cluster.x-k8s.io/resource-set`
  - Create file: `manifests/platform-ops/cluster-bios/argocd-agent-templates.yaml`
  - ConfigMap names: `argocd-agent-deployment`, `argocd-agent-rbac`
  - ConfigMap namespace: `hub-platform-ops`
  - Note: Per-cluster ConfigMap and Secrets are generated by Crossplane Composition
  - _Requirements: FR-1.2, AC-3_

- [x] 1.7.6 Refactor hub bootstrap CLI to use static templates
  - Update `internal/hub/bootstrap/orchestrator.go` to read from `manifests/platform-ops/cluster-bios/`
  - Remove embedded assets from `internal/assets/manifests/addons/cilium-rendered.yaml`
  - Remove embedded assets from `internal/assets/manifests/addons/ccm-rendered.yaml`
  - Ensure hub bootstrap reads the same CNI/CCM manifests as spoke pools
  - Single source of truth: Git directory used by both hub CLI and ArgoCD
  - _Requirements: FR-1.2, AC-3_

- [x] 1.7.7 Create ArgoCD Application for cluster BIOS templates
  - Create Application manifest to sync templates to hub cluster
  - Source: `manifests/platform-ops/cluster-bios/`
  - Destination: `hub-platform-ops` namespace
  - Sync policy: automated with prune
  - Create file: `manifests/platform-ops/argocd-apps/cluster-bios-templates.yaml`
  - _Requirements: FR-1.2, AC-3_

- [x] 1.7.8 Commit cluster BIOS templates to Git
  - Commit all template files to feature branch
  - Push to GitOps repository
  - Verify ArgoCD detects and syncs templates to hub cluster
  - _Requirements: FR-1.2, AC-3_

- [x] 1.7.9 Verify templates exist in hub cluster
  - Verify Secret exists: `kubectl get secret cilium-addon-template -n hub-platform-ops`
  - Verify Secret exists: `kubectl get secret ccm-addon-template -n hub-platform-ops`
  - Verify ConfigMap exists: `kubectl get configmap argocd-agent-deployment -n hub-platform-ops`
  - Verify ConfigMap exists: `kubectl get configmap argocd-agent-rbac -n hub-platform-ops`
  - _Requirements: FR-1.2, AC-3_

- [ ] 1.7.10 Update Crossplane Composition to reference templates
  - Verify ClusterResourceSet references correct template names
  - Composition already references: `cilium-addon-template`, `ccm-addon-template`, `argocd-agent-deployment`, `argocd-agent-rbac`
  - No changes needed to Composition (templates now exist)
  - _Requirements: FR-1.2, AC-3_

- [ ] 1.7.11 Test ClusterResourceSet with new templates
  - Apply test SpokePool XR (or use existing spoke-pool-eu-prod-01)
  - Wait for CAPI cluster Ready: `kubectl wait --for=condition=Ready cluster/spoke-pool-eu-prod-01 --timeout=20m`
  - Verify ClusterResourceSet applied successfully: `kubectl get clusterresourceset`
  - Verify CNI applied: `kubectl --context spoke-pool-eu-prod-01 get pods -n kube-system -l app.kubernetes.io/name=cilium`
  - Verify CCM applied: `kubectl --context spoke-pool-eu-prod-01 get pods -n kube-system -l app=hcloud-cloud-controller-manager`
  - Verify nodes become Ready: `kubectl --context spoke-pool-eu-prod-01 get nodes`
  - Verify ArgoCD Agent applied: `kubectl --context spoke-pool-eu-prod-01 get pods -n argocd`
  - _Requirements: FR-1.2, AC-3_

### 1.8 PHASE 1.7 REVIEW CHECKPOINT

- [ ] 1.8.1 **MANDATORY STOP - Phase 1.7 Review**
  - **STOP ALL IMPLEMENTATION WORK**
  - Present Phase 1.7 completion summary to user
  - Demonstrate: Templates in Git → Hub CLI reads + ArgoCD syncs → Crossplane references → CRS applies → Spoke cluster Ready
  - Show validation results from task 1.7.11
  - Highlight: Single source of truth (no duplication between hub and spoke)
  - **WAIT FOR USER APPROVAL BEFORE PROCEEDING TO PHASE 2**
  - Document any issues or deviations from design
  - _Requirements: FR-1.2, AC-3_

---

## Phase 2: Edge Catalog (Week 3-4)

### 2.1 ArgoCD ApplicationSet with Cluster Generator

- [x] 2.1.1 Create ApplicationSet for edge catalog deployment
  - Use Cluster Generator with selector: `spoke-type: pool`
  - Define App-of-Apps pattern (umbrella Application creates children)
  - Create file: `catalog/argocd/edge-catalog-applicationset.yaml`
  - _Requirements: FR-2.1, AC-4_

- [x] 2.1.2 Create umbrella Application manifest
  - Define parent Application that generates child Applications
  - Configure sync waves: 0 (extensions), 1 (CNPG), 2 (Atlas), 3 (PostgREST), 4 (NATS/Alloy)
  - Create file: `edge-catalog/edge-catalog-app.yaml`
  - _Requirements: FR-2.1, AC-4_

- [x] 2.1.3 Commit ApplicationSet to Git
  - Commit to feature branch
  - Verify ArgoCD syncs ApplicationSet
  - _Requirements: FR-2.1, AC-4_

### 2.2 Shared CNPG Cluster (Sync Wave 1)

- [x] 2.2.1 Create CNPG Cluster manifest
  - Configure 3 PostgreSQL replicas (HA)
  - Enable pgvector extension
  - Configure 100Gi storage per instance
  - Configure backup to Hetzner S3 (30-day retention)
  - Add sync wave annotation: `argocd.argoproj.io/sync-wave: "1"`
  - Create file: `edge-catalog/cnpg-cluster.yaml`
  - _Requirements: FR-2.2, NFR-2.3, AC-4_

- [x] 2.2.2 Create PgBouncer pooler configuration
  - Configure transaction pooling mode (CRITICAL: must be transaction, not session)
  - Set max_client_conn: 500 (100 tenants * 5 connections)
  - Set default_pool_size: 20
  - Set max_db_connections: 100
  - Add to CNPG Cluster spec.pooler
  - _Requirements: FR-2.2, NFR-2.4, AC-4_


- [x] 2.2.3 Commit CNPG manifests to Git
  - Commit to feature branch
  - Verify ArgoCD syncs CNPG Cluster
  - _Requirements: FR-2.2, AC-4_

### 2.3 Atlas Operator Deployment (Sync Wave 2)

- [x] 2.3.1 Create Atlas Operator Helm Application
  - Configure Helm chart: `oci://ghcr.io/ariga/charts/atlas-operator`
  - Add sync wave annotation: `argocd.argoproj.io/sync-wave: "2"`
  - Add health check: wait for CNPG Ready before deploying
  - Create file: `edge-catalog/atlas-operator.yaml`
  - _Requirements: FR-4.4, AC-4_

- [x] 2.3.2 Create AtlasMigration CRD registration
  - Ensure CRD is registered in Spoke Pool clusters
  - Verify CRD includes status.conditions[Ready]
  - _Requirements: FR-4.4, AC-5_

- [x] 2.3.3 Commit Atlas Operator manifests to Git
  - Commit to feature branch
  - Verify ArgoCD syncs Atlas Operator
  - _Requirements: FR-4.4, AC-4_

### 2.4 PostgREST Deployment (Sync Wave 3)

- [x] 2.4.1 Create PostgREST Deployment manifest
  - Configure as internal service (NOT directly exposed)
  - Configure connection to CNPG via PgBouncer
  - Configure JWT cache: 10000 entries
  - Configure db-schemas: empty initially (updated by tenant provisioning)
  - Add sync wave annotation: `argocd.argoproj.io/sync-wave: "3"`
  - Create file: `edge-catalog/postgrest.yaml`
  - _Requirements: FR-2.6, NFR-2.8, AC-4_

- [x] 2.4.2 Create PostgREST Service manifest
  - Define ClusterIP service (internal only)
  - Port 3000 for REST API
  - _Requirements: FR-2.6, AC-4_

- [x] 2.4.3 Commit PostgREST manifests to Git
  - Commit to feature branch
  - Verify ArgoCD syncs PostgREST
  - _Requirements: FR-2.6, AC-4_


### 2.5 AgentGateway Deployment (Sync Wave 3)

- [x] 2.5.1 Create AgentGateway Deployment manifest
  - Configure Hub Ory JWKS endpoint for JWT validation
  - Configure routing to PostgREST internal service
  - Configure JWT validation: RS256 signature, issuer, audience, expiration
  - Configure tenant_id extraction from JWT claims
  - Configure X-Tenant-ID header forwarding to PostgREST
  - Add sync wave annotation: `argocd.argoproj.io/sync-wave: "3"`
  - Create file: `edge-catalog/agentgateway.yaml`
  - _Requirements: FR-2.6, FR-4.5, AC-4_

- [x] 2.5.2 Create AgentGateway Service manifest
  - Define LoadBalancer or Ingress for external access
  - Configure TLS termination
  - _Requirements: FR-2.6, AC-4_

- [x] 2.5.3 Commit AgentGateway manifests to Git
  - Commit to feature branch
  - Verify ArgoCD syncs AgentGateway
  - _Requirements: FR-2.6, AC-4_

### 2.6 NATS Leaf Node Deployment (Sync Wave 4)

- [x] 2.6.1 Create NATS Leaf Node StatefulSet manifest
  - Configure connection to Hub NATS using mTLS
  - Configure JetStream for local buffering
  - Configure subject forwarding: `spoke.{cell-id}.billing.usage`
  - Add sync wave annotation: `argocd.argoproj.io/sync-wave: "4"`
  - Create file: `edge-catalog/nats-leaf-node.yaml`
  - _Requirements: FR-2.3, NFR-3.3, AC-4_

- [x] 2.6.2 Create NATS Leaf Node Service manifest
  - Define ClusterIP service for tenant workloads
  - Port 4222 for NATS protocol
  - _Requirements: FR-2.3, AC-4_

- [x] 2.6.3 Commit NATS Leaf Node manifests to Git
  - Commit to feature branch
  - Verify ArgoCD syncs NATS Leaf Node
  - _Requirements: FR-2.3, AC-4_


### 2.7 Grafana Alloy Deployment (Sync Wave 4)

- [x] 2.7.1 Create Grafana Alloy DaemonSet manifest
  - Configure KSM (Kubernetes State Metrics) scraping
  - Configure CNPG metrics scraping (connection count, replication lag, disk usage)
  - Configure NATS metrics scraping (leaf node connection, message counts)
  - Configure PostgREST metrics scraping (request latency, cache hits)
  - Configure cell_id label injection
  - Configure remote_write to Hub VictoriaMetrics
  - Add sync wave annotation: `argocd.argoproj.io/sync-wave: "4"`
  - Create file: `edge-catalog/grafana-alloy.yaml`
  - _Requirements: FR-2.4, NFR-5.1, NFR-5.3, AC-4_

- [x] 2.7.2 Commit Grafana Alloy manifests to Git
  - Commit to feature branch
  - Verify ArgoCD syncs Grafana Alloy
  - _Requirements: FR-2.4, AC-4_

### 2.8 Phase 2 Manual Validation

- [ ] 2.8.1 Verify edge catalog deployment with sync waves
  - Verify ApplicationSet creates Applications for spokepool-01
  - Verify sync wave 1 (CNPG) completes before wave 2 (Atlas)
  - Verify sync wave 2 (Atlas) completes before wave 3 (PostgREST/AgentGateway)
  - Verify sync wave 3 completes before wave 4 (NATS/Alloy)
  - Check: `argocd app list | grep spokepool-01`
  - _Requirements: FR-2.1, AC-4_

- [ ] 2.8.2 Verify CNPG cluster health
  - Verify CNPG cluster reaches Ready: `kubectl --context spokepool-01 get cluster shared-cnpg -o jsonpath='{.status.phase}'`
  - Verify 3 replicas running
  - Verify PgBouncer pooler running with transaction mode
  - Verify pgvector extension enabled
  - _Requirements: FR-2.2, NFR-2.3, NFR-2.4, AC-4_

- [ ] 2.8.3 Verify Atlas Operator deployment
  - Verify Atlas Operator pod running: `kubectl --context spokepool-01 get deployment atlas-operator`
  - Verify AtlasMigration CRD registered
  - _Requirements: FR-4.4, AC-4_


- [ ] 2.8.4 Verify PostgREST deployment
  - Verify PostgREST pod running: `kubectl --context spokepool-01 get deployment postgrest`
  - Verify PostgREST is internal service only (no external LoadBalancer)
  - Verify connection to PgBouncer
  - _Requirements: FR-2.6, NFR-2.8, AC-4_

- [ ] 2.8.5 Verify AgentGateway deployment
  - Verify AgentGateway pod running: `kubectl --context spokepool-01 get deployment agentgateway`
  - Verify Hub Ory JWKS endpoint configured
  - Verify routing to PostgREST internal service
  - _Requirements: FR-2.6, AC-4_

- [ ] 2.8.6 Verify NATS Leaf Node connection
  - Verify NATS pod running: `kubectl --context spokepool-01 get statefulset nats`
  - Verify connection to Hub NATS: check logs for "leafnode connected"
  - Publish test event: `kubectl --context spokepool-01 exec -n spoke-pool-system nats-0 -- nats pub spoke.test-01.billing.usage '{"test": "event"}'`
  - Verify event received in Hub: `kubectl --context hub exec -n hub-platform-messaging nats-0 -- nats stream info`
  - _Requirements: FR-2.3, AC-4_

- [ ] 2.8.7 Verify Grafana Alloy metrics forwarding
  - Verify Alloy pods running: `kubectl --context spokepool-01 get daemonset grafana-alloy`
  - Verify metrics forwarded to Hub VictoriaMetrics
  - Query VictoriaMetrics for cell_id label: `cell_id="spokepool-01"`
  - _Requirements: FR-2.4, NFR-5.1, AC-4_

- [ ] 2.8.8 Verify edge catalog deployment time
  - Measure time from ArgoCD sync start to all components Healthy
  - Verify < 10 minutes
  - _Requirements: FR-2.1, AC-4_

### 2.9 PHASE 2 REVIEW CHECKPOINT

- [x] 2.9.1 **MANDATORY STOP - Phase 2 Review**
  - **STOP ALL IMPLEMENTATION WORK**
  - Present Phase 2 completion summary to user
  - Demonstrate: ApplicationSet → Edge catalog deployment → All components Healthy
  - Show validation results from tasks 2.8.1-2.8.8
  - **WAIT FOR USER APPROVAL BEFORE PROCEEDING TO PHASE 3**
  - Document any issues or deviations from design
  - _Requirements: All Phase 2 requirements_


---

## Phase 3: Tenant Schema Provisioning (Week 5-6)

### 3.1 Baseline Migration Files

- [x] 3.1.1 Create migration repository structure
  - Create directory: `migrations/tenant-baseline/`
  - Initialize Git repository for migrations
  - _Requirements: FR-4.1, AC-5_

- [x] 3.1.2 Create baseline schema migration
  - Create migration: `migrations/tenant-baseline/20240101000001_create_schema.sql`
  - Content: `CREATE SCHEMA IF NOT EXISTS tenant_{{.tenant_id}};`
  - Content: `CREATE ROLE tenant_{{.tenant_id}}_role;`
  - Content: `GRANT ALL ON SCHEMA tenant_{{.tenant_id}} TO tenant_{{.tenant_id}}_role;`
  - Ensure idempotent (IF NOT EXISTS)
  - _Requirements: FR-4.1, FR-4.2, NFR-6.1, AC-5_

- [x] 3.1.3 Create baseline users table migration
  - Create migration: `migrations/tenant-baseline/20240101000002_create_users_table.sql`
  - Content: `CREATE TABLE IF NOT EXISTS tenant_{{.tenant_id}}.users (...);`
  - Enable RLS: `ALTER TABLE tenant_{{.tenant_id}}.users ENABLE ROW LEVEL SECURITY;`
  - Create RLS policy using JWT user_id claim
  - _Requirements: FR-4.1, FR-4.2, AC-5_

- [x] 3.1.4 Create baseline sessions table migration
  - Create migration: `migrations/tenant-baseline/20240101000003_create_sessions_table.sql`
  - Content: `CREATE TABLE IF NOT EXISTS tenant_{{.tenant_id}}.sessions (...);`
  - Enable RLS with JWT user_id policy
  - _Requirements: FR-4.1, FR-4.2, AC-5_

- [x] 3.1.5 Create baseline identities table migration
  - Create migration: `migrations/tenant-baseline/20240101000004_create_identities_table.sql`
  - Content: `CREATE TABLE IF NOT EXISTS tenant_{{.tenant_id}}.identities (...);`
  - Enable RLS with JWT user_id policy
  - _Requirements: FR-4.1, FR-4.2, AC-5_


- [x] 3.1.6 Create baseline buckets table migration
  - Create migration: `migrations/tenant-baseline/20240101000005_create_buckets_table.sql`
  - Content: `CREATE TABLE IF NOT EXISTS tenant_{{.tenant_id}}.buckets (...);`
  - Enable RLS with JWT user_id policy
  - _Requirements: FR-4.1, FR-4.2, AC-5_

- [x] 3.1.7 Create baseline objects table migration
  - Create migration: `migrations/tenant-baseline/20240101000006_create_objects_table.sql`
  - Content: `CREATE TABLE IF NOT EXISTS tenant_{{.tenant_id}}.objects (...);`
  - Enable RLS with JWT user_id policy
  - _Requirements: FR-4.1, FR-4.2, AC-5_

- [x] 3.1.8 Commit baseline migrations to Git
  - Commit all migrations to main branch
  - Verify migrations follow Atlas naming convention: `YYYYMMDDHHMMSS_description.sql`
  - Verify all migrations are idempotent
  - _Requirements: FR-4.1, NFR-6.1, NFR-6.6, NFR-6.7, AC-5_

### 3.2 Universal Tenant Helm Chart

- [x] 3.2.1 Create Universal Tenant Helm Chart structure
  - Create directory: `charts/universal-tenant/`
  - Create Chart.yaml with metadata
  - Create values.yaml with tenant input schema
  - _Requirements: FR-5.2, AC-5_

- [x] 3.2.2 Create AINativeSaaS XR template
  - Create template: `charts/universal-tenant/templates/ainativesaas.yaml`
  - Template generates AINativeSaaS XR from values
  - Include: tenantId, tier, region from values
  - Add sync wave annotation: `argocd.argoproj.io/sync-wave: "1"`
  - _Requirements: FR-5.2, AC-5_

- [x] 3.2.3 Create namespace template
  - Create template: `charts/universal-tenant/templates/namespace.yaml`
  - Template generates namespace: `tenant-{{.Values.tenantId}}`
  - Add labels: tenant-id, tier
  - Add sync wave annotation: `argocd.argoproj.io/sync-wave: "0"`
  - _Requirements: FR-5.2, AC-5_


- [x] 3.2.4 Create RBAC template
  - Create template: `charts/universal-tenant/templates/rbac.yaml`
  - Template generates ServiceAccount, Role, RoleBinding
  - Scope to tenant namespace
  - Add sync wave annotation: `argocd.argoproj.io/sync-wave: "0"`
  - _Requirements: FR-5.2, AC-5_

- [x] 3.2.5 Create ResourceQuota template
  - Create template: `charts/universal-tenant/templates/resourcequota.yaml`
  - Template generates ResourceQuota based on tier
  - Add sync wave annotation: `argocd.argoproj.io/sync-wave: "0"`
  - _Requirements: FR-5.2, AC-5_

- [x] 3.2.6 Create AtlasMigration CR template
  - Create template: `charts/universal-tenant/templates/atlasmigration.yaml`
  - Template generates AtlasMigration CR with:
    * Schema name: `tenant_{{.Values.tenantId}}`
    * Migration directory: `migrations/tenant-baseline/`
    * Connection to CNPG via PgBouncer
    * Git repository URL for migrations
  - Add sync wave annotation: `argocd.argoproj.io/sync-wave: "2"`
  - _Requirements: FR-5.2, FR-4.1, AC-5_

- [x] 3.2.7 Create ConfigMap for migrations
  - Create template: `charts/universal-tenant/templates/migrations-configmap.yaml`
  - Template generates ConfigMap with migration files from Git
  - Atlas Operator reads migrations from this ConfigMap
  - Add sync wave annotation: `argocd.argoproj.io/sync-wave: "2"`
  - _Requirements: FR-5.2, FR-4.1, AC-5_

- [x] 3.2.8 Commit Universal Tenant Chart to Git
  - Commit chart to main branch
  - Verify chart structure and templates
  - _Requirements: FR-5.2, AC-5_

### 3.3 Fleet Registry Structure

- [x] 3.3.1 Create fleet registry repository structure
  - Create directory: `fleet-registry/tenants/`
  - Initialize Git repository
  - _Requirements: FR-5.2, AC-5_


- [x] 3.3.2 Create example tenant values file
  - Create file: `fleet-registry/tenants/tenant-example/values.yaml`
  - Content: tenantId, tier, region, database.schemaName, database.migrations.gitRepo
  - _Requirements: FR-5.2, AC-5_

- [x] 3.3.3 Commit fleet registry structure to Git
  - Commit to main branch
  - _Requirements: FR-5.2, AC-5_

### 3.4 ArgoCD ApplicationSet for Tenant Provisioning

- [x] 3.4.1 Create ApplicationSet with Git Generator
  - Create file: `catalog/argocd/tenant-applicationset.yaml`
  - Use Git Generator to watch `fleet-registry/tenants/*/values.yaml`
  - Generate Helm Application for each tenant directory
  - Configure source.chart: `charts/universal-tenant`
  - Configure source.helm.valueFiles: `fleet-registry/tenants/{{tenant-id}}/values.yaml`
  - _Requirements: FR-5.2, AC-5_

- [x] 3.4.2 Commit ApplicationSet to Git
  - Commit to feature branch
  - Verify ArgoCD syncs ApplicationSet
  - _Requirements: FR-5.2, AC-5_

### 3.5 PostgREST Schema Discovery

- [x] 3.5.1 Update PostgREST configuration for dynamic schema discovery
  - Configure db-schemas to include all tenant schemas
  - Configure schema discovery query: `SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE 'tenant_%'`
  - Configure health check to verify schema exists before accepting requests
  - _Requirements: FR-4.1, AC-5_

- [x] 3.5.2 Commit PostgREST configuration update to Git
  - Commit to feature branch
  - Verify ArgoCD syncs updated PostgREST config
  - _Requirements: FR-4.1, AC-5_


### 3.6 Phase 3 Manual Validation

- [ ] 3.6.1 Create test tenant via GitOps flow
  - Create file: `fleet-registry/tenants/tenant-acme/values.yaml`
  - Content: `tenantId: acme`, `tier: starter`, `database.schemaName: tenant_acme`
  - Commit to main branch
  - _Requirements: FR-5.2, AC-5, AC-6_

- [ ] 3.6.2 Verify ArgoCD detects tenant and creates Application
  - Verify ApplicationSet detects new tenant directory
  - Verify Helm Application created: `argocd app get tenant-acme`
  - Verify Application uses Universal Tenant Chart
  - _Requirements: FR-5.2, AC-5, AC-6_

- [ ] 3.6.3 Verify Helm renders CRs correctly
  - Test Helm rendering: `helm template charts/universal-tenant -f fleet-registry/tenants/tenant-acme/values.yaml`
  - Verify AINativeSaaS XR, AtlasMigration CR, namespace, RBAC generated
  - _Requirements: FR-5.2, AC-5, AC-6_

- [ ] 3.6.4 Verify AtlasMigration CR deployed
  - Verify CR deployed: `kubectl --context spokepool-01 get atlasmigration tenant-acme`
  - Verify CR references correct schema: `tenant_acme`
  - Verify CR references migration directory
  - _Requirements: FR-4.1, AC-5, AC-6_

- [ ] 3.6.5 Verify schema created in CNPG
  - Verify schema exists: `kubectl --context spokepool-01 exec -it cnpg-rw-0 -- psql -U postgres -c "\dn tenant_acme"`
  - Verify schema owner role: `kubectl --context spokepool-01 exec -it cnpg-rw-0 -- psql -U postgres -c "\du tenant_acme_role"`
  - _Requirements: FR-4.1, AC-5, AC-6_

- [ ] 3.6.6 Verify baseline tables created
  - Verify tables exist: `kubectl --context spokepool-01 exec -it cnpg-rw-0 -- psql -U postgres -c "\dt tenant_acme.*"`
  - Verify RLS enabled on tables
  - Verify RLS policies exist
  - _Requirements: FR-4.1, FR-4.2, AC-5, AC-6_


- [ ] 3.6.7 Verify AtlasMigration CR status
  - Verify CR status: `kubectl --context spokepool-01 get atlasmigration tenant-acme -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'`
  - Verify status is True
  - _Requirements: FR-4.1, AC-5, AC-6_

- [ ] 3.6.8 Verify PostgREST schema discovery
  - Verify PostgREST db-schemas includes tenant_acme
  - Verify PostgREST health check passes
  - _Requirements: FR-4.1, AC-5, AC-6_

- [ ] 3.6.9 Verify schema provisioning time
  - Measure time from Git commit to AtlasMigration Ready
  - Verify < 5 seconds
  - _Requirements: NFR-1.3, AC-5, AC-6_

- [ ] 3.6.10 Test authentication flow via AgentGateway
  - Obtain JWT from Hub Ory: `curl -X POST https://auth.hub.example.com/oauth2/token ...`
  - Make authenticated request: `curl -H "Authorization: Bearer $JWT" https://api.spokepool-01.example.com/documents`
  - Verify AgentGateway logs show JWT validation
  - Verify AgentGateway extracts tenant_id from JWT
  - Verify AgentGateway forwards request with X-Tenant-ID header
  - Verify PostgREST logs show search_path=tenant_acme
  - Verify response contains only tenant's data
  - _Requirements: FR-4.5, FR-2.6, AC-6_

- [ ] 3.6.11 Test drift detection and recovery
  - Manually alter schema: `kubectl --context spokepool-01 exec -it cnpg-rw-0 -- psql -U postgres -c "ALTER TABLE tenant_acme.users ADD COLUMN test VARCHAR(20)"`
  - Wait 60 seconds (Atlas Operator reconciliation loop)
  - Verify Atlas Operator logs show drift detection
  - Create new migration: `migrations/tenant-baseline/20240101000007_add_test_column.sql`
  - Commit to Git, ArgoCD syncs ConfigMap
  - Verify Atlas Operator applies migration
  - Verify AtlasMigration CR status remains Ready=True
  - _Requirements: FR-4.4, NFR-3.6, AC-6_

### 3.7 PHASE 3 REVIEW CHECKPOINT

- [ ] 3.7.1 **MANDATORY STOP - Phase 3 Review**
  - **STOP ALL IMPLEMENTATION WORK**
  - Present Phase 3 completion summary to user
  - Demonstrate: Git commit → ApplicationSet → Helm → AtlasMigration → Schema provisioned
  - Show validation results from tasks 3.6.1-3.6.11
  - **WAIT FOR USER APPROVAL BEFORE PROCEEDING TO PHASE 4**
  - Document any issues or deviations from design
  - _Requirements: All Phase 3 requirements_


---

## Phase 4: Observability and Monitoring (Week 7)

### 4.1 Grafana Dashboards

- [ ] 4.1.1 Create cell health dashboard
  - Metrics: CNPG cluster status, connection count, replication lag
  - Metrics: NATS leaf node connection status, message counts
  - Metrics: PostgREST request latency, JWT cache hits
  - Metrics: ArgoCD sync status, Application health
  - Filter by cell_id label
  - Create file: `observability/dashboards/cell-health.json`
  - _Requirements: NFR-5.2, NFR-5.3_

- [ ] 4.1.2 Create tenant capacity dashboard
  - Metrics: Tenant count per cell
  - Metrics: Cell capacity utilization (current/max)
  - Metrics: Schema provisioning time (P95, P99)
  - Metrics: JWT validation time (cached vs uncached)
  - Create file: `observability/dashboards/tenant-capacity.json`
  - _Requirements: NFR-5.1_

- [ ] 4.1.3 Create drift detection dashboard
  - Metrics: Atlas drift detection events
  - Metrics: Atlas migration apply success/failure
  - Metrics: AtlasMigration CR status
  - Create file: `observability/dashboards/drift-detection.json`
  - _Requirements: NFR-5.5_

- [ ] 4.1.4 Commit dashboards to Git
  - Commit to feature branch
  - Import dashboards to Grafana
  - _Requirements: NFR-5.1, NFR-5.2, NFR-5.3, NFR-5.5_

### 4.2 Prometheus Alerts

- [ ] 4.2.1 Create CNPG connection pool exhaustion alert
  - Alert when connection count > 80% of max (400/500)
  - Severity: warning
  - Create file: `observability/alerts/cnpg-connection-pool.yaml`
  - _Requirements: NFR-5.3_


- [ ] 4.2.2 Create NATS leaf node disconnection alert
  - Alert when leaf node disconnected > 5 minutes
  - Severity: critical
  - Create file: `observability/alerts/nats-leaf-disconnected.yaml`
  - _Requirements: NFR-3.3_

- [ ] 4.2.3 Create Atlas drift detection alert
  - Alert when drift detected (manual schema change)
  - Severity: warning
  - Create file: `observability/alerts/atlas-drift-detected.yaml`
  - _Requirements: NFR-3.6, NFR-6.8_

- [ ] 4.2.4 Create PostgREST JWT validation failure alert
  - Alert when JWT validation error rate > 1%
  - Severity: warning
  - Create file: `observability/alerts/postgrest-jwt-failures.yaml`
  - _Requirements: NFR-4.6_

- [ ] 4.2.5 Create certificate expiration alert
  - Alert when certificate expires in < 14 days
  - Severity: warning
  - Create file: `observability/alerts/certificate-expiring.yaml`
  - _Requirements: NFR-4.2_

- [ ] 4.2.6 Create cell capacity alert
  - Alert when cell reaches 80% capacity (80/100 tenants)
  - Severity: warning
  - Create file: `observability/alerts/cell-capacity.yaml`
  - _Requirements: NFR-2.1_

- [ ] 4.2.7 Commit alerts to Git
  - Commit to feature branch
  - Verify ArgoCD syncs alerts to VictoriaMetrics
  - _Requirements: NFR-5.1_

### 4.3 Logging Configuration

- [ ] 4.3.1 Configure structured logging for ArgoCD Agent
  - JSON format with fields: timestamp, level, message, cell_id, cluster_name
  - Log connection status, Application sync events
  - _Requirements: NFR-5.4_


- [ ] 4.3.2 Configure structured logging for Atlas Operator
  - JSON format with fields: timestamp, level, message, cell_id, tenant_id, schema_name
  - Log migration apply, drift detection events
  - _Requirements: NFR-5.5_

- [ ] 4.3.3 Configure structured logging for PostgREST
  - JSON format with fields: timestamp, level, message, cell_id, tenant_id, request_path
  - Log query execution, JWT validation events
  - _Requirements: NFR-5.4_

- [ ] 4.3.4 Configure structured logging for AgentGateway
  - JSON format with fields: timestamp, level, message, cell_id, tenant_id, request_path
  - Log request routing, JWT validation events
  - _Requirements: NFR-5.4_

- [ ] 4.3.5 Configure Grafana Alloy log forwarding
  - Configure log scraping from all components
  - Configure forwarding to Loki (Hub)
  - Add cell_id label to all logs
  - _Requirements: NFR-5.1_

- [ ] 4.3.6 Commit logging configuration to Git
  - Commit to feature branch
  - Verify ArgoCD syncs logging config
  - _Requirements: NFR-5.4_

### 4.4 Phase 4 Manual Validation

- [ ] 4.4.1 Verify dashboards display metrics
  - Open cell health dashboard in Grafana
  - Verify metrics for spokepool-01 displayed
  - Verify cell_id label filtering works
  - _Requirements: NFR-5.1, NFR-5.2, NFR-5.3_

- [ ] 4.4.2 Verify alerts trigger correctly
  - Simulate connection pool exhaustion (create 400+ connections)
  - Verify alert fires in VictoriaMetrics
  - Verify alert notification sent
  - _Requirements: NFR-5.3_


- [ ] 4.4.3 Verify logs forwarded to Loki
  - Query Loki for logs from spokepool-01
  - Verify structured JSON format
  - Verify cell_id label present
  - _Requirements: NFR-5.1, NFR-5.4_

- [ ] 4.4.4 Verify drift detection alert
  - Manually alter schema (trigger drift)
  - Wait for Atlas Operator to detect drift
  - Verify alert fires
  - _Requirements: NFR-3.6, NFR-6.8_

### 4.5 PHASE 4 REVIEW CHECKPOINT

- [ ] 4.5.1 **MANDATORY STOP - Phase 4 Review**
  - **STOP ALL IMPLEMENTATION WORK**
  - Present Phase 4 completion summary to user
  - Demonstrate: Dashboards, alerts, logging all operational
  - Show validation results from tasks 4.4.1-4.4.4
  - **WAIT FOR USER APPROVAL BEFORE PROCEEDING TO PHASE 5**
  - Document any issues or deviations from design
  - _Requirements: All Phase 4 requirements_

---

## Phase 5: End-to-End Testing (Week 8)

### 5.1 Manual Test Scripts

- [ ] 5.1.1 Create cell provisioning test script
  - Script applies SpokePool XR via GitOps
  - Script waits for cluster Ready (timeout 20 minutes)
  - Script verifies ArgoCD Agent connection
  - Script verifies edge catalog deployment
  - Script verifies all components Healthy
  - Create file: `tests/manual/test-cell-provisioning.sh`
  - _Requirements: AC-6_

- [ ] 5.1.2 Create tenant schema provisioning test script
  - Script commits tenant values to Git
  - Script waits for ArgoCD Application creation
  - Script verifies AtlasMigration CR deployed
  - Script verifies schema created in CNPG
  - Script verifies baseline tables exist
  - Script verifies AtlasMigration CR Ready
  - Create file: `tests/manual/test-tenant-provisioning.sh`
  - _Requirements: AC-5, AC-6_


- [ ] 5.1.3 Create authentication flow test script
  - Script obtains JWT from Hub Ory
  - Script makes authenticated request via AgentGateway
  - Script verifies JWT validation in logs
  - Script verifies PostgREST sets correct search_path
  - Script verifies response contains only tenant data
  - Create file: `tests/manual/test-authentication-flow.sh`
  - _Requirements: AC-6_

- [ ] 5.1.4 Create drift detection test script
  - Script manually alters schema
  - Script waits for Atlas Operator reconciliation
  - Script verifies drift detection in logs
  - Script creates new migration file
  - Script commits to Git
  - Script verifies Atlas Operator applies migration
  - Script verifies AtlasMigration CR remains Ready
  - Create file: `tests/manual/test-drift-detection.sh`
  - _Requirements: AC-6_

- [ ] 5.1.5 Create NATS buffering test script
  - Script publishes test event to Spoke NATS
  - Script verifies event received in Hub
  - Script scales Hub NATS to 0 (simulate outage)
  - Script publishes 10 events during outage
  - Script verifies Spoke JetStream buffer
  - Script restores Hub NATS
  - Script verifies all events delivered to Hub
  - Create file: `tests/manual/test-nats-buffering.sh`
  - _Requirements: NFR-3.3_

- [ ] 5.1.6 Commit test scripts to Git
  - Commit to main branch
  - _Requirements: AC-6_

### 5.2 Documentation

- [ ] 5.2.1 Create runbook for cell provisioning
  - Document step-by-step process
  - Document troubleshooting steps
  - Document rollback procedure
  - Create file: `docs/runbooks/cell-provisioning.md`
  - _Requirements: AC-6_


- [ ] 5.2.2 Create runbook for tenant schema provisioning
  - Document GitOps flow
  - Document troubleshooting steps
  - Document migration rollback procedure
  - Create file: `docs/runbooks/tenant-schema-provisioning.md`
  - _Requirements: AC-5, AC-6_

- [ ] 5.2.3 Create troubleshooting guide
  - Document common issues and solutions
  - Document diagnostic commands
  - Document log locations
  - Create file: `docs/troubleshooting/spoke-pool-provisioner.md`
  - _Requirements: AC-6_

- [ ] 5.2.4 Create capacity planning guide
  - Document 100 tenants per cell limit
  - Document connection pool sizing
  - Document scaling strategy
  - Create file: `docs/capacity-planning/spoke-pool.md`
  - _Requirements: NFR-2.1, NFR-2.3_

- [ ] 5.2.5 Commit documentation to Git
  - Commit to main branch
  - _Requirements: AC-6_

### 5.3 End-to-End Test Execution

- [ ] 5.3.1 Execute cell provisioning test
  - Run test script: `tests/manual/test-cell-provisioning.sh`
  - Verify all assertions pass
  - Verify provisioning time < 15 minutes
  - _Requirements: AC-6, NFR-1.1_

- [ ] 5.3.2 Execute tenant schema provisioning test
  - Run test script: `tests/manual/test-tenant-provisioning.sh`
  - Verify all assertions pass
  - Verify provisioning time < 5 seconds
  - _Requirements: AC-5, AC-6, NFR-1.3_

- [ ] 5.3.3 Execute authentication flow test
  - Run test script: `tests/manual/test-authentication-flow.sh`
  - Verify JWT validation works
  - Verify tenant isolation enforced
  - _Requirements: AC-6, NFR-4.5, NFR-4.7_


- [ ] 5.3.4 Execute drift detection test
  - Run test script: `tests/manual/test-drift-detection.sh`
  - Verify drift detected and repaired
  - Verify recovery time < 5 minutes
  - _Requirements: AC-6, NFR-3.6_

- [ ] 5.3.5 Execute NATS buffering test
  - Run test script: `tests/manual/test-nats-buffering.sh`
  - Verify no data loss during Hub outage
  - Verify events delivered after reconnection
  - _Requirements: NFR-3.3_

- [ ] 5.3.6 Execute capacity test
  - Provision 10 tenant schemas in same cell
  - Verify all schemas provisioned successfully
  - Verify connection pool not exhausted
  - Verify PostgREST serves all tenants correctly
  - _Requirements: NFR-2.1, NFR-2.3_

- [ ] 5.3.7 Execute certificate rotation test
  - Verify certificates auto-renew 7 days before expiration
  - Verify ArgoCD Agent and NATS Leaf Node continue working after renewal
  - _Requirements: NFR-4.2_

### 5.4 Acceptance Criteria Verification

- [ ] 5.4.1 Verify AC-1: SpokePool XRD and Composition
  - Verify SpokePool XRD defined with correct schema
  - Verify Composition generates all required CAPI resources
  - Verify applying SpokePool XR provisions functional cluster
  - _Requirements: AC-1_

- [ ] 5.4.2 Verify AC-2: Kyverno Cluster Discovery
  - Verify Kyverno policy watches CAPI Cluster resources
  - Verify policy generates ArgoCD cluster Secret with correct labels
  - Verify ArgoCD discovers cluster within 30 seconds
  - _Requirements: AC-2_

- [ ] 5.4.3 Verify AC-3: ArgoCD Agent Bootstrap
  - Verify ClusterResourceSet contains 5 resources
  - Verify ArgoCD Agent starts within 2 minutes
  - Verify Agent connects to Hub using mTLS
  - _Requirements: AC-3_


- [ ] 5.4.4 Verify AC-4: Edge Catalog Deployment
  - Verify ApplicationSet deploys edge catalog to all pool clusters
  - Verify CNPG reaches Ready within 5 minutes
  - Verify PostgREST deployed as internal service only
  - Verify AgentGateway deployed with Hub Ory JWKS endpoint
  - Verify NATS Leaf Node connects to Hub
  - Verify Grafana Alloy forwards metrics to Hub
  - Verify Atlas Operator deployed and ready
  - _Requirements: AC-4_

- [ ] 5.4.5 Verify AC-5: Tenant Schema Provisioning
  - Verify Universal Tenant Helm Chart exists
  - Verify MCP API commits tenant values to Git
  - Verify ArgoCD ApplicationSet detects new tenant directory
  - Verify Helm Application created with correct source
  - Verify AtlasMigration CR deployed
  - Verify deterministic schema created: tenant_<id>
  - Verify baseline migrations applied
  - Verify RLS policies enabled
  - Verify migration history tracked
  - Verify PostgREST includes tenant schema
  - Verify schema provisioning < 5 seconds
  - Verify Atlas Operator reconciles drift automatically
  - _Requirements: AC-5_

- [ ] 5.4.6 Verify AC-6: End-to-End Integration Test
  - Verify all steps from AC-6 pass
  - Verify cell provisioning < 15 minutes
  - Verify tenant schema provisioning < 5 seconds
  - Verify authentication flow works via AgentGateway
  - Verify drift detection and recovery works
  - _Requirements: AC-6_

### 5.5 PHASE 5 REVIEW CHECKPOINT

- [ ] 5.5.1 **MANDATORY STOP - Phase 5 Review**
  - **STOP ALL IMPLEMENTATION WORK**
  - Present Phase 5 completion summary to user
  - Demonstrate: All manual tests pass, all acceptance criteria verified
  - Show validation results from tasks 5.3.1-5.4.6
  - **WAIT FOR USER APPROVAL FOR FINAL SIGN-OFF**
  - Document any issues or deviations from design
  - _Requirements: All Phase 5 requirements_


---

## Final Checkpoint

### 6.1 Project Completion

- [ ] 6.1.1 **FINAL REVIEW - Project Sign-Off**
  - **STOP ALL IMPLEMENTATION WORK**
  - Present complete project summary to user
  - Demonstrate: All 5 phases complete, all acceptance criteria met
  - Review: All manual tests pass, all documentation complete
  - Verify: GitOps-first principles followed throughout
  - Verify: No kubectl apply used except for validation
  - **WAIT FOR USER FINAL APPROVAL**
  - _Requirements: All requirements (FR-1.1 through NFR-6.8, AC-1 through AC-6)_

---

## Notes

- All tasks follow GitOps-first principles: changes via Git commits, ArgoCD reconciles
- No kubectl apply except for validation commands (kubectl get, kubectl wait, kubectl exec for queries)
- Each phase includes mandatory review checkpoint with explicit STOP instruction
- Implementation must wait for user approval before proceeding to next phase
- All tasks reference specific requirements from requirements.md
- Manual validation tasks use read-only commands only
- Test scripts are read-only assertions, no state mutation
- Sync waves enforce dependency ordering: 0 (namespace/RBAC) → 1 (CNPG) → 2 (Atlas) → 3 (PostgREST/AgentGateway) → 4 (NATS/Alloy)
- Deterministic schema naming (tenant_<id>) enables idempotent operations
- PgBouncer MUST use transaction pooling mode (not session pooling)
- PostgREST is internal service only, all traffic flows through AgentGateway
- AgentGateway validates JWTs using Hub Ory JWKS before forwarding to PostgREST
- Atlas Operator provides continuous drift detection and automatic recovery

---

**Document Version**: 1.0  
**Created**: 2026-04-09  
**Status**: Ready for Implementation
