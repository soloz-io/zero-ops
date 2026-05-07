# HubEnvironment Controller

## Overview

The HubEnvironmentReconciler is the core controller that orchestrates Day-2 operations for the Zero-Ops Hub cluster. It watches the HubEnvironment Custom Resource and executes a multi-phase reconciliation workflow to provision and configure all Hub infrastructure components.

## Architecture

### Reconciliation Phases

The controller executes phases sequentially, updating status conditions after each phase:

1. **Phase 1: Secret Zero Generation**
   - Generates cryptographically secure bootstrap secrets
   - Creates 8 Kubernetes secrets with ownerReferences
   - Idempotent: reuses existing passwords if secrets exist
   - Sets `SecretZeroGenerated` condition to True

2. **Phase 2a: Database Migrations**
   - Waits for CNPG Cluster to be Ready
   - Executes golang-migrate migrations from embedded filesystem
   - Connects to platform-db-rw (primary), not pooler
   - Handles dirty database state (permanent error, no requeue)
   - Sets `MigrationsComplete` condition to True

3. **Phase 2b: Database Role Provisioning**
   - Creates 7 database roles via database/sql
   - Grants permissions based on CR spec
   - Detects password drift and executes ALTER ROLE
   - Prunes orphaned roles not in CR spec
   - Sets `DatabaseRolesConfigured` condition to True

4. **Phase 3a: Infisical Secret Upload**
   - Waits for Infisical Deployment to be Ready
   - Uploads database credentials to Infisical API
   - Tracks uploaded secrets in `Status.UploadedSecrets` array
   - Skips secrets already uploaded (idempotency)
   - Suspends if infisical-auth secret missing
   - Sets `SecretsBackedUp` condition to True

5. **Phase 3b: OAuth Client Registration**
   - Waits for Hydra Deployment to be Ready
   - Registers OAuth clients using Ory Hydra Go SDK
   - Idempotent client_id check
   - Prunes orphaned clients not in CR spec
   - Sets `OAuthClientsRegistered` condition to True

6. **Phase 3c: NATS Stream Creation**
   - Waits for NATS StatefulSet to be Ready
   - Creates JetStream streams using NATS Go SDK
   - Detects configuration drift and updates streams
   - Prunes orphaned streams not in CR spec
   - Sets `NATSStreamsConfigured` condition to True

7. **Ready Condition**
   - Set when all phases complete successfully
   - Indicates Hub environment is fully operational

### Dependency Readiness Checks

The controller implements readiness checks for external dependencies:

- `isCNPGReady()`: Checks CNPG Cluster Ready condition
- `isInfisicalReady()`: Checks Infisical Deployment status
- `isHydraReady()`: Checks Hydra Deployment status
- `isNATSReady()`: Checks NATS StatefulSet status

Reconciliation waits (requeues after 10 seconds) if dependencies are not ready.

## Error Handling

### Error Classification

The controller distinguishes between transient and permanent errors:

**Transient Errors** (requeue with backoff):
- Database connection failures
- API timeouts (Hydra, Infisical, NATS)
- Network failures
- 5xx HTTP errors

**Permanent Errors** (no automatic requeue):
- Dirty database state (requires manual intervention)
- Invalid CR configuration

### Retry Strategy

- Transient errors: Requeue after 30 seconds (exponential backoff via controller-runtime)
- Dependency not ready: Requeue after 10 seconds
- Permanent errors: Set condition to False, do NOT requeue

### Manual Recovery

For permanent errors (e.g., dirty database), add the `ops.nutgraf.in/reconcile-trigger` annotation:

```bash
kubectl annotate hubenvironment hub-env \
  ops.nutgraf.in/reconcile-trigger="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
```

This clears permanent error conditions and resumes reconciliation.

## Status Conditions

The controller maintains 7 status conditions:

| Condition | Type | Reason | Description |
|-----------|------|--------|-------------|
| SecretZeroGenerated | True/False | Generated/Failed | Bootstrap secrets created |
| MigrationsComplete | True/False | Completed/DirtyDatabase/MigrationFailed | Database migrations executed |
| DatabaseRolesConfigured | True/False | Configured/RoleCreationFailed | Database roles provisioned |
| SecretsBackedUp | True/False | Uploaded/AuthenticationFailed | Secrets uploaded to Infisical |
| OAuthClientsRegistered | True/False | Registered/Failed | OAuth clients registered in Hydra |
| NATSStreamsConfigured | True/False | Configured/Failed | NATS streams created |
| Ready | True/False | AllPhasesComplete/Failed | Hub environment fully operational |

## RBAC Permissions

The controller requires the following permissions:

```yaml
# HubEnvironment CRD
- apiGroups: ["ops.nutgraf.in"]
  resources: ["hubenvironments", "hubenvironments/status", "hubenvironments/finalizers"]
  verbs: ["get", "list", "watch", "update", "patch"]

# Secrets (owned)
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]

# CNPG Cluster (dependency check)
- apiGroups: ["postgresql.cnpg.io"]
  resources: ["clusters"]
  verbs: ["get", "list", "watch"]
```

## Idempotency

All reconciliation operations are idempotent:

- Secret generation reuses existing passwords
- Database migrations use golang-migrate (tracks applied migrations)
- Role creation uses CREATE ROLE IF NOT EXISTS pattern
- OAuth client registration checks client_id before creating
- NATS stream creation checks stream name before creating
- Infisical uploads track uploaded secrets in Status.UploadedSecrets

Running reconciliation multiple times produces the same end state.

## Structured Logging

The controller uses controller-runtime structured logging:

```go
logger := log.FromContext(ctx)
logger.Info("Phase 1: Generating Secret Zero")
logger.Error(err, "Failed to create secret", "secret", secretName)
```

All operations log phase transitions, errors, and important events.

## Watch Configuration

The controller watches:

- **Primary Resource**: HubEnvironment CR
- **Owned Resources**: Secrets created by the operator (via ownerReferences)
- **External Dependencies**: CNPG Cluster (for readiness)

Future tasks will add watches for:
- Hydra/Infisical/NATS Deployments (readiness)
- platform-db-ca secret (certificate rotation)
- Secrets with label `ops.nutgraf.in/db-credentials=true` (password rotation)

## Testing

### Unit Tests (envtest)
- Use controller-runtime fake client
- Test reconciliation logic in isolation
- Mock external API calls

### E2E Tests (KUTTL)
- Deploy real CNPG, Hydra, Infisical, NATS
- Assert database roles exist via SQL queries
- Assert OAuth clients exist via Hydra API
- Assert NATS streams exist via NATS API
- Assert secrets exist in Infisical via API

## Development Workflow

### Local Testing

1. Start a local cluster (kind or k3d)
2. Install CRDs: `make install`
3. Run controller: `make run`
4. Apply HubEnvironment CR: `kubectl apply -f config/samples/`

### GitOps Deployment

1. Commit changes to feature branch
2. Push to GitHub
3. ArgoCD syncs operator to dev cluster
4. Verify reconciliation via status conditions

**NEVER use `kubectl apply` for production deployments. All changes must go through GitOps.**

## Troubleshooting

### Reconciliation Stuck

Check status conditions:
```bash
kubectl get hubenvironment hub-env -o jsonpath='{.status.conditions}' | jq
```

### Dirty Database State

Manual recovery required:
1. Connect to database: `kubectl exec -it platform-db-1 -- psql`
2. Check migration version: `SELECT * FROM schema_migrations;`
3. Fix dirty state: `UPDATE schema_migrations SET dirty = false WHERE version = X;`
4. Trigger reconciliation: Add `ops.nutgraf.in/reconcile-trigger` annotation

### Infisical Upload Suspended

Check if infisical-auth secret exists:
```bash
kubectl get secret infisical-auth -n platform-ops
```

If missing, Phase 3a will suspend until the secret is created.

### Dependency Not Ready

Check dependency status:
```bash
kubectl get cluster platform-db -o jsonpath='{.status.conditions}'
kubectl get deployment infisical -n platform-ops
kubectl get deployment hydra -n ory-system
kubectl get statefulset nats -n platform-core
```

Reconciliation will requeue every 10 seconds until dependencies are ready.

## References

- [Kubebuilder Documentation](https://book.kubebuilder.io/)
- [Controller-Runtime](https://github.com/kubernetes-sigs/controller-runtime)
- [CloudNativePG API](https://cloudnative-pg.io/documentation/current/api_reference/)
- [Ory Hydra Go SDK](https://github.com/ory/hydra-client-go)
- [NATS Go SDK](https://github.com/nats-io/nats.go)
