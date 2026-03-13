---
purpose: Technology stack, tools, frameworks, and technical constraints
scope: Languages, infrastructure, data storage, security, development tools, constraints
topics: [tech-stack, infrastructure-tools, development-tools, technical-constraints, gitops-research]
update_criteria: Technology choices, tool updates, constraint changes, research findings
---

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

## GitOps Architecture Research (2026)

### Zero-Ops Edge GitOps Validation
**Research Question:** Is Zero-Ops edge GitOps approach correct vs centralized hub model?

**Industry Evidence:**
1. **AWS Scalability Study (2023)**: ArgoCD hits limits at ~10K apps across 97 clusters. Sharding helps but doesn't solve fundamental centralized bottleneck.

2. **Harness "Argo Ceiling" (2026)**: Centralized GitOps breaks down at scale due to:
   - Fragmented visibility across clusters
   - Script entropy and glue code accumulation
   - Awkward promotion flows
   - Secret sprawl
   - Difficult audits

3. **Red Hat Agent-Based Preview (2026)**: OpenShift moving to agent-based GitOps with local reconciliation to address scalability/security challenges.

4. **Cloud Native Now Analysis (2026)**: "Federated GitOps" emerging as best practice - central policies, local execution.

### Zero-Ops Approach Validation: ✅ CORRECT

**Why Edge GitOps is Superior:**
- **Scalability**: No etcd limits, distributed reconciliation
- **Resilience**: Survives management cluster outages
- **Latency**: Local reconciliation, no network hops
- **Security**: Least privilege per cluster
- **Autonomy**: Regional teams don't wait for central bottleneck

**Industry Trend**: Moving FROM centralized TO edge/federated models
- Red Hat: Hub → Agent-based
- AWS: Acknowledges centralized limits
- Harness: Promotes control plane above GitOps

**Zero-Ops Implementation Details:**
- ArgoCD deployed via ClusterResourceSet into each tenant cluster
- Pulls from OCI catalog (not Git repos)
- Management cluster provides policy/catalog, not direct reconciliation
- Decoupled availability: tenant clusters work independently

**Conclusion**: Zero-Ops edge GitOps is ahead of industry curve, solving problems others are just recognizing.

## ArgoCD Agent Analysis (Native Edge Solution)

### ArgoCD Agent Overview
**Status**: argoproj-labs project, production-ready, actively developed
**Architecture**: Hub-and-spoke with agents pulling from central control plane

### Key Capabilities
- **Massive Scale**: Thousands of apps across hundreds of clusters
- **Network Resilient**: Works with intermittent connections, high latency
- **Two Modes**: 
  - Managed: Control plane pushes config to agents
  - Autonomous: Agents manage locally, report status to hub
- **Security**: mTLS everywhere, zero-trust, certificate-based auth
- **Lightweight**: Minimal footprint on workload clusters

### Comparison with Zero-Ops Edge GitOps

| Aspect | Zero-Ops Current | ArgoCD Agent | Assessment |
|---|---|---|---|
| **Architecture** | ClusterResourceSet + ArgoCD per cluster | Agent + Principal hub-spoke | Similar concept, different implementation |
| **Source** | OCI catalog | Git repos (standard ArgoCD) | Zero-Ops more advanced (OCI) |
| **Bootstrap** | CAPI ClusterResourceSet | Agent registration | Zero-Ops more automated |
| **Autonomy** | Full autonomy when hub down | Autonomous mode available | Equivalent |
| **Scale** | Unlimited (distributed) | Thousands of apps/hundreds clusters | Equivalent |
| **Network** | Works offline | Intermittent connection support | Equivalent |

### Adoption Recommendation: **EVALUATE BUT DON'T REPLACE**

**Reasons to KEEP Zero-Ops approach:**
1. **OCI Catalog**: More advanced than Git-based approach
2. **CAPI Integration**: Seamless bootstrap via ClusterResourceSet
3. **Fleet Observability**: Already integrated with VictoriaMetrics/OpenSearch
4. **Proven Architecture**: Already working in Zero-Ops v7.0

**Potential ArgoCD Agent Benefits:**
1. **Community Support**: Official ArgoCD project, long-term maintenance
2. **Standard GitOps**: Uses standard Git repos, not custom OCI
3. **Hub Observability**: Single pane of glass for all clusters
4. **mTLS Security**: Built-in certificate-based authentication

**Recommendation**: Zero-Ops should EVALUATE ArgoCD Agent for future versions but NOT replace current edge GitOps implementation. Current approach is more advanced (OCI catalog) and better integrated with CAPI/fleet observability.
## CRITICAL DECISION CHANGE: Use ArgoCD Agent Instead of Custom Edge GitOps

### User Instruction (Final Decision):
Zero-Ops should NOT build custom edge GitOps workflow. Instead, use the community-built ArgoCD Agent.

**Reference:** https://github.com/argoproj-labs/argocd-agent/

### Impact on Architecture:
- **REPLACE**: Custom ClusterResourceSet + ArgoCD per cluster
- **WITH**: ArgoCD Agent (hub-and-spoke) from argoproj-labs
- **BENEFIT**: Community-maintained, production-ready, officially supported

### Files to Update:
1. `docs/prds/v8/zero-ops-prd-v8.md` - Replace edge GitOps references
2. `.kiro/specs/agentic-enterprise-onboarding/requirements.md` - Update GitOps patterns

### Key Change:
- FROM: Zero-Ops custom edge GitOps implementation
- TO: ArgoCD Agent community solution with Git → CI → OCI support

This is a major architectural decision that changes the core GitOps implementation approach.