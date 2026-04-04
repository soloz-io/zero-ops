# Hub Operator Troubleshooting

## Issue 1: CNPG Scheme Registration Error

**Date:** 2026-04-03  
**Status:** Fixed

### Problem
Operator pods were running but showing error:
```
no kind is registered for the type v1.Cluster in scheme
```

### Root Cause
CNPG API types were not registered in the operator's scheme, preventing the operator from watching CNPG Cluster resources.

### Fix
Added CNPG scheme registration in `operators/hub-operator/cmd/main.go`:

```go
import (
    cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
)

func init() {
    utilruntime.Must(cnpgv1.AddToScheme(scheme))
}
```

**Commit:** [hash from git log]

---

## Issue 2: ResourceVersion Error on Secret Creation

**Date:** 2026-04-03  
**Status:** Fixed

### Problem
Operator was failing to create secrets with error:
```
resourceVersion should not be set on objects to be created
```

### Root Cause
The `generateOrReuse*` functions in `operators/hub-operator/internal/secrets/generator.go` were returning existing secret objects (which include `resourceVersion` metadata) when secrets already existed. The controller then tried to `Create()` these objects, which is invalid because `resourceVersion` should only be set on existing resources, not new ones.

### Fix
Modified all `generateOrReuse*` functions to return `nil` when a secret already exists, instead of returning the existing secret object:

```go
func generateOrReuseInfisicalSecrets(..., existing *corev1.Secret) (*corev1.Secret, error) {
    if existing != nil {
        // Return nil to indicate secret already exists (don't try to create)
        return nil, nil
    }
    return GenerateInfisicalSecrets(...)
}
```

Applied to:
- `generateOrReuseInfisicalSecrets`
- `generateOrReusePlatformDBApp`
- `generateOrReuseInfisicalDBCredentials`
- `generateOrReuseOryDBCredentials`

The controller's existing logic already handles `nil` secrets correctly by skipping them in the creation loop.

**Commit:** cb03f59

---

## Issue 3: Nil Pointer Dereference in Secret Generation

**Date:** 2026-04-03  
**Status:** Fixed

### Problem
Operator was panicking with:
```
panic: runtime error: invalid memory address or nil pointer dereference
at generator.go:311
```

### Root Cause
When `infisical-db-credentials` already existed, `generateOrReuseInfisicalDBCredentials` returned `nil`. The code then tried to access `infisicalDBCreds.Data["username"]` causing a nil pointer dereference.

### Fix
Modified `GenerateSecretZero` to handle nil returns by reading credentials from existing secrets:

```go
var username, password string
if infisicalDBCreds != nil {
    username = string(infisicalDBCreds.Data["username"])
    password = string(infisicalDBCreds.Data["password"])
} else if existing, ok := existingSecrets["infisical-db-credentials"]; ok {
    username = string(existing.Data["username"])
    password = string(existing.Data["password"])
} else {
    return nil, fmt.Errorf("infisical-db-credentials not found")
}
```

**Commit:** 700d77b

---

## Issue 4: ResourceVersion Error on platform-db-ca

**Date:** 2026-04-03  
**Status:** Fixed

### Problem
After fixing Issue 3, operator still failing with:
```
Failed to create secret platform-db-ca: resourceVersion should not be set on objects to be created
```

### Root Cause
When `platform-db-ca` already existed, `GenerateSecretZero` was setting `result.PlatformDBCA = existing`, which included the existing secret's `resourceVersion`. The controller then tried to `Create()` this object.

### Fix
Modified `GenerateSecretZero` to return `nil` for `PlatformDBCA` when it already exists:

```go
if existing, ok := existingSecrets["platform-db-ca"]; ok {
    // Reuse existing CA certificate - return nil to skip creation
    result.PlatformDBCA = nil
    caCert := existing.Data["ca.crt"]
    // ... continue with existing CA
}
```

**Commit:** ef55738

---

## Issue 5: Database Connection with CNPG Wildcard

**Date:** 2026-04-04  
**Status:** Fixed

### Problem
Operator failing to connect to database for migrations:
```
failed to ping database: pq: database "*" does not exist (3D000)
```

### Root Cause
CNPG creates `platform-db-superuser` secret with `dbname: *` (wildcard meaning "all databases"). The migrator and role manager read this value and try to connect to a database literally named `*`, which doesn't exist.

### Fix
Added fallback logic in both database connection functions to use `postgres` database when encountering the wildcard:

**File:** `operators/hub-operator/internal/database/migrator.go`
```go
dbname := string(secret.Data["dbname"])

// Handle CNPG wildcard database name
// CNPG uses "*" to denote superuser access to all databases
if dbname == "*" || dbname == "" {
    dbname = "postgres"
}
```

**File:** `operators/hub-operator/internal/database/roles.go`
```go
dbname := string(secret.Data["dbname"])

// Handle CNPG wildcard database name
// CNPG uses "*" to denote superuser access to all databases
if dbname == "*" || dbname == "" {
    dbname = "postgres"
}
```

**Commit:** b7ca8f3

---

## Issue 6: Missing ArgoCD Sync-Wave Annotations

**Date:** 2026-04-04  
**Status:** Fixed

### Problem
Several ArgoCD applications missing `sync-wave` annotations, causing them to deploy in wave 0 before dependencies are ready:
- `mcp-server` (no annotation)
- `kratos-selfservice-ui` (no annotation)

### Fix
Added sync-wave annotations to ensure proper deployment order:

**File:** `manifests/platform-identity/argocd/mcp-server.yaml`
```yaml
metadata:
  name: mcp-server
  namespace: hub-platform-ops
  annotations:
    argocd.argoproj.io/sync-wave: "4"
```

**File:** `manifests/platform-identity/argocd/kratos-ui.yaml`
```yaml
metadata:
  name: kratos-selfservice-ui
  namespace: hub-platform-ops
  annotations:
    argocd.argoproj.io/sync-wave: "4"
```

**Commit:** b7ca8f3

---

## Issue 7: ArgoCD OutOfSync Applications

**Date:** 2026-04-04  
**Status:** Fixed

### Problem
Applications stuck OutOfSync/Degraded due to field ownership conflicts with CRDs and ESO-managed secrets.

### Fix
Added `ServerSideApply=true` and `RespectIgnoreDifferences=true` to sync options:

**File:** `manifests/argocd/apps/platform-external-secrets.yaml`
```yaml
syncPolicy:
  automated:
    prune: true
    selfHeal: true
  syncOptions:
    - CreateNamespace=true
    - ServerSideApply=true
    - RespectIgnoreDifferences=true
```

**Commit:** b7ca8f3

---
