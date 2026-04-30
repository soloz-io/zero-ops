---
inclusion: always
purpose: Technology stack, tools, frameworks, and technical constraints
scope: Languages, infrastructure, data storage, security, development tools, constraints
topics: [tech-stack, infrastructure-tools, development-tools, technical-constraints, gitops-research, service-mesh, zero-trust, observability, usage-metering]
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
- **NATS**: Messaging
- **KEDA**: Pods Scaling
- **Karpenter**: Nodes Scaling
- **Kagents**: Agentic workflows
- **Argo Agents**: Edge gitops

## Service Mesh & Zero-Trust Networking
- **Istio**: Service mesh for zero-trust networking
- **SPIFFE/SPIRE**: Workload identity and cryptographic authentication
- **Envoy**: Service proxy (part of Istio data plane)
- **mTLS**: Mutual TLS for service-to-service communication

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
- **JWKS**: JSON Web Key Set for JWT signature validation
- **cert-manager**: TLS certificate management
- **Infisical**: Secret manager
- **Teleport**: PAM

## Observability & Monitoring
- **VictoriaMetrics**: Metrics storage and querying
- **OpenSearch**: Log aggregation and search
- **Grafana Alloy**: Metrics collection and forwarding
- **cnpg2monitor**: Custom CNPG monitoring operator
- **postgresai**: Custom CNPG monitoring operator
- **K8sGPT**: AI-powered cluster diagnostics
- **ClickHouse**: Log aggregation and search
- **Jaeger**: Distributed tracing backend
- **OpenTelemetry**: Observability framework (traces, metrics, logs)

## Usage Metering & Billing
- **OpenMeter**: Real-time usage metering and billing engine (Hub cluster)
- **OTLP**: OpenTelemetry Protocol for usage event emission from AgentGateway to OpenMeter
- **CloudEvents**: Event format for usage tracking metadata
- **kube-sbt Metering**: Go abstractions for tenant/user-scoped usage queries and entitlements

## Development Tools
- **sqlc**: Type-safe SQL code generation for Go APIs
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
- **PostgREST**: Auto-generated REST APIs for dashboards and reporting

## Git & CI/CD
- **GitHub**: Source code and GitOps repositories
- **GitHub Actions**: CI/CD pipelines
- **OCI Artifacts**: Service catalog packaging
- **Argo Workflows**: Safe execution layer for operations

### Backup
- **velero**: Kubernetes backup and recovery tool
- **S3 Backup**: CNPG Database backup

### Policy Engine
- **Kyverno**

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
- **Event-Driven Provisioning**: Tenant creation MUST emit NATS events (`opensbt_onboardingRequest`) and return immediately. Application Plane handles async provisioning and emits success/failure events
- **Two-Step Onboarding**: Use TenantRegistration (pending) → Event → Provisioning → Tenant (active) flow, not direct tenant creation

## kube-sbt Abstraction Layer

**Purpose**: Reusable Go library for multi-tenant SaaS backends (Kubernetes-native alternative to AWS SBT)

**Core Philosophy**: Provider-agnostic interfaces. Swap Ory→Keycloak or NATS→Kafka without changing business logic.

### Core Interfaces
- **IAuth**: Authentication and authorization (Ory Stack implementation)
- **IEventBus**: Async event communication (NATS implementation)
- **IProvisioner**: Infrastructure provisioning (Crossplane implementation)
- **IStorage**: Data persistence (PostgreSQL implementation)
- **ISecretManager**: Secret management (Infisical implementation)
- **IMetering**: Usage metering and entitlements (OpenMeter implementation)
- **IUserManager**: Tenant user management (PostgREST + RLS implementation)

### Default Providers
- **Ory Stack**: Auth provider (Kratos + Hydra + Keto)
- **NATS**: Event bus provider (replaces AWS EventBridge)
- **PostgreSQL + sqlc**: Storage provider (replaces DynamoDB)
- **Infisical**: Secret manager (replaces AWS Secrets Manager)
- **Crossplane + ArgoCD**: Provisioning provider (replaces CloudFormation)
- **OpenMeter + OTLP**: Metering provider (replaces AWS CloudWatch + Cost Explorer)
- **PostgREST + RLS**: User management provider (tenant database access)

### Architecture Separation
- **Control Plane**: Tenant management, billing, provisioning, identity, MCP server
- **Application Plane**: Tenant workloads, databases, application services

**Key Principle**: Control Plane and Application Plane are logically separated but can run on same or different clusters depending on tier (Starter vs Enterprise).


