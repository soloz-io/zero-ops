# Attribution and divergence

This plugin's behaviour is derived from Google's Kubernetes KMS plugin for Cloud KMS.

| | |
|---|---|
| Upstream | https://github.com/GoogleCloudPlatform/k8s-cloudkms-plugin |
| Package | `plugin/v2` |
| Divergence point | `a88bafe6cfcc8727e6a7243dfa16f978b1c9a468` (2026-05-14) |
| Licence | Apache License 2.0 — `reference-projects/k8s-cloudkms-plugin/LICENSE` |
| Copyright | Copyright 2018 Google LLC |

Upstream ships no `NOTICE` file, so Apache-2.0 §4(d) imposes nothing here. This file
exists because the provenance is worth recording regardless of what the licence
compels, and because a fork that does not say where it forked from cannot be reviewed
against the thing it forked from.

## What is taken, and what is not

**No upstream source file is copied verbatim.** Taking `plugin/v2` as files was
considered and rejected for two reasons, both recorded in `internal/service/service.go`:
it vendors its own generated copy of the KMS v2 proto (`plugin/v2/api.pb.go`) where this
module deliberately pins `k8s.io/kms` to the version the control plane runs, and it is
built on the REST client `google.golang.org/api/cloudkms/v1`, whose base64-string
interface is the reason upstream has base64 handling at all.

What is taken is **behaviour**, in three places where upstream had already solved
something this plugin had got wrong:

- **the cached `key_id`.** Upstream keeps the last known identifier so `Status` stays
  answerable during a Cloud KMS outage — "KeyId in StatusResponse cannot be empty and
  shouldn't trigger key migration in case of transient remote service unavailability"
  (`plugin/v2/plugin.go`). An earlier version of this plugin surfaced the error instead,
  which the API server would have read as a key change.
- **the operator-supplied `key_id` suffix** (`--key-suffix`, `setKeyID`). This is
  upstream's answer to the no-reuse rule, and it is the right one: the discriminator is
  supplied at rotation time rather than remembered in a process that does not outlive a
  node.
- **probing by encrypting.** Upstream's `Status` encrypts a fixed `"ping"` plaintext and
  reads `EncryptResponse.Name` rather than reading `CryptoKey.Primary`. This plugin used
  `GetCryptoKey` and was wrong to: that needs `cloudkms.cryptoKeys.get`, which
  `roles/cloudkms.cryptoKeyEncrypterDecrypter` does not carry, so the IAM policy the ADR
  described would not have worked. Upstream's way needs no permission beyond encrypt —
  and the version then comes from the crypto path, which cannot be ahead of itself the
  way an eventually-consistent metadata read can.
- **decryption addressed to the parent key** (`extractKeyName`), which is what lets data
  written before any number of rotations keep decrypting.

One literal fragment is adapted: the resource-name regex in
`internal/keystore/gcpkms.go` (`keyFromVersionName`) follows upstream's
`keyResourceRegEx`.

## What diverges

Seven deltas. Each is a defect or a gap in upstream for this platform's purposes, and
each is commented at the point where it is implemented.

1. **Identity.** Upstream obtains credentials from `google.DefaultClient` and the GCE
   metadata server (`plugin/http_client.go`). These nodes are Hetzner; there is no
   metadata server. Application Default Credentials, with a Workload Identity Federation
   credential configuration. — `internal/keystore/gcpkms.go`
2. **Exact-version encryption.** Upstream encrypts against the parent `CryptoKey` and
   reports whatever version came back. `EncryptRequest.Name` accepts a `CryptoKeyVersion`
   too, and naming it is required: changing a key's primary version is eventually
   consistent, so against the parent the status path and the encrypt path can resolve
   different versions — and the API server requires those two `key_id`s to be equal
   (`k8s.io/apiserver` `encryptionconfig/config.go:424`), marking the provider unhealthy
   otherwise. — `internal/keystore/gcpkms.go`, `internal/service/service.go`
3. **Integrity.** Upstream performs no CRC32C verification on any of the four paths. A
   silently corrupted wrapped DEK is unrecoverable — the write succeeds and the Secret
   can never be decrypted. — `internal/keystore/gcpkms.go`
4. **`Status` freshness.** Upstream reads its cached `key_id`, *then* probes, *then*
   updates the cache, so its first `Status` after a rotation disagrees with `Encrypt` and
   trips the equality check in (2) for a poll interval on every rotation. The observation
   is installed before the answer is composed. — `internal/service/service.go`
5. **Protocol version.** Upstream advertises `v2beta1`; this advertises `v2`. Both are
   accepted (`k8s.io/kms@v0.31.6` `apis/v2/api.proto:37`, validator at
   `encryptionconfig/config.go:496`) and `v2` is the recommended string.
6. **Generated types.** `k8s.io/kms` pinned to the control plane's version rather than a
   vendored proto copy. See `go.mod`.
7. **Image and runtime.** Upstream's `Dockerfile` is `FROM alpine:latest` with no `USER`.
   This image is digest-pinned and non-root, and the static pod is Guaranteed QoS with
   `drop: ALL` and leaf-only `hostPath` mounts. — `Dockerfile`, `deploy/static-pod.yaml`

`scripts/validate/preflight/100-kms-identifier-is-deterministic.py` asserts (2), (4) and
the parent-key decryption structurally, because each of them regresses silently and only
shows up after a rotation.

## What was removed from this plugin in the course of forking

- `internal/keyid` — composed an identifier from the cluster name, key name and version
  number. The `CryptoKeyVersion` resource name already carries project, location, key
  ring, key and version, so the composition restated GCP and added a second thing that
  could be wrong.
- `internal/active` — enforced ADR-100's no-reactivation rule in process memory, which
  meant it lapsed on every restart and every control-plane node replacement: the case it
  existed for (`v1 → v2 → restart → v1`) was the case it could not catch. Deriving the
  rule from GCP instead was considered and is unsafe for an unrelated reason, recorded in
  `internal/keystore/gcpkms.go`. ADR-100 records where the invariant lives now.
