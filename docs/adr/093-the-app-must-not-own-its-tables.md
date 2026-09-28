# ADR-093: The application must not own the tables it queries

**Date:** 2026-09-28
**Status:** Accepted
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

ADR-014's diagram said `crossplane_admin (owner - migrations)`; this ADR amends it to name the per-app owner, since the platform's admin role is not the owner this decision settles on.

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

**The migration credential is an owner credential, and must never be mounted by an
application workload.** This is stated because the two-role model makes it
load-bearing: `tenant_<t>_<a>_owner` is LOGIN-capable and can perform any DDL and
ownership operation in that tenant's database. Only the baseline migration Job
carries it. A workload that mounts it is back to the state this ADR exists to
remove, and worse -- it would own objects without anyone noticing, because
everything would appear to work.

A three-role model is the stronger production boundary and is deliberately NOT
adopted here:

```
owner       owns objects, NOLOGIN
migrator    LOGIN, may SET ROLE to owner
runtime     LOGIN, DML only, RLS applies
```

It removes the login-capable owner entirely. It is deferred because the separation
that actually fixes the defect is owner-from-runtime, and adding a third role now
enlarges a migration that already has an outage in its ordering. When the owner
credential's blast radius becomes the binding constraint, this is the shape to move
to; ADR-093 should be amended rather than replaced.

**`FORCE ROW LEVEL SECURITY` is deliberately not used.** It makes the migration
identity subject to the application's row-security policies, coupling schema and
data migration behaviour to runtime row-security semantics. What a migration then
sees depends on whether some policy happens to permit its rows -- PostgreSQL
default-denies when none does -- so the behaviour of every future migration becomes
a function of policies written for request handling. Separating ownership from
runtime access removes the coupling entirely, rather than making migrations
correct by careful policy authorship.

The exemption also ends up in the right place: identity, rather than a
`SECURITY DEFINER` function that each future caller must remember to route
through.

**A per-app owner rather than `crossplane_admin`.** Reusing the platform's admin
role is cheaper: zero new objects, and the DefaultPrivileges already name it. It is
rejected because it makes one identity the owner of every tenant's data on the
spoke, which is the same shape ADR-090 removed from the runtime role three days
earlier -- a cluster-wide role shared across tenants. Ownership is the last thing
that should be shared, and the argument that `crossplane_admin` "already has
reach" is the argument that was wrong about `tenant-<appId>-user`.

### DefaultPrivileges cover future objects only

`ALTER DEFAULT PRIVILEGES` applies to objects created **after** it is set. It
grants nothing on tables, sequences, schemas or functions that already exist. This
migration moves an already-created baseline, not an empty database, so the grants
the runtime role needs on today's objects must be established explicitly:

```
existing tables      -> runtime: SELECT, INSERT, UPDATE, DELETE
existing sequences   -> runtime: SELECT, UPDATE, USAGE
existing schemas     -> runtime: USAGE
existing functions   -> runtime: EXECUTE, where the application calls them
future objects       -> DefaultPrivileges, as already configured
```

`REASSIGN OWNED` transfers ownership and does **not** carry privileges or default
privileges with it, so the two halves are genuinely separate work: ownership moves
in one statement, and the runtime role's access to what moved has to be granted.
Verifying only that ownership changed would leave an application that owns nothing
and can read nothing, which fails in exactly the same silent way as the defect
being fixed.

Both halves are acceptance criteria, not implementation detail.

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
with the owner's privileges rather than the caller's.

A `SECURITY DEFINER` function is a privilege escalation by design, so it is
constrained rather than merely declared:

```
SECURITY DEFINER
SET search_path = pg_catalog, pg_temp     -- fixed and trusted
every object reference schema-qualified   -- not resolved through search_path
REVOKE EXECUTE ON FUNCTION ... FROM PUBLIC
GRANT  EXECUTE ON FUNCTION ... TO <runtime role>
no dynamic SQL
returns the tenant-local user id and nothing more
```

The `search_path` and `PUBLIC` rules are the two that matter: without them a
caller can shadow an unqualified object reference, and every role on the cluster
can execute a function running as the owner.

**`SECURITY DEFINER` does not make the first-request race atomic.** It changes the
privilege a statement runs with, nothing else. The race ADR-057 describes -- two
requests from the same person observing no user and both creating one -- is
defeated by the uniqueness constraint on `(provider, provider_user_id)` and an
`INSERT ... ON CONFLICT` that returns the winner's row, so the loser adopts it
rather than returning a user id no identity refers to. What the function buys is
that this pattern exists in ONE place instead of being reimplemented by every
caller; correctness under the race is the constraint's, not the function's. The
earlier draft of this ADR claimed otherwise and was wrong.

### Both names are composed once, and the budget follows the longest

ADR-090's rule was that the role name has ONE definition. There are now two names,
so the rule extends rather than repeats: `dbRoleName` and `dbOwnerRoleName` are
both composed at the chart/XR boundary and propagated. No composition rebuilds
`tenant_<...>_user` or `tenant_<...>_owner` from its components, and the XRD's
equality rule covers both fields, so neither can drift from what it is derived
from.

The budget is derived from the **longest** generated role, not reasoned about per
consumer:

```
len("tenant_") + len(tenantId) + 1 + len(appId) + len("_owner")  <=  63
        7                        1                        6
```

which is `len(tenantId) + len(appId) <= 49`, one tighter than ADR-090's 50 because
`_owner` is one byte longer than `_user`. The grammar admits only single-byte
ASCII, so characters are bytes and the bound is exact.

Current usage is 15 (`nutgraf` + `waypoint`), so no existing object is affected --
but the rule must tighten BEFORE the owner role exists, not after. An over-budget
app would otherwise produce a truncated owner name, which is the silent collision
ADR-090 exists to prevent, arriving through the one name ADR-090 did not cover.

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

## Acceptance criteria

Implementation is not complete until all four hold, each verified against the
database rather than inferred from the manifests:

1. **The runtime role owns zero protected objects.** The ADR-090 cutover's own
   query shape -- schemas, relations and functions, counted separately -- must
   return zero for `tenant_<t>_<a>_user` in every tenant database.
2. **The runtime role holds only the privileges it needs**: DML on the baseline
   tables, USAGE on their schemas and sequences, EXECUTE on the resolution
   function. No ownership, no DDL, no CREATE.
3. **Existing objects AND future objects are both covered** -- explicit grants for
   what exists today, DefaultPrivileges for what migrations create next. Passing
   one and not the other produces an application that owns nothing and can read
   nothing.
4. **The resolution function is hardened and genuinely atomic**: fixed
   `search_path`, schema-qualified references, `EXECUTE` revoked from `PUBLIC` and
   granted only to the runtime role, and a real `INSERT ... ON CONFLICT` against
   the uniqueness constraint rather than a read-then-write.

The behavioural test is the probe that exposed the defect, inverted. Connected as
the runtime role with no claims set, `SELECT count(*) FROM public.users` returns
**0**; inside `withUserContext` it returns that user's rows and no others. Today
it returns every row.

## Executed for oranger, 2026-09-28 (and the order was wrong)

The ADR said: apply the composition, let the migration Job establish the grants,
then `REASSIGN OWNED`. **That order does not work, and the live run proved it in
the first thirty seconds.**

Once the Job runs as the owner, it cannot touch tables it does not yet own:

```
applying 20240101000001_create_users_table.sql
NOTICE:  relation "users" already exists, skipping
ERROR:   must be owner of table users
```

`CREATE TABLE IF NOT EXISTS` is a no-op on an existing table, but the
`ALTER TABLE ... ENABLE ROW LEVEL SECURITY` that follows is not, and the owner role
is not yet the owner. The Job cannot apply its own grants until ownership has
already moved.

**So `REASSIGN OWNED` comes FIRST, then the Job.** The corrected order:

```
1. composition syncs      owner role created, database owner moves,
                          Job FAILS -- expected, it owns nothing yet
2. REASSIGN OWNED BY <runtime> TO <owner>
3. delete the failed Job so ArgoCD recreates it
4. Job succeeds, applying the grants in 20240101000006
```

Between 2 and 4 the application can reach nothing: it no longer owns the tables
and has not yet been granted access. For oranger that window was harmless -- it has
no database consumer at all. **For any application with live connections this is an
outage**, and it is the reason waypoint's cutover must be planned around its own
traffic rather than run the same way.

### Result

```
runtime owns:        0 schemas, 0 relations, 0 functions
runtime holds:       SELECT, INSERT, UPDATE, DELETE on public.users
                     (no TRUNCATE, no REFERENCES, no ownership)
CREATE TABLE:        refused
no claims set:       users 0 rows, identities 0 rows
claims = my user:    1 row
claims = another:    0 rows
INSERT, no claims:   ERROR: new row violates row-level security policy
```

An hour earlier the same probe on the same database returned every row and the
INSERT succeeded.

### What is NOT done

Criterion 4 -- the hardened `SECURITY DEFINER` resolution function -- is
application-side and has not been written. It did not block this cutover because
nothing calls `resolveUser` yet. It now BLOCKS the first code that does: with RLS
enforced, reading `identities` before a user context exists returns nothing, so
resolution would create a duplicate user and collide on the uniqueness constraint.
That function is the first thing oranger's user-management work needs, not a
follow-up to it.

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
