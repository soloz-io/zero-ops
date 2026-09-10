# ADR-075: On-Prem Nodes are a Capability, Not a Provider

**Date:** 2026-09-10

**Status:** Proposed

## Context

ADR-046 introduced the hybrid provider: a box whose default workers are on-prem machines joining over Tailscale, with both control planes on the tailnet because a on-prem node's only `InternalIP` is its tailnet address and Cilium derives its VXLAN endpoint from it (§21, invariant 6).

"Hybrid" became the name for that arrangement, and `--provider` became the switch. Five facts bear on whether it should have.

**`provider` names four decisions at once.** It selects the spoke-pool directory, the Crossplane composition, the Cilium addon, and whether on-prem nodes exist. Those have different lifetimes: the first three are infrastructure the platform maintains, the fourth is something a tenant chooses and may change.

**The choice is currently made once and cannot be revised.** A box bootstrapped as `hetzner` has no supported path to gaining a on-prem node. Nothing about the arrangement is inherently permanent -- it is permanent because the decision was encoded as an identity rather than as a setting.

**The joining machinery is already installed on every box, switched off.** `driver_hetzner.go` writes the `tailscale-hybrid-psk` Secret with empty values on every hetzner cluster, because an unresolvable `contentFrom.secret` blocks KubeadmConfig rendering entirely. Every Tailscale step in the ClusterClass is guarded on `[ -s /etc/tailscale-hostname ]` and is a no-op until that Secret holds something. The capability is present and inert.

**Two keys gate on-prem nodes; twelve others differ for unrelated reasons.** Both providers now run `routing-mode: tunnel`, `tunnel-protocol: vxlan` and `auto-direct-node-routes: false` -- the hybrid addon's header still calls the routing mode its first deliberate delta, which has not been true for some time.

What gates an on-prem node is `mtu` (1230 against 1200) and `devices` (runtime detection against `eth+ enp+ tailscale0`). Those two decide whether a node reachable only over the tailnet can carry pod traffic.

The remaining twelve differences are a second axis that was decided at the same time and written into the same file: `gateway-api-hostnetwork-enabled`, `external-envoy-proxy`, `envoy-xds-mode`, `envoy-keep-cap-netbindservice`, `disable-envoy-version-check`, `node-port-mode`, `node-port-bind-protection`, `enable-auto-protect-node-port-range`, `bpf-lb-sock`, `tunnel-port`, `proxy-use-original-source-address`, `direct-routing-device` and `enable-source-ip-verification`. Most belong to hostNetwork Gateway mode with decoupled Envoy (ADR-046 addenda 10 and 15), which is about how a hub binds `:80/:443` on its control-plane host. None of them is required by an on-prem node.

**Neither differing value is a tuned one.** 1230 is Cilium's stock VXLAN MTU, and ADR-046 §6 records what it produces: a 1280-byte outer packet, exactly `tailscale0`'s MTU, with zero headroom. 1200 was chosen to give 1250 and fit strictly inside. A box at 1230 is not optimised for Hetzner; it is on a default that happens not to have met a tailnet yet. `devices` listing `tailscale0` on a machine that has no such interface matches nothing and costs nothing.

## Decision

**On-prem nodes are a capability a tenant may enable at any point in a cluster's life. The platform runs one datapath, sized for the smallest path any node may ever take.**

### Two settings are platform-wide; the rest of the difference is not unified

Every box runs `mtu: 1200` and `devices: eth+ enp+ tailscale0`, whether or not it has an on-prem node today.

Those two are the whole of what makes the capability available later. They are the only settings that cannot be changed on a live cluster without restarting every Cilium agent and interrupting pod traffic; fixing them at the value that works everywhere removes the one irreversible part of the decision. Everything else the capability needs -- the Secret, the ClusterClass hook, the join -- is already dynamic.

`devices` listing `tailscale0` on a machine with no such interface matches nothing and costs nothing. The MTU costs thirty bytes of payload per packet, about 2.4%, on a box that never gains an on-prem node. It is accepted, and it is the price of the decision being revisable: a platform that saved those thirty bytes would be one where choosing Hetzner on day one silently forecloses on-prem nodes on day four hundred, and the tenant would not learn that until they asked.

**The other twelve differences are deliberately left alone.** Unifying them would give every box hostNetwork Gateway mode, external Envoy, NodePort protections disabled and `enable-source-ip-verification: false` -- a security posture change arriving as a side effect of a capability toggle. That axis is a separate decision, and this one does not make it.

### Enabling is a declaration, and it is reversible

A tenant enables the capability by editing the cluster's values file:

```yaml
onPrem:
  enabled: true
  tailnet: acme.ts.net
```

Enabling populates the Tailscale Secret and puts the control plane on the tailnet. Disabling empties it. Neither changes the datapath, because the datapath does not depend on the setting.

Selection is a value and never a version (ADR-063), so adopting this capability and upgrading a bundle stay independent acts.

### `provider` returns to naming one thing

`provider` selects infrastructure: where control-plane nodes are created, which composition provisions them, which credentials reach the cloud API. It stops implying anything about on-prem nodes.

`hybrid` is therefore withdrawn as a provider. A box that ran as `hybrid` is a box whose provider is `hetzner` and whose `onPrem` capability is enabled, which is what it always was.

### `hybrid` is retained in code until the arrangement it names is proven elsewhere

The withdrawal above is the decision. It is **not** yet the state of the tree, and the gap is deliberate rather than pending cleanup.

`hybrid` is the only provider on which on-prem nodes have ever run. Deleting it before `hetzner` with `onPrem.enabled` has provisioned a cluster and carried pod traffic to a node on someone's premises would remove the only working configuration in favour of one that has never been exercised -- and would do so at the moment there is nothing left to compare against when it fails.

So both exist for now. `hetzner` gains the capability; `hybrid` keeps working and unchanged.

**The exit condition is a hetzner box with `onPrem.enabled` that has provisioned a control plane, admitted a node on the tenant's own premises, and passed pod traffic across that boundary in both directions.** When that has happened, `hybrid` is removed: the provider entry, `manifests/providers/hybrid/`, its Crossplane composition, its spoke-pool directories, and its entries in the support matrix.

Naming the condition is the point. A transitional state with no stated end is indistinguishable from a decision nobody made, and this repository already carries one of those -- a chart default that outlived the ADR justifying it by four released versions, because the comment beside it still cited the superseded decision as live.

### What this does not make instantaneous

**Nodes already joined do not gain a tailnet address.** `preKubeadmCommands` runs once, at join. Enabling the capability enrols every node that joins afterwards; existing nodes keep the addresses they registered with. Where that matters is the control plane: ADR-046 §21 requires the hub CP on the tailnet for cross-node pod traffic to on-prem nodes, so a box enabling this after its control plane exists must replace those nodes to complete the arrangement.

This is stated rather than solved. The capability is available at any time; on an existing cluster, making it *effective* costs a control-plane roll. That is a smaller and more predictable operation than a datapath change, and it is the residue this decision does not remove.

## Alternatives considered

**Reserve the datapath at scaffold time, behind a flag.** Rejected. It is this decision with a switch in front of it, and the switch is the problem: a tenant would have to predict at scaffold time whether they will ever want a on-prem node, which is precisely the prediction they cannot make. It also leaves two datapaths to test rather than one.

**Keep the per-provider datapath and change MTU when the capability is enabled.** Rejected. Lowering MTU on a live cluster restarts every agent and interrupts pod traffic, so the capability would be available at any time in name and be a maintenance window in practice. A capability whose use requires an outage is not one a tenant reaches for.

**Keep `hybrid` as a provider and add on-prem nodes to `hetzner` as well.** Rejected. It doubles the matrix -- four combinations where two decisions exist -- and leaves `provider` naming two things. The support matrix would grow entries nobody exercises, which ADR-063 rejects for the same reason it rejects publishing an artefact per environment.

**Unify the whole of the two provider bases, not just the two keys.** Rejected, having read the diff. Twelve further keys differ, and they carry hostNetwork Gateway mode, decoupled Envoy, NodePort protection settings and source-IP verification. Adopting them platform-wide to deliver an on-prem capability would change the security posture of every box as a side effect, which is not a trade this decision is entitled to make on a tenant's behalf.

**Author the missing `cilium-addon-hetzner.yaml`.** Deferred, not rejected. A released hetzner box cannot bootstrap today because that artefact has never existed, and this ADR does not conjure it: with the two keys unified the two addons are closer, but the remaining twelve differences mean they are still two. Producing it is a real piece of work and is tracked separately.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Cilium datapath settings | `zero-ops` | Platform | hub-operator | Every node | Day-1+ |
| `onPrem` selection | `<tenant>-gitops` | Tenant | ArgoCD | Tailscale Secret | Day-1+ |
| Tailnet auth key | tenant's secret store | Tenant | ESO | ClusterClass pre-kubeadm hook | Day-0 and after |
| Home node membership | the node itself | Tenant | — | kubeadm join | any |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

A tenant can add on-prem capacity to a box that was built without it, by editing a file. That was previously impossible and the impossibility was undocumented.

One datapath is tested rather than two, and the combination matrix loses a dimension.

The two provider addons move closer together, which makes authoring the missing `cilium-addon-hetzner.yaml` a smaller job than it was. It does not remove the need for it.

The Tailscale machinery that was already installed on every box becomes something a tenant can reach, rather than dead weight carried for a provider they did not choose.

### Negative

Every box pays about 2.4% of payload for a capability most will not use. The saving is available only by making the decision irreversible, which is the trade this ADR refuses.

Enabling the capability on an existing cluster requires replacing control-plane nodes before on-prem nodes can exchange pod traffic with them. The capability is available immediately; the arrangement is not.

Two providers describe one arrangement until the exit condition above is met, so the tree carries a redundant cell and the support matrix carries entries for it. That is a real cost of the staging, paid to keep a working configuration while its replacement is unproven.

A tenant can now enable a capability whose other half -- an actual machine on their own premises, running the join -- the platform cannot see or verify. A box can report the capability enabled and have nothing joined.

## Impact

- **Amends ADR-046.** `hybrid` is withdrawn as a provider; the arrangement it named becomes `provider: hetzner` with the `onPrem` capability enabled. The datapath settings it specifies per-provider become platform-wide. Its invariants are unchanged: invariant 6 still requires a tailnet address on every node carrying pod traffic, and §21 still puts the control plane on the tailnet.
- **Amends ADR-066.** On-prem nodes are named as a selectable capability, and the boundary between cluster machinery and tenant selection is where this decision is drawn.
- **Confirms ADR-063.** Selection is a value, never a version.
- **Amends the support matrix.** `hetzner` becomes supportable in every environment. The `hybrid` entries stay until the exit condition is met, and are withdrawn with the provider.
- No change to ADR-072: the capability is declared in the tenant's repository and reconciled by the tenant's control plane, like everything else there.

## References

- ADR-039: Platform Ownership Model
- ADR-046: Hybrid Provider Cell (Hetzner Control Plane + On-Prem Workers)
- ADR-063: The Platform Bundle and its Version
- ADR-066: The Platform Boundary
- ADR-072: Tenant-Controlled Day-0 and Declarative Cluster Lifecycle
