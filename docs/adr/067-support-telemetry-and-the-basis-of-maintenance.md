# ADR-067: Support Telemetry and the Basis of Maintenance

**Date:** 2026-09-07
**Status:** Proposed

## Context

Maintenance of the control plane is what the platform sells. ADR-065 places that control plane inside the tenant's box and gives the platform no access to it, recording as a consequence that support, incident response and defect reproduction all depend on what a tenant reports or chooses to share.

Three facts make that consequence load-bearing rather than incidental.

**A maintenance proposal is presently unverified against its target.** ADR-064 has the platform raise a pull request advancing a cluster's bundle version. Whether that version is safe for a given cluster depends on that cluster's state — the versions it runs, the resources it holds, whether its reconciliation is currently healthy — and none of it is legible to the platform. The proposal is reasoned from declarations alone.

**The dependency-update analogy stops here.** A dependency bump is checkable in the repository that receives it. A control-plane upgrade is not: correctness is a property of the cluster, not of the declaration, so the mechanism ADR-064 borrows does not bring its verification with it.

**The export mechanism already exists.** ADR-013 establishes Grafana Alloy forwarding metrics and logs by `remote_write` to an endpoint supplied by configuration, with credentials delivered through External Secrets. It runs egress-only against a destination outside the cluster today. What is missing is not a mechanism but a decision about destination, scope and obligation.

## Decision

**A maintenance claim extends exactly as far as the telemetry a tenant exports.**

### Telemetry is exported by the box, never collected from it

Observability for support is egress-only and originates inside the tenant's box. The box sends; the platform receives. No platform component initiates a connection into a tenant's cluster, holds a credential for one, or queries it.

The tenant holds the destination and the credential. Withdrawing them stops the stream and stops nothing running, which is the same property ADR-065 gives to the repository access behind maintenance proposals: what the platform may do is granted by the tenant and revocable by the tenant, and revocation degrades service rather than operation.

### The scope is the control plane, not the tenant's business

What is exported is what a maintenance obligation requires: the health and reconciliation state of the control plane, the ADR-063 bundle version and component versions in effect, drift and policy violations, and the outcome of upgrades. That is the evidence a proposal is reasoned from and the evidence that it worked.

Workload data, application logs, database contents, secret material and end-user identifiers are not exported for support. A tenant's business is not observable to the platform, and a support obligation is not a reason to make it so. Where a tenant wants workload observability, that is a workload under ADR-066 and is the tenant's to run.

### A proposal is gated on evidence from its target

An upgrade proposal is accompanied by a pre-flight verdict produced inside the box, against the cluster the proposal targets, and reported through the same export. A proposal that cannot be verified against its target is raised as unverified and says so.

This closes the gap the dependency-update analogy leaves open. The judgement about whether a bundle is safe for a cluster is made where the cluster is, and only the verdict crosses the boundary.

### What is not observed is not supported

A tenant that exports nothing receives the bundle and the proposals and no undertaking about the outcome on its clusters. This is stated rather than implied, and it is the honest form of the alternative — a maintenance claim over a cluster whose state is unknown is a claim that cannot be met.

Support obligations therefore follow the export, and are graded by it rather than by a tier bought independently of it.

### Alternatives considered

**Grant the platform read access into tenant clusters.** Rejected. It reintroduces standing access into every box, which is the property ADR-065 exists to remove, and it makes the platform a holder of credentials for every tenant's cluster — the concentration that ADR's cross-tenant reasoning rules out.

**Support on tenant-reported information alone.** Rejected as the basis of a paid obligation. It makes every incident begin with a request for evidence, defect reproduction depend on a tenant's diagnostic skill, and an upgrade's outcome unknown to the party that proposed it.

**Export everything the box observes.** Rejected. It places a tenant's application and business data in the platform's custody for no maintenance purpose, and creates a retention and disclosure obligation disproportionate to what support requires.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Control-plane telemetry | tenant's box | Tenant | Collection agent | Platform | Day-1+ |
| Export destination and credential | tenant's box | Tenant | ESO | Collection agent | Day-1+ |
| Pre-flight verdicts | tenant's box | Tenant | Collection agent | Platform | Day-1+ |
| Received telemetry | Platform | Platform | — | Platform | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

The maintenance claim becomes verifiable. The platform proposes an upgrade knowing the state of the cluster receiving it and learns whether it succeeded.

A proposal is gated on evidence from the cluster it targets, so the failure mode where a correct bundle meets an unhealthy cluster is caught before merge rather than after.

The boundary ADR-065 draws survives. Everything crosses outward, under a grant the tenant makes and can withdraw, and no credential into a tenant's cluster exists to be compromised.

What support covers is derivable from what is exported, so the obligation can be stated precisely instead of promised broadly.

### Negative

The platform holds tenant operational data and acquires the retention, access and disclosure obligations that follow, including for tenants in regulated jurisdictions.

Telemetry becomes a dependency of the upgrade path. A broken export presents as an estate that cannot be safely advanced, and the pre-flight runs in the box, so a defect in it is present across the field.

A tenant that exports nothing is supportable only on a reduced basis, which is a commercial conversation at the point of sale rather than a technical setting.

Pre-flight verdicts are produced by the tenant's own control plane, so the platform is acting on evidence it did not gather and cannot independently confirm.

## Impact

- **Extends ADR-013.** Its collection pattern gains a destination outside the box, carrying control-plane telemetry only.
- **Confirms ADR-065.** Telemetry is egress-only under a revocable grant, so the platform gains evidence without gaining access.
- **Constrains ADR-064.** A promotion proposal carries a pre-flight verdict from its target, or is marked unverified.
- **Confirms ADR-066.** Control-plane observability is control plane; workload observability is a workload.

## References

- ADR-013: Hub-Spoke Observability Architecture with Dual Collection Patterns
- ADR-039: Platform Ownership Model
- ADR-063: The Platform Bundle and its Version
- ADR-064: Bundle Promotion and Cell-Scoped Policy
- ADR-065: The Control Plane Ships Into the Box
- ADR-066: The Control Plane Boundary
