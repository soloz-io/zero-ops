# Zero-Ops Platform:

## Development Principles & Guidelines

Zero-Ops is an enterprise Hub-Spoke PaaS platform providing a complete infrastructure management solution for Kubernetes clusters across multiple cloud providers. 

## Business model

Syself + Civo Konstruct

### Syself

- Allow customers to use the OSS version and setup their own clusters in BYOC model.
- Customers can enable their maintenance needs just by CI setting like in Renovate.
- The changes needed are made to their repo via PR. since its gitops env, expect things to reconcile.

### Civo Konstruct

- Open sourced IDP
- They take care of cluster creation + pre defined IDP.

### Zero-Ops - Bundle/Coupled
- We provide cluster creation + IDP + beyond
- We handpicked OSS tools that can be coupled together to run a software company.
- It can be BYOC or BYOK.
- No subscription fee. Tool is free to use. Play only for maintenance.
- Most economical software factory platform
- AI native IDP

To maintain the integrity, stability, and production-readiness of the platform, all contributions and technical solutions must adhere to the following principles:

- **Production-Ready & Idiomatic Code:** All code must be idiomatic, widely adopted, enterprise-grade, and production-ready. Non-idiomatic or experimental approaches are not accepted.
- **Strict ADR Alignment:** Every proposed solution must explicitly reference and align with the existing Architecture Decision Records (ADRs) and core platform principles.
- **Justified Deviations:** If a solution must deviate from existing ADRs, it must include a comprehensive justification and a proposal for the necessary ADR updates to accommodate the change.
- **Declarative Operations (GitOps First):** Direct imperative cluster changes (e.g., `kubectl rollout restart` or `kubectl apply`) are strictly prohibited. Infrastructure mutations must be handled by finding and fixing bugs in the codebase, with all changes applied via GitOps.

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

# Legacy way
# export HCLOUD_TOKEN=$(cat k8-secrets/hetzner/token)
# ./bin/hub bootstrap \
#   --name=hub \
#   --region=hel1 \
#   --debug 2>&1 | tee .zero-ops/bootstrap-hub.log

# New way
./scripts/hub-bootstrap.sh

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
  --kubeconfig=k8-secrets/kubeconfig/hub.kubeconfig

# Only for DEV, If IAM user already exists with access keys, delete old key first:
# aws iam delete-access-key --user-name hub-operator-secrets-manager-production --access-key-id <OLD_KEY_ID>

# Step 3: Configure GitHub Access (Secret Zero)
# This enables ArgoCD to sync manifests from ALL repositories in the soloz-io organization
# Creates organization-wide credentials (repo-creds) for:
# - zero-ops repository (platform manifests)
# - fleet-registry repository (tenant configurations)
# - Any other repositories in the soloz-io organization
# CRITICAL: Must run BEFORE init-secrets so platform-data namespace exists
export GITHUB_TOKEN=$(cat k8-secrets/github/github-pat-token)
./bin/hub configure-github-access \
  --ghcr-pat=$GITHUB_TOKEN \
  --kubeconfig=k8-secrets/kubeconfig/hub.kubeconfig

# Step 4: Wait for ArgoCD to sync and create namespaces
# ArgoCD will create platform-data, platform-security, and other namespaces
# Note: We must poll until the namespace exists, as kubectl wait fails if it is missing.
until kubectl get namespace platform-data --kubeconfig=k8-secrets/kubeconfig/hub.kubeconfig >/dev/null 2>&1; do
  echo "Waiting for ArgoCD to create platform-data namespace..."
  sleep 5
done

kubectl wait --for=jsonpath='{.status.phase}'=Active namespace/platform-data \
  --timeout=60s --kubeconfig=k8-secrets/kubeconfig/hub.kubeconfig

# Step 5: Initialize bootstrap secrets (Secret Zero)
# This generates CA certificate, Infisical master keys, and backs them up to AWS
# TLS is enabled from Day 0 - no upgrade step needed
# Step 3.5 automatically bootstraps Infisical (Org, Project, Machine Identity)
# No port-forward needed - the CLI uses pod exec + REST API internally
INFISICAL_API_URL=http://localhost:8080 ./bin/hub init-secrets --kubeconfig=k8-secrets/kubeconfig/hub.kubeconfig

# Step 6: Wait for ArgoCD to sync and deploy database
kubectl wait --for=condition=ready pod -l cnpg.io/cluster=platform-db \
  -n platform-data --timeout=600s \
  --kubeconfig=k8-secrets/kubeconfig/hub.kubeconfig

# Steps 7-8 (former manual UI steps) are now automated in init-secrets Step 3.5
# The deprecated `hub configure-eso` has been replaced by automatic bootstrapping.

# Teardown Hub cluster
./bin/hub teardown --name=hub

# Only use if local kind cluster does not exist and already pivoted
export HCLOUD_TOKEN=$(cat k8-secrets/hetzner/token) && ./bin/hub teardown --name=hub --force --confirm 2>&1 | tee .zero-ops/teardown-hub.log

# Teardown spoke clusters (independent of hub)
export HCLOUD_TOKEN=$(cat k8-secrets/hetzner/token) && ./bin/hub spoke teardown --force 2>&1 | tee .zero-ops/teardown-spoke.log

# Teardown specific spoke cluster
export HCLOUD_TOKEN=$(cat k8-secrets/hetzner/token) && ./bin/hub spoke teardown --name=spoke-pool-eu-prod-01 --force 2>&1 | tee .zero-ops/teardown-spoke.log

```

**Command Execution Order (CRITICAL):**

1. **`hub bootstrap`** - Creates Kubernetes cluster and deploys ArgoCD
2. **`hub configure-aws-secrets-manager`** - Injects AWS credentials for disaster recovery
3. **`hub configure-github-access`** - Injects GitHub credentials (enables ArgoCD sync and namespace creation)
4. **Wait for namespaces** - ArgoCD creates platform-data, platform-security, etc.
5. **`hub init-secrets`** - Generates CA, Infisical keys, AND bootstraps Infisical (Org/Project/Machine Identity)
6. **Wait for Database** - ArgoCD syncs and deploys PostgreSQL cluster with TLS

**Why this order matters:**
- GitHub credentials must be injected BEFORE `init-secrets` (ArgoCD needs to create platform-data namespace)
- `init-secrets` requires platform-data namespace to exist (created by ArgoCD sync)
- `init-secrets` generates CA certificate offline and injects it before CNPG starts (Day-0 Deterministic Injection)
- Step 3.5 of `init-secrets` auto-creates the Infisical Machine Identity (replaces `configure-eso`)
- CNPG uses the CLI-generated CA (via spec.certificates.serverCASecret)
- Infisical uses the same CA for TLS verification (via DB_ROOT_CERT)
- Both CNPG and Infisical start with TLS enabled on first boot - no restart loops
- `init-secrets` also creates the `infisical-auth` Secret and patches `hub-bootstrap-config`
- If you skip `configure-aws-secrets-manager`, disaster recovery will not work
- If you skip `configure-github-access`, ArgoCD cannot sync and namespaces won't be created
- GitHub credentials are organization-scoped (repo-creds type) to support multiple repositories (zero-ops, fleet-registry, etc.)

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


