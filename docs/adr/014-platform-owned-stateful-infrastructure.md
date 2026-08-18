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

> "Stateful infrastructure runs on worker nodes — never on control-plane nodes — in every cluster, every provider cell, and every environment."

The platform provides centralized stateful services with enterprise-grade GitOps management. Applications consume them via declarative connection patterns without managing infrastructure lifecycle.

### Platform-Wide Placement Rule (Mandatory)

> **Stateful infrastructure runs on worker nodes — never on control-plane nodes — in the Hub and in every Spoke, in every provider cell (Hetzner, hybrid), and in every environment (dev, staging, prod). There are no exceptions and no per-cluster opt-outs.**

This rule is platform-wide and topology-independent. It carries forward the node-scheduling requirement from ADR-002 (superseded) and is the single authority for stateful workload placement.

**Rationale.** Control-plane nodes run `etcd`, `kube-apiserver`, and the scheduling/control loops the entire fleet depends on (ADR-034). Co-locating stateful workloads there mixes data-plane risk into the control plane: disk pressure, I/O contention, and pod churn from a database can destabilise cluster control operations — and every CP node is a failure domain for every pod scheduled onto it. Worker nodes are the designated runtime capacity; the control plane is infrastructure, not capacity. Placement must therefore be a declared property of the workload, never an accident of storage binding or available disk.

**The rule (mandatory, SHALL):**

- ✅ SHALL: CNPG Clusters, CNPG Poolers, Redis, ClickHouse, and **every stateful workload pod** run on **worker nodes only**, in every cluster.
- ✅ SHALL: database migration jobs run on **worker nodes only**, in every cluster.
- ✅ SHALL: stateful platform workloads pin placement with `nodeSelector: node-role.kubernetes.io/worker: ""` — the CAPI/kubeadm **key-only** label; the value `"true"` is **invalid and never matches**.
- ✅ SHALL: control-plane nodes carry the `control-plane:NoSchedule` taint (kubeadm default). The taint SHALL NOT be removed, and stateful workloads SHALL NOT tolerate it.
- ✅ SHALL: placement be **enforced by selector + taint** — never incidentally by storage binding (a node that can attach a PVC is not a placement decision).
- ✅ SHALL: every stateful workload's StorageClass be provisionable on **its worker node class**: Hetzner workers → `hcloud-volumes`; home-lab workers in hybrid cells → `local-path` (ADR-046). A PVC whose storage class the scheduled node cannot serve is a placement violation, not a scheduling detail.

**BANNED (never, in any topology):**

- ❌ SHALL NOT: any stateful workload (CNPG, Redis, ClickHouse, stateful jobs) run on a control-plane node.
- ❌ SHALL NOT: any workload tolerate the control-plane taint to place stateful data on the CP.
- ❌ SHALL NOT: `node-role.kubernetes.io/worker: "true"` be used anywhere — it silently never matches and silently schedules onto the CP instead.
- ❌ SHALL NOT: placement be left implicit (no nodeSelector) and "whatever node has the right storage class" be relied upon.

**Enforcement owner.** Placement selectors and taints are platform-owned (spoke catalog / hub core services manifests); fleets do not choose placement for stateful resources (ADR-047 tier boundaries). Verification: each cluster is inspected for stateful pods on CP nodes and for missing `control-plane:NoSchedule` taints during post-bootstrap validation.

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
- **Placement**: CNPG clusters, poolers, and migration jobs run on **worker nodes only** (`nodeSelector: node-role.kubernetes.io/worker: ""`) per the Platform-Wide Placement Rule; never on control-plane nodes.
- **Declarative creation**: Crossplane provider-sql creates databases/users
- **Credentials**: Infisical → ESO → Kubernetes secrets
- **Access control**: Database owner `crossplane_admin`, app users via DefaultPrivileges
- **Applications consume**: External connection strings via ESO secrets
- **✅ BANNED**: Application postInitSQL for database creation

### Redis
- **Platform provides**: StatefulSet in `platform-data` namespace
- **Placement**: worker nodes only per the Platform-Wide Placement Rule; never on control-plane nodes.
- **High availability**: Master-replica configuration
- **Applications consume**: Service endpoint with internal cluster networking
- **Security**: Service mesh mTLS for external access
- **Persistence**: PVC with automated volume snapshots

### ClickHouse
- **Platform provides**: ClickHouseInstallation (Altinity operator) in `platform-data` namespace
- **Placement**: worker nodes only per the Platform-Wide Placement Rule; never on control-plane nodes.
- **Shared cluster**: Database-level isolation per service
- **Enterprise features**: Distributed tables, query quotas, user-level access control
- **Credentials**: Stored in Infisical (System of Record). Lifecycle owned by Hub Operator per ADR-039.
- **Applications consume**: External connection strings with database-specific access
- **Access control**: Each service user granted access only to their database

**Production Readiness Checklist:**
- [ ] HA configuration (≥ 2 replicas) - deferred until staging/production
- [ ] **Placement Rule verified in every cluster**: no stateful pods on control-plane nodes, `nodeSelector: node-role.kubernetes.io/worker: ""` present on all stateful platform workloads, `control-plane:NoSchedule` taint present on every CP node
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

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Database Clusters (physical) | Kubernetes API | CNPG | CNPG | Crossplane, Applications | Day-1+ |
| Stateful Workload Placement (nodeSelector + taints) | Kubernetes API / spoke catalog / hub core services manifests | Platform Engineering | kube-scheduler + ArgoCD | All stateful platform workloads | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive
- Unified backup strategy via CNPG continuous archiving to Hetzner Object Storage (PostgreSQL)
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

## Backup and restore contract

**Vendor and endpoint.** The platform supports a single cloud vendor — Hetzner —
and a single backup store: Hetzner Object Storage, bucket `spoke-pool-backups`,
endpoint `https://hel1.your-objectstorage.com`. This contract applies to every
platform-owned CNPG cluster (`shared-cnpg`) on every spoke, in every topology
and every environment.

**`endpointURL` is mandatory.** Every CNPG manifest must declare
`spec.backup.barmanObjectStore.endpointURL`; without it barman defaults to the
AWS S3 endpoint (`s3.amazonaws.com`) — a misconfiguration class this contract
bans. (The pre-contract `cnpg-cluster.yaml` shipped without it; the ADR-046 §11
codified procedure corrects it.)

**Credentials.** CNPG authenticates to the store with the platform's existing
Hetzner credential: Infisical key `hcloud-token`, delivered to every spoke via
ESO into the `s3-credentials` Secret (keys `access-key-id` /
`secret-access-key`). The signing region is the static literal `hel1` — the
S3 endpoint is region-authoritative and no regional key exists in Infisical.
The hub-operator's Secrets-Manager IAM user is **BANNED** as a barman
credential: the pre-contract manifest used it, and archiving failed on every
WAL from cluster creation (13,948 failures, zero archives; barman attempted
`CreateBucket` on the default S3 endpoint — the incident that produced this
contract).

**Per-spoke isolation.** Each spoke's backups live under
`s3://spoke-pool-backups/<spoke-name>/`, applied by the
`platform-spoke-catalog` ApplicationSet kustomize patch on
`spec.backup.barmanObjectStore.destinationPath`. The shared prefix
(`shared-cnpg/`) is **BANNED**: it cross-contaminates recoveries between
spokes.

**Base backups.** Recovery requires a base backup plus WALs. Each spoke
declares a static `ScheduledBackup` (`cluster.name: shared-cnpg`,
`backupOwnerReference: cluster`, daily schedule, `immediate: true` at
creation); it inherits the Cluster's barman configuration — the CRD carries no
destination fields of its own. Retention: `30d`.

**Ownership.** CNPG owns the physical backup lifecycle (ADR-043); the contract
above is platform-owned and enforced by post-bootstrap validation. ADR-046 §11
holds the isolation mechanics and the cutover procedure that implements this
contract.

## References

- **ADR 003**: ESO-Infisical Integration Pattern
- **ADR-0009**: Zero-Trust Multi-Layer Authentication with JWKS and Service Mesh
- **ADR 034**: Control-Plane Failure Domains (etcd/kube-apiserver blast radius — the basis for the Platform-Wide Placement Rule)
- **ADR 043**: Control-plane authority model — CNPG owns the database physical lifecycle, including backup
- **ADR 046**: Hybrid workload location contract (home vs Hetzner workers; `local-path` vs `hcloud-volumes` storage)
- **ADR 046 §11**: Hybrid topology database model — isolation mechanics and the cutover procedure implementing this contract
- **ADR 047**: Tier boundaries (placement selectors are platform-owned, not fleet-owned)