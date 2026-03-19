# zero-ops

Zero-Ops Platform: A Go monorepo for Kubernetes infrastructure automation with multiple service binaries.

## Architecture

Zero-Ops is structured as a single-module Go monorepo with multiple independent binaries:

```
zero-ops/
├── cmd/                    # Binary entry points
│   ├── hub/                # Hub cluster management
│   ├── opensbt/            # SaaS builder toolkit control plane
│   ├── zero-ops-api/       # Tenant lifecycle API
│   ├── auth-proxy/         # OAuth2/JWT authentication proxy
│   └── mcp-server/         # Model Context Protocol server
└── internal/               # Private packages
    ├── hub/                # Hub cluster logic
    ├── opensbt/            # OpenSBT packages
    ├── auth-proxy/         # Auth proxy logic
    ├── api/                # API handlers
    └── db/                 # Database layer
```

## Binaries

### 1. Hub (`hub`)
Bootstrap and manage Hub (Management) Clusters on Hetzner Cloud using Cluster API.

**Build:**
```bash
make build-hub
# or
go build -o bin/hub ./cmd/hub
```

**Usage:**
```bash
# Bootstrap Hub Cluster with Ubuntu (default, production-ready)
export HCLOUD_TOKEN=<your-hetzner-token>
./bin/hub bootstrap \
  --name=mothership \
  --region=fsn1

# Bootstrap with Talos (auto-builds snapshot)
./bin/hub bootstrap \
  --name=mothership \
  --region=fsn1 \
  --os=talos \
  --build-talos-image

# Teardown cluster
./bin/hub teardown --name=mothership
```

### 2. OpenSBT (`opensbt`)
SaaS Builder Toolkit control plane for multi-tenant application management.

**Build:**
```bash
make build-opensbt
```

**Usage:**
```bash
# Set environment variables
export KRATOS_PUBLIC_URL=http://kratos-public:4433
export HYDRA_PUBLIC_URL=http://hydra-public:4444
export NATS_URLS=nats://nats:4222
export DATABASE_URL=postgres://postgres:postgres@localhost:5432/opensbt

./bin/opensbt
```

### 3. Tenant API (`zero-ops-api`)
REST API for tenant lifecycle management with PostgreSQL backend.

**Build:**
```bash
make build-api
```

**Usage:**
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

### 4. Auth Proxy (`auth-proxy`)
OAuth2/JWT authentication proxy for Ory Hydra/Kratos integration.

**Build:**
```bash
make build-auth-proxy
```

### 5. MCP Server (`mcp-server`)
Model Context Protocol server for AI agent integration.

**Build:**
```bash
make build-mcp-server
```

## Development

**Build all binaries:**
```bash
make build-all
```

**Build specific binary:**
```bash
make build-hub
make build-opensbt
make build-auth-proxy
make build-mcp-server
```

**Run tests:**
```bash
make test
```

**Docker builds:**
```bash
docker build -f cmd/hub/Dockerfile -t zero-ops/hub:latest .
docker build -f cmd/opensbt/Dockerfile -t zero-ops/opensbt:latest .
docker build -f cmd/auth-proxy/Dockerfile -t zero-ops/auth-proxy:latest .
docker build -f cmd/mcp-server/Dockerfile -t zero-ops/mcp-server:latest .
```

## OS Support (Hub Cluster)

Hub supports both Ubuntu and Talos via `--os` flag:

**Ubuntu (default):** Production-ready with ClusterClass support. Uses KubeadmControlPlane for scalable cluster topology management. Immutable OS with atomic updates, no Packer build required.

**Talos:** Available for testing. Uses TalosControlPlane with direct cluster resources. Requires Packer-built snapshot via `--build-talos-image` flag.

## State Management

Hub bootstrap state is tracked in `~/.zero-ops/state/<cluster-name>.json`. To retry a failed bootstrap or start fresh:

```bash
# Clear state for specific cluster
rm -f ~/.zero-ops/state/<cluster-name>.json

# Example: clear state for 'mothership' cluster
rm -f ~/.zero-ops/state/mothership.json
```

## License

Apache 2.0


