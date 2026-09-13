# ADR-079: A Day-0 Phase Declares Whether It Can Be Re-Run

**Date:** 2026-09-13

**Status:** Proposed

*Constrained by: ADR-040 (Day-0 vs Day-1 Lifecycle Boundary), ADR-072 (Tenant-Controlled Day-0)*

> Every phase is retried. Not every phase can be.

## Context

Day-0 is a state machine. `runPhase` records each completed phase, skips what is
already done, and resumes from the first that is not — which is what makes a
bootstrap survive a laptop closing, a token expiring, or a cluster that took
longer than a timeout allowed. It is one of the better things about this
platform: a failed run costs the failed phase, not the cluster.

That guarantee holds for anything whose effect is a Kubernetes object. Applying
a manifest twice is applying it once. It does not hold for a phase that writes
somewhere else, and nothing said so.

Three failures in three days, all the same shape, each found by a resume:

**Infisical secret upload.** `CreateOrUpdateSecret` checked existence and then
created. The check missed, the create was refused with `Secret already exists`,
and a resumed bootstrap died on `infisical-db-username` after three and a half
minutes of work that had all succeeded. The secret was there because the run
being resumed had put it there.

**The ArgoCD repository credential.** Day-0 wrote the correct organisation-scoped
credential; an ExternalSecret owned the same Secret and overwrote it within the
hour. The imperative write was not wrong, it was simply not the thing that
decides.

**Zitadel's setup.** `cmd/setup` writes event data and, separately,
`projections.migrations` — its record of what it has done. A failure between
them leaves a database in which work happened and nothing recorded it. Every
retry re-runs `03_default_instance`, collides with the constraint its
predecessor wrote, fails with `AlreadyExists`, and by its own log "setup failed,
skipping cleanup". No retry can ever succeed. The chart's values carried a
comment asserting the opposite — *"restartable by design — cmd/setup runs
cleanup on interruption"* — which is true of interruption and false of failure.

Each was fixed where it was found. That is three fixes and no rule, and the
fourth instance is already somewhere in the tree.

The common property is not "external system". It is that **the effect of the
phase is not transactional**, so a partial effect is a state the phase itself
cannot reason about. Retrying is then not recovery; it is repetition against
evidence of the last attempt.

## Decision

**A Day-0 phase declares whether re-running it is safe, and the state machine
refuses to resume one that has not said so.**

Three kinds, and every phase is exactly one:

**`reentrant`** — running it again is the same as running it once. Applying
manifests, waiting for a condition, reading state. The overwhelming majority,
and the current behaviour is already correct for them.

**`resettable`** — running it again is safe only against a clean slate, and the
slate can be cleaned. The phase names how. Zitadel is this: its initialisation
is atomic at the database, so an unfinished one is recovered by recreating the
database, not by repairing it.

**`once`** — running it again is not safe and cannot be made safe. The phase
must not be retried automatically; a human decides. Nothing is in this category
today, and a phase that lands here is a design problem, not a classification.

### Initialisation is atomic at the thing it initialises

For a `resettable` phase the reset is not a workaround, it is the rollback that
the absent transaction would have provided. This platform already states that
contract elsewhere: CNPG's `bootstrap: initdb` versus `recovery`, and the
`database-recovery` overlay that rebuilds `platform-db` from its archive rather
than repairing it in place (ADR-014).

Recreating is safe precisely because incomplete initialisation means there is
nothing to preserve. The guard is therefore not "is the data important" but "did
initialisation finish", and the state machine already knows.

### A reset is gated on the phase, never on the state alone

The condition to reset is: **the phase did not complete, AND the state carries
the signature of a partial effect.** Both halves.

The phase gate is what stops a reset becoming data loss. A completed phase means
the component initialised successfully at some point, and a later inconsistency
is a running system's problem — to be diagnosed, never silently dropped.

The signature must be specific enough to distinguish *half-done* from *not
started* and from *done*. For Zitadel that is `eventstore.events2` present and
`projections.migrations` absent: work written, nothing recorded. A vaguer test
would eventually drop a healthy database.

### The phase that owns a component asserts the component works

`awaitBoundaryInventory` answers "did the boundary generate its Applications",
which is not "does the identity provider serve". Zitadel's setup failed while
boundary 04 reported activated, and the first visible symptom was an
ExternalSecret in another namespace stalling for eleven minutes — because
`iam-admin-pat` was never created, so the identity token was never uploaded, so
ESO had nothing to deliver.

A phase that deploys a component asserts that component reached its own notion
of ready, within a bounded time, and reports the component's own error when it
does not. The cost of not doing this is not a missed failure; it is a failure
found in the wrong place, by someone reading the wrong logs.

## Consequences

### Positive

1. A resumed bootstrap is either safe or refused, and never silently poisons
   the state it is resuming.
2. Failures are named by the phase that owns them, so the first log a reader
   opens is the right one.
3. The classification is a checkable property. A new phase with no declaration
   is a gate failure, not something discovered by the next resume.

### Negative

1. Every phase must be classified, including the forty that are obviously
   reentrant. That is real work and most of it is uninteresting.
2. A `resettable` phase needs its signature written and kept true. A signature
   that drifts from the component's schema is a reset that stops firing, or one
   that fires when it should not.
3. Resetting destroys state. The guards make it safe; they do not make it
   reversible.

## Impact

- `runPhase` takes a re-entrancy declaration and applies the reset before a
  retry of a `resettable` phase.
- Boundary 04 is the first `resettable` phase:
  `resetZitadelIfInitIncomplete` recreates the Zitadel database when
  initialisation did not finish, and `awaitZitadelReady` asserts the identity
  provider serves before the phase reports success.
- **Confirms ADR-040.** Day-0 selects and seeds; this says what it means for a
  step of that to be repeated.
- **Extends ADR-014.** Its atomic-initialisation contract for `platform-db`
  becomes the general rule for any database a platform component initialises.

## References

- ADR-014: Platform-Owned Stateful Infrastructure — initialisation and restore
- ADR-035: Enterprise PKI — the fleet issuer's coordinates, another Day-0 write
  that reached the cluster only through the declared state
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-072: Tenant-Controlled Day-0
