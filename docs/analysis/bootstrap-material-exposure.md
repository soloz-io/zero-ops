# Control-plane bootstrap material is readable by any pod

**Status:** confirmed on a live cluster, 2026-10-12. Not remediated.
**Scope:** every CAPI cluster on this platform. Verified on `nutgraf-hub`.
**Independent of ADR-100.** ADR-100 changes one item in the exposed payload and four
others are untouched. Nothing in that ADR fixes this, and it must not be treated as
progress against it.

## What was found

`http://169.254.169.254/hetzner/v1/userdata` serves the server's original cloud-init
user-data over **unauthenticated HTTP**, for the life of the server. It is reachable from:

| from | reachable | verified |
|---|---|---|
| the host shell | yes | shares the link-local interface |
| a `hostNetwork: true` pod | yes | HTTP 200, 22,250 bytes |
| **an ordinary pod** | **yes** | plain TCP to `169.254.169.254:80` returned `HTTP/1.1 200 OK` |

The third is what makes this severe: Cilium's default routing does not filter link-local
egress, so an unprivileged workload with no host privileges reads it directly.

## What is in the payload

Four PEM private keys, matched by SHA-256 against the cluster's own certificate Secrets in
`platform-capi` on `nutgraf-hub` — **exact digest equality on all four**:

| path in payload | matching Secret | what it authorises |
|---|---|---|
| `/etc/kubernetes/pki/ca.key` | `nutgraf-hub-ca` | sign a client certificate in `system:masters` — unconstrained cluster-admin, bypassing all RBAC |
| `/etc/kubernetes/pki/etcd/ca.key` | `nutgraf-hub-etcd` | authenticate directly to etcd on 2379/2380; read, modify or destroy all cluster state without the API server |
| `/etc/kubernetes/pki/front-proxy-ca.key` | `nutgraf-hub-proxy` | sign request-header certificates — impersonate any user or group via the aggregation layer |
| `/etc/kubernetes/pki/sa.key` | `nutgraf-hub-sa` | mint valid RS256 service-account JWTs for any ServiceAccount in any namespace |

`enc.yaml`, carrying the secretbox key for at-rest etcd encryption, is in the same payload.

**Any one of the four is complete cluster takeover.** Digests are recorded in the T39 report
and deliberately not repeated here.

## Why it cannot be cleaned up in the guest

The payload is served **from the hypervisor**, not from the node. Nothing running on the
server can purge, expire, rotate or mutate it. Consequences:

- A `preKubeadmCommands` shred reaches the on-disk copy at
  `/var/lib/cloud/instance/user-data.txt` and **not** the reachable one.
- A file mode of `0600` on the written file is irrelevant.
- **A node that has ever booted with this user-data stays exposed until the node is
  replaced.** Rotating the keys and replacing the nodes are therefore the same operation,
  which is the main thing to know before scoping remediation.

Copies that persist regardless of anything done on the node: Hetzner's stored `user_data`
for the server's lifetime; the per-Machine bootstrap-data Secret on the hub, retained for
the Machine's whole life with an owner reference but no finalizer and nothing cleaning it
up; and cloud-init's own logs and the serial console, unverified.

## Why this is not a misconfiguration

KCP must place the cluster PKI in bootstrap data, because kubeadm cannot issue certificates
without it and CAPI needs the CA to build the admin kubeconfig. The platform did not choose
to put these keys in user-data; **CAPI puts them there by design, and the provider re-serves
user-data.** Any CAPI cluster on a provider that does so has this exposure.

That is worth stating because it sets where a fix can live: not in a value we are setting
wrongly, but in the delivery mechanism or in access to the endpoint.

## Containment, and what each layer does not cover

1. **Pod egress deny** (`manifests/providers/hetzner/k8s/deny-instance-metadata.yaml`,
   drafted, **not applied**) — closes ordinary pods, which is the path that makes this
   reachable by tenant workloads. Leaves the host and `hostNetwork` pods.
   Note: `hcloud-csi-node` is `hostNetwork: true`, so it needs no exemption; a namespace-wide
   exemption would be both unnecessary and a standing hole, and Cilium deny rules take
   precedence over allows, so an exemption must be expressed by not selecting the pod.
2. **Host-level block after bootstrap** — would close the host, `hostNetwork` pods and
   ordinary pods together. Open question: which consumers need the endpoint *ongoing* rather
   than only at bootstrap. A packet filter cannot distinguish `/hetzner/v1/metadata` from
   `/hetzner/v1/userdata`, so this is all-or-nothing per consumer.
3. **Eliminating the material from bootstrap data** — the only actual remediation, and the
   only one that addresses why tenant workloads can reach control-plane secrets at all.
   Not designed.

Layers 1 and 2 are blast-radius reduction. Neither removes the exposure, and neither helps a
node that has already booted.

## An ordering prerequisite, found while narrowing the policy

**The live `hcloud-csi-node` on `nutgraf-hub` is pod-networked, while the template says
`hostNetwork: true`.** So applying the pod-egress deny to the hub *first* would break volume
mounting cluster-wide. The policy is correct for the declared state and wrong for the running
one.

**The cause is systemic, not a one-off.** These addons ship as `ClusterResourceSet`s with no
`strategy`, which is CAPI's default **`ApplyOnce`**: applied when a cluster is created and
never again. `ccm-addon-template.yaml:43-47` already says so in as many words, and records a
live one-off patch on `nutgraf-01` as evidence.

The consequence is larger than this policy: **every addon-template change made since a
cluster was created is unapplied on that cluster, silently, with nothing reporting the
divergence.** Editing a template fixes future clusters only. Before any containment is
applied, the drift on each target has to be enumerated rather than assumed — and the
enumeration is worth having independently of this finding.

Order, therefore: enumerate addon drift → correct the live DaemonSet → apply the policy.
Reversing the first two causes the outage the policy was meant to avoid.

## Why the real fix converges with ADR-100

Of the three containment layers, only eliminating the material from bootstrap data is
remediation. The shape proposed for it — control-plane nodes fetch CA material from the hub
over an authenticated channel instead of receiving it in user-data — **has a secret-zero
problem that is not stated by proposing it**: the fetch must be authenticated by something,
and that something has to arrive in bootstrap data, which is the exposed channel.

That is the same problem ADR-100's bootstrap identity spent several rounds on, and it has the
same answer: a **short-lived** credential in user-data, spent at first boot, after which
every unpurgeable copy holds something expired. ADR-100 records it as the only mitigation that
survives an unpurgeable copy.

**So the two tracks converge on one mechanism.** They remain independent decisions — ADR-100
changes one item in the payload and four are untouched — but a short-lived bootstrap
credential is the prerequisite for both, and building it twice would be a mistake.

## An L7 proxy trades an exposure for an availability dependency

A node-local proxy that permits `/hetzner/v1/metadata/*` and refuses
`/hetzner/v1/userdata` does close all three paths, and a packet filter cannot do this —
both paths share one IP, protocol and port, so L3/L4 blocking is all-or-nothing.

What it costs is worth stating before anyone builds it: `hcloud-csi-node` reads
`instance-id` and `availability-zone` **on every restart**, not only at bootstrap, and fails
to start without them. A proxy in that path means a proxy failure is a cluster-wide volume
outage on the next node reboot. `dynamic-node-ip.sh` is subtler — it skips the fetch once
`--provider-id` is in `kubeadm-flags.env`, so ordinary restarts are safe, but a
`kubeadm upgrade` rewrites that file and the node then needs metadata or kubelet crash-loops.

That is an exposure converted into an availability dependency on the control plane's startup
path. It may still be the right trade; it should be made knowingly.

## Open decisions

- **Rotation and node replacement** of all four keys on `nutgraf-hub`, and the order in which
  that is safe to do.
- **`nutgraf-01`** runs the `waypoint` and `oranger` tenant workloads, so any of those pods
  can read its control-plane PKI today. Whether that is handled as a live incident, and who
  is told, is not a platform-engineering call.
- Whether this becomes its own ADR. The fix in layer 3 is a decision with real consequences
  and would warrant one.
