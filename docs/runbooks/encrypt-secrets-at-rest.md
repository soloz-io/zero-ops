# Runbook: encrypting Kubernetes Secrets at rest

**Applies to:** ADR-003 §6 (Protection at rest), ADR-076 (`secret-encryption-key`)
**Status:** required once per cluster — the management cluster and every workload
cluster. A cluster is not covered until step 6 has passed on it.
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

The key is generated once per box by hub-operator and escrowed under ADR-076's
`secret-encryption-key`, because etcd **and every etcd backup** are encrypted with
it. Confirm it is escrowed before proceeding: a box that encrypts etcd with a key
that exists nowhere else has traded disclosure for total loss.

```bash
# The key exists in the store, and the escrow holds a copy.
kubectl -n platform-capi get externalsecret secret-encryption-config \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'; echo
kubectl -n platform-capi get secret secret-encryption-config \
  -o jsonpath='{.data.enc\.yaml}' | base64 -d | grep -c secretbox
```

Expect `True` and `1`. If the ExternalSecret is not Ready, **stop** — the next
control-plane node to be created would fail to bootstrap on a missing file.

## 1. Adopt the control-plane template

The provider is an API server argument plus a file on every control-plane node, so
it arrives with `spokepool-control-plane-v2`. Sync the provider base.

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

## What this does not protect against

`secretbox` keeps the key on the control-plane host, in a file the API server reads.
It protects etcd data, etcd backups and disk snapshots. It does **not** protect
against compromise of a control-plane node — that is what KMS v2 with an external
key is for, and ADR-003 §6 records what adopting it would still have to decide.
