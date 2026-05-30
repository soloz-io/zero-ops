# ADR 034: Control Plane Failure Domains and Data Plane Autonomy

## Status

Accepted

## Context

The Zero-Ops platform relies on a centralized Hub cluster hosting critical control plane components (Crossplane, ArgoCD, Infisical, NATS, PostgreSQL). A formal definition of survivability, blast radius, and recovery objectives is required to ensure that a Hub outage does not cause cascading failures across the global fleet of Spoke clusters.

## Decision

The platform enforces strict "Data Plane Autonomy." Spoke clusters must continue to serve tenant API traffic, enforce Row-Level Security, and process database transactions indefinitely without connectivity to the Hub.

### Component Failure Semantics

**Hub Cluster Total Failure:**
Platform-driven topology scaling and provisioning halts. Spoke-local autoscaling (HPA, VPA, KEDA), local operator reconciliation, and local CNPG failovers continue uninterrupted. End-user traffic and database operations continue normally.

**Hub PostgreSQL Failure:**
Hub APIs become read-only or completely unavailable. Platform tenant onboarding and provisioning operations halt. Existing Spoke workloads remain unaffected.

**Crossplane Failure:**
Existing infrastructure (CNPG databases, Redis, ClickHouse) running on Spokes remains fully operational. Crossplane `provider-kubernetes` ceases synchronization. Infrastructure mutations, deletions, and new XR provisions queue until Crossplane is restored.

**ArgoCD Failure:**
Tenant workloads (BFF, Frontend, Waypoint) continue running. GitOps synchronizations halt. Tenant CI/CD updates to image digests fail to deploy. Existing deployments cannot scale or roll back via GitOps.

**Infisical Failure:**
Spoke-local External Secrets Operator (ESO) relies on its local cache. Pods referencing already-materialized Kubernetes Secrets continue to schedule and start. Pods requiring fresh runtime credential resolution fail.
*Secret Rotation SLO:* Hub outages under 24 hours result in no expected tenant impact. For Hub outages exceeding the Secret TTL, rotation guarantees no longer apply.

**NATS Failure:**
Cross-cluster telemetry buffering (JetStream) queues locally on Spokes. Remote trigger executions and Hub-to-Spoke orchestration commands fail. Tenant applications function normally.

### Disaster Recovery Objectives

**Hub Control Plane:**
- RTO (Recovery Time Objective): 4 hours
- RPO (Recovery Point Objective): 15 minutes

**Spoke Workloads:**
- Unaffected by Hub outage.

### Hub Dependency Matrix

| Component | Runtime Traffic | Provisioning | GitOps | Secret Rotation |
| :--- | :--- | :--- | :--- | :--- |
| **Hub Down** | ✅ | ❌ | ❌ | ❌ |
| **Hub PostgreSQL Down** | ✅ | ❌ | ✅ | ✅ |
| **Crossplane Down** | ✅ | ❌ | ✅ | ✅ |
| **ArgoCD Down** | ✅ | ✅ | ❌ | ✅ |
| **Infisical Down** | ✅ | ✅ | ✅ | ⚠️ |
| **NATS Down** | ✅ | ⚠️ | ✅ | ✅ |

## Consequences

### Positive
- Data plane survivability is architecturally isolated from Hub failures; a total Hub control plane outage results in zero downtime for tenant end-user traffic.
- Hub disaster recovery operations do not impact active Spoke workloads.
- Incident responders have deterministic guidelines for component outages based on the defined RTO/RPO and dependency matrix.

### Negative
- Platform-level infrastructure mutation, scaling, and credential rotation operations are tightly coupled to Hub availability and pause during outages.
