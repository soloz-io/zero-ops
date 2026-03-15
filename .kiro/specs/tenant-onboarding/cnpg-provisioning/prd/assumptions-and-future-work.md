# CNPG Provisioning - Assumptions & Future Work

**Document Version:** 1.0  
**Status:** DRAFT  
**Created:** 2026-03-10  
**Purpose:** Document all assumptions made during Phase 2 CNPG provisioning and identify future work required for production-grade implementation.

---

## Current Phase 2 Scope (Validated)

**What We're Building Now:**
1. Platform DB (`zero-ops-platform-db`) - 3-node CNPG cluster in Management Cluster
2. Namespace topology label patching for `zero-ops-system`
3. PodMonitor CRD installation (Prometheus Operator CRDs only)
4. `cnpg2monitor` operator - watches CNPG Clusters, generates PodMonitors, emits K8s Events
5. RBAC for operator in `cnpg2monitor-system` namespace

**What We're NOT Building Now:**
- Grafana Alloy deployment (Phase 4)
- VictoriaMetrics deployment (Phase 4)
- OpenSearch deployment (Phase 4)
- `kube-events-exporter` deployment (Phase 4)
- `postgres-ai` integration (Phase 7 - DiagnosticsAgent)
- Tenant cluster CNPG provisioning (Phase 3)

---

## Critical Assumptions

### A1. Observability Stack Deployment (Phase 4 Dependency)
**Assumption:** PodMonitors created in Phase 2 will remain dormant until Phase 4 deploys the Fleet Observability Stack.

**Risk:** If Phase 4 is delayed, we have no visibility into database health.

**Mitigation Required:**
- Document that PodMonitors are "forward-compatible" and will activate automatically when Alloy deploys
- Consider adding a validation step in Phase 4 to verify all existing PodMonitors are discovered by Alloy

**Future Work (Phase 4):**
- Deploy Grafana Alloy with `prometheus.operator.podmonitors` component enabled
- Configure Alloy remote_write to VictoriaMetrics with mTLS
- Deploy VictoriaMetrics cluster (vminsert, vmstorage, vmselect)
- Deploy OpenSearch cluster with index templates for k8s-events
- Deploy `kube-events-exporter` to stream K8s Events to OpenSearch
- Validate end-to-end metric flow: CNPG → Alloy → VictoriaMetrics
- Validate end-to-end event flow: cnpg2monitor → K8s Events → kube-events-exporter → OpenSearch

---

### A2. Namespace Topology Labels (Manual Prerequisite)
**Assumption:** The `zero-ops-system` namespace will be manually patched with topology labels before the operator starts.

**Current State:** Management cluster bootstrap (Phase 1) does NOT inject these labels.

**Labels Required:**
```yaml
nutgraf.in/cluster_id: "mothership"
nutgraf.in/region: "fsn1"  # or user-provided region from bootstrap
nutgraf.in/cloud_provider: "hetzner"
nutgraf.in/availability_zone: "fsn1-dc14"  # Hetzner datacenter
nutgraf.in/cluster_class: "management"
```

**Risk:** If labels are missing, PodMonitors will be created without topology labels, breaking v7.0 correlation queries.

**Future Work (Phase 1 Backport or Phase 2 Enhancement):**
- Option A: Update management cluster bootstrap to inject these labels during `zero-ops mgmt bootstrap`
- Option B: Add a pre-flight check in `cnpg2monitor` operator to validate namespace labels exist before reconciling
- Option C: Add a CLI command `zero-ops mgmt label-namespace` to patch labels post-bootstrap

**Recommended:** Option A (backport to Phase 1) for consistency.

---

### A3. Tenant Cluster Label Propagation (Phase 3 Dependency)
**Assumption:** When tenant clusters are provisioned via CAPI in Phase 3, their namespaces will automatically receive topology labels from CAPI/ArgoCD.

**Current State:** No mechanism exists to propagate CAPI cluster metadata to namespace labels.

**Future Work (Phase 3):**
- Implement a CAPI webhook or controller that watches `Cluster` resources
- Extract topology from `Cluster.spec.topology.variables` (region, cloud_provider, etc.)
- Patch the target namespace with `nutgraf.in/*` labels
- Ensure `cnpg2monitor` is deployed to tenant clusters via `ClusterResourceSet`

**Alternative:** Use ArgoCD ApplicationSet to template namespace labels from CAPI cluster metadata.

---

### A4. Multi-Cluster Operator Deployment (Phase 3 Dependency)
**Assumption:** The `cnpg2monitor` operator will be deployed to tenant clusters via `ClusterResourceSet` in Phase 3.

**Current State:** Operator only runs in Management Cluster.

**Future Work (Phase 3):**
- Create `ClusterResourceSet` manifest that injects:
  - `cnpg2monitor` operator deployment
  - PodMonitor CRDs
  - RBAC (ServiceAccount, ClusterRole, ClusterRoleBinding)
- Test operator behavior in multi-tenant scenarios (namespace isolation)
- Ensure operator only watches CNPG Clusters in its own cluster (no cross-cluster watching)

---

### A5. Database Health Checks (Phase 7 Dependency)
**Assumption:** SQL-based health checks (index bloat, query performance) are handled by `postgres-ai` via the `DiagnosticsAgent`, NOT by the `cnpg2monitor` operator.

**Current State:** No SQL-based validation in Phase 2.

**Future Work (Phase 7 - Multi-Agent Collaboration):**
- Integrate `postgres-ai` package with `DiagnosticsAgent`
- Implement scheduled `pg_index_pilot` jobs for proactive index maintenance
- Configure `DiagnosticsAgent` to query `opensearch-mcp` for CNPG events before running SQL diagnostics
- Add runbook corpus entries for common CNPG issues (replication lag, connection pool exhaustion, etc.)

**PRD Reference:** Section 5.5 explicitly forbids SQL queries in the operator.

---

### A6. Backup & Disaster Recovery (Out of Scope)
**Assumption:** CNPG's built-in backup features (Barman, S3 integration) are configured separately, not by the `cnpg2monitor` operator.

**Current State:** No backup configuration in Phase 2.

**Future Work (Phase 2.5 or Phase 3):**
- Configure CNPG `Cluster.spec.backup` with S3-compatible storage (Hetzner Object Storage or AWS S3)
- Set up scheduled backups (daily full, hourly WAL archiving)
- Test point-in-time recovery (PITR)
- Document backup retention policies (30 days for platform DB, configurable for tenant DBs)

**Risk:** Without backups, data loss is catastrophic. This should be prioritized immediately after Phase 2.

---

### A7. High Availability & Failover Testing (Out of Scope)
**Assumption:** CNPG's built-in HA (synchronous replication, automatic failover) works correctly without additional operator logic.

**Current State:** No chaos testing or failover validation in Phase 2.

**Future Work (Phase 2.5 or Phase 3):**
- Chaos engineering tests:
  - Kill primary pod, verify automatic failover to replica
  - Simulate network partition between replicas
  - Test behavior under disk pressure (PVC full)
- Validate that `cnpg2monitor` emits events during failover (e.g., "Primary changed from pod-0 to pod-1")
- Ensure PodMonitors continue scraping metrics during failover (no metric gaps)

---

### A8. Secret Management & Rotation (Out of Scope)
**Assumption:** The `-app` Secret created by CNPG is consumed directly by `zero-ops-api` without rotation.

**Current State:** No secret rotation mechanism.

**Future Work (Phase 6 or Security Hardening):**
- Integrate with External Secrets Operator or Vault for secret management
- Implement automated password rotation (CNPG supports this via `Cluster.spec.enableSuperuserAccess: false` and role-based access)
- Ensure `zero-ops-api` can handle secret updates without downtime (connection pool refresh)

---

### A9. Resource Limits & Autoscaling (Out of Scope)
**Assumption:** Platform DB uses fixed resource requests/limits and does not autoscale.

**Current State:** No autoscaling configuration in Phase 2.

**Future Work (Phase 3 or Performance Tuning):**
- Define resource requests/limits based on load testing:
  - CPU: 2-4 cores per pod
  - Memory: 4-8 GB per pod
  - Storage: 20 GB initial, expand as needed
- Implement PVC autoscaling (resize PVCs when disk usage > 80%)
- Consider horizontal scaling (read replicas) for tenant databases under heavy load

---

### A10. Monitoring Alert Rules (Phase 4 Dependency)
**Assumption:** VictoriaMetrics alert rules for CNPG metrics are defined in Phase 4, not Phase 2.

**Current State:** No alerting in Phase 2.

**Future Work (Phase 4):**
- Define vmalert recording rules for CNPG metrics:
  - `cnpg:replication_lag:seconds` - replication lag > 10s
  - `cnpg:connection_pool:utilization` - connection pool > 80%
  - `cnpg:disk:usage_percent` - disk usage > 85%
- Configure Alertmanager webhook to `zero-ops-api` for agent-driven remediation
- Ensure alerts include topology labels for regional correlation

---

### A11. Tenant Database Isolation (Phase 3 Dependency)
**Assumption:** Tenant databases are provisioned in separate namespaces with network policies for isolation.

**Current State:** Only platform DB exists in `zero-ops-system`.

**Future Work (Phase 3):**
- Implement namespace-per-tenant model
- Apply Kubernetes NetworkPolicies to restrict cross-tenant database access
- Ensure `cnpg2monitor` respects namespace boundaries (only watches CNPG Clusters in authorized namespaces)
- Test that tenant A cannot access tenant B's database even if they know the connection string

---

### A12. Cost Optimization & Right-Sizing (Out of Scope)
**Assumption:** Database instance sizes are fixed and not optimized based on actual usage.

**Current State:** All CNPG clusters use the same resource profile.

**Future Work (Phase 5 or Cost Optimization):**
- Integrate with `MetricsAgent` to analyze actual CPU/memory/disk usage
- Implement automated right-sizing recommendations (e.g., "Cluster X is over-provisioned, reduce to 2 instances")
- Add cost attribution per tenant (track storage, compute, backup costs)
- Expose cost metrics to `ReportAgent` for billing

---

## Production-Grade Checklist

**Before declaring CNPG provisioning "production-ready," the following must be completed:**

### Phase 2 (Current) - Minimum Viable Product
- [x] Platform DB deployed and accessible
- [x] `cnpg2monitor` operator functional
- [x] PodMonitors generated with topology labels
- [x] K8s Events emitted on spec changes
- [ ] Namespace topology labels patched (manual step documented)

### Phase 2.5 (Immediate Follow-Up) - Data Safety
- [ ] Backup configuration (S3, daily full + hourly WAL)
- [ ] Point-in-time recovery tested
- [ ] Failover testing (chaos engineering)
- [ ] Secret rotation mechanism

### Phase 3 (Tenant Clusters) - Multi-Tenancy
- [ ] Tenant namespace label propagation
- [ ] `cnpg2monitor` deployed via `ClusterResourceSet`
- [ ] Network policies for tenant isolation
- [ ] Tenant database provisioning via CLI

### Phase 4 (Observability) - Visibility
- [ ] Grafana Alloy deployed and scraping PodMonitors
- [ ] VictoriaMetrics receiving CNPG metrics
- [ ] OpenSearch receiving CNPG events
- [ ] Alert rules configured (replication lag, disk usage, etc.)
- [ ] End-to-end metric/event flow validated

### Phase 5 (Cost & Performance) - Optimization
- [ ] Resource right-sizing based on actual usage
- [ ] Cost attribution per tenant
- [ ] Autoscaling for high-load databases

### Phase 6 (Security Hardening) - Compliance
- [ ] External secret management (Vault/ESO)
- [ ] Automated password rotation
- [ ] Audit logging for database access
- [ ] Encryption at rest (CNPG supports LUKS)

### Phase 7 (AI Diagnostics) - Intelligence
- [ ] `postgres-ai` integration with `DiagnosticsAgent`
- [ ] Scheduled `pg_index_pilot` jobs
- [ ] Runbook corpus for CNPG issues
- [ ] Automated remediation workflows

---

## Open Questions

**Q1:** Should namespace topology labels be injected during management cluster bootstrap (Phase 1 backport) or as a separate Phase 2 step?
- **Recommendation:** Backport to Phase 1 for consistency.

**Q2:** Should the `cnpg2monitor` operator validate namespace labels exist before creating PodMonitors, or fail silently?
- **Recommendation:** Fail with a clear error event: "Namespace missing required topology labels."

**Q3:** Should backup configuration be part of Phase 2 or deferred to Phase 2.5?
- **Recommendation:** Phase 2.5 (immediate follow-up) since data loss is catastrophic.

**Q4:** Should the operator support multi-cluster watching (e.g., Management Cluster operator watching tenant clusters)?
- **Recommendation:** No. Each cluster runs its own operator instance for isolation and blast radius containment.

**Q5:** Should the operator generate ServiceMonitors in addition to PodMonitors for service-level metrics?
- **Recommendation:** No. CNPG metrics are pod-level. ServiceMonitors are unnecessary.

---

## Conclusion

Phase 2 delivers the **minimum viable CNPG provisioning** to unblock `zero-ops-api` deployment. However, **production-grade readiness requires Phases 2.5, 3, 4, and 7** to address data safety, multi-tenancy, observability, and AI diagnostics.

**Immediate Next Steps After Phase 2:**
1. Backport namespace label injection to Phase 1 (management cluster bootstrap)
2. Implement backup configuration (Phase 2.5)
3. Conduct failover testing (Phase 2.5)
4. Plan Phase 3 (tenant cluster CNPG provisioning)

**Critical Path Dependencies:**
- Phase 4 (Observability Stack) is required for any meaningful monitoring
- Phase 7 (Multi-Agent Collaboration) is required for AI-driven diagnostics
- Phase 2.5 (Backup & DR) is required before production use

---

**Document Owner:** Platform Architecture Team  
**Review Cadence:** After each phase completion  
**Last Updated:** 2026-03-10
