# ADR 033: Fleet Scale Targets & SLOs

## Status

Accepted

## Context

The platform lacks explicit scalability boundaries and performance targets, which risks control plane degradation, API server exhaustion, and undefined blast radii as the fleet expands.

## Decision

A single Hub control plane architecture is strictly bounded to the following scale limits and SLOs. Reaching 80% of any capacity limit triggers horizontal Hub sharding.

**Capacity Limits per Hub:**
- Maximum Spoke Clusters: 500
- Maximum Platform Tenants: 10,000
- Maximum Crossplane XRs: 25,000
- Maximum ArgoCD Applications: 15,000

**Performance SLOs (P99):**
- Crossplane XR Reconciliation Latency: < 60 seconds
- GitOps Sync-to-Ready (Infrastructure): < 5 minutes
- Hub API Query Latency: < 200ms

**Sharding Strategy:**
Scaling beyond these limits requires deploying a new Hub cluster (e.g., `hub-us-east-2`). Tenants and Spokes are pinned to a specific Hub at provisioning time. Cross-Hub migration is not supported as a self-service operation. Platform Engineering may execute controlled migration procedures during disaster recovery or fleet rebalancing events.

## Consequences

### Positive
- Defines explicit blast radius boundaries per Hub.
- Prevents control plane degradation through early sharding signals.

### Negative
- Cross-Hub migration is not self-service; Platform Engineering may execute controlled migrations during DR or rebalancing.
