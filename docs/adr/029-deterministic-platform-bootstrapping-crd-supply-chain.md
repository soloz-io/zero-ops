# ADR-029: Deterministic Platform Bootstrapping and CRD Supply Chain Security

## Status
Accepted (revised)

## Governing Principle

> Build-time acquisition. Git-time verification. Deploy-time zero external dependency. Runtime semantic readiness.

## Context

During the orchestration of Spoke clusters, we encountered severe reconciliation deadlocks and admission controller failures. Specifically, Kyverno validation policies rejected tenant workloads because the `Rollout` Custom Resource Definition (CRD) was fetched via a live GitHub URL during Kustomize rendering and had not reached an `Established` state in the Kubernetes API before the policies were applied.

Relying on raw external URLs during continuous reconciliation introduces a critical supply-chain vulnerability, breaks air-gapped compatibility, and prevents deterministic disaster recovery. Furthermore, relying purely on ArgoCD `sync-waves` within a single monolithic Application does not guarantee semantic API readiness.

## Decision

To achieve enterprise-grade determinism and supply-chain integrity, we mandate the following patterns for all foundational platform infrastructure:

### 1. Strict CRD Vendoring with Provenance

No infrastructure Kustomization or Helm chart may fetch CRDs from live remote URLs (e.g., `raw.githubusercontent.com`) during reconciliation. All third-party CRDs MUST be downloaded, verified, and vendored into the Git repository.

The source URL, upstream version or tag, and SHA-256 digest MUST be recorded in the vendored artifact's provenance header. Where upstream signatures or attestations are available, they SHOULD be verified before the artifact is admitted into the repository.

Updates MUST occur through reviewed pull requests and MUST NOT occur during cluster reconciliation.

> Vendoring gives reproducibility. Verification gives supply-chain integrity.

### 2. Immutable Vendor with Deterministic Local Overlays

Vendored third-party manifests SHOULD remain byte-for-byte identical to their verified upstream artifacts whenever practical. Platform-specific metadata (such as ArgoCD sync-wave annotations) SHOULD be applied through small, explicit local Kustomize overlays using the `patches` field.

Patches MUST be local to the repository and SHOULD be narrowly targeted. This preserves the ability to diff vendored artifacts against upstream for integrity verification while allowing platform-specific configuration.

This is distinct from remote dynamic inputs: a local Kustomize patch committed to Git is deterministic and does not introduce runtime dynamism.

### 3. Semantic Readiness Gating

Sync waves MUST be used for ordering but MUST NOT be relied upon as the sole readiness mechanism. ArgoCD sync waves determine reconciliation order with a configurable inter-wave delay; they do not encode elapsed readiness time. Wave number `5` does not mean "five units of readiness" — it means "reconcile after waves -5, 0, 1, 2, 3, 4."

Before resources depending on a CRD are reconciled, bootstrap automation MUST verify that the CRD has reached:

```
status.conditions[type=Established].status=True
```

The following readiness chain MUST be satisfied sequentially:

```
CRD registered
  → CRD Established / API served
    → Operator/controller ready
      → Admission controller webhooks ready
        → Policies installed and active
          → Workload reconciliation
```

Each transition MUST be gated by explicit health conditions, not by wave number or elapsed time.

### 4. Sync Wave Ordering Convention

The following sync waves establish deterministic ordering within ArgoCD Applications:

| Layer | Wave | Description |
|-------|------|-------------|
| CRDs | -5 | Foundational API extensions |
| Operators/controllers | 1 | Controllers that own the CRDs |
| Admission/validation policies | 5+ | Kyverno, OPA, or equivalent policy engines |
| Dependent workloads | 10+ | Platform services and tenant prerequisites |

Wave separation establishes ordering only. Semantic readiness MUST be established independently through resource health conditions or explicit readiness gates.

### 5. Decoupled Bootstrap Applications

Foundational API extensions and their controllers SHALL be managed by a dedicated `platform-bootstrap` ArgoCD Application, entirely decoupled from the `platform-infrastructure` Application.

```
platform-bootstrap
├── CRDs (vendored, sync-wave -5)
├── Operators/controllers (sync-wave 1)
├── Admission controllers
└── Foundational controllers

platform-infrastructure
├── ClusterPolicies
├── Platform configuration
├── Shared services
└── Tenant prerequisites
```

CRDs, operators, and admission controllers have a different upgrade cadence, blast radius, ownership model, rollback strategy, and privilege level from normal platform configuration. Splitting them into a dedicated Application creates a meaningful lifecycle boundary.

The bootstrap Application's health MUST gate downstream Application reconciliation. Simply creating two Applications does not automatically create a dependency contract — bootstrap readiness must be explicitly verified before platform-infrastructure is synchronized.

## Ownership

This ADR defines CRD supply chain security and bootstrapping conventions. For resource ownership, see ADR-039.

## Consequences

### Positive

- Disaster recovery becomes deterministic with respect to CRD source artifacts and no longer depends on upstream source availability during reconciliation.
- Air-gapped deployments are natively supported for foundational infrastructure.
- Zero downtime or blocked queues caused by GitHub rate-limiting, DNS failures, or upstream outages during reconciliation.
- Supply-chain integrity is verifiable through provenance headers and artifact hashing.
- Lifecycle boundaries between bootstrap and infrastructure enable independent upgrade cadences and blast-radius isolation.

### Negative

- Vendored CRDs increase repository footprint.
- Dependency updates require an explicit upgrade process: provenance verification, rendered-manifest validation, policy checks, and platform integration testing before merge.

### Mitigations

- Dependency automation SHOULD detect new upstream versions and open pull requests with provenance metadata pre-populated.
- CI validation SHOULD verify that vendored artifacts match their claimed SHA-256 digest and render correctly through Kustomize.
- Re-vendoring follows the same pull-request workflow as any other platform change — no special access or out-of-band steps.

## Lifecycle

```
                  SUPPLY CHAIN
                       │
                       ▼
            Upstream signed release
                       │
                 verify/hash
                       │
                       ▼
              ┌─────────────────┐
              │ Git repository  │
              │ vendored CRDs   │
              └────────┬────────┘
                       │
                       ▼
                platform-bootstrap
                       │
              ┌────────┴─────────┐
              │                  │
           CRDs (-5)        CRD Established?
                                 │
                                 ▼
                          Controllers (1)
                                 │
                          Controller Ready?
                                 │
                                 ▼
                       Admission Controller
                                 │
                         Webhooks Ready?
                                 │
                                 ▼
                platform-infrastructure
                                 │
                          Policies (5+)
                                 │
                                 ▼
                        Workloads (10+)
```

## Related ADRs

- ADR-021: Boundary-Driven GitOps and Day-0 Choreography — defines the Application boundary model
- ADR-034: Control-Plane Failure Domains — basis for stateful workload placement rules
- ADR-039: Platform Ownership Model — complete ownership matrix
