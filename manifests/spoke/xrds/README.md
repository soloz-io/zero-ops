# Crossplane XRD Definitions

This directory contains Crossplane Composite Resource Definitions (XRDs) for the Zero-Ops platform.

## SpokePool XRD

**File**: `spokepool-v1.yaml`

Defines the declarative API for provisioning Spoke Pool clusters (cells) that host multiple Starter tier tenants with schema-level isolation.

### API Schema

```yaml
apiVersion: nutgraf.in/v1alpha1
kind: SpokePool
metadata:
  name: spokepool-01
spec:
  region: hel1                    # Hetzner datacenter (hel1, nbg1, hel1)
  nodePool:
    count: 3                      # Worker nodes (1-10)
    instanceType: cx33            # Hetzner server type (cx33, cx41, cx51)
  maxTenantCapacity: 100          # Max tenant schemas per cell (1-100)
```

### Supported Regions

- `hel1`: Falkenstein, Germany
- `nbg1`: Nuremberg, Germany
- `hel1`: Helsinki, Finland

### Supported Instance Types

- `cx33`: 2 vCPU, 8 GB RAM
- `cx41`: 4 vCPU, 16 GB RAM
- `cx51`: 8 vCPU, 32 GB RAM

### Status Fields

- `phase`: Current provisioning phase (Pending, Provisioning, Ready, Failed)
- `conditions`: Detailed status conditions
- `clusterEndpoint`: Kubernetes API endpoint
- `cellId`: Unique cell identifier
- `tenantCount`: Current number of tenants

### Requirements

- FR-1.1: Declarative Cell Creation
- AC-1: SpokePool XRD and Composition
- NFR-3.1: Idempotent provisioning

### Related Files

- Composition: `xrds/compositions/spokepool-hetzner.yaml`
- ClusterResourceSet: `xrds/compositions/spokepool-clusterresourceset.yaml`
