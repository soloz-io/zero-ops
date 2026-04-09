# Crossplane Integration Analysis

**Spec**: spoke-pool-provisioner  
**Component**: Crossplane (Infrastructure Provisioning Engine)  
**Purpose**: Orchestrate SpokePool XR → CAPI Cluster + ClusterResourceSet provisioning  
**Status**: Crossplane already implemented in Hub, SpokePool XRD/Composition not implemented

---

## 1. Position in Spoke Pool Provisioning Flow

```
Platform Admin applies SpokePool XR (FR-1.1)
    ↓
Crossplane (Hub) reconciles SpokePool Composition ← THIS INTEGRATION
    ↓
Generates: CAPI Cluster + HetznerCluster + MachineDeployment + ClusterResourceSet
    ↓
CAPI provisions Hetzner VMs (FR-1.2)
    ↓
ClusterResourceSet injects ArgoCD Agent (FR-1.2)
    ↓
Kyverno generates ArgoCD cluster Secret (FR-1.3)
    ↓
ArgoCD deploys edge catalog (FR-2.1)
```

**Critical Constraint**: Crossplane runs ONLY on Hub cluster. It orchestrates infrastructure provisioning but does NOT run in Spoke clusters.

---

## 2. Architecture Overview

### 2.1 Crossplane Hub-Spoke Pattern

```
Hub Cluster (Crossplane Control Plane)
    ↓
    Platform Admin: kubectl apply -f spokepool-01.yaml
    ↓
    Crossplane Controller watches SpokePool XR
    ↓
    Selects Composition based on labels/selectors
    ↓
    Generates Composed Resources:
        - CAPI Cluster CR
        - HetznerCluster CR
        - MachineDeployment CR
        - ClusterResourceSet CR (with ArgoCD Agent manifests)
    ↓
    CAPI Controller provisions Hetzner VMs
    ↓
Spoke Pool Cluster (Provisioned Infrastructure)
```

**Key Principle**: Crossplane is the orchestration layer. It generates CAPI resources, but CAPI does the actual VM provisioning.


### 2.2 SpokePool XRD (Composite Resource Definition)

**Purpose**: Define the schema for SpokePool custom resources

**Spec-Specific Schema** (FR-1.1):
```yaml
apiVersion: apiextensions.crossplane.io/v1
kind: CompositeResourceDefinition
metadata:
  name: spokepools.zero-ops.io
spec:
  group: zero-ops.io
  names:
    kind: SpokePool
    plural: spokepools
  claimNames:
    kind: SpokePoolClaim
    plural: spokepoolclaims
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
              region:
                type: string
                description: "Hetzner datacenter region (e.g., fsn1, nbg1, hel1)"
                enum: ["fsn1", "nbg1", "hel1"]
              nodePool:
                type: object
                properties:
                  count:
                    type: integer
                    description: "Number of worker nodes"
                    minimum: 1
                    maximum: 10
                    default: 3
                  instanceType:
                    type: string
                    description: "Hetzner server type"
                    enum: ["cx21", "cx31", "cx41", "cx51"]
                    default: "cx31"
              maxTenantCapacity:
                type: integer
                description: "Maximum number of tenant schemas per cell"
                minimum: 10
                maximum: 100
                default: 100
            required:
              - region

          status:
            type: object
            properties:
              phase:
                type: string
                description: "Provisioning phase: Pending, Provisioning, Ready, Failed"
              clusterName:
                type: string
                description: "Name of the provisioned CAPI cluster"
              kubeconfig:
                type: string
                description: "Reference to kubeconfig Secret"
              tenantCount:
                type: integer
                description: "Current number of tenants in this cell"
              conditions:
                type: array
                items:
                  type: object
                  properties:
                    type:
                      type: string
                    status:
                      type: string
                    reason:
                      type: string
                    message:
                      type: string
                    lastTransitionTime:
                      type: string
                      format: date-time
```

**Key Design Decisions**:
- `region`: Enum-constrained to valid Hetzner datacenters
- `nodePool.count`: Limited to 1-10 nodes (prevents over-provisioning)
- `maxTenantCapacity`: Limited to 100 tenants per cell (NFR-2.1)
- `status.phase`: Tracks provisioning lifecycle
- `status.tenantCount`: Enables capacity monitoring (US-3.1)


### 2.3 SpokePool Composition

**Purpose**: Define HOW to provision a Spoke Pool cell from a SpokePool XR

**Composition Pattern** (AC-1):
```yaml
apiVersion: apiextensions.crossplane.io/v1
kind: Composition
metadata:
  name: spokepool-hetzner
  labels:
    crossplane.io/xrd: spokepools.zero-ops.io
    provider: hetzner
spec:
  compositeTypeRef:
    apiVersion: zero-ops.io/v1alpha1
    kind: SpokePool
  
  mode: Pipeline
  pipeline:
  - step: render-capi-cluster
    functionRef:
      name: function-go-templating
    input:
      apiVersion: gotemplating.fn.crossplane.io/v1beta1
      kind: GoTemplate
      source: Inline
      inline:
        template: |
          ---
          apiVersion: cluster.x-k8s.io/v1beta1
          kind: Cluster
          metadata:
            name: {{ .observed.composite.resource.metadata.name }}
            namespace: hub-platform-capi
            labels:
              spoke-type: pool
              cell-id: {{ .observed.composite.resource.metadata.name }}
          spec:
            clusterNetwork:
              pods:
                cidrBlocks: ["10.244.0.0/16"]
              services:
                cidrBlocks: ["10.96.0.0/12"]
            controlPlaneRef:
              apiVersion: controlplane.cluster.x-k8s.io/v1beta1
              kind: KubeadmControlPlane
              name: {{ .observed.composite.resource.metadata.name }}-cp
            infrastructureRef:
              apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
              kind: HetznerCluster
              name: {{ .observed.composite.resource.metadata.name }}

          ---
          apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
          kind: HetznerCluster
          metadata:
            name: {{ .observed.composite.resource.metadata.name }}
            namespace: hub-platform-capi
          spec:
            controlPlaneRegion: {{ .observed.composite.resource.spec.region }}
            sshKeys:
              hcloud:
              - name: default
            controlPlaneLoadBalancer:
              enabled: true
              type: lb11
          ---
          apiVersion: controlplane.cluster.x-k8s.io/v1beta1
          kind: KubeadmControlPlane
          metadata:
            name: {{ .observed.composite.resource.metadata.name }}-cp
            namespace: hub-platform-capi
          spec:
            replicas: 1
            version: v1.31.6
            machineTemplate:
              infrastructureRef:
                apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
                kind: HCloudMachineTemplate
                name: {{ .observed.composite.resource.metadata.name }}-cp-mt
            kubeadmConfigSpec:
              clusterConfiguration:
                apiServer:
                  extraArgs:
                    cloud-provider: external
                controllerManager:
                  extraArgs:
                    cloud-provider: external
              initConfiguration:
                nodeRegistration:
                  kubeletExtraArgs:
                    cloud-provider: external
              joinConfiguration:
                nodeRegistration:
                  kubeletExtraArgs:
                    cloud-provider: external

          ---
          apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
          kind: HCloudMachineTemplate
          metadata:
            name: {{ .observed.composite.resource.metadata.name }}-cp-mt
            namespace: hub-platform-capi
          spec:
            template:
              spec:
                type: cx21
                imageName: ubuntu-24.04
          ---
          apiVersion: cluster.x-k8s.io/v1beta1
          kind: MachineDeployment
          metadata:
            name: {{ .observed.composite.resource.metadata.name }}-workers
            namespace: hub-platform-capi
          spec:
            clusterName: {{ .observed.composite.resource.metadata.name }}
            replicas: {{ .observed.composite.resource.spec.nodePool.count }}
            selector:
              matchLabels:
                cluster.x-k8s.io/cluster-name: {{ .observed.composite.resource.metadata.name }}
            template:
              spec:
                clusterName: {{ .observed.composite.resource.metadata.name }}
                version: v1.31.6
                bootstrap:
                  configRef:
                    apiVersion: bootstrap.cluster.x-k8s.io/v1beta1
                    kind: KubeadmConfigTemplate
                    name: {{ .observed.composite.resource.metadata.name }}-workers-kct
                infrastructureRef:
                  apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
                  kind: HCloudMachineTemplate
                  name: {{ .observed.composite.resource.metadata.name }}-workers-mt

          ---
          apiVersion: infrastructure.cluster.x-k8s.io/v1beta1
          kind: HCloudMachineTemplate
          metadata:
            name: {{ .observed.composite.resource.metadata.name }}-workers-mt
            namespace: hub-platform-capi
          spec:
            template:
              spec:
                type: {{ .observed.composite.resource.spec.nodePool.instanceType }}
                imageName: ubuntu-24.04
          ---
          apiVersion: bootstrap.cluster.x-k8s.io/v1beta1
          kind: KubeadmConfigTemplate
          metadata:
            name: {{ .observed.composite.resource.metadata.name }}-workers-kct
            namespace: hub-platform-capi
          spec:
            template:
              spec:
                joinConfiguration:
                  nodeRegistration:
                    kubeletExtraArgs:
                      cloud-provider: external
          ---
          apiVersion: addons.cluster.x-k8s.io/v1beta1
          kind: ClusterResourceSet
          metadata:
            name: {{ .observed.composite.resource.metadata.name }}-argocd-agent
            namespace: hub-platform-capi
          spec:
            clusterSelector:
              matchLabels:
                cell-id: {{ .observed.composite.resource.metadata.name }}
            resources:
            - name: argocd-agent-deployment
              kind: ConfigMap
            - name: argocd-agent-config
              kind: ConfigMap
            - name: argocd-agent-mtls-cert
              kind: Secret
            - name: argocd-agent-mtls-ca
              kind: Secret
            - name: argocd-agent-rbac
              kind: ConfigMap
```

**Key Design Decisions**:
- **Pipeline Mode**: Uses Crossplane function-go-templating for dynamic resource generation
- **CAPI Resources**: Generates 8 resources (Cluster, HetznerCluster, KubeadmControlPlane, 2x HCloudMachineTemplate, MachineDeployment, KubeadmConfigTemplate, ClusterResourceSet)
- **Ubuntu 24.04**: Matches Hub cluster OS (not Talos)
- **Kubernetes v1.31.6**: Matches Hub cluster version
- **ClusterResourceSet**: Contains 5 resources for ArgoCD Agent bootstrap (FR-1.2, AC-3)


---

## 3. Component Responsibilities

### 3.1 Crossplane Controller (Hub)

**Responsibilities**:
- Watch for SpokePool XR create/update/delete events
- Select appropriate Composition based on labels/selectors
- Execute Composition pipeline (function-go-templating)
- Generate CAPI resources (Cluster, HetznerCluster, MachineDeployment, ClusterResourceSet)
- Update SpokePool XR status based on composed resource status
- Handle composition errors and retries

**Configuration Requirements**:
```yaml
# Crossplane Deployment (Hub)
apiVersion: apps/v1
kind: Deployment
metadata:
  name: crossplane
  namespace: crossplane-system
spec:
  replicas: 1
  template:
    spec:
      containers:
      - name: crossplane
        image: crossplane/crossplane:v1.14.5
        args:
        - core
        - start
        - --enable-composition-functions
        - --enable-composition-webhook-schema-validation
        env:
        - name: POD_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
```

**Crossplane Functions** (required for Pipeline mode):
```yaml
# function-go-templating (for SpokePool Composition)
apiVersion: pkg.crossplane.io/v1beta1
kind: Function
metadata:
  name: function-go-templating
spec:
  package: xpkg.upbound.io/crossplane-contrib/function-go-templating:v0.4.0
```


### 3.2 SpokePool XR Lifecycle

**Creation Flow** (US-1.1):
```
1. Platform Admin: kubectl apply -f spokepool-01.yaml
2. Crossplane validates XR against XRD schema
3. Crossplane selects Composition (spokepool-hetzner)
4. Crossplane executes Pipeline:
   - function-go-templating renders CAPI resources
5. Crossplane creates composed resources in hub-platform-capi namespace
6. CAPI Controller provisions Hetzner VMs
7. ClusterResourceSet injects ArgoCD Agent manifests
8. Crossplane updates SpokePool XR status:
   - phase: Provisioning → Ready
   - clusterName: spokepool-01
   - kubeconfig: spokepool-01-kubeconfig
```

**Update Flow**:
```
1. Platform Admin: kubectl patch spokepool spokepool-01 --type=merge -p '{"spec":{"nodePool":{"count":5}}}'
2. Crossplane detects XR spec change
3. Crossplane re-executes Composition pipeline
4. Crossplane updates MachineDeployment.spec.replicas: 3 → 5
5. CAPI scales worker nodes
6. Crossplane updates SpokePool XR status
```

**Deletion Flow**:
```
1. Platform Admin: kubectl delete spokepool spokepool-01
2. Crossplane deletes composed resources (CAPI Cluster, HetznerCluster, etc.)
3. CAPI deprovisions Hetzner VMs
4. Crossplane removes SpokePool XR finalizer
5. SpokePool XR is deleted
```


---

## 4. Idempotency and Reconciliation

### 4.1 Idempotent Operations (NFR-3.1)

**Crossplane Guarantees**:
- Re-applying identical SpokePool XR has no side effects
- Crossplane compares desired state (XR spec) vs actual state (composed resources)
- Only creates/updates/deletes resources when state differs
- Uses Kubernetes server-side apply for atomic updates

**Example**:
```bash
# Apply SpokePool XR twice
kubectl apply -f spokepool-01.yaml
kubectl apply -f spokepool-01.yaml  # No-op, no resources created/updated

# Verify idempotency
kubectl get cluster -n hub-platform-capi spokepool-01 -o yaml
# Only one Cluster resource exists, not two
```

### 4.2 Continuous Reconciliation

**Reconciliation Loop**:
```
Crossplane Controller (every 60s by default):
    ↓
    List all SpokePool XRs
    ↓
    For each XR:
        ↓
        Compare XR.spec vs composed resources
        ↓
        If drift detected:
            ↓
            Re-execute Composition pipeline
            ↓
            Update composed resources
        ↓
        Update XR.status based on composed resource status
```

**Drift Detection**:
- Manual changes to CAPI resources are reverted by Crossplane
- Example: If someone manually scales MachineDeployment replicas, Crossplane resets it to XR.spec.nodePool.count


---

## 5. Status Reporting

### 5.1 Status Propagation

**Flow**:
```
CAPI Cluster.status.phase: Provisioned
    ↓
Crossplane reads composed resource status
    ↓
Crossplane updates SpokePool XR.status.phase: Ready
    ↓
Crossplane updates SpokePool XR.status.conditions
```

**Status Mapping**:
| CAPI Cluster Phase | SpokePool Phase | Reason |
|--------------------|-----------------|--------|
| Pending | Pending | Cluster creation initiated |
| Provisioning | Provisioning | VMs being provisioned |
| Provisioned | Ready | Cluster operational |
| Failed | Failed | Provisioning error |
| Deleting | Deleting | Cluster teardown in progress |

### 5.2 Condition Types

**SpokePool Conditions**:
```yaml
status:
  conditions:
  - type: Ready
    status: "True"
    reason: ClusterProvisioned
    message: "CAPI cluster spokepool-01 is ready"
    lastTransitionTime: "2026-04-08T10:30:00Z"
  - type: Synced
    status: "True"
    reason: ReconcileSuccess
    message: "Successfully reconciled"
    lastTransitionTime: "2026-04-08T10:30:00Z"
```


---

## 6. mTLS Certificate Pre-Generation

### 6.1 Certificate Injection Pattern (AC-1)

**Problem**: ArgoCD Agent requires mTLS certificate at cluster bootstrap, but cert-manager runs in Hub (not Spoke)

**Solution**: Pre-generate certificate in Hub, inject via ClusterResourceSet

**Flow**:
```
1. Crossplane Composition generates Certificate CR (cert-manager)
2. cert-manager issues certificate and stores in Secret
3. Crossplane patches Secret reference into ClusterResourceSet
4. CAPI injects Secret into Spoke cluster at bootstrap
5. ArgoCD Agent mounts Secret and connects to Hub
```

**Composition Snippet**:
```yaml
- step: generate-mtls-cert
  functionRef:
    name: function-go-templating
  input:
    apiVersion: gotemplating.fn.crossplane.io/v1beta1
    kind: GoTemplate
    source: Inline
    inline:
      template: |
        ---
        apiVersion: cert-manager.io/v1
        kind: Certificate
        metadata:
          name: {{ .observed.composite.resource.metadata.name }}-argocd-agent
          namespace: hub-platform-capi
        spec:
          secretName: {{ .observed.composite.resource.metadata.name }}-argocd-agent-mtls
          issuerRef:
            name: argocd-ca-issuer
            kind: ClusterIssuer
          commonName: argocd-agent-{{ .observed.composite.resource.metadata.name }}
          dnsNames:
          - argocd-agent-{{ .observed.composite.resource.metadata.name }}.spoke-pool.svc
          duration: 8760h  # 1 year
          renewBefore: 168h  # 7 days (NFR-4.2)
```


---

## 7. Observability and Monitoring

### 7.1 Metrics

**Crossplane Metrics**:
```
crossplane_managed_resource_exists{name="spokepool-01", kind="Cluster"}
crossplane_managed_resource_ready{name="spokepool-01", kind="Cluster"}
crossplane_managed_resource_synced{name="spokepool-01", kind="Cluster"}
crossplane_composition_reconcile_duration_seconds{composition="spokepool-hetzner"}
crossplane_composition_reconcile_errors_total{composition="spokepool-hetzner"}
```

**Custom Metrics** (via Crossplane status):
```
spokepool_tenant_count{cell_id="spokepool-01"} 45
spokepool_max_capacity{cell_id="spokepool-01"} 100
spokepool_capacity_utilization{cell_id="spokepool-01"} 0.45
```

### 7.2 Logging

**Crossplane Logs**:
```json
{
  "timestamp": "2026-04-08T10:30:00Z",
  "level": "info",
  "msg": "Reconciling composite resource",
  "composite": "spokepool-01",
  "composition": "spokepool-hetzner",
  "phase": "Provisioning"
}
```

**Composition Errors**:
```json
{
  "timestamp": "2026-04-08T10:30:00Z",
  "level": "error",
  "msg": "Composition pipeline failed",
  "composite": "spokepool-01",
  "step": "render-capi-cluster",
  "error": "template execution failed: invalid region"
}
```


---

## 8. Spec Alignment

### 8.1 Functional Requirements

| Requirement | Implementation | Validation |
|-------------|----------------|------------|
| FR-1.1 | SpokePool XRD defines schema: region, nodePool, maxTenantCapacity | XRD YAML validation |
| FR-1.1 | Applying SpokePool XR triggers Crossplane | Crossplane controller logs |
| FR-1.1 | Crossplane provisions CAPI Cluster + HetznerCluster + MachineDeployment | kubectl get cluster -n hub-platform-capi |
| FR-1.1 | ClusterResourceSet automatically created | kubectl get clusterresourceset -n hub-platform-capi |
| FR-1.1 | Cell-id derived from metadata.name | Cluster label: cell-id=spokepool-01 |
| FR-1.2 | ClusterResourceSet contains 5 resources | kubectl get clusterresourceset -o yaml |
| FR-1.2 | mTLS certificate pre-generated | cert-manager Certificate CR |

### 8.2 Non-Functional Requirements

| Requirement | Implementation | Target | Validation |
|-------------|----------------|--------|------------|
| NFR-1.1 | Crossplane + CAPI provisioning | < 15 min | Time from XR apply to Cluster Ready |
| NFR-3.1 | Crossplane idempotent reconciliation | No side effects | Re-apply XR, verify no resource churn |

### 8.3 Acceptance Criteria

| Criteria | Implementation | Validation |
|----------|----------------|------------|
| AC-1 | SpokePool XRD defined | kubectl get xrd spokepools.zero-ops.io |
| AC-1 | SpokePool Composition generates CAPI resources | kubectl get composition spokepool-hetzner |
| AC-1 | Composition patches mTLS cert into ClusterResourceSet | kubectl get secret -n hub-platform-capi |
| AC-1 | Applying SpokePool XR provisions functional cluster | kubectl get cluster -n hub-platform-capi |


---

## 9. Implementation Checklist

### 9.1 Hub Prerequisites

- [ ] Crossplane installed in Hub cluster (v1.14+)
- [ ] Crossplane function-go-templating installed
- [ ] CAPI + CAPH providers installed
- [ ] cert-manager installed and configured
- [ ] Hetzner Cloud API token configured in Crossplane ProviderConfig
- [ ] ArgoCD CA ClusterIssuer created

### 9.2 XRD and Composition

- [ ] SpokePool XRD created: `spokepools.zero-ops.io`
- [ ] XRD schema validated (region enum, nodePool constraints, maxTenantCapacity)
- [ ] SpokePool Composition created: `spokepool-hetzner`
- [ ] Composition uses Pipeline mode with function-go-templating
- [ ] Composition generates 8 CAPI resources
- [ ] Composition generates Certificate CR for mTLS
- [ ] Composition patches certificate Secret into ClusterResourceSet

### 9.3 ClusterResourceSet

- [ ] ClusterResourceSet contains 5 resources (Deployment, ConfigMap, mTLS cert, CA, RBAC)
- [ ] ClusterResourceSet selector matches Cluster labels: `cell-id: <cluster-name>`
- [ ] ArgoCD Agent manifests stored in ConfigMaps
- [ ] mTLS certificate Secret referenced in ClusterResourceSet

### 9.4 Testing

- [ ] Unit test: XRD schema validation
- [ ] Unit test: Composition template rendering
- [ ] Integration test: Apply SpokePool XR, verify CAPI resources created
- [ ] Integration test: Verify ClusterResourceSet bound to Cluster
- [ ] Integration test: Verify mTLS certificate generated
- [ ] E2E test: Apply SpokePool XR, wait for Cluster Ready, verify ArgoCD Agent connects


---

## 10. Design Patterns (from sbt-patterns)

### 10.1 Declarative Provisioning

**Pattern**: Infrastructure as Code via Crossplane XRs

**Application**: Platform Admin declares desired state (SpokePool XR), Crossplane reconciles actual state

**Reference**: `docs/sbt-design-principles.md` - Section 5 (Provisioning Abstraction)

### 10.2 GitOps-First Approach

**Pattern**: All infrastructure changes via Git commits

**Application**: SpokePool XRs stored in Git, ArgoCD syncs to Hub, Crossplane provisions infrastructure

**Reference**: `docs/sbt-design-principles.md` - Section 6 (GitOps-First Approach)

### 10.3 Idempotent Operations

**Pattern**: Re-applying identical configuration has no side effects

**Application**: Crossplane compares desired state vs actual state, only updates when drift detected

**Reference**: `docs/saas-architecture-principles.md` - Operational Excellence Pillar

---

## 11. Troubleshooting Guide

### 11.1 SpokePool XR Stuck in Pending

**Symptom**: SpokePool XR.status.phase remains "Pending" for > 5 minutes

**Diagnosis**:
```bash
# Check Crossplane controller logs
kubectl logs -n crossplane-system deployment/crossplane | grep spokepool-01

# Check Composition selection
kubectl get spokepool spokepool-01 -o yaml | grep composition

# Check composed resources
kubectl get cluster,hetznercluster,machinedeployment -n hub-platform-capi -l cell-id=spokepool-01
```

**Resolution**:
- Verify Composition exists and matches XR labels
- Verify Hetzner Cloud API token is valid
- Verify CAPI + CAPH providers are running


### 11.2 Composition Pipeline Failure

**Symptom**: SpokePool XR.status.phase = "Failed", condition message shows template error

**Diagnosis**:
```bash
# Check Crossplane function logs
kubectl logs -n crossplane-system deployment/function-go-templating

# Check Composition pipeline steps
kubectl get composition spokepool-hetzner -o yaml | grep -A 20 pipeline

# Manually render template
kubectl crossplane beta render spokepool-01.yaml spokepool-hetzner.yaml
```

**Resolution**:
- Verify template syntax (Go template)
- Verify XR spec fields match template expectations
- Verify function-go-templating version compatibility

### 11.3 mTLS Certificate Not Generated

**Symptom**: ClusterResourceSet missing mTLS certificate Secret

**Diagnosis**:
```bash
# Check Certificate CR
kubectl get certificate -n hub-platform-capi | grep spokepool-01

# Check cert-manager logs
kubectl logs -n cert-manager deployment/cert-manager

# Check Secret
kubectl get secret -n hub-platform-capi | grep spokepool-01-argocd-agent-mtls
```

**Resolution**:
- Verify cert-manager is running
- Verify ArgoCD CA ClusterIssuer exists
- Verify Certificate CR is valid

---

**Document Version**: 1.0  
**Last Updated**: 2026-04-08  
**Next Review**: After implementation completion
