# Runbook: Gate 1 — KMS v2 against a real key store

**Applies to:** ADR-100 (KMS v2 for Secrets at rest), its Completion gates
**Status:** the first of three gates. Until all three pass, `secretbox` remains the
provider in force on every cluster (ADR-003 §6).

**This does not touch `nutgraf-hub` or `nutgraf-01`.** Both are encrypted and verified
under `secretbox`, and neither is where a provider that has never served a real
request gets exercised.

## What this gate is for

The plugin's unit tests establish that it is internally consistent. They cannot
establish that the Kubernetes encryption architecture works, because every one of the
following depends on a real API server, a real key store, or both:

| | what only a real run proves |
|---|---|
| the API server starts with the provider configured | the socket path, the static-pod ordering, and the `EncryptionConfiguration` shape are right |
| a Secret writes and reads | the wrap and unwrap paths work against the real store |
| etcd holds `k8s:enc:kms:v2:` | the KMS provider is in force — not `secretbox`, not `identity` |
| the key rotates in the store | the premise the whole ADR rests on |
| the plugin reports a NEW `key_id` | version-qualified identifiers do what the vendor's cannot. Asserted from the plugin's own log in step 8, not inferred from reads still working |
| the API server ACCEPTS that identifier | it is well-formed to the consumer, not only to us |
| pre-rotation ciphertext still decrypts | retained previous versions, via the self-describing blob |
| post-rotation writes use the new state | the rotation achieved something |
| **the API server restarts and reads** | **the DEK-cache case — see below** |
| the plugin restarts | no local state was load-bearing |
| the key store becomes unreachable | the documented behaviour, not an accident. **Corrected 2026-10-05:** KMS v2 writes are not guaranteed to fail during an outage — writes may continue while the API server has the required DEK material cached. Test the COLD read instead, by restarting the API server during the outage, which is where unavailable material is actually required |
| the key store returns | recovery without operator action |
| a full Secret rewrite completes | the migration ADR-100 calls part of adoption |

### The restart row is the one that must not be skipped

The API server caches a data encryption key after using it. So a plugin that returns
the wrong bytes from `Unwrap` **passes a write-then-read test**: the write is correct,
and the read is served from cache. It fails only once the cache is cold.

That is not hypothetical. During implementation, `Unwrap` nearly shipped casting the
API's response string to bytes — and the API returns the plaintext **base64-encoded**,
so it would have handed the API server 44 bytes of text where a 32-byte AES-GCM key
belongs. Write: fine. Warm read: fine. After a restart: Secrets that were readable an
hour earlier stop decrypting, and it looks as though the restart broke the cluster.

A unit test now pins the byte contract. This gate is what proves it end to end.

## The environment

A **disposable, production-shaped** control plane. Not a toy, and not a spoke.

```
kind cluster (disposable)
      │
      ├── kube-apiserver  (static pod, kubeadm-managed)
      │        │  --encryption-provider-config
      │        │  unix socket, hostPath-mounted
      │        ▼
      └── kms-plugin      (STATIC POD, /etc/kubernetes/manifests)
               │  credential: a file on the node, not a Secret
               │  HTTPS
               ▼
         Infisical Cloud KMS
               ├── dedicated test key
               └── dedicated machine identity, scoped to that key alone
```

### Why kind, and what that buys

`kind` is kubeadm-based, so three things are *identical* to a real node rather than
analogous:

- **Static pods** are read from `/etc/kubernetes/manifests` by kubelet with no API
  server involved. That is the only ordering that terminates: the API server cannot
  decrypt etcd until the plugin answers, so a plugin the API server must schedule is a
  deadlock.
- **`hostPath` mounts** into the API server's static pod behave the same way, including
  the `DirectoryOrCreate` behaviour that `secretbox` already needed.
- **`kubeadmConfigPatches`** in the kind config take a `ClusterConfiguration` with
  `apiServer.extraArgs` and `apiServer.extraVolumes` — **the same stanza, with the same
  field names, that `KubeadmControlPlaneTemplate` carries.** So the configuration shape
  this gate exercises is the shape v5 will ship, not a translation of it.

### What this does NOT prove, stated so it is not assumed

**CAPI delivery.** kind is not provisioned by CAPI, so this gate does not exercise the
ClusterClass path that puts these files on a node.

That residual risk is small and already partly evidenced: the v3/v4 rollout proved
CAPI renders a `files` entry with `contentFrom.secret` to an exact path with an exact
per-cluster Secret name, observed on replaced nodes on both clusters. A static-pod
manifest at `/etc/kubernetes/manifests/kms-plugin.yaml` and a credential file arrive by
that identical mechanism — they are more files, not a new mechanism.

What is genuinely new in v5 is covered by gate 3, not here.

## Isolation requirements

Non-negotiable, because this is a first exercise of a provider against a live key
store:

- **A dedicated KMS key**, created for this gate and used by nothing else. It will be
  rotated repeatedly and may be destroyed.
- **A dedicated machine identity**, scoped to that key alone: no ordinary secret paths,
  no project-wide read. If it can read anything but that one key, the scope is wrong.
- **No production tenant or application secrets** on this cluster, at any point. The
  Secrets written here are probes.
- **Revoke the identity at teardown.** A disposable cluster leaves a non-disposable
  credential behind otherwise.

## The credential bootstrap, which is the architectural question this gate settles

ADR-100 decides the plugin's credential is node-level material living outside etcd,
because the plugin must authenticate *before* the API server can read a Secret. What
it does not decide is how the file arrives — and that is the same three-case problem
the encryption key already has:

| | where the credential comes from | circular? |
|---|---|---|
| a workload cluster | a Secret in the **hub's** `platform-capi`, rendered to a node file by CAPI | no — a different cluster's etcd |
| the hub, node replacement | the same, rendered by the hub's own API server, which is already running | no |
| the hub, full rebuild | there is no hub; it must come from the bootstrap cluster or the escrow | **yes, unless sourced outside** |

The third case is the one that matters, and it is exactly why `secret-encryption-key`
is escrowed. **Gate 1 must therefore run with the credential arriving as a plain file
on the node**, in the location and with the permissions v5 will use — not as a
Kubernetes Secret, and not from an environment variable set by hand. Proving it with a
`kubectl`-created Secret would prove a mechanism v5 cannot use.

Whether that credential joins the escrow is a question for gate 3 and ADR-076's
membership test — *can the value be regenerated without loss?* A machine identity's
secret can be reissued, so it probably does **not** belong in the escrow. Recording the
question rather than settling it here.

## Sequence

1. **Create the key and identity** in Infisical. Record the key's UUID and the
   identity's client id. Do not record the client secret anywhere but the credential
   file.
2. **Build and load the plugin image** into the disposable cluster, by digest.
3. **Write the node files**: the credential file, the plugin's static-pod manifest, and
   the `EncryptionConfiguration` naming the KMS provider first and `identity` second.
4. **Start the cluster** with the `kubeadmConfigPatches` carrying
   `encryption-provider-config` and the socket `extraVolume`.
5. **Walk the table above**, in order, recording the `key_id` at each step and the etcd
   prefix at each write.
6. **Tear down**: delete the cluster, revoke the identity, delete the key.

### Verify the prefix and the identifier, not just that reads work

A successful `kubectl get secret` proves nothing about which provider served it — the
API server decrypts on the way out either way, and `identity` is still second in the
list. Two things must be read directly:

- the stored value begins **`k8s:enc:kms:v2:`**, read from etcd on the node;
- the plugin's reported `key_id` **changes across a rotation and not otherwise**,
  observed at `Status` rather than inferred from behaviour.

The `secretbox` runbook's step 8 loop is the model for the first, with the prefix
changed and **its trailing-slash caveat intact**: `/registry/secrets/` without the
slash also matches unrelated keys.

## Load behaviour, which is not a correctness question

The API server **polls `Status` continuously** and may issue a large number of decrypt
calls at startup, when every cached DEK is cold. Against a cloud key store that is
rate limiting and latency, not just correctness:

- record `Status` poll frequency and the latency the plugin adds;
- restart the API server with a few hundred Secrets present and watch for throttling;
- note that this plugin answers `Status` from its cached snapshot and makes **no**
  network call per poll, unlike the vendor plugin, which performs a real encrypt on
  every `Status`. That difference is the reason it is worth measuring rather than
  assuming — and it has a cost: a cached snapshot can report healthy while the key
  store is unreachable. Whether `Status` should go unhealthy on a stale snapshot is a
  decision this gate should inform.

## Exit criteria

Every row of the table observed, with the prefix and `key_id` evidence recorded, and
the outage and recovery rows showing the behaviour ADR-100 specifies rather than
whatever happened.

Then gate 2 (the production image) and gate 3 (rotation, outage and recovery at
scale). **The v5 ClusterClass is written after both**, because a template pointing a
control plane at an unproven provider is a cluster that does not start — on the two
clusters this platform is operated from.
