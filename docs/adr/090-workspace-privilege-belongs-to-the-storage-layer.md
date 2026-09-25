# ADR-090: Workspace Privilege Belongs to the Storage Layer, Not the Sandbox Pod

**Status:** Accepted
**Date:** 2026-09-25
**Amends:** ADR-052 §14 / §14.2 (the workspace-sync sidecar)
**Upholds:** ADR-021 Blocker 4 (PSA `restricted` on tenant namespaces)

## Context

A sandbox that asks for a durable workspace cannot start. The EphemeralJob is
created, the operator retries forever, and no Pod is ever admitted:

```
pods "ej-sandbox-playground-…" is forbidden: violates PodSecurity "restricted:latest":
  privileged            (workspace-sync must not set privileged=true)
  allowPrivilegeEscalation != false   (workspace-sync)
  unrestricted capabilities           (workspace-sync, workspace-sync-shared)
  runAsNonRoot != true                (workspace-sync, workspace-sync-shared)
  runAsUser=0                         (workspace-sync, workspace-sync-shared)
```

The caller sees a step that never returns. Nothing reports a policy decision.

Two decisions, each defensible alone, contradict:

- **ADR-021 Blocker 4** puts `pod-security.kubernetes.io/enforce: restricted` on
  every tenant namespace, and says no workload can bypass it.
- **ADR-052 §14.2** adds a `workspace-sync` sidecar that needs `/dev/fuse`, the
  setuid `fusermount3`, and mount propagation — so it runs `privileged`, as
  `runAsUser: 0`. Its own comment concedes the point: *"the choice is privileged
  or no FUSE, and the ADR should say so rather than describe a middle ground
  that does not exist."*

It said so, and then placed the privileged container inside a Pod that the
`restricted` profile governs.

## The distinction this ADR exists to record

> **Do not classify the sandbox Pod as trusted merely because the Pod spec was
> authored by the platform. Classify the storage infrastructure as trusted, and
> keep the sandbox workload unprivileged.**

Pod Security Admission evaluates the **whole Pod**. A sandbox Pod runs tenant
code — that is its entire purpose. Adding a privileged sidecar beside that code
does not produce "a platform Pod with a tenant container"; it produces a
privileged Pod containing untrusted workload, and moving it to a namespace whose
policy permits privilege only relocates the problem.

Authorship of a Pod spec is not a trust boundary. What runs in the Pod is.

## Rejected

**Lower the tenant namespace to `baseline`.** Does not even work: Baseline blocks
`privileged` too. It would void ADR-021 Blocker 4 for every fleet-authored Pod in
exchange for nothing.

**A platform-owned namespace at `privileged`.** The Pod still contains tenant
code. This is the proposal the distinction above was written against.

**A PSA exemption for the operator's ServiceAccount.** Mechanically wrong as well
as too broad: PSA exempts usernames, RuntimeClasses and namespaces, and Kubernetes
states that exempting the identity which creates a *workload* does not exempt the
Pods a controller subsequently creates. The chain here is
operator → EphemeralJob → Job controller → Pod, so the operator's identity is not
the identity that creates the Pod.

**Drop `persistent_workspace` from the agent definition.** A capability
regression. Admissible as a labelled diagnostic to isolate a failure; never as
the architecture.

## Decision

**The privileged operation moves out of the sandbox Pod. The workspace becomes a
Kubernetes volume, and the privilege lives in a platform-owned node plugin.**

```
tenant namespace, PSA restricted
┌──────────────────────────────┐
│ sandbox Pod                  │
│   tenant workload            │
│   runAsNonRoot, no privilege │
└──────────────┬───────────────┘
               │  volume mount
               ▼
        CSI node plugin            ← privileged, platform-owned,
        DaemonSet, no tenant code     one per node, not per sandbox
               │
               ▼
        object store (S3)
```

This is what Kubernetes' storage architecture is for: mounting a filesystem is a
node-level operation, so node plugins are privileged infrastructure. AWS ships
Mountpoint for S3 as a CSI driver rather than as a sidecar in every consuming Pod,
for this reason.

Both original decisions survive intact. ADR-021 keeps "no tenant workload bypasses
`restricted`". ADR-052 keeps "the platform provides durable and shared workspaces".
What changes is only *where* the privilege sits.

### What maps to CSI, precisely

The current implementation is not a live S3 mount. `operators/workspace-sync`
keeps a content-addressed store:

```
<appId>/<workspaceId>/objects/<sha256>        immutable file content
<appId>/<workspaceId>/checkpoints/<id>.json   a manifest naming the tree
```

and performs four privileged operations, all of which are node operations:

| operation | CSI equivalent |
|---|---|
| `squashfuse` mount of the restored archive | `NodePublishVolume` |
| `fuse-overlayfs` writable layer over it | `NodePublishVolume` |
| `os.Chown(root, uid, uid)` handing the tree to the workload | `fsGroup` / CSI fsGroup policy |
| `HostToContainer` mount propagation into the workload | unnecessary — the kubelet mounts the volume |

CSI volumes are a permitted volume type under the Restricted profile, which is
what makes this a solution rather than a relocation of the problem.

### Checkpointing is a platform contract, not a CSI semantic

This distinction is load-bearing and easy to lose.

CSI defines `NodeUnpublishVolume` as removal of a volume publication. It defines
nothing about application checkpoints. Durability here is **the platform's
contract**, and the ADR must not describe it as something CSI provides.

So: **`NodeUnpublishVolume` is the teardown boundary at which the driver performs
a final flush and checkpoint before removing the publication.** The checkpoint
operation is platform-defined.

```
RUNNING
  ├── periodic checkpoint          (driver-owned, per publication)
  └── boundary checkpoint          (requested, see below)

TERMINATING
  ├── final flush / checkpoint     (platform-defined)
  ├── NodeUnpublishVolume          (CSI)
  └── volume unmounted
```

Using that boundary does preserve the ordering the native sidecar was chosen for
— the kubelet unpublishes after the workload's containers have exited, so the
tree is read only once it has stopped being written.

**Every checkpoint operation MUST be idempotent.** CSI RPCs are retried and
reconciled; a final checkpoint that runs twice must produce one outcome, and a
retried publish must not duplicate or corrupt a manifest. The store is already
content-addressed, which makes this cheap to honour rather than merely required.

**Periodic checkpointing is driver-owned.** The node plugin maintains a
per-volume checkpoint worker for each active publication; while a publication is
active, that worker may checkpoint the mounted workspace on the configured
interval. This is the driver's design, not a CSI behaviour, and the ADR does not
tie itself to any particular process model for it.

### The boundary-checkpoint API is a Kubernetes resource, not a network endpoint

The sidecar today serves a loopback control API on `:7070` — `/checkpoint`,
`/flush`, `/restore`, `/list-checkpoints` — and harness-runtime proxies
`/workspace/checkpoint` to it. An agent calls it at a task boundary, which is the
point of a boundary checkpoint rather than a timer. CSI has no data-plane API for
a workload, so this needs somewhere else to live.

It does **not** become an HTTP endpoint on the privileged node plugin. That would
create a second privileged API surface whose authorization and node-routing
semantics the platform would then own, on the one component that must stay
minimal.

Instead it is a Kubernetes resource:

```
harness-runtime
      │  (session-authenticated)
      ▼
Workspace Control API            unprivileged; the only creator of requests
      │
      ▼
WorkspaceCheckpointRequest       platform namespace, RBAC-restricted
      │
      ├──────────────┐
      ▼              ▼
node A agent     node B agent    watches only its own node's requests
      │
      ▼
verify workspaceID + nodeName + publicationID against locally published volumes
      │
      ▼
checkpoint  →  status on the request
```

A request carries an immutable workspace/session identity and an explicit target
node. The node-local agent watches only the trusted request stream and acts only
on publications it actually holds.

What this buys, each from Kubernetes rather than from new code: authentication
and authorization from the API server and RBAC, so only the control service may
create requests; routing from explicit node identity rather than service
discovery; reconciliation, because requests survive a process restart; and audit,
because requests and their statuses are ordinary objects.

### Invariant: tenant-authored resources select no storage configuration

Workspace identity (`appId`, `workspaceId`, `sharedWorkspaceId`, `checkpointId`,
`checkpointIntervalSeconds`) travels as volume attributes on a CSI ephemeral
inline volume, which is the shape `EphemeralJob.spec.workspacePersistence`
already carries.

That mechanism needs an explicit guard, because Kubernetes warns that inline
ephemeral CSI is inappropriate where attributes expose administrator-controlled
parameters, and recommends restricting which drivers may be used inline.

> **Tenant-authored resources MUST NOT be able to select CSI volume attributes,
> object-store locations, host paths, mount options, credentials, or any other
> driver configuration. Workspace attributes are generated exclusively by the
> platform operator from an authenticated workspace identity.**

The identifiers above are **opaque**. They name a workspace; they must never
become an indirect means of selecting an arbitrary storage resource. A fleet that
could choose the bucket, the prefix or the mount options would have recovered, by
another route, the privilege this ADR removes. Admission must additionally
restrict inline use of this driver to the platform operator.

### The shared workspace is a separate volume

Today a second sidecar mounts the shared tree *inside* the session's tree, so a
session's snapshot can skip it. That nesting is not carried forward.

> **The shared workspace is modelled as a separate CSI volume publication.
> Nested volume and subPath semantics are not part of the storage driver's
> persistence contract.**

```
sandbox Pod
├── /workspace          CSI volume: private workspace
└── /workspace/shared   CSI volume: shared workspace
```

Two publications with independent lifecycles are far easier to reason about than
a driver that must understand a nested filesystem topology, and it removes the
skip-list coupling between the two instances.

What this does **not** settle is the shared workspace's **consistency model**:
concurrent writers, who owns a checkpoint of a shared tree, conflict semantics,
and whether several sessions may publish the same shared workspace at once.
Those are real questions and they are not mounting questions. They get their own
decision; this ADR only fixes that sharing is expressed as a second volume rather
than as a nested mount.

### Consequences

**Positive.** Privilege is held by one DaemonSet per node, reviewable in one
place, running no tenant code — instead of two privileged containers in every
sandbox Pod. `restricted` holds for every Pod in a tenant namespace with no
exemption anywhere. The boundary-checkpoint path gains authentication,
authorization, routing, reconciliation and audit from the API server rather than
from a bespoke control plane. A sandbox Pod's spec becomes something a fleet
could read and verify.

**Negative.** A CSI driver is materially more work than a sidecar, and it is
platform code on the node's critical path: a crash-looping node plugin takes out
every sandbox on that node, where a broken sidecar took out one Pod. A boundary
checkpoint is now an API round trip and a watch rather than a loopback call, so
it is slower and its latency depends on the API server. There are two new
platform components — the control service and the node agent — plus a CRD.
Rollout is per-node, so a cluster mid-upgrade runs two mechanisms, and the
driver must be admission-restricted to the platform operator before it is
installed, not after.

**Deliberately deferred.** The shared workspace's **consistency model**:
concurrent writers, who owns a checkpoint of a shared tree, conflict semantics,
and whether several sessions may publish one shared workspace simultaneously.
This ADR settles only that sharing is a second volume rather than a nested
mount. The rest is a separate decision, and naming it here is not the same as
having taken it.

## Until it exists

Sandboxes that request a durable workspace do not start. That is the correct
behaviour for an unresolved contradiction: it fails closed, loudly, at admission,
rather than running tenant code beside a privileged container.

An agent definition may drop `persistent_workspace` to exercise the rest of the
path. That is a diagnostic, it is recorded as one, and it is not a fix.

## References

- ADR-021 — Blocker 4, PSA `restricted` on tenant namespaces; upheld
- ADR-052 §14, §14.2 — the workspace-sync sidecar; amended
- ADR-089 — the tenant baseline, the other thing a fleet gets without asking
- Kubernetes Pod Security Standards; Pod Security Admission (exemption dimensions)
- Kubernetes Pod Security Standards — CSI volumes are permitted under Restricted
- Kubernetes Volumes / CSI; Mountpoint for Amazon S3 as a CSI driver
- Kubernetes Ephemeral Volumes — the warning on inline CSI and admin-controlled
  attributes that the invariant above answers
- CSI specification (`csi.proto`) — `NodeUnpublishVolume` removes a publication
  and defines no checkpoint semantics
