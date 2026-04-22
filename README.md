# Zero-Ops Platform:

## Elevator Pitch:

**Zero-Ops** is an MCP-first, Gitops PAAS platform that provisions production-grade, multi-tenant environments for building AI-native SAAS products like replit, Lovable, Emergent. 

## Elevator Pitch Extended

**One-click AI-native SaaS infrastructure, fully automated.**

Zero-Ops is an MCP-first platform that provisions production-grade, multi-tenant SaaS environments in a single declarative command. Speak to your IDE: "Create my enterprise environment" — get a complete stack: Kubernetes cluster (Crossplane + CAPI), HA PostgreSQL with pgvector, GitOps (ArgoCD), secrets management (Infisical), observability (VictoriaMetrics + Grafana), and privileged access (Teleport). 

**BYOC model** — runs in your cloud account. **GitOps-first** — all changes via Git, zero imperative mutations. **Agentic-native** — AI agents propose infrastructure changes via PRs, you approve. From zero to production in 15 minutes, with full Kubernetes control and eject capability.

## Platform Structure

Zero-Ops Platform (PaaS)
└── Tenant: "App Builder" (THE SAAS tenant - One product)
    └── Users: alice, bob, charlie (your customers)
        └── Applications: Each user builds their own apps
            └── Forms: Each app contains multiple forms
                └── Tables: Each form has dynamic schema

Zero-Ops Platform: PaaS Platform
│
└── Tenant: app-builder (single SAAS tenant for the PAAS - One product)
    │
    ├── User: alice@construction.com
    │   └── Application: "Construction Management System"
    │       ├── Form: "Order Management"
    │       │   ├── Table: orders
    │       │   ├── Table: projects
    │       │   └── Table: materials
    │       └── Form: "Equipment Tracking"
    │           ├── Table: equipment
    │           └── Table: maintenance_logs
    │
    ├── User: bob@recruiting.com
    │   └── Application: "Applicant Tracking System"
    │       ├── Form: "Candidates"
    │       │   ├── Table: candidates
    │       │   ├── Table: job_openings
    │       │   └── Table: interview_invitations
    │       └── Form: "Job Portal"
    │           └── Table: job_portal_links
    │
    └── User: charlie@retail.com
        └── Application: "Inventory Management"
            └── Forms: ...


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
# Step 1: Bootstrap Hub Cluster with Ubuntu (default, production-ready)
export HCLOUD_TOKEN=<your-hetzner-token>
./bin/hub bootstrap \
  --name=mothership \
  --region=fsn1

# Step 2: Configure AWS Secrets Manager for Infisical encryption key recovery (REQUIRED)
# This must be done BEFORE init-secrets to enable disaster recovery
./bin/hub configure-aws-secrets-manager \
  --aws-access-key-id=<your-aws-access-key-id> \
  --aws-secret-access-key=<your-aws-secret-access-key> \
  --aws-region=ap-south-1 \
  --kubeconfig=k8-secrets/kubeconfig/hub-cp.kubeconfig

# Step 3: Initialize bootstrap secrets (Secret Zero)
# This generates Infisical master keys and backs them up to AWS
./bin/hub init-secrets \
  --kubeconfig=k8-secrets/kubeconfig/hub-cp.kubeconfig

# Step 4: Wait for Infisical to be ready (check pods are running)
kubectl get pods -n hub-platform-security --kubeconfig=k8-secrets/kubeconfig/hub-cp.kubeconfig

# Step 5: Create Machine Identity in Infisical UI
# 1. Access Infisical UI (port-forward or ingress)
# 2. Go to Access Control -> Machine Identities
# 3. Create "eso-operator" identity
# 4. Copy Client ID and Client Secret

# Step 6: Configure ESO authentication to Infisical and ArgoCD GitHub access
./bin/hub configure-eso \
  --infisical-client-id=<client-id-from-infisical-ui> \
  --infisical-client-secret=<client-secret-from-infisical-ui> \
  --ghcr-username=<your-github-username> \
  --ghcr-pat=<your-github-personal-access-token> \
  --kubeconfig=k8-secrets/kubeconfig/hub-cp.kubeconfig

# Teardown cluster
./bin/hub teardown --name=mothership
```

**Command Execution Order (CRITICAL):**

1. **`hub bootstrap`** - Creates Kubernetes cluster and deploys ArgoCD
2. **`hub configure-aws-secrets-manager`** - Injects AWS credentials for disaster recovery (MUST run before init-secrets)
3. **`hub init-secrets`** - Generates Infisical master keys, backs them up to AWS, starts Infisical pods
4. **Wait for Infisical** - Verify Infisical pods are running and healthy
5. **Create Machine Identity** - Use Infisical UI to create ESO authentication credentials
6. **`hub configure-eso`** - Injects ESO auth to Infisical + ArgoCD GitHub access + GHCR pull secret

**Why this order matters:**
- AWS credentials must exist BEFORE `init-secrets` runs (operator needs them for backup)
- `init-secrets` must run BEFORE `configure-eso` (Infisical must be running to create Machine Identity)
- `configure-eso` enables GitOps workflow (ArgoCD syncs ESO manifests, ESO syncs secrets from Infisical)
- If you skip `configure-aws-secrets-manager`, disaster recovery will not work
- If you run `configure-eso` before Infisical is ready, you cannot create Machine Identity

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

Hub supports Ubuntu only:

**Ubuntu (default):** Production-ready with ClusterClass support. Uses KubeadmControlPlane for scalable cluster topology management. Immutable OS with atomic updates, no Packer build required.

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


