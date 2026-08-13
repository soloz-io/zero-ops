# ADR 036: Pluggable Infrastructure Provider Architecture

## Status
Accepted

> **§3 superseded by [ADR-046](./046-hybrid-provider-home-worker.md).** The CAPD local-development model (decision 3) was removed with the CAPD/local provider in WS0. The hybrid provider cell (Hetzner CP + home-lab WSL2 workers) replaces local development on real hardware.

## Context
The platform's infrastructure provisioning logic is tightly coupled to Hetzner Cloud. Elements such as Cluster API (CAPI) `ClusterClass` definitions, Crossplane Compositions, Hub CLI preflight checks, and Day-0 bootstrap secrets explicitly expect Hetzner environments. This tight coupling prevents local development, limits future multi-cloud expansion, and makes continuous integration testing expensive and slow.

## Decision
We will decouple cloud-specific implementations from the core platform using a pluggable provider architecture across all control plane layers:

1. **Hub CLI Strategy Pattern:** The `cmd/hub` bootstrap sequence will use a Provider Interface. This interface will dictate which preflight validators to run, which Secret Zero generation logic to execute, and which CAPI infrastructure providers (e.g., `infrastructure-hetzner` vs `infrastructure-docker`) to initialize.

2. **Strict Provider Isolation:** Core platform services SHALL NOT contain provider-specific conditionals or provider-specific resource definitions. All cloud-specific behavior SHALL be implemented behind provider interfaces and provider-owned manifests. This boundary is enforceable at code review: any import of a cloud-specific package into core orchestration logic is a violation.

3. **Local Development via CAPD:** Cluster API Provider Docker (CAPD) is the official local development and integration testing provider. CAPD SHALL NOT be considered a production-supported provider. Local Hub clusters running in `kind` will provision Spoke clusters as isolated Docker networks, ensuring the local development loop mirrors exact production CAPI lifecycle semantics for validation purposes while accepting that CAPD lacks production-grade networking, storage, and availability guarantees.

4. **Dynamic Crossplane Routing:** We will deprecate hardcoded provider parameters in the `SpokePool` API specification. Instead, `SpokePool` claims will use Crossplane Composition Selectors (label matching) to dynamically route provisioning requests to provider-specific Compositions (e.g., Hetzner vs. CAPD).

5. **Provider Capability Contract:** Providers SHALL declare supported platform capabilities through a capability contract used by validation and admission workflows. Capability contracts SHALL be versioned and backwards compatible across provider releases to prevent admission logic breakage during provider updates. This prevents silent failures when a tenant requests a feature (such as GPUs, load balancers, or block storage) that not all clouds support.

6. **Provider Kustomize Components:** Cloud-specific Kubernetes manifests (such as Cloud Controller Managers, CSI drivers, and external-dns webhooks) will be extracted from core platform boundaries and isolated into dedicated Kustomize component directories. These components will be injected dynamically based on the target environment.

7. **Composition Reuse Mandate:** Provider implementations SHALL maximize reuse through shared Composition Functions, reusable patch sets, and common ClusterClass templates. This prevents composition explosion as the number of supported providers grows.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Infrastructure XRs | Kubernetes API | Crossplane | Crossplane | Spokes, Tenants | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences
### Positive
- Enables zero-cost, end-to-end testing of the entire Hub-Spoke architecture on local development machines using Docker.
- Core platform boundaries and GitOps choreography remain completely cloud-agnostic.
- Accelerates onboarding of new cloud providers without refactoring core platform logic.
- Provider capability contracts prevent silent cross-cloud failures at admission time.
- Reusable composition patterns limit maintenance overhead as the provider portfolio expands.

### Negative
- Increases the total number of Crossplane Compositions, ClusterClasses, and Kustomize overlays the platform team must maintain, mitigated by the composition reuse mandate.
- Provider capability contracts require upfront investment in capability modelling and admission integration.
