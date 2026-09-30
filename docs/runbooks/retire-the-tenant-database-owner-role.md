# Runbook: retiring the tenant database owner role

**Applies to:** ADR-099 (the platform provisions a database, not a schema)
**Status:** **prerequisite** to two separate things, for every database on the
spoke:

1. syncing the tenant-db composition change that removes the owner role; and
2. **any application upgrading to `zero-ops-auth` 0.18.0.**

The second is the urgent one and is easy to miss. See "Why 0.18.0 is blocked on
this" below.

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

## Why 0.18.0 is blocked on this

Deleting the baseline migrations from the chart (rc.130) removed the files. It
changed **nothing in any live database**. Every database provisioned before
2026-10-01 still has, right now:

- `users` and `identities` with **row-level security enabled and policies
  present**, owned by `tenant_<t>_<a>_owner`;
- the `resolve_user` function, `SECURITY DEFINER`, owned by the same role.

`zero-ops-auth` **0.17.0 and earlier still work**, because they call that
function, and it runs as its owner.

**0.18.0 does not.** It issues its own SQL as `tenant_<t>_<a>_user`, which is not
the owner, so the policies apply — and they compare against
`request.jwt.claims`, which nothing sets. They match zero rows. `resolveUser`
would find no identity for a person who has one, judge them new, and attempt an
insert that the same policies refuse.

So the order is: **this runbook, then 0.18.0.** Not the other way round.

## What to run

Per tenant database, **connected as a superuser** — `postgres`, from the CNPG
cluster's own superuser secret.

Not `crossplane_admin`. An earlier version of this runbook said to use it, on the
strength of the composition declaring `Grant{role: crossplane_admin, memberOf:
<owner role>, withOption: ADMIN}` and a matching one for the app role. Measured on
both live databases on 2026-10-01, it is a member of **neither**:

```
crossplane_admin_member_of_both = f, f
```

Why the declared grants did not produce the membership is worth finding out
separately — they carried `deletionPolicy: Orphan`, so a rebuild can leave them
recorded and unreconciled — but it does not change what to do here. `REASSIGN
OWNED` needs the privileges of both the old and the new role, so anything short of
a superuser needs a membership grant first, which is itself a superuser operation.
Use the superuser directly rather than granting a path to it and leaving it behind.

**Check before you start**, because the answer decides nothing else in this
runbook but will tell you if the ground has moved:

```sql
SELECT pg_has_role('crossplane_admin', :'old', 'MEMBER') AS member_of_old,
       pg_has_role('crossplane_admin', :'app', 'MEMBER') AS member_of_app,
       current_setting('is_superuser') AS you_are_superuser;
```

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

### This moves everything, not just the baseline tables

Say so out loud, because the word "baseline" in this runbook's title undersells it.
Measured on 2026-10-01:

| | what the old role still owned |
|---|---|
| oranger | the database, 29 relations in `public`, 2 functions |
| waypoint | the database, 118 relations in `public`, and all of `workflow`, `drizzle`, `workflow_drizzle` and `graphile_worker` — 4 schemas — plus 9 functions |

Moving all of it to the application is the intended outcome, not a side effect:
under ADR-099 the application owns what is in its database. It is also what fixes
waypoint's queue, since `graphile_worker`'s row-level security exempts the owner
and defines no policies.

`REASSIGN OWNED` is the right tool **here** and was the wrong one inside the old
baseline migrations: it is database-wide, which is exactly what is wanted when the
intent is "this role owns nothing any more", and exactly what was not wanted when
the intent was "these particular schemas".

Run `DROP OWNED` after the reassign, not before — it drops objects the role owns,
and after step 2 it owns none, so it removes only privileges granted to it.

## 5. Remove the baseline's row-level security

Once the application owns these tables it is exempt from their policies, so they
bind nobody while still reading as enforcement in `\d`. That inert state is
exactly what ADR-093 found and called worse than no policy at all, and ADR-099
decides they are removed rather than left.

```sql
DO $$
DECLARE t text; p record;
BEGIN
  FOREACH t IN ARRAY ARRAY['users','sessions','identities','buckets','objects'] LOOP
    IF EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
                WHERE n.nspname='public' AND c.relname=t) THEN
      FOR p IN SELECT polname FROM pg_policy pol
                 JOIN pg_class c ON c.oid=pol.polrelid
                 JOIN pg_namespace n ON n.oid=c.relnamespace
                WHERE n.nspname='public' AND c.relname=t LOOP
        EXECUTE format('DROP POLICY IF EXISTS %I ON public.%I', p.polname, t);
      END LOOP;
      EXECUTE format('ALTER TABLE public.%I DISABLE ROW LEVEL SECURITY', t);
    END IF;
  END LOOP;
END $$;

-- The platform's function goes too: it was SECURITY DEFINER only because the
-- application could not read its own tables, and 0.18.0 does not call it.
-- Drop it AFTER every workload on this database is on 0.18.0, not before.
-- DROP FUNCTION IF EXISTS public.resolve_user(text, text, text);
```

**Only the five `public` baseline tables, named explicitly.** Do NOT disable
row-level security anywhere else, and specifically not in `graphile_worker`: its
RLS with no policies is graphile-worker's own access control, correct and
deliberate once the application owns the tables, and turning it off would open the
queue to every role in the database.

An application that wants per-user isolation writes its own policies now, against
its own schema, knowing what they are: a backstop against its own forgotten
`WHERE` clauses, not a boundary — it supplies the claim they compare against.

## Before you sync: who else holds the owner credential?

The role's Secret disappears with it, and anything mounting it fails at its next
run rather than at the sync. Found this way on 2026-10-01: **waypoint's SDK chart
runs its own migration Job as `tenant-waypoint-db-owner`.** After the reassign the
runtime role owns everything, so such a Job switches to the application's own
credential — and that switch must land AFTER step 2, because before it the runtime
role cannot run DDL.

```bash
grep -rn "db-owner" <each application repo> --include="*.yaml"
```

The platform cannot answer this from its own tree; the consumers are in the
application repositories.

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
