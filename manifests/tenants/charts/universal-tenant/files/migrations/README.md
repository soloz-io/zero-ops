# The tenant baseline

`users`, `identities`, `sessions`, `buckets`, `objects` — created in the
database of every app that has one, by `templates/tenant-baseline-migration.yaml`.

These are not one application's tables. `zero-ops-auth`'s `resolveUser()` maps an
OIDC subject to a tenant-local user id and stores the mapping in `identities`;
every app that authenticates a person depends on them existing, and on their
joining by subject rather than by email — the mutable field, reassignable
between people, whose reuse silently merges two accounts.

## How they are applied

An ordinary `Job` — not a PreSync hook, which would never fire because hooks are
not part of ArgoCD's desired-state comparison — running `psql` from the image the
spoke's own Postgres runs. Its name carries a hash of the SQL, so it re-runs when
and only when these files change. It connects with the app's OWNER credential
(ADR-093) -- not the application's -- holds no Kubernetes API token, and reaches
nothing but `shared-cnpg` on 5432.

## Writing one

Idempotent, always. There is no migration ledger, and the whole set re-applies
on every sync:

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
