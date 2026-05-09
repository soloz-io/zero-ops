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

The platform provides centralized stateful services with enterprise-grade GitOps management. Applications consume them via declarative connection patterns without managing infrastructure lifecycle.

## Architecture

### Enterprise Stateful Infrastructure Pattern

```
┌─────────────────────────────────────────────────────────────────┐
│                    PLATFORM CONTROL PLANE                      │
│                     (Hub Cluster)                            │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────────┐  ┌─────────────────┐  ┌──────────────┐ │
│  │   Crossplane    │  │     ArgoCD     │  │  Infisical  │ │
│  │                 │  │                 │  │             │ │
│  │ • CNPG Clusters │  │ • Stateful     │  │ • Secrets   │ │
│  │ • Redis Stateful│  │   Infra Helm   │  │ • Rotation  │ │
│  │ • ClickHouse    │  │   Charts       │  │ • Audit     │ │
│  │ • provider-sql  │  │ • Backup Jobs  │  │             │ │
│  │ • Backups       │  │ • Monitoring   │  │             │ │
│  └─────────────────┘  └─────────────────┘  └──────────────┘ │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼ Declarative APIs
┌─────────────────────────────────────────────────────────────────┐
│                   APPLICATION LAYER                             │
│                  (Tenant Workloads)                           │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────────┐  ┌─────────────────┐  ┌──────────────┐ │
│  │   Applications │  │   ExternalSecret │  │   Service    │ │
│  │                 │  │                 │  │   Mesh       │ │
│  │ • Connection    │  │ • Auto-sync     │  │ • mTLS       │ │
│  │   Strings      │  │ • Rotation     │  │ • Policies   │ │
│  │ • No Embedded  │  │ • Templates     │  │ • Observability│ │
│  │   Databases    │  │ • Quoting       │  │             │ │
│  └─────────────────┘  └─────────────────┘  └──────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

### Stateful Service Architecture

#### PostgreSQL (CNPG - CloudNativePG)
```
platform-data Namespace
├── shared-cnpg (Cluster)
│   ├── crossplane_admin (owner - migrations)
│   ├── tenant-{id}-user (application - via provider-sql)
│   ├── tenant-{id}-db (logical database)
│   └── shared-cnpg-rw (Pooler service)
├── Database Lifecycle
│   ├── Creation: Crossplane provider-sql (declarative)
│   ├── Credentials: Infisical → ESO → Secrets
│   ├── Migrations: crossplane_admin (platform-owned)
│   └── Access: Least-privilege via DefaultPrivileges
└── Backup Strategy
    ├── Continuous WAL archiving (CNPG)
    ├── Scheduled snapshots (Barman)
    └── Point-in-time recovery capability
```

#### Redis (StatefulSet + Service)
```
platform-data Namespace
├── redis-cluster (StatefulSet)
│   ├── redis-master (primary)
│   ├── redis-replica-N (read replicas)
│   └── redis-service (cluster endpoint)
├── Access Pattern
│   ├── Internal: Pod-to-Pod (no auth)
│   ├── External: Service mesh mTLS
│   └── Monitoring: Redis Exporter + Prometheus
└── Persistence
    ├── PVC: Redis data persistence
    ├── Backup: Kubernetes Volume Snapshots
    └── Recovery: Automated restore procedures
```

#### ClickHouse (Altinity Operator)
```
platform-data Namespace
├── clickhouse-cluster (ClickHouseInstallation)
│   ├── clickhouse-server-N (replicas)
│   ├── clickhouse-service (endpoint)
│   └── zookeeper-cluster (coordination)
├── Database Isolation
│   ├── openmeter (service database)
│   ├── service-{id} (per-service databases)
│   └── User-level access control
├── Access Control
│   ├── service-{id}-user (limited to own database)
│   ├── read-only users (analytics)
│   └── admin users (platform operations)
└── Enterprise Features
    ├── Distributed tables (horizontal scaling)
    ├── Replicated tables (data durability)
    ├── Query quotas (noisy neighbor prevention)
    └── Monitoring: ClickHouse Exporter + Grafana
```

### GitOps Integration Pattern

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Platform      │    │   fleet-registry│    │     ArgoCD     │
│   Engineers     │───▶│   Repository   │───▶│ ApplicationSets │
│                 │    │                 │    │                 │
│ • Database      │    │ • stateful/     │    │ • Sync stateful │
│   definitions   │    │   infra/        │    │   infra charts  │
│ • Backup        │    │   values.yaml   │    │ • Monitor       │
│   policies     │    │                 │    │   health        │
│ • Rotation     │    │                 │    │ • Auto-repair   │
│   schedules     │    │                 │    │                 │
└─────────────────┘    └─────────────────┘    └─────────────────┘
                              │
                              ▼
                    ┌─────────────────┐
                    │  Hub Cluster    │
                    │  ┌─────────────┐ │
                    │  │ CNPG, Redis │ │
                    │  │ ClickHouse  │ │
                    │  │ Backups     │ │
                    │  │ Monitoring  │ │
                    │  └─────────────┘ │
                    └─────────────────┘
```

### Database-as-a-Service (DBaaS) Flow

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Application   │    │   Crossplane    │    │   PostgreSQL   │
│   Request      │───▶│   Composition  │───▶│   Cluster      │
│                 │    │                 │    │                 │
│ • Database      │    │ • provider-sql  │    │ • CREATE DB    │
│   name         │    │ • Role creation │    │ • CREATE USER  │
│ • Permissions   │    │ • Grant rights  │    │ • GRANT        │
│ • Quotas        │    │ • DefaultPrivs  │    │ • SET LIMITS   │
└─────────────────┘    └─────────────────┘    └─────────────────┘
                              │
                              ▼
                    ┌─────────────────┐
                    │   Infisical    │
                    │  ┌─────────────┐ │
                    │  │ Credentials │ │
                    │  │ Rotation   │ │
                    │  │ Audit      │ │
                    │  └─────────────┘ │
                    └─────────────────┘
                              │
                              ▼
                    ┌─────────────────┐
                    │   Application  │
                    │  ┌─────────────┐ │
                    │  │ Connection  │ │
                    │  │ String     │ │
                    │  │ Auto-retry │ │
                    │  └─────────────┘ │
                    └─────────────────┘
```

### PostgreSQL
- **Platform provides**: CNPG clusters in `platform-data` namespace
- **Declarative creation**: Crossplane provider-sql creates databases/users
- **Credentials**: Infisical → ESO → Kubernetes secrets
- **Access control**: Database owner `crossplane_admin`, app users via DefaultPrivileges
- **Applications consume**: External connection strings via ESO secrets
- **✅ BANNED**: Application postInitSQL for database creation

### Redis
- **Platform provides**: StatefulSet in `platform-data` namespace  
- **High availability**: Master-replica configuration
- **Applications consume**: Service endpoint with internal cluster networking
- **Security**: Service mesh mTLS for external access
- **Persistence**: PVC with automated volume snapshots

### ClickHouse
- **Platform provides**: ClickHouseInstallation (Altinity operator) in `platform-data` namespace
- **Shared cluster**: Database-level isolation per service
- **Enterprise features**: Distributed tables, query quotas, user-level access control
- **Credentials**: Hub-operator generates and uploads to Infisical
- **Applications consume**: External connection strings with database-specific access
- **Access control**: Each service user granted access only to their database

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