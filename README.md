# Zero-Ops Platform:

## Elevator Pitch:

**Zero-Ops** is an MCP-first, Gitops PAAS platform that provisions production-grade, multi-tenant environments for building AI-native SAAS products like replit, Lovable, Emergent. 

## Elevator Pitch Extended

**One-click AI-native SaaS infrastructure, fully automated.**

Zero-Ops is an MCP-first platform that provisions production-grade, multi-tenant SaaS environments in a single declarative command. Speak to your IDE: "Create my enterprise environment" — get a complete stack: Kubernetes cluster (Crossplane + CAPI), HA PostgreSQL with pgvector, GitOps (ArgoCD), secrets management (Infisical), observability (VictoriaMetrics + Grafana), and privileged access (Teleport). 

**BYOC model** — runs in your cloud account. **GitOps-first** — all changes via Git, zero imperative mutations. **Agentic-native** — AI agents propose infrastructure changes via PRs, you approve. From zero to production in 15 minutes, with full Kubernetes control and eject capability.

## Platform Structure

Zero-Ops Platform (PaaS)
├── CNPG Cluster (Spoke)
│   ├── tenant_app-builder_db (logical database - ONE SaaS tenant)
│   │   ├── Account: Construction Co (super admin: alice@construction.com)
│   │   │   ├── User: worker1@construction.com
│   │   │   ├── User: worker2@construction.com
│   │   │   └── Applications/Forms (Forms: Each app contains multiple forms, Each form has dynamic schema, shared by workers)
│   │   │
│   │   ├── Account: Recruiting Inc (super admin: bob@recruiting.com)
│   │   │   ├── User: recruiter1@recruiting.com
│   │   │   └── Applications/Forms
│   │   │
│   │   └── Account: Retail Store (super admin: charlie@retail.com)
│   │       └── Users...
│   │
│   ├── tenant_another-saas_db (different SaaS tenant)
│   └── tenant_yet-another_db (different SaaS tenant)

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
export HCLOUD_TOKEN=$(cat k8-secrets/hetzner/token)
./bin/hub bootstrap \
  --name=hub \
  --region=fsn1 \
  --debug 2>&1 | tee .zero-ops/bootstrap-hub.log

# Step 2: Configure AWS Secrets Manager for Infisical encryption key recovery (REQUIRED)
# This must be done BEFORE init-secrets to enable disaster recovery
# Prerequisites: AWS CLI configured with IAM admin permissions (aws configure or export AWS_PROFILE=<admin-profile>)
# This command will:
# 1. Create IAM user: hub-operator-secrets-manager-production
# 2. Create IAM policy with Secrets Manager permissions
# 3. Generate access keys and inject into Kubernetes secret
export AWS_PROFILE=zerotouch-platform-admin  # Use profile with IAM admin permissions
./bin/hub configure-aws-secrets-manager \
  --environment=development \
  --aws-region=ap-south-1 \
  --kubeconfig=k8-secrets/kubeconfig/hub-cp.kubeconfig

# Only for DEV, If IAM user already exists with access keys, delete old key first:
# aws iam delete-access-key --user-name hub-operator-secrets-manager-production --access-key-id <OLD_KEY_ID>

# Step 3: Configure GitHub Access (Secret Zero)
# This enables ArgoCD to sync manifests and create platform namespaces
# CRITICAL: Must run BEFORE init-secrets so platform-data namespace exists
export GITHUB_TOKEN=$(cat k8-secrets/github/token)
./bin/hub configure-github-access \
  --ghcr-pat=$GITHUB_TOKEN \
  --kubeconfig=k8-secrets/kubeconfig/hub-cp.kubeconfig

# Step 4: Wait for ArgoCD to sync and create namespaces
# ArgoCD will create platform-data, platform-security, and other namespaces
kubectl wait --for=condition=ready namespace platform-data --timeout=300s \
  --kubeconfig=k8-secrets/kubeconfig/hub-cp.kubeconfig

# Step 5: Initialize bootstrap secrets (Secret Zero)
# This generates CA certificate, Infisical master keys, and backs them up to AWS
# TLS is enabled from Day 0 - no upgrade step needed
# NOTE: Now works because platform-data namespace exists (created by ArgoCD)
./bin/hub init-secrets \
  --kubeconfig=k8-secrets/kubeconfig/hub-cp.kubeconfig

# Step 6: Wait for Infisical to be ready (check pods are running)
kubectl get pods -n platform-security --kubeconfig=k8-secrets/kubeconfig/hub-cp.kubeconfig

# Step 7: Create Machine Identity in Infisical UI
# 1. Access Infisical UI (port-forward or ingress)
# 2. Go to Access Control -> Machine Identities
# 3. Create "eso-operator" identity
# 4. Copy Client ID and Client Secret

# Step 8: Configure ESO authentication to Infisical
# This enables ESO to sync secrets from Infisical
./bin/hub configure-eso \
  --infisical-client-id=<client-id-from-infisical-ui> \
  --infisical-client-secret=<client-secret-from-infisical-ui> \
  --kubeconfig=k8-secrets/kubeconfig/hub-cp.kubeconfig

# Step 9: Wait for ArgoCD to sync and deploy database
kubectl wait --for=condition=ready pod -l cnpg.io/cluster=platform-db \
  -n platform-data --timeout=600s \
  --kubeconfig=k8-secrets/kubeconfig/hub-cp.kubeconfig

# Teardown cluster
./bin/hub teardown --name=hub
```

**Command Execution Order (CRITICAL):**

1. **`hub bootstrap`** - Creates Kubernetes cluster and deploys ArgoCD
2. **`hub configure-aws-secrets-manager`** - Injects AWS credentials for disaster recovery
3. **`hub configure-github-access`** - Injects GitHub credentials (enables ArgoCD sync and namespace creation)
4. **Wait for namespaces** - ArgoCD creates platform-data, platform-security, etc.
5. **`hub init-secrets`** - Generates CA certificate, Infisical master keys with TLS enabled from Day 0
6. **Wait for Infisical** - Verify Infisical pods are running
7. **Create Machine Identity** - Use Infisical UI to create ESO authentication credentials
8. **`hub configure-eso`** - Injects ESO auth to Infisical (enables secret management via GitOps)
9. **Wait for Database** - ArgoCD syncs and deploys PostgreSQL cluster with TLS

**Why this order matters:**
- AWS credentials must exist BEFORE `init-secrets` runs (operator needs them for backup)
- GitHub credentials must be injected BEFORE `init-secrets` (ArgoCD needs to create platform-data namespace)
- `init-secrets` requires platform-data namespace to exist (created by ArgoCD sync)
- `init-secrets` generates CA certificate offline and injects it before CNPG starts (Day-0 Deterministic Injection)
- CNPG uses the CLI-generated CA (via spec.certificates.serverCASecret)
- Infisical uses the same CA for TLS verification (via DB_ROOT_CERT)
- Both CNPG and Infisical start with TLS enabled on first boot - no restart loops
- `configure-eso` enables GitOps workflow (ArgoCD syncs database manifests)
- If you skip `configure-aws-secrets-manager`, disaster recovery will not work
- If you skip `configure-github-access`, ArgoCD cannot sync and namespaces won't be created

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

Hub bootstrap state is tracked in `.zero-ops/state/<cluster-name>.json`. Bootstrap logs are saved to `.zero-ops/bootstrap-<cluster-name>.log`. To retry a failed bootstrap or start fresh:

```bash
# Clear state for specific cluster
rm -f .zero-ops/state/<cluster-name>.json

# Example: clear state for 'hub' cluster
rm -f .zero-ops/state/hub.json

# View bootstrap logs
cat .zero-ops/bootstrap-hub.log
```

## License

Apache 2.0


