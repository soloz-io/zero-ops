# ADR-105: Encryption of Kubernetes Secrets at rest

**Date:** 2026-10-15
**Status:** Accepted

## Context

Kubernetes Secrets are stored in etcd. Without an encryption provider configured on the API
server they are stored base64-encoded and not encrypted, so possession of an etcd snapshot,
a disk image or a backup yields their contents.

ADR-003 §6 records the control that closes this and has been in force since 2026-10-02. The
mechanism it describes is deployed on every cluster this platform operates. No ADR carried it
as its subject: §6 sits inside the secret-lifecycle pattern because at-rest encryption was
framed there as protection for the delivery cache, and the only ADR titled for secrets at
rest proposed a different mechanism and is halted. This ADR records what is deployed, so the
live control has a subject of its own.

ADR-100 proposed holding the key outside the cluster in an external key-management service.
It is halted. A key-management provider must answer before the API server will serve; a
component that precedes the API server cannot hold a Kubernetes service-account token or
reference a Secret, because the authority that issues them is the API server. No supported
identity mechanism exists for that position. The consequence for this ADR is that the
mechanism recorded here is the platform's mechanism and not an interim state.

ADR-104 records that node bootstrap material is readable by any workload on this
infrastructure provider, and that the key described here is delivered through that channel.
That exposure is the subject of ADR-104 and bounds the protection this ADR provides.

## Decision

**Kubernetes Secrets are encrypted at rest with `secretbox`, under a key that is unique per
cluster.**

The encryption configuration lists `secretbox` first, so every write is encrypted, and
retains `identity` second, so Secrets written before the provider was active remain readable.
Only the `secrets` resource is listed. ConfigMaps and all other resources in etcd are stored
as they were, which is a property worth stating because "etcd is encrypted" is a common and
incorrect reading of this control.

The key is held as a per-cluster Kubernetes Secret on the management cluster and delivered to
control-plane nodes as a file through the cluster class, owned by root and readable only by
root. The API server is configured to read it from that path.

The key is escrowed (ADR-076) because it cannot be regenerated: a cluster whose key is lost
cannot read its own Secrets. This is the opposite of the position ADR-100 reasoned toward for
an externally held key, where a copy outside the key store would itself be the exposure. The
distinction is which failure dominates — loss for a key held locally, copy for a key held
remotely.

**What this control protects, stated precisely.** An attacker holding an etcd snapshot, a
disk image or a backup, and not also holding the encryption configuration, cannot read
Secrets. That is the threat this addresses.

**What it does not protect against.** An attacker with the encryption configuration can
decrypt any snapshot or backup taken while that key was in force, offline, permanently,
leaving no record. On this infrastructure provider the configuration is obtainable without
host compromise, because it is delivered through node bootstrap material and that material is
re-served to any workload — see ADR-104. The protection is therefore real against the backup
and disk-theft case and substantially weaker against an attacker with any cluster foothold,
until ADR-104 is satisfied.

Rotation replaces the key in two verified stages and requires a control-plane roll, because
the configuration is a file delivered at provisioning. A procedure exists in the command-line
tool. A two-key configuration template, which would allow a new key to be added before the
old one is withdrawn, does not exist; it was deferred while ADR-100 was expected to replace
the mechanism, and that reason has lapsed. Until it exists, rotation is a sequenced operation
rather than an overlap, and this is recorded as a gap rather than as a completed control.

## Ownership

Reference ADR-039 (Platform Ownership Model).

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| At-rest encryption key | Infisical (escrow, ADR-076) | CLI | none | API server | Day-0 |
| Per-cluster encryption configuration Secret | Git (cluster class) | CLI | CAPI | API server | Day-0 |
| Encryption configuration file on the node | Git (cluster class) | CAPI | CABPK | API server | Day-0 |

The key has no reconciler. Its lifecycle is Day-0 and its replacement is an operator-initiated
procedure, not a controller loop.

## Consequences

### Positive

- Secrets in an etcd snapshot, disk image or backup are not readable without the encryption
  configuration, which is the case encryption at rest exists to address.
- The key is unique per cluster, so exposure of one cluster's key does not extend to another.
- The key is escrowed, so cluster recovery does not depend on a single copy.
- The mechanism requires no component outside the cluster and no identity bridge, which is
  what makes it available where ADR-100's approach is not.

### Negative

- The key is delivered through node bootstrap material, which on this provider is readable by
  any workload. The protection is bounded by ADR-104 and is weaker than the mechanism itself
  implies.
- An exposed key permits offline, permanent and unauditable decryption of every snapshot taken
  while it was in force. Unlike a credential held in an external key service, it cannot be
  revoked.
- Rotation requires a control-plane roll and has no overlap window, so a key cannot be
  replaced without a sequenced operation.
- Only the `secrets` resource is encrypted. Material placed in a ConfigMap is not protected,
  and nothing enforces that distinction.

## Impact

- **ADR-003 §6** remains the detailed control. Its position statement is corrected to name
  `secretbox` as the mechanism rather than as a fallback, and its references to ADR-100 are
  corrected to record that ADR as halted. This ADR is its subject-level record; §6 is not
  superseded.
- **ADR-100** is halted and does not supersede this. Its amendment to ADR-003 §6 is withdrawn.
- **ADR-104** bounds the protection recorded here and is the work that would strengthen it.
- **ADR-076** covers escrow of the key.
- No ADR is superseded by this record.

## References

- ADR-003: ESO and Infisical pattern (§6, Protection at rest)
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-043: Control Plane Authority Model
- ADR-076: Escrow of unregenerable platform secrets
- ADR-100: The key that encrypts etcd lives outside the box (halted)
- ADR-104: No key material in node bootstrap material
