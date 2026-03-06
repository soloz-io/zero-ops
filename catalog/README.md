# Catalog Directory

This directory contains add-on services for the Zero-Ops Platform.

## Structure

Services are organized by category:
- `gitops/` - GitOps engines (ArgoCD, Flux)
- `cni/` - CNI plugins (Cilium, Calico)
- `databases/` - Database operators (CloudNativePG)
- `cloud-providers/` - Cloud provider integrations (Hetzner CCM/CSI)
- `ioc/` - Infrastructure as Code (Crossplane)
- `os/` - Operating system configs (Talos)
- `secrets/` - Secret management (KSOPS)
- `messaging/` - Messaging systems (NATS)
- `agentic/` - Agentic systems (kagents)
- `autoscaling/` - Autoscaling (KEDA)

## Service Structure

Each service has:
- `service.yaml` - Metadata (name, version, dependencies, tier)
- `install.yaml` - Installation manifests

## Usage

Management cluster uses fixed services from catalog (Phase 1).
Tenant clusters will support service selection (Phase 2).
