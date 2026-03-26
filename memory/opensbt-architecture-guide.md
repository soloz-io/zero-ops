---
purpose: Definitive guide for open-sbt architecture and integration patterns
scope: Platform-wide architectural guidance for building SaaS applications using the open-sbt framework
topics: open-sbt Control Plane, Application Plane boundaries, event-driven onboarding, Status Controller pattern
update_criteria: Update when architectural decisions change or new patterns emerge
---

# open-sbt Architecture Guide

## 1. Overview: The Framework vs. The Platform

This document defines the architecture and integration patterns for **open-sbt**, a Kubernetes-native port of the AWS SaaS Builder Toolkit (SBT). 

**Critical Distinction:** `open-sbt` is a **reusable SaaS toolkit/library**, *not* your business application. 
* **open-sbt** provides generic multi-tenant SaaS primitives (Tenant Lifecycle, Authentication, Billing, Metering, Event Routing).
* **The Zero-Ops Platform** (or any other application) is a *consumer* of `open-sbt`. It contains your specific business domain logic (e.g., `agents-core`, `mcp-server`).

**Do not tightly couple business domain services into the `open-sbt` library.**

---

## 2. Understanding the SBT Architecture

### 2.1 The Architectural Planes

```text
┌─────────────────────────────────────────────────────────┐
│ CONTROL PLANE (The open-sbt Library)                    │
│ Purpose: Generic Multi-tenant SaaS infrastructure       │
│                                                         │
│ ├── Tenant Lifecycle Management (Registration & State)  │
│ ├── User Management & Auth (via Ory Stack)              │
│ ├── Billing & Metering (Pluggable)                      │
│ └── Event Routing (via NATS)                            │
└───────────────────────┬─────────────────────────────────┘
                        │
      Asynchronous NATS Events (e.g., opensbt_onboardingRequest)
                        │
┌───────────────────────▼─────────────────────────────────┐
│ PLATFORM SERVICES (Your Business Domain)                │
│ Purpose: The actual product you are selling             │
│                                                         │
│ ├── Agent Management (agents-core)                      │
│ └── MCP Server (mcp-server)                             │
└───────────────────────┬─────────────────────────────────┘
                        │
      Asynchronous NATS Events (e.g., opensbt_agentDeployRequested)
                        │
┌───────────────────────▼─────────────────────────────────┐
│ APPLICATION PLANE (Tenant's Deployed Resources)         │
│ Purpose: Tenant-specific infrastructure & workloads     │
│                                                         │
│ ├── Provisioning Orchestrator (Go Daemon committing to Git)
│ ├── GitOps Engine (ArgoCD)                              │
│ ├── Infrastructure Engine (Crossplane)                  │
│ └── Isolated Tenant Namespaces & Databases (CNPG)       │
└─────────────────────────────────────────────────────────┘
```

### 2.2 Key Definitions

**Control Plane (`open-sbt/controlplane`):**
- **What it is:** The global infrastructure managing the SaaS lifecycle across all tenants.
- **What it does:** Orchestrates tenant registration, enforces tier quotas, manages user identities, coordinates billing, and issues standardized events.
- **What it's NOT:** It does not contain business logic like "Agents" or "MCP".

**Application Plane Orchestrator (`open-sbt/applicationplane`):**
- **What it is:** A worker daemon that translates Control Plane events into actual infrastructure state.
- **What it does:** Listens to `onboardingRequest` events, generates Helm/Kustomize manifests, commits them to a tenant Git repository, and publishes `provisionSuccess` upon completion.

**Application Plane (Tenant Workloads):**
- **What it is:** The actual isolated resources running per-tenant.
- **What it does:** Runs PostgreSQL databases (via CNPG), tenant-specific AI agents, and dedicated ingress controllers.

---

## 3. The Event-Driven Tenant Lifecycle (CRITICAL)

The most important pattern in SBT is the **Asynchronous Two-Step Onboarding**. Tenant creation must **never** be a synchronous blocking API call that provisions infrastructure.

### 3.1 TenantRegistration vs. Tenant

1. **TenantRegistration:** Represents a *request* or *intent* to onboard.
2. **Tenant:** Represents a fully provisioned, active, and routable tenant.

### 3.2 The Onboarding Flow

```text
1. User calls POST /api/v1/tenant-registrations
   └─> Control Plane saves `TenantRegistration` to PostgreSQL (Status: PENDING)
   └─> Control Plane publishes NATS event `opensbt_onboardingRequest`
   └─> Control Plane returns HTTP 201 Created (with Registration ID) immediately.

2. Application Plane Orchestrator
   └─> Subscribes to `opensbt_onboardingRequest`
   └─> Claims warm pool slot OR generates Helm values for tenant namespace/DB
   └─> Commits configurations to Git (GitOps Repo)
   └─> (ArgoCD syncs and Crossplane provisions the actual resources)
   └─> Upon ArgoCD health check passing, Orchestrator publishes `opensbt_provisionSuccess`.

3. Control Plane
   └─> Subscribes to `opensbt_provisionSuccess`
   └─> Updates `TenantRegistration` to COMPLETED
   └─> Inserts record into `Tenants` table (Status: ACTIVE)
```

**Rule:** Your HTTP handlers must **never** wait for infrastructure to be provisioned. They write state to the database, emit an event, and return.

---

## 4. The "Status Controller" Pattern

To achieve a "Zero-Ops" environment, the Control Plane APIs must be incredibly fast and resilient. 

**Rule:** The Control Plane API must **NEVER** query the Kubernetes API or the ArgoCD API synchronously to fetch a tenant's status.

### 4.1 How to handle Status (The Right Way)
1. ArgoCD syncs resources and monitors their health.
2. ArgoCD is configured with Webhooks pointing to a lightweight receiver.
3. The receiver updates the PostgreSQL `tenants` table columns: `argo_sync_status` and `argo_health_status` (or emits an `EventArgoSyncCompleted` NATS event).
4. When a user queries `GET /api/v1/tenants/:id`, the API simply reads the `argo_health_status` directly from PostgreSQL.

### 4.2 How to handle Status (The Wrong Way)
```go
// ❌ WRONG: Do not do this in your API handlers or Provisioners
func GetProvisioningStatus(tenantID string) {
    // Making a live HTTP call to ArgoCD or Kubernetes during an API request
    resp, _ := http.Get("http://argocd-server/api/v1/applications/tenant-" + tenantID)
    return resp.Status
}
```

---

## 5. Event Naming & Structure

Events are the lifeblood of `open-sbt`. They are categorized by the **plane** that emits them.

### 5.1 Standard NATS Subjects

Format: `opensbt.{detailType}`

**Control Plane Events (Published by API/Control Plane):**
- `opensbt.opensbt_onboardingRequest`
- `opensbt.opensbt_offboardingRequest`
- `opensbt.opensbt_billingSuccess`
- `opensbt.opensbt_tenantUserCreated`

**Application Plane Events (Published by Provisioning Orchestrator):**
- `opensbt.opensbt_provisionSuccess`
- `opensbt.opensbt_provisionFailure`
- `opensbt.opensbt_deprovisionSuccess`

**Platform/Business Events (Published by your specific apps):**
- `opensbt.zeroops_agentDeployRequested`
- `opensbt.zeroops_agentHealthChanged`

### 5.2 Event Idempotency (The Inbox Pattern)

Because NATS guarantees at-least-once delivery, every event consumer **must** be idempotent.
Use the `IStorage.IsEventProcessed(eventID)` method to check if a NATS Message ID has already been handled before executing provisioning logic.

---

## 6. Service Classification: Where Does My Code Go?

When writing new code, use this guide to determine where it belongs.

### 6.1 `open-sbt` (The Core Framework)
*   **Tenant Management:** API for Registrations, Tenants, and Tiers.
*   **User Management:** Integration with Ory Kratos/Keto.
*   **Billing/Metering:** Standard interfaces (`IBilling`, `IMetering`) for Stripe/Amberflo/Moesif.
*   *Note: If a feature is specific to your SaaS product (like AI Agents), it DOES NOT go here.*

### 6.2 Platform Services (Your Business Domain)
*   **Agent Management (`agents-core`):** Manages Agent CRUD. Uses `open-sbt` interfaces to find out what tier a tenant is on, but the agent logic itself is domain-specific.
*   **MCP Server (`mcp-server`):** Provides platform capabilities via Model Context Protocol.

### 6.3 Application Plane Orchestrator (`open-sbt/applicationplane`)
*   **GitOps Committer:** Listens to `opensbt_onboardingRequest`, generates `values.yaml` for a tenant, commits to GitHub/GitLab.
*   **Warm Pool Manager:** Pre-generates "empty" tenant namespaces to speed up onboarding.

---

## 7. Pluggable Provider Interfaces

`open-sbt` relies heavily on interfaces so technologies can be swapped without rewriting business logic.

*   **`IAuth`**: Implemented by `ory` (Kratos/Hydra/Keto). Handles identities and relationship-based access control.
*   **`IEventBus`**: Implemented by `nats`. Handles async pub/sub.
*   **`IStorage`**: Implemented by `postgres` (with `sqlc`). Handles relational state and Row-Level Security (RLS).
*   **`IProvisioner`**: Implemented by `gitops`. Handles translating tenant requests into Git commits for ArgoCD.

**Rule of Thumb:** Use these interfaces when communicating with the infrastructure layer. *Do not* create interfaces for external third-party SaaS APIs (like a specific AI model registry); just use their client SDK directly.

---

## 8. Anti-Patterns to Avoid

### 8.1 ❌ Synchronous Provisioning
**Bad:** HTTP `POST /tenants` -> Create DB Record -> Create Kubernetes Namespace -> Return 200 OK.
**Good:** HTTP `POST /tenant-registrations` -> Create DB Record -> Publish Event -> Return 201 Created. Worker picks up event -> Creates Namespace -> Publishes Success Event.

### 8.2 ❌ Leaking Domain Logic into the Toolkit
**Bad:** Putting `Agent`, `Prompt`, or `LLMConfig` structs inside `open-sbt/models/`.
**Good:** Keeping `open-sbt/models/` restricted to `Tenant`, `User`, `Tier`, `Event`, `Meter`. Your domain models belong in your platform services.

### 8.3 ❌ Querying Live Infrastructure State
**Bad:** Calling the K8s API or ArgoCD API to check if a pod is running during an HTTP GET request.
**Good:** Webhooks updating a `last_observed_status` column in PostgreSQL. API reads directly from PostgreSQL.

### 8.4 ❌ Database Writes Bypassing RLS Context
**Bad:** `db.Exec("UPDATE custom_table SET val=1")`
**Good:** Always wrap database calls in the TenantDB wrapper to ensure `SET LOCAL app.tenant_id = $1` is executed, guaranteeing multi-tenant data isolation at the database engine level.

---
**Last Updated:** 2024-05-20
**Maintainer:** Platform Architecture Team
**Status:** Authoritative - Supersedes all previous documentation
```