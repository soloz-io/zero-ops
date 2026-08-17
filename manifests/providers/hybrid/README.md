# Hybrid Provider Cell (ADR-046)

Hybrid spoke: Hetzner control-plane + home-lab (WSL2) worker nodes, connected
via Tailscale. See `docs/adr/046-hybrid-provider-home-worker.md` for the full
topology, invariants and codified workarounds.

## Gateway API on the host network (hostNetwork mode)

- `gatewayAPI.hostNetwork.enabled: true` — the embedded Envoy binds
  `0.0.0.0:80/443` directly on the control-plane node; the generated Service
  is ClusterIP.
- Public entry point is the external Hetzner LB (`waypoint-gateway-lb`,
  `77.42.12.176`), provisioned idempotently via
  `scripts/hybrid/ensure-waypoint-lb.sh` (external resource — out of GitOps
  scope; the CCM cannot manage it in hostNetwork mode).
- `cilium-hostnetwork-mangle-guard` DaemonSet continuously re-asserts the
  `CILIUM_PRE_mangle` RETURN rule for dports 80/443 (Cilium's stock
  transparent-socket mark rule breaks the external TCP handshake; Cilium wipes
  foreign rules on every chain re-sync, so the guard re-inserts every 3s).

## Known limitation: Envoy stuck-drain on Gateway changes

> *Known Limitation: Modifying `Gateway` annotations or listeners triggers an
> Envoy hot-restart in Cilium. In hostNetwork mode, this frequently results in
> a stuck drain state (parent shuts down, child fails to bind, traffic drops).
> Workaround: After applying Gateway changes, manually restart the Cilium
> agent on the control-plane node:*
>
> `kubectl delete pod -n kube-system -l k8s-app=cilium --field-selector spec.nodeName=<cp-node>`

Tracked as upstream technical debt (embedded-Envoy hot-restart in hostNetwork
mode); a watchdog is deliberately not added — Gateway topology is a Day-1
operation in this environment.
