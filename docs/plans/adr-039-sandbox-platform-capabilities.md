# Plan: platform capabilities blocking product ADR-039 (sandbox containment + previews)

Started 2026-10-20. Owner: platform team lead. Executor: team member (TM).
Status key: TODO / IN PROGRESS / BLOCKED / DONE. Nothing is implemented until the
user approves this plan.

## Request (product team, 2026-10-20)

Seven items product ADR-039 depends on:

1. Sandbox egress allow-list
2. Sandbox inbound default-deny
3. Sandbox resource limits
4. Stronger isolation for an untrusted browser (RuntimeClass)
5. A separate renderer pod
6. A preview hostname per session, on a separate registrable domain
7. Gateway-enforced access to previews

## Verified live state (nutgraf-01, 2026-10-20, read-only)

| # | Finding | Evidence |
|---|---------|----------|
| 1 | Egress to the internet is open: `toEntities: world` on :443 (marked STOPGAP). The `toFQDNs` allow-list is kept but doesn't take effect. | `manifests/spoke/spoke-catalog/infra/sandbox-network-policy.yaml:110-140` |
| 2 | Inbound isn't restricted. The 3 policies that select `agents.x-k8s.io/sandbox` (CCNP `agent-sandbox-default-deny`, CNP `sandbox-to-sdk`, CNP `sdk-sandbox-egress-contract`) are egress-only. No policy has an `ingress` section, so the endpoint stays allow-all inbound. | `kubectl get ccnp,cnp -A` |
| 3 | No limits anywhere. The operator sets **requests only** (`withRequests`, `ephemeraljob_controller.go:974,1190,1198,1925`; default 2 CPU/4Gi; sidecars use their own defaults). It sets no `Limits` or `ephemeral-storage`. No LimitRange exists. The only ResourceQuota (`tenant-nutgraf-waypoint/burst-compute`) is scoped to PriorityClass `burst-tenant`, and dev has no burst pool, so standard sandboxes have no quota at all. | grep on the operator; `kubectl get limitrange,resourcequota -A` |
| 4 | The cluster has no RuntimeClass. Nodes are mixed: 2× Flatcar (containerd 2.2.5, kernel 6.12) and 1× Ubuntu (containerd 1.7.26, kernel 6.8). | `kubectl get runtimeclass`, `kubectl get nodes` |
| - | No sandbox pod was running when checked, so the requests were confirmed from source, not from a live pod. | |

## Disposition

| # | Accept? | Record | Target | Note |
|---|---------|--------|--------|------|
| 1 | Accept: platform defect | ADR-106 (sandbox containment baseline, amends ADR-052) | 2026-10-27 | Needs the FQDN root cause first (T1). Until then, an interim tightening that doesn't break sessions. |
| 2 | Accept: platform defect | ADR-106 | 2026-10-23 | Enforce only after the flow inventory (T2). |
| 3 | Accept: platform defect | ADR-106 | 2026-10-24 | Operator default limits plus an ephemeral-storage limit, and a namespace quota for the standard class. |
| 4 | Accept | ADR-107 (sandbox isolation runtime) | decision 2026-10-30 | gVisor RuntimeClass, opt-in per EphemeralJob, with node scheduling. Must be proven on both node OSes (T4). |
| 5 | Don't take as a new primitive | covered by ADR-106 | 2026-11-06 | Alternative: run the renderer as a second EphemeralJob correlated by session label, with a platform-rendered pairing policy (sandbox→renderer on a declared port). No "companion pod" concept. |
| 6 | Accept; needs the user's decision on the domain | ADR-108 (untrusted preview origin) | decision 2026-11-06 | A separate registrable domain has to be bought; wildcard DNS plus a DNS-01 wildcard cert, and a dedicated preview listener. |
| 7 | Accept in modified form | ADR-108 | decision 2026-11-06 | Product shouldn't depend on the app's OIDC session on the preview domain, because a separate registrable domain means no shared cookies. Alternative: a short-lived signed capability minted by the app and exchanged on first hit for a host-only cookie that the gateway validates. Which agentgateway v1.5.0 mechanism can do this needs verifying in source (T6). |

## Tasks for the team member

Each task is read-only research unless stated. **Gate for every task:** no cluster
writes, no commits or pushes, no change to any policy, chart or operator. If a finding
contradicts this plan, stop and report; don't work around it.

- **T1 (item 1): FQDN root cause. TODO.** At Cilium v1.17.18 (`reference-projects/cilium`, `git show v1.17.18:`), find why the `toFQDNs` rules in `sandbox-network-policy.yaml` don't populate. Check for a missing DNS L7 rule (`toPorts.rules.dns`), DNS proxy settings in `cilium-config-base.yaml`, the resolver path (node-local or CoreDNS, and its namespace/labels), and the `cilium fqdn cache list` output. Deliver: root cause with source lines, the minimal policy diff, and a live command list for the user to run. Gate: stop at the diff.
- **T2 (item 2): inbound flow inventory. TODO.** Using Hubble, or policy audit mode if it's already enabled (don't enable it), list every source that connects TO sandbox pods during one session: the SDK, agent-vault, the operator, probes, workspace-sync. Deliver: a table of source → port → purpose, and a proposed ingress rule set. Gate: don't start a session yourself; ask the lead for one.
- **T3 (item 3): limits sizing. TODO.** From the operator source and Prometheus or Grafana history, if available, give the peak CPU, memory and disk per container for the sandbox workload, agent-vault and workspace-sync. Propose default limits plus an ephemeral-storage limit, and say how a job overrides them. Deliver: the numbers with sources, and the operator change sketched (not written). Gate: no operator edits.
- **T4 (item 4): runtime feasibility. TODO.** Check gVisor (runsc) against containerd 2.2.5 on Flatcar and 1.7.26 on Ubuntu: install path (Flatcar sysext vs apt), the CAPI/KubeadmConfig hook needed (remember [[zero-ops-capi-template-immutability]]), and compatibility with Cilium's datapath, Chromium in gVisor, and the workspace-sync sidecar. Compare with Kata on Hetzner (does it need nested virt?). Deliver: a recommendation with sources. Gate: no node changes.
- **T5 (item 5): pairing policy. TODO, after T2.** Sketch the CNP the operator or chart would render for a session-labelled sandbox→renderer pair. Gate: sketch only.
- **T6 (item 7): gateway mechanism. TODO.** At agentgateway v1.5.0 (`reference-projects/agentgateway`), list which authentication policies the gateway supports (jwtAuth, extAuthz, cookie handling, per-host listeners) and whether a capability → host-only cookie exchange is possible without a custom service. Deliver: findings with file:line. Gate: no design commitments.

## Decisions needed from the user

- **D-PLAN:** approve the dispositions, the ADR numbers 106–108 and the dates above.
- **D-DOMAIN:** which separate registrable domain to buy for previews (item 6).
- **D-INTERIM-1:** interim egress tightening before T1 lands: keep `world:443` but deny link-local, metadata and RFC1918. Verify first that the `world` entity doesn't already exclude them.

## Next step

Wait for D-PLAN. When it's approved, issue T1–T4 and T6 in parallel; T5 follows T2.

## Round 2: product reply (2026-10-20)

Accepted as proposed, with the dates given: ADR-106 (items 1–3), ADR-107, and ADR-108 (item 6).
D-PLAN is settled on the product side.

**Item 5, corrected direction (accepted).** Pairing policy rendered per session, owned by the job:
- the renderer may reach its owning sandbox on the app's declared port (HTTP and WebSocket);
- the sandbox may reach its renderer on the renderer's declared port;
- the renderer accepts no other inbound traffic, and its only other outbound is the session's S3 binding (ADR-052). This needs T1's FQDN fix, because the S3 endpoint is a public host;
- the renderer runs under the ADR-107 RuntimeClass and ADR-106 limits.

Evidence must show the renderer is refused when it tries to reach another session's sandbox or renderer, a public host, the DB pooler, or the SDK. Cilium can't express "same label value as me", so the operator renders one CNP pair per session, owner-referenced for cleanup.

**Item 3.** Ship the default limit and the per-job override on 2026-10-24. **The namespace ceiling waits for product's video-export peak.** Don't set it below that number.

**Item 1.** Product wants the destinations observed from sandboxes over the last 7 days. Retention isn't verified: Hubble's ring buffer may keep only minutes. T1b checks what exists. If nothing does, enabling DNS visibility is a cluster write, so the user runs it, and 7 days are collected from that point.

**Item 2.** Product agreed to a test session. Proposed slot 2026-10-22 (the user sets the time); it feeds T2.

**Item 7.** Product asks for gateway-run SSO on the preview host. Verified at agentgateway v1.5.0:
- the session cookie has Path=/, HttpOnly, SameSite Lax/Strict/None and Secure, and is host-only, but the gateway can't set **Partitioned** (`http/oidc/session.rs:206-305`);
- **one fixed redirect_uri per OIDC policy**, matched on the callback path (`oidc/redirect.rs`, `oidc/mod.rs:131,243`);
- **no `prompt` control**;
- **ext_authz exists** (`http/ext_authz.rs`).

Conclusion: the gateway-run code flow inside a cross-site iframe isn't workable as specified. Without Partitioned, the gateway's transaction and session cookies are third-party in the embed; whether browsers drop them is still to be proven. Per-preview hosts would need one gateway policy and one issuer redirect per preview, and the issuer leg runs framed with no silent-only guarantee.

Proposed for ADR-108 (issuance stays in the platform; product implements no auth):
- a platform preview-access service, reached on the app host behind the existing gateway SSO, mints a single-use handoff bound to one host that expires within seconds;
- the preview host's gateway sends `/_handoff` to the same service, which redeems the handoff, sets a host-only `Secure; HttpOnly; SameSite=None; Partitioned` cookie and redirects to remove the code from the URL;
- every preview request (including the WebSocket upgrade) is checked through ext_authz against the preview record: owner subject, sharing list and expiry;
- revocation is a record update; audit comes from the ext_authz decision log.

Still to prove: Chrome, Safari and Firefox, including one that blocks third-party cookies.

### Task additions (same gates: read-only, no writes, stop at findings)
- T1b: what flow or DNS history exists on nutgraf-01 (Hubble buffer size, relay, exporters, Grafana Cloud). Deliver: what can be produced for 7 days, or the exact enable step for the user.
- T5 (revised): sketch the per-session pairing CNPs for the corrected rules, plus the list of negative tests.
- T6 (revised): (a) confirm the four agentgateway findings above and check whether a response transformation can append `Partitioned` to Set-Cookie; (b) check whether Zitadel v4.15.3 allows wildcard redirect URIs and what frame-ancestors / X-Frame-Options its login UI sends (clone at the tag into the scratchpad); (c) at v1.5.0, check whether ext_authz applies to WebSocket upgrades and what it can pass to the authz service (headers, cookie, host); (d) using current browser vendor docs, check partitioned-cookie support in Safari, Firefox and Chrome. Deliver: file:line or doc URL for each finding.

## Round 3: product accepts the item 7 handoff (2026-10-20)

- Item 5 agreed, including its dependency on item 1 for S3.
- Item 7 accepted. Product's five points are answered in the draft
  `docs/adr/108-untrusted-preview-origin.md`, status Proposed:
  - cookie issuer: §4;
  - fallback: top-level window, §7;
  - expiry and refusals: §6;
  - record and code interface: §2–3;
  - check cost and revocation: §5, no cache, under 2 s measured.
- Decision on 2026-11-06, after the acceptance criteria are proven.
- Video export: product runs a 2K export on 2026-10-23 and sends the session id and
  time window. Platform reads the pod's peak from metrics (T3 confirms the source).
- Test session confirmed for 2026-10-22; the user sets the time.
- The team member was dispatched on T1, T1b, T2-prep, T3–T5 and T6(a–e), all read-only. Waiting for the report.

## Round 4: product questions (2026-10-20)

- Product accepted the ADR-108 interface and will build against it.
- Their four points are folded into the ADR:
  - fallback = the preview opens outside the canvas; Storage Access is evaluated in the proof, not promised (§7, criterion 7);
  - the state endpoint (§3a), with the posted message treated as a hint only (criterion 8);
  - jobRef is mutable and the host belongs to the record; the ownerReference is optional, and expiresAt is required without it (§2, criterion 9);
  - one record per (jobRef, port), 409 PreviewTargetInUse, with a controller tie-break (§2, criterion 10).
- New refusal state `unavailable` (503).

## Round 5: interface closed (2026-10-20)

Product accepted ADR-108 as drafted, including `unavailable`, and builds against it.

Product's choices:
- lifetime tied to the job (ownerReference form);
- the state endpoint is authoritative;
- the preview opens outside the canvas in any browser where the embed fails;
- `409 PreviewTargetInUse` is a product error.

Product's ADR-039 stays Proposed until ADR-108 is decided on 2026-11-06 and the proof is published.

Platform still owes:
- ADR-106 dates: item 2 on 10-23, item 3 on 10-24 (no ceiling), item 1 on 10-27;
- the ADR-107 decision on 10-30;
- the ADR-108 proof and decision on 11-06;
- 7 days of domains;
- the export peak after 10-23;
- the 10-22 session.

Waiting on the team member's research report.

## Status 2026-10-20 (end of day): the team member was stopped and the lead did the work directly

| Item | State |
|------|-------|
| ADR-106 doc | written, Accepted |
| Item 1, root cause | FOUND: the sandbox DNS rule has no L7 `rules: dns:`, and the DNS proxy only intercepts where one exists (Cilium language.rst:621-624) |
| Item 1, interim | DONE in source: egressDeny on private and link-local ranges for :443/:80 |
| Item 1, allow-list | BLOCKED on the probe `probes/106-sandbox-dns-visibility-probe.yaml` (user runs it) and on product's domain list |
| Item 1b, 7-day history | NOT DONE |
| Item 2 | DONE in source: CCNP ingress host-only, plus chart `sandbox-ingress-from-sdk`. Deploy the chart first; the 10-22 session validates |
| Item 3 | DONE in source: operator `withLimits` (4/8Gi/20Gi workload, 1/1Gi/1Gi sidecars); `go build` and `vet` clean. Ceiling waits for the 10-23 export |
| Item 3, standard-sandbox quota | NOT DONE; needs product numbers |
| Item 5 (renderer pairing) | designed in ADR-106 §4; operator code NOT written |
| ADR-107 | NOT STARTED: runtime research (T4) was never completed |
| ADR-108 | interface agreed; implementation NOT STARTED; preview domain not bought |

Nothing is committed.

## Status 2026-10-20 (late): implementation complete in source, nothing committed

Rule set by the user: permanent and idiomatic only. No interim measures, no
fallbacks; any gap fails hard.

| Item | State |
|------|-------|
| ADR-106 §1 egress | DONE. The `world:443` stopgap is REMOVED. The sandbox CCNP carries L7 DNS, the pooler, and a private-range deny. Apps declare hosts (`sandbox.egress.hosts`). The operator grants each job its storage host (`<job>-storage`). Tenant-wide anchors exclude sandboxes and peers |
| ADR-106 §2 ingress | DONE: host-only CCNP ingress, plus `sandbox-ingress-from-sdk` |
| ADR-106 §3 limits | DONE: operator `withLimits`. The ceiling waits for product's 10-23 export peak (an input, not an interim) |
| ADR-106 §4 renderer | DONE: `spec.peer`, `session_peer.go`, `session-peer-default-deny` |
| ADR-107 | DONE: `spec.isolation`, RuntimeClass `sandboxed`, installer DaemonSet, per-node CiliumNodeConfig |
| ADR-108 | DONE: `PreviewAccess` CRD, `cmd/preview-access`, webhook, gateway listener, tenant route, env plumbing. `previewDomain` is REQUIRED; preview-access refuses to start without it |
| Checks | `cluster/106`, `107`, `108` (read-only); `76` exclusion-key fix |
| Operator tests | pass (fixture gained the fleet ConfigMap the storage grant reads) |

Rollout order (the user runs every step):
1. Product declares `sandbox.egress.hosts` (plus `previewAccess.enabled` and `workloadRbac` for previewaccesses).
2. Probe P1–P4 on the spoke.
3. Commit; CI builds the operator image (both binaries).
4. Release the tenant chart, then the spoke catalog.
5. Run the 106/107/108 checks.
6. Label one node for ADR-107.
7. Buy and delegate the preview domain; add it to the dns01 `dnsZones`; set `previewDomain`.

Known remaining gap, NOT changed (it affects every app): `tenant-identity-endpoint-egress`
grants `world:443` to every non-sandbox tenant pod. It is the same pre-existing
stopgap. The permanent fix is the same declared-hosts model for workloads. It is
proposed to the user and waits for consent.

## Round 6: product's declarations (2026-10-20)

- `sandbox.egress.hosts`: api.deepseek.com, api.runpod.ai, api.tavily.com,
  registry.npmjs.org (new: the code builder installs packages in the sandbox).
  Dropped: heygen, elevenlabs, openai, groq (SDK only); github.com and api.github.com.
- `previewAccess.enabled: true`, and `previewaccesses` in `sdk-workload-sa` RBAC.
- Merged before release 2. That merge is gate G2 in the team member's brief.
- Local Kind (decision): not a target for ADR-106/107/108. Dev is the first
  environment where they are enforced and served. Locally, a renderer is refused
  (AdmissionRejected, RuntimeClass not found); previews aren't served. A local
  profile (Cilium in Kind, cert-manager, gVisor on the Kind node image, a local
  preview domain with trusted TLS) is backlog, scoped only if product needs it.

## Round 7: product asks for Kind as a target for ADR-107 and ADR-108 (2026-10-20)

**ADR-107 local: BUILT, not yet run.**
- `manifests/local/kind/{operator,runtime}`, `scripts/local/kind-profile.sh`.
- The operator gained the required `--network-policy=cilium|none`.
- Found and fixed: the per-job CiliumNetworkPolicies would have failed every
  storage-using job on Kind (no `cilium.io/v2`).
- The installer now extracts in its own container (Kind has no bzip2).
- UNVERIFIED until the script runs: gVisor systrap inside a Kind node container.

**ADR-108 local: BLOCKED on a decision.**
- The local stack has no issuer and no tenant gateway: the frontend uses `VITE_DEV_AUTH`.
- preview-access identifies the app by its tenant-gateway pod and the user by a
  platform-issued session; locally there is neither.
- No header-trust bypass will be built. That would be a stub on a security path.
- Options are with the user: (A) local runs the real path; (B) previews tested in dev only.
