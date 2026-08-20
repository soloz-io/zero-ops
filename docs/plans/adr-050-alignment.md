# ADR-050 Alignment Plan

**Date:** 2026-08-20
**Goal:** Bring the platform into compliance with ADR-050 (Tenant Authentication via
AgentGateway), close its P0/P1 security contracts, and land per-environment
subdomains with browser-trusted TLS — all terminating at the hub.

**Locked decisions** (confirmed 2026-08-20):

| Question | Decision |
|---|---|
| Hub → home-worker pod reachability | **Hub joins the tailnet** (amends ADR-046) |
| Scope of ADR-050 gaps | **Everything** — platform + BFF-side contracts specified for fleet-registry |
| Hub's own hostnames | **Also move into the per-environment zone** |

Resulting hostname model (env is a DNS zone, production is bare):

```
dev    api.dev.nutgraf.in  auth.dev.nutgraf.in  console.dev.nutgraf.in
       waypoint.dev.nutgraf.in  api.waypoint.dev.nutgraf.in  oranger.dev.nutgraf.in
prod   api.nutgraf.in      auth.nutgraf.in      console.nutgraf.in
       waypoint.nutgraf.in      api.waypoint.nutgraf.in      oranger.nutgraf.in
```

**Every hostname above resolves to the hub load balancer.** No public name resolves
to a spoke. That is ADR-050 hardening item 5.

---

## Observed state (facts, verified live)

| # | Finding |
|---|---|
| 1 | **The hub edge is deployed but unreachable.** ingress-nginx LB `65.109.41.89`, `agentgateway` 1/1, `auth-proxy` 1/1, Ingresses for api/auth/console/argocd — but **none of those hostnames resolve**. |
| 2 | **Six hub certificates have been stuck 3–6 days.** `agentgateway-tls`, `api-zero-ops-tls`, `auth-zero-ops-tls`, `console-zero-ops-tls`, `argocd-server-tls`, `victoriametrics-tls` — all `pending`, all failing HTTP-01 self-check (`failed to perform self check GET … http://api.nutgraf.in/.well-known/acme-challenge/…`) because the names do not resolve. Stale `cm-acme-http-solver-*` Ingresses are accumulating. |
| 3 | **AgentGateway routes exist with no backend.** `agentgateway-config.yaml` routes `waypoint.nutgraf.in` → `frontend-workload.tenant-waypoint.svc.cluster.local:3000` and `api.waypoint.nutgraf.in` → `bff-workload…:3001`. Its own comment: *"until [MCS] lands, this route has no live backend."* |
| 4 | **The live path is the one ADR-050 bans.** `waypoint.nutgraf.in` → `77.42.12.176` (spoke Gateway LB) → spoke HTTPRoutes → tenant Services. This is the direct-BFF network path of hardening item 5. |
| 5 | **ClusterMesh blocker A — cluster identity collision.** Both clusters run `cluster-name: default`, `cluster-id: 0`. ClusterMesh requires unique names and IDs 1–255. |
| 6 | **ClusterMesh blocker B — pod CIDR overlap.** Hub uses `10.244.0.0/24`, `10.244.1.0/24`, `10.244.2.0/24`; spoke uses `10.244.0.0/24`, `10.244.1.0/24`. Both derive from `podSubnet: 10.244.0.0/16` (`spokepool-clusterclass-v1.yaml:199`). ClusterMesh cannot route between overlapping pod CIDRs. |
| 7 | **ClusterMesh blocker C — the deep one.** All waypoint workloads (`frontend-workload`, `bff-workload`, `sdk-workload`, `builder-serve-workload`) run on `flatcar-node-1`, a NAT'd home worker reachable only over Tailscale. Cilium encapsulates directly to the node hosting a pod, so the hub must reach `100.85.175.14`. ADR-046 line 42 states the hub does not run Tailscale. **Resolved by the locked decision: the hub joins the tailnet.** |
| 8 | **MCS is entirely absent.** No `clustermesh-apiserver`, no ServiceExport/ServiceImport CRDs, `enable-mcs-api` unset on both clusters. |

**Sequencing consequence:** ADR-050 hardening item 5 says to remove the spoke
HTTPRoutes. Doing that before MCS works takes waypoint down completely, because
the hub has no backend (finding 3). **Cutover is last, not first.**

---

## Work already committed that this plan reverses

Commit `4a1a5197` and the subsequent uncommitted work built for **spoke
termination**. Under ADR-050 these move to the hub:

- `manifests/spoke/spoke-catalog/infra/acme-cluster-issuer.yaml` — per-tenant ACME
  solvers bound to spoke Gateways. The hub already issues via ingress-nginx HTTP-01
  (`hub-core-services/ingress/cluster-issuer.yaml`), which becomes the only issuer
  for public names. Retire the spoke issuer once cutover completes.
- `manifests/spoke/spoke-catalog/infra/external-dns.yaml` — spoke-side external-dns
  publishing spoke Gateway hostnames. Under ADR-050 tenant hostname records are
  owned by the hub. Move external-dns to the hub and scope it to the env zone.
- The cert-manager `ExperimentalGatewayAPISupport` feature gate on the spoke
  becomes unnecessary for public certs (the hub uses the Ingress solver).

Keep: the Hetzner DNS token correction, and the env-zone `DOMAIN` values in
`manifests/environments/*`.

---

## Phase 0 — Make the hub edge reachable

Independent value, and everything else depends on it. Nothing here needs MCS.

1. Create DNS A records for the hub platform hostnames → `65.109.41.89`
   (`api.dev`, `auth.dev`, `console.dev`, `argocd.dev`, `victoriametrics.hub.dev`
   under `dev.nutgraf.in`, per the locked zone decision).
2. Confirm the six stuck certificates issue once the names resolve. They are
   already requested; cert-manager retries on its own.
3. Delete the accumulated stale `cm-acme-http-solver-*` Ingresses and orphaned
   Challenges after issuance.
4. Update hub Ingress hosts and `dev-certificates.yaml` to the env-zone names.

**Gate:** all six certificates `READY=True`; `curl https://api.dev.nutgraf.in`
returns a trusted response.

## Phase 1 — Hub joins the tailnet (ADR-046 amendment)

The hub control-plane and worker nodes need `tailscaled`, using the same mechanism
the spoke CP already uses — ClusterClass `preKubeadmCommands` with the authkey and
hostname delivered from the `tailscale-hybrid-psk` secret
(`manifests/providers/hybrid/k8s/tailscale-psk-es.yaml`).

- Apply to the hub's own ClusterClass/bootstrap path, mirroring
  `spokepool-clusterclass-v1.yaml`.
- **Do not advertise or accept podCIDR subnet routes** — ADR-046 addendum 17.1.
  Tailscale stays a node-level underlay; Cilium owns pod routing.
- Amend ADR-046 line 42 (topology table) which currently states the hub does not
  run Tailscale.

**Gate:** hub node can reach `100.85.175.14` (flatcar-node-1) over the tailnet.

## Phase 2 — ClusterMesh prerequisites

1. **Unique cluster identity.** Assign `cluster-name` / `cluster-id` per cluster
   (e.g. hub = 1, spoke = 2). Must be set on both; `default`/`0` is invalid for mesh.
2. **Re-IP the spoke pod CIDR.** Change `podSubnet` from `10.244.0.0/16` to a
   non-overlapping range (e.g. `10.245.0.0/16`) at
   `manifests/providers/_shared/spokepool-clusterclass-v1.yaml:199`.
   `serviceSubnet` may stay — ClusterIPs are cluster-local and may overlap.
3. **Rebuild the spoke.** podSubnet is fixed at kubeadm init; this is not an
   in-place change. The spoke is dev and disposable, and the collapsed ClusterClass
   already requires a reprovision for any template edit.

**Gate:** `cilium status` on both clusters reports the new cluster name/ID; pod
CIDRs are disjoint.

## Phase 3 — ClusterMesh + MCS enablement

1. Deploy `clustermesh-apiserver` on both clusters, exposed over the tailnet
   (not the public internet).
2. Enable `enable-mcs-api` on both; install the MCS CRDs
   (`ServiceExport`, `ServiceImport`).
3. Establish the mesh and verify both directions.

**Gate:** `cilium clustermesh status` healthy on both; a test Service exported from
the spoke resolves and connects from a hub pod.

## Phase 4 — Export tenant services and wire AgentGateway

1. `ServiceExport` for `frontend-workload` and `bff-workload` in `tenant-waypoint`
   (and the oranger equivalents when it gains a public surface).
2. Confirm the AgentGateway backends in `agentgateway-config.yaml` resolve through
   MCS. The existing backend hostnames may need the MCS form
   (`<svc>.<ns>.svc.clusterset.local`) rather than `cluster.local`.
3. Verify the hub can reach the BFF/frontend **before** any DNS change.

**Gate:** from the hub, a request to the AgentGateway route returns the tenant
frontend — while public DNS still points at the spoke.

## Phase 5 — Zone and certificates at the hub

1. Tenant hostnames become `waypoint.dev.nutgraf.in` /
   `api.waypoint.dev.nutgraf.in`, served by the **hub** AgentGateway.
2. Certificates issue at the hub via the existing ingress-nginx HTTP-01
   `letsencrypt-prod` ClusterIssuer. Use the staging issuer while iterating —
   Let's Encrypt allows 5 duplicate certs per week.
3. **Identity stack follows the zone** (locked decision 3). This is the widest
   blast radius in the plan:
   - Hydra issuer `https://auth.nutgraf.in` → `https://auth.dev.nutgraf.in`
     (`hub-core-services/identity/ory-hydra/values.yaml`)
   - Kratos `base_url` and allowed return URLs
     (`ory-kratos/values.yaml`)
   - AgentGateway OAuth `redirectURI` values (`agentgateway-config.yaml`)
   - **`internal/auth-proxy/hydra.go:67,92`** — callback URLs are compiled into the
     Go binary and must become configuration
   - Registered Hydra client redirect URIs must be updated to match, or login breaks
     with `redirect_uri mismatch`
4. Move external-dns to the hub, scoped to the env zone, sourcing from Ingress and
   Gateway. Retire the spoke-side instance.

**Gate:** trusted HTTPS on all hub and tenant hostnames; a full OIDC login
round-trip succeeds against the new issuer.

## Phase 6 — Cutover (ADR-050 hardening item 5)

Only after Phase 4's gate passes.

1. Repoint tenant DNS from `77.42.12.176` (spoke) to `65.109.41.89` (hub).
2. **Remove the spoke HTTPRoutes** (`waypoint-frontend`, `waypoint-bff`) and the
   spoke `Gateway`. These live in the private fleet-registry repo.
3. Remove `extraServices` 80/443 from the spoke CAPH load balancer
   (`spokepool-clusterclass-v1.yaml`) — the spoke no longer serves public traffic.
4. Retire the spoke ACME ClusterIssuers and the spoke `waypoint-tls` Certificate.
5. Verify the spoke is not publicly reachable on 80/443 at all.

**Gate:** tenant services reachable **only** through the hub AgentGateway;
direct spoke access returns nothing.

## Phase 7 — Close ADR-050's security contracts

### Platform-side (zero-ops)

| Contract | Work |
|---|---|
| Hardening 5 — direct-path denial | Phase 6 |
| Hardening 1 — separate OAuth client credentials | Verify `<tenant>-bff-client` secret reaches only the BFF; auth-proxy holds it for Day-0 registration only |
| P0-1 — audience/scope contract | Fixed audience per tenant on the Hydra client; AgentGateway validates |
| P0-3 — gateway→BFF credential trust | Confirm JWT policy strips client `Authorization` before transformation |
| P1-4 — cookie/session contract | AgentGateway OIDC cookie flags, TTL, encryption key handling |
| P1-5 — failure-closed | Confirm each dependency fails closed, incl. **MCS unavailable → BFF inaccessible** |
| P1-6 — audit events | Emit the named OIDC/authorization events; never log tokens or codes |
| P1-7 — local dev auth | Explicit opt-in, fail-closed in production |
| Upstream gaps 1–5 | AgentGateway lacks logout endpoint, cookie key rotation, claims revalidation, structured audit, JWKS refresh. Track upstream; **gap 5 means Hydra key rotation requires a gateway restart** — document as an operational constraint |

### Fleet-registry side (specify; another owner implements)

| Contract | Requirement |
|---|---|
| Hardening 2 — token store | Shared fleet Redis, tenant-namespaced, TLS-only, subject-scoped with TTLs |
| Hardening 3 — atomic refresh rotation | `SET NX PX` cross-replica mutex; access+refresh stored atomically |
| Hardening 4 — multi-replica | No sticky sessions required once the shared store is in place |
| Hardening 6 — forged-header tests | Prove `X-Auth-Subject` / `X-Auth-Tenant` from a client cannot override gateway identity |
| Hardening 7 — integration test | login → code → exchange → store → resource, plus expired-token refresh and concurrent refresh |
| Hardening 8 / P0-5 — logout & revocation | BFF deletes stored tokens; gateway session is independent |
| P0-2 — two-layer authorization | Gateway-level and BFF-level checks |
| P0-4 — delegated-token identity | Bind stored tokens to subject |
| P1-1 — state→session binding | OAuth `state` bound to the session |
| P1-2 — refresh failure state machine | Defined behaviour on rotation failure |
| P1-3 — Redis bearer-token threat model | ACL users, key namespaces, documented threat model |
| Route removal | Delete `waypoint-frontend` / `waypoint-bff` HTTPRoutes and the spoke Gateway (Phase 6) |

## Phase 8 — Reconcile the ADRs

1. **Rewrite ADR-051** (currently Proposed) so hostnames terminate at the hub. Its
   Impact section flags the ADR-050 conflict as unresolved; that flag is replaced
   by the decision recorded here. Ownership rows for hostname records and public
   certificates move to the hub.
2. **Amend ADR-046** — hub joins the tailnet (line 42 topology table); note that
   tenant workloads on home workers are now reached by the hub via ClusterMesh over
   the tailnet; record the spoke pod CIDR change.
3. **ADR-050 → Accepted** once the P0 contracts are enforced.

---

## Verification

```sh
# Phase 0 — hub edge reachable and trusted
kubectl --kubeconfig=<hub> get certificate -A | grep -v True   # expect: none pending
curl -sS -o /dev/null -w '%{http_code}\n' https://api.dev.nutgraf.in

# Phase 1 — hub on the tailnet, no subnet-route regression
tailscale status                       # hub node present
ip route show table 52 | grep 10.24    # expect empty (ADR-046 addendum 17.1)

# Phase 2/3 — mesh healthy
cilium status | grep -i cluster        # unique name/id, both clusters
cilium clustermesh status              # connected both directions

# Phase 4 — hub reaches the tenant BEFORE any DNS change
kubectl --kubeconfig=<hub> exec -n platform-edge deploy/agentgateway -- \
  curl -sS -o /dev/null -w '%{http_code}\n' http://frontend-workload.tenant-waypoint.svc.clusterset.local:3000

# Phase 6 — cutover complete, spoke not publicly reachable
dig +short waypoint.dev.nutgraf.in     # expect the HUB LB
curl -sS -o /dev/null -w '%{http_code}\n' https://waypoint.dev.nutgraf.in   # 200, trusted
curl -sS --max-time 8 http://<spoke-lb-ip>                                  # expect no service

# ADR-050 item 5 — the assertion that matters
kubectl --kubeconfig=<spoke> get httproute,gateway -A   # expect: no public tenant routes
```

## Risks

- **Cutover is all-or-nothing per hostname.** DNS moves and spoke routes are
  removed together; if the hub path regresses, the tenant is down. Phase 4's gate
  exists to prove the hub path *before* DNS moves.
- **Identity-stack change is the widest blast radius.** Changing the Hydra issuer
  invalidates existing sessions and requires every registered redirect URI to move
  in lockstep. Login breaks on any mismatch. The two compiled Go constants are the
  easiest to miss.
- **Spoke rebuild is required** (Phase 2 pod CIDR). Combine with the pending
  reprovision the collapsed ClusterClass already needs.
- **Hub on the tailnet widens the hub's exposure**, which ADR-046 originally
  avoided by design. Tailnet ACLs should scope what the hub may reach.
- **AgentGateway upstream gap 5** — static JWKS means Hydra signing-key rotation
  requires a gateway restart. This is an availability constraint on a routine
  security operation.
