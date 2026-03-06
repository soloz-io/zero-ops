# zero-ops

Zero-Ops Platform CLI for bootstrapping Management Clusters on Hetzner Cloud using Cluster API and Talos Linux.

## Quick Start

```bash
# Build CLI
make build

# Bootstrap Management Cluster (auto-builds Talos snapshot)
export HCLOUD_TOKEN=<your-hetzner-token>
./bin/zero-ops mgmt bootstrap \
  --name=mothership \
  --region=fsn1 \
  --build-talos-image
```

The `--build-talos-image` flag automatically creates a Talos Linux snapshot in your Hetzner account (reused on subsequent runs).
