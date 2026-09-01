# ADR-029: Deterministic Platform Bootstrapping and CRD Supply Chain Security

## Status
Accepted

## Context
During the orchestration of Spoke clusters, we encountered severe reconciliation deadlocks and admission controller failures. Specifically, Kyverno validation policies rejected tenant workloads because the `Rollout` Custom Resource Definition (CRD) was fetched via a live GitHub URL during Kustomize rendering and had not reached an `Established` state in the Kubernetes API before the policies were applied. 

Relying on raw external URLs during continuous reconciliation introduces a critical supply-chain vulnerability, breaks air-gapped compatibility, and ruins disaster recovery determinism. Furthermore, relying purely on ArgoCD `sync-waves` within a single monolithic Application does not guarantee semantic API readiness.

## Decision
To achieve enterprise-grade determinism and supply-chain integrity, we mandate the following patterns for all foundational platform infrastructure:

1. **Strict CRD Vendoring:** No infrastructure Kustomization or Helm chart may fetch CRDs from live remote URLs (e.g., `raw.githubusercontent.com`). All third-party CRDs must be downloaded, audited, and vendored locally into the Git repository (e.g., `crds/` directories).
2. **Direct Annotation over Dynamic Patching:** Foundational manifests (like CRDs) should be annotated directly in their vendored files rather than relying on dynamic Kustomize patching (`patchesStrategicMerge` / `patchesJson6922`), reducing rendering brittleness.
3. **Semantic Readiness Gating:** CRD installation, Operator deployment, and Policy enforcement must be temporally separated. 
   - Foundational CRDs MUST execute at `sync-wave: "-5"`.
   - Operators and Controllers execute at `sync-wave: "1"`.
   - Cluster-wide validation policies (Kyverno, OPA) MUST execute at `sync-wave: "5"` or higher to guarantee the API discovery cache has fully stabilized.
4. **Decoupled Bootstrapping (Future-Proofing):** As the platform scales, CRDs and Operators SHALL be extracted into a dedicated `platform-bootstrap` ArgoCD Application, entirely decoupled from the `platform-infrastructure` Application.

## Ownership

This ADR defines CRD supply chain security and bootstrapping conventions. For resource ownership, see ADR-039.

## Consequences
- **Positive:** Disaster recovery is 100% reproducible. Air-gapped deployments are natively supported. Zero downtime or blocked queues caused by GitHub rate-limiting or outages.
- **Negative:** Slightly increased repository footprint due to vendored CRD YAML files. Platform engineers must manually update the vendored CRDs when upgrading third-party operators.