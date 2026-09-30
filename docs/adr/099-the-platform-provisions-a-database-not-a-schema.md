# ADR-099: The platform provisions a database, not a schema

**Date:** 2026-10-01
**Status:** Accepted
**Supersedes:** ADR-093 (the app must not own its tables), ADR-089 (the tenant
baseline has an owner again)
**Amends:** ADR-057 (tenant user management)

## Context

The platform shipped a "tenant baseline": `users`, `identities`, `sessions`,
`buckets` and `objects`, created in every application's database by a Job in the
universal-tenant chart, with row-level security policies over them.

Asked what those tables solve, the answer turned out to be: mostly nothing.

```
users        2 references across the platform and both applications
identities   0 direct references -- only the platform's own function touched it
sessions     0
buckets      0
objects      0
```

Three of the five were never read or written by anything. They are leftovers from
an earlier product shape, and `buckets`/`objects` were never about identity at
all.

The two that are used are used through one library call, `resolveUser()`, which
maps an OIDC subject to a local user id.

Meanwhile the baseline had cost a great deal. ADR-093 observed — correctly — that
the policies enforced nothing, because PostgreSQL exempts a table's owner from its
own policies and the application owned the tables. It concluded that the platform
should take ownership. That conclusion was wrong, and the reason is visible in the
policy itself:

```sql
USING (id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid)
```

**The application sets that setting.** The policy catches a handler that forgot a
`WHERE`; it does not constrain code that writes a different `user_id`, because the
application holds the connection. It was a backstop against developer error inside
one application — not a boundary between platform and tenant, and not isolation
between tenants, who have separate databases.

Taking ownership to make that backstop bite produced, in order: a `SECURITY
DEFINER` function so the application could read its own identities table; a
120-line migration granting back what the split had taken; a production outage
when graphile-worker — which enables RLS, defines no policies and no grants, so
ownership *is* its access control — lost the owner exemption it depends on; and a
further 123-line migration, with a new fleet declaration, to hand ownership back
wherever the split did not fit. That last one is the tell: a mechanism whose only
job was to undo the decision in the places it was wrong.

## Decision

**The platform provisions a database and a role. Everything inside it belongs to
the application.**

No tables, no functions, no policies, no migration Job. A tenant declares it needs
a database and receives an empty one it owns.

### What the platform still offers, and why it is a library

`resolveUser()` stays in `zero-ops-auth`, as ordinary client-side SQL against
tables the application declares. Shipping code is not maintaining a schema, and
the distinction is the whole of this ADR: the platform may know something useful
about identity without owning rows in a tenant's database.

What it knows is one rule that does not announce itself when broken:

> **Join on the subject, never on the email address.** An address is mutable at the
> provider and reassignable between people, so a lookup keyed on it merges two
> accounts the moment an address changes hands — and the result is one person
> reading another's records, with nothing reporting an error.

The library also owns the first-sighting race, which needs `UNIQUE (provider,
provider_user_id)` and an `ON CONFLICT` path that deletes the loser's orphaned
user row rather than returning an id no identity points at.

The two tables it expects ship as `zero-ops-auth/schema/identity.sql` — **an
example to copy into the application's own migrations**, not something the
platform applies. From then on the shape is the application's to change.

### The `SECURITY DEFINER` function is deleted, not relocated

It existed only because ADR-093 had taken the tables away. A function the platform
creates inside a tenant's database is the platform owning an object in that
database, which is the thing this ADR removes.

### Row-level security is removed, not left enabled

An inert policy that reads as enforcement in `\d` is worse than no policy, because
it stops people asking — which is exactly what ADR-093 found. Removing the
ownership split without removing the policies would recreate that state. An
application that wants the backstop writes its own, against its own schema,
knowing it is a guard against its own bugs and not a boundary.

## Consequences

### Positive

- 993 lines of migration SQL, a ~300-line Job, and the `db-owner` role and its
  grants stop existing. There is no declaration surface for exceptions because
  there is no rule to except.
- A library that breaks an application's queue cannot happen again: the platform
  does not own objects in the tenant's database, so nothing it does can change
  ownership of one.
- `resolveUser` needs no privilege the caller lacks, so the function, the grant,
  and the `REVOKE ... FROM PUBLIC` all go.

### Negative

- No IDOR backstop by default. A missing `WHERE user_id = $1` is a real bug rather
  than an empty result. That is the application's loss to accept, it costs one
  clause per query, and it is written where a reviewer can see it.
- Each application now carries two tables' DDL it did not write. Adoption is a
  no-op against existing databases, but it is one more thing in their migrations.
- An application that ignores `resolveUser` and joins on email will do so
  undetected. The library is available and documented; it is not enforced, and
  under this ADR it cannot be.

### Transition

Nothing moves. `users` and `identities` already exist with rows in both
applications' databases; the platform simply stops managing them and each
application adopts the existing DDL. `sessions`, `buckets` and `objects` can be
dropped by whoever wants to, whenever, by the application that owns them.

One operation already performed must NOT be repeated by an adopting application:
the `ory` → `zitadel` rewrite of `identities.provider`, which has run.

**Deleting the migrations from the chart changes nothing in a live database.** On
every database provisioned before this ADR, the baseline tables still carry
row-level security and are still owned by the separate owner role, and the
`SECURITY DEFINER` function still exists. `zero-ops-auth` 0.17.0 keeps working
because it calls that function; **0.18.0 does not**, because it issues its own
SQL as a role the policies apply to, and they match nothing.

So the live cutover is a one-time operator action, not a consequence of the
release: `docs/runbooks/retire-the-tenant-database-owner-role.md`. It hands
ownership to the application's role, removes the policies, and is a prerequisite
to 0.18.0 as well as to retiring the owner role.

## Impact

- `manifests/tenants/charts/universal-tenant` — `files/migrations/` and
  `templates/tenant-baseline-migration.yaml` deleted.
- `manifests/spoke/spoke-catalog/infra/tenant-db/composition.yaml` — the
  `db-owner` role and its two ADMIN OPTION grants exist only for that Job.
  Retired SEPARATELY and later: `DROP ROLE` fails on a role that owns objects, so
  it cannot go in the same release as the deletion.
- `packages/auth` 0.18.0 — `resolveUser` issues its own SQL again; the email
  guards the database function held move into it; `schema/identity.sql` ships as
  an example.
- `scripts/validate/verify-oranger-database.sh` — deleted; it asserted the
  baseline's shape.
- ADR-093 and ADR-089 — superseded. ADR-057 keeps the mapping and the email rule
  and loses the baseline.
