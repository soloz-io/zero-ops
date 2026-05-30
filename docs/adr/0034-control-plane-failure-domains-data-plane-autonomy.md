# ADR 034: Control Plane Failure Domains and Data Plane Autonomy

## Status

Accepted

## Context

The Zero-Ops platform relies on a centralized Hub cluster hosting critical control plane components (Crossplane, ArgoCD, Infisical, NATS, PostgreSQL). A formal definition of survivability, blast radius, and recovery objectives is required to ensure that a Hub outage does not cause cascading failures across the global fleet of Spoke clusters.

A critical distinction underpins this ADR: **Runtime Autonomy** and **Lifecycle Autonomy** are not the same. Runtime Autonomy (existing traffic, workloads, secrets, and certificates continue functioning) is achievable and guaranteed. Lifecycle Autonomy (secret rotation, certificate issuance, provisioning, GitOps sync continue during Hub outage) is not guaranteed and follows the dependency matrix below. The platform accepts that centralized dependencies (Infisical, ArgoCD, Crossplane) control lifecycle operations while the data plane operates independently at runtime.

## Decision

The platform enforces strict **Runtime Autonomy** as the definition of Data Plane Autonomy. Spoke clusters must continue serving tenant API traffic, enforcing Row-Level Security, and processing database transactions indefinitely without connectivity to the Hub. Lifecycle operations (secret rotation, certificate issuance, provisioning, GitOps sync) may pause during Hub outages — the platform guarantees runtime survival, not lifecycle independence.

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
*Certificate Issuance:* Existing leaf certificates continue serving until expiry. New certificate issuance and renewal require Infisical availability (via infisical-issuer or Intermediate CA distribution).

**NATS Failure:**
Cross-cluster telemetry buffering (JetStream) queues locally on Spokes. Remote trigger executions and Hub-to-Spoke orchestration commands fail. Tenant applications function normally.

### Disaster Recovery Objectives

**Hub Control Plane:**
- RTO (Recovery Time Objective): 4 hours
- RPO (Recovery Point Objective): 15 minutes

**Spoke Workloads:**
- Unaffected by Hub outage.

### Hub Dependency Matrix

| Component | Runtime Traffic | Provisioning | GitOps | Secret Rotation | Certificate Issuance |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Hub Down** | ✅ | ❌ | ❌ | ❌ | ❌ |
| **Hub PostgreSQL Down** | ✅ | ❌ | ✅ | ✅ | ✅ |
| **Crossplane Down** | ✅ | ❌ | ✅ | ✅ | ✅ |
| **ArgoCD Down** | ✅ | ✅ | ❌ | ✅ | ✅ |
| **Infisical Down** | ✅ | ✅ | ✅ | ⚠️ | ❌ |
| **NATS Down** | ✅ | ⚠️ | ✅ | ✅ | ✅ |

## Consequences

### Positive
- Runtime Autonomy is architecturally isolated from Hub failures; a total Hub control plane outage results in zero downtime for tenant end-user traffic.
- Hub disaster recovery operations do not impact active Spoke workloads.
- Incident responders have deterministic guidelines for component outages based on the defined RTO/RPO and dependency matrix.
- Lifecycle operations (secret rotation, certificate issuance, provisioning, GitOps sync) pause during Hub outages — this is explicit and expected, matching industry patterns (OpenShift ESO, Vault, AWS Secrets Manager).

### Negative
- Platform-level infrastructure mutation, scaling, credential rotation, and certificate issuance operations are tightly coupled to Hub availability and pause during outages.
