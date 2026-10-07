# KMS v2 adoption: the dev plan

**Dev only. There is no staging and no production, and no cluster here holds anything that
must survive.** That is not a caveat — it removes most of the procedure this document used
to describe, and the removals are the point.

ADR-100 is still `Proposed`. What follows is how the design gets exercised, not an approved
production change.

## What "dev only, no backward compatibility" removes

The previous version of this plan carried a staged migration: provision under `secretbox`,
upload the trust anchor, roll onto KMS, retain `identity` for reads, rewrite every Secret,
count what remains, remove the fallback. Every step of that exists to keep **data already
written** readable while the provider changes underneath it.

There is no such data. So:

| dropped | why it existed | why it goes |
|---|---|---|
| the `secretbox` phase on a new cluster | the CA must exist before a trust anchor can reference it | still true, but it is a **provisioning-order** problem, not a migration one — see below |
| retaining `identity` for reads | Secrets kubeadm wrote before the provider was active are plaintext | on a cluster that boots with KMS already configured, **kubeadm's own writes go through the provider**. No plaintext is ever written, so there is nothing for a fallback to read |
| rewriting every Secret, and counting | existing objects are sealed under the old provider | there are no existing objects |
| the `identity`-removal one-way door | removing a fallback makes a missed object unreadable | nothing was missed because nothing was migrated |
| staged rollback boundaries | production cannot be rebuilt | **`kubectl delete cluster` is the rollback.** It is cheaper, faster and more complete than any staged path |
| a separate disposable canary | production must not be the first target | `nutgraf-01` and `kms-canary-01` are both dev. `kms-canary-01` goes first because it has no workloads, not because it is safer |

**What does not go** is everything that validates the design rather than protecting data:
the key-scoped IAM denials, the outage behaviour, credential expiry and revocation, and the
federation lifecycle. Those are true or false independently of what environment they run in,
and the outage test already caught one assumption that was backwards.

## KMS from first boot

The ordering constraint is real and narrow: a trust anchor names the cluster's CA, and CAPI
creates that CA when the `Cluster` is reconciled — **before the first node boots.**

`internal/soloz-cli/cluster/provisioner.go` already separates those moments:
`applyCluster` at line 239, `WaitForReady` at line 689. The CA Secret (`<cluster>-ca`)
appears between them.

```
soloz kms init            key, identity, IAM binding          (no cluster involved)
        ↓
applyCluster              CAPI generates <cluster>-ca
        ↓
upload the trust anchor   from that CA, before any node boots
soloz kms trust-anchor    refuses if it did not take
        ↓
first node boots          encryption config already names KMS
                          initContainer issues the plugin certificate
                          kubeadm's own Secrets are wrapped from the first write
```

No migration, no fallback, no second roll. A cluster either comes up encrypted under KMS or
does not come up — and in dev, one that does not come up gets deleted.

**`--no-plaintext-fallback` is correct from day 0 here**, and it is the flag that was built
for a case that had not arrived yet. On a cluster with prior plaintext, dropping `identity`
is the dangerous edit; on one that never had any, retaining it would be the odd choice.

## The sequence

### 1 — GCP resources for the cluster

```
soloz kms init --cluster <name> --with-deny-probe --with-canary
```

**Proven by:** `soloz kms verify` — the key, its 90-day rotation period, its 30-day destroy
window, and exactly one role bound to one service account.

### 2 — Federation, and the identity proven scoped

Pool, X.509 provider with the CA as trust anchor, OIDC provider with the JWKS uploaded.

```
soloz kms trust-anchor --cluster <name> --ca-file <the cluster CA>
soloz kms jwks         --cluster <name> --jwks-file jwks.json
soloz kms prove        --cluster <name> --impersonate <the plugin's service account>
```

**Proven by:** 12 of 12 — five administrative denials on the canary key, cross-key denial on
the deny-probe. Then `soloz kms revoke-impersonation`, because the grant exists to collect
evidence and must not outlive it.

### 3 — Provision with KMS configured

The v5 template, the ClusterClass patches, `--encryption-mode kms-v2`,
`--no-plaintext-fallback`.

**Proven by:** the cluster reaches `Provisioned`; the plugin's `Status` is healthy;
`soloz_kms_plugin_healthy` is 1; a Secret written and read back; and etcd showing
`k8s:enc:kms:v2:soloz-kms:` read directly out of the etcd pod. With no `identity` in the
list, a successful read is already evidence — unlike the migration path, where `identity`
could have served it.

**If it does not come up:** read the plugin's logs and the initContainer's, then delete the
cluster. There is nothing to recover and nothing to roll back to.

### 4 — The tests that validate the design, not the data

In order, on `kms-canary-01`:

- **Outage.** A write must succeed from a cached data key; a **cold read after an
  API-server restart must FAIL**; recovery must need no operator action. This is the one
  that already found a backwards assumption in Gate 1 — the first version asserted writes
  would fail, and they do not.
- **Credential expiry and revocation.** Each must be distinguishable from an unreachable
  authority. The ADR records that gRPC status codes do **not** separate them reliably, so
  this needs the separate credential probe — and if that probe is not built, this fails
  rather than being waived.
- **Rotation.** Rotate the key in GCP; the reported `key_id` must change, pre-rotation
  ciphertext must still decrypt, and `soloz_kms_plugin_active_key_version` must rise.
- **Drift detection.** Lower the version floor on a branch and confirm `soloz kms check`
  alarms and the carrier delivers.

### 5 — `nutgraf-01`

Same sequence, on a cluster that has workloads. This one **is** a migration — it has
Secrets under `secretbox` — so it is the only place the staged path applies, and the honest
dev answer is to ask whether rebuilding it is cheaper than migrating it. It probably is.

### 6 — The hub

Last, and it needs its own validation. A spoke cannot prove hub rebuild recovery: a
rebuilt hub has a new CA while the provider holds the old one, and the hub is the cluster
whose own rebuild depends on the key it cannot yet read. ADR-100 stays `Proposed` until
that is demonstrated **on the hub**.

## What is no longer worth doing

**Enumerating and rotating every credential the disclosed `nutgraf-01` key exposed.** The
rotation closed the window; the transitive exposure is real in principle and the credentials
are dev credentials for dev infrastructure. Rotating them is hygiene somebody can do when
convenient, not incident response. If any of them reaches something that is not dev — a
registry token, a Hetzner project, an Infisical path shared with something real — that one
is worth rotating now, and that is a short list to check rather than a programme.

**Pre-production acceptance criteria as blocking gates.** ADR-100's A1–A6 stay as the
criteria for production adoption, which is the right place for them. They are not gates on
dev work, and treating them as such is how a week goes into proving a backup restoration for
a cluster nobody would restore.
