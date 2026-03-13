# Technology Stack

## Core Languages & Frameworks
- **Go**: Primary backend language for zero-ops-api, agents, MCP servers
- **Python**: identity-service (Ory stack abstraction layer)
- **Rust**: AgentGateway (CNCF open source)
- **JavaScript/TypeScript**: Platform Console frontend
- **Bash**: CLI tooling and automation scripts

## Infrastructure & Platform
- **Kubernetes**: Container orchestration (Ubuntu + kubeadm, not Talos)
- **Crossplane**: Infrastructure provisioning engine
- **ArgoCD**: GitOps continuous deployment
- **CAPI/CAPH**: Cluster API with Hetzner provider
- **Hetzner Cloud**: Primary cloud provider (BYOC model)

## Data & Storage
- **PostgreSQL**: Primary database with CNPG operator
- **pgvector**: Vector similarity search for AI features
- **PgBouncer**: Connection pooling (via CNPG spec.pooler)
- **Hetzner S3**: Object storage
- **KSOPS + Age**: Secret encryption in Git

## Authentication & Security
- **Ory Kratos**: Identity management
- **Ory Hydra**: OAuth2/OIDC token issuer
- **Ory Keto**: Relationship-based authorization
- **JWT**: Authentication tokens with JWKS validation
- **cert-manager**: TLS certificate management

## Observability & Monitoring
- **VictoriaMetrics**: Metrics storage and querying
- **OpenSearch**: Log aggregation and search
- **Grafana Alloy**: Metrics collection and forwarding
- **cnpg2monitor**: Custom CNPG monitoring operator
- **K8sGPT**: AI-powered cluster diagnostics

## Development Tools
- **sqlc**: Type-safe SQL code generation
- **Testcontainers-Go**: Integration testing with real databases
- **Gin**: HTTP web framework for Go APIs
- **Helm**: Kubernetes package management
- **Kustomize**: Kubernetes configuration management

## AI & Agent Runtime
- **LiteLLM**: AI model gateway and routing
- **gVisor (runsc)**: Sandboxed agent execution environment
- **MCP (Model Context Protocol)**: Agent-to-platform communication
- **PostgREST**: Auto-generated REST APIs from PostgreSQL schema

## Git & CI/CD
- **GitHub**: Source code and GitOps repositories
- **GitHub Actions**: CI/CD pipelines
- **OCI Artifacts**: Service catalog packaging
- **Argo Workflows**: Safe execution layer for operations

## Technical Constraints
- **No Talos Linux**: Ubuntu + kubeadm only (CACPPT compatibility)
- **No Unit Tests**: E2E tests only following TDD principles
- **HTTPS Only**: All endpoints require TLS 1.2+
- **GitOps First**: No direct Kubernetes API writes except bootstrap
- **MCP First**: All platform capabilities via MCP interface
- **BYOC Only**: No shared cloud billing, tenant owns compute costs