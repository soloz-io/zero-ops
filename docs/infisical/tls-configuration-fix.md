# Infisical TLS Configuration Fix

## Issue Summary
Infisical pods entering CrashLoopBackOff due to contradictory TLS configuration causing `self-signed certificate in certificate chain` errors.

## Root Cause
Configuration anti-pattern with contradictory TLS settings:
- `DB_SSL_MODE=disable` in `infisical-postgres-connection` secret
- `DB_ROOT_CERT` present in `infisical-secrets` secret (from old direct connection)
- Infisical's Knex code checks `if (process.env.DB_ROOT_CERT)` and forces SSL regardless of `DB_SSL_MODE`

**Result:** Infisical tried to use TLS with an outdated certificate, causing handshake failures.

**Correct Configuration:** After pooler migration (March 30), TLS configuration should have been updated to use CNPG's pooler certificates, not removed entirely.

## Best Practice
TLS Everywhere (Best Practice)
Infisical → PgBouncer → PostgreSQL
     TLS         TLS           TLS

## Architecture Context
Production-grade TLS configuration (TLS everywhere):
- Client (Infisical) → Pooler: YES TLS (using CNPG-provided certificates)
- Pooler → PostgreSQL: YES TLS (handled by CNPG internally)

This implements end-to-end encryption across all hops, following zero-trust networking principles.

## Fixes Applied

### 1. Database Role Creation
**File:** `manifests/platform-database/setup-platform-roles-job.yaml`
- Added `cnpg_pooler_pgbouncer` role creation
- PgBouncer pooler requires this role for authentication

### 2. CLI Secret Generation Logic
**File:** `internal/hub/components/installer.go`

**InstallInfisicalSecrets():**
- Reads CA certificate from CNPG-managed `platform-db-ca` secret
- Extracts `ca.crt` and base64 encodes it
- Injects as `DB_ROOT_CERT` in `infisical-secrets`
- Now generates: `ENCRYPTION_KEY`, `AUTH_SECRET`, `REDIS_URL`, `DB_ROOT_CERT`
- Enables TLS for Infisical → PgBouncer connection

**InstallPostgresConnectionSecret():**
- Creates connection params with `DB_SSL_MODE=require` (TLS enabled)
- Points to pooler endpoint: `platform-db-pooler.zero-ops-system.svc`
- Enforces consistent TLS configuration across all connection parameters

### 3. PgBouncer Authentication Configuration
**File:** `manifests/platform-database/platform-db-pooler.yaml`
- Added `authQuery: "SELECT usename, passwd FROM pg_shadow WHERE usename=$1"`
- Enables PgBouncer to authenticate users via PostgreSQL's pg_shadow
- CNPG automatically creates `user_search` function when pooler with authQuery is deployed
- Fixes "bouncer config error" by enabling proper authentication mechanism

### 4. Helm Chart Configuration
**File:** `manifests/platform-infisical/values.yaml`
- Uses `envFrom` to inject `infisical-secrets`
- Uses `extraEnv` for individual DB connection params from `infisical-postgres-connection`
- Added comments explaining TLS configuration strategy

### 5. CLI Command Reordering
**File:** `cmd/hub/init_secrets.go`
- Reordered to fix circular dependency:
  1. Generate Infisical's own secrets first (no Infisical API needed)
  2. Wait for Infisical to become healthy
  3. Store other credentials via Infisical API

## Production-Grade TLS Pattern
Enforces TLS everywhere with consistent configuration:

**TLS Enabled (current - production-grade):**
- `DB_SSL_MODE=require` (or `verify-full` for stricter validation)
- `DB_ROOT_CERT` present (CNPG pooler CA certificate)
- End-to-end encryption: Infisical → PgBouncer → PostgreSQL

**Benefits:**
- Zero-trust networking (encrypt all hops)
- Defense in depth
- Service mesh compatible
- Future-proof architecture

## Resolution Steps
1. Build updated CLI: `go build -o bin/hub ./cmd/hub`
2. Regenerate secrets: `hub init-secrets --kubeconfig ~/.kube/config`
3. Verify `DB_ROOT_CERT` contains CNPG CA: `kubectl get secret infisical-secrets -n zero-ops-system -o jsonpath='{.data.DB_ROOT_CERT}' | base64 -d | openssl x509 -text -noout | head -20`
4. Verify `DB_SSL_MODE=require`: `kubectl get secret infisical-postgres-connection -n zero-ops-system -o jsonpath='{.data.DB_SSL_MODE}' | base64 -d`
5. Infisical pods restart automatically and connect via TLS successfully

## Related Issues
- Missing `cnpg_pooler_pgbouncer` role (fixed)
- Missing PgBouncer authQuery configuration (fixed)
- TLS configuration drift from March 30 pooler migration
- Previous workarounds with `NODE_TLS_REJECT_UNAUTHORIZED` (reverted)

## References
- `docs/infisical/database-connection-pooling.md` - Architecture
- `docs/infisical/migration-failure-rca.md` - Previous TLS issues
