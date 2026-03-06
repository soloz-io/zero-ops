## OS Choice: Flatcar Container Linux

We use Flatcar Container Linux instead of Ubuntu or Talos Linux for all cluster nodes. Ubuntu was ruled out because stock Ubuntu nodes accumulate configuration drift at scale — operators can SSH in and make ad-hoc changes, OS patches require per-node package management, and maintaining a hardened image pipeline is a significant ongoing engineering cost. Talos was the preferred choice architecturally — fully immutable, API-driven, zero drift by design — but the Talos control plane provider (CACPPT) does not ship a `TalosControlPlaneTemplate` CRD, which is a hard requirement for CAPI ClusterClass topology. Without ClusterClass, every tenant cluster requires a full concrete resource bundle rather than a lightweight topology instantiation, which does not scale operationally. Flatcar gives us the immutable, container-optimised foundation we need — read-only `/usr`, atomic A/B partition OS upgrades, Ignition-based sealed first-boot configuration, no manual package management — while remaining fully compatible with `KubeadmControlPlane` and therefore ClusterClass. When CACPPT closes the `TalosControlPlaneTemplate` gap upstream, migration from Flatcar to Talos is a ClusterClass update and a rolling node replacement; no application or platform logic changes.

---

**Open issues with Flatcar vs Talos worth tracking:**

- SSH is available on Flatcar nodes — drift via direct node access is possible through operator discipline, not structural prevention as with Talos
- The root filesystem has writable areas — Talos enforces stricter immutability at the OS level
- CVE surface is larger than Talos — Flatcar includes more userspace than Talos's 12-binary minimal image
- No Talos-equivalent API for node operations — debugging requires SSH rather than a purpose-built operations API