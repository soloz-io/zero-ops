# ADR 014: Platform-Owned Stateful Infrastructure

**Date:** 2026-05-02  
**Status:** Accepted  
**Authors:** Platform Engineering Team  
**Related ADRs:**
- [ADR 002: Hub CNPG Database Migration Pattern](./002-hub-cnpg-database-migration-pattern.md)
- [ADR 006: Multi-Tenant Database Pattern](./006-multi-tenant-database-pattern.md)

## Context

Applications requiring stateful infrastructure (PostgreSQL, Redis, ClickHouse) can either deploy embedded databases via Helm chart dependencies or consume platform-provided centralized services.

OpenMeter Helm chart includes embedded Bitnami PostgreSQL and Redis. This creates operational fragmentation: each application manages its own database lifecycle, backups, monitoring, and upgrades independently.

## Decision

**Guiding Principle:**
> "Stateful infrastructure is a platform concern; applications consume it—they don't own it."

The platform provides centralized stateful services. Applications consume them via connection strings and disable embedded databases in their Helm charts.

### PostgreSQL
- Platform provides CNPG clusters in `hub-platform-data` namespace
- Applications request databases via `CREATE DATABASE` in CNPG postInitSQL
- Hub-operator generates credentials and uploads to Infisical
- Applications consume via external connection strings

### Redis
- Platform provides StatefulSet in `hub-platform-data` namespace
- Applications consume via service endpoint
- No authentication required (internal cluster network)

### ClickHouse
- Platform provides ClickHouseInstallation (Altinity operator) in `hub-platform-data` namespace
- Shared cluster with database-level isolation per service (e.g., `openmeter` database)
- Hub-operator generates credentials and uploads to Infisical
- Applications consume via external connection strings with database-specific access
- User-level access control: each service user granted access only to their database

**Production Readiness Checklist:**
- [ ] HA configuration (≥ 2 replicas) - deferred until staging/production
- [ ] Network access restricted to Pod CIDR (not `::/0`)
- [ ] User-level quotas and query limits enforced
- [ ] Storage sizing based on workload projections (current: 10Gi for dev/MVP)
- [ ] Replication strategy defined for durability
- [ ] Backup and restore procedures documented
- [ ] Monitoring and alerting configured
- [ ] Resource controls to prevent noisy neighbor issues
- [ ] Implement query monitoring and slow query alerts
- [ ] Configure distributed tables for horizontal scaling

### Application Configuration
- Applications set `postgresql.enabled: false` and `redis.enabled: false` in Helm values
- Applications configure external endpoints in `config` section
- Credentials injected via ExternalSecret-created secrets

## Consequences

### Positive
- Unified backup strategy via CNPG continuous archiving (PostgreSQL)
- Centralized monitoring via cnpg2monitor operator (PostgreSQL)
- Consistent upgrade procedures across all applications
- Resource efficiency through shared clusters
- Single team owns all stateful infrastructure operations
- Database-level isolation provides strong security boundaries
- Scalable pattern: add new databases without new clusters

### Negative
- Database cluster failure affects multiple applications
- Database upgrades require coordinating with all application teams
- Applications cannot choose their own database versions

### Mitigations
- CNPG HA (3 replicas) with automated failover (PostgreSQL)
- Connection pooling (PgBouncer) prevents noisy neighbor issues (PostgreSQL)
- Database-level isolation in ClickHouse prevents cross-service data access
- User-level access control enforces least-privilege principle
- Maintenance windows communicated via platform calendar
- Move metering and billing to separate HA cluster in future

## References

- **ADR 003**: ESO-Infisical Integration Pattern
- **ADR-0009**: Zero-Trust Multi-Layer Authentication with JWKS and Service Mesh