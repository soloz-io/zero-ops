# ADR-047: Fleet Tenant Deployment Contract

**Date:** 2026-08-13
**Status:** Accepted

*Amended by: ADR-062 (Onboarding and Scaffolding a Tenant), ADR-073 (Workload Delivery is Version Pinning), ADR-074 (Withdrawing Atlas)*

> `fleetId` is `tenantId` throughout (ADR-062). The rejection of fleets authoring
> external workload repositories stands, but on new ground: ADR-073 replaces
> cross-tenant containment with the argument that a published version already
> identifies the content, and withdraws `workloads.gitRepo`, `gitPath` and
> `gitRevision` from the fleet values schema. ADR-074 withdraws the `migrations`
> block with them.

## Context

- ADR-004 establishes a dual-repository contract: platform code lives in zero-ops; tenant runtime state lives in fleet-registry, which is the ArgoCD source of truth for tenant workloads.
- ADR-021 separates deployment into independent ArgoCD application boundaries with no sync-wave orchestration for workloads. ADR-007 defines the fleet-registry spoke flow: the universal tenant chart materializes a tenant's AINativeSaaS XRD, which composes a TenantDatabase XRD reconciled on the spoke.
- ADR-031 defines tenant secret isolation: the cell machine identity has broad tenant-path access, therefore isolation depends on tenants being denied the ability to author ExternalSecrets and RBAC. ADR-014 assigns stateful infrastructure to the platform; applications consume it. ADR-020 governs migrations as forward-only with expand → migrate → contract and a minimum 24-hour compatibility window. ADR-027 separates fleet workload configuration (fleet-registry values) from shared bases (zero-ops).
- ADR-019 is the authoritative definition of Waypoint's shared-service topology and is unaffected by this ADR.
- fleet-registry/README.md is the canonical definition of the fleet concept: a fleet is a tenant-owned logical product/deployment boundary, decoupled from Kubernetes and GitOps implementation details.
- Observed platform state: the three tenant provisioning ApplicationSets that reconciled the tenant lifecycle resources were removed when the Day-0 bootstrap was replaced by environment-manager; nothing currently reconciles those resources. This is implementation state, not the architectural decision.
- Fleets source manifests in fleet repositories, while ArgoCD reconciles rendered state in fleet-registry; without a generator, the two diverge permanently.

## Decision

### Fleet Structure

A fleet is a logical product/deployment boundary: the collective set of workloads and supporting resources belonging to one tenant, reconciled through the tenant's workload Application across its assigned spoke/environment. The tenant namespace is its primary Kubernetes isolation boundary; the platform's tenant provisioning ApplicationSets provide the reconciliation mechanism (see fleet-registry/README.md). For the current deployment model, a fleet is realized through three tenant lifecycle resources: `{fleetId}-xr`, `{fleetId}-spoke`, and `tenant-{fleetId}-workloads`.

### Tenant Provisioning ApplicationSets

The platform SHALL provide the tenant provisioning ApplicationSets required to reconcile the three tenant lifecycle resources according to ADR-021 boundary separation and ADR-007 fleet flow. This decision is stated in terms of the resources that must be reconciled, not the specific ApplicationSet implementation.

### Three-Tier Ownership Boundary

| Tier | Scope | Owns |
|---|---|---|
| Tier 1 | Platform / cluster | spoke catalog: CRDs, controllers, cluster RBAC, cluster-wide network policies |
| Tier 2 | Platform / namespace | `{fleetId}-spoke` rendering platform-authored fleet RBAC and ExternalSecrets |
| Tier 3 | Fleet / workload | `tenant-workloads`: Deployments, Rollouts, Jobs, Services, ConfigMaps, ServiceAccounts, Ingress, NetworkPolicies |

The governing principle: **a fleet consumes platform capabilities but cannot author platform security or control-plane capabilities.** Consequently, `tenant-workloads` may not author Role, RoleBinding, ExternalSecret, Secret, PVC, StatefulSet, CRDs, or cluster-scoped resources. Fleet-created ServiceAccounts are permitted, but they cannot create or modify RBAC bindings; effective elevated permissions are exclusively granted through platform-owned RBAC.

### Secrets and Machine Identity

ADR-031 is preserved: tenants remain denied ExternalSecret authoring. The platform renders ExternalSecrets through `{fleetId}-spoke` against the tenant secret store, and the Infisical machine identity remains platform-managed.

### Data Plane

Tenant databases are provisioned through the TenantDatabase abstraction. Fleets consume connection state through the platform-provided pooler secret; they never provision stateful infrastructure (ADR-014).

### Migrations

Fleet migrations are forward-only per ADR-020, use the expand → migrate → contract pattern with a minimum 24-hour compatibility window, and are gated by a hard PreSync gate. PreSync failure blocks the rollout while existing pods continue serving; application rollback is performed by reverting the image reference.

### State Rule

Transient state is owned by the fleet; persistent state is owned by the platform (ADR-014).

### Sandbox Workloads

Sandbox-capable fleets consume a platform-owned shared sandbox service through its namespaced API only. The sandbox network policy is platform-owned, cluster-wide, and default-deny.

### Certificates

Fleets may declare `Certificate` resources; they may never author Issuers. Issuance flows exclusively through the platform issuer per ADR-035.

### Drift Control

fleet-registry remains the ArgoCD source of truth. Structural and environment synchronization is performed by a fleet CI generator. The generator MUST be deterministic and MUST fail CI if generated output differs from the committed fleet-registry state.

### Alternatives Considered

- Opening the `tenant-workloads` whitelist to ExternalSecret and RBAC authoring — rejected; it would invalidate ADR-031 isolation.
- Fleets authoring external workload repositories (BYOWR) — rejected; it contradicts the dual-repository contract (ADR-004) and workload separation (ADR-021/027).
- Fleets authoring cluster-scoped resources — rejected; `clusterResourceWhitelist` remains empty.

## Ownership

This ADR defines architectural constraints and does not own platform resources. For resource ownership, see ADR-039 (Platform Ownership Model); Kubernetes resources are reconciled by ArgoCD from the Git System of Record.

## Consequences

### Positive

- A single durable contract governs every fleet, independent of fleet topology.
- The trust boundary is explicit: fleets consume capabilities but cannot author platform security capabilities.
- ADR-019 and ADR-031 are preserved rather than weakened.
- Drift between fleet source and rendered ArgoCD state is eliminated deterministically.

### Negative

- Fleet capabilities are bounded by the platform's provisioning surface; novel resource types require platform extension.
- Sandbox and migration facilities are platform services, adding platform-side review before a fleet can adopt them.

## Impact

- ADR-019 remains authoritative for Waypoint's shared-service topology; it is neither amended nor superseded. Waypoint-specific deployment details are implementation.
- ADR-031 is preserved and operationalized; no amendment required.
- Implementation remediation task (not an architectural decision): restore the tenant provisioning ApplicationSet manifest that was removed during the bootstrap transition so the three tenant lifecycle resources are reconciled again.

## References

- fleet-registry/README.md: fleet-registry for multi tenant (canonical fleet definition)
- ADR-004: Dual-Repository GitOps Pattern
- ADR-007: Fleet Registry Spoke Flow
- ADR-014: Platform-Owned Stateful Infrastructure
- ADR-019: Waypoint Shared SaaS Platform Service
- ADR-020: Enterprise Migration Governance with Atlas
- ADR-021: Boundary-Driven GitOps and Day-0 Choreography
- ADR-027: GitOps Workload Separation and Remote Bases
- ADR-031: Tenant Secret Isolation Topology
- ADR-035: Enterprise PKI and Delegated Trust via Infisical OSS
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-041: Controller Responsibility Matrix
- ADR-043: Control Plane Authority Model

## Addendum (2026-08-23): no tenant identifiers in platform code

This ADR and ADR-004 place tenant runtime state in fleet-registry and have the
platform render tenant resources parameterised by fleet — `{fleetId}-spoke`, never a
literal name. That direction was never stated as a rule the platform itself must
obey, and the platform has drifted across it.

### The rule

**A platform manifest or binary SHALL NOT contain a tenant or fleet identifier.**
Tenant-specific OAuth clients, hostnames, secrets and ExternalSecrets are created
during onboarding, from fleet-registry, keyed by `fleetId`. The platform provides
the capability; it does not know who consumes it.

"Platform" here means `manifests/hub-core-services/`, `manifests/argocd/`,
`internal/`, `cmd/` and `operators/`. Tenant material belongs under
`manifests/tenants/` and the fleet provisioning ApplicationSets.

### What drift looks like

`waypoint` is currently named in nine platform files, including two Go sources:

```
internal/auth-proxy/hydra.go        ClientID "waypoint-public-client",
                                    "waypoint-bff-client", and their redirect URIs
internal/auth-proxy/config.go       WaypointBFFClientSecret / WAYPOINT_BFF_CLIENT_SECRET
hub-core-services/identity/auth-proxy/{deployment,kustomization}.yaml
hub-core-services/identity/auth-proxy/waypoint-bff-client-secret-es.yaml
hub-core-services/identity/hydra-maester/oauth2clients.yaml
hub-core-services/api-gateway/agentgateway-config.yaml
hub-core-services/identity/ory-kratos/values.yaml
```

Onboarding a second tenant would require a second env var and a code change. That
is the clearest evidence this is drift rather than design.

### Why it is not merely untidy

It produces a dependency the platform cannot satisfy. `waypoint-bff-client-secret`
is an ExternalSecret in `platform-edge` that resolves against a key only produced
once that tenant's OAuth client is registered. On a fresh hub no tenant has
onboarded, so it can never resolve: it was the single ExternalSecret still failing
at 21/22 after every other secret converged, and it fails on every rebuild.

It had been diagnosed repeatedly as a seeding gap — should the key be pre-seeded,
or should hydra-maester mint it? Both answers are wrong. The dependency should not
exist at platform bootstrap, because the tenant does not exist yet.

### Enforcement

`scripts/validate/preflight/99-tenant-identifiers.sh` fails when a tenant
identifier appears in a platform path outside the recorded baseline. The nine files
above are the baseline: they warn rather than fail, so the rule is enforceable
today and the debt stays visible, while any NEW leak — a tenth file, or a second
tenant hardcoded the same way — fails before a cluster is built.

### Remediation (not yet done)

1. `RegisterClient` takes a client spec rather than compiled-in IDs and redirect URIs.
2. `oauth2clients.yaml` moves to the tenant provisioning path, templated by `fleetId`.
3. `waypoint-bff-client-secret-es.yaml` becomes a per-fleet ExternalSecret rendered
   at onboarding against the tenant secret store — the shape this ADR already
   specifies.
4. `agentgateway-config.yaml` and `ory-kratos/values.yaml` lose hardcoded hostnames.

The baseline shrinks as these land; when it is empty the entries are removed and
the check becomes unconditional.

