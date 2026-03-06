# Design Patterns: Management Cluster Bootstrap

**Feature:** Platform Bootstrap (Journey A)  
**Version:** 1.0  
**Status:** APPROVED

---

## Core Patterns

1. **Ephemeral Bootstrap** - Use temporary Kind cluster to provision permanent Management Cluster, then discard
2. **Declarative Operator** - Manage CAPI providers via CRDs (cluster-api-operator) instead of imperative commands
3. **Topology-as-Code** - Define reusable cluster blueprints (ClusterClass) that hydrate into concrete resources
4. **Embedded Assets** - Package manifests in CLI binary via go:embed for version consistency
5. **State Pivot** - Migrate CAPI resources from bootstrap to Management Cluster for self-hosting
6. **Fixed Component Installation** - Install required platform services sequentially with verification
7. **Immutable Infrastructure** - Use Talos Linux with API-driven config, no SSH access
8. **Binary Management** - Auto-download and verify clusterctl/talosctl instead of manual installation
9. **Namespace Co-location** - Place all CAPI resources in single namespace (CAPI limitation)
10. **Preflight Validation** - Validate prerequisites before starting long-running operations

## Anti-Patterns (Avoided)

1. **Fat Client** - Storing cluster state locally (use thin client with Management Cluster API)
2. **Imperative Configuration** - Using `clusterctl init` commands (use cluster-api-operator CRDs)
3. **Manual Component Installation** - Requiring users to install ArgoCD/CNPG manually (use embedded manifests)
4. **SSH-Based Node Management** - Using SSH for node access (use Talos API via talosctl)

## Pattern Flow

```
Preflight → Ephemeral Bootstrap → Declarative Operator → Topology-as-Code → 
State Pivot → Fixed Components → Immutable Infrastructure
```

## Key Decisions

- **Declarative over Imperative**: cluster-api-operator enables GitOps, self-healing, declarative upgrades
- **Ephemeral over Direct**: CAPI requires existing cluster; Kind provides clean bootstrap environment
- **Fixed over Selectable**: Management Cluster is internal infra; all components are required dependencies
- **Talos over Ubuntu**: Immutable OS, API-driven, zero-touch operations align with platform goals
