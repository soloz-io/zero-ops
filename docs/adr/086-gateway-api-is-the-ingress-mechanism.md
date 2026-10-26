# ADR-086: Gateway API Is the Ingress Mechanism

**Date:** 2026-09-21

**Status:** Proposed

*Constrained by: ADR-039 (Platform Ownership Model), ADR-046 (Hybrid Provider and Home Worker), ADR-050 (Tenant Authentication via AgentGateway), ADR-051 (Environment DNS Naming and Public Gateway TLS), ADR-065 (The Control Plane Ships Into the Box), ADR-071 (No Catalog), ADR-085 (The Cilium and Gateway API Upgrade)*

> One proxy on the path, and it is the one the CNI already runs.

## Context

Every public hostname this platform serves — the management cluster's own
consoles, a spoke's tenant hostnames, a spoke's observability query endpoint —
arrives through Gateway API resources served by Cilium. No component authors an
`Ingress`, and no ingress controller is deployed. The vendored third-party
charts that ship `Ingress` templates have them disabled.

That arrangement has never been recorded as a decision. It is threaded through
ADR-046, ADR-051 and ADR-083 as an assumption, which leaves the platform's
routing mechanism established by convention rather than by choice, and leaves
any constraint that follows from it — including a capability blocked on a
Gateway API feature — reading as an accident.

Two facts bound the choice.

Cilium is already this platform's CNI and is not optional. The infrastructure is
CAPI on Hetzner, where no managed network plugin exists, so the platform owns a
CNI regardless of how it routes ingress. Cilium implements Gateway API natively
through the Envoy it already runs for its own policy enforcement.

Ingress NGINX, the controller the reference model uses and the most widely
deployed alternative, is retired. Maintenance was best-effort until March 2026;
there are no further releases, no bugfixes, and no fixes for security
vulnerabilities discovered after that date. Existing deployments continue to
run and existing artifacts remain available, so this is not an outage — it is
the absence of a future.

## Decision

**Gateway API is the platform's ingress mechanism, served by Cilium.** Public
routing is expressed as `Gateway`, `HTTPRoute` and — where the implementation
serves it — `ListenerSet`. The platform authors no `Ingress`, and a vendored
chart's `Ingress` template stays disabled.

**No second ingress proxy is deployed.** Adding one would place two proxies on
the same path: Cilium's Envoy terminates and enforces policy for the traffic it
already carries, and a separate controller would either duplicate that or sit in
front of it. Each hop is a place for the datapath invariants ADR-046 records to
be re-established or lost, and on the hybrid provider those invariants were
expensive to obtain.

**A capability that requires a Gateway API feature waits for that feature.** It
is not delivered through a second mechanism kept for the purpose. Retaining an
alternative to reach around a version gap means running and securing that
alternative permanently, which is a larger commitment than the wait.

### Alternatives considered

**Ingress NGINX.** Rejected: retired. Adopting it would mean taking a dependency
that receives no security fixes, and taking it specifically to obtain
decentralised listener ownership that Gateway API expresses natively through
ListenerSet. The reference model still deploys it; this is a case where
following the principle and following the implementation diverge, and the
principle is what transfers.

**A maintained third-party controller** — Traefik, Envoy Gateway, or another.
Rejected: each is a second proxy alongside Cilium's Envoy, bought to avoid
waiting for a feature the existing proxy will serve. The cost is permanent and
the benefit is temporary.

**Both mechanisms, choosing per capability.** Rejected: two routing models mean
two ownership models, two certificate delivery paths and two places a hostname
can be published from. ADR-051 assigns hostname routing to the platform as a
security control, and a second mechanism is a second place that control has to
hold.

## Ownership

This ADR establishes the routing mechanism and does not own platform resources.
Ownership of the objects expressed through it is assigned by ADR-051 — the
Gateway, its class and its delegation policy to the platform, a listener and its
certificate to the consumer that declares it. For resource ownership, see
ADR-039.

## Consequences

### Positive

One proxy on the path, already present, already carrying the datapath
invariants ADR-046 establishes.

No dependency on a retired component, and no obligation to secure one.

Listener and certificate ownership is expressible natively, which is the
property ADR-051 requires.

### Negative

The platform's ingress capability advances at the pace of Gateway API and of
Cilium's implementation of it. A feature not yet served is a capability not yet
available, and ADR-051 requires such a capability to be blocked rather than
approximated.

Gateway API's API surface changes faster than the Ingress API it succeeds, so
the version pin moves more often and moves together with the CNI (ADR-085).

### Neutral

The reference model's ingress implementation is not adopted. Its ownership
principle is, and ADR-051 expresses it in Gateway API terms.

## Impact

Records a decision the platform already implements; no manifest changes.

Explains ADR-051's position that a listener capability is blocked when Gateway
API does not serve it, which is otherwise an unexplained constraint.

Gives ADR-085's upgrade a second reason beyond ListenerSet: the platform's
ingress stack stays current with the only mechanism it runs.

## References
- ADR-039: Platform Ownership Model
- ADR-046: Hybrid Provider and Home Worker
- ADR-050: Tenant Authentication via AgentGateway
- ADR-051: Environment DNS Naming and Public Gateway TLS
- ADR-065: The Control Plane Ships Into the Box
- ADR-071: No Catalog
- ADR-085: The Cilium and Gateway API Upgrade
