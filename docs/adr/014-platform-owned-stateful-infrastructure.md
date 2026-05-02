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
- Deferred until scale requirements justify deployment
- When deployed, follows same platform-owned pattern

### Application Configuration
- Applications set `postgresql.enabled: false` and `redis.enabled: false` in Helm values
- Applications configure external endpoints in `config` section
- Credentials injected via ExternalSecret-created secrets

## Consequences

### Positive
- Unified backup strategy via CNPG continuous archiving
- Centralized monitoring via cnpg2monitor operator
- Consistent upgrade procedures across all applications
- Resource efficiency through shared clusters
- Single team owns all stateful infrastructure operations

### Negative
- Database cluster failure affects multiple applications
- Database upgrades require coordinating with all application teams
- Applications cannot choose their own database versions

### Mitigations
- CNPG HA (3 replicas) with automated failover
- Connection pooling (PgBouncer) prevents noisy neighbor issues
- Maintenance windows communicated via platform calendar
- Move metering and billing to seperate HA cluster in future
