# ADR-093: The application must not own the tables it queries

**Date:** 2026-09-28
**Status:** Proposed
**Amends:** ADR-057 (which specifies the RLS context and is silent on the precondition for it to have any effect)
**Relates to:** ADR-090 (one name for the database role), ADR-089 (the tenant baseline has an owner again), ADR-014

## Context

The tenant baseline ships `users`, `identities`, `sessions`, `buckets` and
`objects` with row-level security policies keyed on the acting user. ADR-057
specifies how the acting user reaches those policies -- a transaction-scoped
`request.jwt.claims`, set with a bound parameter, scoped locally because the
connection is pooled -- and `withUserContext` implements it correctly.

**None of it has any effect, because PostgreSQL exempts a table's owner from its
policies.** Demonstrated on the live spoke, connected as the application's own
role with no claims set:

```
current_user: tenant_nutgraf_oranger_user
rows visible with NO jwt claims set: 2      -- policy would match zero
row_security: on
```

Every baseline table reports `rls=true forced=false`, owned by the same role the
application connects as. The isolation the schema advertises does not exist, and
nothing reports that: no error, no warning, and a policy visible in `\d` that
reads as enforcement.

`withUserContext` additionally has **no callers**, so even the mechanism ADR-057
designed is unused. Fixing that alone would change nothing while ownership stands.

### The separation already exists in the design

This is not a missing concept. It is a wiring defect in an existing one.

`DefaultPrivileges` in the spoke composition is already shaped for two roles:

```
targetRole: crossplane_admin        # whose newly created objects
role:       <dbRoleName>            # grant DML to the application
```

and the ACLs are live in oranger's database today:

```
crossplane_admin -> tenant_nutgraf_oranger_user = arwdDxt   (tables)
crossplane_admin -> tenant_nutgraf_oranger_user = rwU       (sequences)
```

ADR-014's own diagram says `crossplane_admin (owner - migrations)`.

Those grants never fire for the baseline tables, because
`tenant-baseline-migration.yaml` connects with `tenant-<app>-db-credentials` --
the **application's** credential. The Job creates the tables, so the application
owns them, so the grants are redundant and the policies are inert. One wrong
credential on one Job defeats the entire design.

## Decision

**The role that runs migrations and owns objects is not the role the application
connects as.**

A per-app owner is introduced, `tenant_<tenantId>_<appId>_owner`, following
ADR-090's encoding exactly. The existing `tenant_<tenantId>_<appId>_user` keeps
its name and becomes the runtime role -- it is not renamed, so no credential the
application holds changes identity twice in one week.

```
tenant_<t>_<a>_owner    owns the database and every object; runs migrations;
                        exempt from RLS by ownership, which is correct -- a
                        migration must see everything
tenant_<t>_<a>_user     the application's connection; owns nothing; holds DML
                        through DefaultPrivileges; RLS APPLIES
```

**`FORCE ROW LEVEL SECURITY` is deliberately not used.** It was the other
candidate and it is worse: `FORCE` subjects the *owner* to the policies too, so
every future baseline migration performing DML would silently see nothing, and the
exemption a migration legitimately needs would have to be re-granted through a
`SECURITY DEFINER` function that each future caller must remember to route
through. Separating the roles puts the exemption where it belongs -- in identity,
not in a function call convention.

**A per-app owner rather than `crossplane_admin`.** Reusing the platform's admin
role is cheaper: zero new objects, and the DefaultPrivileges already name it. It is
rejected because it makes one identity the owner of every tenant's data on the
spoke, which is the same shape ADR-090 removed from the runtime role three days
earlier -- a cluster-wide role shared across tenants. Ownership is the last thing
that should be shared, and the argument that `crossplane_admin` "already has
reach" is the argument that was wrong about `tenant-<appId>-user`.

### `resolveUser` needs a path that predates the user context

`resolveUser` maps a provider subject to a tenant-local user by reading
`identities` -- before any acting user exists. Under RLS the identity policy
(`user_id = claims->>'user_id'`) matches nothing, so the lookup finds no row,
creates a second user, and collides on the uniqueness constraint.

Resolution is therefore a `SECURITY DEFINER` function owned by the owner role,
with `EXECUTE` granted to the runtime role, which the library calls instead of
issuing the SELECT and INSERT itself.

This is consistent with ADR-057's decision that resolution is a **library on the
application's own connection, not a service**: there is still no network hop, and
no second component on the request path. What changes is that the statement runs
with the owner's privileges rather than the caller's. It also makes the
first-request race that ADR-057 describes atomic in one place rather than in every
caller.

### The byte budget tightens by one

ADR-090 fixed the budget at 50 characters for `tenantId` + `appId`, from
`tenant_`(7) + `_`(1) + `_user`(5) = 13 fixed bytes. `_owner` is one byte longer,
so the owner name costs 14 and the budget becomes **49**. Both XRD CEL rules move
with it. Current usage is 15 (`nutgraf` + `waypoint`), so no existing object is
affected -- but the rule must tighten before the owner role exists, not after, or
the first over-budget app produces a truncated owner and a silent collision of the
kind ADR-090 exists to prevent.

## Migration, and why the order is not negotiable

**The moment ownership moves, RLS begins applying to the application.** With no
`withUserContext` callers, it sees zero rows. This is the same shape as the
ADR-090 Phase 3/4 gap: a change that appears to succeed and fails at the next
query.

So the sequence is by application, not by platform:

```
oranger   no database consumer exists at all (oranger-serve has egress: [] and
          no database driver), so ownership can move NOW, before any
          user-management code is written. It is the safe rehearsal, and doing it
          first means its code is written against enforced RLS from the start.

waypoint  its SDK holds ~40 pooled connections and queries workflow tables on
          every request. Ownership must NOT move until that code calls
          withUserContext on every path that touches a policy-bearing table.
          Moving first is an immediate outage.
```

The baseline tables are the only ones carrying policies today; waypoint's own
`workflow*` schemas have none, so its exposure is narrower than the connection
count suggests -- but "narrower" is not "none", and the audit belongs to that
cutover rather than to this ADR.

## Consequences

- **RLS becomes real.** The same probe that returns 2 rows today returns 0 without
  a user context, and the acting user's rows with one. That is the acceptance test.
- **A second credential per app.** The migration Job moves to the owner credential;
  the application keeps the one it has.
- **DefaultPrivileges start mattering.** `targetRole` becomes the per-app owner
  instead of `crossplane_admin`, and the grants that are currently redundant become
  the only reason the application can read anything.
- **Existing objects need `REASSIGN OWNED`**, exactly as ADR-090's cutover did, and
  with the same verification across schemas, relations and functions.
- **A migration that performs DML now runs as a role that sees everything**, which
  is correct, and which `FORCE ROW LEVEL SECURITY` would have broken.
- **This is the second role change in a week.** That is a real cost and the reason
  the runtime role keeps its name: the application's credential identity does not
  move again, only what it is permitted to own.
