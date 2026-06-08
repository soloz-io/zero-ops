# ADR-020: Enterprise Migration Governance with Atlas

**Date:** 2026-05-11
**Status:** Proposed

## Context

Hub database migrations currently use imperative Kubernetes Jobs (`migrate/migrate`) while tenant migrations use declarative AtlasMigration CRs. This creates operational complexity and governance gaps. Migrations lack formal policies for immutability, rollback, lineage, and recovery procedures. The platform has no CI/CD validation for migration integrity or documented procedures for Atlas failure scenarios.

## Decision

### Database Ownership Boundaries

CNPG owns physical layer (Pods, PVCs, Services, HA, backups). Provider-SQL owns logical layer (Database, Role, Grant CRs). Atlas owns schema layer (DDL, migrations, versioning). No overlapping ownership between layers. Each layer has independent reconciliation domains and failure modes.

### Migration Naming Convention

Use UTC timestamp-based naming: `YYYYMMDDHHMMSS_descriptive_name.sql`. CI enforces monotonic ordering to prevent merge collision ambiguity. If two migrations are created simultaneously, CI fails and requires manual timestamp adjustment. Examples: `20260511143000_create_agent_registry_tables.sql`, `20260511144500_add_agent_status_rls.sql`.

### Migration Immutability

Applied migrations are immutable forever. Never edit historical migration SQL files, rebase migration commit history, force-rewrite `atlas.sum` files, or modify migration filenames after commit. Create new migration files for schema changes.

### Migration Behavior

Migration behavior must be deterministic and drift-safe. Conditional DDL should be used sparingly and only where explicitly justified. Overusing `IF NOT EXISTS` or `CREATE OR REPLACE` can mask drift, hide failed assumptions, or conceal incompatible state.

### Rollback Philosophy

Forward-fix migrations only. Do not create "down" migration files. For problematic schema changes, create new forward-fix migrations. Use application rollback for deployment issues, database backups for data recovery, feature flags for behavioral rollbacks.

### Migration Domain Separation

Maintain strict separation between migration domains: `database/migrations/atlas/control-plane/` and `database/migrations/atlas/hub/`. Each domain has independent `AtlasMigration` CR and `atlas.sum`. No cross-domain migration dependencies.

### Checksum Policy

`atlas.sum` must be generated from final committed content using `atlas migrate hash --dir`. Never create placeholder checksums. Historical migration files and `atlas.sum` are immutable. Manual checksum regeneration is only permitted for unapplied migrations during active development before promotion. Never regenerate checksums for applied migrations.

### Drift Governance

Manual production schema changes are forbidden. Ad-hoc `psql` DDL is forbidden. Emergency `ALTER TABLE` outside migration process is forbidden except under break-glass procedure requiring platform engineering approval. All schema changes must go through migration process with CI/CD validation.

### Multi-Version Compatibility

Use expand → migrate → contract pattern for rolling upgrades. Support minimum 24-hour compatibility window between expand and contract phases. This is guaranteed platform-wide for all services. Exceptions require explicit platform engineering approval and documented compatibility window.

### Migration Locking

Atlas Operator handles concurrent migration execution via database-level advisory locks. Multiple Atlas controllers cannot simultaneously mutate the same schema. Lock timeout is 5 minutes. Lock acquisition failures result in retry with exponential backoff. Manual lock breaking requires break-glass procedure.

### Merge Collision Handling

CI enforces monotonic timestamp ordering. If merge conflict occurs due to simultaneous migration creation, CI fails and requires manual timestamp adjustment. Never rebase migration history to resolve ordering conflicts.

### Database Connection

Reuse existing credential secrets: `control-plane-db-credentials` for control-plane migrations, `hub-db-credentials` for hub migrations. Use standard PostgreSQL connection URL format with SSL required.

### Atlas Failure Recovery

For dirty migration states: identify failed version, investigate logs, restore from backup if data corruption, retry if transient error. For partially applied migrations: verify schema state, reconcile manually if drift detected. For failed rollouts: check operator health, database connectivity, lock states, restart operator if needed, create forward-fix migration for logic errors. For schema drift: identify source, restore expected state if unintentional, document if intentional, implement preventive controls. Manual reconciliation: pause Atlas Operator, manual schema inspection, create forward-fix migration for corrections, resume Atlas Operator. Never regenerate `atlas.sum` for applied migrations.

### Recovery Operational Details

PITR expectations: 15-minute RPO, 1-hour RTO for critical databases. Migration freeze procedures: 24-hour freeze before major releases, emergency migration freeze requires CTO approval. Rollback authority: platform engineering for schema changes, service owners for application rollbacks. Escalation ownership: platform engineering on-call for Atlas issues, database administrators for data recovery.

### CI/CD Validation

Required pre-merge checks: atlas migrate validate, atlas migrate lint, checksum verification, destructive change detection, migration format check. Migrations cannot merge without passing all validation checks.

### Migration Review

Required checklist: naming convention compliance, deterministic behavior, no destructive changes without approval, forward-fix pattern, backward compatibility considered, performance impact assessed, appropriate transaction boundaries, robust error handling, adequate documentation, peer review completed, atlas.sum generated from final content, CI/CD validation passed. Requires database owner approval.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Database Schemas | Git | Atlas Operator | Atlas Operator | provider-sql | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

Unified migration strategy across Hub and Spoke, declarative GitOps schema lifecycle, elimination of imperative migration orchestration, documented failure recovery procedures, CI/CD validation prevents broken migrations, immutable migration lineage with checksums, explicit ownership boundaries, drift governance, locking semantics.

### Negative

Increased governance process slows simple changes, team learning curve for Atlas patterns, tooling dependency on Atlas CLI in CI/CD, operational overhead for monitoring Atlas Operator health, merge collision handling requires manual intervention.
