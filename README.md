# zero-ops

Zero-Ops Platform CLI for bootstrapping Management Clusters on Hetzner Cloud using Cluster API.

## Quick Start

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

