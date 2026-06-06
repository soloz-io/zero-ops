---
name: provider-environment-matrix
description: >
  Reference for the matrix topology architecture (ADR 036/037/038) — how
  environments (dev/stg/prod/ephemeral) and infrastructure providers
  (local/hetzner) intersect dynamically at bootstrap time. Use this skill
  whenever the task involves AppSet paths, provider isolation, environment
  gating, spoke pool placement, the environment-manager Helm chart, bootstrap
  wiring, or any question about why a provider-specific component exists
  or does not exist on a given Hub. Also trigger when someone asks about
  the boundary ApplicationSets, the `environmentSlug` or `provider` Helm
  values, the infra-provider AppSet element, or the three-ADR matrix design.
---

# Provider & Environment Matrix Topology

The platform uses three intersecting ADRs to achieve strict isolation between
environments and infrastructure providers. Every Hub cluster is identified by
a **matrix cell**: exactly one `environmentSlug` x `provider` combination.

## The Three Dimensions

Every bootstrap passes exactly three Helm values to the `environment-manager`
chart. No defaults — all three are required at runtime.

| Dimension | ADR | CLI Flag | Helm Value | Purpose |
|-----------|-----|----------|------------|---------|
| Revision | 038 | (derived from git branch) | `environmentRevision` | Git SHA/branch for ArgoCD to track |
| Environment | 037 | `--environment` | `environmentSlug` | Which overlay dir (`dev/stg/prod/ephemeral`) |
| Provider | 036 | `--provider` | `provider` | Which infra provider (`local/hetzner/aws`) |

The bootstrap CLI (`cmd/hub/bootstrap.go`) maps:
- `--provider=docker` → Helm `provider=local` (directory name)
- `--environment=dev` (docker default) → Helm `environmentSlug=dev`
- `--environment=prod` (hetzner default) → Helm `environmentSlug=prod`

## Directory Structure

```
manifests/
├── argocd/environment-manager/    # Helm chart (ADR 038)
│   ├── values.yaml                # matrix dimensions (commented out, CLI-only)
│   └── templates/
│       ├── 01-platform-infra-appset.yaml    # infra boundary
│       ├── 02-platform-data-appset.yaml     # data boundary
│       └── 03-platform-services-appset.yaml # services boundary
├── environments/                  # Environment overlays (ADR 037)
│   ├── base/                      # empty ConfigMap stub
│   ├── dev/                       # kustomization + patch-config.yaml
│   ├── stg/                       # stg.nutgraf.in
│   └── prod/                      # nutgraf.in
├── providers/                     # Provider resources (ADR 036)
│   ├── local/k8s/                 # CAPD ClusterClass, Composition, storage-class
│   └── hetzner/k8s/               # Hetzner ClusterClass, Composition, CCM, external-dns
└── spoke/spoke-pools/             # SpokePool claims at matrix intersection
    ├── dev/capd/                  # local-dev.yaml
    └── prod/hetzner/              # spoke-pool-eu-prod-01.yaml
```

## How the Matrix Resolves at Runtime

The `environment-manager` chart's three ApplicationSets use Go template
expressions that interpolate the Helm values into directory paths:

### 03-platform-services-appset.yaml (key elements)

```yaml
# Environment-specific config (ADR 037)
path: 'manifests/environments/{{ .Values.environmentSlug }}'

# Provider-specific infrastructure (ADR 036)
path: 'manifests/providers/{{ .Values.provider }}/k8s'

# SpokePool claims at the matrix intersection
path: 'manifests/spoke/spoke-pools/{{ .Values.environmentSlug }}/{{ .Values.provider }}'
```

A bootstrap with `--environment=dev --provider=docker` produces paths:
- `manifests/environments/dev`
- `manifests/providers/local/k8s`
- `manifests/spoke/spoke-pools/dev/local`

A bootstrap with `--environment=prod --provider=hetzner` produces:
- `manifests/environments/prod`
- `manifests/providers/hetzner/k8s`
- `manifests/spoke/spoke-pools/prod/hetzner`

## Provider Isolation Rules

1. **No provider-specific code in core platform services.** All Hetzner or
   CAPD resources live under `manifests/providers/<name>/`. The AppSets never
   reference `hetzner` or `docker` by name — they use `{{ .Values.provider }}`.

2. **Provider infrastructure is applied via the `infrastructure-provider`
   element** in `03-platform-services`. This is a Kustomize Application that
   points at `manifests/providers/{{ .Values.provider }}/k8s`. Only one
   provider's resources are ever reconciled on any given Hub.

3. **Provider-specific list elements removed from AppSets.**
   - `external-dns` (Hetzner webhook), `hcloud-ccm` → moved to `providers/hetzner/k8s/`
   - `ingress-nginx`, `cert-manager-webhook-hetzner` → removed from `01-platform-infra`
   - The `infrastructure-provider` element replaced all of these.

4. **SpokePool claims use `compositionSelector.matchLabels.provider`** to
   dynamically route to the correct Crossplane Composition. The webhook that
   previously hardcoded this mapping was removed.

## Environment Gating Rules

1. **Environment overlays (`manifests/environments/<slug>/`) contain only**
   environment-specific ConfigMap patches (DOMAIN, CLUSTER_ID,
   INFISICAL_ENVIRONMENT_SLUG). No provider references.

2. **Environment overlays no longer include `components:`** referencing
   provider Kustomize components. Provider selection is done entirely via
   the `infrastructure-provider` AppSet element.

3. **Sequential promotion path:** ephemeral → dev → stg → prod. Each
   environment boots on a physically isolated Hub cluster.

4. **CODEOWNERS** gates prod overlay changes to Platform Engineering.

## Bootstrap Wiring (hub CLI)

The `internal/hub-cli/bootstrap/orchestrator.go:deployPlatform()` method
renders the `environment-manager` chart:

```go
helmCmd := exec.CommandContext(ctx, "helm", "template", "environment-manager",
    "manifests/argocd/environment-manager",
    "--set", "environmentRevision="+envRevision,
    "--set", "environmentSlug="+o.EnvironmentSlug,
    "--set", "provider="+providerForHelm,
)
```

The provider name is mapped: `docker` → `local` (to match the directory path
`manifests/providers/local/k8s/`).

## Adding a New Provider

1. Create `manifests/providers/<name>/k8s/` with:
   - ClusterClass (CAPI templates)
   - Crossplane Composition (labeled `provider: <name>`)
   - Any provider-specific CRDs, CCMs, webhooks, or secrets
2. Add a SpokePool claim at `manifests/spoke/spoke-pools/<env>/<name>/`
3. No AppSet changes needed — the `infrastructure-provider` element
   dynamically resolves `manifests/providers/{{ .Values.provider }}/k8s`.
4. Add a provider driver in `internal/hub-cli/bootstrap/` implementing the
   `Provider` interface.

## Adding a New Environment

1. Create `manifests/environments/<slug>/` with `kustomization.yaml` and
   `patch-config.yaml` (DOMAIN, CLUSTER_ID)
2. No AppSet changes needed — the `hub-environment` element dynamically
   resolves `manifests/environments/{{ .Values.environmentSlug }}`.
3. Add SpokePool claims at `manifests/spoke/spoke-pools/<slug>/<provider>/`
