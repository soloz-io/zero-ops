Excellent! This clarifies the namespace architecture. Now let me check where ArgoCD actually is and where certificates should be:

Now let me check the current Composition to see what namespace it's using:

Perfect! Now I have the complete picture. Let me provide the final comprehensive report:

---

## **FINAL REPORT: ArgoCD Agent Certificate Architecture**

### **Current State Analysis:**

Based on the ADR, integration docs, and actual cluster state:

**Namespace Architecture (per ADR):**
- ArgoCD is in `platform-ops` namespace ✅
- ClusterResourceSet is in `platform-ops` namespace ✅
- Certificates should be in `platform-ops` namespace (where ArgoCD lives)

**Current ClusterResourceSet References:**
1. ✅ `spoke-pool-eu-prod-01-hetzner-credentials` (Secret) - EXISTS
2. ✅ `cilium-addon-template` (Secret) - EXISTS
3. ✅ `ccm-addon-template` (Secret) - EXISTS
4. ✅ `argocd-namespace` (ConfigMap) - EXISTS
5. ✅ `argocd-agent-deployment` (ConfigMap) - EXISTS
6. ✅ `spoke-pool-eu-prod-01-argocd-agent-config` (ConfigMap) - EXISTS
7. ✅ `argocd-agent-rbac` (ConfigMap) - EXISTS
8. ❌ `argocd-agent-client-cert` (Secret) - **MISSING**
9. ❌ `argocd-agent-ca-secret` (Secret) - **MISSING**

### **The Root Cause:**

The file `manifests/hub-core-services/security/argocd-agent-cert.yaml` defines certificates in the **wrong namespace** (`argocd`) and uses a **template approach** that doesn't match the design.

**What it currently does:**
- Creates certificates in `argocd` namespace
- Uses a single "template" Certificate CR
- Was never deployed (no ArgoCD Application exists)

**What it should do (per integration docs):**
- Create **CA certificates** in `platform-ops` namespace (one-time)
- Crossplane Composition generates **per-cluster certificates** dynamically
- Certificate names: `argocd-agent-<cell-id>-tls`

### **The Correct Solution:**

**Option 1: Simple Fix (Matches Current Composition)**
Create static CA and client certificates in `platform-ops` namespace that can be shared across all spoke pools:

```yaml
# platform-ops namespace
---
# Self-signed CA
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: argocd-agent-ca
  namespace: platform-ops
spec:
  isCA: true
  commonName: argocd-agent-ca
  secretName: argocd-agent-ca-secret
  duration: 87600h  # 10 years
  renewBefore: 720h
  issuerRef:
    name: selfsigned-issuer
    kind: ClusterIssuer

---
# Shared client certificate (all spoke pools use same cert)
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: argocd-agent-client
  namespace: platform-ops
spec:
  secretName: argocd-agent-client-cert
  duration: 2160h  # 90 days
  renewBefore: 168h
  commonName: argocd-agent-client
  usages:
    - client auth
  issuerRef:
    name: argocd-agent-issuer
    kind: Issuer
```

**Option 2: Per-Cluster Certificates (Matches Integration Docs)**
Refactor Composition to use Pipeline mode with function-go-templating to generate per-cluster certificates. This is more complex but more secure.