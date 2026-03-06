# Design Patterns: Management Cluster Bootstrap

**Feature:** Platform Bootstrap (Journey A)  
**Version:** 1.0  
**Status:** APPROVED

---

## Core Patterns

1. **Ephemeral Bootstrap** - Use temporary Kind cluster to provision permanent Management Cluster, then discard.
2. **Declarative Operator (Phase 2)** - Target state is to manage CAPI providers declaratively via `cluster-api-operator` CRDs; Phase 1 intentionally uses `clusterctl init` from the CLI, operator-backed GitOps is the roadmap.
3. **Topology-as-Code** - Define reusable cluster blueprints (ClusterClass) that hydrate into concrete resources.
4. **Embedded Assets** - Package manifests in CLI binary via `go:embed` for version consistency.
5. **State Pivot** - Migrate CAPI resources from bootstrap to Management Cluster for self-hosting using `clusterctl move`.
6. **Fixed Component Installation** - Install required platform services sequentially with verification, preserving the CCM → `providerID` → node readiness → ArgoCD dependency chain.
7. **Immutable Infrastructure** - Use Talos Linux with API-driven config, no SSH access.
8. **Binary Management** - Auto-download and verify `clusterctl` / `talosctl` instead of manual installation.
9. **Namespace Co-location** - Place all CAPI resources in a single namespace as a deliberate pivot-reliability choice so `clusterctl move` has a clear, per-namespace boundary (CAPI itself supports multi-namespace, we intentionally do not use it here).
10. **Preflight Validation** - Validate prerequisites before starting long-running operations.
11. **Reconciliation Loop** - Use level-triggered polling/reconciliation (e.g. `WaitForReady`) based on desired vs current state, with idempotent operations that are safe to retry until convergence.
12. **Sealed Configuration** - Generate, sign, and treat Talos machine configuration as immutable sealed artifacts created at bootstrap time, updated only via new sealed configs rather than in-place edits.
13. **Phase-Gated State Machine** - Model bootstrap as a linear, forward-only sequence of idempotent phases (`BootstrapPhase`), with durable progress tracking and safe resume but no skipping ahead.

## Anti-Patterns (Avoided)

1. **Fat Client** - Storing *cluster workload/runtime state* locally or treating local files as the source of truth (use the Management Cluster API as the authority). A narrow exception is allowed for minimal **bootstrap progress state** persisted locally (for example `~/.zero-ops/.../state.json`) before a Management Cluster exists.
2. **Imperative Configuration** - Relying on ad-hoc imperative changes once the declarative, operator-driven path is in place. In **Phase 1** the CLI intentionally calls `clusterctl init` imperatively; this becomes an anti-pattern only after provider lifecycle is fully managed via `cluster-api-operator` and GitOps.
3. **Manual Component Installation** - Requiring users to install ArgoCD/CNPG manually (use embedded manifests)
4. **SSH-Based Node Management** - Using SSH for node access (use Talos API via talosctl)

## Pattern Flow

```
Preflight → Ephemeral Bootstrap → Declarative Operator → Topology-as-Code → 
State Pivot → Fixed Components → Immutable Infrastructure
```

## Key Decisions

- **Declarative over Imperative (target state)**: `cluster-api-operator` enables GitOps, self-healing, declarative upgrades; Phase 1 uses imperative `clusterctl init` with a clear migration path
- **Ephemeral over Direct**: CAPI requires existing cluster; Kind provides clean bootstrap environment
- **Fixed over Selectable**: Management Cluster is internal infra; all components are required dependencies
- **Talos over Ubuntu**: Immutable OS, API-driven, zero-touch operations align with platform goals
