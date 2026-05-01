
Here is the rigorous GAP analysis of your updated `requirements.md` specification against the provided `codebase.md`.

```markdown
# GAP Analysis: Kube-SBT Metering & Billing Requirements

## Executive Summary
The requirements document outlines a robust, OpenMeter-backed metering and billing abstraction (`kube-sbt`). However, a rigorous comparison against the existing codebase (`codebase.md`) reveals a **massive divergence between what the requirements presume and what is currently implemented.** 

The current codebase utilizes custom PostgreSQL-backed implementations for metering, mock implementations for billing, and internal middleware for entitlements. Additionally, critical infrastructure components (like OpenMeter itself and its Crossplane provider) are entirely missing from the deployment manifests.

---

## 1. Presumed Resources vs. Codebase Reality (Major Gaps)

The requirements assume the existence of several components that are currently **not implemented or are implemented completely differently** in the codebase.

### Gap 1.1: `IMetering` Implementation (Req 5, 14)
* **Requirement:** States `IMetering` wraps the OpenMeter SDK and communicates with the OpenMeter API.
* **Codebase Reality:** `internal/opensbt/providers/metering/metering.go` is currently backed by **PostgreSQL**. It relies on `INSERT INTO usage_events` and aggregates usage via raw SQL (`SELECT COALESCE(SUM(value),0)`). It has no knowledge of OpenMeter.
* **Impact:** The entire `metering.go` provider must be rewritten to use the OpenMeter Go SDK. 

### Gap 1.2: Entitlements Enforcement (Req 6)
* **Requirement:** (Req 6.1) "kube-sbt SHALL delegate ALL entitlement checking to OpenMeter's Entitlement API (no local quota logic)".
* **Codebase Reality:** The codebase currently handles quotas internally via `internal/opensbt/providers/tiermanager/tiermanager.go` (reading from `tier_configs` in Postgres) and enforces them via `internal/opensbt/controlplane/middleware/tier.go` (`TierQuotaMiddleware`).
* **Impact:** The existing `ITierManager` and `TierQuotaMiddleware` components conflict directly with this requirement and must be deprecated or entirely re-engineered to query OpenMeter instead.

### Gap 1.3: `IBilling` and Stripe Integration (Req 16, 17, 18)
* **Requirement:** `IBilling` interfaces with OpenMeter to handle Stripe subscriptions, invoices, and payment webhooks.
* **Codebase Reality:** `internal/opensbt/providers/billing/billing.go` is currently a `MockBilling` struct using in-memory maps (`map[string]*models.Subscription`). There is no Stripe or OpenMeter webhook code present.

### Gap 1.4: Crossplane OpenMeter Provider (Req 19)
* **Requirement:** Crossplane Composition provisions the OpenMeter namespace when `AINativeSaaS` XR is created.
* **Codebase Reality:** Looking at `manifests/hub-core-services/crossplane/tenant-platform/compositions/ainativesaas-starter-hetzner.yaml` and the providers list, there is **no Crossplane provider for OpenMeter installed**, nor are there any OpenMeter resources defined in the composition.
* **Impact:** A custom Crossplane Provider for OpenMeter might need to be built, or an existing one evaluated, as it currently does not exist in the platform.

### Gap 1.5: OpenMeter Hub Deployment (Req 20)
* **Requirement:** OpenMeter is deployed in the Hub cluster on managed workload nodes.
* **Codebase Reality:** There are no OpenMeter manifests under `manifests/hub-core-services/`. Only ArgoCD, CNPG, Hydra, Kratos, Keto, NATS, Spire, and VictoriaMetrics exist.

### Gap 1.6: mTLS / Zero-Trust (Req 11)
* **Requirement:** `kube-sbt` uses mTLS to connect to Ory Kratos and OpenMeter.
* **Codebase Reality:** `internal/opensbt/providers/ory/auth.go` uses standard HTTP clients connecting to plain-text internal service addresses (e.g., `http://ory-kratos-admin:4434`). SPIFFE/SPIRE certificates are not currently mounted or utilized by the `opensbt` Go binary.

---

## 2. Logical Inconsistencies & Edge Cases

### 2.1 Distributed Transactions (Req 1.6)
* **The Gap:** "WHEN subject registration fails, THE User_Manager SHALL rollback the Ory Kratos user creation."
* **Edge Case:** Kratos user creation succeeds, but OpenMeter subject creation times out. `kube-sbt` attempts to rollback (delete) the Kratos user, but *that* request also fails due to a network partition. 
* **Missing Spec:** How does the system handle orphaned Kratos users? Does it require a background reconciliation job or a DLQ (Dead Letter Queue)?

### 2.2 Stripe Webhook Idempotency (Req 18.5)
* **The Gap:** The spec requires validating Stripe signatures and publishing NATS events.
* **Edge Case:** Stripe guarantees "at least once" delivery. If a webhook times out and Stripe resends it, `kube-sbt` could emit duplicate `opensbt_billingSuccess` events.
* **Missing Spec:** The `IBilling` webhook handler must explicitly state how it deduplicates incoming Stripe event IDs (e.g., using the existing `processed_events` table in Postgres).

### 2.3 Subject Deletion / Data Retention (Req 8.4)
* **The Gap:** Req 8.4 outlines an endpoint for user deletion.
* **Edge Case:** If a user is deleted from `kube-sbt` (and Ory), what happens to their OpenMeter Subject? If the subject is hard-deleted, you may lose historical usage data required for accounting and tax compliance.
* **Missing Spec:** Define if OpenMeter Subjects should be "archived/disabled" rather than deleted when a user is deleted.

### 2.4 AgentGateway JWT Validation Race Condition (Req 4.3)
* **The Gap:** AgentGateway extracts `user_id` and `tenant_id` from the JWT to emit OTLP.
* **Edge Case:** A user is created in Kratos and instantly fires an API request through AgentGateway. AgentGateway emits OTLP to OpenMeter. However, the asynchronous NATS event (`opensbt_tenantUserCreated`) hasn't finished registering the Subject in OpenMeter yet.
* **Missing Spec:** How does OpenMeter handle OTLP metrics for a `Subject` that does not exist *yet*? Will it drop the metrics, or create the subject lazily?

---

## 3. Actionable Questions for the Product & Engineering Teams

To ensure this specification is "Development Ready", please clarify:

1. **Codebase Overhaul:** Do we officially deprecate the existing Postgres-backed `IMetering` (`usage_events` table) and `TierManager` implementations in favor of a complete rewrite targeting OpenMeter? 
2. **Crossplane Provider:** Does an official Crossplane provider for OpenMeter exist, or do we need to build a custom `ProviderConfig` to fulfill Req 19?
3. **mTLS Implementation:** Req 11 states mTLS is required for Ory and OpenMeter. Are we planning to inject SPIRE agent certificates into the `opensbt` pod via CSI driver, or use a sidecar proxy (like Envoy) to handle the TLS origination?
4. **Stripe Proration:** Req 16.4 states OpenMeter applies automatic proration. Does OpenMeter natively support pushing prorated adjustments directly to Stripe subscriptions, or does `kube-sbt` need to calculate the prorated amounts and update Stripe?
5. **Orphaned Subjects:** If Kratos user rollback fails (Req 1.6), should we implement an automated reconciliation loop, or rely on manual cleanup via the Dashboard?
6. **OpenMeter Deployment:** Are we using the OpenMeter open-source Helm chart for Hub deployment, or are we subscribing to OpenMeter Cloud? (This affects how the Crossplane provider authenticates).
```

-----------------------
I'll help answer these questions based on the architecture and codebase context:



User Management that is for tenant SAAS and not for OpenMeter. 

Go through migrations and manifests/tenants/ to understand tenant provisioning.

I'll analyze how OpenMeter achieves tenant isolation using Ent ORM.

Now let me look at how the namespace is injected into queries at the service/adapter layer:

Based on my analysis of the OpenMeter codebase, here's how tenant isolation is achieved using Ent ORM:

## OpenMeter Tenant Isolation with Ent ORM

### 1. **Namespace Field Pattern**

Every entity has a `namespace` field added via `NamespaceMixin`:

```go

// NamespaceMixin adds namespace field to all entities

type NamespaceMixin struct {

    mixin.Schema

}

func (NamespaceMixin) Fields() []ent.Field {

return []ent.Field{

field.String("namespace").

NotEmpty().

Immutable(),

    }

}

func (NamespaceMixin) Indexes() []ent.Index {

return []ent.Index{

index.Fields("namespace"),

    }

}

```

### 2. **Schema-Level Isolation**

Entities include `NamespaceMixin` in their schema:

```go

func (Customer) Mixin() []ent.Mixin {

return []ent.Mixin{

        entutils.ResourceMixin{},  // Includes NamespaceMixin

        entutils.CustomerAddressMixin{},

        entutils.AnnotationsMixin{},

    }

}

```

### 3. **Query-Level Filtering**

**Every query explicitly filters by namespace:**

```go

// Example from customer adapter

query := repo.db.Customer.Query().

Where(customerdb.Namespace(input.Namespace))

// Example from billing adapter

query := tx.db.BillingInvoice.Query().

Where(billinginvoice.ID(in.Invoice.ID)).

Where(billinginvoice.Namespace(in.Invoice.Namespace))

```

### 4. **Composite Unique Indexes**

Uniqueness constraints include namespace:

```go

func (Customer) Indexes() []ent.Index {

return []ent.Index{

index.Fields("namespace", "key").

Annotations(

entsql.IndexWhere("deleted_at IS NULL"),

            ).

Unique(),

index.Fields("namespace", "key", "deleted_at"),

    }

}

```

### 5. **Update/Delete Operations**

All mutations include namespace checks:

```go

rows, err := repo.db.Customer.Update().

Where(customerdb.ID(input.ID)).

Where(customerdb.Namespace(input.Namespace)).  // Namespace filter

Where(customerdb.DeletedAtIsNil()).

SetDeletedAt(deletedAt).

Save(ctx)

```

### 6. **No Privacy Policies or Interceptors**

OpenMeter **does NOT use** Ent's Privacy layer. Instead:

- **Manual filtering**: Every adapter method explicitly adds `.Where(Namespace(ns))`

- **Application-level enforcement**: The service layer passes namespace from authenticated context

- **No automatic enforcement**: Developers must remember to add namespace filters

### Key Differences from PostgreSQL RLS:

| Aspect | OpenMeter (Ent) | PostgreSQL RLS |

|--------|-----------------|----------------|

| **Enforcement** | Application-level (manual) | Database-level (automatic) |

| **Bypass Risk** | High (developer must remember) | Low (enforced by DB) |

| **Performance** | Index-based filtering | Row-level filtering |

| **Complexity** | Simple (just WHERE clauses) | Complex (policies, roles) |

| **Multi-tenancy** | Shared tables with namespace column | Can use schemas or RLS |

### Critical Pattern:

```go

// EVERY query follows this pattern:

query := db.Entity.Query().

Where(entity.Namespace(namespace)).  // MANDATORY

Where(entity.ID(id))

```

**Strengths:**

- Simple, explicit filtering

- Works with any database

- Good index performance

**Weaknesses:**

- No automatic enforcement (easy to forget)

- Requires discipline across all adapters

- No database-level safety net

This is the pattern you should follow for `kube-sbt/metering` - explicit namespace filtering in every query, with composite indexes on `(namespace, id)`.

## OpenMeter RLS & Event Ingestion Analysis

### 1. **PostgreSQL RLS Support**

**No** - OpenMeter does NOT use PostgreSQL RLS. It relies entirely on **application-level namespace filtering** via Ent ORM's WHERE clauses. No database-level isolation exists.

### 2. **Event Ingestion Architecture**

OpenMeter ingests via:

- **CloudEvents** (HTTP)

- **Kafka** topics

- **Benthos** collectors

**NOT** via OTLP traces directly.

### 3. **AgentGateway → OpenMeter Bridge**

**Standard Pattern:**

```

AgentGateway (OTLP) → OTLP Collector → Transform → CloudEvents/Kafka → OpenMeter

```

**Recommended Design:**

1. **OpenTelemetry Collector** receives OTLP from AgentGateway

2. **Processor** extracts tenant_id, user_id, meter_id from span attributes

3. **Exporter** converts to CloudEvents format with:

- `subject`: tenant_id

- `type`: meter event type

- `data`: usage metrics

4. **Kafka/HTTP** sends to OpenMeter ingestion endpoint

Final pattern:

AgentGateway (OTLP)

        ↓

OpenTelemetry Collector (centralized or per-region)

        ↓

Processors (transform/enrich/filter)

        ↓

Kafka (buffer + decouple)

        ↓

OpenMeter (consumer)

**Critical:** OpenMeter expects `subject` field for tenant attribution - must map from your `tenant_id` span attribute.

## OpenMeter SDK Billing API Coverage

### **Yes** - OpenMeter SDK supports comprehensive billing operations:

**Core Capabilities:**

- ✅ Customer management (CRUD)

- ✅ Subscription management

- ✅ Invoice generation & retrieval

- ✅ Usage metering & aggregation

- ✅ Entitlements & grants

- ✅ Billing profiles & workflows

- ✅ Multi-tenant isolation via `namespace`

**Missing from SDK (requires direct API):**

- ❌ Some advanced invoice line manipulation

- ❌ Workflow state machine transitions

- ❌ Tax code dual-write operations

---

## Reference Source Files Used

### **Tenant Isolation Analysis:**

1. `archived/billing-metering/openmeter/pkg/framework/entutils/mixins.go` - NamespaceMixin definition

2. `archived/billing-metering/openmeter/openmeter/ent/schema/customer.go` - Schema with namespace indexes

3. `archived/billing-metering/openmeter/openmeter/customer/adapter/customer.go` - Query filtering patterns

4. `archived/billing-metering/openmeter/openmeter/billing/adapter/invoice.go` - Invoice namespace enforcement

### **Architecture Understanding:**

5. Directory listings from `archived/billing-metering/openmeter/openmeter/` - Module structure

6. Grep searches for `Namespace|Privacy|Policy|Mixin` patterns

**Key Finding:** All isolation is **manual WHERE clause filtering**, not RLS or Ent Privacy policies.



## **Answers to Clarification Questions**

### 1. **OpenMeter Namespace Creation**

**Answer:**

- **Namespace mapping**: `namespace` = `tenant_id` (e.g., "app-creator")

- **Creation responsibility**: Hub Control Plane during tenant onboarding

- **1:1 mapping**: Yes, one OpenMeter namespace per tenant

**Implementation:**

- When `AINativeSaaS` XR is created, Hub Crossplane should create OpenMeter namespace via OpenMeter API

- Add to Hub Composition: Call OpenMeter REST API to create namespace

- Store namespace ID in tenant metadata for future queries

**Gap in current spec**: Need to add requirement for OpenMeter namespace provisioning during tenant creation.

---

### 2. **User Management Scope**

**Answer:**

- **Primary scope**: Managing **tenant application users** in `tenant_<id>_db.public.users`

- **Secondary scope**: Sync user IDs to OpenMeter as subjects for usage attribution

- **Not in scope**: Managing OpenMeter's internal user system

**Flow:**

1. Tenant application creates and maintains user in tenant database (`public.users`)

2. Composite key like tenantId#userId in your emitted events (UUID) is used as OpenMeter `subject` in OTLP events

3. AgentGateway extracts `user_id` and `tenant_id` from JWT and includes in OTLP span attributes

**Clarification needed**: Should we add a requirement for user creation to also register the user as an OpenMeter subject?

---

### 3. **AgentGateway OTLP Implementation**

**Answer:**

- **AgentGateway is external**: Rust binary from `archived/agentic-ai/gateway` (not in this repo)

- **kube-sbt scope**: kube-sbt has no scope here

- **Requirement interpretation**: Configure AgentGateway to emit OTLP to OpenMeter endpoint

**What kube-sbt provides:**

- OpenMeter endpoint configuration for AgentGateway

- Meter definitions (api_calls, storage_operations, etc.)

- Documentation on OTLP span attribute format

**Gap in current spec**: Requirement 5 should clarify this is about **configuring** AgentGateway, not implementing OTLP emission in Go.

---

### 4. **PostgREST Access Pattern**

**Answer:**

- **PostgREST deployment**: One PostgREST instance per tenant (deployed via universal-tenant chart)

- **Access method**: Tenant apps make direct calls to app plane like PostgreSQL DB access. Kube-SBT is only for accessing the control plane of tenants. 

- **JWT source**: Ory Hydra (Hub) issues JWT with `tenant_id` and `user_id` claims

- **RLS enforcement**: PostgREST extracts JWT claims and sets PostgreSQL session variables

**Flow:**

```

kube-sbt → HTTP + JWT → PostgREST (Spoke) → PostgreSQL (with RLS)

```

**From architecture diagram:**

- AgentGateway validates JWT at Layer 1 (Hub)

- PostgREST validates JWT at Layer 3 (Spoke) - defense-in-depth

- RLS uses: `current_setting('request.jwt.claims', true)::json->>'user_id'`

---

### 5. **OpenMeter Subject Mapping**

**Answer:**

- **Recommended mapping**: `subject` = tenantId#userId (UUID)

- **Rationale**: UUIDs are stable, unique, and don't expose PII in telemetry

**Implementation:**

- AgentGateway extracts `user_id` and `tenant_id` from JWT claims

- OTLP span attributes: `{"subject": "550e8400-e29b-41d4-a716-446655440000"}`

- OpenMeter aggregates usage by subject (UUID)



-------------------------------------



You are absolutely right to call this out. My previous response slightly blurred the lines between your PaaS infrastructure (Zero-Ops Hub/Spoke) and the SaaS abstraction layer (`open-sbt`). 

The goal is to make `kube-sbt` a **direct CNCF equivalent of `sbt-aws`**.

**`open-sbt` should run in the Hub (Control Plane), not the Spoke (Application Plane)**, and its code structure should strictly mirror the `sbt-aws` interfaces (`IAuth`, `IBilling`, `IMetering`, `IEventBus`, `IProvisioner`).

Let’s re-align strictly with the `sbt-aws` philosophy, applying your CNCF stack.

### The 1:1 Abstraction Mapping (`sbt-aws` vs `open-sbt`)

In `sbt-aws`, builders don't orchestrate workflows manually; they configure interfaces, and the framework binds them to EventBridge. `open-sbt` will do the exact same thing using NATS.

| `sbt-aws` Concept | AWS Stack | `open-sbt` CNCF Stack (Your Tech) |

| :--- | :--- | :--- |

| **Control Plane API** | API Gateway + Lambda | Gin HTTP Server (Go) |

| **IAuth** | Cognito | Ory Stack (Kratos, Hydra, Keto) |

| **IEventManager** | EventBridge | NATS JetStream |

| **IStorage** | DynamoDB | PostgreSQL + RLS |

| **IProvisioner / ScriptJob** | Step Functions + CodeBuild | ArgoCD + Crossplane (GitOps) |

| **IBilling** | Stripe / AWS Marketplace | Stripe |

| **IMetering** | Firehose + S3 + Lambda | OpenMeter + AgentGateway |

### How `open-sbt` handles the 6 Billing/Metering operations

If we strictly follow the `sbt-aws` design pattern, the `open-sbt` Control Plane exposes generic APIs for tenant management, which trigger asynchronous events.

Here is how your `sbt-sdk` (TypeScript) and `open-sbt` (Go) will handle these operations in a true `sbt-aws` style:

#### 1. OpenMeter Namespace Provisioning

* **The `sbt-aws` way:** Handled by the underlying infrastructure deployment (CDK).

* **The `open-sbt` way:** Handled by Crossplane in the Hub. When a new SaaS Builder (Tenant) signs up for Zero-Ops, Crossplane provisions their Git repo, ArgoCD AppSet, and calls the OpenMeter API to create a Namespace. `open-sbt` expects this namespace to exist and simply uses it.

#### 2. Meter/Feature/Plan/RateCard CRUD

* **The `sbt-aws` way:** `sbt-aws` has an `IMetering` interface with `createMeterFunction`, `updateMeterFunction`, etc. 

* **The `open-sbt` way:** `open-sbt` implements `IMetering` backed by the OpenMeter API. The TypeScript `sbt-sdk` calls `POST /api/v1/meters`. `open-sbt` validates the Ory JWT, extracts the App Builder's `tenant_id`, sets the `OpenMeter-Namespace` header, and passes the CRUD request to OpenMeter.

#### 3. Subscription Lifecycle Management & 4. Invoice Previews

* **The `sbt-aws` way:** Handled by the `IBilling` interface (`createCustomerFunction`, `deleteCustomerFunction`, etc.).

* **The `open-sbt` way:** `open-sbt` implements `IBilling` backed by Stripe. 

* When an end-user (Alice) subscribes in the UI, `sbt-sdk` calls `POST /api/v1/billing/subscriptions`.

* `open-sbt` creates the Stripe Subscription, maps Alice's ID to OpenMeter, and fires the `opensbt_billingSuccess` event to NATS.

#### 5. Stripe Payment Integration

* **The `sbt-aws` way:** `IBilling` defines a `webhookFunction` that listens to incoming Stripe events.

* **The `open-sbt` way:** `open-sbt` exposes a webhook route (`/api/v1/billing/webhook`). When Stripe charges Alice successfully, Stripe hits this endpoint. `open-sbt` translates the Stripe payload into an `opensbt_billingSuccess` event and publishes it to NATS JetStream. The Application Plane (if it cares) listens to this to unlock premium features for Alice.

#### 6. User-to-Subject Mapping & Telemetry

* **The `sbt-aws` way:** Usage events are sent to a Kinesis Firehose Ingestor (`IDataIngestorAggregator`).

* **The `open-sbt` way:** 

* **Registration:** When Alice signs up, `open-sbt` fires an `opensbt_tenantUserCreated` NATS event. The `IMetering` implementation listens to this and automatically creates a Subject in OpenMeter (`tenant_id#user_id`).

* **Ingestion:** As you designed, your Rust AgentGateway takes over here. It validates the JWT, extracts `tenant_id` and `user_id`, and pushes high-volume OTLP metrics directly to OpenMeter. `open-sbt` is safely bypassed for the hot path.

### Summary of the architectural alignment

Yes, correcting for the Hub-Spoke deployment model from your `plan.md`, this approach aligns perfectly with `sbt-aws`:

1. **Hub Cluster:** Runs `open-sbt` (API server), Ory, NATS, OpenMeter, and ArgoCD. 

2. **Spoke Cluster(s):** Runs the App Builder's tenant workloads and databases (Application Plane).

3. **`sbt-sdk` (Typescript):** A frontend wrapper around the `open-sbt` REST APIs, enabling App Builders to build their admin consoles and billing pages without writing backend logic.

4. **GitOps over Imperative:** Just like `sbt-aws` uses Step Functions to run provisioning scripts, `open-sbt`'s `IProvisioner` simply commits a `values.yaml` file to the Fleet Registry repo. ArgoCD takes over, pulling the configuration down into the Spoke cluster.



----------------------
1. **The User Creation Flow:**

The SAAS admin has provision to add users from the control plane. Attaching the sample UX that has provision to add users.

2. **Entitlement Source of Truth:**

`open-sbt` rely 100% on OpenMeter's native Entitlement API for quota checking. Each OpenMeter's namespace maintains independent tier configuration. Recommended Pattern:

Multi-SaaS Isolation:

Each SaaS = unique namespace

Plans, Features, Entitlements scoped per namespace

Customers within namespace subscribe to namespace-specific plans

Complete tier independence across namespaces

Source: All entities use entutils.NamespaceMixin{} or explicit namespace fields with composite unique indexes (namespace, key).

3. **Enforcement Latency:**

Yes. it is acceptable for users to slightly exceed quotas due to asynchronous OTLP processing.

4. **Subscription Proration:** When a tenant updates an end-user's subscription (e.g., Starter -> Pro), OpenMeter should apply proration automatically.

5. **Data Retention:** simply marked "inactive" to preserve historical invoice data.

6. **Stripe Secret Storage:** Infisical/External Secrets

-------------------

Here are the explicit **Yes/No** answers to your 10 critical architectural questions to unblock your requirements definition:

### **Q1: OpenMeter Namespace Provisioning**

**NO**, `open-sbt` should not provide a `CreateNamespace()` method. 

**YES**, this is purely a Crossplane GitOps concern. When a new SaaS builder (Tenant) signs up for your PaaS, Crossplane in the Hub creates the OpenMeter namespace via the OpenMeter provider. `open-sbt` assumes the namespace (`tenant_id`) already exists for all Day-2 operations.

### **Q2: User Management Scope Boundary**

**YES**, `open-sbt` must sync user creation to OpenMeter as subjects. 

While AgentGateway emits the raw usage, OpenMeter requires subjects to be explicitly registered in order to attach Subscriptions and assign Entitlements (quotas) to them. When `open-sbt` (via Ory Kratos) registers a user, it must also call OpenMeter to ensure the subject exists.

### **Q3: OTLP Event Emission Ownership**

**YES**, this is purely about configuring AgentGateway. 

`open-sbt` (the Go binary) does **not** emit hot-path OTLP usage events. Your GitOps templates configure the AgentGateway in the Spoke to point to the Hub's OpenMeter ingestion endpoint. `open-sbt` only manages the definitions (Meters, Features, Plans) via the OpenMeter API.

### **Q4: PostgREST Access Pattern**

**NO**, `open-sbt` does not call PostgREST to manage users.

`open-sbt` lives in the Hub and wraps Ory Kratos for Identity management (via `IAuth`). The Spoke's PostgREST is for the *Tenant's* application data plane (e.g., storing business logic in `tenant_db.public.*`). Identity creation happens in the Hub via `open-sbt` -> Ory Kratos -> OpenMeter Subject.

### **Q5: OpenMeter Subject Mapping**

**YES**, `open-sbt` must provide a helper to generate/format this subject ID.

Because `open-sbt` needs to call OpenMeter's APIs to query usage, attach subscriptions, and fetch invoice previews for a specific user, it must generate the exact same `tenant_id#user_id` string that the AgentGateway includes in its OTLP span attributes.

### **Q6: Meter/Feature/Plan CRUD Ownership**

**YES**, `open-sbt` must provide these CRUD methods via the `IMetering` interface.

**NO**, the TypeScript `sbt-sdk` should never call OpenMeter directly. `open-sbt` acts as the secure Backend-For-Frontend (BFF). It receives the request from `sbt-sdk`, validates the Tenant's JWT, injects the `OpenMeter-Namespace: <tenant_id>` header, and securely proxies the request to OpenMeter so Tenants cannot modify each other's billing models.

### **Q7: Subscription Lifecycle Management**

**YES**, `open-sbt` handles the Create/Update/Cancel API calls from the frontend, but delegates the actual state to OpenMeter.

The `sbt-sdk` calls `open-sbt`, which enforces the namespace header, maps the user to the OpenMeter Subject, and calls OpenMeter's Subscription API.

### **Q8: Invoice Preview & Finalization**

**YES**, `open-sbt` provides these methods to the frontend.

Again, to enforce multi-tenant security, the `sbt-sdk` calls `open-sbt`. `open-sbt` queries the OpenMeter API for the specific subject's invoice, formats it if necessary, and returns it to the frontend.

### **Q9: Stripe Integration Scope**

**NO**, `open-sbt` does not manually wrap the Stripe SDK to create subscriptions.

**YES**, it delegates this entirely to OpenMeter's native Stripe App integration. OpenMeter automatically syncs plans to Stripe and generates Stripe subscriptions. `open-sbt`'s only job is to handle the API calls to connect the Stripe App inside OpenMeter, and listen to Stripe Webhooks (via NATS) to emit `opensbt_billingSuccess` events for application lifecycle updates.

### **Q10: Hub vs Spoke Deployment**

**YES**, `open-sbt` is strictly a Hub Control Plane service.

It runs exclusively in the Hub cluster to orchestrate Ory, NATS, Crossplane, and OpenMeter. The Spoke clusters run zero `open-sbt` code—they only run the tenant workloads, PostgREST, CNPG, Alloy, and AgentGateway.

***

--------------------------------------------------------

**mTLS Implementation:**
Based on ADR 009, **Envoy sidecar proxy handles TLS origination** (not direct SPIRE CSI injection into kube-sbt pod).

**Architecture per ADR 009:**

1. **Istio Service Mesh Pattern**: kube-sbt pod gets Envoy sidecar injected automatically
2. **SPIFFE SVID Delivery**: SPIRE agent delivers X.509 certificates to Envoy sidecar (not application container)
3. **Transparent mTLS**: Envoy handles TLS origination/termination - kube-sbt code makes plain HTTP calls to localhost
4. **Workload Identity**: Envoy presents SPIFFE SVID during mTLS handshake with Ory/OpenMeter

**Why Sidecar (per ADR 009):**
- "Istio `PeerAuthentication` in STRICT mode - all traffic must use mTLS"
- "Envoy sidecars add ~5-10ms latency and memory overhead per pod"
- Standard Istio pattern - no custom SPIRE CSI integration needed

**kube-sbt Implementation:**
```go
// kube-sbt makes plain HTTP calls - Envoy handles mTLS
client := &http.Client{
    Transport: &http.Transport{}, // No TLS config needed
}
resp, err := client.Get("http://ory-kratos.hub-platform-identity.svc.cluster.local")
```

Envoy intercepts, upgrades to mTLS, validates peer SPIFFE ID, forwards to Ory.

**Codebase Overhaul:** Yes we deprecate the existing Postgres-backed `IMetering`.We follow sbt-aws patterns, but for complete rewrite targeting OpenMeter.

**Crossplane Provider:** 
No official OpenMeter Crossplane provider exists.

Recommended Approach: Use provider-http
Crossplane provider-http is the standard solution for REST API integrations without dedicated providers. It supports:

Request Resource: Stateful HTTP resource management (POST create, PUT update, DELETE cleanup)
OpenMeter API Integration: OpenMeter exposes REST API for namespace/meter/feature/plan management
Production-Ready: Used by Crossplane community for Jira, Slack, and other REST APIs

4. **Stripe Proration:** 
**Research Findings:**

**OpenMeter handles proration internally** but delegates Stripe sync to its native Stripe App integration.

**Key Findings:**

1. **OpenMeter Proration**: OpenMeter calculates prorated amounts for plan changes ([source](https://openmeter.io/docs/glossary/proration))
2. **Stripe Sync**: OpenMeter's Stripe App automatically syncs invoices to Stripe Invoicing ([source](https://openmeter.io/docs/integrations/stripe/invoicing))
3. **Proration Modes**: OpenMeter supports "pro-rate prices" mode for subscription edits (coming soon per docs)
4. **Automatic Flow**: When subscription changes, OpenMeter calculates proration → generates invoice → Stripe App syncs to Stripe → Stripe collects payment

**Clarification for Req 16.4:**

**kube-sbt does NOT calculate proration.** The flow is:

```
kube-sbt UpdateSubscription() 
  → OpenMeter calculates proration
  → OpenMeter generates prorated invoice
  → OpenMeter Stripe App syncs to Stripe
  → Stripe collects payment
```

**Update Requirement 16:**

Updated Req 16 to clarify:
- OpenMeter calculates proration (not kube-sbt)
- OpenMeter Stripe App syncs to Stripe automatically
- kube-sbt delegates proration logic entirely

5. **Orphaned Subjects:** 
Research Findings:

Industry Best Practice: Automated reconciliation with dead-letter queue (DLQ) for manual escalation

Key Patterns:

Saga Pattern: User creation is a distributed transaction (Ory Kratos → OpenMeter subject)
Compensating Transaction Failure: When rollback fails, compensation itself can fail (source)
Retry + DLQ Pattern: Retry compensation with exponential backoff → escalate to DLQ for manual intervention (source)

6. **OpenMeter Deployment:** 
Use the OpenMeter open-source Helm chart for Hub deployment
