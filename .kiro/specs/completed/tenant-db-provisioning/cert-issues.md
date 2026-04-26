## Permanent Fix Proposal: Pure Crossplane Object Pattern for mTLS Certificates

### Problem Summary
Alloy and NATS pods stuck in `0/N ready` state waiting for mTLS certificates (`alloy-client-cert`, `nats-leafnode-client-cert`) that were never delivered to spoke cluster.

**Root Cause:** Hybrid Kyverno→CRS pattern only applies at bootstrap (11 days ago). Certificates added 33 hours ago never reached spoke.

---

### Solution: Remove Kyverno+CRS, Use Pure Crossplane Objects

Following the industry-standard pattern documented in ADR (Red Hat ACM ManifestWork equivalent), implement continuous reconciliation via Crossplane Objects.

---

### Changes Required

#### 1. Update SpokePool Composition
**File:** `xrds/compositions/spokepool-hetzner.yaml`

**Add 3 Certificate Objects** (after existing resources, before status section):

**Alloy Client Certificate:**
```yaml
- name: alloy-client-cert-object
  base:
    apiVersion: kubernetes.crossplane.io/v1alpha2
    kind: Object
    metadata:
      name: # patched from XR name
    spec:
      managementPolicies: ["*"]
      providerConfigRef:
        name: # patched from XR name
      forProvider:
        manifest:
          apiVersion: v1
          kind: Secret
          metadata:
            name: alloy-client-cert
            namespace: spoke-platform-observability
            labels:
              managed-by: crossplane
              source-cluster: hub
              source-namespace: hub-platform-observability
          type: kubernetes.io/tls
          data: {} # patched from Hub secret
      readinessChecks:
        - type: None
  patches:
    - type: FromCompositeFieldPath
      fromFieldPath: metadata.name
      toFieldPath: spec.providerConfigRef.name
    - type: FromCompositeFieldPath
      fromFieldPath: metadata.name
      toFieldPath: metadata.name
      transforms:
        - type: string
          string:
            fmt: "%s-alloy-cert"
    - type: ToCompositeFieldPath
      fromFieldPath: status.conditions
      toFieldPath: status.certificateDelivery.alloy
```

**NATS Leafnode Client Certificate:**
```yaml
- name: nats-leafnode-cert-object
  base:
    apiVersion: kubernetes.crossplane.io/v1alpha2
    kind: Object
    metadata:
      name: # patched
    spec:
      managementPolicies: ["*"]
      providerConfigRef:
        name: # patched
      forProvider:
        manifest:
          apiVersion: v1
          kind: Secret
          metadata:
            name: nats-leafnode-client-cert
            namespace: spoke-platform-messaging
            labels:
              managed-by: crossplane
              source-cluster: hub
              source-namespace: hub-platform-messaging
          type: kubernetes.io/tls
          data: {} # patched
      readinessChecks:
        - type: None
  patches:
    - type: FromCompositeFieldPath
      fromFieldPath: metadata.name
      toFieldPath: spec.providerConfigRef.name
    - type: FromCompositeFieldPath
      fromFieldPath: metadata.name
      toFieldPath: metadata.name
      transforms:
        - type: string
          string:
            fmt: "%s-nats-cert"
    - type: ToCompositeFieldPath
      fromFieldPath: status.conditions
      toFieldPath: status.certificateDelivery.nats
```

**ArgoCD Agent Client Certificate:**
```yaml
- name: argocd-agent-cert-object
  base:
    apiVersion: kubernetes.crossplane.io/v1alpha2
    kind: Object
    metadata:
      name: # patched
    spec:
      managementPolicies: ["*"]
      providerConfigRef:
        name: # patched
      forProvider:
        manifest:
          apiVersion: v1
          kind: Secret
          metadata:
            name: argocd-agent-client-cert
            namespace: argocd
            labels:
              managed-by: crossplane
              source-cluster: hub
              source-namespace: hub-platform-gitops
          type: kubernetes.io/tls
          data: {} # patched
      readinessChecks:
        - type: None
  patches:
    - type: FromCompositeFieldPath
      fromFieldPath: metadata.name
      toFieldPath: spec.providerConfigRef.name
    - type: FromCompositeFieldPath
      fromFieldPath: metadata.name
      toFieldPath: metadata.name
      transforms:
        - type: string
          string:
            fmt: "%s-argocd-cert"
    - type: ToCompositeFieldPath
      fromFieldPath: status.conditions
      toFieldPath: status.certificateDelivery.argocd
```

**Add Certificate Data Patches** (need to reference Hub secrets - implementation detail TBD based on how cert-manager stores them)

---

#### 2. Remove Kyverno Policies
**Files to delete:**
- `manifests/hub-core-services/security/observability-cert.yaml` (Kyverno transform policies)
- `manifests/hub-core-services/security/nats-leafnode-cert.yaml` (Kyverno transform policies)

**Keep:** CA transformation policies (static, bootstrap-only)

---

#### 3. Update ClusterResourceSet
**File:** `xrds/compositions/spokepool-hetzner.yaml` (CRS section)

**Remove from CRS resources list:**
- `alloy-client-cert` secret reference
- `nats-leafnode-client-cert` secret reference
- `argocd-agent-client-cert` secret reference

**Keep in CRS:** Static bootstrap resources only (Cilium, CCM, CSI, ArgoCD base)

---

#### 4. Update SpokePool XRD (Status Fields)
**File:** `xrds/definitions/spokepool-v1.yaml`

**Add to status schema:**
```yaml
certificateDelivery:
  type: object
  properties:
    alloy:
      type: array
      items:
        type: object
    nats:
      type: array
      items:
        type: object
    argocd:
      type: array
      items:
        type: object
```

---

### Implementation Steps

1. **Read full SpokePool Composition** to understand current structure
2. **Add 3 Certificate Objects** with proper patches
3. **Implement certificate data patching** (reference Hub secrets)
4. **Update SpokePool XRD** with status fields
5. **Remove Kyverno policies** for cert transformation
6. **Update CRS** to remove cert references
7. **Commit via GitOps** (ArgoCD will reconcile)
8. **Verify Crossplane Objects created** on Hub
9. **Verify Secrets created** on Spoke
10. **Verify Alloy and NATS pods become healthy**

---

### Validation Criteria

**Success when:**
```bash
# Hub: Crossplane Objects exist and ready
kubectl get object -n hub-platform-ops | grep cert

# Spoke: Secrets exist
kubectl get secret -n spoke-platform-observability alloy-client-cert
kubectl get secret -n spoke-platform-messaging nats-leafnode-client-cert

# Spoke: Workloads healthy
kubectl get pods -n spoke-platform-observability  # Alloy 2/2 ready
kubectl get pods -n spoke-platform-messaging      # NATS 1/1 ready

# Application status
kubectl get application spoke-pool-eu-prod-01 -n argocd
# status.sync.status=Synced AND status.health.status=Healthy
```

---

### Rollback Plan

If issues occur:
1. Revert Composition changes via Git
2. Re-apply Kyverno policies
3. Manually create secrets on spoke as temporary fix
4. Investigate and retry

---

### Estimated Effort
- **Implementation:** 2-3 hours
- **Testing:** 1 hour
- **Total:** 3-4 hours

---

### Risk Assessment
**Low Risk:**
- Pattern proven by Red Hat ACM (ManifestWork equivalent)
- No impact on existing workloads (additive change)
- Clear rollback path
- Spoke cluster continues running during implementation

---

Ready to proceed with implementation?