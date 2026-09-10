# Restoring platform-db from its archive

Use this when the hub database's data is gone and the object-store archive is the
only copy left: its node was re-provisioned, its `local-path` volume went with it,
or the Cluster was deleted.

**This bounds data loss; it does not eliminate it.** Recovery restores to the last
archived WAL. Anything written after that is gone. The hub runs `instances: 1`, so
there is no replica to fail over to — that is a topology decision (ADR-070), not
something this procedure can recover.

Do not run this to "refresh" a working database. `bootstrap` is read once, at
Cluster creation, so this procedure necessarily destroys the Cluster object first.

## Before you start

1. **Confirm the data is actually gone.** A Pending pod is not a lost volume. If
   the PVC still exists and the node is coming back, wait — restoring loses every
   unarchived write for nothing.

   ```
   kubectl -n platform-data get cluster platform-db -o wide
   kubectl -n platform-data get pvc
   ```

2. **Confirm the archive is readable and find a backup to restore to.** Recovery
   by explicit `backupID` is well defined; a bare "latest" over this prefix is not,
   because the archive holds WALs from more than one incarnation of the cluster
   (see the note in `manifests/hub-core-services/database/platform-db.yaml`).

   ```
   kubectl -n platform-data get backup -o custom-columns=\
NAME:.metadata.name,PHASE:.status.phase,ID:.status.backupId,STOPPED:.status.stoppedAt
   ```

   If no Backup object survives, list the prefix directly with the S3 credentials
   in the `s3-credentials` Secret. If the archive is empty, stop: there is nothing
   to restore and `initdb` is the only path forward.

3. **Check which prefix the lost cluster was writing to.**

   ```
   kubectl -n platform-data get cluster platform-db \
     -o jsonpath='{.spec.backup.barmanObjectStore.serverName}'
   ```

   That value is what `externalClusters[0].barmanObjectStore.serverName` in
   `manifests/hub-core-services/database-recovery/kustomization.yaml` must name.
   The `backup` serverName in the same file must be a **different, unused** prefix.
   If they match, the replacement archives into the WALs it restored from and
   `barman-cloud-check-wal-archive` blocks every backup afterwards.

## Restoring

1. Point the hub's database Application at the recovery overlay
   (`manifests/hub-core-services/database-recovery`) instead of the placement
   overlay, in the tenant's own repository, and let ArgoCD sync.

2. Delete the Cluster object. Nothing else recreates it, and `bootstrap` is only
   read at creation.

   ```
   kubectl -n platform-data delete cluster platform-db
   ```

3. Watch the replacement come up. CNPG creates a recovery Job before any instance
   pod; that Job is where a bad `backupID` or an unreadable prefix surfaces.

   ```
   kubectl -n platform-data get pods -w
   kubectl -n platform-data logs -l cnpg.io/jobRole=full-recovery -f
   ```

4. **Verify the data is actually there** before declaring success. A recovered
   cluster reports Healthy whether or not it holds what you expected.

   ```
   kubectl -n platform-data exec -it platform-db-1 -- psql -U postgres -c '\l'
   ```

   The databases the platform expects are `control_plane`, `hub` and `openmeter`.

5. **Confirm archiving restarted on the new prefix.** If it has not, the guard is
   refusing the prefix — which means the two serverNames were not distinct.

   ```
   kubectl -n platform-data get cluster platform-db \
     -o jsonpath='{.status.conditions[?(@.type=="ContinuousArchiving")]}'
   ```

## Afterwards

Revert the Application to the placement overlay and **fold the new serverName into
the base manifest**. The recovery overlay is written for the next recovery, and
leaving `platform-db-v3` only in the overlay means the base still names the prefix
the cluster no longer writes to.

Do not bump the base serverName at any other time. `check-wal-archive` runs only
before a cluster's first archive, so renaming on a running cluster re-arms the
guard against a prefix that may not be empty and stops archiving — trading a
working backup for a broken one.

## Related

- `manifests/hub-core-services/database-recovery/` — the overlay this uses
- ADR-046 §11 — placement classes and why `local-path` has no snapshots
- ADR-070 — why single-instance is a topology decision, and what HA would require
- ADR-075 — on-prem nodes as a capability, and why moving placement is a restore
