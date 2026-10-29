# Transfer: Grafana's dependent resources change Application owner

The Grafana instance, its datasources, the platform folder, the public route and the
dashboards moved from the `platform-dashboards` Application to a new `grafana`
Application (ADR-021 amendment 2026-10-29, Decision 5). The CRDs and the operator did
not move: `platform-dashboards` remains their owner.

This runbook is the transfer. **Rehearse it on an isolated cluster before the
development hub**, and do not treat a green sync as evidence.

## Why it needs a procedure

ArgoCD provides no ordering between independently reconciled Applications. Both
Applications auto-sync with `prune: true`, so at the moment the resources leave
`platform-dashboards`' desired state, two outcomes are possible and nothing in ArgoCD
chooses between them:

- `grafana` syncs first, adopts the live objects by re-applying them (server-side
  apply, same names), and `platform-dashboards` no longer tracks them — nothing is
  deleted; or
- `platform-dashboards` refreshes first, sees resources it tracks and no longer
  declares, and prunes them. Deleting a `Grafana` CR takes its Deployment with it.

Stage 1 removes the second outcome by making the live objects refuse pruning before
the move lands.

## Invariants

| | |
|---|---|
| I1 | No CRD changes Application owner. `grafana-operator-crds.yaml` stays in `platform-dashboards`. |
| I2 | Every moved object keeps its `metadata.uid`. Recreation with the same name is a failure. |
| I3 | No window where a prune-enabled Application tracks an object that has left its desired state. |
| I4 | Evidence, not a green sync: UIDs before and after, the tracking label's value, and no `Delete` events. |
| I5 | Interrupted at any point, the recovery is to restore the previous desired state in Git. |

## Before you start — establish, do not assume

1. **Resource tracking.** `argocd-cm` sets `application.instanceLabelKey:
   argocd.argoproj.io/instance` and does **not** set
   `application.resourceTrackingMethod`. The chart is pinned at `7.9.1`, and the
   documented default differs between the 2.x and 3.0 lines, so read the live
   configuration rather than the chart:
   ```
   kubectl -n platform-ops get cm argocd-cm -o jsonpath='{.data.application\.resourceTrackingMethod}{"\n"}'
   kubectl -n platform-observability get grafana platform -o jsonpath='{.metadata.labels}{"\n"}{.metadata.annotations}{"\n"}'
   ```
   Label tracking records the owner in that label; annotation tracking records it in
   `argocd.argoproj.io/tracking-id`. Both are handled by this procedure, but you must
   know which one you are watching.
2. **ApplicationSet deletion policy**, because the boundary relies on retention:
   ```
   kubectl -n platform-ops get deploy argocd-applicationset-controller \
     -o jsonpath='{.spec.template.spec.containers[0].args}{"\n"}'
   ```
   Expect no `--policy` that overrides `applicationsSync: create-update`.
3. **Record identities.** Keep this output; it is the I2 evidence:
   ```
   kubectl -n platform-observability get grafana,grafanadatasource,grafanafolder,grafanadashboard,httproute,externalsecret \
     -o custom-columns=KIND:.kind,NAME:.metadata.name,UID:.metadata.uid,OWNER:'.metadata.labels.argocd\.argoproj\.io/instance' > /tmp/grafana-uids-before.txt
   ```

## Stage 1 — the live objects refuse pruning (already in Git, commit `010c9fbf`)

The moving resources carry `Prune=false` in their sync options; the generated
dashboards carry it from `scripts/observability/mixin/render.py`, so a regeneration
keeps it. Sync `platform-dashboards` and confirm the annotation is on the **live**
objects, not just in Git:

```
kubectl -n platform-observability get grafana platform \
  -o jsonpath='{.metadata.annotations.argocd\.argoproj\.io/sync-options}{"\n"}'
```

Every moved kind must show `Prune=false` before stage 2 is synced. If any does not,
stop: the protection is per-object and a gap is exactly where the deletion happens.

## Stage 2 — the move (commit `bf4342a5`)

The resources leave `platform-dashboards`' path and arrive in
`manifests/hub-core-services/grafana`, generated as the `grafana` Application.

1. Sync the boundary and let both Applications settle.
2. `grafana` adopts the objects by applying them. Confirm ownership moved:
   ```
   kubectl -n platform-observability get grafana platform \
     -o jsonpath='{.metadata.labels.argocd\.argoproj\.io/instance}{"\n"}'   # expect: grafana
   ```
3. Confirm identity survived — this is the acceptance test:
   ```
   kubectl -n platform-observability get grafana,grafanadatasource,grafanafolder,grafanadashboard,httproute,externalsecret \
     -o custom-columns=KIND:.kind,NAME:.metadata.name,UID:.metadata.uid,OWNER:'.metadata.labels.argocd\.argoproj\.io/instance' > /tmp/grafana-uids-after.txt
   diff /tmp/grafana-uids-before.txt /tmp/grafana-uids-after.txt
   ```
   Only the OWNER column may differ. **A changed UID means an object was deleted and
   recreated: the transfer failed I2**, even if everything is Healthy.
4. No deletions occurred:
   ```
   kubectl -n platform-observability get events --field-selector reason=Delete
   ```
5. Grafana itself: the Deployment's pods are not restarted by an ownership change, and
   its state is in the `grafana` database on the hub's CNPG cluster, not in a PVC.
   Confirm the dashboards still render and the datasources still answer.

## Stage 3 — remove the transitional protection (not yet in Git)

Only once stages 1 and 2 are verified on the development hub. `Prune=false` left in
place means a resource deleted from Git is never removed from the cluster, which is a
GitOps hole, so this stage is not optional:

1. Remove `,Prune=false` from `manifests/hub-core-services/grafana/grafana.yaml` and
   from the two annotation sites in `scripts/observability/mixin/render.py`, and
   regenerate the dashboards (`scripts/observability/mixin/generate.sh`, CI/linux).
2. Sync and confirm nothing is pruned: the resources are in `grafana`'s desired state,
   so there is nothing for either Application to remove.

## If it goes wrong

- **Objects pruned during stage 2.** Re-sync `grafana`; it recreates them from Git.
  The `Grafana` CR returns with a new UID and the operator rebuilds its Deployment;
  dashboards, datasources and the folder are declarative and return intact. Grafana's
  own state — users, preferences, starred dashboards — is in PostgreSQL and is not
  touched by any of this. Record the UID change: I2 failed, and the procedure needs
  revisiting before it is used again.
- **Both Applications claim the resources.** Expected briefly during stage 2 and
  reported as a shared resource. If it persists, one side still declares them: check
  that `platform-dashboards` no longer contains the moved files.
- **Interrupted between stages.** Revert to the previous commit in Git and sync. Every
  stage is a Git state, which is why there is no imperative recovery step.
