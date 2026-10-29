# Grafana API ownership anchor

This Application owns the **grafana-operator and its CRDs**, and nothing else.

The Grafana instance, its datasources, the platform folder, the public route and
the generated dashboards are `manifests/hub-core-services/grafana` — a separate
Application (ADR-021 amendment 2026-10-29). They were split because every resource
there is ready only once the operator here is running, and ArgoCD treats one
Application's sync as a single unit: a `Grafana` CR that cannot go healthy held up
the sync that would have started the operator it was waiting for (ADR-021 addendum
2026-08-22).

The name is unchanged on purpose. This Application is the installed owner of the
Grafana CRDs; renaming it would transfer CRD ownership to obtain a tidier name, and
a CRD that changes Application owner is the one move in this refactor that can
cascade into deleting every custom resource it serves.

## Ordering inside this Application

| Resource | Wave | Why |
|---|---|---|
| `grafana-operator-crds.yaml` (13 CRDs) | `-5` | the API must be `Established` first |
| `grafana-operator.yaml` (SA, RBAC, Service, Deployment) | `1` | the controller that serves that API |

Resource waves order one Application's own sync and are the only ordering ArgoCD
guarantees here. There is no ordering between this Application and `grafana`; that
one recovers by retry (`limit: -1`, capped backoff) until these CRDs exist.

## Vendored CRDs

`grafana-operator-crds.yaml` is vendored per ADR-029 §1 and pinned **below 5.25.0**
deliberately: 5.25.0's CRDs validate with the CEL `format` library, which the hub's
Kubernetes 1.31 does not have. Provenance and digests are in
`manifests/provenance/platform-dashboards/`.
