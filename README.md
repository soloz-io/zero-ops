# zero-ops

Zero-Ops Platform: Management Cluster CLI + Tenant API for Kubernetes infrastructure automation.

## Components

### 1. Management Cluster CLI (`zero-ops`)
Bootstrap and manage Kubernetes clusters on Hetzner Cloud using Cluster API.

[CLI Documentation →](./README.md#quick-start)

### 2. Tenant API (`zero-ops-api`)
REST API for tenant lifecycle management with PostgreSQL backend.

[API Documentation →](./cmd/zero-ops-api/README.md)

## Quick Start - CLI

```bash
# Build CLI
make build

# Bootstrap Management Cluster with Flatcar (default, production-ready)
export HCLOUD_TOKEN=<your-hetzner-token>
./bin/zero-ops mgmt bootstrap \
  --name=mothership \
  --region=fsn1

# Bootstrap with Talos (auto-builds snapshot)
./bin/zero-ops mgmt bootstrap \
  --name=mothership \
  --region=fsn1 \
  --os=talos \
  --build-talos-image
```

## Quick Start - API

```bash
# Start API with Docker Compose
cd cmd/zero-ops-api
docker-compose up -d

# Create a tenant
curl -X POST http://localhost:8080/api/v1/tenants \
  -H "Content-Type: application/json" \
  -d '{
    "name": "acme-corp",
    "email": "admin@acme.com",
    "plan": "professional"
  }'
```

Flatcar uses Hetzner's default stable image. Talos requires `--build-talos-image` or `--image-id` with existing snapshot.

## OS Support

Zero-Ops supports both Flatcar and Talos via `--os` flag:

**Flatcar (default):** Production-ready with ClusterClass support. Uses KubeadmControlPlane for scalable cluster topology management. Immutable OS with atomic updates, no Packer build required.

**Talos:** Available for testing. Uses TalosControlPlane with direct cluster resources. Requires Packer-built snapshot via `--build-talos-image` flag.

## State Management

Bootstrap state is tracked in `~/.zero-ops/state/<cluster-name>.json`. To retry a failed bootstrap or start fresh:

```bash
# Clear state for specific cluster
rm -f ~/.zero-ops/state/<cluster-name>.json

# Example: clear state for 'mothership' cluster
rm -f ~/.zero-ops/state/mothership.json
```

Here is a concise paragraph you can drop straight into your README:

---

