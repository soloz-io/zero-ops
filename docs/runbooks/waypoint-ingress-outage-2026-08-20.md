# waypoint.nutgraf.in unreachable — diagnosis and implementation plan

**Date:** 2026-08-20
**Cluster:** `spoke-pool-hybrid-dev-01` (hybrid provider, ADR-046)
**Hub:** `hub-hybrid-dev`
**Status:** Diagnosed, not yet fixed. This document is the implementation brief.

---

## 1. Symptom

```
$ dig +short waypoint.nutgraf.in A
77.42.12.176                      # correct — waypoint-gateway-lb public IPv4

$ nc -zv 77.42.12.176 80          # LB accepts TCP
Connection to 77.42.12.176 port 80 succeeded!

$ curl http://waypoint.nutgraf.in
curl: (52) Empty reply from server

$ curl https://waypoint.nutgraf.in
curl: (35) Recv failure: Connection reset by peer
```

DNS and the Hetzner load balancer are healthy. The LB has no working backend.

---

## 2. Causal chain (four layers, established bottom-up)

### L4 — Nothing listens on `:80`/`:443` on the control-plane node

```
$ kubectl exec -n kube-system cilium-qgpp4 -c cilium-agent -- ss -lntp | grep -E ':80 |:443 '
(no output)
```

`scripts/hybrid/ensure-waypoint-lb.sh` points the LB at the CP server's **private IP,
ports 80 and 443, PROXY protocol** (lines 75–97). With nothing bound, every forwarded
connection dies immediately — exactly the `curl (52)` / `(35)` signature.

Two independent reasons nothing is bound:

**(a) No Gateway or HTTPRoute exists in the cluster.**

```
$ kubectl get gateway -A ; kubectl get httproute -A
No resources found
No resources found
$ kubectl get ciliumenvoyconfig -A
No resources found
$ kubectl get secrets -n cilium-secrets
No resources found in cilium-secrets namespace.
```

The `cilium` GatewayClass is Accepted and all Gateway API CRDs are installed — there is
simply no Gateway object to program Envoy with.

**(b) Gateway hostNetwork mode has been turned off, contradicting ADR-046 addendum 8.**

Live `cilium-config`:

```
gateway-api-hostnetwork-enabled=false
gateway-api-hostnetwork-nodelabelselector=
envoy-keep-cap-netbindservice=false
external-envoy-proxy=true
```

This matches the repo state — `manifests/providers/hybrid/k8s/cilium-addon-hybrid.yaml:144`
sets `gateway-api-hostnetwork-enabled: "false"`, and the `cilium-hostnetwork-mangle-guard`
and `cilium-node-ip-reconciler` DaemonSets have been removed. The uncommitted working-tree
edit to `manifests/providers/hybrid/cilium-values.yaml` flips
`gatewayAPI.hostNetwork.enabled` from `true` to `false` with the comment *"Disabled —
Gateway API listeners use NodePort+TPROXY redirect path."*

ADR-046 addendum 8 rejects that path explicitly: NodePort+TPROXY *"silently drops
genuinely-external SYNs (no SYN-ACK, no RST, no BPF trace) on ALL interfaces"*. The
external LB design depends on hostNetwork mode — it targets host ports 80/443 directly
and cannot target a NodePort or a CCM-provisioned Service.

### L3 — The Gateway is missing because its ArgoCD Application cannot sync

Hub Application `platform-ops/tenant-waypoint-dev-workloads`:

- source: `https://github.com/soloz-io/fleet-registry.git`, path `tenants/waypoint/workloads`
- destination: `spoke-pool-hybrid-dev-01` / `tenant-waypoint`
- status: **OutOfSync / Missing**, `SyncError: one or more synchronization tasks are not valid (retried 10 times)`

All 22 resources are `Missing`, including `Gateway waypoint-gateway`,
`HTTPRoute waypoint-frontend`, `HTTPRoute waypoint-bff`, and the
`frontend-workload` / `bff-workload` / `sdk-workload` / `builder-serve-workload` Rollouts.

Per-resource sync messages:

```
Gateway waypoint-gateway    SyncFailed  The Kubernetes API could not find
                                        gateway.networking.k8s.io/Gateway for requested
                                        resource tenant-waypoint/waypoint-gateway. Make sure
                                        the "Gateway" CRD is installed on the destination cluster.
HTTPRoute waypoint-frontend SyncFailed  (same, HTTPRoute)
HTTPRoute waypoint-bff      SyncFailed  (same, HTTPRoute)
Rollout frontend-workload   SyncFailed  Rollout.argoproj.io "" not found
Rollout bff-workload        SyncFailed  Rollout.argoproj.io "" not found
Rollout sdk-workload        SyncFailed  Rollout.argoproj.io "" not found
Rollout builder-serve-workload SyncFailed Rollout.argoproj.io "" not found
```

**The CRDs are in fact present on the spoke** — verified:

```
gateways.gateway.networking.k8s.io    2026-08-20T08:24:32Z
httproutes.gateway.networking.k8s.io  2026-08-20T08:24:32Z
rollouts.argoproj.io                  2026-08-20T08:23:13Z
```

So this is a **stale ArgoCD cluster / API-resource cache** for destination
`spoke-pool-hybrid-dev-01`, not a missing CRD. `tenant-oranger-dev-workloads` fails
identically, confirming it is per-cluster and not per-app.

### L2 — Platform convergence is blocked by an unreachable ESO webhook

Hub Application `platform-spoke-catalog-spoke-pool-hybrid-dev-01` — **OutOfSync / Degraded**:

```
one or more objects failed to apply, reason: Internal error occurred: failed calling webhook
"validate.clustersecretstore.external-secrets.io": failed to call webhook:
Post "https://eso-external-secrets-webhook.external-secrets.svc:443/validate-...?timeout=5s":
dial tcp 10.99.213.200:443: connect: operation not permitted (retried 5 times).
```

The webhook Service has **zero endpoints**:

```
$ kubectl get endpoints -n external-secrets
NAME                           ENDPOINTS   AGE
eso-external-secrets-webhook               4h8m

$ kubectl get pods -n external-secrets -o wide
eso-external-secrets-6f6fcd68f4-tg2qt                 0/1 CrashLoopBackOff  10.244.1.152  flatcar-node-1
eso-external-secrets-cert-controller-864f6bb556-2lcx5 0/1 CrashLoopBackOff  10.244.1.92   flatcar-node-1
eso-external-secrets-webhook-5cb4ccd944-257bf         0/1 CrashLoopBackOff  10.244.1.149  flatcar-node-1
```

All three are on `flatcar-node-1` and all three die with the same error:

```
unable to create managed secret client:
Get "https://10.96.0.1:443/api": dial tcp 10.96.0.1:443: i/o timeout
```

Same failure on every other pod scheduled to the home worker:
`agent-sandbox-controller`, `local-path-provisioner`.

### L1 — ROOT CAUSE: pods on `flatcar-node-1` cannot reach anything off-node

Every pod on the home worker is network-isolated from the control plane.

```
$ kubectl exec cross-test -- nc -zv -w 5 10.96.0.1 443          # apiserver ClusterIP
nc: 10.96.0.1 (10.96.0.1:443): Connection timed out
$ kubectl exec cross-test -- nc -zv -w 5 100.118.202.60 6443    # CP tailnet IP
nc: 100.118.202.60 (100.118.202.60:6443): Connection timed out
$ kubectl exec cross-test -- nc -zv -w 5 10.244.0.196 53        # CoreDNS pod on CP
nc: 10.244.0.196 (10.244.0.196:53): Connection timed out
```

Everything *below* Tailscale is correct — this is not a Cilium misconfiguration:

- **BPF service map is correct:**
  `14  10.96.0.1:443/TCP  ClusterIP  1 => 100.118.202.60:6443/TCP (active)`
- **ipcache is correct:** `10.244.0.x/32 ... tunnelendpoint=100.118.202.60`
- **Kernel routes are correct, both directions:**
  - flatcar: `10.244.0.0/24 via 100.118.202.60 dev tailscale0 proto kernel`
  - CP: `10.244.1.0/24 via 100.85.175.14 dev tailscale0 proto kernel`
- **Route selection is correct:**
  `ip route get 100.118.202.60 from 10.244.1.34 → dev tailscale0 table 52`
- **Netfilter is not dropping:** all chain policies `ACCEPT`; `CILIUM_FORWARD -i lxc+ -j ACCEPT`
- **KubeProxyReplacement: True**, `Controller Status: 65/65 healthy`

Packet trace — the SYN reaches the stack on flatcar and never arrives at the CP:

```
# cilium-dbg monitor on flatcar-node-1
-> stack flow 0x93514749, identity 15767->kube-apiserver state new
   10.244.1.34:39689 -> 100.118.202.60:6443 tcp SYN

# cilium-dbg monitor on the CP, same window: ZERO packets from 10.244.1.34
```

**`tailscaled` on `flatcar-node-1` logs the drop explicitly:**

```
open-conn-track: timeout opening (TCP 10.244.1.34:39463 => 10.244.0.196:53); no associated peer node
open-conn-track: timeout opening (TCP 100.85.175.14:36284 => 10.244.0.2:4240);  no associated peer node
open-conn-track: timeout opening (TCP 10.244.1.92:50618 => 100.118.202.60:6443) to node [UWT7U]; online=yes
```

`tailscale status` health check:

```
# Health check:
#     - Some peers are advertising routes but --accept-routes is false
```

`tailscale debug prefs`:

```json
"RouteAll": false,
"AdvertiseRoutes": ["10.244.1.0/24"]
```

Corroborating: `Cluster health: 1/2 reachable`;
`cilium_drop_count_total direction=INGRESS reason="Stale or unroutable IP" = 3029`.

#### Why `--accept-routes: false` breaks this topology

The node `InternalIP` on **both** nodes is a Tailscale IP (`100.85.175.14`,
`100.118.202.60`), so *all* cross-node pod traffic transits `tailscale0`. Once a packet
enters the TUN device, **`tailscaled` — not the kernel — decides delivery**, using its own
WireGuard peer `AllowedIPs`.

`--accept-routes` does two distinct things:

1. adds each peer's advertised subnet prefixes to the **local WireGuard peer `AllowedIPs`** — required for the datapath in both directions;
2. installs those prefixes as **kernel routes in table 52** — this is what collided with Cilium's own routes.

Removing the flag disabled **both**. Effect:

- Egress to `10.244.0.0/24`: no peer owns the prefix locally → `no associated peer node` → dropped on flatcar.
- Egress to `100.118.202.60:6443` sourced from `10.244.1.x`: a peer is found and the packet is sent, but the **CP's** `tailscaled` also has `RouteAll: false`, so `10.244.1.0/24` is not in its peer config for flatcar → dropped on ingress at the CP. This is why the CP monitor sees nothing.

The routes **are approved** in the Tailscale control plane — `AllowedIPs` from the netmap
include `10.244.0.0/24` and `PrimaryRoutes` is set on both nodes. The breakage is purely
local policy.

#### Introduced by

`347dfe53` — *"refactor: remove --accept-routes from Tailscale configurations to prevent
pod route collisions"*. It created `spokepool-control-plane-v6` (= v5 minus
`--accept-routes`) and edited `scripts/hybrid/provision-flatcar-worker.sh`. Its stated
rationale is incorrect for this topology:

> *"Cilium handles cross-node pod routing natively via Kubernetes node InternalIPs;
> Tailscale subnet route acceptance is unnecessary and dangerous."*

Cilium's `auto-direct-node-routes` does install the kernel routes — and they are present
and correct — but the transport underneath them is Tailscale, which enforces its own
cryptokey routing. The v5 problem it was fixing (stale podCIDR routes from orphaned tailnet
nodes hijacking local pod traffic into table 52) is real, but it is caused by **effect 2**;
the fix removed **effect 1** as collateral damage.

#### Why it surfaced now

`bdd3bd3c` — *"add cilium-bootstrap-cleanup service to remove stale iptables DNAT rules
after Cilium initialization"* — was masking it. `join-cluster.sh` inserts
`10.96.0.1:443 → <control-plane-endpoint>` DNAT plus
`POSTROUTING -d <CP_HOST> -j MASQUERADE`. The control-plane endpoint is the **public Hetzner
LB** (`95.217.168.74:443`, confirmed from the `-home-worker-join` Secret), so API traffic was
masqueraded out `eth0` over the public internet and never touched the tailnet.
`cilium-bootstrap-cleanup.service` deletes those rules once `cilium-agent` is Running,
removing the last working path and exposing the `347dfe53` regression.

---

## 3. Secondary defect (independent, will cause recurring damage)

`systemd-networkd` on `flatcar-node-1` has hijacked every Cilium interface.

```
$ ip -4 addr
2:  eth0               inet 172.30.0.11/24
4:  cilium_net         inet 172.30.0.11/24      <-- wrong
9:  lxc54e3dce02596    inet 172.30.0.11/24      <-- wrong
11: lxcbd452eaebd8d    inet 172.30.0.11/24      <-- wrong
13: lxcf0b301c452cd    inet 172.30.0.11/24      <-- wrong
15: lxcdcc0929d6295    inet 172.30.0.11/24      <-- wrong
17: lxcba2fb6cea238    inet 172.30.0.11/24      <-- wrong
19: lxc5078b7e4b96f    inet 172.30.0.11/24      <-- wrong
23: lxc_health         inet 172.30.0.11/24      <-- wrong
29: lxcb1f7653c2cfb    inet 172.30.0.11/24      <-- wrong

$ ip route
default via 172.30.0.1 dev eth0 proto static
default via 172.30.0.1 dev cilium_net proto static
default via 172.30.0.1 dev lxcbd452eaebd8d proto static
... (10 competing default routes, one per veth)
```

Cause: `provision-flatcar-worker.sh` writes `/etc/systemd/network/10-static.network` with

```ini
[Match]
Type=ether
```

`Type=ether` matches **every** ethernet-class link, which includes Cilium's `cilium_net`
and every `lxc*` pod veth created after boot. The same block appears twice —
`provision-flatcar-worker.sh:352-361` (Ignition) and `:687-698` (cloud-config fallback).

Aggravating: the block sets `DHCP=yes` **and** a static `Address=`/`Gateway=`
simultaneously, and `setup-node.sh` (`provision-flatcar-worker.sh:415-422`) iterates
`/sys/class/net/*` adding an address and default route to the first non-`lo` device — which
after Cilium starts can be `cilium_host` or an `lxc*` device.

This is not the cause of the current outage (routing still resolves correctly), but it is
latent corruption of the Cilium datapath that will produce nondeterministic failures.

---

## 4. Implementation plan

Phases are ordered by dependency. Do not skip ahead — L4 verification is meaningless
until L1 is fixed.

### Phase 1 — Restore cross-node pod routing (root cause)

**Decision required.** Two viable approaches; recommendation is 1A.

**1A (recommended) — restore `--accept-routes`, fix the collision at its source.**

Minimal, reverses a known-bad change, and returns to the ADR-046 documented design.

1. `manifests/providers/_shared/spokepool-clusterclass-v1.yaml:1179` — restore
   `--accept-routes` on the `spokepool-control-plane-v6` `tailscale set` line:
   ```
   ... && tailscale set --advertise-routes="${POD_CIDR}" --accept-routes; fi'
   ```
   Create this as **`spokepool-control-plane-v7`** rather than mutating v6 in place, and
   bump the `controlPlane.ref.name` at line 12. Rotating the ref rolls the control-plane
   machine (ADR-046 addendum 1) — plan for it.
2. `spokepool-worker-bootstrap-v1` (line 1195 onward) has **no `advertise-routes` /
   `accept-routes` line at all** — its only Tailscale command is
   `tailscale up ... --accept-dns=false` at line 1272. Burst workers therefore never
   advertise their podCIDR. Not live today (burst pool is at `replicas: 0`), but add the
   same `tailscale set --advertise-routes="${POD_CIDR}" --accept-routes` postKubeadm step
   there or scaling the burst pool will reproduce this outage on Hetzner workers.
   (Lines 766 and 974 are the v4 and v5 control-plane templates — historical, not the
   worker template. Leave them alone.)
3. `scripts/hybrid/provision-flatcar-worker.sh:484` — add `--accept-routes`:
   ```sh
   /opt/bin/tailscale set --advertise-routes="$POD_CIDR" --accept-routes 2>/dev/null || true
   ```
4. **Address the original v5 collision properly.** The v5 bug was stale tailnet devices
   advertising overlapping podCIDRs. Add a de-enrollment step to the provisioning script:
   before enrolling a node, delete any existing tailnet device with the same hostname via
   the Tailscale API, so an orphaned `flatcar-node-1` cannot keep an approved
   `10.244.1.0/24`. Today the tailnet holds exactly two devices and no orphans, so
   `--accept-routes` is safe to re-enable immediately.
5. Live remediation (no reprovision needed):
   ```sh
   # on flatcar-node-1
   tailscale set --accept-routes
   # on the CP node
   tailscale set --accept-routes
   ```
   Then verify `tailscale debug prefs` shows `"RouteAll": true` on both, and that
   `tailscale status` no longer reports the advertising-routes health warning.
6. After enabling, confirm Cilium's `auto-direct-node-routes` entries still win over any
   tailscaled-installed table-52 routes. If they collide, that is the v5 symptom — resolve
   by removing the stale device, not by disabling the flag.

**1B (durable alternative) — switch to tunnel routing so the tailnet never sees pod IPs.**

Set `routingMode: tunnel` (VXLAN) in `manifests/providers/hybrid/cilium-values.yaml` and
the rendered addon. Pod traffic is then encapsulated with node **tailnet IPs** as outer
src/dst, so `tailscaled` only ever handles `100.x` ↔ `100.x` packets. Subnet route
advertisement and acceptance become unnecessary and the collision class is structurally
impossible. `mtu: 1200` is already set for exactly this (ADR-046 addendum 6, which
describes the VXLAN configuration). Cost: reverses the "routing-mode: native" decision in
the ADR body, and adds encapsulation overhead.

Note the ADR is already self-inconsistent here — the body says `routing-mode: native`,
addendum 6 is titled *"Hybrid Cilium VXLAN MTU (1200) & Tailscale Overlay Invariant"* and
describes VXLAN behaviour. Whichever option is chosen, reconcile that contradiction.

**Gate:** do not proceed until all of the following pass.

```sh
kubectl exec cross-test -- nc -zv -w 5 10.96.0.1 443           # must succeed
kubectl exec cross-test -- nc -zv -w 5 10.244.0.196 53         # must succeed
kubectl exec -n kube-system cilium-6cpx4 -c cilium-agent -- \
  cilium-dbg status | grep 'Cluster health'                    # must read 2/2 reachable
kubectl get pods -n external-secrets                           # all 3 Running
kubectl get endpoints -n external-secrets                      # webhook has endpoints
```

### Phase 2 — Fix the systemd-networkd interface hijack

1. `provision-flatcar-worker.sh:352-361` and `:687-698` — replace the match:
   ```ini
   [Match]
   Name=eth0
   ```
   (or `Name=en*` / `Driver=hv_netvsc` for the Hyper-V NIC). Never `Type=ether`.
   Explicitly exclude Cilium-managed links as defence in depth:
   ```ini
   [Match]
   Name=eth*
   Name=!cilium_* !lxc* !tailscale*
   ```
2. Resolve the `DHCP=yes` + static `Address=`/`Gateway=` contradiction — pick one. Static
   is what the node-index scheme (`172.30.0.$((10 + NODE_IDX))`) implies.
3. `setup-node.sh` (`provision-flatcar-worker.sh:415-422`) — remove the
   `for dev in /sys/class/net/*` address/route loop, or constrain it to `eth0`. It races
   Cilium and can address a `cilium_*`/`lxc*` device.
4. Live remediation on `flatcar-node-1` (or reprovision the node, which is cleaner):
   ```sh
   for i in $(ip -o link | awk -F': ' '/cilium_net|lxc/ {print $2}' | cut -d@ -f1); do
     ip addr del 172.30.0.11/24 dev "$i" 2>/dev/null
     ip route del default via 172.30.0.1 dev "$i" 2>/dev/null
   done
   ```
   Verify only `eth0` carries `172.30.0.11/24` and exactly one default route remains.

### Phase 3 — Restore Gateway hostNetwork mode (ADR-046 addendum 8)

The current NodePort+TPROXY configuration cannot work with the external LB.

1. `manifests/providers/hybrid/cilium-values.yaml` — revert the uncommitted edit:
   ```yaml
   gatewayAPI:
     hostNetwork:
       enabled: true
       nodes:
         matchLabels:
           node-role.kubernetes.io/control-plane: ""
   ```
   Keep `envoy.enabled: true` (addendum 10, decoupled standalone Envoy).
2. `manifests/providers/hybrid/k8s/cilium-addon-hybrid.yaml` — bring the rendered addon
   back in line:
   - `gateway-api-hostnetwork-enabled: "true"` (line 144)
   - `gateway-api-hostnetwork-nodelabelselector: "node-role.kubernetes.io/control-plane="` (line 145)
   - `envoy-keep-cap-netbindservice: "true"` (line 366)
   - `NET_BIND_SERVICE` in the `cilium-envoy` DaemonSet capability list
   - **restore the `cilium-hostnetwork-mangle-guard` DaemonSet** — without it Cilium's
     stock `CILIUM_PRE_mangle` transparent-socket rule breaks the external TCP handshake
     (addendum 8)
   - restore `cilium-node-ip-reconciler` **only if** Phase 1B (tunnel mode) is chosen;
     under 1A with native routing, re-evaluate against addendum 6
3. Reconcile the two working-tree comment edits that assert the opposite rationale
   (`cilium-values.yaml` header, `cilium-addon-hybrid.yaml:1170-1176`) — the
   `mountPropagation: HostToContainer` change is separately justified by the live Flatcar
   finding and can stay.
4. After applying, restart the Envoy DaemonSet and confirm host binding:
   ```sh
   kubectl exec -n kube-system cilium-qgpp4 -c cilium-agent -- ss -lntp | grep -E ':80 |:443 '
   ```

### Phase 3b — Fix the PROXY-protocol mismatch (independent bug)

**This alone reproduces `curl (52) Empty reply from server` even after Phases 1–4 pass.**

The Hetzner LB is provisioned with `--proxy-protocol=true`
(`ensure-waypoint-lb.sh:75-77`), so it prepends a PROXY v1/v2 preamble to every forwarded
connection. Cilium's Gateway listener must be configured to parse it, and currently is not.
The rendered addon is **internally inconsistent**:

| Location | Value |
|---|---|
| `cilium-values.yaml:31` (source) | `proxyProtocol: true` |
| `cilium-addon-hybrid.yaml:138` (`cilium-config` ConfigMap — what the agent reads) | `enable-gateway-api-proxy-protocol: "false"` |
| `cilium-addon-hybrid.yaml:1554` (operator arg) | `--enable-gateway-api-proxy-protocol=true` |
| Live cluster | `enable-gateway-api-proxy-protocol=false` |

The agent reads the ConfigMap, so the effective value is `false`. Envoy then treats the
PROXY preamble as malformed HTTP and resets the connection — precisely the failure the
`ensure-waypoint-lb.sh` header comment warns about (lines 39-41: *"gateway-api proxy
protocol is required ... 'connection reset by peer' and every HTTP health check fails"*).

**Fix:** set `enable-gateway-api-proxy-protocol: "true"` in the ConfigMap block at
`cilium-addon-hybrid.yaml:138` so all three locations agree, then restart the agent and
Envoy DaemonSets.

Both ends must match. If PROXY protocol is instead disabled on the Gateway, it must also be
removed from the LB services — but keep it enabled: it is what preserves the client source
IP, and `ensure-waypoint-lb.sh` already pairs it with **TCP** (not HTTP) health checks for
exactly this reason.

### Phase 3c — Re-point the external Hetzner LB at the new control-plane server

`waypoint-gateway-lb` is an **external, non-GitOps resource** (ADR-046 addendum 8). It
targets a specific Hetzner **server ID**. This spoke was re-provisioned on 2026-08-20
(~4h50m before this investigation), so the control-plane server is new and the LB is
almost certainly still pointing at the deleted machine.

```sh
export HCLOUD_TOKEN=...   # not available during this investigation; the CLI had no context
./scripts/hybrid/ensure-waypoint-lb.sh
hcloud load-balancer describe waypoint-gateway-lb   # confirm target ID + health
```

The script is idempotent: it re-adds the target with `use_private_ip=true` and re-asserts
the 80/443 services, PROXY protocol, and TCP health checks. Run it **after** Phase 3/3b, so
the health check has something to succeed against.

**Also re-add the Hetzner firewall rules.** ADR-046 addendum 4 records that LB→node rules
are *not* codified anywhere in this repo and must be re-created by hand after every spoke
re-provision. Under hostNetwork mode the required rules are LB → CP node **TCP 80 and 443**
over the private network (not the old nodePort range). Missing rules present exactly the
same symptom as a missing listener.

### Phase 4 — Unblock ArgoCD delivery

Phases 1 and 3 must be green first — ESO must be Running before `platform-spoke-catalog`
can sync.

1. Confirm `platform-spoke-catalog-spoke-pool-hybrid-dev-01` reaches Synced/Healthy once
   the ESO webhook has endpoints. Hard-refresh if it does not retry on its own.
2. Clear the stale cluster cache for destination `spoke-pool-hybrid-dev-01` so ArgoCD
   discovers the Gateway API and Rollout CRDs. In order of increasing blast radius:
   - hard refresh `tenant-waypoint-dev-workloads` (`argocd app get --hard-refresh`)
   - invalidate the cluster cache (`argocd cluster ... --invalidate-cache` or the UI's
     *Invalidate Cache* on the cluster)
   - restart the hub `argocd-application-controller-0`
3. Verify the Application syncs all 22 resources and that
   `Gateway waypoint-gateway`, `HTTPRoute waypoint-frontend`, `HTTPRoute waypoint-bff`
   become Synced/Healthy.
4. `tenant-oranger-dev-workloads` fails identically — it should recover with the same fix
   and serves as the confirmation that the cause was per-cluster cache, not per-app.

### Phase 5 — End-to-end verification

```sh
kubectl get gateway -n tenant-waypoint          # PROGRAMMED=True, address assigned
kubectl get httproute -n tenant-waypoint        # Accepted, ResolvedRefs
kubectl get secrets -n cilium-secrets           # TLS cert synced for the listener
kubectl get rollouts -n tenant-waypoint         # frontend/bff/sdk/builder-serve Healthy
kubectl exec -n kube-system cilium-qgpp4 -c cilium-agent -- ss -lntp | grep -E ':80 |:443 '
curl -sS -o /dev/null -w 'http=%{http_code}\n'  http://waypoint.nutgraf.in
curl -sS -o /dev/null -w 'https=%{http_code}\n' https://waypoint.nutgraf.in
curl -sS -o /dev/null -w 'api=%{http_code}\n'   https://api.waypoint.nutgraf.in
```

Also confirm the LB target is healthy — `hcloud load-balancer describe waypoint-gateway-lb`
(requires `HCLOUD_TOKEN`; the CLI had no active context during this investigation).

Note ADR-046 addendum 9/10: creating the Gateway triggers an Envoy hot-restart. With
decoupled Envoy (`envoy.enabled: true`) this should be safe, but watch for the stuck-drain
signature (`:80`/`:443` bound but returning `000`) and confirm the
`wait-for-envoy-release` init gate behaves.

### Phase 6 — Codify

Per ADR-046 addendum 12's adhoc-change rule, nothing may live only in shell history.

1. **New ADR-046 addendum 14** — *Tailscale subnet-route acceptance is load-bearing for
   hybrid pod networking.* Record: node InternalIPs are tailnet IPs, so all cross-node pod
   traffic transits `tailscale0`; `--accept-routes` controls WireGuard peer `AllowedIPs`,
   not just kernel routes; removing it silently blackholes all cross-node pod traffic with
   `no associated peer node`. Supersede the `347dfe53` rationale and the v6 comment block
   at `spokepool-clusterclass-v1.yaml:976-982`. State the correct remedy for stale-route
   collisions (de-enroll orphaned tailnet devices at provision time).
2. **Addendum 15** — `systemd-networkd` `[Match]` must never use `Type=ether` on a CNI
   node; it captures `cilium_*` and `lxc*` links.
3. **Correct addendum 8's status** if hostNetwork is restored, or write a new addendum
   explaining why it was abandoned — the current repo state silently contradicts an
   Accepted decision.
4. Resolve the `routing-mode: native` (body) vs VXLAN (addendum 6) contradiction.
5. Extend `post-bootstrap-validate.sh` with a cross-node pod-datapath gate:
   pod-on-worker → `10.96.0.1:443` and pod-on-worker → pod-on-CP must both succeed, and
   `cilium-dbg status` must report `Cluster health: N/N reachable`. This outage would have
   been caught at provision time by that single check.

---

## 5. Known unknowns — verify these before assuming the plan is complete

These were **not** verifiable from the two clusters and this repo. Each could add work.

1. **The Gateway/HTTPRoute specs were never read.** They live in
   `github.com/soloz-io/fleet-registry` at `tenants/waypoint/workloads`, which was not
   accessible during this investigation. Before Phase 5, read them and confirm:
   - listener `hostname` values match `waypoint.nutgraf.in` / `api.waypoint.nutgraf.in`
   - the TLS listener's `certificateRefs` target a Secret that actually gets created
   - `spec.gatewayClassName: cilium`
   - `spec.addresses` / annotations do not assume a LoadBalancer Service (incompatible with
     hostNetwork mode, which forces the gateway Service to ClusterIP)
   - the `infrastructure.annotations` carry whatever Cilium needs for PROXY protocol
2. **TLS certificate chain.** `cilium-secrets` is empty. The listener cert comes from
   cert-manager via the `infisical-fleet-issuer` ClusterIssuer, which depends on ESO, which
   depends on Phase 1. ADR-046 addendum 12 records that this ClusterIssuer has gone missing
   before without self-healing (CRS `Reconcile` does no drift repair). Verify the issuer
   exists and the Certificate reaches Ready, or HTTPS will fail while HTTP succeeds.
3. **Live CP vs. template rotation.** `preKubeadmCommands` only run at machine bootstrap,
   so editing the ClusterClass does **not** fix the running control plane. Apply
   `tailscale set --accept-routes` live for the immediate fix; the v7 ref rotation is for
   future machines and **rolls the CP machine** when applied. These are separable — do the
   live fix first, verify, then decide when to take the roll.
4. **No rollback path is written.** Phase 1A is a revert of `347dfe53` so its rollback is
   trivial, but Phase 3 (hostNetwork) has the addendum 9/10 stuck-drain risk. Capture the
   current `cilium-config` and Envoy DaemonSet before changing them.
5. **`HCLOUD_TOKEN` was unavailable**, so no statement in this document about the live LB's
   targets or health is first-hand — Phase 3c is reasoned from the re-provision timeline
   and the script's logic, not observed.

## 6. Notes for the implementing agent

- Spoke kubeconfig: `KUBECONFIG=/tmp/spoke-fresh.kubeconfig`.
  Hub: `zero-ops/k8-secrets/kubeconfig/hub-hybrid-dev.kubeconfig`.
- Useful debug handles already on the cluster: `ipt-debug` (kube-system, hostNetwork +
  privileged, can `nsenter -t 1 -m -n`) and `cross-test` (default ns, busybox on
  `flatcar-node-1`). Remove both when finished, along with the stale `clusterip-test3`,
  `cp2-host-debug`, and `node-debugger-*` pods in `default`.
- The cilium agent pods have no `tcpdump`, `nc`, or `ping`. `cilium-dbg monitor`,
  `ss`, `ip`, and `iptables` are available. `nsenter` into PID 1 works from `ipt-debug`
  on the Flatcar node but **not** from the CP agent pod (`Operation not permitted`).
- Unrelated pre-existing issues seen during triage, out of scope but worth filing:
  `grafana-alloy` in `CreateContainerConfigError` on both nodes, Kyverno cleanup CronJobs
  in `ImagePullBackOff`, and high restart counts across CP-node pods (argocd controller 30,
  repo-server 34, hcloud-csi-controller 89) suggesting an earlier period of API instability.
