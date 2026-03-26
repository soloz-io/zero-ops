---
inclusion: always
---
<!------------------------------------------------------------------------------------
   Add rules to this file or a short description and have Kiro refine them for you.
   
   Learn about inclusion modes: https://kiro.dev/docs/steering/#inclusion-modes
-------------------------------------------------------------------------------------> 

# Technology Stack

## Core Languages & Frameworks
- **Go**: Primary backend language for zero-ops-api, agents, MCP servers
- **Rust**: AgentGateway (CNCF open source)
- **JavaScript/TypeScript**: Platform Console frontend
- **Bash**: CLI tooling and automation scripts

## Infrastructure & Platform
- **Kubernetes**: Container orchestration (Ubuntu + kubeadm, not Talos)
- **Crossplane**: Infrastructure provisioning engine
- **ArgoCD**: GitOps continuous deployment
- **Argo Workflows**: Provisioning orchestration
- **CAPI/CAPH**: Cluster API with Hetzner provider
- **Hetzner Cloud**: Primary cloud provider (BYOC model)
- **NATS**: Messaging
- **KEDA**: Pods Scaling
- **Karpenter**: Nodes Scaling
- **Kagents**: Agentic workflows
- **Argo Agents**: Edge gitops

## Data & Storage
- **PostgreSQL**: Primary database with CNPG operator
- **pgvector**: Vector similarity search for AI features
- **PgBouncer**: Connection pooling (via CNPG spec.pooler)
- **Hetzner S3**: Object storage
- **HashiCorp Vault**: Secret management

## Authentication & Security
- **Ory Kratos**: Identity management
- **Ory Hydra**: OAuth2/OIDC token issuer
- **Ory Keto**: Relationship-based authorization
- **JWT**: Authentication tokens with JWKS validation
- **cert-manager**: TLS certificate management

## Observability & Monitoring
- **VictoriaMetrics**: Monitoring Metrics storage and querying
- **ClickHouse**: Business metrics (billing, usage, analytics)
- **OpenSearch**: Log aggregation and search
- **Grafana Alloy**: Metrics collection and forwarding
- **cnpg2monitor**: Custom CNPG monitoring operator
- **postgresai**: Custom CNPG monitoring operator
- **K8sGPT**: AI-powered cluster diagnostics

## Development Tools
- **sqlc**: Type-safe SQL code generation for Go APIs
- **Testcontainers-Go**: Integration testing with real databases
- **Gin**: HTTP web framework for Go APIs
- **Helm**: Kubernetes package management
- **Kustomize**: Kubernetes configuration management

## AI & Agent Runtime
- **LiteLLM**: AI model gateway and routing
- **AgentSandbox (runsc)**: Sandboxed agent execution environment
- **MCP (Model Context Protocol)**: Agent-to-platform communication
- **PostgREST**: Auto-generated REST APIs for dashboards and reporting

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
- **Status Controller Pattern**: Control Plane APIs MUST read tenant status from PostgreSQL cache only, NEVER query Kubernetes/ArgoCD APIs directly. ArgoCD webhooks update the cache via NATS events (`opensbt_argoSyncCompleted`)
- **Event-Driven Provisioning**: Tenant creation MUST emit NATS events (`opensbt_onboardingRequest`) and return immediately. Application Plane handles async provisioning and emits success/failure events
- **Two-Step Onboarding**: Use TenantRegistration (pending) → Event → Provisioning → Tenant (active) flow, not direct tenant creation