# Infisical + External Secrets Operator Integration

**Status:** Production Ready  
**Last Updated:** 2026-03-30  
**ESO Version:** v2.2.0 (Helm chart v0.14.2)  
**Infisical Version:** v1.7.5

## Overview

This document describes the integration between Infisical (secret storage backend) and External Secrets Operator (ESO) for GitOps-compliant secret synchronization in the Zero-Ops platform.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     GitOps Flow                              │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│  Git Repo                                                     │
│  └── manifests/                                               │
│      ├── crds/                  (Sync Wave 1)                 │
│      │   ├── eso-crd-00.yaml   # ClusterExternalSecret       │
│      │   ├── eso-crd-21.yaml   # ClusterSecretStore          │
│      │   └── eso-crd-22.yaml   # SecretStore                 │
│      │                                                         │
│      ├── platform-external-secrets/  (Sync Wave 4)            │
│      │   └── helm-release.yaml # ESO Controller              │
│      │                                                         │
│      └── platform-cluster-secret-store/  (Sync Wave 5)        │
│          └── cluster-secret-store.yaml                        │
│                                                               │
│                          ↓ ArgoCD Sync                        │
│                                                               │
├─────────────────────────────────────────────────────────────┤
│                   Kubernetes Cluster                          │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│  ┌──────────────────────────────────────────────┐            │
│  │  External Secrets Operator (ESO)             │            │
│  │  Namespace: external-secrets-system          │            │
│  │                                               │            │
│  │  Controller: platform-external-secrets       │            │
│  │  Image: ghcr.io/external-secrets/            │            │
│  │         external-secrets:v2.2.0              │            │
│  └──────────────────────────────────────────────┘            │
│                          ↓                                    │
│                   Watches CRDs                                │
│                          ↓                                    │
│  ┌──────────────────────────────────────────────┐            │
│  │  ClusterSecretStore                          │            │
│  │  Name: infisical-backend                     │            │
│  │                                               │            │
│  │  Provider: Infisical                         │            │
│  │  ServerURL: https://infisical.zero-ops-      │            │
│  │             system.svc                       │            │
│  │  Auth: Universal Auth (client credentials)  │            │
│  │  Scope: platform/prod                        │            │
│  └──────────────────────────────────────────────┘            │
│                          ↓                                    │
│                   Connects to                                 │
│                          ↓                                    │
│  ┌──────────────────────────────────────────────┐            │
│  │  Infisical API                               │            │
│  │  Namespace: zero-ops-system                  │            │
│  │                                               │            │
│  │  Service: infisical.zero-ops-system.svc      │            │
│  │  Database: PostgreSQL (CNPG + PgBouncer)     │            │
│  │  Secrets: Project "platform", Env "prod"     │            │
│  └──────────────────────────────────────────────┘            │
│                                                               │
└─────────────────────────────────────────────────────────────┘
```

## Deployment History

### Issue 1: CRD Size Exceeded ArgoCD Annotation Limit

**Problem:**  
- Original 1.7MB CRD bundle exceeded ArgoCD's 262KB annotation limit
- Error: `metadata.annotations: Too long: must have at most 262144 bytes`

**Solution:**  
- Split bundle into 23 individual CRD files (eso-crd-00.yaml through eso-crd-22.yaml)
- Enabled ServerSideApply in ArgoCD Application:
  ```yaml
  syncOptions:
    - ServerSideApply=true
  ```
- Largest individual files: 607KB (eso-crd-02, eso-crd-05) - under limit with SSA

**Commit:** `3955861` - feat: split ESO v2.2.0 CRDs into 23 files for ServerSideApply

### Issue 2: API Version Mismatch

**Problem:**  
- ESO v0.11.0 controller expected `external-secrets.io/v1beta1` API
- CRDs only provided `external-secrets.io/v1` API
- Error: `no matches for kind "ExternalSecret" in version "external-secrets.io/v1beta1"`

**Solution:**  
- Upgraded Helm chart from v0.11.0 to v0.14.2
- Updated image tag from v0.11.0 to v2.2.0
- Ensured controller and CRDs use same API version (v1)

**Commit:** `9ef9ade` - fix: upgrade ESO Helm chart to v0.14.2 to match v2.2.0 CRDs

### Issue 3: Missing Required Field in ClusterSecretStore

**Problem:**  
- ESO v2.2.0 CRDs require `spec.provider.infisical.secretsScope` field
- Error: `spec.provider.infisical.secretsScope: Required value`

**Solution:**  
- Added secretsScope configuration:
  ```yaml
  spec:
    provider:
      infisical:
        secretsScope:
          projectSlug: "platform"
          environmentSlug: "prod"
  ```

**Commit:** `5994ab2` - fix: add required secretsScope to Infisical ClusterSecretStore

## Configuration

### ClusterSecretStore Specification

```yaml
apiVersion: external-secrets.io/v1
kind: ClusterSecretStore
metadata:
  name: infisical-backend
  annotations:
    argocd.argoproj.io/sync-wave: "5"
spec:
  provider:
    infisical:
      serverURL: "https://infisical.zero-ops-system.svc"
      secretsScope:
        projectSlug: "platform"           # Infisical project
        environmentSlug: "prod"            # Infisical environment
        expandSecretReferences: true       # Resolve ${SECRET_REF}
        recursive: false                   # Don't fetch nested folders
        secretsPath: "/"                   # Root path
      auth:
        universalAuth:
          credentialsRef:
            clientId:
              name: infisical-auth
              key: client-id
              namespace: external-secrets-system
            clientSecret:
              name: infisical-auth
              key: client-secret
              namespace: external-secrets-system
```

### ESO Helm Chart Configuration

```yaml
# manifests/argocd/apps/platform-external-secrets.yaml
source:
  chart: external-secrets
  repoURL: https://charts.external-secrets.io
  targetRevision: 0.14.2
  helm:
    values: |
      installCRDs: false  # Managed separately via sync-wave 1

      image:
        repository: ghcr.io/external-secrets/external-secrets
        tag: "v2.2.0"
        pullPolicy: IfNotPresent

      replicaCount: 1

      resources:
        requests:
          cpu: 10m
          memory: 64Mi
        limits:
          memory: 128Mi

      processClusterExternalSecret: true
      processClusterStore: true
      processSecretStore: true

      metrics:
        service:
          enabled: true
          port: 8080

      serviceMonitor:
        enabled: true
        interval: 30s
```

## Bootstrap Process

ESO requires a chicken-and-egg bootstrap because it needs credentials to connect to Infisical, but those credentials should ideally be managed by ESO itself.

### Break-Glass Bootstrap Secret

Created imperatively via `cmd/hub/configure_eso.go`:

```go
func InstallInfisicalAuth(ctx context.Context, clientID, clientSecret string) error {
    secret := &corev1.Secret{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "infisical-auth",
            Namespace: "external-secrets-system",
        },
        Type: corev1.SecretTypeOpaque,
        Data: map[string][]byte{
            "client-id":     []byte(clientID),
            "client-secret": []byte(clientSecret),
        },
    }
    // Create using client-go kubernetes clientset
}
```

**Usage:**
```bash
./hub configure-eso \
  --infisical-client-id="<client-id>" \
  --infisical-client-secret="<client-secret>" \
  --kubeconfig=~/.kube/config
```

After bootstrap, this secret enables ESO to connect to Infisical and sync all other secrets via GitOps.

## Sync Wave Ordering

Critical dependency chain enforced via ArgoCD sync-waves:

1. **Wave 1:** CRD Application (`platform-external-secrets-crds`)
   - Installs all 23 ESO CRDs
   - Uses ServerSideApply to handle large CRDs

2. **Wave 4:** ESO Controller (`platform-external-secrets`)
   - Deploys ESO operator pods
   - Waits for CRDs to be established

3. **Wave 5:** ClusterSecretStore (`platform-cluster-secret-store`)
   - Creates Infisical backend connection
   - Requires ESO controller to be running

4. **Wave 6+:** ExternalSecret Resources
   - Sync secrets from Infisical to Kubernetes
   - Requires ClusterSecretStore to be ready

## Validation

### Check ESO Controller Status

```bash
kubectl get pods -n external-secrets-system -l app.kubernetes.io/name=external-secrets
```

Expected output:
```
NAME                                         READY   STATUS    RESTARTS   AGE
platform-external-secrets-5f58f7c8cb-d68nk   1/1     Running   0          5m
```

### Check ClusterSecretStore Status

```bash
kubectl get clustersecretstores.external-secrets.io infisical-backend
```

Expected output:
```
NAME                AGE    STATUS   CAPABILITIES   READY
infisical-backend   10m                           
```

### Check CRD API Version

```bash
kubectl get crd clustersecretstores.external-secrets.io -o jsonpath='{.spec.versions[*].name}'
```

Expected output:
```
v1
```

### Verify Infisical Provider Support

```bash
kubectl get crd clustersecretstores.external-secrets.io \
  -o jsonpath='{.spec.versions[0].schema.openAPIV3Schema.properties.spec.properties.provider.properties}' \
  | jq 'keys' | grep infisical
```

Expected output:
```
"infisical",
```

## Troubleshooting

### ESO Controller CrashLoopBackOff

**Symptom:**
```
Error: no matches for kind "ExternalSecret" in version "external-secrets.io/v1beta1"
```

**Cause:** Controller version doesn't match CRD API version

**Fix:** Ensure Helm chart version matches CRD version:
- Chart v0.14.2 → Image v2.2.0 → CRDs v1 ✅
- Chart v0.11.0 → Image v0.11.0 → CRDs v1beta1 ❌

### ClusterSecretStore Validation Error

**Symptom:**
```
spec.provider.infisical.secretsScope: Required value
```

**Cause:** ESO v2.2.0 requires secretsScope field

**Fix:** Add to ClusterSecretStore spec:
```yaml
spec:
  provider:
    infisical:
      secretsScope:
        projectSlug: "platform"
        environmentSlug: "prod"
```

### ArgoCD Annotation Size Error

**Symptom:**
```
metadata.annotations: Too long: must have at most 262144 bytes
```

**Cause:** Large CRD files exceed annotation limit

**Fix:** Enable ServerSideApply in ArgoCD Application:
```yaml
syncOptions:
  - ServerSideApply=true
```

### RBAC Errors for ClusterPushSecret

**Symptom:**
```
clusterpushsecrets.external-secrets.io is forbidden
```

**Cause:** ServiceAccount lacks permissions for PushSecret CRDs

**Impact:** Non-critical - PushSecret feature not used in platform

**Fix:** Ignore or add RBAC permissions if PushSecret needed

## References

- [External Secrets Operator Documentation](https://external-secrets.io/)
- [Infisical Documentation](https://infisical.com/docs)
- [ESO Infisical Provider](https://external-secrets.io/latest/provider/infisical/)
- [ArgoCD ServerSideApply](https://argo-cd.readthedocs.io/en/stable/user-guide/sync-options/#server-side-apply)
- Related Docs:
  - `docs/infisical/database-connection-pooling.md` - PgBouncer configuration
  - `docs/infisical/migration-failure-rca.md` - Infisical migration issues
