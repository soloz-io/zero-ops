# Crossplane Compositions

This directory contains Crossplane Compositions that implement the provisioning logic for XRDs.

## SpokePool Hetzner Composition

**File**: `spokepool-hetzner.yaml`

Implements the SpokePool XRD for Hetzner Cloud infrastructure using CAPI (Cluster API) and CAPH (Cluster API Provider Hetzner).

### Generated Resources

When a SpokePool XR is applied, this Composition generates:

1. **CAPI Cluster** - Main cluster resource with labels `spoke-type: pool` and `cell-id: <name>`
2. **HetznerCluster** - Hetzner-specific infrastructure (load balancer, network)
3. **KubeadmControlPlane** - Single control plane node (Ubuntu 24.04, k8s v1.31.6)
4. **HCloudMachineTemplate** (Control Plane) - cx21 instance template
5. **MachineDeployment** - Worker node deployment
6. **KubeadmConfigTemplate** - Worker node bootstrap configuration
7. **HCloudMachineTemplate** (Workers) - Worker instance template (cx31/cx41/cx51)

### Architecture

- **OS**: Ubuntu 24.04 (not Talos)
- **Kubernetes**: v1.31.6
- **CNI**: Cilium v1.14.5 (installed via postKubeadmCommands)
- **CCM**: Hetzner Cloud Controller Manager
- **Network**: 
  - Pod CIDR: 10.244.0.0/16
  - Service CIDR: 10.96.0.0/12
  - Hetzner Network: 10.0.0.0/16

### Patching Strategy

The Composition uses Crossplane's Pipeline mode with patch-and-transform function:

- **FromCompositeFieldPath**: Propagates spec values from SpokePool XR to generated resources
- **ToCompositeFieldPath**: Updates SpokePool status with cluster state
- **Transforms**: Maps regions to network zones, formats resource names

### Status Propagation

- `status.phase` - Cluster provisioning phase
- `status.cellId` - Derived from metadata.name
- `status.clusterEndpoint` - Kubernetes API endpoint

### Requirements

- FR-1.1: Declarative Cell Creation
- AC-1: SpokePool XRD and Composition
- NFR-1.1: Provisioning time < 15 minutes

### Related Files

- XRD: `xrds/definitions/spokepool-v1.yaml`
- ClusterResourceSet: `xrds/compositions/spokepool-clusterresourceset.yaml` (to be created)
