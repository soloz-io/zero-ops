# cnpg2monitor Operator - Overview & Value Proposition

**Version:** 1.0  
**Status:** DESIGN  
**Created:** 2026-03-10  
**Purpose:** Define what cnpg2monitor does and why it's needed despite CNPG's built-in monitoring features

---

## Executive Summary

`cnpg2monitor` is a lightweight Kubernetes operator that bridges CloudNativePG (CNPG) database clusters with the Zero-Ops v7.0 Fleet Observability Stack. While CNPG can auto-generate PodMonitors via `.spec.monitoring.enablePodMonitor: true`, it lacks critical capabilities for multi-cluster, multi-tenant environments:

1. **Topology Label Injection:** Reads namespace labels and injects fleet-wide topology metadata into PodMonitor relabelings
2. **Event Correlation:** Emits Kubernetes Events for CNPG lifecycle changes to enable AI-driven diagnostics
3. **Zero-Touch Automation:** Eliminates manual PodMonitor configuration across 100+ tenant clusters

**Result:** Every CNPG database automatically gets perfectly labeled metrics in VictoriaMetrics and lifecycle events in OpenSearch, enabling the Multi-Agent Collaboration System to correlate incidents across regions, clouds, and clusters.

---

## What CNPG Provides Out-of-the-Box

### Built-in Monitoring Features

CloudNativePG (v1.19+) includes:

1. **Metrics Exporter**
   - Exposes Prometheus metrics on port 9187 (named `metrics`)
   - Predefined metrics: WAL stats, replication lag, backup status, etc.
   - Custom metrics via ConfigMap/Secret

2. **Auto-Generated PodMonitor**
   ```yaml
   apiVersion: postgresql.cnpg.io/v1
   kind: Cluster
   spec:
     monitoring:
       enablePodMonitor: true  # Creates PodMonitor automatically
   ```
   
   **Generated PodMonitor:**
   ```yaml
   apiVersion: monitoring.coreos.com/v1
   kind: PodMonitor
   metadata:
     name: cluster-example
     namespace: default
   spec:
     selector:
       matchLabels:
         cnpg.io/cluster: cluster-example
     podMetricsEndpoints:
     - port: metrics
   ```

3. **Operator Metrics**
   - CNPG operator exposes its own metrics on port 8080

---

## What CNPG Does NOT Provide

### Critical Gaps for Multi-Cluster Environments

1. **No Topology Label Injection**
   - CNPG-generated PodMonitors have NO relabelings
   - Metrics lack `cluster_id`, `region`, `cloud_provider`, `availability_zone`
   - AI agents cannot correlate metrics across clusters

2. **No Namespace Label Reading**
   - CNPG doesn't read namespace metadata
   - Topology labels stored in namespace are ignored
   - Manual copying required for each cluster

3. **No Lifecycle Event Emission**
   - CNPG doesn't emit custom K8s Events for:
     - Cluster scaling (3 → 5 replicas)
     - Configuration changes (PostgreSQL parameters)
     - Storage expansion (20Gi → 50Gi)
   - DiagnosticsAgent cannot build incident timelines

4. **No Dynamic Label Updates**
   - If namespace labels change, PodMonitors stay stale
   - Manual updates required across all clusters

---

## What cnpg2monitor Adds

### Core Value Propositions

#### 1. Topology Label Injection (Primary Value)

**Problem:** Metrics without topology labels are useless for fleet-wide correlation.

**Solution:** cnpg2monitor reads namespace labels and injects them into PodMonitor relabelings.

**Before (CNPG-generated PodMonitor):**
```yaml
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
spec:
  podMetricsEndpoints:
  - port: metrics
    # NO relabelings - metrics lack topology context
```

**After (cnpg2monitor-patched PodMonitor):**
```yaml
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
spec:
  podMetricsEndpoints:
  - port: metrics
    relabelings:
    - targetLabel: cluster_id
      replacement: "mothership-01"
    - targetLabel: region
      replacement: "fsn1"
    - targetLabel: cloud_provider
      replacement: "hetzner"
    - targetLabel: availability_zone
      replacement: "fsn1-dc14"
    - targetLabel: cluster_class
      replacement: "management"
```

**Impact:**
- MetricsAgent can query: `cnpg_collector_up{region="fsn1", cloud_provider="hetzner"}`
- Cross-region incident correlation: "All Hetzner databases in fsn1 are slow"
- Cost attribution: "Tenant X uses 50GB across 3 regions"

---

#### 2. Event Correlation for AI Diagnostics

**Problem:** When a database crashes, DiagnosticsAgent needs a timeline of recent changes.

**Solution:** cnpg2monitor watches CNPG Cluster spec changes and emits Kubernetes Events.

**Events Emitted:**
```yaml
Type: Normal
Reason: CNPGClusterScaled
Message: "Cluster scaled from 3 to 5 instances"

Type: Normal
Reason: CNPGConfigChanged
Message: "PostgreSQL parameter 'shared_buffers' changed from 128MB to 256MB"

Type: Normal
Reason: CNPGStorageExpanded
Message: "Storage expanded from 20Gi to 50Gi"
```

**Event Flow:**
```
cnpg2monitor → K8s Events → kube-events-exporter → OpenSearch → DiagnosticsAgent
```

**Use Case:**
1. Database crashes at 10:15 AM
2. DiagnosticsAgent queries OpenSearch: "Show CNPG events in last hour"
3. Finds: "shared_buffers changed at 10:05 AM"
4. Agent recommends: "Rollback shared_buffers change - likely cause of OOM crash"

---

#### 3. Zero-Touch Automation at Scale

**Problem:** Manual PodMonitor management doesn't scale to 1000+ clusters.

**Solution:** cnpg2monitor automates everything.

**Workflow:**

1. **Tenant Provisions Database:**
   ```bash
   zero-ops cluster create tenant-foo --region us-east
   ```

2. **CAPI Creates Namespace with Labels:**
   ```yaml
   apiVersion: v1
   kind: Namespace
   metadata:
     name: tenant-foo
     labels:
       nutgraf.in/cluster_id: "tenant-foo-us-east-01"
       nutgraf.in/region: "us-east"
       nutgraf.in/cloud_provider: "aws"
   ```

3. **Tenant Creates CNPG Cluster:**
   ```yaml
   apiVersion: postgresql.cnpg.io/v1
   kind: Cluster
   metadata:
     name: app-db
     namespace: tenant-foo
     labels:
       nutgraf.in/monitored: "true"
   spec:
     instances: 3
     monitoring:
       enablePodMonitor: true
   ```

4. **CNPG Creates PodMonitor (no relabelings)**

5. **cnpg2monitor Patches PodMonitor:**
   - Reads namespace labels
   - Adds relabelings with topology labels
   - Sets owner reference for garbage collection

6. **Grafana Alloy Scrapes Metrics:**
   - Discovers PodMonitor
   - Scrapes CNPG pods
   - Applies relabelings
   - Ships to VictoriaMetrics with perfect labels

**Result:** Tenant gets instant observability without touching YAML.

---

#### 4. Dynamic Label Synchronization

**Problem:** Namespace labels change (e.g., region migration), PodMonitors become stale.

**Solution:** cnpg2monitor watches namespace changes and updates PodMonitors.

**Scenario:**
1. Namespace label changes: `nutgraf.in/region: "us-east"` → `"us-west"`
2. cnpg2monitor detects change
3. cnpg2monitor updates all PodMonitors in that namespace
4. Metrics immediately reflect new region label

---

## Architecture: Two Implementation Options

### Option 1: PodMonitor Patcher (RECOMMENDED)

**Approach:** Leverage CNPG's `enablePodMonitor: true`, patch the generated PodMonitor.

**Operator Logic:**
1. Watch CNPG Clusters with label `nutgraf.in/monitored: "true"`
2. Wait for CNPG to create PodMonitor
3. Read namespace topology labels
4. Patch PodMonitor to add relabelings
5. Watch CNPG Cluster spec changes → emit Events
6. Watch namespace label changes → update PodMonitors

**Pros:**
- ✅ Simpler (50% less code)
- ✅ Leverages CNPG's built-in feature
- ✅ CNPG handles PodMonitor lifecycle (creation, deletion)
- ✅ Less maintenance burden

**Cons:**
- ⚠️ Requires patching existing resources (more complex reconciliation)
- ⚠️ Depends on CNPG's PodMonitor format staying stable

---

### Option 2: Full PodMonitor Generator (ORIGINAL PLAN)

**Approach:** Disable CNPG's `enablePodMonitor`, create PodMonitors from scratch.

**Operator Logic:**
1. Watch CNPG Clusters with label `nutgraf.in/monitored: "true"`
2. Read namespace topology labels
3. Generate PodMonitor with relabelings
4. Set owner reference to CNPG Cluster
5. Watch CNPG Cluster spec changes → emit Events
6. Watch namespace label changes → update PodMonitors

**Pros:**
- ✅ Full control over PodMonitor format
- ✅ No dependency on CNPG's PodMonitor feature
- ✅ Cleaner reconciliation (create vs patch)

**Cons:**
- ⚠️ Duplicates CNPG functionality
- ⚠️ More code to maintain
- ⚠️ Must handle PodMonitor deletion manually

---

## Comparison: CNPG vs cnpg2monitor

| Feature | CNPG Built-in | cnpg2monitor | Manual Process |
|:---|:---:|:---:|:---:|
| Create PodMonitor | ✅ | ✅ | ✅ |
| Expose metrics on port 9187 | ✅ | N/A | N/A |
| Read namespace topology labels | ❌ | ✅ | ✅ |
| Inject topology labels into PodMonitor | ❌ | ✅ | ✅ |
| Emit K8s Events for lifecycle changes | ❌ | ✅ | ❌ |
| Update PodMonitors on namespace label changes | ❌ | ✅ | ✅ |
| Scale to 1000+ clusters | ✅ | ✅ | ❌ |
| Zero-touch automation | ⚠️ (partial) | ✅ | ❌ |

---

## Integration with Zero-Ops v7.0 Stack

### Data Flow

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Tenant creates CNPG Cluster                             │
│    (with label: nutgraf.in/monitored=true)                │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 2. CNPG Operator                                            │
│    - Provisions PostgreSQL pods                             │
│    - Creates PodMonitor (if enablePodMonitor: true)         │
│    - Exposes metrics on port 9187                           │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 3. cnpg2monitor Operator                                    │
│    - Reads namespace topology labels                        │
│    - Patches PodMonitor with relabelings                    │
│    - Emits K8s Events on CNPG changes                       │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 4. Grafana Alloy (Phase 4)                                  │
│    - Discovers PodMonitor via prometheus.operator.podmonitors│
│    - Scrapes CNPG pods on port 9187                         │
│    - Applies relabelings (topology labels)                  │
│    - Ships to VictoriaMetrics via remote_write              │
└─────────────────────────────────────────────────────────────┘
                           ↓
┌─────────────────────────────────────────────────────────────┐
│ 5. Fleet Observability Stack                                │
│    - VictoriaMetrics: Stores metrics with topology labels   │
│    - OpenSearch: Stores CNPG lifecycle events               │
│    - MetricsAgent: Queries metrics by region/cloud          │
│    - DiagnosticsAgent: Correlates events with incidents     │
└─────────────────────────────────────────────────────────────┘
```

---

## Success Metrics

### Phase 2 (MVP)

- ✅ Platform DB (`zero-ops-platform-db`) has PodMonitor with topology labels
- ✅ Metrics visible in VictoriaMetrics with correct labels (Phase 4)
- ✅ K8s Events emitted for CNPG scaling operations

### Phase 3 (Multi-Tenant)

- ✅ 100 tenant clusters, each with 3 CNPG databases = 300 PodMonitors
- ✅ All PodMonitors have correct topology labels
- ✅ Zero manual PodMonitor creation
- ✅ Namespace label changes propagate to PodMonitors within 30 seconds

### Phase 7 (AI Diagnostics)

- ✅ DiagnosticsAgent successfully correlates database crashes with config changes
- ✅ MetricsAgent queries metrics by region: `{region="us-east"}`
- ✅ ReportAgent generates cost reports by cloud provider

---

## Alternatives Considered

### Alternative 1: Manual PodMonitor Creation

**Approach:** Platform Admins manually create PodMonitors for each CNPG cluster.

**Rejected Because:**
- ❌ Doesn't scale to 1000+ clusters
- ❌ Human error in topology label copying
- ❌ No event emission for AI diagnostics
- ❌ Violates "Zero-Ops" principle

---

### Alternative 2: Helm Chart with PodMonitor Template

**Approach:** Template PodMonitors in CNPG Helm chart, pass topology labels via values.

**Rejected Because:**
- ❌ Requires passing topology labels manually via Helm values
- ❌ No dynamic updates when namespace labels change
- ❌ Doesn't work for tenant-provisioned clusters (they don't use Helm)
- ❌ No event emission

---

### Alternative 3: ArgoCD ApplicationSet Generator

**Approach:** Use ArgoCD to generate PodMonitors from CNPG Clusters.

**Rejected Because:**
- ❌ ArgoCD doesn't watch CNPG Cluster CRDs
- ❌ ArgoCD doesn't read namespace labels dynamically
- ❌ No event emission capability
- ❌ Adds unnecessary GitOps complexity

---

### Alternative 4: Grafana Alloy Custom Component

**Approach:** Extend Grafana Alloy to read namespace labels and inject them during scraping.

**Rejected Because:**
- ❌ Alloy doesn't have access to namespace metadata during scraping
- ❌ Would require forking Alloy (maintenance burden)
- ❌ No event emission capability
- ❌ Violates separation of concerns (collection vs orchestration)

---

## Decision: Build cnpg2monitor

### Justification

1. **Unique Value:** No existing tool provides topology label injection from namespace metadata
2. **AI Enablement:** Event emission is critical for DiagnosticsAgent timeline correlation
3. **Scale:** Automation is mandatory for 1000+ clusters
4. **Simplicity:** Operator is lightweight (~500 LOC for Option 1)
5. **Maintainability:** Uses standard controller-runtime patterns

### Recommended Approach

**Option 1: PodMonitor Patcher**

**Rationale:**
- Leverages CNPG's built-in `enablePodMonitor: true`
- Simpler implementation (patch vs create)
- Less code to maintain
- CNPG handles PodMonitor lifecycle

**Implementation Phases:**
1. Phase 2: Build operator, deploy to Management Cluster
2. Phase 3: Deploy to tenant clusters via ClusterResourceSet
3. Phase 4: Validate with Grafana Alloy + VictoriaMetrics
4. Phase 7: Validate event correlation with DiagnosticsAgent

---

## Conclusion

`cnpg2monitor` is a small but critical operator that bridges the gap between CNPG's built-in monitoring and Zero-Ops' fleet-wide observability requirements. While CNPG can create PodMonitors, it cannot inject topology labels from namespace metadata or emit lifecycle events for AI correlation. These capabilities are essential for multi-cluster, multi-tenant environments where manual configuration is infeasible.

**Next Steps:**
1. Finalize implementation approach (Option 1 vs Option 2)
2. Create requirements.md, design.md, tasks.md
3. Implement operator using documented patterns
4. Deploy to Management Cluster (Phase 2)
5. Validate with Grafana Alloy (Phase 4)

---

**Document Owner:** Platform Architecture Team  
**Status:** APPROVED FOR IMPLEMENTATION  
**Last Updated:** 2026-03-10
