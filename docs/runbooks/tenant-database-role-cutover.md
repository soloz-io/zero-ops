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

## Phase 3 — Transfer database ownership

```sql
ALTER DATABASE "<db>" OWNER TO "<target-role>";
```

The composition's `database.spec.owner` now patches from `spec.dbRoleName`, so the
declarative state converges to the same identity. For oranger this is the whole of
the ownership work.

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

## Phase 5 — Credential convergence

Let the ExternalSecret reconcile, then confirm the Secret actually changed:

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
