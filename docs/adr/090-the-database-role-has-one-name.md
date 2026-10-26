# ADR-090: The database role has one name

**Date:** 2026-09-28
**Status:** Accepted
**Amends:** ADR-014 (whose `tenant-{id}-user` predates ADR-088 and now reads as an endorsement of the per-app form)
**Relates to:** ADR-088 (the tenant and the application are different axes), ADR-051 (a single spoke serves multiple tenants), ADR-089 (the tenant baseline has an owner again)

## Context

A tenant application authenticates to the spoke's shared PostgreSQL cluster as a
role. That role's name was derived in **seven** independent places:

| Where | From | How |
|---|---|---|
| `database` → `spec.owner` | `spec.appId` | `tenant-%s-user` |
| `db-user` → `metadata.name` | `spec.appId` | `tenant-%s-user` |
| `db-user-admin-option` → `memberOf` | `spec.appId` | `tenant-%s-user` |
| `default-privileges-tables` → `role` | `spec.appId` | `tenant-%s-user` |
| `default-privileges-sequences` → `role` | `spec.appId` | `tenant-%s-user` |
| `pooler-app-secret` → `data.url` | `{{ .username }}` | from Infisical |
| hub operator | `tenantId` | `tenant-%s-user` |

Six agreed. The seventh — the one the application actually authenticates with —
was written by the hub operator from **tenantId**, and nothing compared them.

It passed for a year because the platform had one identifier for the customer and
the product, so the first tenant's tenantId *was* its appId. ADR-088 split the
axes. The second application of a real tenant was the first case where the two
readings differed, and it failed as:

```
psql: FATAL: password authentication failed for user "tenant-nutgraf-user"
```

logged by the tenant baseline migration Job. Every reference in the chain was a
valid Kubernetes object, nothing crashed, and the visible effect was an
application whose database had no `users` table.

Two apps of one tenant would additionally have **collided**: one role for both,
one password overwriting the other.

## Decision

### The name has exactly one definition

`spec.dbRoleName` on the XR, composed once in the chart beside `scopeId`, for the
reason `scopeId` itself exists — *"combined once, here, rather than at every
Crossplane patch site that builds a name from it."* Every consumer reads the
field. Nothing reconstructs the name.

### The encoding is `tenant_<tenantId>_<appId>_user`

**A PostgreSQL role is a cluster-wide identity.** `pg_authid` and
`pg_auth_members` are shared catalogs (`relisshared = t`), so two databases in one
cluster cannot hold distinct roles of the same name — confirmed by creating
another database's role name and being refused. ADR-051 states that a single spoke
serves multiple tenants, and ADR-014 puts one `shared-cnpg` on each spoke holding
every tenant's database. So the name must be **injective over (tenantId, appId)**.

**`tenant-{appId}-user` is not.** Two tenants on one spoke with an app of the same
name resolve to one role, which the composition makes `spec.owner` of the database
and grants DefaultPrivileges on. That is a cross-tenant data path, not a naming
clash.

**`tenant-{scopeId}-user` is not either**, and this is the subtler failure.
`scopeId` is `<tenantId>-<appId>`, and `-` is legal *inside* both components, so:

```
("foo",     "bar-baz")  →  foo-bar-baz
("foo-bar", "baz")      →  foo-bar-baz
```

No dash-based separator fixes this: `--` is also legal inside a component
(`foo--bar` matches the grammar).

**`_` is the delimiter because the grammar forbids it.** Both identifiers are
constrained to `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, enforced by the API server. A
component therefore cannot contain `_`, so the name has exactly one parse. The
delimiter is structural, not cosmetic.

### Length is a correctness property, not a limit

PostgreSQL's identifier limit is **63 bytes**, and it **truncates** rather than
failing:

```
NOTICE:  identifier "tenant-aaa…-tenant-alpha-user" will be truncated to "tenant-aaa…-tenant-alp"
CREATE ROLE
```

Two applications sharing a 51-byte prefix would truncate onto one role —
reintroducing exactly what the delimiter prevents, by a route no manifest shows.

The fixed parts cost 13 bytes (`tenant_` + `_` + `_user`), leaving **50** for the
two components together. The grammar admits only single-byte ASCII, so characters
are bytes here and the bound is exact. Both XRDs carry it as a CEL rule, verified
against a live cluster before being committed: 30+20=50 accepted, 30+21=51
rejected, and at exactly 50 the rendered role is exactly 63 bytes.

> **Amendment 2026-09-28 (ADR-093).** The budget is now **49**, not 50. ADR-093
> adds a second generated role, `tenant_<tenantId>_<appId>_owner`, and `_owner` is
> one byte longer than `_user`, so the fixed parts cost 14. The rule this ADR
> establishes is unchanged and is the reason the number moved cleanly: the bound
> follows the LONGEST generated role rather than being reasoned about per
> consumer. Both XRD CEL rules carry 49.

**The byte-safety argument depends on the grammar**, which is why there is now
exactly one XRD defining `tenantdatabases.nutgraf.in`. Two were deployed by
different ArgoCD owners and disagreed on `maxLength`; a contract two schemas claim
is not a contract.

### `dbRoleName` is derived, never declared

A second CEL rule rejects any value that disagrees with its components:

```
self.dbRoleName == 'tenant_' + self.tenantId + '_' + self.appId + '_user'
```

so the field cannot become an independent source of truth even if a future
composition forgets to patch it.

### The PostgreSQL identity lives in `crossplane.io/external-name`

A Kubernetes object name is an RFC 1123 name, which forbids `_`. provider-sql
reads the role name from the external-name annotation and nowhere else:
`meta.GetExternalName(mg)` is the only source at the `pg_roles` lookup in Observe,
at `CREATE ROLE`, at `ALTER ROLE` and at `DROP ROLE`, always wrapped in
`pq.QuoteIdentifier`; `Role.spec.forProvider` carries no name field. Verified in
both the pinned v0.14.0 and the vendored v0.16.1, neither of which calls
`SetExternalName`.

Three namespaces, one job each:

```
metadata.name                  Kubernetes identity
crossplane.io/external-name    PostgreSQL identity
spec.dbRoleName                the canonical platform value both derive from
```

### A username is not a secret

Infisical holds the **password and nothing else**. `database`, `host`, `port` and
`sslmode` sit in the same Secret template and are all patched or literal;
`username` was the one exception, and it was in the secret store only because it
was generated beside a password. That exception is what allowed a second source of
truth for an identity the composition already owns.

The hub operator therefore generates a password and holds no naming logic.

## Consequences

**The Secret contract is unchanged.** Consumers still read `username`, `password`,
`database`, `host`, `port`, `sslmode`; only the *source* of `username` moved.

**Changing `external-name` is not the cutover.** It only changes which PostgreSQL
role the managed `Role` object represents. Five distinct transitions sit behind
this ADR, and only the first is declarative:

```
Kubernetes identity          metadata.name -- deliberately NOT changed here
  != Crossplane external identity   the annotation -- what this composition moves
  != database ownership migration    ALTER DATABASE ... OWNER
  != object ownership migration      REASSIGN OWNED
  != credential cutover              the Secret, and restarting what holds it
  != legacy-role retirement          NOLOGIN, then DROP
```

`ALTER DATABASE ... OWNER` is **necessary but not sufficient** for a database that
already holds objects: schemas, tables, sequences, views, types and functions keep
their existing owners, and the composition's `DefaultPrivileges` only affect objects
created afterwards. The object-ownership migration is a separate, manual step, and
it is the one easiest to omit -- an omission that surfaces as a working credential
with no access to the data it was issued for.

See `docs/runbooks/tenant-database-role-cutover.md`, which is a prerequisite to
rolling this out rather than a companion to it.

**Applying this composition is a role-identity change for every provisioned
database, not one application's fix.** On sync the external name moves, so
provider-sql creates the new role — and the old role still owns the database and
holds its privileges. Every fleet whose credential names a legacy role needs a
controlled cutover. This is deliberately **not** automated: `db-user` carries
`deletionPolicy: Delete` and provider-sql's delete is a bare
`DROP ROLE IF EXISTS` with no `REASSIGN OWNED`, which fails while the role owns a
database and leaves the managed resource stuck in deletion. For the same reason
`metadata.name` is left on its legacy shape here; renaming it belongs to the
cutover, after ownership has moved.

**Consumers must be enumerated from the database, not from Kubernetes.** A sweep of
`deploy,sts,ds,job,cronjob` across every namespace reported that the only consumer
of tenant database credentials was the one-shot baseline migration Job. That was
wrong: waypoint's SDK runs as an Argo Rollouts `Rollout`, which the sweep never
queried, and it held roughly forty pooled connections while the claim was being
made. `pg_stat_activity` joined to pod addresses is the authoritative list; an
empty Kubernetes search proves only that the search was incomplete.

**`scopeId` remains non-injective, and it also names namespaces.** The pair above
lands two distinct (tenant, app) pairs in one `tenant-foo-bar-baz` namespace.
That is a higher-severity boundary problem than the role name was, and it is
recorded here rather than fixed: widening this change to cover namespaces would
enlarge a migration that already touches every provisioned database.

## Alternatives rejected

**`.` as the delimiter.** It satisfies both grammars — forbidden in components,
legal in RFC 1123 names — so it would need no annotation. Rejected because
`tenant.nutgraf.oranger.user` reads as a schema-qualified SQL name in exactly the
logs where the identity is debugged.

**Truncate, or truncate-and-hash, to fit 63 bytes.** Rejected: it trades a
readable identity for an opaque one to solve a problem that admission-time
validation solves exactly.

**An opaque stable identifier for the role.** Unnecessary — the existing grammar
already guarantees a safe delimiter. It remains the answer if that grammar is ever
widened to admit `_`.

**Keeping `username` in Infisical and correcting the operator's axis.** This was
implemented first and is what the ADR replaces. It makes an eighth derivation
agree with the other seven and adds a reconciler to repair the duplicate when it
drifts. Removing the duplicate is the fix; policing it is not.
