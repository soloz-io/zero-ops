# ADR-052 Burst Execution — implementation plan

**Date:** 2026-08-25
**Status:** Architecture approved; execution gated on E0–E2.
**Supersedes for scope:** the WS-numbered work streams in `adr-052-alignment.md`
**Relates to:** ADR-005, ADR-012, ADR-021, ADR-031 (waypoint), ADR-036, ADR-039,
ADR-041, ADR-043, ADR-046, ADR-047, ADR-052

ADR-052 is the decision. This plan is implementation state and carries no
architectural authority; where the two disagree, ADR-052 controls — except on
the points in §Amendments required, which ADR-052 does not yet cover.

---

## Goal

Waypoint submits batch work through a platform abstraction, and that work
executes on Hetzner burst capacity. The platform owns placement, bounds and
capacity; the fleet owns only the request.

## Scope boundaries — decided, not negotiable within this plan

**Retirement is out of scope.** This plan ends at E8, a coexistence state.

| Artefact | Disposition |
|---|---|
| `oranger/services/ephemeral-worker` | untouched — not modified, not deleted, not invoked by the new path |
| `waypoint/operators/ephemeral-provisioner` | untouched — left present and operational |
| `ephemeral_jobs` table, Graphile task, `ephemeral-worker.ts` | untouched — left present and reachable |
| `HCLOUD_TOKEN` | **not revoked** |

E8 is therefore **not** "ownership transfer complete." It is *platform path
validated and the waypoint workload path migrated; legacy provisioner retirement
deferred.* Saying otherwise would claim a boundary that does not exist while the
old provisioner still holds a cell-wide cloud credential.

Retirement is a separate lifecycle change: quiesce → observe → remove
invocations → revoke `HCLOUD_TOKEN` → delete provisioner → remove orphan sweep.

**Coexistence is safe.** Verified: `internal/hetzner/client.go:181` filters
server-side on `label_selector=oranger.dev/managed-by=ephemeral-provisioner`.
CAPH-provisioned burst nodes do not carry that label, so the orphan sweep cannot
enumerate or delete them. Neither system can reclaim the other's machines.

---

## Decisions locked

### D1 — The controller stamps classification; admission owns placement

The `EphemeralJob` controller sets exactly one placement-adjacent field on the
Job podTemplate:

```yaml
labels:
  compute.nutgraf.in/burst-class: "true"
```

That is workload *identity* — it answers "does the platform contract place this
workload in the burst execution class?" The controller owns it because it owns
the `EphemeralJob` state machine.

The controller MUST NOT set `nodeSelector`, `tolerations`, `priorityClassName`
or node affinity. Those are platform admission policy. This preserves ADR-041
without amendment.

```
EphemeralJob controller
    └─ burst-class=true
         └─ Kyverno mutate: workload-location, toleration, PriorityClass
              └─ Kyverno validate: burst-class ⇒ Hetzner placement present
                   └─ scheduler: burst nodes only
```

### D2 — The security invariant rests on RBAC, and must be asserted

D1 holds only if a fleet cannot create execution Pods or Jobs directly — either
forging `burst-class` onto a home-lab pod, or omitting it to escape burst
placement onto home-lab capacity.

> **Invariant:** no fleet workload identity may hold `create` on `pods` or
> `jobs` in its own namespace.

Waypoint satisfies this today by accident, not by assertion: `dev/values.yaml`
grants `sandboxes`, `pods` get/list, `pods/log` get, `services`
create/get/delete — and `universal-tenant/templates/rbac.yaml` renders exactly
and only what `workloadRbac` declares, with no implicit grants. **This becomes a
preflight check.** The day a fleet's `workloadRbac` gains `jobs: [create]`,
Hetzner-only silently stops being enforceable.

### D3 — Immutable digests are a fleet/platform publishing contract

The controller does **not** resolve tags to digests. That would pull registry
credentials, network dependency, tag-mutability semantics and supply-chain logic
into a workload controller.

The ABI is not weakened and gains no burst exception. Images arrive as digests.

### D4 — The burst node template is one value, rendered twice

Scale-from-zero requires the autoscaler to model a node that does not exist. The
same platform-rendered value must feed both the ClusterClass/MachineDeployment
and the CA annotations:

```
Burst node definition (single source)
├── workload-location=hetzner          (label)
├── workload-class=burst:NoSchedule    (taint)
├── cpu / memory / maxPods
└── other scheduler-relevant attributes
```

→ `capacity.cluster-autoscaler.kubernetes.io/{cpu,memory,labels,taints}`

**This coupling is a correctness requirement, not documentation.** A taint
applied to the real node but absent from the annotations makes the autoscaler
simulate a node the pending pod cannot tolerate; it concludes scale-up will not
help and declines to scale. The pod pends forever and the autoscaler logs no
error. Two hand-maintained strings for one taint is a silent-outage generator.

### D5 — Fail-closed placement with deliberate recovery exclusions

Validation runs at `failurePolicy: Fail`. This is not an argument for `Ignore`;
it is an argument for narrow matching plus explicit exclusions for
cluster-critical recovery components. See Finding 3 — the current cluster gets
this wrong today, independently of this plan.

---

## Findings — verified on the live dev spoke, 2026-08-25

Evidence gathered read-only via `spoke-pool-hybrid-dev-01-kubeconfig`, plus one
server-side dry-run that persisted nothing.

### Finding 1 — Gate 0 was abandoned after one failed attempt, and is inert to fix

| Commit | Date | Effect |
|---|---|---|
| `ee2ddd97` | Aug 12 | XRD `nodePool.count` minimum relaxed to 0. **Merged to main.** |
| `bac465a9` | Aug 23 20:47 | Added CA bounds annotations, left the replicas patch. CAPI rejected the combination; the Cluster `Object` sat `Synced=False` from 15:23:40Z, blocking *every* Crossplane update to that Cluster. |
| `0557c269` | Aug 23 21:26 | Reverted. Composition byte-identical to pre-ADR-052. |

Nothing since, on any branch. The "nodePool.count work under test" referenced in
the revert message was `ee2ddd97`, which had already landed eleven days earlier.

**The change is behaviourally inert for the dev claim.**
`spoke-pools/dev/hybrid/hybrid-dev.yaml:41` sets `nodePool.count: 0`, and the
composition base is already `replicas: 0`. The patch writes the value the base
already holds. Removing both changes no replica count — it changes only *who
owns the field* (`capi-topology` yields `f:replicas`) and adds the annotations.
No replica transition, no node churn, no drain.

The residual risk is procedural, and it is the one `0557c269` documented: a
rejected manifest fails the entire `Object` update, because
`provider-kubernetes` applies the Cluster with a full **Update**, not a
server-side apply. Base field, patch and annotations move in one commit, and the
`Object`'s `Synced` condition is checked immediately after.

*Open sub-decision:* `nodePool.count` is `required` in the XRD and stays
meaningful for the pure-hetzner composition, but becomes required-and-inert for
hybrid. Leave the wart, or make the XRD provider-conditional.

### Finding 2 — The image ABI is real, enforced, and has never met the sandbox path

`enforce-tenant-abi` is `validationFailureAction: Enforce`, `READY=True`, age 30h.
The only running tenant workloads are digest-pinned:

```
tenant-oranger/oranger-serve-workload-*
  ghcr.io/soloz-io/oranger/oranger-serve@sha256:c99b566e...
```

`tenant-waypoint` contains **zero pods**. That is why no contradiction was
visible: the sandbox path has never run on this spoke.

Server-side dry-run of a pod mirroring the real sandbox `agent-vault` sidecar
(`infisical/agent-vault:0.38.0`, correct `securityContext`, correct labels):

```
admission webhook "validate.kyverno.svc-fail" denied the request:
enforce-tenant-abi:
  require-image-digest: 'validation error: Container images must reference
    immutable sha256 digests, not mutable tags.
    rule require-image-digest failed at path /spec/containers/0/image/'
```

**Consequence beyond the burst path.** This blocks the *sandbox* path too, and
that is independent of ADR-052. Three call sites need digests:

1. `sandbox-k8s/src/index.ts` — `K8S_AGENT_VAULT_IMAGE`, default
   `infisical/agent-vault:0.38.0`, and the harness image.
2. `submit-ephemeral-job/index.ts` — image resolves from `parsedInput.image`,
   then `HETZNER_*_IMAGE` env, then a hardcoded
   `ghcr.io/soloz-io/tts-worker:git-def09ac8328f` fallback. All three paths.
3. Whatever publishes the oranger worker images must emit digests into workflow
   definitions and env. This has an owner outside `zero-ops` and is the likely
   long pole on E5.

D3 is confirmed as the right call — the invariant is genuinely held, not
aspirational. But it costs the vertical-slice doc's success criterion 1: the
workflow JSON *is* the fixture, and it must now be edited to carry digests.

### Finding 3 — Cluster-wide admission depends on one pod, today (pre-existing)

Investigating D5 surfaced a live availability risk unrelated to burst work:

```
kyverno-resource-validating-webhook-cfg
  webhook:        validate.kyverno.svc-fail
  failurePolicy:  Fail
  objectSelector: {}
  namespaceSelector: NotIn [kube-system], NotIn [kyverno]
  rules:  ""            pods, pods/ephemeralcontainers, replicationcontrollers
          apps          daemonsets, deployments, replicasets, statefulsets
          batch         cronjobs, jobs
          argoproj.io   rollouts
  operations: CONNECT, CREATE, DELETE, UPDATE
```

```
kyverno-admission-controller   replicas=1   PDB: none
  scheduled on spoke-pool-hybrid-dev-01-5kk7h-qsczz (the control-plane node)
```

If that single pod is unavailable, **every workload create, update and delete
across the cluster fails** — `platform-ops`, `argocd`, `crossplane-system`,
`platform-capi`, `cert-manager`, `external-secrets` — everything except
`kube-system` and `kyverno`. `DELETE` is in the operations list, so the cluster
cannot be recovered by removing workloads either.

This is not introduced by this plan and should not wait for it. E4 formalises
the exclusion set; the replica count and PDB are worth fixing sooner.

### Finding 4 — the sandbox Service move must not disturb local development

`packages/sandbox-k8s/src/index.ts` serves two environments from one code path,
branching on `inCluster()` (`process.env.IN_CLUSTER === 'true'`):

| Environment | Service type | Reached at |
|---|---|---|
| in-cluster | `ClusterIP` | `http://<svc>.<ns>.svc.cluster.local:3000` |
| local (kind) | `NodePort` | `http://${K8S_NODE_HOST}:${nodePort}` |

The vendored CRD's `spec.service` is a bare **boolean** — no service-type
selection. A controller-created Service will therefore be `ClusterIP`, which a
kind host cannot reach directly. Moving both environments to `spec.service: true`
would silently break local sandbox development, and it would break it in
`waitHttpReady`, which is the slowest and least obvious place to diagnose.

**Decision: the two implementations stay separate.**

```
provision sandbox networking
├── inCluster()  → spec.service: true, read status.serviceFQDN   (NEW)
└── local        → existing NodePort Service creation            (UNCHANGED)
```

The existing NodePort code is not deleted, not refactored and not shared with
the new path. `readNodePort`, `serviceName` and the local branch of
`waitSandboxReady` / `waitHttpReady` keep working exactly as they do today. The
new in-cluster path is added alongside them, and the two never interleave.

This costs nothing architecturally. The platform-ownership win is the RBAC drop,
and that is unaffected: the local path never executes under the tenant
ServiceAccount, so `services: create/get/delete` still comes out of
`tenants/waypoint/dev/values.yaml`. Ownership moves where it matters; local
development is left alone.

*Verification for E8:* local sandbox creation, readiness and teardown work
unchanged after the in-cluster path lands — run before enabling burst placement,
so a local regression is never confused with a placement failure.

---

## Execution steps and approval gates

The review used one numbering for execution order and another for approval
criteria; they conflicted at 5, 6 and 7. They are separated here.

### Execution — E0 … E8

| # | Step | Depends on |
|---|---|---|
| **E0** | Hybrid composition: remove base `replicas`, remove the `spec.nodePool.count` patch, add the full D4 annotation set — **one atomic commit**. Verify the `Object` reaches `Synced=True`. | — |
| **E1** | Prove Machine ↔ Node correlation: burst node reaches `Ready` with an `hcloud://` providerID, `Machine.status.nodeRef` populated, `NodeHealthy=True`. Scale via `Cluster.spec.topology`, not the MachineDeployment. | E0 |
| **E2** | Prove scale-from-zero *scheduling*: from `replicas: 0`, a pending burst-class pod causes node-group discovery, 0 → 1, correct label and taint on the joined node, pod binds. | E1 |
| **E3** | `BurstCapacity` XRD + composition. `minNodes: 1` — declared as validation/warm mode, not a substitute for E2. Burst pool taint lands here. | E2 |
| **E4** | Platform admission: `burst-tenant` PriorityClass; Kyverno mutate (location, toleration, priority; sandbox netpol label); Kyverno validate at `failurePolicy: Fail` with the D5 exclusion set. `capabilities.burstCompute` → priority-scoped `ResourceQuota`. | E3 |
| **E5** | `EphemeralJob` CRD + controller (new operator in `zero-ops/operators/`): state machine, `batch/v1` Job materialisation, `burst-class` label, split clocks, condition projection, in-cluster callback, TTL. Plus the **job-result sidecar** — see below. Plus the digest plumbing from Finding 2. | E4 |
| **E6** | Fleet enablement: `workloadRbac` for `ephemeraljobs`, `capabilities.burstCompute` in `tenants/waypoint/dev/values.yaml`. D2 preflight check lands here. | E5 |
| **E7** | Waypoint SDK: `submitEphemeralJobStep` gains a CR branch behind `EPHEMERAL_JOB_BACKEND=postgres\|crd`, default `postgres`. Existing path untouched and reachable; rollback is an env var. | E6 |
| **E8** | Sandbox migration and hardening: in-cluster path moves to `spec.service: true` + `status.serviceFQDN`, drop `services` RBAC, digests, onto burst. **The local path is not modified** — see Finding 4. Then soak both consumers. | E7 |

### The job-result sidecar

`worker.sh` is not just a launcher, and both prior plan docs miss this. It
injects `INPUT_S3_URL`, `OUTPUT_S3_PREFIX` and the four `S3_*` vars into the
workload container, then **uploads `result.json` and composes `output_s3_url`**
(`worker.sh:194`, `216–232`). The blueprint reads exactly that value via
`{{@transition-submit-job:Submit Ephemeral Job.output.s3_url}}`.

The controller observing Job completion does not cover this. Since the workload
image must not change and `ephemeral-worker` must not be touched, the guest
agent's role becomes a **platform-owned sidecar** in the Job pod: shared
`emptyDir`, uploads `result.json`, controller reads terminal state and calls
back. New component in `zero-ops`; `oranger/services/ephemeral-worker` is simply
no longer invoked.

### Approval gates — G0 … G8

Applied to whichever execution step delivers them.

| Gate | Requirement |
|---|---|
| G0 | Single-source burst node template; CA scale-from-zero cpu/memory/labels/taints |
| G1 | Machine ↔ Node correlation proven |
| G2 | **Actual scale-from-zero scheduling proven**, not merely node provisioning |
| G3 | Burst classification owned by `EphemeralJob`; placement owned by Kyverno |
| G4 | Fail-closed validation **plus** deliberate platform recovery exclusions |
| G5 | Tenant RBAC invariant tested: no direct Pod/Job creation bypass |
| G6 | Immutable-image ABI verified against the live sandbox path |
| G7 | Both Sandbox and EphemeralJob proven on Hetzner-only capacity |
| G8 | Soak with the legacy provisioner present but unused by the migrated path |

G6 is **discharged** by Finding 2. G0's evidence base is established by
Finding 1; the change itself is not yet made.

---

## Amendments required to ADR-052

The ADR is clear that tenant-demand workloads must burst (§10) and that the
elastic pool is Hetzner in the current topology (§0, §2). It is **not** clear as
an enforced prohibition, and it deliberately keeps `placementClass`
provider-generic (§2 sample value, §4 `<placementClass>` template, §Impact
handing burst size classes to ADR-036). Nothing in it says a tenant job may
never schedule on a home-lab node, and §4's validating policy rejects only
*fleet-authored* tolerations and `priorityClassName` — nothing rejects a pod that
merely lacks the burst nodeSelector.

1. **Scale-from-zero discoverability** becomes an explicit contractual invariant
   (D4). A zero-replica burst MachineDeployment must be autoscaler-discoverable
   without an existing Node.
2. **§4 gains the burst taint**, which it currently references but which does not
   exist in the ClusterClass. Placement is a label *and* a taint, symmetrical.
3. **Hetzner-only is a correctness invariant**, not a placement preference —
   with the classification/placement split (D1) and the RBAC invariant (D2)
   named as its enforcement.
4. **ADR-031 (waypoint) gains a supersession note.** Its Ownership table still
   names `ephemeral-provisioner` as reconciler of ephemeral compute. Its
   Kubernetes-agnosticism rationale no longer holds independently — the same SDK
   already holds an in-cluster client for `agents.x-k8s.io/sandboxes`.

---

## Open decisions

1. **`nodePool.count` for hybrid** — leave required-and-inert, or make the XRD
   provider-conditional (Finding 1).
2. **Finding 3 remediation timing** — fold the exclusion set into E4, or fix
   replicas/PDB/exclusions now as an unrelated availability item.
3. **Burst size classes.** The ClusterClass defines a single `default-worker`;
   machine type comes from the `workerMachineType` SpokePool variable (`cx33` on
   dev hybrid). The workflow's `serverType: cpx22` cannot be honoured per job.
   Advisory for now; per-job sizing must not be promised in the API until a
   second MachineDeployment class exists.
4. **Storage.** Unchanged from `adr-052-alignment.md`: the `EphemeralJob` API
   exposes no volume, disk or model field. Burst nodes are ordinary cluster
   nodes with CSI available, so the options are better than before — but Hetzner
   CSI is `ReadWriteOnce` and a read-many model cache still needs a design. Test
   payloads must omit `storage`.
