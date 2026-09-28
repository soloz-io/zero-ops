# Runbook: cutting a tenant database over to its canonical role

**Applies to:** ADR-090 (the database role has one name)
**Status:** prerequisite to rolling out the ADR-090 composition. Do not sync that
change into a spoke before executing this.
**Last audited against the live spoke:** 2026-09-28

## What this is for

ADR-090 gives the database role one definition, `tenant_<tenantId>_<appId>_user`,
carried on the XR and consumed everywhere. Applying that composition changes the
`crossplane.io/external-name` on each `Role`, which makes provider-sql create the
new role — and leaves the **legacy role still owning the database and every object
in it**.

That is five separate transitions, and only the first is declarative:

```
Kubernetes identity        (metadata.name — deliberately NOT changed by ADR-090)
  ≠ Crossplane external identity   (the annotation — what the composition moves)
  ≠ database ownership migration   (ALTER DATABASE … OWNER)
  ≠ object ownership migration     (REASSIGN OWNED — the one that is easy to miss)
  ≠ credential cutover             (the Secret, and restarting what holds it)
  ≠ legacy-role retirement         (NOLOGIN, then DROP)
```

Changing `external-name` is **not** the cutover. It only changes which PostgreSQL
role the managed `Role` object represents.

## Two corrections this runbook exists to carry

**`ALTER DATABASE … OWNER` is necessary but not sufficient.** It changes the
database's owner and nothing else. Schemas, tables, sequences, views, types and
functions keep their existing owners, and `DefaultPrivileges` only affects objects
created *afterwards*. An existing database needs `REASSIGN OWNED`.

**Do not enumerate consumers by controller kind.** A sweep of
`deploy,sts,ds,job,cronjob` across every namespace reported that the only consumer
of tenant database credentials was the one-shot baseline migration Job. That was
**wrong**: waypoint's SDK runs as an Argo Rollouts `Rollout`, which that sweep
never queried, and it held ~42 pooled connections at the time. The authoritative
consumer list comes from the database:

```sql
SELECT usename, client_addr, application_name, state, count(*)
  FROM pg_stat_activity WHERE usename = '<legacy-role>'
  GROUP BY 1,2,3,4;
```

then resolve each `client_addr` to a pod, and the pod to its `ownerReferences`.
A live session is evidence; an empty Kubernetes search is not.

## Phase 0 — Freeze

Prevent the transition from racing a migration. Scope this to the tenant database
composition; there is no need to freeze the spoke or Crossplane globally.

- Disable auto-sync on the spoke-catalog Application (or set the XR's
  `compositionUpdatePolicy: Manual`) so the external-name change lands when you
  choose.
- Confirm no `tenant-baseline-migration-*` Job is running in the target namespace.

## Phase 1 — Record the current state

Per database, before touching anything:

```sql
\l+ <db>                                     -- owner
SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname='<db>';
SELECT nspname, pg_get_userbyid(nspowner) FROM pg_namespace
  WHERE nspname NOT IN ('pg_catalog','information_schema')
    AND nspname NOT LIKE 'pg_toast%';
SELECT c.relkind, pg_get_userbyid(c.relowner), count(*) FROM pg_class c
  JOIN pg_namespace n ON n.oid=c.relnamespace
  WHERE n.nspname NOT IN ('pg_catalog','information_schema')
    AND n.nspname NOT LIKE 'pg_toast%' GROUP BY 1,2;
SELECT pg_get_userbyid(proowner), count(*) FROM pg_proc p
  JOIN pg_namespace n ON n.oid=p.pronamespace
  WHERE n.nspname NOT IN ('pg_catalog','information_schema') GROUP BY 1;
SELECT count(*) FROM pg_default_acl WHERE pg_get_userbyid(defaclrole)='<legacy>';
SELECT r.rolname, m.rolname FROM pg_roles r
  LEFT JOIN pg_auth_members am ON am.member=r.oid
  LEFT JOIN pg_roles m ON m.oid=am.roleid WHERE r.rolname='<legacy>';
```

Also record the credential Secret's current `username`, and keep a copy of the
current password — rollback depends on it.

**The invariant to establish before proceeding:** every object owned by the legacy
role either will be reassigned to the target role, or is positively known not to be
required. Establish this now, not after changing the database owner.

### State as audited 2026-09-28

**oranger** — trivial. Only `public`, owned by `pg_database_owner`. No objects, no
sessions. Its baseline migration has never succeeded, so there is nothing to
preserve and nothing to reassign.

| | |
|---|---|
| database | `nutgraf-oranger-db` (owner `tenant-oranger-user`) |
| target role | `tenant_nutgraf_oranger_user` |
| objects owned by legacy | none |
| active sessions | none |
| consumers | none (oranger-serve holds no database wiring) |

**waypoint** — substantial. Phase 4 is mandatory.

| | |
|---|---|
| database | `tenant-waypoint-db` (owner `tenant-waypoint-user`) |
| target role | `tenant_nutgraf_waypoint_user` |
| schemas owned by legacy | `graphile_worker`, `workflow`, `workflow_drizzle`, `drizzle` (`public` is `pg_database_owner`) |
| tables / indexes / sequences / views / types | 55 / 106 / 5 / 1 / 1 |
| functions | 8 |
| default ACLs owned by legacy | 0 |
| active sessions | ~42 pooled, from 2 pods |
| consumer | **`Rollout/sdk-workload`** (Argo Rollouts, 2 replicas) |

waypoint's SDK reads **both** affected secrets:

```
DATABASE_URL, CONTROL_PLANE_DATABASE_URL  <- waypoint-pooler-app/url   (DSN embeds the role)
PGUSER, PGPASSWORD, PGHOST, PGPORT, PGDATABASE <- tenant-waypoint-db-credentials/*
```

Note also the orphaned role `tenant-nutgraf-waypoint-user`: it owns nothing and is
a member of nothing, left behind by an older composition revision. It is **not**
the target and must not be adopted as one — retire it with the same quarantine as
any other legacy role.

## Phase 2 — Establish the new PostgreSQL role

Let the ADR-090 composition reconcile. The `Role` object is the same Kubernetes
object; only its external identity moves, so nothing is deleted.

Wait for `SYNCED=True` **and** `READY=True`, then verify in the database itself —
the Kubernetes resource existing is not proof:

```sql
SELECT rolname, rolcanlogin FROM pg_roles WHERE rolname='<target>';
```

## Phase 3 — Database ownership transfers ITSELF

**This is not an operator step.** The composition patches `database.spec.owner`
from `spec.dbRoleName`, and CNPG performs the ownership change during
reconciliation. `ALTER DATABASE ... OWNER` is the *effect*, and the query below is
a verification, not an instruction:

```sql
SELECT datname, pg_get_userbyid(datdba) FROM pg_database WHERE datname='<db>';
-- expect the TARGET role
```

Run it by hand only if reconciliation has not done it.

### What this means for the whole runbook

**Applying the composition starts the cutover.** Phase 2 (the role), Phase 3 (the
database owner) and Phase 5 (the credential) all happen on sync, unattended. That
was not obvious when this runbook was first written -- it assumed Phase 3 was
manual -- and the difference matters:

  the moment the composition syncs, a populated database is owned by a role that
  owns NONE OF THE OBJECTS IN IT, and the application's credential already names
  that role

The only thing keeping the application alive in that window is its EXISTING
pooled connections, which authenticated as the legacy role before the change and
are not re-authenticated. They are not evidence of correctness. When they cycle --
a pod restart, a pooler restart, an idle timeout -- the application reconnects as
the new role and is refused.

Observed on 2026-09-28: rc.104 synced, and waypoint was left owning its database
with 168 relations, 4 schemas and 8 functions still on the legacy role, surviving
on 39 stale sessions.

**So Phase 4 is not a later step. It is the other half of Phase 3, and the gap
between them is an outage waiting for a reconnect.** Do not sync the composition
into a spoke holding a populated tenant database unless you can run Phase 4
immediately afterwards.

## Phase 4 — Reassign existing objects (waypoint)

Run **in each database** where the legacy role owns objects, as a superuser:

```sql
\c <db>
REASSIGN OWNED BY "<legacy-role>" TO "<target-role>";
```

`REASSIGN OWNED` is per-database and does not cross databases; run it once per
database, and re-run the Phase 1 ownership queries afterwards to confirm nothing
remains.

**Do not use `DROP OWNED` here.** It removes objects and privileges rather than
transferring them. It belongs only in Phase 9, and only once you have positively
established that nothing required remains associated with the legacy role.

## Phase 5 — Credential convergence (also automatic)

The composition patches `username` into both ExternalSecret templates from
`spec.dbRoleName`, so this converges on sync like Phase 3. Confirm rather than
perform, then do the part that is NOT automatic: recycling the consumers.

Confirm the Secret actually changed:

```
username = <target-role>
password = unchanged (the role's live password)
```

Check **both** secrets — `tenant-<app>-db-credentials` and
`<app>-pooler-app`, whose `url` embeds the role name in its DSN.

Then restart the consumers found in Phase 1. For waypoint that is a `Rollout`, so
the Deployment verbs do not apply:

```
kubectl argo rollouts restart sdk-workload -n tenant-nutgraf-waypoint
```

Recycle the pooler's server connections too: they were established as the legacy
role and will not re-authenticate on their own.

## Phase 6 — Authentication test

**Do not use the migration Job as the first proof.** Connect directly with the
rendered credential:

```sql
SELECT current_user, current_database();
```

Expect the target role and the target database. Then re-verify the database owner,
schema and object ownership, and that the role can actually read and write what it
needs.

## Phase 7 — Run the baseline migration

This is the strongest end-to-end proof, because it exercises the exact credential
path that originally failed.

- **oranger** — new role, empty database, migration creates `users`, `identities`,
  `sessions`, `buckets`, `objects`.
- **waypoint** — new role, existing database, existing objects reachable, migration
  applies idempotently and changes nothing.

The Job's name carries a hash of the SQL, so a previously failed Job must be
deleted for a new one to be created.

## Phase 8 — Prove the legacy role is unused, then quarantine

All five must hold:

```
no Secret references it
no workload references it        (check Rollouts, not just Deployments)
no active sessions use it        (pg_stat_activity)
no required object is owned by it
no role membership remains
```

Then quarantine rather than delete:

```sql
ALTER ROLE "<legacy-role>" NOLOGIN;
```

This is reversible and is the last point at which rollback is a simple credential
reversal. Leave it here long enough to cover anything that connects on a schedule
rather than continuously.

## Phase 9 — Retire the legacy role

Only after the quarantine has held:

```sql
DROP ROLE "<legacy-role>";
```

`DROP ROLE` fails while the role owns any object or holds any privilege — that
failure is a signal that Phase 4 was incomplete, not an obstacle to work around.
`DROP OWNED BY "<legacy-role>"` is the tool for genuinely unwanted leftovers, and
only once you are certain they are unwanted.

Verify:

```
legacy role absent
target role present
database owner = target role
```

Only now rename the `Role` object's `metadata.name` to a neutral Kubernetes
identity, if you want that. It is deliberately left alone until here: `db-user`
carries `deletionPolicy: Delete`, and provider-sql's delete is a bare
`DROP ROLE IF EXISTS` with no `REASSIGN OWNED`, so renaming it while the role still
owns a database deletes the composed resource, fails the drop, and leaves the
managed resource stuck in deletion with the XR blocked behind it.

## Rollback

**The safe rollback boundary is before Phase 9.** Up to that point the legacy role
still exists, its password is still known, and the target role is additive.

```
1. stop any running migration
2. restore the Secret's username (and password, if it was rotated)
   -- at the source: Infisical for the password; for the username, revert the
      composition or pin the XR, since it is now a patched value
3. ALTER DATABASE "<db>" OWNER TO "<legacy-role>";
4. REASSIGN OWNED BY "<target-role>" TO "<legacy-role>";   -- if Phase 4 ran
5. ALTER ROLE "<legacy-role>" LOGIN;                        -- if Phase 8 ran
6. restart consumers; verify authentication as the legacy role
7. remove or disable the target role later, at leisure
```

After `DROP ROLE`, rollback is no longer a credential reversal — it is a restore.
That is the whole reason Phase 8 quarantines instead of deleting.

## Order of execution

Do **oranger first**. It has no objects, no sessions and no consumers, so it
exercises Phases 2, 3, 5, 6 and 7 end to end with nothing to lose, and its
baseline migration is currently failing anyway. Only then do waypoint, which is
live, populated, and the only case where Phase 4 does real work.

## What actually happened, 2026-09-28

The first real execution, against nutgraf-01 at 0.1.16-rc.104. Recorded because it
differed from the plan in one important way.

| Phase | Plan | Reality |
|---|---|---|
| 2 role created | on sync | on sync |
| 3 database owner | manual `ALTER DATABASE` | **automatic** on sync |
| 5 credential username | manual check | **automatic** on sync |
| 4 object ownership | a later step | **the only manual step, and urgent** |

**waypoint.** rc.104 synced and left the database owned by the new role while 168
relations, 4 schemas and 8 functions stayed on the legacy one; 39 stale sessions
were the only thing still working. A single

```sql
REASSIGN OWNED BY "tenant-waypoint-user" TO "tenant_nutgraf_waypoint_user";
```

moved all three classes exactly (verified 4/168/8 to zero on the legacy role and
4/168/8 on the target). Then `spec.restartAt` on the `Rollout` -- not a Deployment
restart -- plus deleting the pooler pod to drop its server connections. Sessions
went 18 legacy / 0 new, to 9 / 18 mid-rollout, to **0 / 37**. The new pod reached
`Ready=True` and its logs show it querying `workflow_runs` normally.

**oranger.** No `REASSIGN OWNED` was run, deliberately: the Phase 1 audit found
zero objects on its legacy role, so there was nothing to move and the baseline
migration creates the tables owned by the new role directly. Deleting the failed
Job (its name is a hash of the SQL, so it does not re-run on its own) produced a
run that completed in 8s, and all five baseline tables are owned by
`tenant_nutgraf_oranger_user`.

**Phase 8, same day.** All three legacy roles quarantined after confirming zero
active sessions on each:

```sql
ALTER ROLE "tenant-waypoint-user" NOLOGIN;
ALTER ROLE "tenant-oranger-user" NOLOGIN;
ALTER ROLE "tenant-nutgraf-waypoint-user" NOLOGIN;   -- the orphan
```

Afterwards: the three legacy roles `canlogin=false`, both target roles
`canlogin=true`, sessions unaffected, no authentication or permission errors, and
oranger's five baseline tables intact.

The zero-session pre-check is the part that matters. `NOLOGIN` does not terminate
an already-authenticated session, so a role with live sessions would appear to
survive quarantine and then fail at its next reconnect -- the same delayed failure
the Phase 3/4 gap produces, arriving later and looking unrelated.

`DROP ROLE` was deliberately NOT run. `NOLOGIN` is the reversible boundary and the
last clean verification point; dropping is a separate final cleanup.
