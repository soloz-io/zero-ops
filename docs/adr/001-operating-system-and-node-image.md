# ADR-001: Ubuntu is the platform's only supported node operating system

**Date:** 2026-10-15 (consolidated; original decision 2026-05)
**Status:** Accepted

## Context

Cluster API requires node images carrying a bootstrap provider's expected components, and
CAPH's (Cluster API Provider Hetzner) reference implementations are written against a
specific operating system. Two candidates were evaluated for this platform: Ubuntu with the
kubeadm bootstrap provider (CABPK) and kubeadm control-plane provider (CACPK), and Talos
Linux with the Talos bootstrap provider (CABPT) and Talos control-plane provider (CACPPT).

**CAPH is not a factor in the Talos decision and no claim to the contrary should be read
into this record.** CAPH provisions infrastructure — servers, networks, load balancers — and
is bootstrap-agnostic. Talos is explicitly supported at the bootstrap layer by CABPT. The
blockers below are properties of CACPPT and of Talos itself.

Ubuntu 24.04 LTS has been the default since the platform's first management cluster. The
reasons recorded at the time remain the operative ones:

- CAPH officially supports Ubuntu with the kubeadm bootstrap provider, and all CAPH
  templates and examples use Ubuntu 24.04 as the default image.
- Ubuntu works with `KubeadmControlPlane` and `KubeadmControlPlaneTemplate`, which is what
  makes ClusterClass topology available. That is what permits **multi-tenant spoke
  provisioning at scale** by instantiating a topology rather than applying a full concrete
  resource bundle per cluster. ADR-046 and the spoke provisioning path depend on it.
- Kubernetes is installed on the node **by cloud-init and kubeadm** against the official
  image, with no pre-baked Kubernetes components required. Addon delivery — the container
  network interface and the cloud controller manager — is a separate concern recorded in the
  addon decisions, not here.
- CAPH expects Kubernetes components — kubelet, kubeadm, kubectl — to be present in the node
  image. Ubuntu images can be built with them.
- Hetzner's official `ubuntu-24.04` image works with cloud-init for bootstrap configuration
  without modification.

Talos Linux was evaluated twice, in different roles, and rejected both times for distinct
reasons. Both findings were previously recorded outside the numbered decision record — one in
an unnumbered document, one as a subsection of ADR-046 — and are consolidated here.

**As a control-plane operating system — this is the decisive blocker.** CACPPT does not ship
a `TalosControlPlaneTemplate` custom resource definition. ClusterClass topology requires a
control-plane template, so a Talos control plane cannot be instantiated through a topology at
all. Every such cluster would instead require a full concrete resource bundle, which is the
provisioning model this platform does not use.

**As a worker operating system alongside a kubeadm control plane.** Talos is an immutable
appliance governed by its own node daemon. Its nodes expect to obtain cluster certificate
material from a Talos control-plane service rather than by joining a kubeadm control plane,
and they neither run `kubeadm join` nor consume the bootstrap tokens this platform's hub
mints. A Talos worker therefore cannot join a kubeadm control plane without replacing the
control plane as well. On the hybrid provider there was an additional cost: no pre-formatted
hypervisor images with embedded extensions, requiring image transfer, maintenance-mode port
proxying and boot-order manipulation. ADR-046 §13 records this evaluation and the adoption of
Flatcar Container Linux for hybrid workers in its place.

A partial Talos path exists in the platform's own tooling, selected by an operating-system
argument and an image-build argument. It was never usable for spoke control planes, for the
template reason above, and its presence has twice been mistaken for an available option when
assessing alternatives.

## Decision

**Ubuntu is the only supported node operating system on this platform. Talos Linux is not
supported.**

The supported image is Hetzner's official Ubuntu 24.04 LTS image.

**A custom-image capability was documented but does not exist, and is recorded here as a gap
rather than carried forward as if it did.** The superseded document described building a
custom Ubuntu snapshot with Packer, selected by image identifier, including a configuration
path, a server type, a default region, snapshot naming and troubleshooting. None of it is
present: there is no Packer configuration anywhere in the repository, and neither the
image-build nor the image-identifier argument exists in the command-line tool. The
instructions also named a binary and a subcommand that have since been renamed.

Restoring those instructions would document capabilities the platform does not have. If a
custom image is wanted, it is new work; until then the official image is the only image, and
Kubernetes components are installed at boot rather than baked in.

**Flatcar Container Linux remains the operating system for hybrid home-lab workers** as
decided in ADR-046 §13. That decision is unaffected: this record governs the operating system
for provider-hosted nodes and does not reopen the hybrid worker choice.

**The Talos path is removed from the codebase.** A selectable option that cannot be used for
the platform's provisioning model is not a capability; it is a surface that invites the
mistake of treating it as one. Removal covers the operating-system argument's Talos value, the
Talos image-build argument, the branches selecting on it, the Talos bootstrap and
control-plane provider versions, and the applied provider manifests that install those
providers.

## Ownership

This ADR defines operating system and node image standards. It does not own platform
resources. For resource ownership, see ADR-039.

## Consequences

### Positive

- One operating system for provider-hosted nodes, which is the configuration the
  infrastructure provider's reference implementations are written against.
- ClusterClass topology remains available, which the spoke provisioning model requires.
- Removing an unusable alternative eliminates a recurring source of incorrect assessment:
  the Talos path has twice been read as an available option during architectural review.
- One numbered record replaces a how-to document, an unnumbered document and a subsection of
  another ADR.

### Negative

- Immutable-appliance operating systems are foreclosed, and with them the authenticated
  certificate-fetch model such systems use. ADR-104 records an exposure for which that model
  would be a candidate remedy; this decision removes it from consideration while the
  control-plane template limitation stands.
- A single supported operating system concentrates dependence on one vendor's image and
  release cadence.
- Reversing this requires CACPPT to ship a `TalosControlPlaneTemplate`, which is an upstream
  change this platform does not control.

## Impact

- **Supersedes** `docs/adr/dual-os-support.md`, which was unnumbered and carried the
  control-plane template finding. That document is removed and its content is recorded here.
- **Replaces** the former `001-bootstrap-ubuntu-mgnt-cluster.md`, which was a build guide
  rather than a decision record. Its decisions are recorded here. Its operational content was
  **not** carried forward because it described a custom-image workflow absent from the
  codebase and commands that have been renamed; see the Decision above, where that is recorded
  as a gap. Provisioning timings it quoted are omitted as operational rather than decisional.
- **ADR-046 §13** remains the record of the hybrid worker evaluation and the adoption of
  Flatcar. Its Talos findings are summarised here and not superseded.
- **ADR-104** loses one candidate remedy, which is recorded in its own open avenues rather
  than implied here.
- Codebase removal is required by the Decision above and is not optional cleanup.

## References

- ADR-039: Platform Ownership Model
- ADR-040: Day-0 vs Day-1 Lifecycle Boundary
- ADR-043: Control Plane Authority Model
- ADR-046: Hybrid provider home worker (§13, Flatcar adoption)
- ADR-104: No key material in node bootstrap material
