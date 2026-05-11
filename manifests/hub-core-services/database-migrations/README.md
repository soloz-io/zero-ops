# Database Migrations (Atlas)

This directory contains Atlas database migrations for the zero-ops platform, following ADR-020: Migration Governance with Atlas.

## Directory Structure

```
database-migrations/
├── control-plane/
│   ├── 20260511020611_agentregistry_schema.sql
│   ├── 20260511020612_agents_schema.sql
│   └── atlas.sum
├── hub/
│   ├── 20260511020613_spoke_controller_grants.sql
│   ├── 20260511020614_agent_infra_status.sql
│   ├── 20260511020615_agent_infra_status_rls.sql
│   └── atlas.sum
├── control-plane-atlasmigration.yaml
├── control-plane-migrations-configmap.yaml
├── hub-atlasmigration.yaml
└── hub-migrations-configmap.yaml
```

## Migration Naming Convention

All migrations follow UTC timestamp naming: `YYYYMMDDHHMMSS_descriptive_name.sql`

## Governance (ADR-020)

- **Immutable**: Never edit historical migrations
- **No rebasing**: Migration order must be preserved
- **Checksum verification**: atlas.sum files contain SHA256 hashes
- **Domain separation**: control-plane and hub migrations are independent
- **Idempotent SQL**: All migrations use IF NOT EXISTS, CREATE OR REPLACE

## Sync Path

These migrations are synced via the 03-platform-services boundary:
- Applied after Crossplane creates databases/roles (Logical Layer)
- Applied before platform services start
- Synced at wave 3 (after platform-data at wave 2)

## Database Ownership

```
CNPG (Physical Layer) → Crossplane (Logical Layer) → Atlas (Schema Layer)
```

- CNPG: Manages PostgreSQL cluster, physical storage
- Crossplane: Manages database creation and role provisioning
- Atlas: Manages schema migrations and drift detection

## Adding New Migrations

1. Create new SQL file with UTC timestamp: `YYYYMMDDHHMMSS_description.sql`
2. Write idempotent SQL (use IF NOT EXISTS, CREATE OR REPLACE)
3. Update ConfigMap with new migration file
4. Regenerate atlas.sum: `atlas migrate hash --dir file://<directory>`
5. Update ConfigMap with new atlas.sum
6. Commit to Git
7. ArgoCD will sync and Atlas Operator will apply

## Atlas CLI Commands

```bash
# Generate checksums
atlas migrate hash --dir file://manifests/hub-core-services/database-migrations/control-plane
atlas migrate hash --dir file://manifests/hub-core-services/database-migrations/hub

# Validate migrations (requires dev database)
atlas migrate validate --dir file://manifests/hub-core-services/database-migrations/control-plane
atlas migrate validate --dir file://manifests/hub-core-services/database-migrations/hub

# Apply migrations locally (for testing)
atlas migrate apply --dir file://manifests/hub-core-services/database-migrations/control-plane --url $DATABASE_URL
```
