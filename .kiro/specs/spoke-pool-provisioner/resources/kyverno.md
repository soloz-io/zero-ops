I'll research industry-standard patterns for multi-cluster discovery in GitOps environments.

Based on industry research, here's the standard pattern:

## Industry-Standard Pattern: ArgoCD Cluster Secret Auto-Registration

### **Core Pattern: Kyverno Generate Policy + CAPI**

**Industry Standard:** Kyverno policy watches CAPI `Cluster` resources and auto-generates ArgoCD cluster secrets.

**How It Works:**
1. CAPI provisions Spoke cluster → creates kubeconfig Secret
2. Kyverno watches CAPI `Cluster` CR
3. Kyverno extracts kubeconfig from CAPI Secret
4. Kyverno generates ArgoCD cluster Secret in `argocd` namespace
5. ArgoCD/Headlamp auto-discovers via Secret label `argocd.argoproj.io/secret-type: cluster`

### **Implementation for Zero-Ops**

**Step 1: Deploy Kyverno in Hub**
```bash
helm install kyverno kyverno/kyverno -n kyverno --create-namespace
```

**Step 2: Create Kyverno Policy (GitOps)**
```yaml
# catalog/policies/argocd-cluster-registration.yaml
apiVersion: kyverno.io/v1
kind: ClusterPolicy
metadata:
  name: argocd-cluster-registration
spec:
  generateExisting: true
  rules:
  - name: generate-argocd-cluster-secret
    match:
      any:
      - resources:
          kinds:
          - cluster.x-k8s.io/v1beta1/Cluster
    context:
    - name: clusterName
      variable:
        value: "{{request.object.metadata.name}}"
    - name: kubeconfigSecret
      apiCall:
        urlPath: "/api/v1/namespaces/{{request.object.metadata.namespace}}/secrets/{{clusterName}}-kubeconfig"
        jmesPath: "data.value | base64_decode(@) | parse_yaml(@)"
    - name: server
      variable:
        value: "{{kubeconfigSecret.clusters[0].cluster.server}}"
    - name: caData
      variable:
        value: "{{kubeconfigSecret.clusters[0].cluster.\"certificate-authority-data\"}}"
    - name: token
      variable:
        value: "{{kubeconfigSecret.users[0].user.token}}"
    generate:
      synchronize: true
      apiVersion: v1
      kind: Secret
      name: "cluster-{{clusterName}}"
      namespace: argocd
      data:
        metadata:
          labels:
            argocd.argoproj.io/secret-type: cluster
            spoke-type: "{{request.object.metadata.labels.spoke-type}}"
        type: Opaque
        stringData:
          name: "{{clusterName}}"
          server: "{{server}}"
          config: |
            {
              "bearerToken": "{{token}}",
              "tlsClientConfig": {
                "insecure": false,
                "caData": "{{caData}}"
              }
            }
```

**Step 3: CAPI Composition Ensures Kubeconfig Secret**
```yaml
# xrds/compositions/ainativesaas-enterprise-hetzner.yaml
resources:
- name: spoke-cluster
  base:
    apiVersion: cluster.x-k8s.io/v1beta1
    kind: Cluster
    metadata:
      labels:
        spoke-type: silo  # Kyverno uses this
```

**Step 4: Headlamp Auto-Discovers**
- Headlamp watches Secrets in `argocd` namespace
- Filters by label `argocd.argoproj.io/secret-type: cluster`
- Displays in cluster dropdown automatically

### **For Headlamp Specifically**

**Option A: Use ArgoCD Cluster Secrets (Recommended)**
- Headlamp natively reads ArgoCD cluster secrets
- No additional configuration needed
- Works out-of-box with Kyverno pattern

**Option B: Separate Headlamp Kubeconfig Secret**
```yaml
# Kyverno generates BOTH ArgoCD + Headlamp secrets
generate:
  - apiVersion: v1
    kind: Secret
    name: "cluster-{{clusterName}}"
    namespace: argocd  # For ArgoCD
  - apiVersion: v1
    kind: Secret
    name: "{{clusterName}}.conf"
    namespace: headlamp  # For Headlamp
    data:
      config: "{{kubeconfigSecret | to_string(@)}}"
```

Then mount all secrets:
```yaml
# Headlamp Deployment
volumeMounts:
- name: kubeconfigs
  mountPath: /kubeconfigs
volumes:
- name: kubeconfigs
  projected:
    sources:
    - secret:
        name: hub-kubeconfig
    - secret:
        name: spoke-pool.conf
    # Kyverno auto-generates these:
    - secret:
        name: spoke-silo-tenant1.conf
    - secret:
        name: spoke-silo-tenant2.conf
```

### **ESO + Infisical Integration**

**For Spoke ServiceAccount Tokens:**
```yaml
# Hub: ExternalSecret pulls token from Infisical
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: spoke-silo-tenant1-token
  namespace: argocd
spec:
  secretStoreRef:
    name: infisical-store
    kind: ClusterSecretStore
  target:
    name: spoke-silo-tenant1-token
  data:
  - secretKey: token
    remoteRef:
      key: spoke-silo-tenant1
      property: serviceaccount-token
```

Then Kyverno references ESO-generated secret instead of CAPI secret.

### **Summary**

**Industry Pattern:** Kyverno Generate Policy + CAPI Cluster CR → ArgoCD Cluster Secret → Headlamp Auto-Discovery

**No Custom Code Required:**
- Kyverno (CNCF project)
- ArgoCD cluster secret format (standard)
- Headlamp native support

**Actions During Spoke Provisioning:**
1. Crossplane creates CAPI Cluster CR
2. CAPI creates kubeconfig Secret
3. Kyverno watches, generates ArgoCD secret
4. Headlamp/ArgoCD auto-discover

**Zero manual steps.** Fully GitOps-compliant.