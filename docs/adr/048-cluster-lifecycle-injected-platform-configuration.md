# ADR-048: Cluster Lifecycle-Injected Platform Configuration

**Date:** 2026-08-15
**Status:** Accepted

## Context

The spoke ClusterIssuer (`infisical-issuer.infisical.com/v1alpha1`) requires `clientId` and `projectId` **inline** in its spec (infisical-issuer-crd.yaml:78-80, 96-98). Only the client secret is referenceable via `secretRef`; the identity values cannot be consumed from a separate object.

The spoke catalog's shared `cluster-issuer.yaml` therefore cannot carry per-spoke values:

- `projectId` is fleet-constant (the `hub-platform` project UUID shared by all spokes).
- `clientId` is per-spoke and only known at runtime — the spoke machine identity is created by `spoke-identity-operator` during Day-1, after the Crossplane SpokePool composition has already rendered the per-spoke `ClusterResourceSet` at Day-0. The composition therefore cannot know `clientId` at composition time.

This is the direct cause of the observed gap: the spoke ClusterIssuer is deployed with empty `clientId`/`projectId`, leaving cert issuance broken until silently patched.

Candidate fixes were considered and rejected:

1. **Hardcoding per-spoke `clientId` in the shared catalog** — propagates per-spoke values through shared Git manifests owned by every environment. Rejected.
2. **Imperative live patches** (a bespoke cert-operator mutating ClusterIssuer fields) — recreates the coupling that caused this gap; a controller mutating GitOps-owned resources. Rejected.
3. **Expanding `spoke-identity-operator` into a general-purpose PKI / configuration controller** — couples the identity domain to PKI; identity operators expose identity, they do not own its consumers. Rejected.
4. **Operators writing generated Git artifacts per spoke** (ADR-045 evolution) — requires Git write access from a runtime controller and inflates Git history; ADR-045 is a Day-0 CLI pattern, not a Day-1 controller pattern. Rejected as the primary mechanism.

## Decision

> **Cluster lifecycle owns injection of cluster-specific platform configuration. Identity operators expose identity; they do not mutate consumers of that identity.**

### Ownership Contract

| Concern | Owner |
|---|---|
| Spoke identity (machine identity lifecycle, rotation, revocation) | spoke-identity-operator |
| Spoke-specific bootstrap data (clientId, projectId delivery) | CAPI ClusterResourceSet / add-on lifecycle |
| ClusterIssuer definition (CRD, issuer controller, platform template) | Platform GitOps (ArgoCD) |
| Infisical credential Secret (`infisical-auth`) | ESO / platform identity (delivered via CRS wrapper) |
| Certificate issuance | cert-manager + Infisical issuer |
| Tenant certificates | Tenant workload manifests |

### Delivery Chain

```
spoke-identity-operator → machine identity + credentials (clientId, clientSecret)
CRS / add-on lifecycle      → completed ClusterIssuer (clientId, projectId injected)
ArgoCD                      → platform controllers / static manifests
cert-manager + Infisical issuer → certificate lifecycle
```

### Two-Stage Lifecycle

The composition step cannot know `clientId` (Day-0 precedes identity creation). Bootstrapping is therefore a two-stage lifecycle:

1. **Identity stage:** `spoke-identity-operator` provisions the spoke machine identity and exposes the identity material (clientId, clientSecret) — today through the `{spoke}-machine-identity` CRS wrapper Secret (spokemachineidentity_controller.go:266-345) carrying `infisical-auth` into the spoke.
2. **Configuration stage:** the cluster bootstrap / add-on lifecycle reconciles the completed ClusterIssuer once the identity material exists, injecting `clientId` (from the lifecycle's authoritative machine-identity output) and `projectId` (fleet-constant) into the platform template and delivering it to the spoke.

The cluster lifecycle MUST guarantee eventual rendering after the identity stage becomes Ready. CRS `ApplyOnce` alone is insufficient if the initial CRS payload was rendered before `clientId` existed — it would recreate the same race this ADR corrects. The second-stage mechanism must either render the payload only once identity material exists, or re-render when the identity stage transitions to Ready.

The second stage MAY be realized by the existing ClusterResourceSet / add-on machinery. If that machinery cannot express second-stage rendering after identity creation, the platform SHALL add a **small generic cluster-bootstrap / configuration controller** that watches identity material and renders completed bootstrap resources into the CRS payload. That controller must be generic — it may inject any cluster-specific platform configuration, not just the Infisical issuer — and must NOT be a bespoke cert-operator nor an extension of the identity operator.

### Implementation Note (2026-08-16): spoke-identity-operator renders the CRS wrapper

The initial implementation of the configuration stage is realized **in `spoke-identity-operator`**'s `ensureClusterIssuerCRSWrapper` (`spokemachineidentity_controller.go`), which renders the completed ClusterIssuer into the `{spoke}-cluster-issuer` CRS wrapper Secret. This is the minimum-vanilla realization that satisfies every constraint in this ADR:

- The identity operator **exposes identity material** (`clientId` from `SpokeMachineIdentity.status.clientID`, which is rotation-stable) and **generates a lifecycle-delivered rendering** — it does **NOT** watch, patch, or reconcile the spoke's `infisical-fleet-issuer` ClusterIssuer. The "generates, never mutates" contract is explicit and unit-tested.
- Delivery is unchanged: CAPI `ClusterResourceSet` (`spoke-pool-hybrid-dev-01-bootstrap`) gains a new `{spoke}-cluster-issuer` resource whose wrapper Secret is registered in both SpokePool compositions.
- No Git mutation, no per-spoke values committed to shared Git, no imperative post-hoc patches, no controller mutating GitOps-owned resources.
- Fail-closed: while `clientId` or `projectId` is empty no wrapper is published (`ClusterIssuerConfigured` condition documents the deferral), so a half-rendered ClusterIssuer can never reach a spoke via `ApplyOnce`.

### Ownership Boundary (CRS-delivered resources vs ArgoCD)

> Resources delivered by ClusterResourceSet lifecycle injection MUST NOT simultaneously be tracked and pruned by the spoke platform ArgoCD application.

The spoke catalog previously owned a static `ClusterIssuer` placeholder (`cluster-issuer.yaml`) with empty `clientId`/`projectId`. With automated sync + `prune: true; selfHeal: true` on `platform-spoke-catalog-*`, keeping that placeholder in the catalog while CRS delivers the completed issuer would put ArgoCD and the ClusterResourceSet in a prune/recreate race, and any ArgoCD sync would revert the injected values. The placeholder is therefore **removed from the spoke catalog** (`spoke-catalog/infra/kustomization.yaml`); the ClusterIssuer object is delivered exclusively via the CRS lifecycle and must not carry the `argocd.argoproj.io/instance` tracking label. The shared `cluster-issuer.yaml` remains in the catalog directory purely as a reference document and is referenced by neither the kustomization nor any application set.

### Constraints

1. No per-spoke values are committed to shared Git manifests.
2. `projectId` (fleet-constant) belongs to the platform template; `clientId` comes only from the lifecycle's authoritative machine-identity output, never from a shared manifest.
3. The identity operator exposes identity material and is never the reconciler of ClusterIssuer or other PKI resources.
4. The injected ClusterIssuer is delivered via the cluster lifecycle delivery path (CRS / add-on), not via imperative post-hoc patching of a GitOps-owned object.
5. `clientId` is rotation-stable (only the client secret rotates), so a CRS-injected clientId remains valid across secret rotation.
6. CRS `ApplyOnce` semantics apply to the injected payload; identity rotation keeps the wrapper material fresh for future (re)provisioning.

### Interaction with ADR-045

ADR-045 covers Day-0 bootstrap-generated GitOps artifacts written by the CLI (e.g., the hub's static `infisical-fleet-issuer-patch.yaml`). This ADR complements it: spokes whose values are per-cluster and runtime-known are injected via the cluster lifecycle rather than committed generated patches. Hub-static artifacts remain under ADR-045. No ADR is superseded.

## Ownership

This ADR defines architectural constraints. For the resource ownership matrix, see ADR-039 (Platform Ownership Model). New rows for spoke platform configuration and the ClusterIssuer delivery are registered in ADR-039.

## Consequences

### Positive

- Separation of concerns: identity operators own identity, lifecycle owns injection, GitOps owns static manifests, PKI owns certificates.
- No controller mutates GitOps-owned resources.
- No per-spoke values committed to shared Git.
- No Git-writing operator.
- Deterministic Day-0/Day-1 cluster provisioning.
- The injection mechanism is generic and reusable for any future cluster-specific platform configuration, not just the Infisical issuer.

### Negative

- Requires a two-stage lifecycle; the second-stage rendering point may require a new generic controller if the current add-on machinery cannot express it.
- The injected ClusterIssuer is lifecycle-delivered rather than plainly visible in the spoke catalog, increasing the need for clear operational documentation.

### Amendment (2026-08-18): CRS `Reconcile` semantics and delivery assurance

Constraint 6 above needs sharpening after the ADR-046 §12 incident: `ApplyOnce`
applies strictly at provision time, and the spoke CRS strategy `Reconcile`
re-applies payloads **only when the payload/binding hash changes** — neither
performs live-state drift repair. Concretely:

- A CRS-delivered resource deleted after a successful apply (e.g., by an
  ArgoCD namespace-cascade prune) **stays deleted** until the payload hash
  changes. The binding's `applied: true` does not mean the live resources
  still exist.
- CRS is therefore **creation/delivery assurance, not steady-state
  enforcement**. Steady-state enforcement for lifecycle-delivered
  configuration is out of scope by design (constraint: CRS-delivered
  resources are not ArgoCD-tracked); operators rely on the SMI rotation
  procedure (ADR-046 §12) to re-render and re-deliver.
- Health attestation for lifecycle-delivered configuration should be
  *readiness-based* (e.g., a condition on the spoke) rather than assumed
  from a binding status. Tracked as future work: expose a
  CRS-delivery-complete condition via spoke-identity-operator.

## Impact

- ADR-039 (Platform Ownership Model): register spoke platform configuration / ClusterIssuer delivery ownership rows.
- ADR-035 (Enterprise PKI) and ADR-045 (Bootstrap-Generated GitOps Artifacts): unchanged; this ADR complements rather than supersedes them.
- Manifest remediation: the shared `cluster-issuer.yaml` placeholder comments referencing "cert-operator CRS" injection (`manifests/spoke/spoke-catalog/infra/cluster-issuer.yaml`) must be updated to reference this ADR and the two-stage lifecycle, together with the delivery mechanism chosen in the configuration stage. **Done 2026-08-16:** placeholder removed from the catalog kustomization; header updated to document the CRS lifecycle delivery and the ArgoCD ownership boundary.

## References

- ADR-035: Enterprise PKI and Delegated Trust via Infisical OSS
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-042: Bootstrap State Machine
- ADR-045: Bootstrap-Generated GitOps Artifacts