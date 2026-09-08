# ADR-069: The Maintenance Promise

**Date:** 2026-09-08
**Status:** Proposed

## Context

Maintenance is what the platform sells, and ADR-065 gives the tenant the authority to accept or decline it: the platform proposes and never applies. ADR-066 says what the platform maintains. Neither says under what terms the promise holds.

Four facts make that a gap rather than an omission.

**A tenant can decline indefinitely.** ADR-064 records propagation as bounded by the slowest approver and stops there. A tenant may therefore sit on a version with a known vulnerability while holding a maintenance agreement, and nothing states what that agreement still covers.

**The obligation is wider than it was.** ADR-066 places a database engine, an identity provider, a message bus and a mail server on the platform's side, on every box that selects one. An open-ended promise over that surface is not one that can be met.

**A capability's lifecycle has an end.** ADR-066 gives capabilities a progression from catalogued to removed and names what a tenant is owed at the end of it as a question it does not settle.

**Support is already conditional in one place.** ADR-067 makes a maintenance claim extend exactly as far as the telemetry a tenant exports, and says plainly that what is not observed is not supported. The same conditioning has not been applied to what a tenant runs.

## Decision

**The platform never forces an upgrade. It stops promising maintenance for a version that has fallen outside support.**

That sentence keeps ADR-065 intact. The tenant's authority to decline is unchanged and unconditional; what changes is what the platform is answerable for afterwards.

### A version is supported for a stated window

Every published bundle version is supported for a window declared when it is published, expressed in versions, in time, or in both. A tenant inside that window holds the full promise.

The window is a property of the version rather than of a tenant, so what any tenant is entitled to is derivable from what it runs, and no agreement is negotiated per tenant.

### Support degrades in stated steps, and running never stops

A version moves through **supported**, then **deprecated** — maintained, with an end date announced — then **unsupported**, where the platform answers on a best-effort basis and makes no undertaking about outcome, and finally **end-of-life**, where it makes none at all.

Nothing in that progression stops a cluster running. ADR-065 leaves the tenant holding a working box and every artefact it needs, and no step here withdraws anything from a tenant: the cluster reconciles what it reconciled yesterday. What lapses is an obligation, not a capability.

### A security fix is proposed outside the cadence

A fix for a known vulnerability is raised as its own proposal, as soon as a version carrying it is published, rather than waiting for the next promotion. It says what it fixes and what declining it leaves exposed.

The tenant may still decline. What the platform owes is that the choice was offered promptly and its consequence stated, not that the change was made.

### Declining moves a version toward unsupported; it is not a breach

A tenant that declines is exercising the authority ADR-065 gives it. The version it stays on continues through the progression above on the same schedule as for anyone else, and support follows the version.

This is the difference between a platform that cannot compel and one that cannot decline the consequences of not compelling. The tenant chooses; the platform is answerable for what it promised about what the tenant is running.

### The promise is bounded by evidence as well as by version

ADR-067 conditions the promise on exported telemetry. This ADR conditions it on version currency. Both bound the same promise, and a tenant outside either is outside the full promise: the platform cannot be answerable for a cluster whose state it cannot see, nor for a version it no longer maintains.

### A capability leaving the catalogue carries notice and a path

When a capability reaches end-of-life the platform states the date, and offers either a supported successor with a migration path or notice sufficient for a tenant to arrange its own. Deprecation is an announcement with a date, not a withdrawal.

What the platform does not do is remove a capability from a running box. A tenant past end-of-life keeps what it has and holds no promise about it.

### Alternatives considered

**Maintain every version indefinitely.** Rejected. The obligation ADR-066 defines spans a database engine, an identity provider, a message bus and a mail server, and an unbounded version window multiplies that surface by every version ever published. The promise would be one nobody could meet, and a promise that cannot be met is worse than a narrower one that can.

**Compel the upgrade.** Rejected outright. It is the one thing ADR-065 forbids, and the reason a tenant can believe the custody claim at all. A platform that can compel a change can compel any change.

**Terminate the agreement when a tenant declines.** Rejected. Declining is the authority the platform grants, and punishing its exercise would make the grant nominal. Support follows the version, which lets a tenant decline a particular upgrade, stay current overall, and lose nothing.

**Condition support on telemetry alone.** Rejected as insufficient. Evidence tells the platform what a cluster is doing; it does not make an unmaintained version maintainable.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Support window per version | `zero-ops` release record | Platform | Release pipeline | Platform, Tenant | Day-1+ |
| Version a tenant runs | `<tenant>-gitops` | Tenant | ArgoCD | Tenant control plane | Day-1+ |
| Capability lifecycle state | `zero-ops` catalogue | Platform | Release pipeline | Tenant | Day-1+ |
| Support entitlement | derived from the two above | Platform | — | Platform, Tenant | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

What a tenant is entitled to is derivable from the version it runs and the telemetry it exports, so it is the same question for every tenant and is answerable without reference to an agreement.

The platform's obligation is bounded by a window it declares rather than by however long any tenant chooses to wait, which is what makes the obligation over ADR-066's surface meetable.

A tenant that declines an upgrade loses no capability and no data, and can return to support by accepting a later proposal. Declining is reversible.

A security fix is offered promptly and its consequence stated, so a tenant that declines does so knowing what it is accepting.

### Negative

A tenant can hold a vulnerable version, and the platform's remedy is to stop promising rather than to act. That is the cost of ADR-065's boundary, and this ADR bounds the platform's exposure without reducing the tenant's.

Support windows create a floor on upgrade frequency. A tenant that wants to move rarely and stay fully supported cannot, and for some that will be the wrong platform.

Every published version carries a maintenance obligation for the length of its window, so publishing more often is not free.

Two conditions now bound the promise — version currency and exported telemetry — and a tenant can be outside one while inside the other. Which of them is unmet has to be said plainly, or a lapse will be read as arbitrary.

## Impact

- **Extends ADR-064.** Propagation bounded by the slowest approver was recorded there as a consequence; the terms under which that boundedness is acceptable are recorded here.
- **Extends ADR-066.** The end-of-life question it names as unsettled is settled here.
- **Extends ADR-067.** Telemetry and version currency bound the same promise, and both are stated as conditions on it.
- **Confirms ADR-065.** The platform never compels. Support follows the version rather than the tenant, so declining stays an exercise of authority rather than a breach.

## References

- ADR-039: Platform Ownership Model
- ADR-064: Bundle Promotion and Cell-Scoped Policy
- ADR-065: The Control Plane Ships Into the Box
- ADR-066: The Platform Boundary
- ADR-067: Support Telemetry and the Basis of Maintenance
