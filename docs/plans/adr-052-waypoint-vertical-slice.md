# ADR-052 Vertical Slice — proving burst capacity with `hetzner-job-processor-workflow`

**Date:** 2026-08-23
**Goal:** Triggering the existing Waypoint workflow
`packages/app-workflows/src/workflows/hetzner-job-processor/latest/hetzner-job-processor-workflow.json`
creates an `EphemeralJob` in `tenant-waypoint`, which eventually schedules a pod
onto a Hetzner burst node in the same spoke cluster, runs to completion, and
resumes the workflow to its terminal state.

This is the narrowest end-to-end path that exercises every ADR-052 layer. It is
a validation slice, not the migration — the migration is
`adr-052-alignment.md`.

## Success criteria

1. **The workflow JSON is not edited.** It is the test fixture. Its
   `system/submit-ephemeral-job` config keys — `image`, `resources`, `storage`,
   `input`, `output`, `callbackBaseUrl`, `timeoutSeconds`, `serverType` — must
   continue to work as authored.
2. **The workload image is not edited.** It continues to read the same
   input/output environment variables it reads today.
3. The workflow reaches `state-4 (Complete)` with
   `agent_output_files.url` populated from
   `{{@transition-submit-job:Submit Ephemeral Job.output.s3_url}}`.
4. Before the run, `kubectl get nodes -l workload-location=hetzner` returns
   nothing. After it, the node has been created **and** reclaimed.

The workflow's own step description already reads *"Creates an EphemeralJob CR
to provision a Hetzner CX32 server"* — the fixture was written against a CR
contract, so this slice restores the shape the blueprint always assumed.

## The trace

Eight checkpoints. Each must be observable independently, so a failure localises
to one layer.

| # | Layer | Observation |
|---|---|---|
| 1 | Blueprint | Workflow suspends at `transition-submit-job` with a pending hook |
| 2 | Tenant → platform API | `kubectl get ephemeraljob -n tenant-waypoint` shows the CR, `phase: Pending` |
| 3 | Job controller | `kubectl get job,pod -n tenant-waypoint` shows a `batch/v1` Job and a `Pending` pod |
| 4 | Scheduler | Pod event `FailedScheduling`; `EphemeralJob.status.phase: Provisioning` with the reason projected |
| 5 | Autoscaler | On the hub: `MachineDeployment` `md-0` replicas `0 → 1` |
| 6 | CAPI / CAPH | `kubectl get machine -n platform-capi` → `Provisioning` → `Running`; node joins the spoke `Ready` with `workload-location=hetzner` |
| 7 | Execution | Pod binds to the burst node, pulls, runs, `Completed`; `EphemeralJob.phase: Succeeded` |
| 8 | Resume | Controller calls the callback; workflow advances `state-2 → state-3 → state-4`; sandbox notified |

Checkpoints 5 and 6 are on the **hub** (`platform-capi`); the rest are on the
spoke. Nothing in this trace crosses a cluster boundary for *workload* traffic —
the pod, its Service access and its callback target are all in the same spoke
(ADR-052 §0).

## What the platform must ship

Ordered by dependency. Each has an exit criterion provable without the layer
above it.

**P0 — prove Machine↔Node correlation on a burst node.** *(blocked: see
Finding 1b — do not scale `md-0` until the replicas/annotations change lands as
one commit.)*
Scale `md-0` to `1` by hand and confirm the joining node receives a
`providerID` from CCM, that CAPI populates `Machine.status.nodeRef`, and that
`NodeHealthy` becomes `True`. Without this, the autoscaler can scale **up** from
unschedulable pods but cannot identify which node to remove — burst nodes are
never reclaimed and the ADR-052 §6 cost model fails silently, which is the worst
available failure mode. Note the spoke's CCM has restarted repeatedly while
failing to parse the home node's `unmanaged://` provider ID; it has since been
stable, but its health is a precondition, not an assumption.
*Exit:* a burst node reaches `Ready` with a `hcloud://` providerID, its Machine
reports `NodeHealthy: True`, and scaling back to `0` removes it cleanly.

*(Expect this step to fight Finding 1 — that is the point. Do P0 by patching the
`Cluster` topology replicas, not the MachineDeployment, so the topology
controller is cooperating rather than reverting.)*

**P1 — `BurstCapacity` XR makes `md-0` autoscalable.**
The hybrid composition currently pins
`spec.topology.workers.machineDeployments[md-0].replicas: 0`
(`manifests/providers/hybrid/k8s/spokepool-hybrid-composition.yaml:91`). The XR
must change that to: **omit `replicas`** and set the autoscaler bounds
annotations on the topology entry. See *Finding 1* — this is the difference
between a working slice and an infinite fight.
*Exit:* the generated `MachineDeployment` carries
`cluster.x-k8s.io/cluster-api-autoscaler-node-group-{min,max}-size`; manually
patching its `replicas` to `1` produces a joined node **that the topology
controller does not revert**, and patching back to `0` removes it.

**P2 — cluster-autoscaler for the dev spoke.**
One instance on the hub, `--cloud-provider=clusterapi`, using the
CAPI-generated `<spoke>-kubeconfig` in `platform-capi` for the workload cluster.
*Exit:* a hand-written unschedulable pod carrying the burst nodeSelector and
toleration triggers scale-up and is reclaimed after
`scaleDownUnneededAfter`; the autoscaler refuses to exceed `maxNodes`.

**P3 — `EphemeralJob` CRD and controller.**
`compute.nutgraf.in/v1alpha1`, deployed by `spoke-catalog/infra/` into
`platform-ops`. State machine, `batch/v1` Job materialisation, pod/node
condition projection into `status`, completion callback, TTL. Split timeouts per
*Finding 3*.
*Exit:* a hand-written `EphemeralJob` runs a trivial image to completion on
home-lab capacity (burst placement off), and its callback fires.

**P4 — Placement, priority, quota.**
`burst-tenant` PriorityClass below every platform workload; Kyverno mutating
policy adding nodeSelector + toleration + priority class to `EphemeralJob`-owned
pods in enabled namespaces; validating policy rejecting fleet-authored
tolerations and `priorityClassName`.
*Exit:* an enabled namespace's job pod is mutated onto burst; a non-enabled
namespace's identical pod is not.

**P5 — Waypoint fleet enablement.**
In `fleet-registry/tenants/waypoint/dev/values.yaml`:

```yaml
capabilities:
  burstCompute:
    enabled: true
    maxConcurrentJobs: 2
    quota: { requests.cpu: "4", requests.memory: 8Gi, pods: "2" }

workloadRbac:
  - serviceAccount: sdk-workload-sa
    rules:
      - apiGroups: ["compute.nutgraf.in"]
        resources: ["ephemeraljobs"]
        verbs: ["get", "list", "watch", "create", "delete"]
      - apiGroups: ["compute.nutgraf.in"]
        resources: ["ephemeraljobs/status"]
        verbs: ["get"]
```

This is the same Tier-2 mechanism that already grants the SDK
`agents.x-k8s.io/sandboxes`.
*Exit:* `sdk-workload-sa` can create an `EphemeralJob` in `tenant-waypoint` and
cannot create one in any other namespace.

**P6 — Image pull on cold nodes.**
Every burst node starts with an empty image cache. The tenant's
`ghcr-pull-secret` (rendered by the universal-tenant chart) must be referenced
by the job pod spec; the controller propagates `imagePullSecrets` from the
`EphemeralJob`.
*Exit:* the workload image pulls on a freshly joined node with no manual
credential step.

## What Waypoint changes

**Exactly one function body**: `submitEphemeralJobStep` in
`packages/waypoint-sdk/src/transition/system/submit-ephemeral-job/index.ts`.

- **Unchanged:** `SubmitEphemeralJobInputSchema`, so the workflow JSON's config
  keys keep parsing; `SubmitEphemeralJobResultSchema`; hook registration and the
  `/webhooks/resume` path.
- **Replaced:** `insertEphemeralJob` + `enqueueEphemeralProvisioning` become a
  Kubernetes `create` of an `EphemeralJob` CR, using the same in-cluster client
  pattern already proven in `packages/sandbox-k8s`.

No other Waypoint file changes for this slice. `ephemeral_jobs`, the Graphile
task and `operators/ephemeral-provisioner` are **left in place and idle** —
their deletion is WS5 of the alignment plan, not this slice.

## Contract mapping

How each key the workflow already sends is honoured:

| Workflow config key | Lands as | Note |
|---|---|---|
| `image` | `EphemeralJob.spec.image` | validated against the fleet's allowed registry prefixes |
| `resources.cpu` / `.memory` | pod `resources.requests` | drives which burst node size the scheduler needs |
| `input` | `spec.input.objectRef` | env-projected with the names the image already reads |
| `output` | `spec.output.objectRef` | controller composes `output.s3_url` for the resume payload |
| `callbackBaseUrl` | `spec.completion.webhook` | now an **in-cluster** call, controller → BFF (*Finding 4*) |
| `timeoutSeconds` | `spec.timeoutSeconds` | execution budget only (*Finding 3*) |
| `serverType` | advisory | one burst pool exists today (*Finding 2*) |
| `storage` | **not mapped** | must be empty for this slice (*Finding 7*) |

## Findings this slice forces

**Finding 1 — ClusterClass topology fights the autoscaler. CONFIRMED on the
live dev spoke, 2026-08-23.** `md-0` is generated from
`Cluster.spec.topology`, where `replicas: 0` is hardcoded. Server-side apply
ownership on `spoke-pool-hybrid-dev-01-md-0-wxw9t` reads:

```
manager=capi-topology  op=Apply   owns f:replicas -> True
manager=manager        op=Update  owns f:replicas -> False
```

and the object carries `topology.cluster.x-k8s.io/owned`. The `Cluster` itself
shows field managers `capi-topology` **and** `crossplane-kubernetes-provider`,
confirming the Crossplane → provider-kubernetes → CAPI path is live.

So an autoscaler patch to `spec.replicas` is reverted on the next topology
reconcile: scale-up, revert to `0`, pod never schedules, and **no error appears
anywhere** — the autoscaler reports success and the pod stays `Pending`.

The topology entry must **omit** `replicas` and carry the autoscaler bounds
annotations instead, so the topology controller yields replica ownership.

*Zero-cost verification* — no machine is created by this check:

```
kubectl get machinedeployment <md> -n platform-capi --show-managed-fields -o json \
  | jq '.metadata.managedFields[] | {manager, owns: (tostring|contains("f:replicas"))}'
```

P1 is done when `capi-topology` no longer owns `f:replicas`.

**Finding 1b — omitting `replicas` from the composition base is not enough, and
CAPI enforces the rule itself.** Verified 2026-08-23 by landing the change and
watching it fail. Two things the first attempt got wrong:

1. A `FromCompositeFieldPath` patch re-injects the field:
   `spec.nodePool.count → ...machineDeployments[0].replicas`. Removing it from
   the base leaves the patch, so the rendered `Object` still carried
   `replicas: 0`. **Both** must go together.
2. `provider-kubernetes` applies the `Cluster` with a full **Update** (field
   manager `crossplane-kubernetes-provider`, `op=Update` — not server-side
   apply), so a rejected manifest fails the *entire* object update, not just the
   offending field.

The rejection is authoritative and is worth quoting, because it is CAPI
independently asserting this ADR's §2:

```
admission webhook "validation.cluster.cluster.x-k8s.io" denied the request:
machineDeployments[md-0].replicas: Invalid value: 0: cannot be set
  ... if the same MachineDeploymentTopology has autoscaler annotations
```

Consequences to carry into P1:

- `replicas` and the autoscaler bounds are **mutually exclusive by CAPI's own
  validation**. There is no partial migration: the base field, the patch, and
  the annotations move in one commit or not at all.
- A half-landed change **wedges the Cluster**. While rejected, the `Object` sits
  `Synced=False (ReconcileError)` and *no* Crossplane update to that Cluster
  lands — including unrelated ones. The spoke keeps running (`Ready=True`), so
  the failure is quiet unless the `Object` condition is checked.
- `spec.nodePool.count` stops driving the burst pool. Whatever consumes that
  field must be retired in the same change.

**Status: deferred.** The annotations were reverted (`d4955289`) because
`spec.nodePool.count → replicas` is under separate test. P1 resumes when that
work lands, and then changes base, patch and annotations together.

**Finding 2 — there is one burst size class, not many.** The ClusterClass
defines a single `default-worker` class, and machine type comes from the
SpokePool variable `workerMachineType` (`cx33` for dev hybrid). The workflow's
`serverType: cpx22` therefore cannot be honoured per job. For this slice it is
advisory and the pool size governs. Multiple size classes require a second
MachineDeployment class in the ClusterClass — out of scope here, and a good
reason not to promise per-job sizing in the API yet.

**Finding 3 — the job timeout must not start at submission.** Cold capacity
acquisition is minutes (server create, boot, `kubeadm join`, image pull). If
`timeoutSeconds` runs from submission, a cold scale-up consumes the job's
execution budget and the job fails having never run — the same defect shape as
the current implementation, which anchors `started_at` at claim time. The CRD
needs two clocks: `provisioningTimeoutSeconds` (submission → pod Running) and
`timeoutSeconds` (pod Running → terminal). Only the second is what the workflow
author is setting.

**Finding 4 — the completion callback becomes an in-cluster call, and gets
safer.** Today the workload VM calls a public BFF endpoint over the internet,
carrying a token. Under ADR-052 the **controller** observes Job completion and
calls back over cluster DNS. The workload container never handles the callback
token and never needs egress to the BFF. `callbackBaseUrl` continues to work
unchanged; its resolution just moves in-cluster.

**Finding 5 — burst workers already satisfy ADR-052 placement.** Verified in
`spokepool-worker-bootstrap-v1`: the worker `KubeadmConfigTemplate` sets
`nodeRegistration.kubeletExtraArgs` to
`cloud-provider: external` and `node-labels: workload-location=hetzner`, and
declares **no** `postKubeadmCommands`. Two consequences, both favourable:

- a burst node self-labels `workload-location=hetzner`, so the ADR-052 §4
  nodeSelector matches with no change to the ClusterClass;
- because the worker template does not untaint, a burst node keeps
  `node.cloudprovider.kubernetes.io/uninitialized` until CCM initialises it, so
  `providerID` is set, CAPI populates `Machine.status.nodeRef`, and the
  autoscaler can correlate the node to its node group.

**Finding 6 — the control-plane node has no `providerID`, and this masks the
above.** On the live spoke, `Machine spoke-pool-hybrid-dev-01-cx9nj-6qgqq`
reports `NodeHealthy: False — Waiting for a node with matching ProviderID`, and
the node has `providerID: <none>`. Cause:
`_shared/spokepool-clusterclass-v1.yaml:349` runs
`kubectl taint nodes --all node.cloudprovider.kubernetes.io/uninitialized-` in
the **control-plane** `postKubeadmCommands`. The comment records the intent —
"unblocks scheduling if the CCM lags" — but removing the taint also removes
CCM's trigger to initialise the node, so `providerID` is never set.

Because it runs only at control-plane bootstrap, it affects only nodes existing
at that moment. Burst workers joining later are untouched (Finding 5). The
practical impact is therefore **not** a blocker for this slice, but it must be
understood before trusting the autoscaler: the one Machine↔Node correlation
currently observable on this spoke is broken, so it cannot be used as evidence
that correlation works. P0 below establishes that evidence properly.

**Finding 7 — `storage` has no home yet.** The workflow passes
`storage` through to the step, and ADR-052 deliberately exposes no volume, disk
or model field pending the deferred storage decision. **The test trigger payload
must omit `storage`, and the chosen workload must need no model volume.** A job
that needs one is testing the deferred decision, not the burst path. Note that
burst nodes are ordinary cluster nodes with CSI available, so that decision has
better options than it did — but it is still a separate decision.

## Procedure

1. Confirm the precondition: `kubectl get nodes -l workload-location=hetzner`
   on the spoke returns nothing.
2. Trigger the workflow with a payload that sets `image`, `input`, `output`,
   `callbackBaseUrl`, `sessionId`, `outputFilename`, `outputFilepath`,
   `outputFormat`, `outputMediaType`, `appId`, `notifyMessage` — and **omits
   `storage`**.
3. Walk the eight checkpoints in order, recording the wall-clock time at each.
4. After terminal state, confirm the burst node is reclaimed within
   `scaleDownUnneededAfter`.

**Expected timings** — record actuals; they set `minNodes` and
`provisioningTimeoutSeconds` for real:

| Segment | Expectation |
|---|---|
| CR created → pod `Pending` | seconds |
| `FailedScheduling` → autoscaler scale-up | under a minute |
| Machine create → node `Ready` | minutes — server provision, boot, container runtime install, `kubeadm join` |
| Node `Ready` → pod `Running` | image pull on a cold cache; size-dependent |
| Terminal → node reclaimed | `scaleDownUnneededAfter` |

## Failure triage

| Symptom | Layer | Likely cause |
|---|---|---|
| No CR appears | Tenant | RBAC (P5), or the step still writing to `ephemeral_jobs` |
| CR `Pending`, no Job | P3 | Controller not watching, or CRD not established |
| Pod `Pending`, no `FailedScheduling` | P4 | Kyverno did not mutate — namespace not enabled |
| `FailedScheduling`, replicas stay `0` | P2 | Autoscaler cannot reach the hub, or node group not discovered |
| Replicas go `1 → 0` unprompted | **P1 / Finding 1** | Topology controller reverting; `replicas` still set in topology |
| Node `Ready`, pod still `Pending` | P4 | Taint/toleration or nodeSelector mismatch |
| Pod `ImagePullBackOff` | P6 | `imagePullSecrets` not propagated to the job pod |
| Job succeeds, workflow never resumes | P3 / Finding 4 | Callback not fired, or hook token mismatch |
| Job fails at the timeout having never run | **Finding 3** | Single clock — provisioning consumed the execution budget |

## Known-broken adjacent state

`hcloud-csi-controller` on the dev spoke is in `CrashLoopBackOff` (234 restarts
as of 2026-08-23). This slice provisions no `PersistentVolumeClaim`, so it is not
on the path — Finding 7 keeps storage out. It is recorded here so that a CSI
failure encountered during the slice is recognised as pre-existing rather than
newly caused.

## Out of scope

Sandbox placement onto burst (alignment plan WS4), deleting the legacy
implementation (WS5), metering (WS6), and the storage decision (Finding 7).
This slice proves one workflow, one job, one node.
