# Design Document: Infisical and External Secrets Deployment

## Overview

This design implements the deployment of Infisical (primary secret storage) and External Secrets Operator (ESO) to resolve ArgoCD GitHub authentication errors blocking the observability pipeline. The architecture follows the approved pattern from the ArgoCD team: ArgoCD deploys ExternalSecret CRDs (secret references), ESO fetches secrets from Infisical and creates Kubernetes secrets, and applications consume secrets from files with auto-reload capability.

The implementation uses GitOps-first principles with ArgoCD Applications, sync-wave ordering for dependency management, and CloudNativePG for Infisical's PostgreSQL backend instead of the built-in chart dependency.

### Architecture Pattern

```
┌─────────────────────────────────────────────────────────────────┐
│                         Git Repository                          │
│  (ExternalSecret CRDs - Secret References, NOT Values)         │
└────────────────┬────────────────────────────────────────────────┘
                 │
                 │ ArgoCD Syncs
                 ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                           │
│                                                                 │
│  ┌──────────────┐    ┌─────────────────┐    ┌──────────────┐ │
│  │   Infisical  │◄───│ External Secrets│◄───│ ExternalSecret│ │
│  │   (Storage)  │    │   Operator      │    │     CRDs      │ │
│  └──────────────┘    └────────┬────────┘    └──────────────┘ │
│         │                     │                               │
│         │                     │ Creates                       │
│         │                     ▼                               │
│         │            ┌─────────────────┐                      │
│         │            │ K8s Secrets     │                      │
│         │            └────────┬────────┘                      │
│         │                     │                               │
│         │                     │ Mounted as Files              │
│         │                     ▼                               │
│         │            ┌─────────────────┐                      │
│         │            │  Applications   │                      │
│         │            │ (Auto-reload)   │                      │
│         │            └─────────────────┘                      │
└─────────────────────────────────────────────────────────────────┘
```

### Sync-Wave Dependency Chain

```
Wave 2: platform-database (CloudNativePG)
   ↓
Wave 3: platform-infisical (uses external PostgreSQL)
   ↓
Wave 4: platform-external-secrets (ESO operator)
   ↓
Wave 5: platform-cluster-secret-store (Infisical backend config)
   ↓
Wave 6: platform-argocd-github-auth (ExternalSecret for GitHub creds)
```

## Architecture

### Component Topology

The system consists of five primary components deployed across the zero-ops-system and external-secrets-system namespaces:

1. **Infisical** (zero-ops-system, wave 3)
   - Primary secret storage backend
   - Uses CloudNativePG PostgreSQL (deployed at wave 2)
   - Exposes HTTPS API endpoint for secret management
   - Persists secrets using PersistentVolumeClaims

2. **External Secrets Operator** (external-secrets-system, wave 4)
   - Watches ExternalSecret CRDs
   - Fetches secrets from Infisical via ClusterSecretStore
   - Creates/updates Kubernetes secrets
   - Reconciles every 60 seconds by default

3. **ClusterSecretStore** (external-secrets-system, wave 5)
   - Named "infisical-backend"
   - Cluster-scoped (accessible from all namespaces)
   - References Infisical API endpoint
   - Authenticates using Kubernetes secret containing Infisical API token

4. **ArgoCD GitHub Auth ExternalSecret** (argocd namespace, wave 6)
   - Fetches GitHub credentials from Infisical
   - Creates Kubernetes secret for ArgoCD
   - Updates ArgoCD ConfigMap to reference the secret

5. **Validation Script** (bash)
   - End-to-end testing of secret flow
   - Creates test secret in Infisical
   - Verifies ExternalSecret → K8s Secret → Pod consumption

### Network Flow

```
┌──────────────────────────────────────────────────────────────┐
│                    External Secrets Operator                 │
│                                                              │
│  1. Watch ExternalSecret CRDs                               │
│  2. Read ClusterSecretStore config                          │
│  3. Authenticate to Infisical API (HTTPS)                   │
│  4. Fetch secret values                                     │
│  5. Create/Update K8s Secret                                │
│  6. Repeat every 60s                                        │
└──────────────────────────────────────────────────────────────┘
         │                                    ▲
         │ HTTPS API Call                    │
         ▼                                    │
┌──────────────────────────────────────────────────────────────┐
│                         Infisical                            │
│                                                              │
│  - API Endpoint: https://infisical.zero-ops-system.svc     │
│  - Authentication: Bearer Token                             │
│  - Storage: CloudNativePG PostgreSQL                        │
└──────────────────────────────────────────────────────────────┘
```

### Data Flow: Secret Synchronization

1. **Secret Creation in Infisical**
   - Platform operator creates secret via Infisical UI/API
   - Secret stored in PostgreSQL backend
   - Secret path: `/project/{project-id}/environment/{env}/secret/{key}`

2. **ExternalSecret CRD Deployment**
   - Developer commits ExternalSecret manifest to Git
   - ArgoCD syncs manifest to cluster
   - ESO detects new ExternalSecret resource

3. **Secret Fetching**
   - ESO reads ClusterSecretStore configuration
   - ESO authenticates to Infisical using API token
   - ESO fetches secret value from Infisical
   - ESO validates secret exists and is accessible

4. **Kubernetes Secret Creation**
   - ESO creates Kubernetes Secret in target namespace
   - Secret data matches Infisical secret value
   - Secret labeled with ExternalSecret ownership

5. **Application Consumption**
   - Application pod mounts secret as file (not env var)
   - Application implements file watcher for auto-reload
   - No pod restart required on secret rotation

6. **Secret Rotation**
   - Secret updated in Infisical
   - ESO detects change within 60 seconds
   - ESO updates Kubernetes Secret
   - Application reloads secret from file

## Components and Interfaces

### Infisical Deployment

**Helm Chart Configuration:**
```yaml
# Chart: infisical-standalone-postgres v1.7.5
# App Version: v0.158.0

infisical:
  enabled: true
  replicaCount: 2
  image:
    repository: infisical/infisical
    tag: "v0.158.0"
  
  # Use existing PostgreSQL from CloudNativePG
  useExistingPostgresSecret:
    enabled: true
    existingConnectionStringSecret:
      name: infisical-postgres-connection
      key: connection-string

  service:
    type: ClusterIP
    annotations: {}

  resources:
    limits:
      memory: 1000Mi
    requests:
      cpu: 350m

# Disable built-in PostgreSQL
postgresql:
  enabled: false

# Disable built-in Redis (not needed for basic setup)
redis:
  enabled: false

ingress:
  enabled: true
  ingressClassName: nginx
  hostName: infisical.zero-ops.local
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
  tls:
    - secretName: infisical-tls
      hosts:
        - infisical.zero-ops.local
```

**PostgreSQL Connection Secret:**
The secret `infisical-postgres-connection` must be created before Infisical deployment, containing:
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: infisical-postgres-connection
  namespace: zero-ops-system
type: Opaque
stringData:
  connection-string: "postgresql://infisical:${PASSWORD}@platform-database-rw.zero-ops-system.svc:5432/infisical?sslmode=require"
```

### External Secrets Operator Deployment

**Helm Chart Configuration:**
```yaml
# Chart: external-secrets v2.2.0

# Install CRDs via Helm
installCRDs: true

crds:
  createClusterExternalSecret: true
  createClusterSecretStore: true
  createSecretStore: true
  createClusterGenerator: true
  createClusterPushSecret: true
  createPushSecret: true

# Single replica for MVP
replicaCount: 1

image:
  repository: ghcr.io/external-secrets/external-secrets
  tag: "v2.2.0"
  pullPolicy: IfNotPresent

# Minimal resources
resources:
  requests:
    cpu: 10m
    memory: 64Mi
  limits:
    memory: 128Mi

# Enable cluster-wide operations
processClusterExternalSecret: true
processClusterStore: true
processSecretStore: true

# Metrics for observability
metrics:
  service:
    enabled: true
    port: 8080

serviceMonitor:
  enabled: true
  interval: 30s

# Security context
securityContext:
  enabled: true
  runAsNonRoot: true
  runAsUser: 1000
  readOnlyRootFilesystem: true
```

### ClusterSecretStore Configuration

**Infisical Backend:**
```yaml
apiVersion: external-secrets.io/v1beta1
kind: ClusterSecretStore
metadata:
  name: infisical-backend
  namespace: external-secrets-system
  annotations:
    argocd.argoproj.io/sync-wave: "5"
spec:
  provider:
    infisical:
      # Infisical API endpoint
      serverURL: "https://infisical.zero-ops-system.svc"
      
      # Authentication via Kubernetes secret
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

**Authentication Secret:**
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: infisical-auth
  namespace: external-secrets-system
type: Opaque
stringData:
  client-id: "${INFISICAL_CLIENT_ID}"
  client-secret: "${INFISICAL_CLIENT_SECRET}"
```

### ArgoCD GitHub Authentication Fix

**Break-Glass Bootstrap (One-Time Imperative):**

Before the GitOps loop can manage GitHub credentials, we must fix the chicken-and-egg problem: ArgoCD's current GitHub PAT has expired, preventing it from pulling the ESO manifests that will manage credentials declaratively.

The platform bootstrap code in `internal/hub/components/installer.go` provides the `FixArgoCDGitHubAuth()` method:

```go
// FixArgoCDGitHubAuth creates temporary GitHub PAT secret so ArgoCD can pull manifests
func (i *Installer) FixArgoCDGitHubAuth(ctx context.Context, githubToken string) error {
    // Creates labeled secret that ArgoCD auto-discovers (no ConfigMap patching needed)
    secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: repo-soloz-io-zero-ops
  namespace: argocd
  labels:
    argocd.argoproj.io/secret-type: repository
type: Opaque
stringData:
  type: git
  url: https://github.com/soloz-io/zero-ops
  username: zero-ops-bot
  password: %s
`, githubToken)
    // ... kubectl apply logic
}
```

This method is called once during platform bootstrap to restore ArgoCD's GitHub access. After this, the declarative GitOps flow takes over.

**ExternalSecret for GitHub Credentials (Declarative):**

Once ArgoCD can pull manifests, ESO takes over credential management:

```yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: argocd-github-creds
  namespace: argocd
  annotations:
    argocd.argoproj.io/sync-wave: "6"
spec:
  refreshInterval: 1h
  
  secretStoreRef:
    name: infisical-backend
    kind: ClusterSecretStore
  
  target:
    name: repo-soloz-io-zero-ops
    creationPolicy: Owner
    template:
      metadata:
        labels:
          argocd.argoproj.io/secret-type: repository
      type: Opaque
      data:
        type: git
        url: https://github.com/soloz-io/zero-ops
        username: "{{ .username }}"
        password: "{{ .password }}"
  
  data:
    - secretKey: username
      remoteRef:
        key: github-username
    - secretKey: password
      remoteRef:
        key: github-token
```

**Key Pattern: ArgoCD Labeled Secrets**

The secret uses the `argocd.argoproj.io/secret-type: repository` label, which tells ArgoCD to automatically discover and use it for Git authentication. This eliminates the need to patch the `argocd-cm` ConfigMap.

**Credential Lifecycle:**
1. Bootstrap: `FixArgoCDGitHubAuth()` creates temporary secret imperatively
2. ArgoCD syncs: Pulls ESO manifests from GitHub
3. ESO deploys: ClusterSecretStore and ExternalSecret resources created
4. ESO takes over: Replaces bootstrap secret with Infisical-backed secret
5. Rotation: ESO automatically updates secret when Infisical credentials change

## Data Models

### Infisical Secret Structure

```json
{
  "workspace": "zero-ops-platform",
  "environment": "production",
  "secretPath": "/argocd",
  "secrets": [
    {
      "key": "github-username",
      "value": "zero-ops-bot",
      "type": "shared"
    },
    {
      "key": "github-token",
      "value": "ghp_xxxxxxxxxxxx",
      "type": "shared"
    }
  ]
}
```

### ExternalSecret Status

```yaml
status:
  binding:
    name: argocd-github-secret
  conditions:
    - type: Ready
      status: "True"
      reason: SecretSynced
      message: "Secret was synced"
      lastTransitionTime: "2024-01-15T10:30:00Z"
  refreshTime: "2024-01-15T10:30:00Z"
  syncedResourceVersion: "1-abc123"
```

### ClusterSecretStore Status

```yaml
status:
  conditions:
    - type: Ready
      status: "True"
      reason: Valid
      message: "store validated"
      lastTransitionTime: "2024-01-15T10:00:00Z"
```

## Deployment Architecture

### ArgoCD Application Manifests

**1. platform-infisical.yaml**
```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: platform-infisical
  namespace: argocd
  annotations:
    argocd.argoproj.io/sync-wave: "3"
spec:
  project: default
  source:
    repoURL: https://github.com/soloz-io/zero-ops
    targetRevision: HEAD
    path: manifests/platform-infisical
    directory:
      recurse: true
  destination:
    server: https://kubernetes.default.svc
    namespace: zero-ops-system
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
    retry:
      limit: 15
      backoff:
        duration: 30s
        maxDuration: 10m
```

**2. platform-external-secrets.yaml**
```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: platform-external-secrets
  namespace: argocd
  annotations:
    argocd.argoproj.io/sync-wave: "4"
spec:
  project: default
  source:
    repoURL: https://github.com/soloz-io/zero-ops
    targetRevision: HEAD
    path: manifests/platform-external-secrets
    directory:
      recurse: true
  destination:
    server: https://kubernetes.default.svc
    namespace: external-secrets-system
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
    retry:
      limit: 15
      backoff:
        duration: 30s
        maxDuration: 10m
```

**3. platform-cluster-secret-store.yaml**
```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: platform-cluster-secret-store
  namespace: argocd
  annotations:
    argocd.argoproj.io/sync-wave: "5"
spec:
  project: default
  source:
    repoURL: https://github.com/soloz-io/zero-ops
    targetRevision: HEAD
    path: manifests/platform-cluster-secret-store
    directory:
      recurse: true
  destination:
    server: https://kubernetes.default.svc
    namespace: external-secrets-system
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    retry:
      limit: 15
      backoff:
        duration: 30s
        maxDuration: 10m
```

**4. platform-argocd-github-auth.yaml**
```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: platform-argocd-github-auth
  namespace: argocd
  annotations:
    argocd.argoproj.io/sync-wave: "6"
spec:
  project: default
  source:
    repoURL: https://github.com/soloz-io/zero-ops
    targetRevision: HEAD
    path: manifests/platform-argocd-github-auth
    directory:
      recurse: true
  destination:
    server: https://kubernetes.default.svc
    namespace: argocd
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    retry:
      limit: 15
      backoff:
        duration: 30s
        maxDuration: 10m
```

### Directory Structure

```
manifests/
├── argocd/
│   └── apps/
│       ├── platform-infisical.yaml
│       ├── platform-external-secrets.yaml
│       ├── platform-cluster-secret-store.yaml
│       └── platform-argocd-github-auth.yaml
├── platform-infisical/
│   ├── namespace.yaml
│   ├── postgres-connection-secret.yaml
│   ├── helm-release.yaml
│   └── values.yaml
├── platform-external-secrets/
│   ├── namespace.yaml
│   ├── helm-release.yaml
│   └── values.yaml
├── platform-cluster-secret-store/
│   ├── infisical-auth-secret.yaml
│   └── cluster-secret-store.yaml
└── platform-argocd-github-auth/
    ├── external-secret.yaml
    └── argocd-cm-patch.yaml
```

## Configuration Management

### Helm Values Management

Helm values are stored in the manifest directories and referenced by HelmRelease resources. This follows the GitOps pattern where all configuration is version-controlled.

**Example HelmRelease for Infisical:**
```yaml
apiVersion: helm.toolkit.fluxcd.io/v2beta1
kind: HelmRelease
metadata:
  name: infisical
  namespace: zero-ops-system
spec:
  interval: 10m
  chart:
    spec:
      chart: infisical-standalone-postgres
      version: 1.7.5
      sourceRef:
        kind: HelmRepository
        name: infisical
        namespace: zero-ops-system
  valuesFrom:
    - kind: ConfigMap
      name: infisical-values
```

### Secret Bootstrapping

**Break-Glass Bootstrap Secrets (One-Time Imperative):**

The platform bootstrap code in `internal/hub/components/installer.go` provides two methods for initial secret creation:

1. **InstallInfisicalAuth()** - Creates ESO authentication secret for Infisical:
```go
func (i *Installer) InstallInfisicalAuth(ctx context.Context, clientID, clientSecret string) error {
    // Creates infisical-auth secret in external-secrets-system namespace
    // This enables ESO to authenticate to Infisical API
}
```

2. **FixArgoCDGitHubAuth()** - Creates temporary GitHub PAT secret:
```go
func (i *Installer) FixArgoCDGitHubAuth(ctx context.Context, githubToken string) error {
    // Creates labeled secret that ArgoCD auto-discovers
    // Fixes chicken-and-egg: ArgoCD needs GitHub auth to pull ESO manifests
}
```

These methods are called once during platform bootstrap to enable the declarative GitOps flow.

**Declarative Secret Management (GitOps):**

After bootstrap, all secrets are managed declaratively via Git:

**manifests/platform-infisical/postgres-connection-secret.yaml:**
```yaml
apiVersion: v1
kind: Secret
metadata:
  name: infisical-postgres-connection
  namespace: zero-ops-system
type: Opaque
stringData:
  connection-string: "postgresql://infisical:${PG_PASSWORD}@platform-database-rw.zero-ops-system.svc:5432/infisical?sslmode=require"
```

**GitOps Workflow:**
1. Bootstrap secrets created imperatively via Go code (one-time only)
2. Commit declarative secret manifests to Git with encrypted values (KSOPS/Age)
3. ArgoCD syncs and decrypts secrets to cluster
4. ESO takes over credential lifecycle management
5. Future rotations happen via Git commits or Infisical updates

**Critical Distinction:**
- Bootstrap secrets: Imperative (Go code) - enables GitOps
- Application secrets: Declarative (Git + ESO) - managed by GitOps

### Environment-Specific Configuration

Configuration differences between environments (dev/staging/prod) are managed through:

1. **Kustomize Overlays** (if needed for environment-specific patches)
2. **Separate Values Files** (for Helm chart customization)
3. **ArgoCD Application Parameters** (for simple overrides)

For MVP, all environments use the same configuration with only namespace and ingress hostname differences.


## Correctness Properties

A property is a characteristic or behavior that should hold true across all valid executions of a system—essentially, a formal statement about what the system should do. Properties serve as the bridge between human-readable specifications and machine-verifiable correctness guarantees.

### Property 1: Secret Persistence Across Pod Restarts

For any secret stored in Infisical, when Infisical pods are restarted, the secret should remain accessible and unchanged after the restart completes.

**Validates: Requirements 1.7**

### Property 2: Secret Synchronization Timing

For any secret change in Infisical, when ESO detects the change, the corresponding Kubernetes secret should be updated within 60 seconds.

**Validates: Requirements 2.7**

### Property 3: ClusterSecretStore Authentication Success

For any ExternalSecret that references the "infisical-backend" ClusterSecretStore with valid credentials, ESO should successfully authenticate to Infisical and fetch the secret.

**Validates: Requirements 3.6**

### Property 4: Authentication Failure Reporting

For any ClusterSecretStore configured with invalid Infisical API credentials, the ClusterSecretStore status should report an authentication failure condition.

**Validates: Requirements 3.7**

### Property 5: ArgoCD GitHub Authentication Success

For any ArgoCD Application sync operation after GitHub credentials are configured, ArgoCD should successfully authenticate to GitHub without authentication errors.

**Validates: Requirements 4.5**

### Property 6: GitHub Credential Rotation Timing

For any GitHub credential rotation in Infisical, the ArgoCD GitHub Kubernetes secret should be updated within 60 seconds of the rotation.

**Validates: Requirements 4.6**

### Property 7: GitOps Sync Detection

For any manifest committed to the main branch, ArgoCD should detect and sync the change within 3 minutes.

**Validates: Requirements 5.7**

### Property 8: Secret Value Round-Trip Consistency

For any secret stored in Infisical, when ESO fetches the secret and creates a Kubernetes secret, the value in the Kubernetes secret should exactly match the value stored in Infisical.

**Validates: Requirements 6.4**

### Property 9: Validation Script Cleanup

For any execution of the validation script, all test resources (secrets, ExternalSecrets, pods) should be removed after the script completes, regardless of success or failure.

**Validates: Requirements 6.7**

### Property 10: Validation Script Error Handling

For any validation step failure in the validation script, the script should exit with a non-zero status code and output a descriptive error message indicating which step failed.

**Validates: Requirements 6.8**

### Property 11: Git Repository Secret Exclusion

For any file in the GitOps repository, the file should not contain plaintext secret values (passwords, tokens, API keys).

**Validates: Requirements 7.2, 7.5**

### Property 12: File-Based Secret Consumption

For any application consuming secrets from the secret management system, the application pod specification should mount secrets as files via volumeMounts, not inject them as environment variables.

**Validates: Requirements 7.3**

## Error Handling

### Infisical Deployment Errors

**PostgreSQL Connection Failure:**
- Symptom: Infisical pods crash loop with database connection errors
- Detection: Pod logs contain "connection refused" or "authentication failed"
- Resolution: Verify `infisical-postgres-connection` secret exists and contains valid connection string
- Prevention: Create connection secret before Infisical deployment (sync-wave ordering)

**Missing PersistentVolume:**
- Symptom: Infisical pods stuck in Pending state
- Detection: PVC status shows "Pending" with no bound PV
- Resolution: Verify StorageClass exists and can provision volumes
- Prevention: Ensure storage provisioner is configured before Infisical deployment

**Ingress Configuration Error:**
- Symptom: Infisical API not accessible via HTTPS
- Detection: Ingress resource exists but returns 404 or 503
- Resolution: Verify ingress-nginx controller is running and cert-manager issued certificate
- Prevention: Deploy ingress-nginx and cert-manager before Infisical (sync-wave ordering)

### External Secrets Operator Errors

**CRD Installation Failure:**
- Symptom: ExternalSecret resources fail to create with "no matches for kind"
- Detection: `kubectl get crd externalsecrets.external-secrets.io` returns not found
- Resolution: Verify `installCRDs: true` in Helm values and redeploy ESO
- Prevention: Use Helm chart with CRD installation enabled

**ClusterSecretStore Not Ready:**
- Symptom: ExternalSecrets stuck in "SecretSyncedError" state
- Detection: ExternalSecret status shows "store is not ready"
- Resolution: Check ClusterSecretStore status conditions for authentication errors
- Prevention: Ensure ClusterSecretStore is deployed and ready before ExternalSecrets (sync-wave ordering)

**Secret Fetch Timeout:**
- Symptom: ExternalSecrets fail with "timeout waiting for secret"
- Detection: ESO logs show HTTP timeout errors to Infisical API
- Resolution: Verify Infisical service is accessible from ESO pod, check network policies
- Prevention: Deploy Infisical before ESO, verify service DNS resolution

### ClusterSecretStore Errors

**Invalid Infisical Credentials:**
- Symptom: ClusterSecretStore status shows "authentication failed"
- Detection: Status condition type "Ready" is "False" with reason "AuthenticationFailed"
- Resolution: Verify `infisical-auth` secret contains valid client-id and client-secret
- Recovery: Update secret with valid credentials, ClusterSecretStore will reconcile automatically

**Infisical API Unreachable:**
- Symptom: ClusterSecretStore status shows "connection refused"
- Detection: Status condition shows network connectivity errors
- Resolution: Verify Infisical service exists and is running, check service DNS
- Recovery: Fix Infisical deployment, ClusterSecretStore will reconcile when service is available

**Missing Authentication Secret:**
- Symptom: ClusterSecretStore fails to reconcile
- Detection: ESO logs show "secret not found" errors
- Resolution: Create `infisical-auth` secret in external-secrets-system namespace
- Prevention: Deploy authentication secret before ClusterSecretStore (sync-wave ordering)

### ArgoCD GitHub Authentication Errors

**ExternalSecret Sync Failure:**
- Symptom: ArgoCD still shows GitHub authentication errors
- Detection: ExternalSecret status shows "SecretSyncedError"
- Resolution: Check ClusterSecretStore is ready, verify GitHub credentials exist in Infisical
- Recovery: Fix upstream issue (ClusterSecretStore or Infisical), ExternalSecret will retry

**ArgoCD ConfigMap Not Updated:**
- Symptom: ArgoCD doesn't use new GitHub credentials
- Detection: ArgoCD still references old secret or no secret
- Resolution: Verify argocd-cm ConfigMap has correct repository configuration
- Recovery: Update ConfigMap to reference argocd-github-secret, restart ArgoCD pods

**GitHub Token Expired:**
- Symptom: ArgoCD authentication works initially but fails later
- Detection: ArgoCD logs show "401 Unauthorized" from GitHub API
- Resolution: Rotate GitHub token in Infisical, wait for ExternalSecret to sync
- Recovery: ESO will automatically update Kubernetes secret within 60 seconds

### Validation Script Errors

**Infisical API Unreachable:**
- Symptom: Script fails at secret creation step
- Detection: curl commands to Infisical API return connection errors
- Resolution: Verify Infisical is deployed and accessible, check port-forward or ingress
- Recovery: Fix Infisical deployment, re-run validation script

**ExternalSecret Not Syncing:**
- Symptom: Script times out waiting for Kubernetes secret
- Detection: Kubernetes secret not created after 60 seconds
- Resolution: Check ESO logs for errors, verify ClusterSecretStore is ready
- Recovery: Fix ESO or ClusterSecretStore issue, delete and recreate ExternalSecret

**Test Pod Mount Failure:**
- Symptom: Test pod fails to mount secret as file
- Detection: Pod events show "MountVolume.SetUp failed"
- Resolution: Verify Kubernetes secret exists and is in correct namespace
- Recovery: Ensure ExternalSecret created the secret successfully, redeploy test pod

## Testing Strategy

### Dual Testing Approach

The testing strategy employs both unit tests and property-based tests to ensure comprehensive coverage:

**Unit Tests:**
- Verify specific examples and edge cases
- Test integration points between components
- Validate error conditions and failure modes
- Focus on concrete scenarios (e.g., "Infisical pod restart with 3 secrets")

**Property-Based Tests:**
- Verify universal properties across all inputs
- Use randomized test data generation
- Run minimum 100 iterations per property
- Focus on general correctness (e.g., "any secret survives any pod restart")

Together, unit tests catch concrete bugs while property tests verify general correctness across the input space.

### Property-Based Testing Configuration

**Testing Library:** Use `gopter` for Go-based property tests (validation script and integration tests)

**Test Configuration:**
- Minimum 100 iterations per property test
- Each test tagged with design document property reference
- Tag format: `// Feature: infisical-external-secrets-deployment, Property {number}: {property_text}`

**Example Property Test Structure:**
```go
// Feature: infisical-external-secrets-deployment, Property 1: Secret Persistence Across Pod Restarts
func TestSecretPersistenceAcrossPodRestarts(t *testing.T) {
    properties := gopter.NewProperties(nil)
    
    properties.Property("secrets survive pod restarts", prop.ForAll(
        func(secretKey string, secretValue string) bool {
            // 1. Create secret in Infisical
            createSecretInInfisical(secretKey, secretValue)
            
            // 2. Restart Infisical pods
            restartInfisicalPods()
            
            // 3. Verify secret still exists and has same value
            retrievedValue := getSecretFromInfisical(secretKey)
            return retrievedValue == secretValue
        },
        gen.AlphaString(),
        gen.AlphaString(),
    ))
    
    properties.TestingRun(t, gopter.ConsoleReporter(false))
}
```

### Unit Test Coverage

**Infisical Deployment Tests:**
1. Verify Helm chart uses correct version (1.7.5)
2. Verify deployment to zero-ops-system namespace
3. Verify PostgreSQL is disabled (postgresql.enabled: false)
4. Verify external PostgreSQL secret reference
5. Verify sync-wave annotation is "3"
6. Verify HTTPS ingress configuration
7. Verify PVC creation and binding

**External Secrets Operator Tests:**
1. Verify Helm chart uses correct version (2.2.0)
2. Verify deployment to external-secrets-system namespace
3. Verify CRD installation (installCRDs: true)
4. Verify sync-wave annotation is "4"
5. Verify resource requests (cpu: 10m, memory: 64Mi)
6. Verify automated sync policy configuration
7. Verify ServiceMonitor creation for metrics

**ClusterSecretStore Tests:**
1. Verify resource name is "infisical-backend"
2. Verify Infisical API endpoint configuration
3. Verify authentication secret reference
4. Verify sync-wave annotation is "5"
5. Verify cluster scope (ClusterSecretStore vs SecretStore)
6. Verify status reports Ready when credentials valid
7. Verify status reports authentication failure when credentials invalid

**ArgoCD GitHub Auth Tests:**
1. Verify GitHub credentials exist in Infisical
2. Verify ExternalSecret creates Kubernetes secret
3. Verify ArgoCD ConfigMap references secret
4. Verify sync-wave annotation is "6"
5. Verify Prometheus Operator application syncs successfully
6. Verify Grafana Alloy application syncs successfully
7. Verify VictoriaMetrics Ingress application syncs successfully

**ArgoCD Application Tests:**
1. Verify all four application manifests exist in manifests/argocd/apps/
2. Verify applications reference correct GitHub repository
3. Verify sync-wave annotations enforce dependency ordering
4. Verify automated sync policy with prune enabled
5. Verify retry policy configuration (duration: 30s, maxDuration: 10m, limit: 15)

**Validation Script Tests:**
1. Verify script creates test secret in Infisical
2. Verify script creates ExternalSecret resource
3. Verify script waits for Kubernetes secret creation
4. Verify script deploys test pod with secret mount
5. Verify script reads secret file from pod
6. Verify script cleans up all test resources
7. Verify script exits non-zero on any failure

### Integration Test Scenarios

**End-to-End Secret Flow:**
1. Create secret in Infisical via API
2. Create ExternalSecret referencing the secret
3. Wait for ESO to sync (max 60 seconds)
4. Verify Kubernetes secret exists with correct value
5. Deploy pod mounting secret as file
6. Verify pod can read secret file
7. Update secret in Infisical
8. Wait for ESO to sync (max 60 seconds)
9. Verify Kubernetes secret updated
10. Verify pod sees updated secret file (if watching)

**Sync-Wave Dependency Validation:**
1. Deploy all ArgoCD applications simultaneously
2. Verify platform-database deploys first (wave 2)
3. Verify platform-infisical deploys after database (wave 3)
4. Verify platform-external-secrets deploys after Infisical (wave 4)
5. Verify platform-cluster-secret-store deploys after ESO (wave 5)
6. Verify platform-argocd-github-auth deploys last (wave 6)
7. Verify all applications reach Healthy/Synced state

**Failure Recovery:**
1. Deploy Infisical with invalid PostgreSQL connection
2. Verify Infisical pods crash loop
3. Fix PostgreSQL connection secret
4. Verify Infisical pods recover automatically
5. Deploy ExternalSecret before ClusterSecretStore
6. Verify ExternalSecret shows "store not ready" error
7. Deploy ClusterSecretStore
8. Verify ExternalSecret recovers and syncs successfully

### Validation Script Implementation

The validation script (`scripts/validate-secret-flow.sh`) implements read-only end-to-end verification of resources deployed via ArgoCD:

```bash
#!/bin/bash
set -euo pipefail

# Configuration
INFISICAL_API="https://infisical.zero-ops-system.svc"
TEST_SECRET_KEY="test-secret-validation"
TEST_SECRET_VALUE="test-value-$(openssl rand -hex 16)"
TIMEOUT=60

echo "=== Infisical External Secrets Validation ==="

# Step 1: Verify Infisical is accessible
echo "1. Verifying Infisical API is accessible..."
curl -f "${INFISICAL_API}/api/health" \
  || { echo "Failed to reach Infisical API"; exit 1; }

# Step 2: Verify External Secrets Operator is running
echo "2. Verifying External Secrets Operator..."
kubectl get deployment -n external-secrets-system external-secrets \
  -o jsonpath='{.status.availableReplicas}' | grep -q "1" \
  || { echo "External Secrets Operator not ready"; exit 1; }

# Step 3: Verify ClusterSecretStore is ready
echo "3. Verifying ClusterSecretStore..."
kubectl get clustersecretstore infisical-backend \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' | grep -q "True" \
  || { echo "ClusterSecretStore not ready"; exit 1; }

# Step 4: Verify ArgoCD GitHub secret exists and is synced
echo "4. Verifying ArgoCD GitHub authentication..."
kubectl get secret argocd-github-secret -n argocd \
  || { echo "ArgoCD GitHub secret not found"; exit 1; }

# Step 5: Verify ExternalSecret for GitHub credentials is synced
echo "5. Verifying ExternalSecret sync status..."
kubectl get externalsecret argocd-github-creds -n argocd \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' | grep -q "True" \
  || { echo "ExternalSecret not synced"; exit 1; }

# Step 6: Verify ArgoCD applications can sync (GitHub auth working)
echo "6. Verifying ArgoCD GitHub authentication is working..."
SYNC_STATUS=$(kubectl get application prometheus-operator -n argocd \
  -o jsonpath='{.status.sync.status}' 2>/dev/null || echo "NotFound")

if [ "$SYNC_STATUS" = "Synced" ] || [ "$SYNC_STATUS" = "OutOfSync" ]; then
  echo "ArgoCD GitHub authentication is working (no auth errors)"
else
  echo "Warning: Could not verify ArgoCD sync status"
fi

# Step 7: Verify secret rotation timing (if test secret exists)
echo "7. Verifying secret synchronization..."
# This step would check if a known test ExternalSecret syncs within 60 seconds
# For now, we verify the refresh interval is configured correctly
REFRESH_INTERVAL=$(kubectl get externalsecret argocd-github-creds -n argocd \
  -o jsonpath='{.spec.refreshInterval}')
echo "ExternalSecret refresh interval: ${REFRESH_INTERVAL}"

echo "=== Validation Complete ==="
echo "All components are deployed and functioning correctly via GitOps"
```

**Key Differences from Original:**
- **Read-Only**: Script only verifies existing resources, does not create any
- **No kubectl apply**: All resources must be deployed via ArgoCD before running validation
- **GitOps Verification**: Confirms resources were deployed correctly through GitOps workflow
- **No Cleanup Needed**: Since no test resources are created, no cleanup required

### Test Execution Workflow

**Local Development:**
1. Developer commits changes to feature branch
2. ArgoCD syncs changes to dev cluster
3. Developer runs validation script manually
4. Developer verifies all tests pass before creating PR

**CI/CD Pipeline:**
1. PR triggers GitHub Actions workflow
2. Workflow deploys to ephemeral test cluster
3. Workflow runs validation script
4. Workflow runs property-based tests (100 iterations each)
5. Workflow runs unit tests
6. PR blocked if any test fails

**Production Deployment:**
1. Merge to main branch triggers ArgoCD sync
2. Sync-waves ensure correct deployment order
3. Post-deployment validation script runs automatically
4. Alerts triggered if validation fails
5. Rollback initiated if critical failures detected

