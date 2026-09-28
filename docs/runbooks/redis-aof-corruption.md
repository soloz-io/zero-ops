# Runbook: platform-redis will not start (corrupt AOF)

**First seen:** 2026-09-28, ~10 hours before detection
**Durable fix:** `--aof-load-corrupt-tail-max-size 4096` in `manifests/hub-core-services/redis/redis.yaml`

## Recognising it

`platform-redis-0` in `CrashLoopBackOff`, and its log ends with:

```
* Done loading RDB, keys loaded: 2308, keys expired: 0.
# Bad file format reading the append only file
  appendonlydir/appendonly.aof.204.incr.aof at offset 27527150.
```

The RDB base loads; the incremental AOF does not. Redis refuses to start on a
corrupt tail, so the pod can never recover on its own — a restart loop is the
steady state, not a transient.

## Why this is a platform-wide outage, not a cache outage

This is the part that makes it urgent. Nothing degrades gracefully:

```
platform-redis-0 crashlooping
  -> platform-redis Service has NO endpoint
    -> Infisical: connect EPERM 10.103.84.45:6379 -> CrashLoopBackOff -> HTTP 503
      -> ESO ClusterSecretStore infisical-backend    "unable to create client"
         ESO SecretStore       tenant-secret-store   "unable to create client"
        -> EVERY Infisical-backed ExternalSecret fails, on the hub AND on every
           spoke, including every tenant database credential and every pull secret
```

Two diagnostic notes worth keeping:

- **`EPERM` on connect is not a network policy denial.** Under Cilium's socket
  load balancer it means the Service has no backend. A policy denial presents as a
  silent packet drop, i.e. a hang. So `EPERM` points at "the pod is gone", not "the
  policy is wrong".
- **The symptom appears three systems away from the cause.** What gets reported is
  "ExternalSecrets are failing" or "the tenant credential is stale". Walk the chain
  down to endpoints before touching ESO or Infisical.

## Repair

Redis distinguishes a **corrupt tail** from a **truncated** AOF and has separate
options and messages for each. This is the corrupt-tail case;
`aof-load-truncated` is the wrong lever and will not help.

### 1. ArgoCD will fight you

The `platform-redis` Application has `automated: {prune: true, selfHeal: true}` and
syncs from the published OCI chart, not from the repository. Scaling the
StatefulSet down is reverted within seconds (`autoHealAttemptsCount` increments and
the Application logs `successfully synced`). Either suspend automation first, or
use the approach below, which does not require it.

### 2. Repair alongside the crashing pod

The PVC is `local-path` / RWO, so a second pod **on the same node** can mount it,
and a crashlooping Redis never successfully loads the AOF — so nothing is appending
to it while you work.

```yaml
apiVersion: v1
kind: Pod
metadata: {name: redis-aof-repair, namespace: platform-data}
spec:
  nodeName: <the node platform-redis-0 is bound to>
  restartPolicy: Never
  containers:
    - name: repair
      image: docker.io/library/redis:8.6-alpine   # the SAME image as the StatefulSet
      command: ["sleep", "3600"]
      volumeMounts: [{name: data, mountPath: /data}]
  volumes:
    - name: data
      persistentVolumeClaim: {claimName: data-platform-redis-0}
```

Use the same image the StatefulSet runs: `redis-check-aof` must match the format
that wrote the file.

### 3. Snapshot before touching anything, and verify the snapshot

```sh
BK=/data/aof-backup-$(date +%Y%m%d)
mkdir -p "$BK" && cp -a /data/appendonlydir "$BK"/
for f in /data/appendonlydir/*; do
  diff <(md5sum < "$f") <(md5sum < "$BK/appendonlydir/$(basename "$f")") \
    && echo "OK $(basename "$f")"
done
df -h /data     # confirm there is room first
```

Do not proceed on an unverified copy. The repair is a truncation; the snapshot is
the only way back.

### 4. Inspect, then fix

```sh
redis-check-aof /data/appendonlydir/appendonly.aof.manifest
#   -> AOF analyzed: size=27527573, ok_up_to=27527150, diff=423
#      size - ok_up_to == the corrupt tail. Sanity-check it is SMALL.

echo y | redis-check-aof --fix /data/appendonlydir/appendonly.aof.manifest
redis-check-aof /data/appendonlydir/appendonly.aof.manifest   # expect diff=0
```

If `diff` is large, stop and escalate — that is not an unclean shutdown, and
truncating it discards real writes.

### 5. Restart and verify the whole chain

Delete the repair pod, then delete `platform-redis-0` to skip the backoff timer.
Verify in order, because each step is the next one's precondition:

```
Redis log      "DB loaded from base file ..." and no format error
Endpoints      kubectl get endpoints platform-redis -n platform-data   (must be non-empty)
Infisical      pod 1/1 Running; curl https://infisical.<domain>/api/status -> 200
ESO stores     kubectl get clustersecretstore; kubectl get secretstore -A  -> Valid/True
ExternalSecrets  READY=True (they also recover on their own refresh interval)
```

Leave the backup directory in place until the ESO stores are confirmed healthy,
then remove it — it is ~28 MB on a 5 Gi volume and costs nothing to keep for a few
days.

## Prevention, and why it needs a release

`manifests/hub-core-services/redis/redis.yaml` now passes
`--aof-load-corrupt-tail-max-size 4096`: Redis discards a corrupt tail up to 4 KiB,
logs it, and starts, while anything larger still stops for a human.

**4096, not 423.** 423 was this incident's exact size; matching it would fix only
the corruption already fixed by hand. 4 KiB covers a partially written final
command and stays far below anything that could conceal real damage.

The option was verified in the **pinned** image rather than assumed from upstream
documentation:

```sh
strings /usr/local/bin/redis-server | grep -x aof-load-corrupt-tail-max-size
redis-server --version   # v=8.6.6, matching the RDB's own "produced by version"
```

**Applying this to the live cluster does not stick.** The Application self-heals to
the published chart, so the setting only takes effect once it ships in a bundle and
`targetRevision` advances. Verified: a direct `kubectl apply` of the rendered
overlay was reverted in about 90 seconds, `autoHealAttemptsCount: 1`, back to
`0.1.16-rc.102`.

## Root cause

An unclean shutdown left a partial write at the end of the incremental AOF. This
box had been under disk pressure, which is the usual way that happens here. Two
mitigations sit outside this runbook and are worth tracking separately: host disk
alarms, and the `local-path` single-node binding that makes this PVC's fate the
node's fate.
