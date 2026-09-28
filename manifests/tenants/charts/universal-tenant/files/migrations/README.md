# The tenant baseline

`users`, `identities`, `sessions`, `buckets`, `objects` — created in the
database of every app that has one, by `templates/tenant-baseline-migration.yaml`.

These are not one application's tables. `zero-ops-auth`'s `resolveUser()` maps an
OIDC subject to a tenant-local user id and stores the mapping in `identities`;
every app that authenticates a person depends on them existing, and on their
joining by subject rather than by email — the mutable field, reassignable
between people, whose reuse silently merges two accounts.

## How they are applied

An ordinary `Job`, not a PreSync hook, running `psql` from the image the spoke's
own Postgres runs.

The reason is NOT that hooks would never fire. ArgoCD supports PreSync hooks and
runs them during a sync; that is a documented and normal way to run schema
migrations, and an earlier version of this file claimed otherwise and was wrong.

The reason is *when* each runs. This Job's name carries a hash of the SQL, so it
runs when and only when these files change. A PreSync hook runs on EVERY sync —
self-heal included — so a migration that takes eight seconds would run on every
reconcile of an unchanged database, and a hook that fails blocks the entire
sync rather than degrading one Job. The hash is what makes re-application cheap
enough to be unconditional. Its name carries a hash of the SQL, so it re-runs when
and only when these files change. It connects with the app's OWNER credential
(ADR-093) -- not the application's -- holds no Kubernetes API token, and reaches
nothing but `shared-cnpg` on 5432.

## Adding a schema

`20240101000006_grant_runtime_access.sql` grants over the schemas that exist when
it runs. A schema created by a later migration inherits none of it.

**A migration that creates an application schema must establish that schema's
runtime `USAGE` and default privileges in the same file.** Creating a schema and
granting access to it are one change.

This is deliberately not automated. A grant that appears because a schema appeared
is a privilege nobody decided to give — every future schema would silently become
readable by the runtime role, including one created for a purpose that should not
be.

## Writing one

Idempotent, always, and applied one file per transaction (`psql
--single-transaction`). There is no migration ledger, and the whole set
re-applies on every sync — so a file is the unit that must be safe to repeat AND
safe to interrupt. Several below `DROP POLICY` immediately before `CREATE POLICY`,
which is what keeps them repeatable; without a transaction, a failure in that
window would leave the table with RLS enabled and no policy, and PostgreSQL
default-denies from there. The application would then read nothing from a database
that looks migrated.

Per file rather than across the set: a file is written to be idempotent, so it is
the right unit to be atomic. Wrapping all of them together would make a failure in
the last file discard the ones that had already succeeded.

- `CREATE TABLE IF NOT EXISTS`, `CREATE INDEX IF NOT EXISTS`
- `CREATE OR REPLACE FUNCTION`
- `DROP POLICY IF EXISTS` / `DROP TRIGGER IF EXISTS` immediately before each
  `CREATE POLICY` / `CREATE TRIGGER`

Nothing that needs superuser. The Job runs as `tenant_<tenantId>_<appId>_owner`,
which owns the database — so `gen_random_uuid()` (built in since PG 13) is fine and
`CREATE EXTENSION` is not.

It deliberately does NOT run as the application's role. It used to, and that is
what made every policy below inert: the Job creates the tables, so the application
owned them, and PostgreSQL exempts a table's owner from its own policies. A
migration that adds a policy while running as the role the policy is meant to
constrain protects nothing (ADR-093).

Name files `NNNNNNNNNNNNNN_description.sql`. They are applied in sorted order.

## What this replaced

Atlas, and its `atlas.sum` checksum file. ADR-074 withdrew the Atlas Operator
and left schema migration with no owner; these files survived the removal and
the thing that applied them did not, so they applied to nothing.

The symptom reached a fleet as `relation "public.identities" does not exist`,
logged once per authenticated request and swallowed by the SDK's middleware by
design — nothing failed, nothing alerted, and every login ran without a user
context. See ADR-089.

Do not regenerate `atlas.sum`; it is gone, and no checksum is verified. The
guarantee is idempotency, not integrity, and the trade is recorded in ADR-089.
