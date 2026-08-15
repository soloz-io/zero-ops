# ADR-049: Dev Infrastructure Simplification, Managed Telemetry, and Load Balancer Consolidation

**Date:** 2026-08-15
**Status:** Accepted

## Context

The platform runs a hybrid development topology (ADR-046) combining a Hetzner-hosted management plane and control plane with home-lab WSL2 worker nodes. Operating this topology under the standard Day-1 service deployment incurred an unnecessary infrastructure footprint on Hetzner:

1. **Stateful Telemetry Overhead:** Self-hosting VictoriaMetrics (`vmstorage`, `vmselect`, `vminsert`, operator, alerts) consumed 60Gi of block storage across 2 Persistent Volumes along with significant memory and CPU.
2. **Premature Analytics Stack:** Deploying ClickHouse (`platform-clickhouse` with 12Gi across 2 PVs, Altinity operator) and OpenMeter for financial metering was premature for development and hybrid evaluation.
3. **Redundant Public Load Balancers:** The `agentgateway` edge service provisioned a dedicated Hetzner Cloud Load Balancer (`type: LoadBalancer`) in addition to the public `ingress-nginx` Load Balancer.
4. **Overprovisioned Spoke Databases:** The spoke shared database (`shared-cnpg`) ran 3 high-availability instances allocating 300Gi of block storage in development.

## Decision

1. **Adopt Managed Telemetry (Grafana Cloud):**
   - Retire self-hosted VictoriaMetrics cluster, operator, alerts, and dedicated ingress.
   - Retain Grafana Alloy as the sole telemetry collector on Hub and Spoke clusters.
   - Configure Alloy to forward infrastructure metrics directly to Grafana Cloud Metrics (Mimir) via Prometheus remote-write, and application logs to Grafana Cloud Logs (Loki).

2. **Defer ClickHouse and OpenMeter:**
   - Remove `platform-clickhouse` and `clickhouse-operator` from the active deployment pipeline.
   - Defer OpenMeter and its secret provisioning until usage-based billing features are actively integrated.

3. **Consolidate Public Ingress on `ingress-nginx`:**
   - Transition `agentgateway` from `type: LoadBalancer` to `type: ClusterIP`.
   - Route `api.nutgraf.in` traffic exclusively through the existing `ingress-nginx` controller via standard Ingress resources.

4. **Right-Size Development Spoke Databases:**
   - Configure spoke PostgreSQL (`shared-cnpg`) with a single instance (`instances: 1`) for non-production environments.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Telemetry Collector Config | Git (`manifests/hub-core-services/grafana-alloy`) | Platform Engineering | ArgoCD | Grafana Alloy | Day-1+ |
| Telemetry Auth Credentials | Infisical | Platform Security | ESO (`ClusterSecretStore`) | Grafana Alloy | Day-1+ |
| Public HTTP/HTTPS Ingress | Git (`manifests/hub-core-services/ingress`, `api-gateway`) | Platform Networking | Ingress NGINX Controller | Edge Consumers | Day-1+ |
| Spoke Shared Database | Git (`manifests/spoke/spoke-catalog/infra/cnpg-cluster.yaml`) | Platform Engineering | CNPG Operator | Spoke Workloads | Day-1+ |

## Consequences

### Positive

- Eliminates 6 Hetzner Cloud block storage volumes (272Gi total storage reclaimed).
- Eliminates 1 dedicated Hetzner Cloud Load Balancer.
- Removes 2 Kubernetes operators (`victoria-metrics-operator`, `altinity-clickhouse-operator`) and 7+ stateful pods from node compute limits.
- Simplifies observability operations by using a managed Prometheus/Loki-compatible backend with zero storage maintenance.

### Negative

- Telemetry data leaves the cluster boundary to an external managed SaaS provider (Grafana Cloud).
- Spoke PostgreSQL cluster in development operates without high-availability automatic failover.

## Impact

- Amends **ADR-013** (Hub-Spoke Observability Architecture): Replaces Hub VictoriaMetrics cluster with Grafana Alloy remote-writing to Grafana Cloud (Mimir/Loki).
- Amends **ADR-014** (Platform-Owned Stateful Infrastructure): Defers ClickHouse and scales development Spoke CNPG cluster to single instance.

## References

- ADR-013: Hub-Spoke Observability Architecture with Dual Collection Patterns
- ADR-014: Platform-Owned Stateful Infrastructure
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-043: Control Plane Authority Model
- ADR-046: Hybrid Provider Cell (Hetzner Control Plane + Home-Lab Workers)
