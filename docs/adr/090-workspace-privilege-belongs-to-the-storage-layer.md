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

Restore-on-start becomes `NodePublishVolume`; the final checkpoint becomes
`NodeUnpublishVolume`, which also restores the ordering guarantee the native
sidecar was chosen for — the kubelet unpublishes after the workload's containers
have exited, so the tree is read only once it has stopped being written. The
120-second periodic checkpoint becomes a timer in the node plugin, which already
holds the mount.

Workspace identity (`appId`, `workspaceId`, `sharedWorkspaceId`, `checkpointId`,
`checkpointIntervalSeconds`) travels as volume attributes on an ephemeral inline
volume, which is what `EphemeralJob.spec.workspacePersistence` already carries.

### What does not map to CSI, and is not pretended to

The sidecar also serves a **loopback control API** on `:7070` — `/checkpoint`,
`/flush`, `/restore`, `/list-checkpoints` — and harness-runtime proxies
`/workspace/checkpoint` to it. An agent calls it at a task boundary, which is the
point of a boundary checkpoint rather than a timer.

CSI has no data-plane API for a workload. So this becomes a **workspace control
service**, reached over the network and authenticated per session, rather than a
container in the Pod. It needs no privilege of its own: a checkpoint requires
walking the merged tree, which exists only at the node's mount, so the service
delegates to the node plugin on that node.

This is the honest shape. A design claiming pure CSI would have to either drop
boundary checkpoints or smuggle the API back into the Pod.

### Consequences

**Positive.** Privilege is held by one DaemonSet per node, reviewable in one
place, running no tenant code — instead of two privileged containers in every
sandbox Pod. `restricted` holds for every Pod in a tenant namespace with no
exemption anywhere. A sandbox Pod's spec becomes something a fleet could read and
verify.

**Negative.** A CSI driver is materially more work than a sidecar, and it is
platform code on the node's critical path: a crash-looping node plugin takes out
every sandbox on that node, where a broken sidecar took out one Pod. The control
service is a new network-reachable component with its own authentication.
Rollout is per-node, so a cluster mid-upgrade runs two mechanisms.

**Unresolved.** Whether the shared workspace — today a second sidecar mounting
inside the session's tree — is a second volume or a subpath of the first. The
nesting exists so a session's snapshot can skip the shared tree; expressing that
across two CSI volumes needs design that is not in this ADR.

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
- Kubernetes Volumes / CSI; Mountpoint for Amazon S3 as a CSI driver
