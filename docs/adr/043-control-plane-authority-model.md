# ADR-043: Control Plane Authority Model

**Date:** 2026-06-08
**Status:** Accepted

## Context

The platform operates across multiple domains: secrets, certificates, infrastructure, Kubernetes state, database schemas, tenant identity, and Spoke lifecycle. Each domain is governed by a different controller. When two ADRs describe overlapping responsibilities, there is no single tie-breaker document to determine which controller has authority.

Without a governing authority model, the following questions recur during architecture reviews:

- Does the Hub Operator own tenant passwords, or does Kube-SBT?
- Does ArgoCD own XRs, or does Crossplane?
- Does the CLI own bootstrap secrets, or does the Hub Operator?
- Does ESO own secret rotation, or does Infisical?

This ADR defines the single authority for each domain. Where any other ADR appears to conflict with this model, this ADR controls.

## Decision

### Authority Assignment

| Domain | Authority | Rationale |
|---|---|---|
| Secrets | Infisical | Secret lifecycle provenance must be centralized. No Kubernetes component generates or owns secrets independently. |
| Certificates | cert-manager | Certificate lifecycle is delegated to the only component capable of issuance, renewal, and revocation. |
| Infrastructure | Crossplane | Infrastructure provisioning and reconciliation require a composition engine; no other component composes managed resources. |
| Kubernetes State | ArgoCD | Git is the system of record for Kubernetes resource desired state. ArgoCD is the only component that delivers from Git to the API server. |
| Database Schema | Atlas Operator | Schema migrations require DDL lifecycle awareness that neither CNPG nor provider-sql provides. |
| Database Physical Lifecycle | CNPG | High availability, failover, backup, and physical resource management are CNPG's exclusive domain. |
| Database Logical Roles | Crossplane provider-sql | Role lifecycle (CREATE, ALTER, GRANT, DROP) is infrastructure-level state reconciled declaratively. |
| Tenant Identity | Kube-SBT | Tenant-to-database credential mapping, Ory API abstraction, and tenant onboarding are Kube-SBT's exclusive domain. |
| Spoke Lifecycle | Hub Operator | Spoke provisioning, teardown, and cross-cluster orchestration require Hub-level visibility that Spoke-local controllers lack. |
| PKI Trust Anchors | CLI | Root CA and Intermediate CA establishment is a Day-0 operation with no Day-1 reconciliation requirement. |
| Machine Identities | Hub Operator | Machine identity lifecycle (creation, scope, rotation) is a Spoke-level concern managed at the Hub. |
| Observability | Observability Stack | Metrics, logs, and traces are collected by Alloy/OTel and stored in VictoriaMetrics/Loki/OpenMeter. No application controller owns telemetry. |
| Admission Policy | Kyverno | Kubernetes admission control requires a policy engine integrated with the API server. |
| Workload Identity | cert-manager | Workload identity is provided by cert-manager-issued certificates. SPIRE was decommissioned (2026-08-13); SPIFFE-based workload identity is no longer used in the platform. |
| Billing Catalog | Billing Operator | Pricing configuration carries legal and compliance weight requiring Git audit trail and declarative reconciliation. |
| Network Policy | CNI (Cilium) | Network policy enforcement is the CNI's exclusive domain. |

### Authority Rules

1. **Exactly one authority exists per domain.** No domain may have co-equal authorities.
2. **An authority may delegate operations but not ownership.** For example, cert-manager delegates certificate issuance to infisical-issuer, but cert-manager remains the authority for certificate lifecycle.
3. **Cross-domain conflicts are resolved by the authority of the resource being modified, not the resource performing the modification.** If the Hub Operator attempts to modify a certificate, cert-manager's authority controls — the Hub Operator's ADR does not.
4. **A controller acting outside its authorized domain produces a platform architecture violation, regardless of whether the operation succeeds technically.**
5. **The authority assignments in this ADR supersede any per-component permission statements in earlier ADRs that contradict them.**

## Consequences

### Positive

- Cross-domain authority questions are resolved by consulting a single document.
- Architecture review can reject designs with "this violates ADR-043" rather than debating controller scope.
- Delegation chains are explicit — an authority may delegate but remains accountable.
- New domains can be added with a single row in this table, immediately subject to existing authority rules.

### Negative

- Authority is centralized; if a domain authority is unavailable, no other component may fill the gap (e.g., if cert-manager is down, no other component may issue a certificate).
- Delegation requires explicit documentation in the delegating authority's ADR.
- If two domains genuinely require shared authority, this ADR must be amended — there is no concept of joint ownership.
