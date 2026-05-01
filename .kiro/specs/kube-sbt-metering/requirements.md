# Requirements Document

## Introduction

This document specifies requirements for the kube-sbt metering and billing system. The system provides Go-based abstractions (IMetering, IBilling) for multi-tenant SaaS platforms, wrapping OpenMeter for usage metering, subscriptions, and invoicing. It runs in the Hub cluster as a secure Backend-For-Frontend (BFF), enforcing tenant isolation via namespace injection and providing subject management for usage attribution.

**Scope:** This is a complete rewrite of the existing Postgres-backed IMetering implementation, following AWS SBT patterns but targeting OpenMeter as the backend. The existing implementation is deprecated.

## Architectural Approach: Operator Pattern (ADR 012)

**CRITICAL DESIGN DECISION:** The billing catalog (Meters, Features, Plans) is managed via **GitOps + Kubernetes Operators**, NOT via REST API.

### Static Catalog (GitOps + hub-operator)

**Managed via Kubernetes Custom Resources:**
- `Meter` (billing.nutgraf.in/v1alpha1) - Usage metric definitions
- `Feature` (billing.nutgraf.in/v1alpha1) - Billable capabilities
- `Plan` (billing.nutgraf.in/v1alpha1) - Pricing tiers and rate cards

**Lifecycle:**
1. SaaS Builder defines billing configuration in `fleet-registry/tenants/<tenant-id>/values.yaml`
2. ArgoCD renders `universal-tenant` Helm chart, creating CRs in Hub cluster
3. `hub-operator` reconciles CRs, syncing to OpenMeter via Go SDK
4. Changes tracked in Git with full audit trail

**Rationale:**
- Billing catalog is infrastructure configuration, not runtime data
- GitOps provides version control, audit trail, and rollback safety
- Consistent with platform patterns (Crossplane, Operators)
- Eliminates complex saga patterns for catalog mutations

### Dynamic Runtime Data (kube-sbt REST API)

**Managed via REST API:**
- User/Subject registration and deletion
- Subscription creation, updates, and cancellation
- Usage queries and entitlement checks
- Invoice operations

**Lifecycle:**
1. End-users interact with SaaS application
2. Application calls `kube-sbt` REST API
3. `kube-sbt` performs runtime operations against OpenMeter
4. No Git involvement (ephemeral runtime state)

**See ADR 012 for complete architectural rationale and implementation details.**

## Glossary

- **kube-sbt**: Kubernetes-based SaaS Builder Toolkit providing abstractions for multi-tenant platforms (CNCF equivalent of AWS SBT)
- **Tenant**: A SaaS builder using the Zero-Ops platform, isolated via OpenMeter namespace
- **Subject**: An end-user of a tenant's application, identified by `{tenant_id}#{user_id}` in OpenMeter
- **OpenMeter**: Real-time usage metering and billing engine with subscription/invoice management
- **Namespace**: OpenMeter's tenant isolation mechanism, maps 1:1 with tenant_id
- **Meter**: A usage metric definition (e.g., "api_calls", "storage_mb")
- **Feature**: A billable capability mapped to one or more meters
- **Plan**: A pricing tier with rate cards defining usage-based or flat pricing
- **Entitlement**: A quota limit associated with a meter for a specific plan
- **Subscription**: A subject's active plan assignment with billing lifecycle
- **AgentGateway**: External Rust service emitting OTLP traces to OpenMeter (configured, not implemented by kube-sbt)
- **Hub**: Management cluster running kube-sbt, OpenMeter, Ory, NATS, Crossplane
- **Spoke**: Application cluster running tenant workloads (no kube-sbt code)
- **OTLP**: OpenTelemetry Protocol for telemetry data transmission
- **IMetering**: kube-sbt interface for usage queries and read-only catalog access
- **IBilling**: kube-sbt interface for subscription/invoice operations
- **hub-operator**: Kubernetes operator managing billing catalog CRs and syncing to OpenMeter
- **GitOps**: Declarative infrastructure management via Git as single source of truth

## Requirements

### Requirement 1: User Identity and Subject Registration

**User Story:** As a SaaS admin, I want to create users via REST API, so that I can manage my application's end-users programmatically.

#### Acceptance Criteria

1. THE User_Manager SHALL integrate with Ory Kratos (IAuth) for identity management
2. WHEN a user is created via REST API, THE User_Manager SHALL register the user as an OpenMeter subject
3. THE Subject_ID SHALL use format `{tenant_id}#{user_id}` where both are UUIDs
4. THE User_Manager SHALL provide a GenerateSubjectID(tenantID, userID) helper method
5. THE Subject registration SHALL include tenant namespace in OpenMeter API call
6. WHEN subject registration fails, THE User_Manager SHALL rollback the Ory Kratos user creation
7. THE User_Manager SHALL support querying subjects by tenant namespace

### Requirement 2: Tenant-Scoped Usage Metrics

**User Story:** As a tenant administrator, I want to view aggregate usage metrics for my entire tenant, so that I can monitor overall consumption against my plan limits.

#### Acceptance Criteria

1. THE Metering_Service SHALL provide a GetUsage(tenantID) method returning tenant-level aggregated metrics
2. THE Metering_Service SHALL query OpenMeter API for tenant-scoped usage data
3. THE Metering_Service SHALL return metrics for: Applications (count), Records (count), Storage (MB), Portal_Users (count)
4. THE Metering_Service SHALL support time-period filtering (daily, monthly, custom range)
5. THE Metering_Service SHALL return usage data paired with limits from tier configuration
6. WHEN OpenMeter API is unreachable, THE Metering_Service SHALL return an error with retry guidance

### Requirement 3: User-Scoped Usage Metrics

**User Story:** As a tenant administrator, I want to view usage metrics for individual users, so that I can track per-user consumption and identify high-usage accounts.

#### Acceptance Criteria

1. THE Metering_Service SHALL provide a GetUsage(tenantID, userID) method returning user-level metrics
2. THE Metering_Service SHALL query OpenMeter API with user-scoped filters
3. THE Metering_Service SHALL return metrics for: Custom_APIs (count), Developer_APIs (count), Batch_Blocks (count), AI_Calls (count)
4. THE Metering_Service SHALL support time-period filtering (daily, monthly, custom range)
5. THE Metering_Service SHALL return usage data paired with limits from tier configuration
6. WHEN a userID does not exist in the tenant, THE Metering_Service SHALL return an empty usage report

### Requirement 4: AgentGateway OTLP Configuration and Spoke-Side Metric Collection

**User Story:** As a platform operator, I want AgentGateway to emit OTLP traces for HTTP requests and spoke-side metric collectors to emit stateful metrics (database rows, storage, workspaces), so that both API usage and domain-specific quotas are automatically metered.

#### Acceptance Criteria

1. THE Platform_Configuration SHALL configure AgentGateway (external Rust binary) to emit OTLP to OpenMeter endpoint
2. THE AgentGateway_Config SHALL specify OpenMeter ingestion endpoint URL in Hub cluster
3. THE AgentGateway SHALL extract tenant_id and user_id from JWT claims and include in OTLP span attributes
4. THE AgentGateway SHALL format subject as `{tenant_id}#{user_id}` in span attributes
5. THE AgentGateway SHALL emit meter events for: api_calls, storage_operations, custom_api_calls, developer_api_calls
6. THE kube-sbt documentation SHALL provide OTLP span attribute format specification
7. THE kube-sbt SHALL NOT implement OTLP emission (AgentGateway responsibility)
8. THE universal-tenant Helm chart SHALL provision per-tenant metric collector CronJobs in Spoke clusters
9. THE Metric collectors SHALL query tenant databases for stateful metrics: database_rows, storage_bytes, workspace_count, form_count, table_count
10. THE Metric collectors SHALL emit OTLP gauge metrics to local OpenTelemetry Collector with tenant_id and meter_id attributes
11. THE OpenTelemetry Collector (Spoke) SHALL forward metrics to OpenMeter (Hub) via mTLS
12. THE Metric collector schedule SHALL be configurable per tenant (default: */5 * * * * - every 5 minutes)
13. THE Metric collector SHALL support tenant-specific metric selection via Helm values (metering.metrics array)

### Requirement 5: OpenMeter Integration for Queries

**User Story:** As a platform operator, I want the metering service to query OpenMeter, so that usage data is centrally tracked and queryable.

#### Acceptance Criteria

1. THE OpenMeter_Provider SHALL implement the IMetering interface
2. THE OpenMeter_Provider SHALL authenticate to OpenMeter API using configured credentials
3. THE OpenMeter_Provider SHALL query usage data via OpenMeter REST API endpoints
4. THE OpenMeter_Provider SHALL parse OpenMeter responses into internal UsageData models
5. WHEN OpenMeter returns rate limit errors, THE OpenMeter_Provider SHALL implement exponential backoff retry
6. THE OpenMeter_Provider SHALL support querying multiple meters in a single request

### Requirement 6: Dynamic Entitlements System

**User Story:** As a SaaS builder, I want quota enforcement via OpenMeter's native Entitlement API, so that each namespace maintains independent tier configuration.

#### Acceptance Criteria

1. THE kube-sbt SHALL delegate ALL entitlement checking to OpenMeter's Entitlement API (no local quota logic)
2. THE IMetering interface SHALL provide CheckEntitlement(namespace, subjectID, featureKey) method
3. THE Entitlement check SHALL query OpenMeter API with namespace and subject filters
4. THE OpenMeter namespace SHALL maintain independent plans, features, and entitlements
5. THE kube-sbt SHALL support eventual consistency (users may slightly exceed quotas due to async OTLP processing)
6. THE Entitlement response SHALL return: hasAccess (bool), used (int64), limit (int64), resetTime (timestamp)
7. WHEN OpenMeter API is unreachable, THE kube-sbt SHALL fail-open with logged warning (allow access)

### Requirement 7: Manual Validation with Demo Tenant

**User Story:** As a platform developer, I want to manually validate user management and usage queries using the "app-creator" tenant, so that I can verify the system works correctly.

#### Acceptance Criteria

1. THE Demo_Provisioner SHALL create sample users in the "app-creator" tenant database
2. THE Demo_Provisioner SHALL provision sample usage events in OpenMeter for the "app-creator" tenant
3. THE Demo_Provisioner SHALL create both tenant-scoped and user-scoped usage data
4. THE Manual_Validation SHALL verify user-scoped queries return correct per-user metrics via API calls
5. THE Manual_Validation SHALL verify tenant-scoped queries return aggregated metrics via API calls
6. THE Manual_Validation SHALL verify entitlement checking returns correct used vs limit values
7. WHEN demo provisioning fails, THE Demo_Provisioner SHALL rollback all created resources

### Requirement 8: REST API for User Management

**User Story:** As a platform developer, I want REST APIs for user management, so that I can create and manage tenant users via HTTP requests.

#### Acceptance Criteria

1. THE API_Server SHALL expose POST /api/v1/tenants/{tenantID}/users endpoint for user creation
2. THE API_Server SHALL expose GET /api/v1/tenants/{tenantID}/users/{userID} endpoint for user retrieval
3. THE API_Server SHALL expose PUT /api/v1/tenants/{tenantID}/users/{userID} endpoint for user updates
4. THE API_Server SHALL expose DELETE /api/v1/tenants/{tenantID}/users/{userID} endpoint for user deletion
5. THE API_Server SHALL expose GET /api/v1/tenants/{tenantID}/users endpoint for user listing with pagination
6. THE API_Server SHALL document all endpoints in OpenAPI 3.0 specification format
7. WHEN API execution fails, THE API_Server SHALL return RFC 7807 Problem Details error responses

### Requirement 9: REST API for Usage Queries

**User Story:** As a platform developer, I want REST APIs for usage queries, so that I can retrieve tenant and user usage metrics via HTTP requests.

#### Acceptance Criteria

1. THE API_Server SHALL expose GET /api/v1/tenants/{tenantID}/usage endpoint for tenant-scoped usage
2. THE API_Server SHALL expose GET /api/v1/tenants/{tenantID}/users/{userID}/usage endpoint for user-scoped usage
3. THE API_Server SHALL expose GET /api/v1/tenants/{tenantID}/entitlements endpoint for entitlement checking
4. THE API_Server SHALL accept query parameters for time period filtering (start_date, end_date, period)
5. THE API_Server SHALL return usage data in JSON format matching OpenMeter response schema
6. THE API_Server SHALL document all endpoints in OpenAPI 3.0 specification format
7. WHEN usage data is unavailable, THE API_Server SHALL return HTTP 503 with retry-after header

### Requirement 10: Hub-Only Deployment Architecture

**User Story:** As a platform architect, I want kube-sbt to run exclusively in the Hub cluster, so that it provides secure multi-tenant abstractions over OpenMeter and Ory.

#### Acceptance Criteria

1. THE kube-sbt SHALL run exclusively in the Hub cluster as part of the control plane
2. THE kube-sbt SHALL wrap Ory Kratos (IAuth) for identity management in the Hub
3. THE kube-sbt SHALL wrap OpenMeter SDK (IMetering, IBilling) for metering/billing operations
4. THE kube-sbt SHALL inject `OpenMeter-Namespace: {tenant_id}` header on all OpenMeter API calls
5. THE kube-sbt SHALL validate tenant JWT before proxying requests to OpenMeter
6. THE Spoke clusters SHALL run zero kube-sbt code (only tenant workloads, PostgREST)
7. THE AgentGateway SHALL run exclusively in Hub cluster (centralized gateway pattern)
8. THE kube-sbt SHALL act as Backend-For-Frontend (BFF) preventing direct OpenMeter access from sbt-sdk

### Requirement 11: Zero-Trust Security Compliance

**User Story:** As a security engineer, I want all inter-service communication to use mTLS with SPIFFE workload identity via Istio service mesh, so that the system maintains zero-trust security posture.

#### Acceptance Criteria

1. THE kube-sbt pod SHALL have Envoy sidecar injected automatically via Istio service mesh
2. THE SPIRE agent SHALL deliver X.509 certificates (SPIFFE SVID) to Envoy sidecar (not application container)
3. THE Envoy sidecar SHALL handle TLS origination/termination transparently
4. THE kube-sbt application code SHALL make plain HTTP calls to localhost (no TLS config in Go code)
5. THE Envoy SHALL intercept outbound calls, upgrade to mTLS, and validate peer SPIFFE IDs
6. THE Istio PeerAuthentication SHALL enforce STRICT mTLS mode for kube-sbt pods
7. THE Istio AuthorizationPolicy SHALL restrict kube-sbt access to authorized principals only
8. THE SPIFFE SVID SHALL auto-rotate every 60 minutes via SPIRE integration
9. WHEN certificate validation fails, THE Envoy SHALL reject the connection and log security events

### Requirement 12: Database Migration for Users Table

**User Story:** As a platform operator, I want idempotent database migrations for the users table, so that tenant databases are consistently provisioned.

#### Acceptance Criteria

1. THE Migration_Script SHALL create the public.users table if it does not exist
2. THE Migration_Script SHALL enable Row-Level Security on the public.users table
3. THE Migration_Script SHALL create RLS policy enforcing user_id matching from JWT claims
4. THE Migration_Script SHALL create indexes on email and created_at columns
5. THE Migration_Script SHALL create trigger for automatic updated_at timestamp updates
6. THE Migration_Script SHALL be idempotent and safe to run multiple times

### Requirement 13: OpenAPI Specification and Manual Validation

**User Story:** As a platform developer, I want comprehensive OpenAPI documentation, so that I can manually validate all API endpoints using tools like Postman or curl.

#### Acceptance Criteria

1. THE API_Documentation SHALL provide OpenAPI 3.0 specification for all REST endpoints
2. THE API_Documentation SHALL include request/response schemas with examples
3. THE API_Documentation SHALL document all error responses with status codes
4. THE API_Documentation SHALL include authentication requirements (JWT bearer tokens)
5. THE API_Documentation SHALL provide example curl commands for each endpoint
6. THE Manual_Validation SHALL use the OpenAPI spec to test all endpoints manually
7. THE Manual_Validation SHALL verify responses match documented schemas

### Requirement 14: Meter and Feature Management (Declarative GitOps)

**User Story:** As a SaaS builder, I want to define meters and features declaratively via GitOps, so that billing catalog configuration is version-controlled and auditable.

#### Acceptance Criteria

1. THE hub-operator SHALL define Custom Resource Definitions (CRDs) for `Meter` and `Feature` under the `billing.nutgraf.in/v1alpha1` API group
2. THE SaaS builder SHALL define Meters and Features declaratively via the `universal-tenant` Helm chart `values.yaml` (billing.meters and billing.features sections)
3. THE hub-operator SHALL reconcile `Meter` and `Feature` CRs by making idempotent API calls to OpenMeter using the Go SDK
4. THE hub-operator SHALL update CR `Status.Conditions` with sync status (Synced: True/False)
5. THE hub-operator SHALL extract `tenantId` from CR spec and inject as OpenMeter namespace parameter
6. THE kube-sbt REST API SHALL ONLY provide read-only endpoints (`GET /api/v1/meters`, `GET /api/v1/features`) for UI display
7. WHEN Meter CR is deleted, THE hub-operator SHALL delete the corresponding meter from OpenMeter
8. WHEN reconciliation fails, THE hub-operator SHALL retry with exponential backoff and update Status.Conditions with error message

### Requirement 15: Plan and Rate Card Management (Declarative GitOps)

**User Story:** As a SaaS builder, I want to define pricing plans and rate cards declaratively via GitOps, so that pricing configuration is version-controlled and auditable.

#### Acceptance Criteria

1. THE hub-operator SHALL define a Custom Resource Definition (CRD) for `Plan` under the `billing.nutgraf.in/v1alpha1` API group
2. THE SaaS builder SHALL define Pricing Plans and Rate Cards declaratively via the `universal-tenant` Helm chart `values.yaml` (billing.plans section)
3. THE Plan specification SHALL support billing cadence (monthly, annual), currency, phases, and rate cards
4. THE Rate card specification SHALL support pricing models: flat, usage_based, tiered_volume, tiered_graduated
5. THE hub-operator SHALL reconcile `Plan` CRs by making idempotent API calls to OpenMeter using the Go SDK
6. THE hub-operator SHALL update CR `Status.Conditions` with sync status (Synced: True/False)
7. THE kube-sbt REST API SHALL ONLY provide read-only endpoints (`GET /api/v1/plans`) for UI display
8. WHEN Plan CR references non-existent features, THE hub-operator SHALL update Status.Conditions with validation error
9. WHEN Plan CR is deleted, THE hub-operator SHALL delete the corresponding plan from OpenMeter

### Requirement 15.1: GitOps Workflow for Billing Catalog

**User Story:** As a SaaS builder, I want to manage billing catalog (Meters, Features, Plans) through Git commits, so that all pricing changes are auditable and rollback-safe.

#### Acceptance Criteria

1. THE SaaS builder SHALL define billing configuration in `fleet-registry/tenants/<tenant-id>/values.yaml` under the `billing` section
2. THE `universal-tenant` Helm chart SHALL render `Meter`, `Feature`, and `Plan` CRs from `values.yaml`
3. THE ArgoCD SHALL sync rendered CRs to Hub cluster namespace `hub-platform-ops`
4. THE hub-operator reconcilers SHALL detect CR changes and sync to OpenMeter within 30 seconds
5. THE CR Status.Conditions SHALL reflect sync status (Synced: True/False) with error messages
6. WHEN SaaS builder commits billing changes to Git, THE changes SHALL be applied to OpenMeter automatically via GitOps pipeline
7. WHEN SaaS builder reverts Git commit, THE billing catalog SHALL rollback to previous state via ArgoCD sync
8. THE billing catalog changes SHALL have full Git audit trail (author, timestamp, commit message)
9. THE hub-operator SHALL use exponential backoff for failed reconciliation attempts (1s, 2s, 4s, 8s, 16s, max 5min)
10. THE hub-operator SHALL emit Kubernetes Events for reconciliation failures visible via `kubectl describe`

### Requirement 16: Subscription Management (IBilling)

**User Story:** As a SaaS builder, I want to manage user subscriptions via kube-sbt APIs with automatic proration, so that plan changes are billed fairly.

#### Acceptance Criteria

1. THE IBilling interface SHALL provide CreateSubscription(namespace, subjectID, planID) method
2. THE Subscription creation SHALL use GenerateSubjectID() helper for subject formatting
3. THE IBilling interface SHALL provide UpdateSubscription (plan changes, quantity updates) method
4. WHEN subscription plan is updated, THE OpenMeter SHALL calculate prorated amounts internally
5. THE OpenMeter Stripe App SHALL automatically sync prorated invoices to Stripe for payment collection
6. THE kube-sbt SHALL NOT implement proration logic (delegated to OpenMeter)
7. THE IBilling interface SHALL provide CancelSubscription(namespace, subscriptionID) method
8. WHEN subscription is cancelled, THE kube-sbt SHALL mark subscription as "inactive" (preserve historical data)
9. THE IBilling interface SHALL provide GetSubscription and ListSubscriptions methods filtered by namespace
10. THE kube-sbt SHALL delegate subscription state management to OpenMeter
11. WHEN subscription creation fails, THE kube-sbt SHALL return OpenMeter error with context

### Requirement 17: Invoice Operations (IBilling)

**User Story:** As a SaaS builder, I want to preview and retrieve invoices via kube-sbt APIs, so that I can display billing information to end-users.

#### Acceptance Criteria

1. THE IBilling interface SHALL provide PreviewInvoice(namespace, subjectID) method
2. THE IBilling interface SHALL provide GetInvoice(namespace, invoiceID) method
3. THE IBilling interface SHALL provide ListInvoices(namespace, filters) method with pagination
4. THE Invoice response SHALL include line items, totals, tax, discounts, and payment status
5. THE kube-sbt SHALL query OpenMeter API with namespace and subject filters
6. THE kube-sbt SHALL format OpenMeter invoice data for sbt-sdk consumption
7. WHEN invoice not found, THE kube-sbt SHALL return 404 with clear error message

### Requirement 18: Stripe Integration via OpenMeter

**User Story:** As a platform operator, I want Stripe payment integration with secure secret storage, so that payment credentials are never exposed in Git.

#### Acceptance Criteria

1. THE OpenMeter Stripe App configuration SHALL be managed via OpenMeter's native UI/API or via a declarative `StripeApp` CRD managed by hub-operator (if GitOps pattern is extended to secrets)
2. THE Stripe API keys and webhook secrets SHALL be stored in Infisical (not Git, not ConfigMaps)
3. THE hub-operator SHALL retrieve Stripe secrets from Infisical via External Secrets Operator when configuring Stripe App
4. THE Stripe webhooks SHALL be routed directly to OpenMeter endpoint POST /api/v1/apps/{appId}/stripe/webhook
5. THE OpenMeter SHALL validate Stripe webhook signatures using app-specific webhook secret
6. THE OpenMeter SHALL update internal invoice state (paid/failed/voided) based on Stripe webhook events
7. THE kube-sbt SHALL NOT intercept Stripe webhooks (OpenMeter handles webhook processing)
8. THE kube-sbt SHALL query OpenMeter API for invoice status and payment details via read-only methods
9. THE kube-sbt MAY subscribe to OpenMeter notification events for billing alerts (optional)
10. THE IBilling interface SHALL NOT provide ConfigureStripeApp method (deprecated in favor of declarative approach)

### Requirement 19: OpenMeter Namespace Provisioning via hub-operator

**User Story:** As a platform operator, I want OpenMeter namespaces automatically provisioned when tenants are created, so that tenant isolation is enforced from Day 0.

#### Acceptance Criteria

1. THE hub-operator SHALL provision OpenMeter namespace when HubEnvironment CR is reconciled
2. THE OpenMeter namespace SHALL use tenant_id as the namespace identifier
3. THE hub-operator SHALL use OpenMeter's `namespace.Manager` Go API (not REST API) for namespace lifecycle
4. THE Namespace provisioning SHALL occur in Phase 0 (before database migrations and role creation)
5. THE Namespace provisioning SHALL complete before kube-sbt attempts any metering operations
6. THE kube-sbt SHALL assume namespace exists for all Day-2 operations (no namespace creation logic)
7. WHEN namespace provisioning fails, THE hub-operator SHALL report failure in HubEnvironment status conditions
8. THE Namespace deletion SHALL be handled by hub-operator during tenant offboarding
9. THE hub-operator SHALL add OpenMeterNamespaceConfigured condition to HubEnvironment status

### Requirement 20: OpenMeter Hub Deployment

**User Story:** As a platform architect, I want OpenMeter deployed in Hub cluster using official Helm chart, so that metering infrastructure is centralized and production-ready.

#### Acceptance Criteria

1. THE OpenMeter deployment SHALL use official open-source Helm chart from OpenMeter project
2. THE OpenMeter deployment SHALL run exclusively in Hub cluster
3. THE OpenMeter pods SHALL be scheduled on Hub managed workload nodes (not control plane nodes)
4. THE OpenMeter deployment SHALL use node selectors: `node-role.kubernetes.io/worker=true`
5. THE OpenMeter deployment SHALL use tolerations for Hub workload taints
6. THE OpenMeter SHALL expose OTLP ingestion endpoint accessible from all Spoke clusters
7. THE OpenMeter SHALL expose REST API endpoint accessible only from kube-sbt (Hub internal)
8. THE OpenMeter deployment SHALL include HA configuration (3+ replicas, PodDisruptionBudget)

### Requirement 21: Orphaned Subject Reconciliation

**User Story:** As a platform operator, I want automated cleanup of orphaned OpenMeter subjects when Kratos rollback fails, so that data consistency is maintained without manual intervention.

#### Acceptance Criteria

1. WHEN OpenMeter subject creation succeeds but Kratos user rollback fails, THE User_Manager SHALL retry rollback 3 times with exponential backoff (1s, 2s, 4s)
2. WHEN all rollback retries fail, THE User_Manager SHALL publish event to NATS topic `opensbt_orphanedSubjects` with subject_id and tenant_id
3. THE Reconciliation_Controller SHALL subscribe to `opensbt_orphanedSubjects` topic
4. THE Reconciliation_Controller SHALL attempt to delete orphaned OpenMeter subjects every 5 minutes
5. WHEN reconciliation succeeds, THE Reconciliation_Controller SHALL remove event from DLQ
6. WHEN reconciliation fails 3 times, THE Reconciliation_Controller SHALL publish alert to NATS topic `opensbt_notifications` for ops team
7. THE Manual_Cleanup_Dashboard SHALL display orphaned subjects requiring manual intervention
