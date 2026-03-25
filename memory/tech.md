---
purpose: Technology stack, tools, frameworks, and technical constraints
scope: Languages, infrastructure, data storage, security, development tools, constraints
topics: [tech-stack, infrastructure-tools, development-tools, technical-constraints, gitops-research]
update_criteria: Technology choices, tool updates, constraint changes, research findings
---

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
- **CAPI+ClusterClass+CAPH**: Cluster API with Hetzner provider
- **Hetzner Cloud**: Primary cloud provider (BYOC model)

## Data & Storage
- **PostgreSQL**: Primary database with CNPG operator
- **pgvector**: Vector similarity search for AI features
- **PgBouncer**: Connection pooling (via CNPG spec.pooler)
- **Hetzner S3**: Object storage

## Authentication & Security
- **Ory Kratos**: Identity management
- **Ory Hydra**: OAuth2/OIDC token issuer
- **Ory Keto**: Relationship-based authorization
- **JWT**: Authentication tokens with JWKS validation
- **cert-manager**: TLS certificate management
- **Infisical**: Secret manager
- **Teleport**: PAM

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
- **AgentRegistry**: Build, deploy, execute agents
- **KAgent**: Runtime for agents and ToolServer creation
- **AgentGateway**: ToolServer Gateway for agents and tenants
- **AgentSandbox**: Sandboxed agent execution environment
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
- **MCP First**: All platform capabilities via MCP interface (no CLI/UI for tenant operations in Phase 1-2)
- **BYOC Only**: No shared cloud billing, tenant owns compute costs
- **Hub Cluster**: Management cluster named "mothership" (3 CP + 2 workers, Hetzner, Ubuntu 24.04, k8s v1.31.6)
- **GitOps Phased Rollout**: Standard ArgoCD is approved strictly for MVP phases to save development time. The final production version MUST use the ArgoCD Agent labs project (hub-and-spoke model) to prevent centralized bottlenecks.
- **Status Controller Pattern**: Hub API must NEVER query the Kubernetes API directly for tenant status. It must read from PostgreSQL, which is continuously updated by the standalone `tenant-controller`.

## open-sbt Abstraction Layer

**Purpose**: Reusable Go library for multi-tenant SaaS backends (Kubernetes-native alternative to AWS SBT)

**Core Philosophy**: Provider-agnostic interfaces. Swap Ory→Keycloak or NATS→Kafka without changing business logic.

### Core Interfaces
- **IAuth**: Authentication and authorization (Ory Stack implementation)
- **IEventBus**: Async event communication (NATS implementation)
- **IProvisioner**: Infrastructure provisioning (Crossplane implementation)
- **IStorage**: Data persistence (PostgreSQL implementation)
- **ISecretManager**: Secret management (Infisical implementation)

### Default Providers
- **Ory Stack**: Auth provider (Kratos + Hydra + Keto)
- **NATS**: Event bus provider (replaces AWS EventBridge)
- **PostgreSQL + sqlc**: Storage provider (replaces DynamoDB)
- **Infisical**: Secret manager (replaces AWS Secrets Manager)
- **Crossplane + ArgoCD**: Provisioning provider (replaces CloudFormation)

### Architecture Separation
- **Control Plane**: Tenant management, billing, provisioning, identity, MCP server
- **Application Plane**: Tenant workloads, databases, application services

**Key Principle**: Control Plane and Application Plane are logically separated but can run on same or different clusters depending on tier (Starter vs Enterprise).

### Package Structure
```
open-sbt/pkg/
├── interfaces/       # Core interfaces (IAuth, IEventBus, etc.)
├── models/           # Data models (Tenant, User, Event)
├── providers/        # Default implementations (ory/, nats/, postgres/, infisical/)
├── controlplane/     # Control Plane components
├── applicationplane/ # Application Plane components
├── events/           # Event definitions and handlers
├── mcp/              # MCP server implementation
└── libraries/        # Multi-tenant microservice libraries
```

### When to Use open-sbt
- Building multi-tenant SaaS backends
- Need provider flexibility (swap auth, events, storage)
- Want Control/App plane separation
- Kubernetes-native architecture

### When NOT to Use open-sbt
- Single-tenant applications
- Vendor lock-in acceptable (use AWS SBT directly)
- Non-Kubernetes deployments
