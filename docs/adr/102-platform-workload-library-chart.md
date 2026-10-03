# ADR-102: Applications render their workloads from one platform library chart

**Date:** 2026-10-03
**Status:** Accepted
**Relates to:** ADR-022 (stable-but-not-ready; Argo Rollouts), ADR-021 (default-deny
egress), ADR-052 (EphemeralJob lifecycle callbacks), ADR-057 (BFF / domain-service
split), ADR-073 (charts versioned with the build), ADR-099 (the app owns its schema)

## Context

Every application ships a BFF chart and a domain-service (SDK) chart. Almost all
of each is platform convention, not application logic:

- the Argo Rollout shape (canary, `Replace=true` because the selector is immutable);
- liveness and readiness probes, and which of them may carry a dependency;
- `ndots:2`, because ndots:5 sends lookups off-cluster and fails under load;
- the security context Kyverno admits: digest image, non-root, read-only root,
  dropped capabilities, no ServiceAccount token unless needed;
- the labels admission requires (`tenant-id`, `app-id`, `cost-center`) and the
  `<component>-workload` names the platform gateway and sibling policies address;
- network-policy patterns, and the schema-migration Job.

Each application copied these by hand, and the copies drifted. Two BFF rollouts
differed in 61 lines once the application name was normalised. One application's
readiness probe pointed at its liveness path, so the readiness endpoint it
implemented gated nothing. Scheduling, grace periods and canary steps differed for
no reason. A platform-wide fix had to be made once per application, and the next
application would have copied the charts a third time.

## Decision

The platform publishes a **library chart, `platform-workload`**, with the bundle
(every chart under `manifests/tenants/charts` is). An application's BFF or SDK
chart depends on it and renders:

```yaml
# templates/workload.yaml
{{ include "platform-workload.all" . }}
```

It declares only what differs, under `workload:` (root `tenantId`, `appId` and
`costCenter` are unchanged):

```yaml
workload:
  component: bff                     # required: bff, sdk, ...
  image: {repository: ghcr.io/<org>/<app>-bff}
  ports: {http: 3001}
  env: [...]
  envFrom: [...]
  networkPolicy:
    allowToSameApp: [{app: sdk, port: 3000}]
  migration: {enabled: true, strategy: presync-hook, command: [...]}
```

It renders a ServiceAccount, Service, Rollout, the workload's network policy
(only when a rule is declared), and the migration (only when enabled). An app
that needs one object rendered differently includes the individual pieces it keeps.

### The contract

1. **Names and selector are compatibility, not style.** `<component>-workload`
   (Rollout, Service), `<component>-workload-sa`, and the Rollout selector
   `{app: <component>, workload-class: stateless-web}`. These match what
   existing charts render, so adopting the library replaces objects in place.
2. **Liveness and readiness are different paths by default.** Liveness is
   `/health` and never depends on anything outside the process. Readiness is
   `/health/ready` and carries dependencies (ADR-022). `bffAuth` in
   `zero-ops-auth` serves it, 503 until the issuer's keys are held.
3. **Admission hardening is rendered, not declared.** An app cannot weaken it
   through values.
4. **Network policy is explicit, and ingress is opt-in.** Shorthands for a
   sibling component (`allowToSameApp`, `allowFromSameApp`), the shared Postgres
   (`sharedPostgres`), and EphemeralJob callbacks (`allowLifecycleCallbacks`,
   ADR-052). Raw rules (`ingress`, `egress`) are reviewed exceptions. What the
   platform already grants every tenant pod (DNS, the issuer's endpoints) is not
   restated. Declaring any ingress makes the pod default-deny, so every caller
   must be listed. Getting that wrong has already caused two outages that
   presented as hangs, not refusals.
5. **Migrations: two strategies, both explicit.**
   - `presync-hook`: an idempotent command on every sync. The egress policy and
     SQL are hooks at wave -6 and the Job at -5. A failed Job is kept until the
     next attempt, so its logs survive.
   - `hashed-job`: an ordinary Job named by a hash of its SQL, command and image,
     for an Application that may already be Synced.

   Either way the pod has no ServiceAccount token and reaches only the shared
   cluster's primary.
6. **Defaults live in the library's templates**, because a library chart's own
   values never reach the chart depending on it. Invalid configuration fails
   rendering: a missing `component`, `tenantId` or image, an unknown migration
   strategy, or a migration with no command.

## Acceptance criteria

Pinned by preflight `102-platform-workload` (W1–W6), rendering a throwaway
consumer chart against the working tree:

- W1: names and selector exactly as above;
- W2: liveness `/health`, readiness `/health/ready` by default;
- W3: digest image, non-root, read-only root, capabilities dropped, `ndots:2`, no
  ServiceAccount token, admission labels;
- W4: presync waves -6/-5 with failed Jobs kept; a hashed Job's name follows its SQL;
- W5: no policy when no rule is declared; `allowLifecycleCallbacks` admits the
  EphemeralJob operator;
- W6: the five invalid configurations above are refused.

An existing BFF and SDK, re-expressed on the library, rendered the same workload
objects as their hand-written charts. The migration Job differed only by
additions: the `workload-class: migration` label, the control-plane exclusion, an
image pull secret, and a hash covering its command and image.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| `platform-workload` library chart | zero-ops repository | Platform | publish pipeline | application charts | Day-1+ |
| Workload values (`workload:`) | application repository | Product team | CI (chart build) | the application's chart | Day-1+ |
| Rendered Rollout, Service, policies, migration | the application's chart release | Product team | ArgoCD | the cluster | Day-1+ |

## Consequences

### Positive

- A platform-wide fix (probes, DNS, security context, network policy) reaches
  every application that depends on the next library release, with no per-app
  edit.
- A new application writes values, not templates.
- The conventions are tested in one place (W1–W6) instead of being reviewed in
  each copy.

### Negative

- A mistake in the library reaches every adopting application at once. The
  preflight contract and the platform's release gate are what bound that.
- An application's rendered workload changes when the platform version it depends
  on moves. That is intended, but it is now a platform release, not an app release.
- Moving a migration from a hand-written `hashed-job` to the library's changes the
  Job's hash, so an idempotent migration runs once more.
- Two applications are a thin basis for an abstraction. The case rests on the next
  application, which would otherwise copy the charts a third time.

## Impact

- `manifests/tenants/charts/platform-workload/` (library; `_values`, `_names`,
  `_rollout`, `_networkpolicy`, `_migration`, `_all`).
- `scripts/validate/preflight/102-platform-workload.sh`.
- Companion: `zero-ops-auth` 0.20.0 `bffAuth`, which serves the readiness path
  this chart probes.
- Adoption is each application's change: depend on the library, move values under
  `workload:`, and serve `/health/ready`.

## References

- ADR-021: Default-deny egress
- ADR-022: Stable-but-not-ready application semantics
- ADR-052: Sandbox compute and S3 workspaces (EphemeralJob callbacks)
- ADR-057: Tenant user management (BFF / domain-service trust)
- ADR-099: The platform provisions a database, not a schema
