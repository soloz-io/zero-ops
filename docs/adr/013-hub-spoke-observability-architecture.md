# ADR 013: Hub-Spoke Observability Architecture with Dual Collection Patterns

**Date:** 2026-05-01  
**Status:** Superseded by ADR-078 (The Observability Capability)  
**Authors:** Platform Engineering Team  

*Constrained by: ADR-066 (The Platform Boundary)*

> **Superseded.** ADR-078 replaces the architecture below. Most of what this ADR
> names was never built: VictoriaMetrics and its alert rules exist in the
> repository but no component deploys them; Loki, the OpenTelemetry Collector
> gateway, the MetricCollector sidecars and the NATS JetStream billing buffer do
> not exist at all. The three telemetry domains this ADR distinguishes, and its
> separation of operational observability from financial metering, survive in
> ADR-078. Read ADR-078 for what ships.

> What the platform observes is bounded by the boundary ADR-066 draws. ADR-067
> records the egress-only telemetry a box exports.

## Context

The Zero-Ops platform operates in a hub-spoke topology. The platform requires observability across three distinct telemetry domains:

1. **Infrastructure Metrics**: Node-level metrics, kubelet stats, Kubernetes resource state
2. **Application Metrics**: Tenant workload metrics, domain-specific metrics from databases
3. **Usage Metering**: Billing events for OpenMeter (API calls, storage operations, resource consumption)

The platform architecture includes:
- Hub cluster with VictoriaMetrics, Loki, OpenMeter, and Headlamp
- Spoke clusters hosting tenant workloads
- AgentGateway emitting OTLP for billing events
- Separation between operational observability and financial metering

### The Core Question

**How should the platform collect and forward telemetry from spoke clusters to the Hub while maintaining tenant isolation and preventing data loss for billing events?**

## Decision

We will implement a **dual collection pattern** using Grafana Alloy DaemonSet for infrastructure metrics and OpenTelemetry Collector Deployment for application/billing telemetry.

### Architectural Components

**Infrastructure Observability:**
- Grafana Alloy deployed as DaemonSet in spoke clusters
- Forwards metrics to VictoriaMetrics (Hub) via remote_write
- Forwards logs to Loki (Hub)

**Application & Billing Telemetry:**
- OpenTelemetry Collector deployed as Deployment (HA, 3 replicas) in spoke clusters
- Receives OTLP from AgentGateway (billing events)
- Receives OTLP from MetricCollector sidecars (domain-specific metrics)
- Forwards to OpenMeter (Hub) via OTLP with mTLS + certificate validation
- Includes NATS JetStream buffer for durability

**Domain-Specific Metrics:**
- MetricCollector deployed as sidecar in tenant PostgREST pods
- Queries tenant databases for stateful metrics (database rows, storage bytes, workspace counts)
- Emits OTLP to local OTel Collector gateway
- Runs continuously as declarative reconciliation loop

### Data Flow

```
Spoke Cluster:
├── DaemonSet: Grafana Alloy → Hub VictoriaMetrics (infra metrics)
├── Deployment: OTel Collector (HA gateway) → Hub OpenMeter (billing)
└── Sidecar: MetricCollector → OTel Collector → Hub OpenMeter (domain metrics)

Hub Cluster:
├── VictoriaMetrics (operational metrics)
├── Loki (logs)
├── OpenMeter (usage metering, billing)
└── Headlamp UI (Prometheus plugin → VictoriaMetrics)
```

### Rationale

1. **Agent Layer for Infrastructure**: Grafana Alloy as DaemonSet collects node-level metrics that must be gathered per-node.

2. **Gateway Layer for Applications**: OTel Collector as Deployment provides centralized aggregation for tenant-scoped metrics.

3. **Sidecar for Domain Metrics**: MetricCollector sidecars enable tenant-specific database queries without requiring per-tenant infrastructure.

4. **Separation of Concerns**: Infrastructure observability (Alloy → VictoriaMetrics) is decoupled from billing metering (OTel → OpenMeter).

5. **Durability Guarantee**: NATS JetStream buffer ensures billing events are not lost during Hub unavailability.

6. **Zero-Trust Security**: All spoke-to-hub communication uses mTLS with certificate-based workload identity validation.

7. **Declarative Pattern**: Continuous sidecars align with ADR-011 (declarative operator state over imperative jobs).

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Observability State | VictoriaMetrics / Loki / OpenMeter | Observability Stack | Alloy / OTel Collector | Operators, SRE | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

1. **Complete Infrastructure Visibility**: Alloy DaemonSet provides node-level metrics
2. **Billing Accuracy**: Durable NATS buffer prevents data loss for billing events
3. **Tenant Isolation**: Metrics tagged with tenant_id, enforced by OpenMeter namespace isolation
4. **Operational Separation**: VictoriaMetrics for ops, OpenMeter for billing
5. **Zero-Trust Compliance**: mTLS + certificate-based workload identity at every hop
6. **Declarative Pattern**: Continuous sidecars align with Kubernetes reconciliation model

### Negative

1. **Complexity**: Three collection patterns (DaemonSet, Deployment, Sidecar) increase operational overhead
2. **Resource Footprint**: Alloy DaemonSet adds per-node overhead
3. **Dual Backends**: VictoriaMetrics and OpenMeter require separate query interfaces

## References

- **ADR 011**: Declarative Operator State over Imperative Jobs
- **ADR 009**: Zero-Trust Networking (Layer 2 SPIRE/Istio decommissioned)
