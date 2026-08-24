# Hetzner infrastructure layer

The spoke `ClusterClass` (`spokepool-v1`) and the CCM/CSI addon templates. Consumed by
both Hetzner-backed provider cells:

- `providers/hetzner` — `resources: [base]`
- `providers/hybrid` — `resources: [../hetzner/base]`

## Why this is not `_shared`

It was `providers/_shared/` until 2026-08-24. That name promised provider-neutrality it
never had: **every document in this directory is Hetzner-bound.** The `ClusterClass`
references `HetznerClusterTemplate` and two `HCloudMachineTemplate`s — those are
infrastructure-provider CRDs, not neutral kinds — and the addon templates carry the
Hetzner CCM and CSI. There is no neutral skeleton underneath, so there was nothing for a
`_shared` directory to hold.

The name mattered because it invited exactly the wrong move: adding a non-Hetzner cell and
extending this layer, or dropping a provider-specific value in because the directory
appeared to be the common one.

## Why hybrid consumes it

A hybrid spoke is a **Hetzner control plane** with home-lab workers (ADR-046 §13), so it
needs this infrastructure layer by construction. Routing that through a directory called
`_shared` disguised a real dependency as a generic one. It is now stated plainly.

## What a non-Hetzner cell does

Not this. A CAPI `ClusterClass` is bound to its infrastructure provider through
`infrastructure.ref` and `machineInfrastructure.ref`, so an AWS cell needs its own
`ClusterClass` over `AWSClusterTemplate`/`AWSMachineTemplate` — a sibling layer, not a
patch on this one. The provider-varying pieces already have a dispatch idiom to follow:
per-provider paths, as ADR-046 §24 does for `providers/<provider>/k8s/cilium-config-base.yaml`.

Concretely, an AWS layer would differ in at least: the instance-metadata endpoint and its
auth flow, the `providerID` scheme, the credential Secret, and the region/zone model.

## What belongs here

Hetzner infrastructure shared by **both** cells. Anything that varies between them belongs
in that cell's own directory — `providers/hetzner/k8s/` or `providers/hybrid/k8s/` — which
is already how the per-provider Crossplane compositions and cilium config bases are split.
