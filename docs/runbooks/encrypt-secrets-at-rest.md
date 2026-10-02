# Runbook: encrypting Kubernetes Secrets at rest

**Applies to:** ADR-003 §6 (Protection at rest), ADR-076 (`secret-encryption-key`)
**Status:** required once per cluster. A cluster is not covered until step 6 has
passed on it.

**This document covers WORKLOAD CLUSTERS only.** The management cluster is encrypted
by a different mechanism and is not reachable from here — see "The management cluster"
below before assuming the fleet is covered.
**Also the rotation procedure.** A new key takes effect only for data written after
it, so rotating is this document again from step 4.

## Why enabling it is not finishing it

Encryption applies to subsequent writes. Turning the provider on leaves every
existing Secret stored exactly as it is now — which is the failure this runbook
exists to prevent: a control enabled, reported as complete, and protecting nothing
already written.

The provider list is ordered. `secretbox` is first so every WRITE is encrypted;
`identity` is second so every existing plaintext READ still succeeds. That ordering
is what makes step 3 safe and what makes step 6 necessary — while `identity`
remains, a plaintext Secret is still readable and the control is partial.

## 0. Before you start

The escrow must be reachable. The key is the one value that cannot be regenerated —
etcd **and every etcd backup** are encrypted with it — so a cluster encrypting with a
key held nowhere else has traded disclosure for total loss (ADR-076
`secret-encryption-key`).

`soloz encryption enable` refuses outright without an escrow rather than generating a
key with nowhere to put it, so this check is a courtesy, not a gate:

```bash
# All four or none. A partial set produces a client that authenticates and fails.
env | grep -c '^INFISICAL_ESCROW_' ; echo "expect 4"

# What the escrow already holds for this cluster. Empty is fine on a first run —
# the key is generated and escrowed. A value means a previous cluster used it, and
# that is the value the new control plane must use.
soloz escrow --help >/dev/null && echo "escrow CLI available"
```

Then confirm the cluster you are about to change is the one you mean, because this
rolls its control plane:

```bash
kubectl --kubeconfig <path> config current-context
kubectl --kubeconfig <path> get kubeadmcontrolplane -A
```

Do not continue on a cluster mid-rollout, or whose control-plane replicas are not all
Ready. A roll started on top of a roll leaves members disagreeing about the write
provider, and a Secret written by one may be unreadable by another.

## 1. Adopt the control-plane template

**Not a sync.** Neither ClusterClass is reconciled by anything: both are applied from
the CLI's embedded assets at Day-0, so a release puts the new template on no running
cluster. An earlier version of this step said to sync the provider base, and nothing
syncs it.

```bash
# a workload cluster FIRST — see "Which cluster first" below
soloz encryption enable --cluster <cell> \
  --class manifests/providers/hetzner/base/spokepool-clusterclass-v1.yaml \
  --kubeconfig <path> --dry-run

soloz encryption enable --cluster <cell> \
  --class manifests/providers/hetzner/base/spokepool-clusterclass-v1.yaml \
  --kubeconfig <path>
```

It restores the key from the escrow or generates and escrows one, applies the provider
configuration Secret, then applies the ClusterClass. The Secret goes first, because a
control plane whose Secret does not exist cannot start and the template is what makes a
node ask for it.

For the management cluster, omit `--class` — it defaults to that cluster's.

## Which cluster first

**A workload cluster, not the management cluster.**

If rolling a workload cluster's control plane goes wrong, tenant workloads are affected
and the management plane — CAPI, ArgoCD, the secret store, the identity provider — stays
up to repair it. Rolling the management cluster first risks losing the thing that would
repair it.

It also proves the whole chain once on a cluster where being wrong is recoverable: key
escrowed, Secret assembled, file on the node, provider active, encrypted prefix in etcd.
Do the management cluster second, with the procedure already exercised.

## 2. Roll the control plane

An API server flag takes effect only on a process start, so each control-plane node
is replaced. CAPI does this when the template changes; it is not instant and it is
not free.

```bash
kubectl -n platform-capi get kubeadmcontrolplane -w
```

Wait for `UPDATED` to equal `REPLICAS` and all nodes `Ready`. Do not proceed while
a node is mid-replacement: the cluster then has members disagreeing about the write
provider, and a Secret written by one may be unreadable by another.

## 3. Verify NEW Secrets are encrypted

The check that matters is the stored form, read from etcd. **An API read shows
plaintext either way** — the API server decrypts on the way out — so `kubectl get
secret` proves nothing here.

```bash
kubectl -n default create secret generic enc-probe --from-literal=k=v

kubectl -n kube-system exec etcd-$(kubectl get nodes \
  -l node-role.kubernetes.io/control-plane -o jsonpath='{.items[0].metadata.name}') -- \
  sh -c 'ETCDCTL_API=3 etcdctl \
    --cacert /etc/kubernetes/pki/etcd/ca.crt \
    --cert /etc/kubernetes/pki/etcd/server.crt \
    --key /etc/kubernetes/pki/etcd/server.key \
    get /registry/secrets/default/enc-probe' | hexdump -C | head -3
```

Expect the value to begin `k8s:enc:secretbox:v1:key1:`. If it begins `k8s` followed
by readable YAML, the provider is not in force — **stop and fix step 2** rather than
proceeding, because step 4 would rewrite every Secret into plaintext again.

```bash
kubectl -n default delete secret enc-probe
```

## 4. Rewrite every existing Secret

This is what actually encrypts the data already there. Each Secret is read and
written back unchanged, so the write goes through `secretbox`.

```bash
kubectl get secrets --all-namespaces -o json \
  | kubectl replace -f -
```

Per namespace instead, when the fleet is large enough that one transaction is
unwise:

```bash
for ns in $(kubectl get ns -o jsonpath='{.items[*].metadata.name}'); do
  echo "== $ns"
  kubectl -n "$ns" get secrets -o json | kubectl replace -f - || echo "FAILED: $ns"
done
```

**Service account token Secrets and Helm release Secrets are included and should
be** — they hold credentials and release data like anything else. A `replace` that
fails on an immutable Secret is reported and skipped; note it, because an immutable
Secret cannot be rewritten this way and must be recreated to become encrypted.

## 5. Verify EVERY Secret is encrypted

Not a sample. The point of the rewrite is that nothing is left behind, and a sample
cannot show that.

```bash
NODE=$(kubectl get nodes -l node-role.kubernetes.io/control-plane \
  -o jsonpath='{.items[0].metadata.name}')
kubectl -n kube-system exec etcd-$NODE -- \
  sh -c 'ETCDCTL_API=3 etcdctl \
    --cacert /etc/kubernetes/pki/etcd/ca.crt \
    --cert /etc/kubernetes/pki/etcd/server.crt \
    --key /etc/kubernetes/pki/etcd/server.key \
    get /registry/secrets --prefix --keys-only' | grep -c .
```

then, for the count of ENCRYPTED ones:

```bash
kubectl -n kube-system exec etcd-$NODE -- \
  sh -c 'ETCDCTL_API=3 etcdctl ... get /registry/secrets --prefix' \
  | grep -c 'k8s:enc:secretbox:v1:key1:'
```

The two counts must match. A difference is the number of Secrets still in plaintext,
and each one is a credential the audit finding still covers.

## 6. Remove the `identity` fallback

Only now. While `identity` is in the list, a plaintext Secret is still readable, so
the control is partial and an incomplete step 4 is invisible.

Remove the `identity` entry from the provider configuration in
`secret-encryption-config-es.yaml`, sync, and roll the control plane again (step 2).

Then re-run step 5. It must still match — **if any Secret was missed, it is now
unreadable**, which is why step 5 is run twice and why the fallback is removed last.

## 7. Record it

The finding is closed for this cluster when: the provider is active, step 5's counts
match, `identity` is gone, and step 5 passed again afterwards. Note the date against
the cluster; a cluster rebuilt from an older template is not covered.

## The management cluster

Step 1 differs for it; steps 2 to 7 are the same.

It is created from a temporary bootstrap cluster and CAPI is then pivoted onto it, so
it ends up managing itself. That gives three cases, and only the last needs anything
special:

| | where the provider configuration is read from | circular? |
|---|---|---|
| first build | the bootstrap cluster | no — different cluster |
| node replacement | its own etcd, through a live API server that already holds the file on disk | no |
| full rebuild | a new bootstrap cluster, with no previous cluster to read from | **the key must come from the escrow** |

An earlier version of this runbook claimed the management cluster could not use this
mechanism at all, on the reasoning that its configuration would live in the etcd it
encrypts. That is wrong for the first two cases: the file is on disk once written, and
a replacement node is provisioned by a control plane that is already running.

The third case is the one that matters, and it is why the key is escrowed
(ADR-076 `secret-encryption-key`). On a rebuild the CLI restores it rather than
minting a new one — a new key against a restored etcd would leave every Secret in it
undecryptable, with the loss happening during the recovery.

Steps 3 to 7 apply unchanged, and the rewrite there is the more consequential of the
two clusters: the management cluster holds the secret store's own credentials, the
identity provider's, and every tenant's provisioning material.

## What this does not protect against

`secretbox` keeps the key on the control-plane host, in a file the API server reads.
It protects etcd data, etcd backups and disk snapshots. It does **not** protect
against compromise of a control-plane node — that is what KMS v2 with an external
key is for, and ADR-003 §6 records what adopting it would still have to decide.
