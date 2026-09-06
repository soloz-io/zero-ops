# Secret Zero Generation Package

This package implements cryptographic secret generation for the Hub Operator's Secret Zero phase (Requirement 4).

## Overview

Secret Zero is the initial set of secrets required to bootstrap the Hub environment. All secrets are generated using cryptographically secure random sources (`crypto/rand`) and are idempotent - existing secrets are reused when regenerating.

## Functions

### `GenerateSecretZero(ctx context.Context, client client.Client, env *v1alpha1.HubEnvironment) error`

Orchestrates the generation of all Secret Zero secrets. Idempotent - safe to call multiple times.

**Generated Secrets:**
- `infisical-secrets` - Infisical application secrets (ENCRYPTION_KEY, AUTH_SECRET, REDIS_URL, DB_ROOT_CERT)
- `platform-db-app` - BasicAuth credentials for platform-db-app user
- `infisical-db-credentials` - PostgreSQL credentials for Infisical
- `infisical-postgres-connection` - Connection string with SSL mode
- `platform-db-ca` - Self-signed CA certificate (TLS secret type)

**All secrets include:**
- `ownerReferences` pointing to the HubEnvironment CR (Requirement 4.22)
- Label `ops.nutgraf.in/db-credentials=true` on credential secrets

### Helper Functions

- `GenerateSecurePassword()` - 32-character hex password using crypto/rand
- `GenerateSelfSignedCA()` - 4096-bit RSA CA with 10-year validity
- `GenerateInfisicalSecrets()` - Infisical application secrets
- `GeneratePlatformDBApp()` - BasicAuth secret for platform-db-app
- `GenerateInfisicalDBCredentials()` - Infisical database credentials
- `GenerateInfisicalPostgresConnection()` - Connection secret with SSL
- `GenerateOryDBCredentials()` - Ory stack database credentials
- `GeneratePlatformDBCA()` - CA certificate TLS secret

## Usage

```go
import (
    "context"
    "github.com/zero-ops/hub-operator/internal/secrets"
    "sigs.k8s.io/controller-runtime/pkg/client"
)

// In controller reconciliation
err := secrets.GenerateSecretZero(ctx, r.Client, hubEnv)
if err != nil {
    return ctrl.Result{}, err
}
```

## Security Considerations

- All passwords use `crypto/rand` for cryptographic security
- CA certificates use 4096-bit RSA keys
- Secrets are namespace-scoped to the HubEnvironment's namespace
- OwnerReferences ensure secrets are garbage collected with the CR
- Idempotency prevents password rotation on every reconciliation
