# Runbook: retiring the tenant database owner role

**Applies to:** ADR-099 (the platform provisions a database, not a schema)
**Status:** **prerequisite** to syncing the tenant-db composition change that
removes the owner role. Do not sync that change into a spoke before executing
this, for every database on it.

## Why this is not declarative

The composition's `db-owner` Role carried `deletionPolicy: Delete`, so removing it
issues `DROP ROLE`. PostgreSQL **refuses to drop a role that owns any object**,
and on every database provisioned before 2026-10-01 this role owns:

- the baseline tables the platform used to create — `users`, `identities`,
  `sessions`, `buckets`, `objects` — and the `resolve_user` function;
- anything the application's own libraries created while the migration Job was
  the connecting role. On waypoint that is the `graphile_worker` schema, whose
  tables enable row-level security and define no policies and no grants, so
  ownership IS their access control.

Left alone, the sync fails with `role "tenant_<t>_<a>_owner" cannot be dropped
because some objects depend on it`, and the failure names none of the above.

**The application is already affected by this ownership**, independently of the
retirement: waypoint's workflow runs stopped re-enqueuing when ownership moved,
because its runtime role is not the owner the queue's RLS exempts. Handing
ownership back fixes that at the same time.

## What to run

Per tenant database. Connect as a role that is a member of both — `crossplane_admin`
holds ADMIN OPTION on each (tenant-db composition, resources 4b and 4c as they
were), so it can do this; the owner's own credential cannot, because it has no
membership in the application's role.

```sql
\set app  'tenant_<tenantId>_<appId>_user'
\set old  'tenant_<tenantId>_<appId>_owner'

-- 1. What the old role still owns. Read this before changing anything.
SELECT n.nspname, c.relname, c.relkind
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE c.relowner = (SELECT oid FROM pg_roles WHERE rolname = :'old')
 ORDER BY 1, 2;

-- 2. Hand everything over, in one transaction.
BEGIN;
REASSIGN OWNED BY :"old" TO :"app";
COMMIT;

-- 3. Nothing left. This must return zero rows.
SELECT n.nspname, c.relname
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
 WHERE c.relowner = (SELECT oid FROM pg_roles WHERE rolname = :'old');

-- 4. Privileges granted TO the old role, which also block the drop.
DROP OWNED BY :"old";
```

`REASSIGN OWNED` is the right tool **here** and was the wrong one inside the old
baseline migrations: it is database-wide, which is exactly what is wanted when the
intent is "this role owns nothing any more", and exactly what was not wanted when
the intent was "these particular schemas".

Run `DROP OWNED` after the reassign, not before — it drops objects the role owns,
and after step 2 it owns none, so it removes only privileges granted to it.

## Then

Sync the composition. Crossplane issues `DROP ROLE` and it succeeds.

Verify the application still connects — it now owns its own tables, which is what
ADR-099 decides — and, on waypoint, that a workflow run re-enqueues.

## What you do NOT need to do

Nothing moves, and no data is copied. The tables stay exactly where they are with
the rows they have; only `relowner` changes. There is no downtime window: an open
connection keeps working across the reassignment.

Do not drop `users`, `identities`, `sessions`, `buckets` or `objects`. They are the
application's now. `identities` in particular is how its existing people are found.
