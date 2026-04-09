# CAPI + CAPH Integration Analysis

**Feature**: Spoke Pool Provisioner  
**Dependency**: Cluster API (CAPI) + Cluster API Provider Hetzner (CAPH)  
**Analysis Date**: 2026-04-08

---

## 1. Current Project Usage

### 1.1 Existing CAPI Implementation

The project already uses CAPI + CAPH for Hub cluster provisioning via:

**Components**:
- **CAPI Operator**: Manages CAPI lifecycle (`internal/hub/capi/operator.go`)
- **ClusterClass Pattern**: Defines reusable cluster templates (`hetzner-mgmt-ubuntu-v1`)
- **ClusterResourceSet**: Injects CNI (Cilium) and CCM (Hetzner Cloud Controller Manager) at bootstrap
- **Hetzner Provider**: Uses CAPH for VM provisioning on Hetzner Cloud

**Current Flow** (Hub Cluster):
```
1. Install CAPI Operator → cert-manager → CAPI providers (core, kubeadm, hetzner)
2. Apply ClusterClass (hetzner-mgmt-ubuntu-v1.yaml)
3. Apply Cluster CR with topology referencing ClusterClass
4. CAPI provisions: HetznerCluster + KubeadmControlPlane + MachineDeployment
5. ClusterResourceSet injects: Hetzner credentials + Cilium CNI + CCM
6. Cluster becomes Ready
```

### 1.2 Key Files

| File | Purpose |
|------|---------|
| `internal/hub/capi/operator.go` | Installs CAPI Operator + providers (core, kubeadm, hetzner) |
| `internal/hub/capi/secret.go` | Creates Hetzner credentials Secret |
| `internal/hub/cluster/provisioner.go` | Provisions CAPI clusters using ClusterClass |
| `internal/assets/manifests/classes/hetzner-mgmt-ubuntu-v1.yaml` | ClusterClass definition (Ubuntu + kubeadm) |
| `internal/assets/manifests/addons/crs.yaml` | ClusterResourceSet template (CNI + CCM + credentials) |

---

## 2. CAPI Resource Hierarchy

### 2.1 Core Resources

```
Cluster (cluster.x-k8s.io/v1beta1)
├── spec.topology.class: "hetzner-mgmt-ubuntu-v1"  # References ClusterClass
├── spec.topology.version: "v1.31.6"               # Kubernetes version
├── spec.topology.controlPlane.replicas: 3
├── spec.topology.workers.machineDeployments[0]
│   ├── class: "default-worker"
│   └── replicas: 2
└── spec.topology.variables[]                      # ClusterClass variables
    ├── region: "fsn1"
    ├── imageId: "ubuntu-24.04"
    ├── hcloudControlPlaneMachineType: "cpx31"
    └── hcloudWorkerMachineType: "cpx31"
```

### 2.2 Generated Resources (by CAPI)

When a Cluster CR is applied, CAPI generates:

```
HetznerCluster (infrastructure.cluster.x-k8s.io/v1beta1)
├── spec.controlPlaneEndpoint.host: "<load-balancer-ip>"
├── spec.controlPlaneLoadBalancer.enabled: true
├── spec.hcloudNetwork.cidrBlock: "10.0.0.0/16"
└── spec.hetznerSecretRef.name: "hetzner-credentials"

KubeadmControlPlane (controlplane.cluster.x-k8s.io/v1beta1)
├── spec.replicas: 3
├── spec.version: "v1.31.6"
├── spec.kubeadmConfigSpec.clusterConfiguration
│   ├── apiServer.extraArgs.cloud-provider: "external"
│   └── controllerManager.extraArgs.cloud-provider: "external"
└── spec.machineTemplate.infrastructureRef
    └── HCloudMachineTemplate (control-plane-machine)

MachineDeployment (cluster.x-k8s.io/v1beta1)
├── spec.replicas: 2
├── spec.template.spec.bootstrap.configRef
│   └── KubeadmConfigTemplate (worker-bootstrap)
└── spec.template.spec.infrastructureRef
    └── HCloudMachineTemplate (worker-machine)
```

### 2.3 ClusterResourceSet Injection

```
ClusterResourceSet (addons.cluster.x-k8s.io/v1beta1)
├── spec.clusterSelector.matchLabels
│   └── cluster.x-k8s.io/cluster-name: "<cluster-name>"
├── spec.strategy: "ApplyOnce"  # Only inject once at bootstrap
└── spec.resources[]
    ├── Secret: hetzner-credentials  # Hetzner API token
    ├── Secret: cilium-addon         # Cilium CNI manifests
    └── Secret: ccm-addon            # Hetzner CCM manifests
```

**Injection Timing**: CAPI injects ClusterResourceSet resources when:
- Cluster.status.phase = "Provisioned"
- Control plane node is Ready
- Before cluster becomes fully operational

---

## 3. Kubeconfig Secret Generation

### 3.1 CAPI-Generated Secret

When a CAPI cluster becomes Ready, CAPI automatically creates a kubeconfig Secret:

**Secret Name**: `<cluster-name>-kubeconfig`  
**Namespace**: Same as Cluster CR  
**Type**: `cluster.x-k8s.io/secret`  
**Data Key**: `value` (contains base64-encoded kubeconfig)

**Example**:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: spokepool-01-kubeconfig
  namespace: hub-platform-capi
  labels:
    cluster.x-k8s.io/cluster-name: spokepool-01
type: cluster.x-k8s.io/secret
data:
  value: <base64-encoded-kubeconfig>
```

### 3.2 Kubeconfig Structure

```yaml
apiVersion: v1
kind: Config
clusters:
- cluster:
    certificate-authority-data: <ca-cert>
    server: https://<load-balancer-ip>:6443
  name: spokepool-01
contexts:
- context:
    cluster: spokepool-01
    user: spokepool-01-admin
  name: spokepool-01-admin@spokepool-01
current-context: spokepool-01-admin@spokepool-01
users:
- name: spokepool-01-admin
  user:
    client-certificate-data: <client-cert>
    client-key-data: <client-key>
```

---

## 4. Integration Points for Spoke Pool Provisioner

### 4.1 SpokePool XRD → CAPI Cluster

**Crossplane Composition Flow**:
```
SpokePool XR (Applied by Platform Admin)
  ↓
Crossplane Composition (spoke-pool-hetzner-v1)
  ↓
Generates:
  1. CAPI Cluster CR (with topology referencing ClusterClass)
  2. ClusterResourceSet CR (ArgoCD Agent + mTLS certs)
  3. (Optional) ArgoCD Application CR for edge-catalog
```

**Key Differences from Hub Cluster**:
| Aspect | Hub Cluster | Spoke Pool Cluster |
|--------|-------------|-------------------|
| ClusterClass | `hetzner-mgmt-ubuntu-v1` | Reuse same ClusterClass |
| ClusterResourceSet | CNI + CCM + credentials | ArgoCD Agent + mTLS certs |
| Node Count | 3 CP + 2 workers | Configurable via SpokePool XR |
| Network | Hub-specific CIDR | Cell-specific CIDR |
| Purpose | Management cluster | Tenant workload cluster |

### 4.2 ClusterResourceSet for Spoke Pool

**Current CRS** (Hub):
```yaml
spec:
  resources:
  - Secret: hetzner-credentials
  - Secret: cilium-addon
  - Secret: ccm-addon
```

**Required CRS** (Spoke Pool):
```yaml
spec:
  resources:
  - Secret: argocd-agent-deployment    # ArgoCD Agent Deployment manifest
  - Secret: argocd-agent-config        # Agent ConfigMap (Hub URL, cluster name)
  - Secret: argocd-agent-mtls-cert     # Client certificate for Hub ArgoCD
  - Secret: argocd-agent-ca            # CA bundle for Hub ArgoCD
  - Secret: argocd-agent-rbac          # ServiceAccount + RBAC for Agent
```

**Why Different?**:
- Hub cluster needs CNI/CCM to become operational
- Spoke Pool needs ArgoCD Agent to pull edge-catalog from Hub
- Edge-catalog (CNPG, NATS, Spire, Alloy) is deployed AFTER agent connects via ArgoCD ApplicationSet

### 4.3 Cluster Discovery (CAPI → ArgoCD)

**Current Gap**: CAPI generates kubeconfig Secret, but ArgoCD doesn't automatically discover it.

**Solution**: Kyverno ClusterPolicy (covered in separate integration doc)

**Flow**:
```
1. CAPI Cluster becomes Ready
2. CAPI creates <cluster-name>-kubeconfig Secret
3. Kyverno watches Cluster.status.phase=Provisioned
4. Kyverno extracts kubeconfig from Secret
5. Kyverno generates ArgoCD cluster Secret
6. ArgoCD discovers cluster within 30 seconds
```

---

## 5. Required Changes for Spoke Pool

### 5.1 New ClusterClass (Optional)

**Decision**: Reuse `hetzner-mgmt-ubuntu-v1` ClusterClass for Spoke Pool clusters.

**Rationale**:
- Same OS (Ubuntu 24.04)
- Same Kubernetes version (v1.31.6)
- Same bootstrap mechanism (kubeadm)
- Only difference is ClusterResourceSet contents

**Alternative**: Create `hetzner-spoke-pool-ubuntu-v1` if Spoke Pool needs different:
- Node sizing defaults
- Network configuration
- Kubernetes version

### 5.2 New ClusterResourceSet Template

**File**: `internal/assets/manifests/addons/spoke-pool-crs.yaml`

**Template Variables**:
```go
type SpokePoolCRSData struct {
    ClusterName       string  // e.g., "spokepool-01"
    Namespace         string  // "hub-platform-capi"
    HubArgoURL        string  // "argocd-agent-principal.hub-platform-ops.svc.cluster.local:8443"
    AgentMTLSCert     string  // Pre-generated client certificate
    AgentMTLSKey      string  // Pre-generated client key
    AgentCA           string  // Hub ArgoCD CA bundle
}
```

### 5.3 Crossplane Composition Structure

**File**: `xrds/compositions/spokepool-hetzner-v1.yaml`

**Composition Resources**:
```yaml
apiVersion: apiextensions.crossplane.io/v1
kind: Composition
metadata:
  name: spokepool-hetzner-v1
spec:
  compositeTypeRef:
    apiVersion: zero-ops.io/v1alpha1
    kind: SpokePool
  resources:
  - name: capi-cluster
    base:
      apiVersion: cluster.x-k8s.io/v1beta1
      kind: Cluster
      spec:
        topology:
          class: hetzner-mgmt-ubuntu-v1
          version: v1.31.6
    patches:
    - type: FromCompositeFieldPath
      fromFieldPath: spec.region
      toFieldPath: spec.topology.variables[0].value
    - type: FromCompositeFieldPath
      fromFieldPath: spec.nodePool.controlPlaneReplicas
      toFieldPath: spec.topology.controlPlane.replicas
    - type: FromCompositeFieldPath
      fromFieldPath: spec.nodePool.workerReplicas
      toFieldPath: spec.topology.workers.machineDeployments[0].replicas
  
  - name: cluster-resource-set
    base:
      apiVersion: addons.cluster.x-k8s.io/v1beta1
      kind: ClusterResourceSet
      spec:
        clusterSelector:
          matchLabels:
            spoke-type: pool
        strategy: ApplyOnce
    patches:
    - type: FromCompositeFieldPath
      fromFieldPath: metadata.name
      toFieldPath: spec.clusterSelector.matchLabels[cluster.x-k8s.io/cluster-name]
```

---

## 6. Certificate Generation for ArgoCD Agent

### 6.1 Pre-Generation Requirement

**Timing**: Certificates MUST be generated BEFORE SpokePool XR is applied.

**Why**: ClusterResourceSet is injected at cluster bootstrap (before any workloads run). Certificates must exist in Hub cluster to be patched into CRS.

### 6.2 Certificate Generation Flow

```
1. Platform Admin prepares to create Spoke Pool
2. Hub generates mTLS certificate for ArgoCD Agent:
   - cert-manager Certificate CR
   - Subject: CN=argocd-agent-spokepool-01
   - Issuer: Hub ArgoCD CA
3. cert-manager creates Secret: argocd-agent-spokepool-01-tls
4. Crossplane Composition reads Secret
5. Composition patches certificate into ClusterResourceSet
6. CAPI injects CRS into Spoke Pool at bootstrap
7. ArgoCD Agent starts with pre-provisioned certificates
```

### 6.3 Certificate CR Example

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: argocd-agent-spokepool-01
  namespace: hub-platform-capi
spec:
  secretName: argocd-agent-spokepool-01-tls
  issuerRef:
    name: argocd-ca-issuer
    kind: ClusterIssuer
  commonName: argocd-agent-spokepool-01
  dnsNames:
  - argocd-agent-spokepool-01
  usages:
  - client auth
```

---

## 7. Open Questions

### 7.1 ClusterClass Reuse

**Q**: Should Spoke Pool clusters reuse `hetzner-mgmt-ubuntu-v1` ClusterClass or have a dedicated `hetzner-spoke-pool-ubuntu-v1`?

**Recommendation**: Reuse existing ClusterClass unless Spoke Pool needs different defaults (node sizing, network config).

### 7.2 ClusterResourceSet Strategy

**Q**: Should CRS use `ApplyOnce` or `Reconcile` strategy?

**Current**: `ApplyOnce` (Hub cluster)  
**Recommendation**: `ApplyOnce` for Spoke Pool (ArgoCD Agent is immutable after bootstrap)

### 7.3 Certificate Rotation

**Q**: How are ArgoCD Agent mTLS certificates rotated after initial bootstrap?

**Options**:
1. Manual rotation (Platform Admin generates new cert, updates Secret)
2. Automatic rotation (cert-manager + external-secrets sync to Spoke)
3. Deferred to Phase 2 (Phase 1 uses long-lived certs)

**Recommendation**: Phase 1 uses long-lived certs (1 year), rotation deferred to Phase 2.

---

## 8. Integration Summary

### 8.1 What We Reuse

✅ CAPI Operator installation (`internal/hub/capi/operator.go`)  
✅ ClusterClass pattern (`hetzner-mgmt-ubuntu-v1`)  
✅ Cluster provisioning flow (`internal/hub/cluster/provisioner.go`)  
✅ Kubeconfig Secret generation (CAPI automatic)  
✅ Hetzner credentials management (`internal/hub/capi/secret.go`)

### 8.2 What We Add

🆕 SpokePool XRD definition (`xrds/definitions/spokepool-v1.yaml`)  
🆕 SpokePool Composition (`xrds/compositions/spokepool-hetzner-v1.yaml`)  
🆕 Spoke Pool ClusterResourceSet template (`internal/assets/manifests/addons/spoke-pool-crs.yaml`)  
🆕 ArgoCD Agent mTLS certificate generation (cert-manager Certificate CR)  
🆕 Kyverno ClusterPolicy for cluster discovery (separate integration doc)

### 8.3 What We Modify

🔧 `internal/hub/cluster/provisioner.go`: Add `ProvisionSpokePool()` method  
🔧 `internal/assets/manifests/addons/`: Add `spoke-pool-crs.yaml` template  
🔧 Crossplane provider configuration: Add SpokePool XRD registration

---

## 9. Next Steps

1. ✅ **CAPI + CAPH Integration** (this document)
2. ⏭️ **Kyverno Integration** (cluster discovery policy)
3. ⏭️ **ArgoCD Integration** (ApplicationSet for edge-catalog)
4. ⏭️ **cert-manager Integration** (mTLS certificate generation)

---

**Document Status**: Complete  
**Ready for Design Phase**: Yes  
**Blockers**: None
