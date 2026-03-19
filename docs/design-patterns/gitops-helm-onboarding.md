---
title: GitOps & Helm Tenant Onboarding
description: The adapted ApplicationSet + Helm onboarding pattern for zero-ops platform using open-sbt
inclusion: always
---

# GitOps & Helm Tenant Onboarding (The Adapted Design)

**Version:** 1.0  
**Date:** 2026-03-17  
**Purpose:** Defines how the `zero-ops` platform utilizes the `open-sbt` toolkit to onboard tenants using an adapted, low-latency GitOps architecture.

## Executive Summary

To achieve an enterprise-grade, one-click SaaS factory without the operational burden of a massive platform team, the `zero-ops` platform adopts a highly optimized GitOps onboarding flow. By converging Red Hat/IBM GitOps patterns with `open-sbt`'s Control/Application Plane architecture, we eliminate custom Kubernetes operators, bypass traditional GitOps polling latency, and implement "Warm Pooling" to achieve sub-second onboarding for shared tiers.

This document analyzes the convergence of multiple GitOps and SaaS patterns to create a unified architecture for automated tenant onboarding. The analysis covers IBM's GitOps patterns, Red Hat's onboarding approaches, Red Hat Community of Practice (COP) projects, open-sbt design principles, and AWS SaaS architecture patterns.

**Key Finding:** The patterns converge into a three-layer architecture combining SaaS business logic, GitOps orchestration, and Kubernetes resource management.

## Pattern Analysis Overview

### Analyzed Patterns

This design is the synthesis of our convergence analysis across six major architectural patterns:

1. **IBM GitOps Pattern (openshift-clusterconfig-gitops):** Adopted the concept of "T-Shirt Sizing" (Basic, Standard, Premium, Enterprise) injected via global Helm values.
2. **Red Hat GitOps Onboarding (Blog Pattern):** Adopted the core mechanism: an ArgoCD `ApplicationSet` using a Git Directory Generator to stamp out tenant Helm charts dynamically.
3. **Red Hat COP Projects (namespace-configuration-operator, etc.):** *Explicitly Rejected* the use of custom Go-based K8s operators for tenant K8s resource generation to reduce operational complexity. Replaced entirely by ArgoCD + Helm.
4. **open-sbt Design Principles:** Leveraged the strict separation of Control Plane (API, Auth) and Application Plane (GitOps, NATS events, Provisioning).
5. **SaaS Architecture Principles (AWS SaaS Lens):** Enforced identity-tenant binding (Ory), defense-in-depth isolation (PostgreSQL RLS, Namespaces), and tier-based resource allocation.
6. **Kubernetes SaaS Patterns:** Mapped user needs to K8s primitives (NetworkPolicies, ResourceQuotas, Crossplane Compositions).

## The Adapted Design: Architecture

The `open-sbt` toolkit provides the `IProvisioner` and `IEventBus` interfaces. The `zero-ops` platform implements these to execute the following architecture:

### 1. The GitOps Repository Structure
All tenant infrastructure state lives in a single, centralized Git repository managed by the Application Plane.

```text
gitops-repo/
├── base-charts/
│   └── tenant-factory/         # Universal Helm chart for all tenants
├── tenants/
│   ├── warm-pool-01/           # Pre-provisioned unassigned tenant
│   │   └── values.yaml
│   ├── warm-pool-02/           # Pre-provisioned unassigned tenant
│   │   └── values.yaml
│   ├── acme-corp-123/          # Active assigned tenant
│   │   └── values.yaml
│   └── stark-ind-456/          # Enterprise BYOC tenant
│       └── values.yaml
└── applicationset.yaml         # The ArgoCD AppSet generator
```

### 2. The ArgoCD ApplicationSet
A single ArgoCD `ApplicationSet` watches the `tenants/` directory. Whenever the `open-sbt` Application Plane commits a new folder, ArgoCD automatically generates an `Application` custom resource.

```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: zero-ops-tenant-factory
spec:
  generators:
  - git:
      repoURL: https://github.com/zero-ops/gitops-repo
      revision: HEAD
      directories:
      - path: tenants/*
  template:
    metadata:
      name: 'tenant-{{path.basename}}'
    spec:
      project: tenants
      source:
        repoURL: https://github.com/zero-ops/gitops-repo
        targetRevision: HEAD
        path: base-charts/tenant-factory
        helm:
          valueFiles:
          - ../../tenants/{{path.basename}}/values.yaml
      destination:
        server: https://kubernetes.default.svc
        namespace: 'tenant-{{path.basename}}'
      syncPolicy:
        automated:
          prune: true
          selfHeal: true
```

### 3. The Universal Tenant Helm Chart
Instead of creating Go-based K8s operators, a single Helm chart handles all resource manifestation based on the T-Shirt size defined in `values.yaml`.

* **Kubernetes Primitives:** Generates `Namespace`, `ResourceQuota`, `NetworkPolicy`, and `RoleBindings`.
* **Crossplane XRs:** Generates Crossplane `CompositeResources` (e.g., `TenantDatabase`, `TenantCluster`) which Crossplane then resolves to provision CNPG Postgres databases or Hetzner CAPI clusters.

---

## Mitigating the Major Concerns

To successfully run this as a one-person AI-Native company, we implement three critical mitigations:

### Mitigation 1: Eliminating Operational Complexity
* **No Custom K8s Operators:** The entire K8s footprint is declarative via Helm and Crossplane.
* **GitOps DR:** Disaster recovery requires only bootstrapping ArgoCD and pointing it to the Git repository. Crossplane and ArgoCD will reconstruct the entire SaaS platform automatically.
* **AI-Operated Platform:** The platform is managed via the `zero-ops-api` MCP server. The Platform Admin's AI agent uses tools like `sync_application` to trigger GitOps syncs and `get_cluster_diagnostics` to debug.

### Mitigation 2: Performance at Scale
* **PostgreSQL RLS Optimization:** RLS is restricted to Shared Pool tiers. The `open-sbt` data layer uses `sqlc` to enforce strict `(tenant_id)` composite indexing on all queries, guaranteeing millisecond query times even with billions of rows. PostgREST provides additional REST API access for dashboards and reporting.
* **Webhook-Driven Syncs (No Polling):** ArgoCD's 3-minute Git polling is disabled. The `open-sbt` Application Plane fires a direct Webhook to the ArgoCD API immediately after a Git commit, triggering instant reconciliation and eliminating API server thrashing.

### Mitigation 3: Solving Onboarding Latency (The Warm Pool Pattern)
Standard GitOps provisioning takes 45-80 seconds. `open-sbt` splits the onboarding flow into two latency-optimized paths:

#### Path A: Shared Pool Tiers (Basic / Standard) - Latency < 2 Seconds
1. The `open-sbt` App Plane maintains a baseline of 10 "warm" (pre-provisioned) namespaces and Postgres schemas in the cluster.
2. **API Call:** User requests a Basic tier SaaS instance.
3. **Instant Claim:** The Control Plane DB instantly marks `warm-pool-01` as belonging to `tenant-123`.
4. **Auth Binding:** Ory Keto instantly creates the relationship `tenant:123#admin@user:xyz`.
5. **Response:** API returns `200 OK` in < 2 seconds. The user can use the platform immediately.
6. **Async Refill:** The Control Plane publishes a NATS event. The App Plane updates `warm-pool-01`'s `values.yaml` in Git to reflect its new owner, and commits a new `warm-pool-11` folder to Git to replace the consumed warm slot.

#### Path B: Dedicated / BYOC Tiers (Premium / Enterprise) - Latency 2-5 Minutes
1. **API Call:** User requests an Enterprise tier (Dedicated Hetzner CAPI Cluster or Dedicated CNPG Database).
2. **Response:** API returns `202 Accepted` instantly.
3. **AI UX Masking:** The frontend AI agent engages the user in a configuration conversation while the provisioning happens.
4. **GitOps Execution:** The App Plane commits a new `tenants/<tenant-id>` folder to Git with `tier: enterprise`.
5. **Crossplane Provisioning:** ArgoCD syncs the Helm chart, which creates a Crossplane `TenantCluster` XR. Crossplane provisions the Hetzner nodes.
6. **Event Streaming:** Progress is streamed back to the frontend Agent via NATS WebSockets until the cluster is ready.

---

## Alignment with SaaS Architecture Principles

1. **Identity Binding:** The onboarding flow intrinsically links the user to the tenant via Ory Kratos (Identity) and Ory Keto (Relationships). The resulting JWT contains the `tenant_id` and `tenant_tier`.
2. **Defense-in-Depth Isolation:**
   * *Data Layer:* PostgreSQL RLS enforced by `open-sbt` middleware.
   * *Compute Layer:* Kubernetes Namespaces + NetworkPolicies (generated via Helm).
   * *Execution Layer:* AgentSandbox (`runsc`) sandboxing for AI Agent execution workflows.
3. **Tier-Based Provisioning:** The entire infrastructure footprint is controlled by a single `tier` property in the tenant's `values.yaml`, feeding into the Crossplane Compositions and K8s Quotas.

---

## Detailed Pattern Analysis

### 1. IBM GitOps Pattern (openshift-clusterconfig-gitops)

**Architecture Overview:**
IBM's approach uses a dual-folder structure with T-Shirt sizing for standardized configurations.

**Key Components Analyzed:**
- **T-Shirt Sizing**: Global values with Basic/Standard/Premium/Enterprise configurations defined in `values-global.yaml`
- **Helm Templating**: Values-based configuration management using helper-proj-onboarding chart
- **Folder Structure**: 
  - `tenant-onboarding/`: Team onboarding configurations
  - `tenants/`: Individual tenant application configurations
  - `clusters/`: Cluster-wide base configurations

**Observed Implementation:**
```yaml
# From tenant-onboarding/values-global.yaml
global:
  application_gitops_namespace: gitops-application
  envs:
    - name: in-cluster
      url: https://kubernetes.default.svc
  tshirt_sizes:
    - name: Enterprise
      quota:
        pods: 100
        limits:
          cpu: 4
          memory: 4Gi
    - name: Basic
      quota:
        limits:
          cpu: 1
          memory: 1Gi
```

**Integration Analysis:**
The IBM T-Shirt sizing approach uses global values with predefined configurations that map to different resource allocations and limits.

### 2. Red Hat GitOps Onboarding Pattern

**Architecture Overview:**
Red Hat's approach emphasizes ApplicationSet-driven discovery with Helm templating for tenant-specific configurations, as described in their blog article "Project onboarding using GitOps and Helm".

**Key Components Analyzed:**
- **ApplicationSet Controller**: Uses Git Generator to walk folder structure and create Applications
- **Helm Charts**: Project-onboarding chart with helper-proj-onboarding dependency
- **Git Repository Structure**: `tenant-projects/{tenant}/{cluster}/values.yaml` pattern
- **T-Shirt Sizing**: Global values-file with predefined Basic/Standard/Premium/Enterprise configurations

**Observed Tenant Onboarding Flow:**
```
1. Create folder: tenant-projects/{tenant-name}/{cluster-name}/
2. Add values.yaml with tenant configuration
3. ApplicationSet Git Generator detects new folder
4. ApplicationSet creates ArgoCD Application
5. ArgoCD deploys using Helm chart with values.yaml
6. Tenant resources provisioned automatically
```

**Integration Analysis:**
The Red Hat ApplicationSet approach uses Git Generators to automatically detect folder structures and create ArgoCD Applications for tenant provisioning.

**ApplicationSet Template (from Red Hat article):**
```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: tenant-onboarding
spec:
  generators:
  - git:
      repoURL: https://github.com/org/tenant-configs
      revision: HEAD
      directories:
      - path: tenants/*
  template:
    metadata:
      name: '{{path.basename}}'
    spec:
      project: tenants
      source:
        repoURL: https://github.com/org/tenant-configs
        targetRevision: HEAD
        path: '{{path}}'
        helm:
          valueFiles:
          - values.yaml
      destination:
        server: https://kubernetes.default.svc
        namespace: '{{path.basename}}'
```

### 3. Red Hat COP Projects Analysis

#### 3.1 namespace-configuration-operator

**Purpose:** Event-driven namespace provisioning with Go templating

**Analyzed Features:**
- **Event-Driven**: Watches for Group, User, and Namespace creation events
- **Go Templates**: Uses text/template for flexible resource generation
- **CRDs**: GroupConfig, UserConfig, NamespaceConfig for configuration
- **Drift Prevention**: Continuously reconciles desired state
- **Team Onboarding Example**: Creates 4 namespaces per team (build, dev, qa, prod)

**Observed Team Onboarding Implementation:**
```yaml
# From examples/team-onboarding/group-config.yaml
apiVersion: redhatcop.redhat.io/v1alpha1
kind: GroupConfig
metadata:
  name: team-onboarding
spec:
  labelSelector:
    matchLabels:
      type: devteam    
  templates:
    - objectTemplate: |
        apiVersion: v1
        kind: Namespace
        metadata:
          name: {{ .Name }}-build
        labels:
          team: {{ .Name }}
          type: build
```

**Integration Analysis:**
The namespace-configuration-operator provides event-driven namespace provisioning that reacts to Kubernetes resource creation events.

**Note:** While the namespace-configuration-operator provides useful patterns, the zero-ops platform explicitly rejects the use of custom Go-based K8s operators for tenant resource generation to reduce operational complexity. This functionality is replaced entirely by ArgoCD + Helm.

#### 3.2 gitops-generator

**Purpose:** Resource generation using GeneratorOptions pattern

**Analyzed Features:**
- **GeneratorOptions API**: Structured configuration for resource generation
- **Multiple Resource Types**: Deployments, Services, Routes, Ingresses
- **Template-based**: Uses Go templates for resource generation
- **Kustomize Integration**: Generates Kustomize-compatible structures

**Observed API Structure:**
```go
// From api/v1alpha1/generator_options.go
type GeneratorOptions struct {
    Name        string `json:"name"`
    Namespace   string `json:"namespace,omitempty"`
    Application string `json:"application"`
    Replicas    int    `json:"replicas,omitempty"`
    TargetPort  int    `json:"targetPort,omitempty"`
    ContainerImage string `json:"containerImage,omitempty"`
    // ... additional fields
}
```

**Integration Analysis:**
The gitops-generator provides structured resource generation through its GeneratorOptions API and template-based approach.

**Note:** While gitops-generator offers structured resource generation, the zero-ops platform achieves similar functionality through the Universal Tenant Helm Chart approach, avoiding the need for additional operators.

#### 3.3 gitops-catalog

**Purpose:** Reusable GitOps patterns and components library

**Key Features:**
- **Component Library**: Pre-built GitOps patterns
- **Best Practices**: Proven configurations
- **Composability**: Mix and match components

**Integration Analysis:**
The gitops-catalog provides a library of reusable GitOps patterns and pre-built configurations for common use cases.

## Pattern Convergence Analysis

### Three-Layer Architecture Deep Dive

#### Layer 1: SaaS Business Logic (open-sbt)

**Responsibilities:**
- Tenant lifecycle management (CRUD operations)
- Event-driven communication between Control Plane and Application Plane
- Authentication and authorization (Ory Stack integration)
- Billing and metering abstractions
- MCP tools for agent integration
- Multi-tenant observability

**Observed open-sbt Interface Alignment:**
The analyzed patterns show compatibility with open-sbt's interface-based architecture through the IAuth, IEventBus, and IProvisioner interfaces.

**Observed GitOps Integration Points:**
The analyzed patterns show integration opportunities where Control Plane operations could trigger GitOps workflows, while Application Plane components could respond to Git-driven deployment events.

#### Layer 2: GitOps Orchestration (Red Hat + IBM)

**Responsibilities:**
- Git-based configuration management
- ApplicationSet-driven tenant discovery
- Helm templating and T-Shirt sizing
- ArgoCD continuous deployment
- Configuration drift prevention

**Key Components:**
```yaml
# ApplicationSet for tenant discovery
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: tenant-factory
spec:
  generators:
  - git:
      repoURL: https://github.com/org/tenant-configs
      revision: HEAD
      directories:
      - path: tenants/*
  template:
    metadata:
      name: 'tenant-{{path.basename}}'
    spec:
      source:
        helm:
          valueFiles:
          - values-{{path.basename}}.yaml
          parameters:
          - name: tenantId
            value: '{{path.basename}}'
          - name: tier
            value: '{{path.basename}}'
```

**Observed GitOps Orchestration Components:**
The Red Hat and IBM patterns demonstrate GitOps orchestration through ApplicationSet controllers, Helm templating, and ArgoCD continuous deployment for configuration management and deployment automation.

#### Layer 3: Kubernetes Resource Management (Simplified Approach)

**Responsibilities:**
- Automatic namespace configuration via Helm templates
- Resource template application through Universal Tenant Helm Chart
- RBAC and security policy enforcement via Helm-generated manifests
- Monitoring and logging setup through Crossplane compositions
- Resource quota management via Kubernetes ResourceQuotas

**Simplified Implementation:**
Instead of using Red Hat COP operators, the zero-ops platform uses a simplified approach:

```yaml
# Universal Tenant Helm Chart generates resources based on tier
{{- if eq .Values.tier "premium" }}
apiVersion: v1
kind: ResourceQuota
metadata:
  name: tenant-{{ .Values.tenantId }}-quota
  namespace: tenant-{{ .Values.tenantId }}
spec:
  hard:
    requests.cpu: "4"
    requests.memory: 8Gi
    limits.cpu: "8"
    limits.memory: 16Gi
{{- else if eq .Values.tier "basic" }}
apiVersion: v1
kind: ResourceQuota
metadata:
  name: tenant-{{ .Values.tenantId }}-quota
  namespace: tenant-{{ .Values.tenantId }}
spec:
  hard:
    requests.cpu: "1"
    requests.memory: 2Gi
    limits.cpu: "2"
    limits.memory: 4Gi
{{- end }}
```

**Key Difference from Red Hat COP Approach:**
The zero-ops platform explicitly avoids custom Kubernetes operators for tenant resource management, instead relying on:
- **Helm templating** for resource generation
- **Crossplane compositions** for infrastructure provisioning
- **ArgoCD ApplicationSets** for deployment orchestration

This approach reduces operational complexity while maintaining the same functionality.

### Analysis Summary

The convergence analysis reveals that the IBM GitOps patterns, Red Hat onboarding approaches, Red Hat COP projects, and open-sbt design principles show compatibility across three architectural layers for SaaS tenant management.

**Key Findings:**

#### 1. Pattern Compatibility
- **Business Logic Layer**: open-sbt provides SaaS-specific abstractions
- **Orchestration Layer**: GitOps handles deployment and configuration management  
- **Resource Management Layer**: Helm templates and Crossplane handle resource lifecycle (avoiding custom operators)

#### 2. Technology Consistency
- **Go-based**: Core platform components use Go for implementation consistency
- **Event-driven**: NATS events and Git events provide coordination
- **Declarative**: Configuration defined as code in Git repositories
- **Operator-free**: Eliminates custom Kubernetes operators for reduced operational complexity

#### 3. Operational Characteristics
- **GitOps**: All infrastructure changes tracked in Git with audit trails
- **Automation**: Minimal manual intervention through ArgoCD and Crossplane
- **Scalability**: Each layer operates independently with clear interfaces
- **Simplicity**: Reduced operational burden through elimination of custom operators

#### 4. Implementation Flexibility
- **Pluggable**: Each layer can use different implementations
- **Extensible**: New patterns can be added through Helm templates and Crossplane compositions
- **Cloud-agnostic**: Works across different Kubernetes distributions
- **Warm Pool Optimization**: Sub-second onboarding for shared tiers

## Analysis Conclusion

The analysis of IBM GitOps patterns, Red Hat onboarding approaches, Red Hat COP projects, and open-sbt design principles reveals compatibility across different aspects of SaaS tenant management. However, the zero-ops platform takes a simplified approach that eliminates operational complexity:

- **Proven patterns** from IBM and Red Hat implementations (ApplicationSet + Helm)
- **Simplified automation** through GitOps workflows without custom operators
- **Interface abstractions** through open-sbt's design
- **Event coordination** through NATS-driven communication
- **Operational simplicity** through elimination of custom Kubernetes operators
- **Performance optimization** through warm pool patterns and webhook-driven syncs

The adapted design demonstrates that enterprise-grade SaaS tenant onboarding can be achieved with minimal operational overhead by carefully selecting and combining proven patterns while avoiding unnecessary complexity.