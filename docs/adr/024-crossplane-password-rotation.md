# ADR-021: Crossplane Password Rotation Pattern

**Date:** 2026-05-11
**Status:** Approved
**Related ADRs:** ADR-020 (Migration Governance), ADR-004 (Dual-Repo GitOps Pattern)

## Context

Hub-operator previously implemented imperative password rotation logic that monitored ESO secrets, executed manual `ALTER ROLE` statements against PostgreSQL, and triggered service restarts. This approach created several issues:

- **Race Conditions:** Hub-operator and Crossplane could both modify database roles
- **Operational Complexity:** Manual service restarts and error handling
- **GitOps Violations:** Password changes applied imperatively outside GitOps control
- **Single Point of Failure:** Hub-operator became critical path for all password updates
- **Inconsistent State:** Manual `ALTER ROLE` could drift from declarative Crossplane state

The platform needed a declarative, GitOps-native approach to password rotation that eliminates orchestration hacks and provides consistent state management.

## Decision

### Crossplane-First Password Rotation

All database role password management moves to Crossplane Role CRs with declarative passwordSecretRef. Hub-operator removes all manual password rotation logic. Crossplane reconciliation loop handles password updates automatically.

### Declarative Role Management

```yaml
apiVersion: postgresql.sql.crossplane.io/v1alpha1
kind: Role
metadata:
  name: mcp-server-role
  namespace: platform-data
spec:
  forProvider:
    database: hub
    name: mcp_server
    passwordSecretRef:
      namespace: platform-data
      name: mcp-server-db-credentials
      key: password
    login: true
    privileges:
      - database: hub
        schema: public
        privileges: [CONNECT, CREATE, TEMPORARY, USAGE]
  providerConfigRef:
    name: hub-sql-provider
```

### Elimination of Manual Rotation Logic

Hub-operator removes all password rotation code:
- No `database.NewRoleManager()` usage
- No `ALTER ROLE` execution
- No manual service restarts
- No ESO secret monitoring

### Crossplane Reconciliation Flow

```
ESO Secret Update → Crossplane Role CR Reconciles → PostgreSQL Password Updated
```

1. ESO updates secret from Infisical
2. Crossplane detects secret change via `passwordSecretRef`
3. Crossplane executes `ALTER ROLE` with new password
4. Services automatically use new password from same secret
5. No manual intervention required

## Rationale

### Declarative GitOps Control

**Before:** Hub-operator imperative password changes outside GitOps
**After:** Crossplane declarative role management in Git

Crossplane ensures all password changes go through GitOps pipeline, providing audit trail and approval workflow.

### Eliminated Race Conditions

**Before:** Hub-operator and Crossplane could both modify roles
**After:** Crossplane is single source of truth for role management

No more dual ownership conflicts or state drift between imperative and declarative systems.

### Simplified Operations

**Before:** Manual service restarts, error handling, monitoring
**After:** Automatic reconciliation, self-healing, status reporting

Crossplane handles all complexity internally with built-in retry logic and status conditions.

### Consistent State Management

**Before:** Manual `ALTER ROLE` could drift from Git state
**After:** Crossplane ensures database state matches CR state

Crossplane continuously reconciles to maintain declared state, preventing manual drift.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Spoke Infrastructure Credentials | Infisical | Hub Operator | ESO | Crossplane provider-sql | Day-1+ |
| Database Roles / Grants | PostgreSQL | Crossplane | Crossplane provider-sql | Tenant Apps | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

- **GitOps Native:** All password changes through Git pipeline
- **Self-Healing:** Crossplane automatically fixes password drift
- **Zero Downtime:** Services use same secret, no restarts needed
- **Reduced Complexity:** Hub-operator code simplified by ~200 lines
- **Better Observability:** Crossplane provides status conditions and metrics
- **Consistent Pattern:** All role management follows same declarative approach

### Negative

- **Crossplane Dependency:** Password rotation requires Crossplane health
- **Learning Curve:** Team must understand Crossplane reconciliation
- **Migration Risk:** Existing manual password changes may need reconciliation

### Neutral

- **No Service Restarts:** Services pick up new passwords on connection/reconnect
- **Same Secret Usage:** No changes to application credential consumption
- **ESO Integration:** Unchanged - still manages secret synchronization

## Implementation

### Phase 1: Crossplane Role CRs (Completed)

- Create Role CRs for all database roles
- Add `passwordSecretRef` to each role
- Include proper privilege grants
- Deploy via 03-platform-services boundary

### Phase 2: Hub-operator Cleanup (Completed)

- Remove `database.NewRoleManager()` imports
- Remove `handlePasswordRotation()` function
- Remove manual service restart logic
- Add documentation comments about Crossplane ownership

### Phase 3: Validation Logic (Proposed)

Add lightweight validation in hub-operator to monitor Crossplane role status:

```go
func (r *HubEnvironmentReconciler) validateCrossplaneRoles(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
    // Check Crossplane Role CRs are Ready
    // Log warnings for unhealthy roles
    // Don't fail reconciliation - just observability
}
```

### Phase 4: Monitoring Integration (Future)

- Crossplane role status metrics
- Password rotation success/failure alerts
- Role drift detection alerts
- Service connection health monitoring

## Migration Guide

### For Existing Clusters

1. **Deploy Crossplane Role CRs** via 03-platform-services
2. **Verify Role Status:** `kubectl get role -n platform-data`
3. **Remove Hub-operator Logic:** Deploy updated hub-operator image
4. **Monitor:** Watch Crossplane reconciliation logs
5. **Validate:** Test password rotation via ESO secret update

### For New Clusters

No migration needed - Crossplane roles deployed automatically during bootstrap.

## Security Considerations

### Secret Access

Crossplane Role CRs reference secrets via `passwordSecretRef`:
- Crossplane needs RBAC to read credential secrets
- No passwords stored in Role CRs (only references)
- ESO continues to manage secret lifecycle

### Audit Trail

All password changes now go through:
1. Infisical (source of truth)
2. ESO (synchronization)
3. GitOps pipeline (approval)
4. Crossplane (application)
5. PostgreSQL (target)

Full audit trail from source to database.

### Failure Modes

- **Crossplane Unhealthy:** Password changes queue until Crossplane recovers
- **Secret Sync Failure:** ESO handles retry logic
- **Database Connection Failure:** Crossplane retries with exponential backoff

## Testing Strategy

### Unit Tests

- Crossplane Role CR validation
- Secret reference resolution
- Privilege grant verification

### Integration Tests

- ESO secret update → Crossplane reconciliation
- Password rotation end-to-end
- Service connectivity after password change

### Chaos Tests

- Crossplane pod deletion during password rotation
- Network partition between Crossplane and database
- ESO secret sync failures

## Future Considerations

### Enhanced Monitoring

- Prometheus metrics for password rotation frequency
- Alerting for failed password updates
- Dashboard for Crossplane role status

### Multi-Database Support

- Pattern extends to additional databases (MySQL, etc.)
- Consistent role management across database types
- Database-agnostic password rotation

### Automation

- Automatic password rotation policies
- Integration with external secret management systems
- Scheduled password updates for compliance

## References

- [Crossplane Provider SQL Documentation](https://docs.crossplane.io/v1.9/reference/provider-sql.html)
- [ADR-020: Migration Governance with Atlas](020-migration-governance-atlas.md)
- [ADR-004: Dual-Repo GitOps Pattern](004-dual-repo-gitops-pattern.md)
- [Kubernetes Secret Management Best Practices](https://kubernetes.io/docs/concepts/configuration/secret/)