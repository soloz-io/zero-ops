# Building Ubuntu Image

Zero-Ops uses Ubuntu 24.04 as the default OS for management clusters, following CAPH (Cluster API Provider Hetzner) official support.

## Why Ubuntu?

CAPH officially supports Ubuntu for Kubernetes clusters. While Talos Linux is supported via CABPT (Cluster API Bootstrap Provider Talos), the management cluster bootstrap uses Ubuntu 24.04 with kubeadm for maximum compatibility with CAPH reference implementations.

## Default Behavior

Zero-Ops uses Hetzner's official Ubuntu 24.04 image by default. No custom image building is required.

```bash
export HCLOUD_TOKEN=<your-token>
./bin/zero-ops mgmt bootstrap \
  --name=mothership \
  --region=fsn1 \
  --os=ubuntu \
  --ssh-key=zero-ops-mac-mini-debug \
  2>&1
```

The bootstrap process:
- Uses Hetzner's official `ubuntu-24.04` image
- Installs Kubernetes via cloud-init and kubeadm
- Installs CNI (Cilium) and CCM via ClusterResourceSet
- Takes ~8-10 minutes for full cluster provisioning

## Custom Ubuntu Image (Optional)

If you need a custom Ubuntu image with pre-installed packages, you can build one using Packer.

### Automatic Build

```bash
export HCLOUD_TOKEN=<your-token>
./bin/zero-ops mgmt bootstrap \
  --name=mothership \
  --region=fsn1 \
  --os=ubuntu \
  --build-ubuntu-image
```

The `--build-ubuntu-image` flag:
- Downloads Packer automatically
- Creates snapshot named `ubuntu-<cluster-name>`
- Reuses existing snapshot if found
- Takes ~3-5 minutes

### Manual Build

```bash
# Set token
export HCLOUD_TOKEN=<your-token>

# Build snapshot
packer init internal/assets/manifests/packer/hetzner-ubuntu.pkr.hcl
packer build \
  -var "hcloud_token=$HCLOUD_TOKEN" \
  -var "snapshot_name=ubuntu-mothership" \
  -var "location=fsn1" \
  internal/assets/manifests/packer/hetzner-ubuntu.pkr.hcl

# Get snapshot ID
curl -H "Authorization: Bearer $HCLOUD_TOKEN" \
  https://api.hetzner.cloud/v1/images | \
  jq -r '.images[] | select(.name=="ubuntu-mothership") | .id'

# Use custom snapshot
./bin/zero-ops mgmt bootstrap \
  --name=mothership \
  --region=fsn1 \
  --os=ubuntu \
  --ubuntu-image-id=<snapshot-id>
```

## Packer Configuration

Location: `internal/assets/manifests/packer/hetzner-ubuntu.pkr.hcl`

- Base image: Ubuntu 24.04 (official Hetzner image)
- Server type: cx23
- Default location: fsn1
- Pre-installs: containerd, kubeadm, kubelet, kubectl

## Troubleshooting

**Snapshot already exists:**
Packer fails if snapshot name exists. Either:
- Delete old snapshot in Hetzner console
- Use `--ubuntu-image-id` with existing snapshot ID
- Change cluster name

**Server type unavailable:**
cx23 not available in all regions. Use fsn1 or update Packer config with available type.

**Build fails:**
Check Hetzner API token has write permissions and sufficient quota for snapshots.

## Talos Linux Alternative

For Talos Linux-based clusters, see `docs/build-talos-image.md`.

## Ownership

This ADR defines operating system and bootstrap image standards for management clusters. It does not own platform resources. For resource ownership, see ADR-039.
