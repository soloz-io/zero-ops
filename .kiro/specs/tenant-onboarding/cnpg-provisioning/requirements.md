# CNPG Provisioning - Requirements Specification

**Version:** 1.0  
**Status:** DRAFT  
**Created:** 2026-03-10  
**Based on:** PRD v2.0, BDD Test Cases, Operator Overview, Reference Patterns  

---

## 1. Executive Summary

This specification defines requirements for Phase 2 CNPG provisioning: deploying the Platform Database (`zero-ops-platform-db`) and implementing the `cnpg2monitor` operator for zero-touch observability integration with the Zero-Ops v7.0 Fleet Observability Stack.

**Scope:** Management Cluster only (tenant clusters deferred to Phase 3)  
**Dependencies:** CloudNativePG operator (already installed), PodMonitor CRDs  
**Integration:** Grafana Alloy (Phase 4), VictoriaMetrics (Phase 4), OpenSearch (Phase 4)

---

## 2. Functional Requirements

### FR1. Platform Database Provisioning

**FR1.1 High Availability Cluster**
- Deploy 3-node PostgreSQL cluster using CloudNativePG
- Cluster name: `zero-ops-platform-db`
- Namespace: `zero-ops-system`
- Database: `zeroops` with owner `zeroops-api`
- Storage: 20Gi per instance using `hcloud-volumes` StorageClass
- **Acceptance Criteria:** Cluster reaches `ClusterPhaseHealthy` within 3 minutes

**FR1.2 Connection Secret Generation**
- CNPG generates Secret: `zero-ops-platform-db-app`
- Contains: `username`, `password`, `dbname`, `uri` keys
- **Acceptance Criteria:** Secret exists and contains non-empty connection string

**FR1.3 Monitoring Schema Bootstrap**
- Create `postgres_ai` schema for future DiagnosticsAgent integration
- Install `pg_stat_statements` extension
- **Acceptance Criteria:** Schema and extension exist in `zeroops` database

### FR2. cnpg2monitor Operator Deployment

**FR2.1 Operator Installation**
- Deploy operator in `cnpg2monitor-system` namespace
- Single replica (leader election disabled for Phase 2)
- Resource limits: 100m CPU, 128Mi memory
- **Acceptance Criteria:** Operator pod reaches Ready state

**FR2.2 RBAC Configuration**
- Watch: `postgresql.cnpg.io/v1 Clusters`, `v1 Namespaces` (scoped to MONITORING_NAMESPACE)
- Manage: `monitoring.coreos.com/v1 PodMonitors`
- Create: `v1 Events`
- **Acceptance Criteria:** ServiceAccount has minimal required permissions via Role/RoleBinding

### FR3. Zero-Touch PodMonitor Generation

**FR3.1 Automatic PodMonitor Patching**
- **WHEN** a CNPG Cluster with label `nutgraf.in/monitored: "true"` and `spec.monitoring.enablePodMonitor: true` is created
- **THEN** the System SHALL locate the CNPG-generated PodMonitor by querying via label selector `postgresql.cnpg.io/cluster: <cluster-name>` within the same namespace
- **The System SHALL NOT rely on hardcoded name assumptions about CNPG's internal naming conventions**
- **The System SHOULD use event-driven PodMonitor watching where possible, with sync-period polling as fallback**
- **IF** the PodMonitor does not exist during reconciliation, **THEN** the System SHALL requeue with 2-second backoff (max 5 retries per event)
- **IF** retries are exhausted without finding the PodMonitor, **THEN** the System SHALL emit a Warning Event with reason `CNPGPodMonitorNotFound` and rely on the next sync cycle (30s) for retry
- **WHEN** the PodMonitor exists, **THEN** the System SHALL patch it with topology labels within 2 seconds
- **IF** `nutgraf.in/monitored: "true"` is present BUT `enablePodMonitor: false`, **THEN** the System SHALL emit a Warning Event with reason `CNPGMonitoringMisconfigured` and skip reconciliation
- **Acceptance Criteria:** PodMonitor exists with correct selector and relabelings

**FR3.2 Topology Label Injection**
- **WHEN** patching a PodMonitor, **THEN** the System SHALL read topology labels from the parent namespace
- **IF** namespace labels with prefix `nutgraf.in/` are missing, **THEN** the System SHALL emit a Warning Event with reason `CNPGTopologyLabelsMissing`, patch the PodMonitor to remove the `nutgraf.in/monitored: "true"` label (preventing Alloy discovery), and skip topology patching
- **WHEN** topology labels are subsequently added to the namespace, **THEN** the System SHALL restore the `nutgraf.in/monitored: "true"` label as part of the topology patch
- **WHEN** namespace topology labels are updated, **THEN** the System SHALL update all managed PodMonitors in that namespace within 30 seconds
- **Implementation Note:** Requires EnqueueRequestsFromMapFunc to map Namespace events to CNPG Clusters
- **Acceptance Criteria:** PodMonitor relabelings match namespace labels exactly

**FR3.3 Lifecycle Event Emission**
- **BEFORE** emitting any lifecycle event, **THEN** the System SHALL read annotations from the Cluster CR:
  - `cnpg2monitor.nutgraf.in/last-scaled-instances` (for CNPGScaled events)
  - `cnpg2monitor.nutgraf.in/last-config-generation` (for CNPGConfigChanged events)  
  - `cnpg2monitor.nutgraf.in/last-storage-generation` (for CNPGStorageExpanded events)
- **WHEN** `cluster.Status.ReadyInstances` matches `cluster.Spec.Instances` AND differs from annotation value, **THEN** emit Event with reason `CNPGScaled` and update `last-scaled-instances` annotation
- **WHEN** `cluster.Status.Phase == "ClusterPhaseHealthy"` AND `cluster.Status.ObservedGeneration == metadata.generation` AND generation exceeds `last-config-generation` annotation, **THEN** emit Event with reason `CNPGConfigChanged` and update annotation
- **WHEN** underlying PVCs report `Status.Capacity` matching `Spec.Storage.Size` AND `metadata.generation` exceeds `last-storage-generation` annotation, **THEN** emit Event with reason `CNPGStorageExpanded` and update annotation
- **Acceptance Criteria:** Events reflect actual state changes, no duplicate events on operator restart

---

## 3. Non-Functional Requirements

### NFR1. Performance & Scalability

**NFR1.1 Reconciliation Performance**
- PodMonitor creation: < 2 seconds after CNPG cluster ready
- Namespace label sync: < 30 seconds after label change
- Memory usage: < 128Mi under normal load
- **Measurement:** Prometheus metrics and resource monitoring

**NFR1.2 API Rate Limiting**
- Kubernetes API QPS: 20 requests/second
- Burst limit: 30 requests
- **Rationale:** Prevents API server overload during cluster creation spikes

### NFR2. Reliability & Availability

**NFR2.1 Operator Resilience**
- Graceful shutdown on SIGTERM (< 30 seconds)
- Automatic restart on crash (Kubernetes handles)
- Reconciliation retry with exponential backoff
- **Measurement:** Uptime metrics and crash loop detection

**NFR2.2 Data Consistency**
- SSA patch payload SHALL target the `podMetricsEndpoints` array element identified by `port: metrics` as the merge key
- cnpg2monitor SHALL claim SSA field ownership of `relabelings` only within that element
- cnpg2monitor SHALL also claim SSA field ownership of `metadata.labels.nutgraf.in/monitored` on the PodMonitor
- cnpg2monitor SHALL NOT include `interval`, `tlsConfig`, `scheme`, or any other fields in the patch payload
- CNPG maintains object ownership via OwnerReferences (cnpg2monitor does NOT add OwnerReference)
- Idempotent reconciliation (safe to run multiple times)
- **Verification:** Delete CNPG cluster, verify PodMonitor cleanup by CNPG's garbage collection

### NFR3. Observability

**NFR3.1 Metrics Exposure**
- Prometheus metrics on port 8080:
  - `cnpg2monitor_podmonitors_created_total`
  - `cnpg2monitor_podmonitors_updated_total`
  - `cnpg2monitor_events_emitted_total{reason}`
- **Integration:** Scraped by Grafana Alloy in Phase 4

**NFR3.2 Structured Logging**
- JSON logs in production, human-readable in debug mode
- Log levels: Info (default), Debug (--debug flag)
- Context propagation: include CNPG cluster name in all logs
- **Format:** Compatible with log aggregation systems

---

## 4. Integration Requirements

### IR1. CloudNativePG Integration

**IR1.1 CRD Compatibility**
- Support CNPG v1.19+ API (`postgresql.cnpg.io/v1`)
- Watch `Cluster` resources only (ignore `Backup`, `ScheduledBackup`)
- Respect CNPG's `enablePodMonitor: true` setting
- **Validation:** Test with CNPG operator v1.19.0

**IR1.2 Metric Port Discovery**
- Target CNPG metrics port: `metrics` (port 9187)
- Use pod selector: `postgresql.cnpg.io/cluster: <cluster-name>`
- **Verification:** Metrics accessible via port-forward

### IR2. Prometheus Operator Integration

**IR2.1 PodMonitor CRD Usage**
- Use `monitoring.coreos.com/v1 PodMonitor` API
- Set `app.kubernetes.io/managed-by: cnpg2monitor` label
- Add `nutgraf.in/monitored: "true"` for Alloy filtering
- **Compatibility:** Tested with prometheus-operator v0.68+

**IR2.2 Relabeling Configuration**
- Static relabelings for topology labels (no regex)
- Target labels: `cluster_id`, `region`, `cloud_provider`, `availability_zone`, `cluster_class`
- **Format:** `targetLabel: <name>`, `replacement: <value>`

### IR3. Grafana Alloy Integration (Phase 4)

**IR3.1 PodMonitor Discovery**
- Alloy discovers PodMonitors via `prometheus.operator.podmonitors` component
- Label selector: `nutgraf.in/monitored: "true"`
- **Assumption:** PodMonitors remain dormant until Alloy deployment

**IR3.2 Metric Flow Validation**
- End-to-end: CNPG → PodMonitor → Alloy → VictoriaMetrics
- Topology labels preserved throughout pipeline
- **Testing:** Deferred to Phase 4 integration tests

---

## 5. Security Requirements

### SR1. RBAC & Permissions

**SR1.1 Least Privilege Access**
- No cluster-admin or wildcard permissions
- Read-only access to CNPG Clusters and Namespaces
- Write access only to PodMonitors and Events
- **Audit:** Regular RBAC review and permission validation

**SR1.2 Namespace Isolation**
- Operator respects namespace boundaries
- No cross-namespace resource modification
- **Verification:** Multi-tenant testing scenarios

### SR2. Secret Handling

**SR2.1 No Database Credentials**
- Operator does NOT access PostgreSQL connection secrets
- Metrics collection via Kubernetes ServiceAccount tokens only
- **Principle:** Separation of concerns - metrics vs. data access

---

## 6. Configuration Requirements

### CR1. Environment Variables

**CR1.1 Runtime Configuration**
```bash
MONITORING_NAMESPACE=zero-ops-system     # Restricts controller cache scope to single namespace
ENABLE_EVENT_EMISSION=true               # Enable K8s event emission
TOPOLOGY_LABEL_PREFIX=nutgraf.in/       # Namespace label prefix
```

**CR1.2 Command-Line Flags**
```bash
--metrics-bind-address=:8080             # Prometheus metrics port
--health-probe-bind-address=:8081        # Health/readiness probes
--sync-duration=30s                      # Reconciliation period
--debug=false                            # Enable debug logging
--kube-qps=20                           # K8s API rate limit
--kube-burst=30                         # K8s API burst limit
```

### CR2. Namespace Prerequisites

**CR2.1 Topology Label Requirements**
- `zero-ops-system` namespace MUST have topology labels before operator starts
- Required labels:
  ```yaml
  nutgraf.in/cluster_id: "mothership"
  nutgraf.in/region: "fsn1"
  nutgraf.in/cloud_provider: "hetzner"
  nutgraf.in/availability_zone: "fsn1-dc14"
  nutgraf.in/cluster_class: "management"
  ```
- **Risk:** Missing labels result in PodMonitors without topology context

---

## 7. Testing Requirements

### TR1. Unit Testing

**TR1.1 Controller Logic**
- Test PodMonitor generation with various topology label combinations
- Test event emission for different CNPG spec changes
- Test error handling for missing namespaces/labels
- **Coverage:** > 80% code coverage

**TR1.2 Integration Testing**
- Test with real CNPG clusters in test environment
- Validate PodMonitor creation/update/deletion lifecycle
- Test namespace label synchronization
- **Framework:** Ginkgo/Gomega with envtest

### TR2. End-to-End Testing

**TR2.1 BDD Test Scenarios**
- Platform Database Bootstrap (Suite 1)
- cnpg2monitor Auto-Wiring (Suite 2)  
- AI Correlation Event Emission (Suite 3)
- **Reference:** `.kiro/specs/tenant-onboarding/cnpg-provisioning/e2e-bdd.md`

**TR2.2 Chaos Testing**
- Operator restart during reconciliation
- CNPG cluster deletion during PodMonitor creation
- Namespace label changes during reconciliation
- **Goal:** Verify eventual consistency and error recovery

---

## 8. Deployment Requirements

### DR1. Kubernetes Manifests

**DR1.1 Operator Deployment**
- Deployment with single replica
- Resource requests/limits defined
- Health/readiness probes configured
- **Location:** `manifests/cnpg2monitor/`

**DR1.2 RBAC Resources**
- ServiceAccount in `cnpg2monitor-system`, Role in `zero-ops-system`, RoleBinding in `zero-ops-system`
- **The RoleBinding SHALL be created in MONITORING_NAMESPACE (zero-ops-system), binding the ServiceAccount from cnpg2monitor-system**
- Generated via kubebuilder RBAC markers
- **Validation:** `kubectl auth can-i` tests

### DR2. Platform Database Manifest

**DR2.1 CNPG Cluster Configuration**
```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: zero-ops-platform-db
  namespace: zero-ops-system
  labels:
    nutgraf.in/monitored: "true"
spec:
  instances: 3
  storage:
    size: 20Gi
    storageClass: hcloud-volumes
  monitoring:
    enablePodMonitor: true
  postgresql:
    parameters:
      shared_preload_libraries: "pg_stat_statements"
  bootstrap:
    initdb:
      database: zeroops
      owner: zeroops-api
      postInitSQL:
        - CREATE SCHEMA IF NOT EXISTS postgres_ai;
        - CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
```

---

## 9. Success Criteria

### SC1. Phase 2 Completion Criteria

**SC1.1 Platform Database**
- [ ] 3-node CNPG cluster running and healthy
- [ ] Connection secret generated and accessible
- [ ] `zero-ops-api` can connect and create tables

**SC1.2 cnpg2monitor Operator**
- [ ] Operator deployed and running
- [ ] PodMonitor created with topology labels
- [ ] K8s Events emitted for cluster changes
- [ ] Prometheus metrics exposed

**SC1.3 Integration Readiness**
- [ ] PodMonitors compatible with Grafana Alloy
- [ ] Topology labels match namespace metadata
- [ ] Event format compatible with OpenSearch

### SC2. Quality Gates

**SC2.1 Code Quality**
- [ ] All unit tests passing (> 80% coverage)
- [ ] All BDD scenarios passing
- [ ] Static analysis (golangci-lint) clean
- [ ] Security scan (gosec) clean

**SC2.2 Documentation**
- [ ] Operator README with deployment instructions
- [ ] Troubleshooting guide for common issues
- [ ] Metrics documentation for monitoring

---

## 10. Assumptions & Dependencies

### A1. External Dependencies

**A1.1 CloudNativePG Operator**
- Version: v1.19+ installed in Management Cluster
- CRDs: `postgresql.cnpg.io/v1` available
- **Risk:** Version incompatibility with PodMonitor generation

**A1.2 Prometheus Operator CRDs**
- PodMonitor CRD (`monitoring.coreos.com/v1`) installed
- **Installation:** Manual CRD application before operator deployment

### A2. Phase Dependencies

**A2.1 Phase 1 Prerequisites**
- Management cluster bootstrapped and accessible
- `zero-ops-system` namespace exists
- **Gap:** Namespace topology labels not injected by Phase 1

**A2.2 Phase 4 Integration**
- Grafana Alloy deployment with `prometheus.operator.podmonitors` component
- VictoriaMetrics cluster for metric storage
- OpenSearch cluster for event storage
- **Timeline:** Phase 2 PodMonitors remain dormant until Phase 4

---

## 11. Risk Assessment

### R1. High-Risk Items

**R1.1 Missing Namespace Labels**
- **Risk:** PodMonitors created without topology labels
- **Impact:** Metrics unusable for fleet-wide correlation
- **Mitigation:** Pre-flight validation in operator startup

**R1.2 CNPG API Changes**
- **Risk:** CNPG operator updates break PodMonitor patching
- **Impact:** Monitoring stops working
- **Mitigation:** Pin CNPG version, test upgrades in staging

### R2. Medium-Risk Items

**R2.1 Grafana Alloy Compatibility**
- **Risk:** Alloy doesn't discover generated PodMonitors
- **Impact:** No metrics collection in Phase 4
- **Mitigation:** Validated against Alloy documentation (2026-03-10)

**R2.2 Resource Limits**
- **Risk:** Operator OOMKilled under high load
- **Impact:** PodMonitor generation stops
- **Mitigation:** Load testing and resource tuning

---

## 12. Future Enhancements (Out of Scope)

### Phase 2.5 Enhancements
- Backup configuration for Platform DB
- Failover testing and chaos engineering
- Secret rotation mechanism

### Phase 3 Enhancements  
- Multi-tenant cluster support
- Tenant namespace label propagation
- ClusterResourceSet deployment

### Phase 4 Integration
- End-to-end metric flow validation
- Alert rule configuration
- Grafana dashboard creation

### Phase 7 AI Integration
- postgres-ai integration with DiagnosticsAgent
- Automated remediation workflows
- Advanced correlation algorithms

---

**Document Owner:** Platform Architecture Team  
**Reviewers:** Senior Platform Engineers  
**Approval Required:** Technical Lead, Product Owner  
**Next Phase:** Design Specification (design.md)