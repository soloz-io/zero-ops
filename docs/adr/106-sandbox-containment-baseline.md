# ADR-106: Every sandbox is contained by default: allow-listed egress, default-deny ingress, bounded resources

**Date:** 2026-10-20
**Status:** Accepted. Implemented and awaiting deploy. Rollout is gated on the
datapath probe (§1) and on each application declaring its sandbox hosts.
**Amends:** ADR-047 (sandbox default-deny), ADR-052 (sandbox compute)
**Relates to:** ADR-107 (sandbox isolation runtime), ADR-108 (untrusted preview origin)

## Context

A sandbox runs tenant-authored code. Verified on nutgraf-01 on 2026-10-20:

1. **Egress was open to the whole internet on :443.** A STOPGAP `toEntities: world`
   rule was added because the `toFQDNs` allow-list never filled. Nothing excluded
   private address space from it.
2. **Ingress was unrestricted.** No policy that selects sandbox pods had an
   `ingress` section, so any pod in any tenant could reach any sandbox port.
3. **Nothing was bounded.** The operator set requests only, with no limits and no
   ephemeral-storage bound. The only quota is scoped to the burst PriorityClass.

These hold for every sandbox, whatever a product does with it.

## Decision

### 1. Egress: declared hosts only

A sandbox reaches nothing outside the cluster that hasn't been declared. Three
sources, and only these:

| Grant | Owner | Where |
|-------|-------|-------|
| DNS, through Cilium's DNS proxy; the tenant DB pooler | platform | `agent-sandbox-default-deny` |
| The application's external hosts, by exact name, :443 | the application, by declaration | universal-tenant `sandbox.egress.hosts` → `sandbox-egress-declared` |
| The job's object storage, by name, :443 | platform, per job | operator: `<job>-storage`, its host read from the same ConfigMap as the job's storage endpoint |

**Why names work now.** At Cilium v1.17.18, `toFQDNs` learns addresses only from
DNS answers the DNS proxy sees, and the proxy intercepts only where an L7 DNS rule
allows the query (`Documentation/security/policy/language.rst:621-624`). The sandbox
DNS rule had no `rules: dns:` section, so the cache stayed empty. That is why
`world:443` was granted, and why it has now been removed rather than kept beside the
fix.

**No private address, whatever a name resolves to.** `egressDeny` covers 10/8,
172.16/12, 192.168/16, 100.64/10 and 169.254/16 on every port. A declared host whose
DNS answer points into private space can't reach the hub, the nodes or cloud
metadata. CIDR rules don't apply where both sides are Cilium-managed or a node IP
(`language.rst:525-526`), so the pooler and DNS are unaffected.

**Undeclared is unreachable, and fails visibly.** A missing host shows up as a
connection timeout inside the sandbox, and Hubble records a policy-denied verdict.
The chart refuses wildcards, IP literals, schemes, ports, uppercase and duplicates
at render time.

**Datapath proof before rollout.** The same L7 DNS rule in the shared
`tenant-dns-egress.yaml` broke DNS for every tenant workload on 2026-09-30.
`probes/106-sandbox-dns-visibility-probe.yaml` runs this exact posture on one pod
first. If its P1 fails, the rollout stops: the cause is found and fixed, and no
wider grant is put back.

### 1a. Tenant-wide grants don't reach sandboxes or peers

`tenant-identity-endpoint-egress` gives every pod with a `tenant-id` label
`world:443`, and `tenant-dns-egress` gives it plain DNS. The operator stamps
`tenant-id` on sandbox and peer pods, and Cilium allows add up. Left alone, those
two policies would cancel the allow-list and the renderer's containment without any
error. Both now exclude `agents.x-k8s.io/sandbox` and
`compute.nutgraf.in/session-peer`. Each of those has its own posture with its own
DNS rule, so the exclusion removes nothing a sandbox has today.

### 4. A session's renderer (product item 5)

A renderer is an EphemeralJob with `spec.peer` (`jobName`, `targetPort`,
`listenPort`).

The operator:

- marks its pod `compute.nutgraf.in/session-peer`;
- refuses a peer carrying the sandbox label, which would inherit the external hosts
  the application declared for its sandboxes;
- refuses a peer whose owner is missing, finished or itself a peer, or whose ports
  aren't declared;
- always runs it under ADR-107's RuntimeClass.

The base posture is `session-peer-default-deny` (spoke catalog): ingress from the
node only, and egress DNS only, through the DNS proxy. On top of that, the operator
renders one policy pair per session, `<peer>-peer` and `<peer>-peer-owner`. Both
select pods by the operator's own UID label, which a submitter can't forge, and both
are owned by the peer job (`internal/controller/session_peer.go`):

- renderer → its own sandbox, on the app's declared port;
- sandbox → its renderer, on the renderer's declared port;
- the renderer gets no other ingress, and its only egress is DNS and the session's
  S3 endpoint. S3 is reached by name, so this needs item 1.

The renderer runs under ADR-107's RuntimeClass and with §3 limits. The evidence is
the renderer being refused when it tries another session's sandbox or renderer, a
public host, the DB pooler, or the SDK.

## Validation

`scripts/validate/cluster/106-sandbox-containment.sh` (S1–S5), read-only. The check
that a platform policy selects something (`76-*`) no longer counts an exclusion key
as evidence of a wrong selector.

## Acceptance criteria

1. The probe passes P1–P4. Then, on a sandbox: `cilium-dbg fqdn cache list` lists
   the allow-listed names, a listed name connects, and any other name times out.
2. Until then, a sandbox connecting to a 10/8 or 169.254/16 address on :443 is
   dropped, and Hubble shows a policy-denied verdict.
3. A pod outside the sandbox's namespace, and a non-SDK pod inside it, can't open
   :3000 on a sandbox. The SDK can, and a session works end to end.
4. A sandbox pod's containers all carry limits. A job stating its own limits keeps
   them. A job requesting more than a default gets a limit equal to its request.
5. Item 4's criteria are met once the renderer exists.

## Consequences

- Each new in-cluster caller of a sandbox needs a named rule. Forgetting one shows
  up as a timeout, not a refusal, because Cilium drops without an RST. The
  2026-10-22 session and Hubble drop verdicts are how a missed caller is found.
- Default limits change scheduling for no one, because requests are unchanged.
  They do change what a runaway workload can take: it is OOM-killed or throttled
  instead of starving its node.

## References

- Plan: `docs/plans/adr-039-sandbox-platform-capabilities.md`
- `manifests/spoke/spoke-catalog/infra/sandbox-network-policy.yaml`
- `manifests/tenants/charts/universal-tenant/templates/sandbox-ingress.yaml`
- `manifests/spoke/spoke-catalog/probes/106-sandbox-dns-visibility-probe.yaml`
- `operators/ephemeral-job-operator/internal/controller/ephemeraljob_controller.go` (`withLimits`)
