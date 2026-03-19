## OS Choice: Ubuntu 24.04

We use Ubuntu 24.04 LTS as the default OS for cluster nodes. This decision is based on CAPH (Cluster API Provider Hetzner) official support and operational requirements.

### Why Ubuntu?

**CAPH Official Support**: CAPH officially supports Ubuntu with Kubeadm bootstrap provider. All CAPH templates and examples use Ubuntu 24.04 as the default image.

**ClusterClass Compatibility**: Ubuntu works seamlessly with `KubeadmControlPlane` and `KubeadmControlPlaneTemplate`, enabling full ClusterClass topology support for scalable multi-tenant cluster provisioning.

**Pre-installed Components**: CAPH expects node images to have kubernetes components (kubelet, kubeadm, kubectl) pre-installed. Ubuntu images can be built with these components using Packer.

**Hetzner Integration**: Official Hetzner `ubuntu-24.04` image is available and works out-of-box with cloud-init for bootstrap configuration.

### Alternative: Talos Linux

Talos was evaluated but CACPPT (Talos Control Plane Provider) does not ship `TalosControlPlaneTemplate` CRD, which is required for ClusterClass topology. Without ClusterClass, every cluster requires full concrete resource bundle rather than lightweight topology instantiation.

**Talos Support**: Available via `--os=talos` flag with custom snapshot built using `--build-talos-image`.