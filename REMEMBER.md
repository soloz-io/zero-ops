# REMEMBER

Read before changing anything here. Every point below is a bug that already
happened and reported success while broken.

## Scoping

- Hub topology decisions key on **`--provider`**, not environment. `HybridDriver`
  overrides after delegating to `HetznerDriver`; never edit `HetznerDriver`
  defaults to fix a hybrid problem.
- Before editing any manifest, check who consumes it. These are **shared**:
  - `internal/assets/manifests/classes/hetzner-mgmt-ubuntu-v1.yaml` — hub
    ClusterClass, selected by **OS**, used by hetzner *and* hybrid.
  - `manifests/providers/_shared/spoke-addons/ccm-addon-template.yaml` — hub + spoke.
  - `manifests/spoke/spoke-bootstrap/cilium-addon-template.yaml` — the **hub**
    cilium addon (name says spoke; it is not).
- Provider-specific behaviour goes in the driver or a ClusterClass variable with
  `enabledIf`, never in a shared file.
- `manifests/providers/hybrid/**` is hybrid-only but still spans dev/stg/prod.
  Single-node assumptions are dev-only; stg/prod spokes run 3 CP nodes.

## Placement and storage (ADR-046 §11 / ADR-014)

- Stateful workloads run on **worker nodes only**. Both selectors required:
  `node-role.kubernetes.io/worker: ""` **and** `workload-location: home|hetzner`.
- Control-plane nodes keep `control-plane:NoSchedule`. Untainting re-enables the
  banned failure: CNPG landing on the CP because storage happened to bind there.
- `local-path` binds on home workers only. `hcloud-volumes` on Hetzner nodes only.
  `hcloud-volumes` stays the **default** class; `local-path` is never default.
- A **Pending PVC means the home worker has not joined** — it is the correct
  failure mode. Do not "fix" it by changing the storage class.

## Bootstrap ordering

- Hybrid hub runs **0 Hetzner workers**; capacity is the home-lab Flatcar node.
- The home worker must join **before `boundary-01`** (phase `home-worker-join`),
  not after the bootstrap. ArgoCD and the operators are platform workloads too.
- CAPI cannot provision home workers. `scripts/hybrid/provision-flatcar-worker.sh`
  does; for `--cluster hub` it mints its own bootstrap token, so it depends on
  nothing that boundary-01 installs.
- `--cluster hub|spoke` **selects** which registry entries to build; column 6 of
  `home-lab.env` decides where each node belongs. It must never retarget a node —
  it once did, so `--cluster hub` joined the *spoke* VM to the hub under its spoke
  name. `--node N` is a positional filter, not a target.
- The kind bootstrap cluster needs **no Hetzner CSI** (it has zero PVCs). The CSI
  controller requires the Hetzner metadata service and CrashLoops on kind.
- HCCM must tolerate `node.cilium.io/agent-not-ready` and carry **no**
  `instance.hetzner.cloud/provided-by` nodeSelector — that label is applied *by*
  HCCM, so the selector can never be satisfied on a fresh cluster. Without both,
  the cluster deadlocks: HCCM Pending → no node IP → cilium aborts → taint stays.
- cilium-operator declares hostPorts: 2 replicas need 2 nodes. Single-node
  clusters must scale it to 1 (`CiliumOperatorReplicas`), not edit the shared addon.

## GitOps

- Fix the **manifest**, then apply that file. Never hand-patch a live object and
  move on — the fix is lost on the next bootstrap.
- CRS payload Secrets (`<cluster>-<addon>-addon`) hold a **copy** of the manifest
  and go stale. After changing an addon, re-sync the Secret or the old content
  pivots to the hub. Their `type` is immutable — patch `.data`, don't recreate.
- `manifests/hub-core-services/**` reaches the hub only if an ApplicationSet
  lists its path. Several directories exist that nothing delivers — check
  `manifests/argocd/environment-manager/templates/*-appset.yaml` before assuming.

## CLI traps

- `--environment ""` is coerced to **`prod`**, sending the spoke AppSet to a path
  that does not exist; the spoke then never provisions. Always pass it.
- `hub spoke teardown --name=X` is a **silent no-op** (it rebuilds the name from
  label parts and never matches). Run it without `--name`, which deletes *every*
  spoke — take an inventory first.
- `hub teardown` does **not** delete the spoke. Tear the spoke down first, or the
  CAPI controllers that would unwind it are gone.
- `hub configure-eso` always returns an error. Do not call it.

## Validation

- `scripts/validate/` — one module per concern. `run.sh preflight` is static and
  runs before anything is created; `run.sh cluster --mode=gate|final` for live
  checks. Adding a check means adding a file.
- A check earns its place only if it catches something that reports **Healthy
  while broken**. Anything kubectl/kustomize/go test already catches does not.
- Post-bootstrap validation is fatal. Do not downgrade it to a warning.

## Images

- No `:latest`. Pin `tag@sha256:`. The check runs against **rendered** output, so
  a kubebuilder `image: controller:latest` overridden by kustomize is fine.
- Builds happen in GitHub workflows; Docker is not available locally. First-party
  images are digest-pinned by CI via `kustomize edit set image` + auto-commit —
  that push must rebase, or it is rejected while the build was running.
- ghcr `argocd-agent` publishes only commit-SHA tags; **quay** carries the semver
  releases. `argocd-agent-{agent,principal}` on ghcr are Helm charts, not images.

## Environment gotchas

- macOS: no `grep -P`, no coreutils `timeout`. Docker **is** required for bootstrap
  (kind hosts the CAPI bootstrap cluster before the pivot).
- `ssh` inside a `while read` loop eats the loop's stdin — use `ssh -n`.
- `powershell -Command -` silently produces **no output** for a multi-line brace
  block. Use `-EncodedCommand` (base64 UTF-16LE).
- Under `set -euo pipefail`, a no-match glob passed to `ls` kills the script with
  no output. Guard with `|| true`.
- `[[ -f x ]] && { ... }` as the last statement of a loop makes the loop exit 1.
- Duplicate YAML keys are silently last-wins. `omitempty` does nothing on
  `time.Time` — use `*time.Time`.
