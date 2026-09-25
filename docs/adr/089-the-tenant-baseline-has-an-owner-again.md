# ADR-089: The Tenant Baseline Has an Owner Again

**Status:** Accepted
**Date:** 2026-09-25
**Amends:** ADR-074 (which left this seat vacant)

## Context

ADR-074 withdrew Atlas and said what it was leaving behind:

> **Atlas is withdrawn from the platform, and schema migration has no owner until one is chosen.**

It removed the Atlas Operator, the `AtlasMigration` resources, the `baseline-migrations`
entry in the tenant composition, the `migrations` block on the `AINativeSaaS` XRD and
the ConfigMap template in the universal-tenant chart. It did not remove the SQL.

Five files — `users`, `sessions`, `identities`, `buckets`, `objects` — stayed in
`charts/universal-tenant/files/migrations/`, with an `atlas.sum` beside them and a
README explaining how to regenerate a checksum for a tool that was gone. Nothing
read any of it. The tables were never created in any tenant database provisioned
after the withdrawal.

### How it surfaced

Not as a failure. As a log line:

```
user context resolution failed: relation "public.identities" does not exist
```

once per authenticated request, on the live dev fleet, for a fortnight.

`zero-ops-auth`'s `resolveUser()` maps an OIDC subject to a tenant-local user id and
records the mapping in `identities`. The SDK's `resolveUserContext` middleware catches
the failure deliberately — internal callers have no acting user and must keep working,
so it logs and continues. Handlers that genuinely need a user are expected to call
`requireUserContext()` and fail there, where the requirement is visible.

**Nothing calls `requireUserContext()`.** So every authenticated request ran without a
user context, no request failed, no alert fired, and the fleet reported healthy. A
gap in the platform's identity storage presented as log noise.

### Why it is not one product's problem

`zero-ops-auth` states the contract in its own documentation:

> The platform's tenant baseline ships `users` and `identities` in every tenant
> database... Left to each tenant, this is the same fifty lines written repeatedly and
> wrongly — most often by joining on email, which is the bug described below.

Email is mutable in the identity provider and can be reassigned between people. A
tenant that writes its own version and joins on it merges two accounts the moment an
address is reused, and the failure is a data-disclosure one rather than an error. The
subject is the stable key, which is what `identities.provider_user_id` holds.

waypoint's SDK already runs a migration Job with two layers — the `workflow` schema and
its own `public` tables. A third layer there would work, and would make one product's
release cadence the owner of a contract every product depends on.

## Decision

**The tenant baseline is applied by the platform, as a `PreSync` `Job` rendered by the
universal-tenant chart into each app's namespace.**

`manifests/tenants/charts/universal-tenant/templates/tenant-baseline-migration.yaml`
renders three objects when an app has a database and the chart is rendering for a spoke:

| object | purpose |
|---|---|
| `ConfigMap/tenant-baseline-migrations` | the SQL, from `files/migrations/*.sql` |
| `CiliumNetworkPolicy/tenant-baseline-migration-egress` | egress to `shared-cnpg:5432`, and nothing else |
| `Job/tenant-baseline-migration` | `psql`, applying each file in sorted order |

### Idempotent instead of ledgered

Every statement is guarded — `CREATE ... IF NOT EXISTS`, `CREATE OR REPLACE`, and
`DROP POLICY`/`DROP TRIGGER IF EXISTS` before each corresponding `CREATE`. Re-running
the set is the same as running it once, which is what makes a hook on every sync safe.

There is no migration ledger and `atlas.sum` is deleted rather than preserved. This is
a trade, not an oversight: a ledger buys ordering guarantees, drift detection and a
refusal to apply modified history, and none of that is free of a tool to enforce it.
**The guarantee here is idempotency, not integrity.** ADR-020's governance is worth
re-reading when this outgrows a fixed set of guarded statements; it is not in force.

### Why a Job and not the operator

`hub-operator`'s imperative migrator is the platform's only other migration path, and
ADR-074 made it load-bearing. It connects to `platform-db-rw` with the **hub's** own
superuser credential. A tenant's database is on a spoke, in a different cluster, behind
a credential the hub does not hold — teaching the hub to reach it would give the hub a
path into every tenant database on every box, which is a far larger grant than this
needs.

The Job runs where the database already is, as the app's own role, with no Kubernetes
API token (`automountServiceAccountToken: false`) and one egress rule.

### The hook is bounded

`activeDeadlineSeconds: 240`, `backoffLimit: 2`, and this is not boilerplate.

A hook that never finishes does not merely stall a sync — its Application cannot be
deleted either, because the resources finalizer waits for the sync to settle first. On
2026-09-24 `tenant-nutgraf-dev-sdk` sat undeletable for 26 hours exactly that way: its
ApplicationSet had requested the deletion five minutes after creating it, and a hook
Job targeting a namespace that did not exist blocked the finalizer indefinitely.
Nothing among 69 Applications reported it.

Failing is recoverable. Hanging is what is not.

### One copy of the SQL

`migrations/tenant-baseline/` at the repository root is deleted. It held a byte-identical
second copy, and it is the path ADR-074 recorded as a defect in its own right: the XRD
defaulted `database.migrations.baseline.gitRepo` to the platform's own repository, giving
every tenant a runtime dependency on it that ADR-063 requires a release to refuse.

The chart's `files/migrations/` is the only copy. A contract about who a person is should
not exist twice.

## Consequences

### Positive

- An app with a database gets `users`, `identities`, `sessions`, `buckets` and `objects`,
  with row-level security, without declaring anything.
- ADR-074's open question is closed for the baseline, with the smallest mechanism that
  closes it.
- The Job holds no Kubernetes API access and one egress rule; its blast radius is one
  database.

### Negative

- **No integrity check.** An edited migration file applies without complaint. Idempotency
  is enforced by review, not by the tool.
- **No ordering guarantee beyond filename sort**, and no record of what was applied when.
  A partial failure is diagnosed by reading the Job's logs, and they survive only until
  the next sync replaces a failed hook.
- **This is the baseline only.** An application's own schema remains that application's
  problem, and the platform still has no general answer for one — ADR-074's question is
  narrowed, not answered.
- **A tenant image pin to maintain.** The Job runs the CloudNativePG Postgres image by
  digest, so a Postgres upgrade on the spoke needs this digest moved with it.

## Impact

- `manifests/tenants/charts/universal-tenant/templates/tenant-baseline-migration.yaml` — new
- `manifests/tenants/charts/universal-tenant/files/migrations/README.md` — rewritten; `atlas.sum` deleted
- `migrations/tenant-baseline/` — deleted, duplicate

## References

- ADR-020 — Atlas migration governance; withdrawn by ADR-074, not in force
- ADR-023 — the three-legged database contract; its third leg, partially refilled
- ADR-063 — a release may not depend on the platform's own git
- ADR-074 — withdrew Atlas and left this open; amended by this
- ADR-087 — configuration and secrets; the credential this Job reads
