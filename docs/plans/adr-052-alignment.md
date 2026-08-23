# ADR-052 Alignment Plan — standing up elastic burst capacity

**Date:** 2026-08-23
**Goal:** Land the ADR-052 burst pool, move sandboxes off home-lab capacity, and
retire the ephemeral-job implementation currently built inside the `waypoint`
and `oranger` fleets — transferring one ownership row at a time and never
leaving a resource with two owners.

ADR-052 is the decision. This plan is implementation state and carries no
architectural authority; where the two disagree, ADR-052 controls.

## Observed state (verified 2026-08-22/23)

**Ephemeral jobs — fleet-built.**

| Component | Location | Role |
|---|---|---|
| `submit-ephemeral-job` step + `ephemeral_jobs` table | `waypoint/packages/waypoint-sdk` | submit API and System of Record, inside the waypoint tenant DB |
| `ephemeral-provisioner` (Go, ~1.4k LOC) | `waypoint/operators/ephemeral-provisioner` | claims jobs, provisions and destroys Hetzner servers, orphan sweep |
| `ephemeral-worker` (`worker.sh` + image) | `oranger/services/ephemeral-worker` | guest agent: pull image, run container, upload to S3, call back |

`oranger` does not call this directly; it authors a Waypoint blueprint and the
Waypoint SDK submits on its behalf. The capability is already consumed
cross-tenant, but through a workflow engine rather than a platform contract.

**Sandboxes — on home-lab capacity.** `packages/sandbox-k8s` creates `Sandbox`
CRs with an `emptyDir`-only podTemplate plus the `agent-vault-entrypoint`
ConfigMap, then a `ClusterIP` Service, and connects at
`http://<svc>.<ns>.svc.cluster.local:3000`. They consume the capacity ADR-052
§10 reserves for platform use.

**Burst pool — defined, unused.** ADR-046 provisions the Hetzner
`MachineDeployment` at `replicas: 0` with `workload-location: hetzner`. It is
the spoke's own worker pool, so scaling it adds nodes to the **same spoke
cluster** the fleet already runs on (ADR-052 §0) — no second cluster is created
at any point in this plan. Nothing currently scales it and nothing schedules to
it.

### Concrete instances of each ADR-052 §Context problem

1. **Fleet holds infrastructure authority.** `ephemeral-provisioner` runs with a
   cell-wide `HCLOUD_TOKEN` and creates and deletes cloud servers directly —
   rejected by ADR-052 §Alternatives and by ADR-041/ADR-043.
2. **`tenant_id` is recorded and never used.** Scanned in
   `internal/store/store.go:31`; zero references in `internal/provision/` or
   `internal/hetzner/`. It scopes no credential, quota, cleanup or metering.
3. **Provider inputs are process-wide singletons.** `VOLUME_ID`,
   `S3_BUCKET_NAME`, `S3_ACCESS_KEY_ID`, `EPHEMERAL_WORKER_IMAGE`,
   `CALLBACK_BFF_BASE_URL`, `GHCR_TOKEN` and `TAILSCALE_AUTHKEY` are read from
   the provisioner's environment, not from the job.
4. **Reclamation has cell-wide blast radius.** `ListServers` selects on
   `oranger.dev/managed-by` and deletes any server absent from `worker_id` in
   *its* database.
5. **Tenant identifier in platform infrastructure.** Managed-by label is
   `oranger.dev/*`.
6. **No metering.** OpenMeter receives nothing.
7. **Guest contract owned by a fleet.** `worker.sh` and its schemas live in
   `oranger`.
8. **Machine posture.** Cloud-init runs `tailscale up` with a shared reusable
   key in plaintext user-data on a VM running a fleet-supplied image with
   `/var/run/docker.sock` mounted; `CreateServer` attaches no firewall and
   injects the `mac-mini-ssh` key. Under ADR-052 none of this survives — burst
   nodes are CAPI-provisioned cluster nodes with the platform's standard
   bootstrap.

**Not yet in production.** `config/deploy.yaml` targets `namespace: default`
with an inline `DATABASE_URL` and image `ephemeral-provisioner:dev`; nothing in
`fleet-registry/tenants/waypoint/` references it, so ArgoCD does not reconcile
it. The whole implementation is deleted rather than migrated.

## Relationship to waypoint ADR-031

waypoint ADR-031 moved ephemeral jobs off Kubernetes CRDs onto Postgres and
Graphile Worker for two reasons:

1. *The workflow engine should not need a Kubernetes client or SA token.* No
   longer holds independently: the same SDK holds an in-cluster client and
   creates `agents.x-k8s.io/sandboxes` CRs today (`packages/sandbox-k8s`).
2. *Do not use the API server as a high-throughput transient queue.* Preserved:
   under ADR-052 a job is one `EphemeralJob` plus one `batch/v1` Job — the same
   object count any cluster-run workload has, with TTL cleanup.

waypoint ADR-031 remains authoritative for waypoint-internal concerns. Its
provisioning decisions are superseded for platform scope by ADR-052.

## Vertical slice first

Before WS1 is scoped in full, `adr-052-waypoint-vertical-slice.md` proves the
whole path with one existing Waypoint workflow: trigger →`EphemeralJob` CR → pod
on a burst node → workflow completes. It surfaces the ClusterClass/autoscaler
replica-ownership conflict and the provisioning-vs-execution timeout split,
both of which change how WS1 and WS5 are built. Run it first.

## Work streams

WS1–WS3 stand up burst capacity. WS4–WS6 migrate consumers. Each is
independently revertible.

**WS1 — `BurstCapacity` XR and the pool envelope.**
`BurstCapacity` XRD plus composition wrapping the ADR-046 burst
`MachineDeployment` in a `provider-kubernetes` `Object`, emitting the
autoscaler bounds annotations from `minNodes`/`maxNodes`. Declare one pool for
the dev hybrid cell with `minNodes: 0, maxNodes: 2`.
*Transfers:* Burst Capacity Envelope.
*Exit:* patching `maxNodes` in Git changes the `MachineDeployment` annotations;
manually setting `replicas: 1` produces a joined Hetzner node labelled
`workload-location: hetzner`, and returning it to `0` removes it cleanly.

**WS2 — cluster-autoscaler.**
Deploy one instance per spoke on the hub with `--cloud-provider=clusterapi`,
using the CAPI-generated spoke kubeconfig. Node group discovery via the CAPI
annotations from WS1.
*Transfers:* Burst Node Count.
*Exit:* a hand-written unschedulable pod with the burst nodeSelector and
toleration causes a scale-up, runs, and the node is reclaimed after
`scaleDownUnneededAfter`; the autoscaler provably refuses to exceed `maxNodes`.

**WS3 — Placement, priority and quota.**
Platform `PriorityClass` (`burst-tenant`, below every platform workload).
Kyverno mutating policy injecting nodeSelector, toleration and priority class
for sandbox pods and `EphemeralJob`-owned pods in enabled namespaces;
validating policy rejecting fleet-authored tolerations and `priorityClassName`.
`capabilities.burstCompute` in the universal-tenant chart rendering the
priority-scoped `ResourceQuota` at Tier 2.
*Transfers:* Burst Placement Policy, Per-Fleet Burst Quota.
*Exit:* an enabled fleet's sandbox pod is mutated onto burst; a non-enabled
fleet's identical pod is not; a fleet-authored burst toleration is rejected; the
quota rejects a pod beyond the fleet's burst allowance while leaving its
in-cluster workloads unaffected.

**WS4 — Sandboxes onto burst.**
Enable `capabilities.burstCompute` for `waypoint` in `dev`. No
`packages/sandbox-k8s` change is expected — verify that first and treat any
required change as a finding, since needing one means constraint 1 was not met.
*Transfers:* nothing — placement change.
*Exit:* an interactive sandbox session works end to end on a burst node; the
`CiliumClusterwideNetworkPolicy` egress allowlist is confirmed still enforcing;
cold-start latency measured and recorded; node reclaimed after the session ends.

**WS5 — `EphemeralJob` and fleet cut-over.**
`compute.nutgraf.in/v1alpha1 EphemeralJob` CRD and controller: state machine,
`batch/v1` Job materialisation, condition projection into status, completion
token, object contract, TTL. Rewrite `submit-ephemeral-job` to create a CR and
register the hook, keeping the blueprint-facing input schema unchanged. Run both
paths against dev and compare. Scale `ephemeral-provisioner` to zero at WS5
start — rollback in the window is a replica count. Then delete `ephemeral_jobs`,
`ephemeral-worker.ts`, the Graphile task, the internal routes,
`operators/ephemeral-provisioner`, and `oranger/services/ephemeral-worker`.
Forward-only, expand → migrate → contract, 24-hour window (ADR-020).
*Transfers:* Ephemeral Job Request.
*Exit:* `waypoint` has no Hetzner credential, no tailnet credential and no
cloud-provisioning code; `oranger` has no guest agent.

**WS6 — Metering and second consumer.**
Attribute pod resource-seconds on burst nodes by namespace through the existing
observability stack; add the burst meter to the billing catalog (ADR-012).
Enable `capabilities.burstCompute` for `oranger` directly, so it submits without
routing through a Waypoint blueprint.
*Transfers:* Burst Compute Usage.
*Exit:* two fleets, one pool, per-fleet burst usage visible in OpenMeter.

## Overlap interlock (WS1 → WS5)

Between WS1 and WS5 the burst pool exists while `ephemeral-provisioner` still
owns in-flight VMs. There is no shared resource: burst nodes are CAPI Machines
labelled and owned by CAPI; the fleet provisioner's servers carry
`oranger.dev/managed-by` and are invisible to CAPI. Neither can reclaim the
other's machines. Credential retirement completes at **revocation in Infisical**,
not when the credential stops being referenced.

## Decisions still open

1. **`minNodes` for the dev cell.** Start at `0` and let WS4 produce the
   measured cold-start number that decides whether warm capacity is warranted.
2. **Sandbox idle timeout vs. `scaleDownUnneededAfter`.** ADR-052 §6 identifies
   the sandbox idle timeout as the dominant burst cost lever. WS4 should record
   actual session lengths before either value is fixed.
3. **Privileged job workloads.** Whether any current job genuinely needs a
   container runtime socket, or whether the images can be rebuilt to run their
   payload directly. Only if one genuinely needs it does the ADR-052 §9
   dedicated pool get built.

## Deferred — large local working sets

Model weights, datasets and caches. Today: one shared Hetzner Volume attached by
ID from the operator's environment, mounted read-write, with `mkfs.ext4` guarded
only by a `blkid` probe. Constraints for that discussion:

- The volume attaches to one server at a time, and `provision()` logs
  `"failed to attach volume, continuing without it"` and proceeds — so a second
  concurrent job silently runs with an empty mount.
- Under ADR-052 the question changes shape: burst nodes are ordinary cluster
  nodes with CSI available, so a PVC becomes possible where it was not before —
  though Hetzner CSI is `ReadWriteOnce`, so a shared read-many cache still needs
  a design.

The `EphemeralJob` API exposes no volume, disk or model field until that
decision lands.
