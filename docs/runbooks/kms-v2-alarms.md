# KMS v2 alarms: what woke you, and what to do

ADR-100 condition 8. **Owner: Platform Security / Control-Plane on-call.**

The scheduled carrier is `.github/workflows/kms-controls.yml`. It runs
`soloz kms check` from outside every cluster, hourly, and routes two failure classes at
different severities. This is what each one means and what to do about it.

## Read this first, whatever woke you

**The cluster is almost certainly still serving.** Every alarm here is about *state* — the
key's configuration, an identity's registration, a recorded invariant — not about a
control plane that has stopped. Kubernetes KMS v2 caches data encryption keys in the API
server, so Secrets keep working for some time after the key authority becomes unreachable,
and nothing in this runbook is a reason to start failing over.

**What is actually urgent is the opposite of obvious:** the blast radius grows with time
and surfaces on a *cold read*. A control plane that looks fine will stop being fine the
next time an API server restarts. So these are not "fix it in the morning", and they are
not "page the tenant" either.

**Nothing in this runbook asks you to let a tool repair anything.** ADR-100 makes the
in-cluster reconciler observe-only on purpose: anything able to update the key encrypting
its own cluster's etcd is a credential whose compromise changes the controls on that key.
Remediation is deliberate, from outside the cluster, by a human.

## `kms-security-alarm` — critical

Payload `class: kms-security-alarm`, exit code 1. Desired and authoritative state differ.

### `primary version REGRESSED`

**The one that matters most.** A key version that was retired is primary again. The KMS v2
interface forbids reusing an identifier and this material's has already been used.

```
soloz kms verify --cluster <name>        # what the key actually says now
soloz kms drift  --cluster <name>        # the floor it is being measured against
```

**Do not lower the version floor to match.** The floor is the record; the key is the
deviation. Lowering it makes the alarm stop and the violation permanent.

Recovery is **forward**: rotate to a new version, so the identifier is one the API server
has not seen. Then advance the floor in `manifests/hub-core-services/crossplane/kms/hub-key.yaml`
as a reviewed commit.

Then ask how it happened. Nothing in the platform can express this — the reconciler has no
primary-version field and the plugin cannot change it — so a regression means somebody
used a credential outside the reconciler. **That is a second incident**, and the first
question is which identity did it.

### `rotationPeriod` / `destroyScheduledDuration` / `purpose` drift

The key's configuration no longer matches the reviewed manifest.

`destroyScheduledDuration` shrinking is the dangerous one: it is the window between a
mistaken destroy and unreadable backups, and shortening it is a reduction in a safety
margin that nothing else will flag.

Restore the key's configuration to what the manifest declares, or — if the policy genuinely
changed — change the manifest in a reviewed commit and say so. Do not do both silently.

### `version floor` absent

The durable half of the no-reactivation rule does not exist for this key, so a reactivation
would pass unnoticed. Add `kms.soloz.io/primary-version-floor` to the manifest, set to the
current primary version, as a reviewed commit. The preflight will start enforcing it.

### `the uploaded signing keys are STALE`

Every projected ServiceAccount token will fail validation, so the Crossplane reconciler
cannot authenticate **while the cluster looks healthy**.

```
kubectl get --raw /openid/v1/jwks > jwks.json
soloz kms jwks --cluster <name> --jwks-file jwks.json
```

Then ask **why**, because this should not happen spontaneously: ADR-100's policy is that
the service-account signing key rotates only as part of an explicit rebuild or
key-replacement. If no rebuild happened, either something rotated it or the policy no
longer holds — and the policy has a recorded expiry condition that this is the trigger for.

### `the provider does not trust this cluster's certificate authority`

The KMS plugin cannot authenticate, so the **next control-plane restart will be unable to
decrypt**. The cluster is serving on cached data keys right now; it will not survive a
restart.

Most likely cause: a rebuild replaced the cluster CA and the trust anchor was not updated.
Re-upload the current CA to the provider, then:

```
soloz kms trust-anchor --cluster <name> --ca-file <the cluster CA>
```

**Do not restart a control-plane node until this passes.**

## `kms-observation-failure` — warning

Payload `class: kms-observation-failure`, exit code 2. **The check could not determine
anything.** The key may be perfectly correct and the *check* may be broken.

This is deliberately not critical and deliberately not silent. Paging security for a 500
from an IAM API teaches people the alarm is unreliable; ignoring it lets a dead checker
masquerade as health.

| what the report says | likely cause |
|---|---|
| the key could not be read | the carrier's own credential, its WIF binding, or a GCP outage |
| the provider could not be read | the same, or the provider was deleted |
| the cluster's signing keys were not supplied | the carrier could not reach the cluster — expected if the cluster is genuinely down, and then *that* is the incident |
| no reviewed desired state | the manifest moved or the path is wrong |

**If it persists for more than two consecutive runs, treat the control as down** and say so
explicitly in the incident channel. An observation failure nobody closes is
indistinguishable from a control nobody has.

## `kms-carrier-failure` — warning

The check produced no exit status, so the control's own delivery is broken. The detector
did not run or did not report. Start with the workflow run linked in the payload.

## Escalation

1. **On-call** acknowledges, triages against this runbook, and remediates. Most of the
   above is one command plus a reviewed commit.
2. **Escalate immediately, without attempting remediation**, for:
   - `primary version REGRESSED` — it implies a credential used outside the reconciler, and
     the identity question matters more than the key does;
   - anything suggesting the key's **IAM policy** changed — that is the one grant that
     could lead to decryption;
   - a trust-anchor failure on the **hub**, because the hub cannot be rebuilt without it.
3. **Tenant notification** is Platform's call, per ADR-067, and is required if data
   confidentiality may have been affected rather than merely availability. A regression and
   an IAM change can affect confidentiality; configuration drift and a stale JWKS do not.

## What is NOT in scope here

- A **disclosed key** is an incident with its own procedure:
  `docs/runbooks/encrypt-secrets-at-rest.md`, and it is a rotation rather than a drift fix.
- **Enabling** encryption at rest, which is a provisioning change and not an alarm.
- **SLO or error-budget breaches.** The plugin now exports latency, failures by reason, the
  active key version and health, but no budget is defined yet and ADR-100 holds that open
  rather than recording a guessed number. There is nothing to page on.
