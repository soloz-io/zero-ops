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

## State Management

Bootstrap state is tracked in `~/.zero-ops/state/<cluster-name>.json`. To retry a failed bootstrap or start fresh:

```bash
# Clear state for specific cluster
rm -f ~/.zero-ops/state/<cluster-name>.json

# Example: clear state for 'mothership' cluster
rm -f ~/.zero-ops/state/mothership.json
```
