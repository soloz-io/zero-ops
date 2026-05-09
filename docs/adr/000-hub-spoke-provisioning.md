# ADR: AINativeSaaS Hub-Spoke Provisioning Pattern

**Status**: Accepted  
**Date**: 2026-04-17  
**Context**: Spoke Pool Provisioner (Phase 3 - Tenant Database Provisioning)  
**Deciders**: Platform Team

---

## Context and Problem Statement

The AINativeSaaS XR provisions per-tenant resources (Database, Pooler, PostgREST, AtlasMigration) in Spoke Pool clusters. We need to determine:

1. **Where should the AINativeSaaS XR be created?** (Hub or Spoke)
2. **How should Crossplane provision resources to Spoke clusters?**
3. **What is the idiomatic Hub-Spoke control plane pattern?**

**Current Implementation**: 
- ApplicationSet deploys Universal Tenant Helm Chart to Spoke cluster
- Helm Chart generates AINativeSaaS XR
- XR is created on Spoke cluster
- **Problem**: Crossplane runs on Hub, not Spoke - XR cannot be reconciled

**Impact**: Phase 3 validation blocked - tenant databases cannot be provisioned.

---

## Decision Drivers

- **Hub-Spoke Separation**: Hub owns intent, Spoke owns stateful workloads
- **Crossplane Location**: Crossplane runs on Hub cluster only
- **Idiomatic Pattern**: Follow industry-standard control plane patterns
- **Declarative Lifecycle**: Single XR creates all tenant resources atomically
- **Consistency**: Align with SpokePool provisioning pattern (uses provider-kubernetes)

---

## Decision Outcome

**Chosen Pattern**: Hub-Based XR with provider-kubernetes Provisioning

### Architecture

```
Hub Cluster (Control Plane)
├── AINativeSaaS XR created here
├── Crossplane reconciles XR
└── provider-kubernetes provisions to Spoke

Spoke Cluster (Data Plane)
├── CNPG Database (provisioned by Crossplane)
├── CNPG Pooler (provisioned by Crossplane)
├── PostgREST Deployment (provisioned by Crossplane)
├── PostgREST Service (provisioned by Crossplane)
└── AtlasMigration CR (provisioned by Crossplane)
```

### Resource Flow

```
1. ApplicationSet generates two Applications:
   - tenant-<id>-xr (destination: Hub)
   - tenant-<id>-spoke (destination: Spoke)

2. Hub Application deploys:
   - AINativeSaaS XR (to Hub cluster)

3. Spoke Application deploys:
   - Namespace
   - RBAC
   - ResourceQuota

4. Crossplane (on Hub) reconciles XR:
   - Wraps resources in provider-kubernetes Object
   - provider-kubernetes provisions to Spoke cluster

5. Resources created on Spoke:
   - CNPG Database CR
   - CNPG Pooler CR
   - PostgREST Deployment + Service
   - AtlasMigration CR

6. Atlas Operator (on Spoke) reconciles AtlasMigration:
   - Applies baseline + tenant-specific migrations
   - Updates CR status: Ready=True
```

---

## Rationale

### 1. Hub Owns Intent, Spoke Owns State

**Idiomatic Control Plane Pattern**:
- **Hub (Control Plane)**: Defines what should exist (XRs, Compositions)
- **Spoke (Data Plane)**: Runs stateful workloads (databases, applications)

This separation enables:
- Centralized governance and policy
- Spoke autonomy (can operate independently)
- Clear failure domains

### 2. Crossplane Runs on Hub Only

**Technical Reality**:
- Crossplane is deployed to Hub cluster
- Crossplane cannot reconcile XRs on Spoke clusters
- Creating XR on Spoke would require Crossplane on every Spoke (anti-pattern)

**Scalability**:
- 100 Spoke clusters = 100 Crossplane instances (wasteful)
- Hub-based Crossplane = single control plane for all Spokes

### 3. provider-kubernetes Bridges Hub to Spoke

**Proven Pattern** (from SpokePool Composition):
- Crossplane creates `kubernetes.crossplane.io/v1alpha2/Object` resources
- provider-kubernetes applies manifests to target cluster
- Spoke resources managed declaratively from Hub

**Consistency**:
- SpokePool uses provider-kubernetes to provision CAPI clusters
- AINativeSaaS should use same pattern for tenant resources

### 3.1 Strict Separation: GitOps for Primitives, Control Planes for Infra

**Enterprise Pattern:**
- **ArgoCD/Flux strictly owns Kubernetes primitives** (Namespaces, RBAC, ConfigMaps, ResourceQuotas, Services, Deployments)
- **Crossplane strictly owns external/stateful infrastructure** (Cloud VMs, Databases, IAM, Buckets, XRs)

**Why This Matters:**
- Prevents "split-brain" race conditions between ArgoCD and Crossplane
- Clear ownership boundaries reduce operational complexity
- Aligns with idiomatic cloud-native patterns where GitOps manages KRM and control planes manage infrastructure

**Banned Pattern:**
- ❌ Crossplane using `provider-kubernetes` to push basic Kubernetes YAML (Namespaces, Roles, RoleBindings)
- ❌ ArgoCD managing external infrastructure resources

**Correct Pattern:**
- ✅ ArgoCD (Universal Tenant Helm Chart) creates Namespace, RBAC, ResourceQuota on Spoke
- ✅ Crossplane creates CNPG Database, Pooler, PostgREST, AtlasMigration on Spoke
- ✅ Clear separation: KRM primitives via GitOps, infrastructure via Control Plane

### 4. AtlasMigration CR Included in Composition

**Critical Decision**: Crossplane creates AtlasMigration CR (not separate Helm chart)

**Why This Matters**:
- **Atomic Provisioning**: Database + Schema applied together
- **No Race Conditions**: PostgREST waits for migration Ready
- **Declarative Lifecycle**: Delete XR = delete DB + schema
- **No Drift**: Schema always matches database state

**Anti-Pattern to Avoid**:
```
❌ App creates DB
❌ Separate process applies schema
❌ PostgREST starts before schema ready
❌ Tenant provisioning fails
```

**Idiomatic Pattern**:
```
✅ XR creates DB + Pooler + PostgREST + AtlasMigration
✅ Crossplane ensures correct order
✅ PostgREST depends on AtlasMigration Ready
✅ Tenant provisioning atomic
```

### 5. Clean Mental Model

**"Creating a tenant is a single API call"**

```bash
kubectl apply -f tenant-acme.yaml  # AINativeSaaS XR on Hub
```

Behind the scenes:
1. Crossplane provisions infrastructure
2. Atlas prepares schema
3. PostgREST exposes API
4. Tenant ready in < 5 seconds

### 6. mTLS Certificate Delivery Pattern

#### Pattern Overview

Zero-Ops uses Crossplane Objects with `references.patchesFrom` to distribute mTLS certificates from Hub to Spoke clusters. This pattern provides continuous reconciliation, automatic rotation, and status tracking without requiring custom agents or bootstrap-only mechanisms.

#### Architecture

**Components:**
- **Hub Cluster**: cert-manager generates certificates, Crossplane distributes them
- **Spoke Clusters**: Receive certificates via provider-kubernetes
- **Crossplane Object**: Defines certificate distribution with source reference
- **provider-kubernetes**: Applies manifests to target Spoke cluster

**Certificate Types:**
1. **CA Certificates** (bootstrap-only, via ClusterResourceSet)
   - Distributed once during cluster creation
   - Static, long-lived (10 years)
   - Used by workloads to verify server/client certificates

2. **Client Certificates** (continuous reconciliation, via Crossplane Object)
   - Distributed and updated continuously
   - Short-lived (90 days), auto-renewed
   - Used by workloads for mTLS authentication

#### Certificate Generation Flow

**Per-Spoke Certificate Generation:**
```
1. SpokePool XR created on Hub
   ↓
2. Crossplane Composition creates Certificate Objects
   - ArgoCD Agent client cert (platform-ops namespace)
   - Alloy client cert (platform-observability namespace)
   - NATS Leafnode client cert (platform-messaging namespace)
   ↓
3. cert-manager generates certificates
   - CN matches cluster name (e.g., "spoke-pool-eu-prod-01")
   - Stored in kubernetes.io/tls Secrets
   - Duration: 90 days, renewBefore: 7 days
```

**Key Properties:**
- One certificate per Spoke cluster (not shared)
- CN matches cluster name for identity verification
- Certificates generated dynamically via Composition

#### Certificate Distribution Flow

**Distribution Path:**
```
Hub: cert-manager → Secret (platform-observability/spoke-pool-eu-prod-01-alloy-client-cert)
     ↓
Hub: Crossplane Object references Hub Secret via patchesFrom
     ↓
Hub: provider-kubernetes reads Secret data and applies to Spoke
     ↓
Spoke: Secret created (platform-observability/alloy-client-cert)
     ↓
Spoke: Alloy DaemonSet mounts certificate via volumeMount
```

#### Continuous Reconciliation

**Automatic Certificate Rotation:**
1. cert-manager rotates certificate on Hub (7 days before expiration)
2. Hub Secret updated with new certificate data
3. Crossplane detects Secret change (watches source Secret)
4. provider-kubernetes pushes updated Secret to Spoke
5. Spoke workload reloads certificate (via volume mount watch)
6. No manual intervention or service restart required

**Reconciliation Guarantees:**
- Crossplane continuously watches source Secrets
- Changes propagate within reconciliation interval (~30s)
- Failed deliveries retry with exponential backoff
- Status conditions reflect delivery state

#### Status Tracking

**Object Readiness Conditions:**
```yaml
status:
  conditions:
    - type: Ready
      status: "True"
      reason: ReconcileSuccess
    - type: Synced
      status: "True"
      reason: ReconcileSuccess
```

**XR Status Propagation:**
- Certificate distribution Objects report readiness to parent XR
- Failed deliveries visible in XR status conditions
- Operators can monitor fleet-wide certificate health

**Traceability:**
- Labels link Spoke Secret to Hub source (`source-cluster`, `source-namespace`)
- Object name includes cluster ID for easy identification
- Crossplane logs show distribution events

#### Blast Radius Isolation

**Per-Spoke ProviderConfig:**
- One ProviderConfig per Spoke cluster
- Separate kubeconfig Secret per Spoke
- Credential compromise affects single Spoke only
- Hub maintains isolation between Spoke clusters

**Security Properties:**
- Certificates never transit through Git
- Hub-to-Spoke communication uses kubeconfig credentials
- Spoke workloads mount certificates from local Secrets
- No shared credentials across Spoke clusters

**Key Patterns:**
- Certificate generation and distribution are separate Objects
- `references.patchesFrom` copies Secret data from Hub to Spoke
- Static secret names on Spoke (no dynamic naming)
- ProviderConfig patched from XR metadata.name (cluster ID)

#### Operational Characteristics

**Advantages:**
- ✅ Continuous reconciliation (not bootstrap-only)
- ✅ Automatic rotation (no manual intervention)
- ✅ Status tracking (visibility into delivery state)
- ✅ Standard Kubernetes API (no custom agents)
- ✅ Aligns with SpokePool provisioning pattern
- ✅ Single control plane tool (Crossplane)

**Limitations:**
- ⚠️ Requires ProviderConfig per Spoke cluster
- ⚠️ Composition verbosity (Object wrappers)
- ⚠️ Debugging requires Crossplane + provider-kubernetes logs

**Operational Requirements:**
- Hub must have network access to Spoke API servers
- Spoke kubeconfig Secrets must be kept up-to-date
- provider-kubernetes needs RBAC for Secret creation on Spoke

---

## Implementation

### Updated ApplicationSet Pattern

**Two ApplicationSets** (replace single ApplicationSet):

1. **tenant-xr-provisioning** (Hub destination)
   - Deploys AINativeSaaS XR to Hub cluster
   - Crossplane reconciles XR
   - Uses provider-kubernetes to provision to Spoke

2. **tenant-spoke-provisioning** (Spoke destination)
   - Deploys Namespace, RBAC, ResourceQuota to Spoke
   - No XR (XR stays on Hub)

### Updated Composition

**Critical Rule:** Crossplane Compositions MUST NOT manage Kubernetes primitives (Namespaces, RBAC, ResourceQuotas). These are the exclusive domain of ArgoCD via the Universal Tenant Helm Chart. Crossplane exclusively provisions external/stateful infrastructure (Cloud VMs, Databases, IAM, Buckets, XRs).

**AINativeSaaS Composition** must include (via provider-kubernetes Object wrappers):

1. **CNPG Database CR**
   ```yaml
   - name: database
     base:
       apiVersion: kubernetes.crossplane.io/v1alpha2
       kind: Object
       spec:
         providerConfigRef:
           name: spoke-pool-provider-config
         forProvider:
           manifest:
             apiVersion: postgresql.cnpg.io/v1
             kind: Database
             spec:
               cluster:
                 name: shared-cnpg
               owner: postgres
   ```

2. **CNPG Pooler CR** (transaction mode, 5 connections)

3. **PostgREST Deployment** (connects via pooler)

4. **PostgREST Service** (ClusterIP, internal only)

5. **AtlasMigration CR** (baseline + tenant-specific migrations)
   ```yaml
   - name: atlas-migration
     base:
       apiVersion: kubernetes.crossplane.io/v1alpha2
       kind: Object
       spec:
         providerConfigRef:
           name: spoke-pool-provider-config
         forProvider:
           manifest:
             apiVersion: db.atlasgo.io/v1alpha1
             kind: AtlasMigration
             spec:
               url: postgresql://postgres@pooler:5432/tenant_acme_db
               dir:
                 configMapRef:
                   name: tenant-migrations
   ```

### ProviderConfig per Spoke

**Required**: One ProviderConfig per Spoke cluster

```yaml
apiVersion: kubernetes.crossplane.io/v1alpha1
kind: ProviderConfig
metadata:
  name: spoke-pool-eu-prod-01
spec:
  credentials:
    source: Secret
    secretRef:
      name: spoke-pool-eu-prod-01-kubeconfig
      namespace: platform-ops
      key: kubeconfig
```

**How Crossplane Knows Which Spoke**:
- AINativeSaaS XR includes `spec.cellId: spoke-pool-eu-prod-01`
- Composition patches cellId to `providerConfigRef.name`
- provider-kubernetes uses correct kubeconfig for target Spoke

---

## Consequences

### Positive

✅ **Idiomatic Hub-Spoke**: Hub owns intent, Spoke owns state  
✅ **Atomic Provisioning**: DB + Schema + API created together  
✅ **No Race Conditions**: PostgREST waits for migration Ready  
✅ **Declarative Lifecycle**: Delete XR = delete all resources  
✅ **Consistent Pattern**: Same as SpokePool (provider-kubernetes)  
✅ **Scalable**: Single Crossplane for all Spokes  
✅ **Clean Mental Model**: One API call creates tenant

### Negative

⚠️ **Composition Complexity**: More verbose with Object wrappers  
⚠️ **ProviderConfig Management**: One per Spoke cluster  
⚠️ **Debugging**: Need to check Crossplane + provider-kubernetes logs  
⚠️ **Migration Effort**: Update existing Composition + ApplicationSet

### Neutral

🔄 **Two ApplicationSets**: XR (Hub) + Spoke resources (Spoke)  
🔄 **Kubeconfig Management**: Crossplane needs Spoke kubeconfigs  
🔄 **RBAC**: provider-kubernetes needs CNPG + Atlas permissions

---

## Alternatives Considered

### Why Not XR on Spoke?

**Rejected Reasons**:
- Requires Crossplane on every Spoke (100 instances)
- Spoke should be data plane, not control plane
- Violates Hub-Spoke separation of concerns
- Doesn't scale (Crossplane overhead per Spoke)

### Why Not Separate AtlasMigration?

**Rejected Reasons**:
- Race condition: DB exists but schema doesn't
- PostgREST starts before migrations applied
- Broken tenant provisioning
- Not atomic (DB and schema separate lifecycles)

**Correct Pattern**:
- Crossplane creates AtlasMigration CR
- Atlas Operator (on Spoke) applies migrations
- PostgREST depends on AtlasMigration Ready

---


## References

- Crossplane provider-kubernetes: https://marketplace.upbound.io/providers/crossplane-contrib/provider-kubernetes
- Hub-Spoke Control Plane Patterns: https://kubernetes.io/blog/2021/12/22/kubernetes-in-kubernetes-and-pxe-bootable-server-farm/
- Atlas Kubernetes Operator: https://atlasgo.io/integrations/kubernetes/operator

---

**Document Version**: 1.0  
**Last Updated**: 2026-04-17  
**Next Review**: After Phase 3 completion
