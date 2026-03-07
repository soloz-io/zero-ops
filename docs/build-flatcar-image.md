# Building Flatcar Image

Zero-Ops uses Packer to build Flatcar Container Linux snapshots in Hetzner Cloud.

## Automatic Build

```bash
go build -o bin/zero-ops ./cmd/zero-ops && export HCLOUD_TOKEN=<your-token> && timeout 900 ./bin/zero-ops mgmt bootstrap --name=mothership --region=nbg1 --build-flatcar-image
```

```bash
export HCLOUD_TOKEN=<your-token>
./bin/zero-ops mgmt bootstrap \
  --name=mothership \
  --region=nbg1 \
  --build-flatcar-image
```

The `--build-flatcar-image` flag:
- Downloads Packer automatically
- Creates snapshot named `flatcar-<cluster-name>`
- Reuses existing snapshot if found
- Takes ~2-3 minutes

## Manual Build

```bash
# Set token
export HCLOUD_TOKEN=<your-token>

# Build snapshot
packer init internal/assets/manifests/packer/hetzner-flatcar.pkr.hcl
packer build \
  -var "hcloud_token=$HCLOUD_TOKEN" \
  -var "snapshot_name=flatcar-mothership" \
  -var "location=nbg1" \
  internal/assets/manifests/packer/hetzner-flatcar.pkr.hcl

# Get All Snapshots
curl -s -H "Authorization: Bearer lHc58bsakrr9csMJtcDOn8raxgjcJMiuhrl9JZKJiEK8ekNTryDYToDcOlZ3xN3z" 'https://api.hetzner.cloud/v1/images?type=snapshot' | jq -r '.images[] | {id, name, type}'

# Get snapshot ID
curl -H "Authorization: Bearer $HCLOUD_TOKEN" \
  https://api.hetzner.cloud/v1/images | \
  jq -r '.images[] | select(.name=="flatcar-mothership") | .id'

# Use snapshot
./bin/zero-ops mgmt bootstrap \
  --name=mothership \
  --region=nbg1 \
  --image-id=<snapshot-id>
```

## Packer Configuration

Location: `internal/assets/manifests/packer/hetzner-flatcar.pkr.hcl`

- Base image: Ubuntu 22.04
- Server type: cx23
- Default location: nbg1
- Installs Flatcar via flatcar-install script

## Troubleshooting

**Snapshot already exists:**
Packer fails if snapshot name exists. Either:
- Delete old snapshot in Hetzner console
- Use `--image-id` with existing snapshot ID
- Change cluster name

**Server type unavailable:**
cx23 not available in all regions. Use nbg1 or update Packer config with available type.

**Build fails:**
Check Hetzner API token has write permissions and sufficient quota for snapshots.
