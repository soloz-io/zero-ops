# KAM (GitOps Application Manager) - Applicability Analysis

## Project Overview
**Source:** `.kiro/specs/agentic-enterprise-onboarding/references/redhat/kam/`

CLI tool for bootstrapping GitOps pipelines with OpenShift Pipelines (Tekton) and ArgoCD.

## Applicability to Zero-Ops Platform

### ❌ NOT APPLICABLE (Conceptual Overlap Only)

**Use Case:** GitOps Bootstrap Automation

**Justification:**
1. **OpenShift-Specific**: KAM assumes OpenShift Pipelines (Tekton) and OpenShift GitOps operator.
2. **Different Workflow**: KAM bootstraps CI/CD pipelines. Zero-Ops bootstraps SaaS environments (Crossplane XRD).
3. **CLI Paradigm Mismatch**: KAM is imperative CLI. Zero-Ops is declarative (AINativeSaaS CR).

## Why NOT Directly Applicable

### 1. Dependency on OpenShift Ecosystem
**KAM Requires:**
- OpenShift Pipelines Operator (Tekton)
- OpenShift GitOps Operator
- OpenShift-specific CRDs (Route, BuildConfig)

**Zero-Ops Uses:**
- Vanilla Kubernetes (Ubuntu/kubeadm)
- Argo Workflows (not Tekton)
- Crossplane (not OpenShift Operators)

### 2. Different Abstraction Level
**KAM Focus:**
- Bootstrap CI/CD pipelines for application delivery
- Generate Tekton Pipeline YAMLs
- Wire GitHub webhooks to Tekton EventListeners

**Zero-Ops Focus:**
- Provision complete SaaS environments (cluster + DB + observability)
- Crossplane Compositions (infrastructure as code)
- MCP-first agentic interface (not CLI-first)

### 3. Workflow Philosophy Conflict
**KAM Workflow:**
```bash
kam bootstrap --service-repo-url <repo> --gitops-repo-url <gitops-repo>
# Generates: pipelines/, environments/, config/
```

**Zero-Ops Workflow:**
```yaml
# User commits AINativeSaaS CR to Git
apiVersion: zero-ops.io/v1
kind: AINativeSaaS
spec:
  tier: enterprise
  cloud: hetzner
# Crossplane reconciles → full environment provisioned
```

## Conceptual Patterns Worth Noting

### ✅ Pattern 1: GitOps Repository Structure
**KAM Convention:**
```
gitops-repo/
  environments/
    dev/
    staging/
    production/
  config/
    argocd/
```

**Zero-Ops Adaptation:**
```
{tenant}-control-plane/
  overlays/
    starter/
    enterprise/
  base/
    secrets/
```

**Takeaway:** Overlay-based environment separation is industry standard. Zero-Ops already follows this.

**Source Reference:**
```
.kiro/specs/agentic-enterprise-onboarding/references/redhat/kam/docs/journey/day1/
```

### ✅ Pattern 2: Sealed Secrets Bootstrap
**KAM Approach:**
- Generates SealedSecret CRs during bootstrap
- Stores public key in Git, private key in cluster

**Zero-Ops Equivalent:**
- KSOPS + Age (already in PRD)
- Age public key in Git, private key in management cluster

**Takeaway:** Zero-Ops secret management is more flexible (SOPS supports multiple backends).

### ❌ Pattern 3: Tekton Pipeline Generation
**Not Applicable:** Zero-Ops uses Argo Workflows, not Tekton.

## Alignment with Zero-Ops Architecture

| KAM Feature | Zero-Ops Equivalent | Adoption Decision |
|---|---|---|
| GitOps repo bootstrap | Crossplane + Git commit | ❌ Different mechanism |
| Tekton Pipelines | Argo Workflows | ❌ Different tool |
| Sealed Secrets | KSOPS + Age | ✅ Already adopted |
| ArgoCD ApplicationSet | fleet-registry pattern | ✅ Already in PRD |
| CLI-driven bootstrap | MCP-driven provisioning | ❌ Different interface |

## Implementation Priority
**Priority:** N/A (Not Applicable)

**Rationale:** KAM solves a different problem (CI/CD bootstrap) than Zero-Ops (SaaS environment provisioning). No direct code reuse possible.

## Risks if Attempted to Adopt
- Introducing Tekton dependency (conflicts with Argo Workflows)
- OpenShift API dependencies (breaks vanilla Kubernetes requirement)
- CLI-first paradigm (conflicts with MCP-first architecture)

## Alternative: Learn from KAM's UX
**Valuable Insight:** KAM's Day 1/Day 2 operations documentation structure.

**Apply to Zero-Ops:**
- Day 1: Bootstrap management cluster (`zero-ops mgmt bootstrap`)
- Day 2: Onboard tenants (`tenant_create` MCP tool)

**Source Reference:**
```
.kiro/specs/agentic-enterprise-onboarding/references/redhat/kam/docs/journey/
```

## Conclusion
KAM is NOT applicable to Zero-Ops due to OpenShift dependencies and different abstraction level. However, its GitOps repository structure and Day 1/Day 2 operations framing are valuable conceptual patterns already reflected in Zero-Ops design.

## Next Steps
None. No code adoption recommended.
