# Critical Analysis: Do We Need cnpg2monitor Operator?

**Date:** 2026-03-10  
**Question:** Does Grafana Alloy's `prometheus.operator.podmonitors` component eliminate the need for cnpg2monitor?  
**Status:** ANALYSIS COMPLETE

---

## What Grafana Alloy Does

### prometheus.operator.podmonitors Component

**Capabilities:**
1. ✅ Discovers existing PodMonitor CRDs in the cluster
2. ✅ Watches for PodMonitor changes (add/update/delete)
3. ✅ Scrapes targets defined in PodMonitors
4. ✅ Applies relabelings defined in PodMonitors
5. ✅ Forwards metrics to remote_write endpoints

**What Alloy Does NOT Do:**
1. ❌ Create PodMonitor CRDs
2. ❌ Read namespace labels
3. ❌ Inject topology labels into PodMonitors
4. ❌ Watch CNPG Cluster CRDs
5. ❌ Emit Kubernetes Events for CNPG lifecycle changes
6. ❌ Automatically generate PodMonitors when CNPG clusters are created

---

## What cnpg2monitor Does (Per PRD v2.0)

### Core Responsibilities

1. **Watch CNPG Cluster CRDs**
   - Monitors `postgresql.cnpg.io/v1 Cluster` resources
   - Filters by label `zero-ops.io/monitored: "true"`

2. **Read Namespace Topology Labels**
   - Queries namespace for `zero-ops.io/*` labels
   - Extracts: `cluster_id`, `region`, `cloud_provider`, `availability_zone`, `cluster_class`

3. **Generate PodMonitor CRDs**
   - Creates `monitoring.coreos.com/v1 PodMonitor` for each CNPG cluster
   - Injects topology labels via `relabelings` array
   - Sets owner references for garbage collection

4. **Emit Kubernetes Events**
   - Fires events on CNPG cluster lifecycle changes (scale, config change, etc.)
   - Events captured by `kube-events-exporter` → OpenSearch

5. **Maintain PodMonitor Lifecycle**
   - Updates PodMonitors when namespace labels change
   - Deletes PodMonitors when CNPG clusters are deleted

---

## The Gap: Why We Need cnpg2monitor

### Problem 1: Manual PodMonitor Creation

**Without cnpg2monitor:**
```yaml
# Platform Admin must manually create this for EVERY CNPG cluster
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: zero-ops-platform-db-monitor
  namespace: zero-ops-system
spec:
  selector:
    matchLabels:
      postgresql.cnpg.io/cluster: zero-ops-platform-db
  podMetricsEndpoints:
  - port: metrics
    relabelings:
    - targetLabel: cluster_id
      replacement: "mothership-01"  # Must manually lookup from namespace
    - targetLabel: region
      replacement: "fsn1"  # Must manually lookup from namespace
    - targetLabel: cloud_provider
      replacement: "hetzner"  # Must manually lookup from namespace
    # ... 5+ more labels
```

**With cnpg2monitor:**
```yaml
# Platform Admin only creates CNPG cluster
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: zero-ops-platform-db
  namespace: zero-ops-system
  labels:
    zero-ops.io/monitored: "true"  # That's it!
spec:
  instances: 3
  storage:
    size: 20Gi
```

**Result:** cnpg2monitor automatically creates the PodMonitor with correct topology labels.

---

### Problem 2: Topology Label Injection

**Challenge:** Topology labels live in Namespace metadata, not in CNPG Cluster spec.

**Namespace:**
```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: zero-ops-system
  labels:
    zero-ops.io/cluster_id: "mothership-01"
    zero-ops.io/region: "fsn1"
    zero-ops.io/cloud_provider: "hetzner"
    zero-ops.io/availability_zone: "fsn1-dc14"
    zero-ops.io/cluster_class: "management"
```

**Without cnpg2monitor:**
- Platform Admin must manually read namespace labels
- Platform Admin must manually copy labels into PodMonitor relabelings
- If namespace labels change, Admin must manually update ALL PodMonitors in that namespace

**With cnpg2monitor:**
- Operator reads namespace labels automatically
- Operator injects labels into PodMonitor relabelings
- Operator watches namespace changes and updates PodMonitors

---

### Problem 3: Multi-Tenant Scale

**Scenario:** 100 tenants, each with 3 CNPG clusters = 300 PodMonitors

**Without cnpg2monitor:**
- 300 manual PodMonitor manifests
- Each with 5+ topology labels manually copied from namespace
- If topology label schema changes (e.g., add `shard_id`), must update 300 PodMonitors

**With cnpg2monitor:**
- 0 manual PodMonitors
- Operator generates all 300 automatically
- Schema changes handled by updating operator logic once

---

### Problem 4: Event Correlation for AI Agents

**PRD Requirement (Journey C):**
> "The operator emits a standard Kubernetes Event of type Normal with reason CNPGClusterScaled. The edge kube-events-exporter captures this event and streams it to the OpenSearch k8s-events index, making it available for the DiagnosticsAgent to query during future anomalies."

**Without cnpg2monitor:**
- No events emitted when CNPG clusters scale/change
- DiagnosticsAgent cannot correlate database changes with incidents
- Example: Database crashes 10 minutes after parameter change → Agent has no timeline

**With cnpg2monitor:**
- Operator watches CNPG Cluster spec changes
- Emits events: `CNPGClusterScaled`, `CNPGConfigChanged`, `CNPGStorageExpanded`
- DiagnosticsAgent queries OpenSearch: "Show me all CNPG events in the last hour"

---

## Comparison Table

| Capability | Grafana Alloy | cnpg2monitor | Manual Process |
|:---|:---:|:---:|:---:|
| Discover existing PodMonitors | ✅ | N/A | N/A |
| Scrape metrics from PodMonitors | ✅ | N/A | N/A |
| Create PodMonitors for CNPG clusters | ❌ | ✅ | ✅ (manual) |
| Read namespace topology labels | ❌ | ✅ | ✅ (manual) |
| Inject topology labels into PodMonitors | ❌ | ✅ | ✅ (manual) |
| Watch CNPG Cluster lifecycle | ❌ | ✅ | ❌ |
| Emit K8s Events for CNPG changes | ❌ | ✅ | ❌ |
| Update PodMonitors on namespace label changes | ❌ | ✅ | ✅ (manual) |
| Delete PodMonitors when CNPG deleted | ❌ | ✅ (via OwnerRef) | ✅ (manual) |
| Scale to 1000+ CNPG clusters | ✅ | ✅ | ❌ (human bottleneck) |

---

## Alternative: Could We Use Existing Tools?

### Option 1: Prometheus Operator

**Does Prometheus Operator create PodMonitors?**
- ❌ No. Prometheus Operator watches PodMonitors (like Alloy), but doesn't create them.
- Prometheus Operator creates Prometheus CRDs, not PodMonitors for arbitrary workloads.

### Option 2: CNPG Operator Built-in Monitoring

**Does CNPG have built-in PodMonitor generation?**
- ❌ No. CNPG exposes metrics on port 9187, but doesn't create PodMonitors.
- CNPG documentation recommends manually creating PodMonitors or ServiceMonitors.

### Option 3: Helm Chart with PodMonitor Template

**Could we template PodMonitors in Helm?**
```yaml
# In CNPG Helm chart
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: {{ .Values.clusterName }}-monitor
spec:
  podMetricsEndpoints:
  - port: metrics
    relabelings:
    - targetLabel: cluster_id
      replacement: {{ .Values.topology.cluster_id }}  # Must pass via Helm values
```

**Problems:**
- ❌ Requires passing topology labels via Helm values (manual)
- ❌ No dynamic updates when namespace labels change
- ❌ No event emission for CNPG lifecycle changes
- ❌ Doesn't work for tenant-provisioned clusters (they don't use Helm)

### Option 4: ArgoCD ApplicationSet with PodMonitor Generator

**Could ArgoCD generate PodMonitors?**
- ❌ ArgoCD doesn't watch CNPG Cluster CRDs
- ❌ ArgoCD doesn't read namespace labels dynamically
- ❌ No event emission capability

---

## Decision: YES, We Need cnpg2monitor

### Justification

1. **Zero-Touch Requirement**
   - PRD explicitly states: "zero-touch observability"
   - Manual PodMonitor creation violates this requirement

2. **Topology Label Injection**
   - No existing tool reads namespace labels and injects them into PodMonitors
   - This is the core value proposition of cnpg2monitor

3. **Event Correlation**
   - DiagnosticsAgent requires CNPG lifecycle events in OpenSearch
   - No existing tool emits these events

4. **Multi-Tenant Scale**
   - Manual process doesn't scale to 1000+ clusters
   - Helm/ArgoCD templates don't handle dynamic label updates

5. **Separation of Concerns**
   - Grafana Alloy: Scrapes metrics from PodMonitors (collection layer)
   - cnpg2monitor: Generates PodMonitors with topology labels (orchestration layer)
   - These are complementary, not overlapping

---

## Simplified Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ Platform Admin                                              │
└─────────────────────────────────────────────────────────────┘
                           │
                           │ 1. Creates CNPG Cluster
                           │    (with label: zero-ops.io/monitored=true)
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ CNPG Operator                                               │
│ - Provisions PostgreSQL pods                                │
│ - Exposes metrics on port 9187                              │
└─────────────────────────────────────────────────────────────┘
                           │
                           │ 2. CNPG Cluster created
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ cnpg2monitor Operator (THIS IS WHAT WE'RE BUILDING)        │
│ - Watches CNPG Cluster CRDs                                 │
│ - Reads namespace topology labels                           │
│ - Generates PodMonitor with relabelings                     │
│ - Emits K8s Events on CNPG changes                          │
└─────────────────────────────────────────────────────────────┘
                           │
                           │ 3. PodMonitor created
                           ▼
┌─────────────────────────────────────────────────────────────┐
│ Grafana Alloy (Phase 4)                                     │
│ - Discovers PodMonitor via prometheus.operator.podmonitors  │
│ - Scrapes CNPG pods on port 9187                            │
│ - Applies relabelings (topology labels)                     │
│ - Ships to VictoriaMetrics                                  │
└─────────────────────────────────────────────────────────────┘
```

---

## Conclusion

**Answer:** YES, we absolutely need cnpg2monitor operator.

**Reason:** Grafana Alloy consumes PodMonitors but doesn't create them. cnpg2monitor bridges the gap between CNPG Cluster creation and PodMonitor generation, with the critical capability of injecting topology labels from namespace metadata.

**Analogy:** 
- Grafana Alloy = Restaurant waiter (serves food that's already prepared)
- cnpg2monitor = Chef (prepares the food with the right ingredients)
- Without the chef, the waiter has nothing to serve.

**Next Step:** Proceed with cnpg2monitor operator implementation as specified in PRD v2.0.

---

**Document Owner:** Platform Architecture Team  
**Reviewed By:** [Your Name]  
**Decision:** PROCEED WITH IMPLEMENTATION
