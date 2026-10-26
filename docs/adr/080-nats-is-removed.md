# ADR-080: NATS Is Removed

**Status:** Accepted (2026-09-13)

## Context

The platform shipped a NATS cluster on every hetzner hub, a leaf node in every
spoke, a `messaging` capability toggle, a JetStream stream declaration on
`HubEnvironment`, per-spoke leaf credentials generated into Infisical and
delivered by Crossplane, a `nats-leafnode-client-cert` PKI identity, an Alloy
scrape job, and a `NATS_URL` handed to kube-sbt.

Nothing used it.

The evidence, gathered before deciding:

**No code path publishes or subscribes.** `internal/kube-sbt/providers/nats`
held the only `IEventBus` implementation, and `NewEventBus` had zero callers
anywhere in the tree. `cmd/kube-sbt` — the binary whose Deployment received
`NATS_URL` — contained no NATS or EventBus reference at all, so that variable was
set and ignored. `cmd/mcp-server` declared `var eventBus interfaces.IEventBus`,
never assigned it, and carried a `// TODO: Initialize event bus`; it also does
not compile, for unrelated reasons.

**The one runtime consumer created streams nobody read.** The hub-operator's
Phase 3 called `CreateOrUpdateStreams` from `HubEnvironment.spec.nats`. That is
the only thing in the platform that ever touched a NATS connection.

**On a live box, nothing had ever connected.** `varz` reported `connections: 0`
with `in_msgs: 230` against `total_connections: 230` — one message per
connection, which is a handshake, not traffic. `jsz` reported `streams: 0,
consumers: 0, messages: 0, bytes: 0`. JetStream was enabled and empty. The
`NATSStreamsConfigured` condition never appeared on the HubEnvironment, so even
the operator's own phase had never completed.

**It was already half-withdrawn.** ADR-046 states plainly that *"NATS has no
consumer inside hub-core-services"* and gates it out of the hybrid placement
class, so a whole class of box already ran without it and nothing noticed.

**It was costing real failures.** ADR-046 records a NATS `volumeClaimTemplates`
patch wedging an Application on StatefulSet spec immutability. The same fault
recurred: `platform-nats` was found stuck in `Running`, retry #14, on
`StatefulSet.apps "nats" is invalid: spec: Forbidden`. A component nothing uses
was consuming the fleet's attention and one of ArgoCD's operation slots.

## Decision

**NATS is removed from the platform, hub and spoke.**

Removed: the hub StatefulSet and its hetzner provider overlay; the
`platform-nats` Application and the `messaging` capability that gated it; the
spoke leaf node, its six placement overlays and its `nats-leafnode-client-cert`;
the `platform-messaging` namespace on both sides; the per-spoke leaf credentials
in both Crossplane compositions, along with their Infisical key mappings and
generation entries; `HubEnvironment.spec.nats` with its `NATSConfig` and
`NATSStream` types; the hub-operator's stream phase, readiness check and
StatefulSet watch; `internal/kube-sbt/providers/nats`; the `NATS_URL`
environment variable; the Alloy scrape; and the `nats-io` module dependencies.

### What is not removed

`interfaces.IEventBus` stays. It is a transport-agnostic port, and removing NATS
is not the same decision as removing the abstraction an event bus would plug
into. Its consumers — `controlplane`, `applicationplane`, `billing`,
`argocdagent` — are unchanged and, as before, construct no bus.

### The roles ADR-034 assigned to it

ADR-034 lists NATS under *"cross-cluster telemetry buffering (JetStream)"* and
*"remote trigger executions and Hub-to-Spoke orchestration commands"*. Neither
was built, and both have since been answered elsewhere: ADR-078 makes
observability a capability that does not use NATS, and ADR-077's Support Agent
reports over egress HTTPS with a client certificate. ADR-034's failure matrix is
amended accordingly.

**This does not decide that the platform will never need a message bus.** It
decides that carrying one for four years of "later" is worse than adding one when
something needs it. A future transport is a new decision with a live consumer to
justify it, not the resurrection of this one.

## Consequences

### Positive

1. One less stateful component to run, back up, place, patch and reason about on
   every hetzner hub, and one less in every spoke.
2. A recurring Application wedge disappears with the StatefulSet that caused it.
3. Three module dependencies leave the CLI a tenant runs.
4. The capability list stops promising something no box exercises. A toggle for a
   component nothing uses is worse than no toggle: it reads as a supported
   choice.

### Negative

1. A tenant that had set `capabilities.messaging.enabled` finds the key gone. It
   was on by default everywhere and gated nothing observable, so the effect is
   confined to the value file.
2. Removal prunes the StatefulSet and its PVC on the next sync. There is nothing
   in it — `messages: 0` — but it is still a delete.
3. Anything later needing hub↔spoke messaging starts from nothing. Accepted: see
   above.

## Impact

- `02-platform-data-appset.yaml` no longer generates `platform-nats`; boundary 02
  is CNPG and Redis.
- Both spokepool compositions drop two `ClusterResourceSet` entries. **The
  `resources[]` array is positional and its patches address it by index**, so the
  survivors were renumbered: the list is now 17 entries and the eight index
  references were remapped.
- `post-bootstrap-validate.sh` loses section 8; sections 9–14 renumbered to 8–13.
- **Amends ADR-034.** NATS leaves the failure-domain matrix.
- **Amends ADR-046.** Its hybrid gating rationale is moot; the component it gated
  no longer exists.
- **Amends ADR-066.** `messaging` is withdrawn from the selectable capabilities.

## References

- ADR-034: Control Plane Failure Domains — where NATS was assigned its roles
- ADR-046: Hybrid Provider — already recorded that it had no consumer
- ADR-066: The Platform Boundary — the capability list it leaves
- ADR-077: The Support Agent — reports over egress HTTPS, not a bus
- ADR-078: The Observability Capability — telemetry without NATS
