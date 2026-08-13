# Environment Manager

Orchestrates three boundary ApplicationSets (`01-platform-infra`, `02-platform-data`, `03-platform-services`) using a Helm chart with an ArgoCD list generator.

## Why This Exists

The platform previously deployed 35+ static ArgoCD Application manifests from `manifests/argocd/bootstrap/`. Each child Application declared `targetRevision: HEAD`, which ArgoCD resolved against the repository default branch (`main`). This was correct for production but made ephemeral preview development impossible — feature branches could not be tested because every Application loaded from `main`.

## What We Tried (and Rejected)

| Approach | Result |
|----------|--------|
| **Orchestrator string replacement:** `strings.Replace` to patch `targetRevision: HEAD` at bootstrap time | Revision survived only until ArgoCD's next reconciliation cycle — apps reverted to `main` |
| **Kustomize ConfigMap + replacements:** `configMapGenerator` with `revision=HEAD`, orchestrator overlay patched the value | Same convergence problem — ArgoCD renders committed kustomization with `revision=HEAD`, overwriting injected value |
| **ArgoCD Matrix Generator + Git file generator:** Matrix combined Helm revision value with YAML files read from GitHub | Git file generator provides path metadata only — cannot read YAML content fields like `appName`. Variables render empty |

## Why ApplicationSet + List Generator

The list generator with inline elements is the simplest mechanism that satisfies all ADR-038 requirements:

1. **Continuous ArgoCD reconciliation:** Revision is a generator parameter (`environmentRevision` from Helm values). ArgoCD renders it on every sync cycle, not just at bootstrap.

2. **Explicit opt-in:** `isPlatformOwned: "true"` elements receive `{{ .Values.environmentRevision }}`. Third-party Helm charts (`isPlatformOwned: "false"`) keep their pinned `targetRevision`.

3. **No repository pollution:** Feature branch names exist only as Helm values — never committed to Git.

4. **Single source of truth:** Adding a new platform Application requires adding one list element to the appropriate boundary template. No file creation rituals, no label management, no Git repo cloning overhead.

### The Revision Routing Logic

```text
element.isPlatformOwned == "true"
  → targetRevision = {{ .Values.environmentRevision }}     (feat/my-branch or main)

element.isPlatformOwned == "false"
  → targetRevision = {{ .targetRevision }}                 (pinned version from element)
```

### The Bootstrap Flow

```text
hub bootstrap --provider=hetzner   (from feat/webhook-refactor)
  → git push origin feat/webhook-refactor
    → helm template environment-manager --set environmentRevision=feat/webhook-refactor
      → kubectl apply → ApplicationSets created
        → ArgoCD ApplicationSet controller generates 51 child Applications
          → Platform-owned apps track feat/webhook-refactor continuously
```

## Helm Escaping Pattern

ArgoCD template directives pass through Helm using double-brace escaping:

```yaml
# Helm template file               # After Helm rendering          # After ArgoCD processing
name: '{{"{{"}} .appName {{"}}"}}' → name: '{{ .appName }}'       → name: 'platform-database'
```

Helm expressions render normally (no escaping):
```yaml
{{ .Values.environmentRevision }}  → feat/webhook-refactor
```

## Files

```
manifests/argocd/environment-manager/
├── Chart.yaml
├── values.yaml                                          # environmentRevision: "main"
├── templates/
│   ├── 01-platform-infra-appset.yaml                    # 17 elements (core operators, ArgoCD)
│   ├── 02-platform-data-appset.yaml                     #  4 elements (CNPG, Redis, NATS, ClickHouse)
│   └── 03-platform-services-appset.yaml                 # 30 elements (APIs, spoke pools, identity)
└── README.md
```

## Related ADRs

- [ADR-038](../../../docs/adr/038-continuous-revision-tracking-ephemeral-environments.md) — Continuous Revision Tracking for Ephemeral Environments
- [ADR-004](../../../docs/adr/004-dual-repo-gitops-pattern.md) — Dual-Repository GitOps Pattern
- [ADR-021](../../../docs/adr/021-boundary-driven-gitops-and-day-0-choreography.md) — Boundary-Driven GitOps
