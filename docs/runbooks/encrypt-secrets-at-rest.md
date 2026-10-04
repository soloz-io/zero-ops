# Runbook: encrypting Kubernetes Secrets at rest

**Applies to:** ADR-003 §6 (Protection at rest), ADR-076 (`secret-encryption-key`)
**Status:** required once per cluster. A cluster is not covered until step 9 has
passed on it.

**IT IS TWO ROLLS, NOT ONE, AND TWO RELEASES.** The control-plane template is
adopted in two phases: **v3** puts the key file on every control-plane node and
nothing reads it, then **v4** adds the API server argument that does. The reason is
in "Why it is two phases" below. Budget for both before starting; stopping after v3
leaves a cluster that is not encrypted and does not claim to be.

**This document covers WORKLOAD CLUSTERS only.** The management cluster is encrypted
by a different mechanism and is not reachable from here — see "The management cluster"
below before assuming the fleet is covered.
**ROTATION IS NOT YET SUPPORTED, and this document is not it.** An earlier version
said rotating was "this document again from step 4". It is not, and following that
would make every existing Secret unreadable.

Rotating `secretbox` needs TWO keys in the provider list at once — the new one first
so writes use it, the old one second so everything already written still reads —
then a roll, then a rewrite of every Secret, then removal of the old key and another
roll. `secret-encryption-config.yaml` renders exactly one key (`key1`), so there is
no configuration in which both are present, and replacing the single key is
precisely the "new key against existing data" failure the escrow exists to prevent.

Closing it means a two-key template and a procedure of its own. ADR-100 (KMS v2)
changes the mechanism anyway, which is why this has not been built: under KMS the
key identifier is a version and rotation is the plugin's concern, not a file's.
Until one of those lands, **the key for a cluster is set once** and a new key means
a new cluster.

## Why enabling it is not finishing it

Encryption applies to subsequent writes. Turning the provider on leaves every
existing Secret stored exactly as it is now — which is the failure this runbook
exists to prevent: a control enabled, reported as complete, and protecting nothing
already written.

The provider list is ordered. `secretbox` is first so every WRITE is encrypted;
`identity` is second so every existing plaintext READ still succeeds. That ordering
is what makes step 6 safe and what makes step 9 necessary — while `identity`
remains, a plaintext Secret is still readable and the control is partial.

## Why it is two phases

`encryption-provider-config` is what makes the API server REQUIRE the file. A path
it cannot read is a control plane that does not start — and on a workload cluster
that is a cluster nobody can log in to repair, while on the management cluster it is
the thing every other cluster is repaired from.

The file arrives through a CAPI ClusterClass patch that renders its Secret name from
`{{ .builtin.cluster.name }}`. That render cannot be verified from a laptop: it
happens inside CAPI at topology-reconcile time, and the only honest proof is a real
node. Shipping the argument and the file together makes the first evidence of a
correct render "the API server came up" — or did not.

So v3 delivers the mount and the file and nothing that reads them. A node that comes
up with the file missing or misnamed is an ordinary healthy node, and step 3 is `ls`
on it. v4 adds one line, after that evidence exists.

Each phase is a template SPEC change, so each needs a new template NAME and each
rolls the control plane. Each also needs a release: `soloz encryption enable` applies
the ClusterClass from the CLI's own embedded assets, so the CLI binary has to carry
the phase you are applying.

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

## 1. Adopt the control-plane template (phase 1, v3)

**Not a sync.** Neither ClusterClass is reconciled by anything: both are applied from
the CLI's embedded assets at Day-0, so a release puts the new template on no running
cluster. An earlier version of this step said to sync the provider base, and nothing
syncs it.

Confirm first that the CLI you are about to run carries v3 and not the argument:

```bash
soloz encryption enable --cluster <cell> \
  --class manifests/providers/hetzner/base/spokepool-clusterclass-v1.yaml \
  --kubeconfig <path> --dry-run | grep -E 'control-plane-v|encryption-provider-config'
```

Expect `spokepool-control-plane-v3` and **no** `encryption-provider-config`. If the
argument is there, this CLI carries v4 and you are skipping the gate that makes the
roll safe.

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

**The Secret is named `<cluster>-encryption-config`, one per cluster.** It was briefly
one flat `secret-encryption-config` in `platform-capi` for every cluster of a class,
which is one key for all of them — and the first cluster to rotate would have left the
others' etcd undecryptable by a key they still believed in. The name is built by
`assets.EncryptionSecretName` on the create side and by the ClusterClass's
`secretEncryptionConfig` patch on the read side; preflight 88 pins the two together,
because a disagreement is not reported as a missing Secret, it is a node that never
finishes bootstrapping.

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

## 2. Roll the control plane (first roll)

An API server flag takes effect only on a process start, so each control-plane node
is replaced. CAPI does this when the template changes; it is not instant and it is
not free.

**On a spoke, check `providerID` before trusting a stall.** CAPI matches Machines to
Nodes by `.spec.providerID` and nothing else, so a node that is Ready and serving
traffic but carries none is, to CAPI, a Machine that never joined: empty `NODENAME`,
and a roll that waits reporting `NodeHealthy=False/NodeProvisioning` — which names a
provisioning step rather than a missing field. The spoke control-plane template
omitted the injection block until v3 for exactly this reason. Preflight 87 now fails
a template that ships `dynamic-node-ip.sh` without it.

```bash
kubectl -n platform-capi get kubeadmcontrolplane -w
```

Wait for `UPDATED` to equal `REPLICAS` and all nodes `Ready`. Do not proceed while
a node is mid-replacement: the cluster then has members disagreeing about the write
provider, and a Secret written by one may be unreadable by another.

## 3. Verify the file landed, and is the right file

**This is the gate the two-phase split exists to create.** Nothing reads the file
yet, so being wrong here costs nothing — which is the only moment at which that is
true.

```bash
NODE=$(kubectl get nodes -l node-role.kubernetes.io/control-plane \
  -o jsonpath='{.items[0].metadata.name}')

# The file exists, is root-owned and 0600.
kubectl debug node/$NODE -it --image=busybox -- \
  ls -l /host/etc/kubernetes/enc/enc.yaml

# It is THIS cluster's key, and the provider order is secretbox then identity.
kubectl debug node/$NODE -it --image=busybox -- \
  grep -E 'secretbox|identity|name:' /host/etc/kubernetes/enc/enc.yaml
```

Also confirm the Secret CAPI read it from is named for this cluster:

```bash
kubectl -n platform-capi get secret <cluster>-encryption-config
```

If the file is absent, the patch did not render — **stop here**. The cluster is
healthy and unencrypted, which is the state v3 is designed to fail into. A missing
`<cluster>-encryption-config` Secret with the file absent means the name disagreed;
a present Secret with the file absent means the patch did not apply to the
control-plane template.

Do not proceed to v4 until this passes on a replaced node. A node that predates the
roll may hold a file written by an earlier procedure, so check one that CAPI
actually replaced.

## 4. Adopt the argument (phase 2, v4)

One line, and it is the line that can break the cluster:

```yaml
apiServer:
  extraArgs:
    encryption-provider-config: /etc/kubernetes/enc/enc.yaml
```

added to the control-plane template, under a **new template name** (`-v4`), because
the spec of a template a Cluster references is immutable or effectively so — an edit
the webhook rejects leaves ArgoCD wedged on a resource it can neither apply nor drop.
Then release, so the CLI embeds it, and run `soloz encryption enable` again exactly
as in step 1.

`cmd/soloz/encryption_test.go` asserts the argument is ABSENT. That assertion is the
recorded phase boundary: updating it is part of this step, deliberately, so the
change cannot happen by accident.

## 5. Roll the control plane (second roll)

As step 2. After this the provider is in force.

If the API server does not come up, the file is what to look at first — step 3 proved
it was there under v3, so a failure here is the argument's path or permissions, not
the render.

## 6. Verify NEW Secrets are encrypted

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
by readable YAML, the provider is not in force — **stop and fix step 5** rather than
proceeding, because step 7 would rewrite every Secret into plaintext again.

```bash
kubectl -n default delete secret enc-probe
```

## 7. Rewrite every existing Secret

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

## 8. Verify EVERY Secret is encrypted

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

## 9. Remove the `identity` fallback

Only now. While `identity` is in the list, a plaintext Secret is still readable, so
the control is partial and an incomplete step 7 is invisible.

Remove the `identity` entry from the provider configuration in
the provider configuration and re-apply it, then roll the control plane again
(step 5). That is a THIRD roll, and it is not optional.

The configuration is written by `soloz encryption enable` from
`internal/assets/manifests/secrets/secret-encryption-config.yaml`, so removing the
plaintext provider is an edit to that template followed by another `encryption
enable`. Nothing reconciles the Secret, which is why this is an explicit step and
not something that happens on a sync.

Then re-run step 8. It must still match — **if any Secret was missed, it is now
unreadable**, which is why step 8 is run twice and why the fallback is removed last.

## 10. Record it

The finding is closed for this cluster when: the provider is active, step 8's counts
match, `identity` is gone, and step 8 passed again afterwards. Note the date against
the cluster; a cluster rebuilt from an older template is not covered.

## The management cluster

Step 1 differs for it; steps 2 to 10 are the same, both rolls included.

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

Steps 3 to 10 apply unchanged, and the rewrite there is the more consequential of the
two clusters: the management cluster holds the secret store's own credentials, the
identity provider's, and every tenant's provisioning material.

## What this does not protect against

`secretbox` keeps the key on the control-plane host, in a file the API server reads.
It protects etcd data, etcd backups and disk snapshots. It does **not** protect
against compromise of a control-plane node — that is what KMS v2 with an external
key is for, and ADR-003 §6 records what adopting it would still have to decide.
