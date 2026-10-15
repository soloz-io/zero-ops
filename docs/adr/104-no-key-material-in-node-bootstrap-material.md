# ADR-104: No key material in node bootstrap material

**Date:** 2026-10-15
**Status:** Proposed

## Context

Node bootstrap material on this platform is readable by any workload. The Hetzner metadata
service re-serves a server's original cloud-init user-data over unauthenticated HTTP at a
link-local address, for the lifetime of the server. Reachability was confirmed on a live
cluster from an ordinary pod with no host privileges and no elevated capabilities: the
service answered, and the response carried the full payload.

The payload contains key material. Verified by digest equality against the corresponding
cluster Secrets on `nutgraf-hub`:

| material in node bootstrap material | what possession grants |
|---|---|
| Kubernetes CA private key | client certificates in `system:masters`; unconstrained cluster-admin independent of RBAC |
| etcd CA private key | direct etcd authentication; all cluster state readable and writable without the API server |
| front-proxy CA private key | request-header identity assertion through the aggregation layer |
| service-account signing key | valid service-account tokens for any namespace |
| secretbox encryption key (ADR-003 §6) | offline decryption of any etcd snapshot or backup, unrevocable and unaudited |
| shared network join credential (ADR-052) | network membership, reusable |

Possession of any one of the first four is equivalent to full cluster compromise.

This is not a misconfigured value. Cluster certificate material is placed in bootstrap data
by the control-plane provider because kubeadm cannot issue certificates without it, and the
admin kubeconfig is derived from the same material. Every cluster on a provider that
re-serves user-data carries this exposure.

Three properties of the exposure bound the remedies available. The payload is served by the
hypervisor, so nothing within the guest can purge, expire or alter it. A copy persists in the
provider's server record and in a per-machine bootstrap data Secret on the management
cluster. And the exposure attaches to the server, so a server that has booted with a given
payload remains exposed for its lifetime, which makes key rotation and server replacement the
same operation.

ADR-100 proposed moving the etcd encryption key outside the cluster. That addresses one row
of the table above and is halted for unrelated reasons. It does not reduce this exposure.

## Decision

**Node bootstrap material must contain no key material.**

The remedy must change what is placed in bootstrap material. Remedies that change the
lifetime or the custody of material still placed there are insufficient, for reasons that
follow from the exposure's properties rather than from preference:

- Rotation does not apply. The exposure is bound to the server for its lifetime, so rotation
  without server replacement leaves the prior payload served.
- Reduced validity does not transfer from ADR-100. A short-lived credential can be made
  worthless by expiry; a certificate authority that is the trust root for an entire cluster
  cannot be short-lived.
- Guest-side removal cannot reach the served copy. On-disk mode and ownership of the local
  copy are not controls over it.

Network-level restriction of access to the metadata address reduces the set of workloads able
to read the payload. It is recorded as containment and does not satisfy this decision: the
host, host-networked workloads, the provider's stored record and the management cluster's
bootstrap data Secret all remain. Containment also introduces an availability dependency,
because node-local components read instance metadata from the same address and port and
cannot be distinguished from the exposing path at the network layer.

**Until this decision is satisfied, no new cluster class that places key material in node
bootstrap material may be used for tenant clusters.**

The direction of a compliant design is not yet decided. Three are under consideration and the
first is preferred because it reduces what is placed in bootstrap material rather than
protecting it:

1. Control-plane certificate material held inside the cluster and referenced from bootstrap
   material by a single-use short-lived key, as kubeadm's certificate upload mechanism
   provides.
2. Selection of a provider whose instance metadata does not re-serve user-data, which makes
   this a provider-selection decision.
3. Retrieval of certificate material over an authenticated channel at first boot, which
   requires a credential in bootstrap material and therefore reduces rather than removes the
   exposure.

**That determination has been made, and option 1 is unavailable.** The kubeadm bootstrap
API exposes no certificate-key or certificate-upload mechanism: the fields that would express
it are absent from the vendored API at Cluster API **v1.13.4**, which is newer than the
v1.8.5 bootstrap provider deployed here. The absence is therefore not resolved by upgrading.
The only mechanism the API provides for placing certificate material on a node is file
content in bootstrap material, sourced from a Secret — the exposed channel itself.

**Option 1 consequently requires an upstream change** and is not available as a platform
decision. The three remaining avenues are recorded below in the order their cost is incurred,
not in order of preference.

1. **Ask whether the infrastructure provider can stop re-serving user-data per server.** The
   cheapest question available and the only one whose positive answer removes the exposure
   outright without changing the platform. Unverified; no such control is documented.
2. **Determine whether the alternative bootstrap provider already deployed here avoids the
   exposure.** Talos bootstrap and control-plane providers are pinned and applied on this
   platform. Talos does not use kubeadm and distributes machine configuration by a different
   mechanism. Whether its control-plane machine configuration places certificate material in
   user-data is unverified and is the next determination to make, because a positive answer
   makes this an in-platform migration rather than a provider change.
3. **Provider selection**, which makes the remedy a commercial and operational decision rather
   than an engineering one.

**If none of the three yields a remedy, the honest position is containment with the exposure
recorded**, not a remedy claimed. Containment closes the path by which ordinary workloads
reach the payload, which is the path that makes this reachable by tenant workloads, and leaves
the host, host-networked workloads, the provider's stored record and the management cluster's
bootstrap data Secret. That is a reduction in blast radius and this decision would remain
unsatisfied.

## Ownership

This ADR defines a constraint on the content of node bootstrap material and does not own
platform resources. For resource ownership, see ADR-039.

## Consequences

### Positive

- The constraint is stated against the content of bootstrap material, so compliance is
  assessable from the templates that define it rather than from runtime inspection.
- Satisfying it removes the exposure for all material in the table, not only for the etcd
  encryption key.
- ADR-100's two-phase credential construction exists only because bootstrap material is
  readable. Satisfying this decision removes the need for it.

### Negative

- Existing clusters remain exposed until their servers are replaced, so remediation is
  coupled to server lifecycle and cannot be delivered by configuration alone.
- Option 1 leaves the first control-plane machine of a cluster as an open case, because the
  admin kubeconfig is derived from certificate material that does not yet exist inside the
  cluster.
- If the deployed provider does not support a bootstrap flow that omits certificate material,
  the platform's options reduce to provider selection or accepted containment.
- Containment measures adopted in the interim introduce an availability dependency on the
  metadata address for node-local components.

## Impact

- **ADR-003 §6** remains in force. The secretbox encryption key is one row of the exposure
  table, and this ADR does not change how at-rest encryption is configured.
- **ADR-100** is halted and does not address this exposure. Its canary evidence stands as the
  source of the observations recorded here.
- **ADR-052** records a shared, reusable network join credential delivered through the same
  channel, which this decision also covers.
- No existing ADR is superseded. This decision adds a constraint that applies to any ADR
  placing material in node bootstrap material.

## References

- ADR-003: ESO and Infisical pattern (§6, encryption at rest)
- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-043: Control Plane Authority Model
- ADR-052: Sandbox compute and workspace credentials
- ADR-100: KMS v2 for secrets at rest (halted)
- `docs/analysis/bootstrap-material-exposure.md`: observations and verification commands
