# Requirements Document

## Introduction

This document specifies requirements for the kube-sbt metering and billing system. The system provides Go-based abstractions (IMetering, IBilling) for multi-tenant SaaS platforms, wrapping OpenMeter for usage metering, subscriptions, and invoicing. It runs in the Hub cluster as a secure Backend-For-Frontend (BFF), enforcing tenant isolation via namespace injection and providing subject management for usage attribution.

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
- **IMetering**: kube-sbt interface for meter/feature/plan CRUD and usage queries
- **IBilling**: kube-sbt interface for subscription/invoice operations and Stripe integration

## Requirements

### Requirement 1: User Identity and Subject Registration

**User Story:** As a platform operator, I want user identities managed via Ory Kratos and synced to OpenMeter as subjects, so that usage can be attributed to individual users.

#### Acceptance Criteria

1. THE User_Manager SHALL integrate with Ory Kratos (IAuth) for identity management
2. WHEN a user is created via Ory Kratos, THE User_Manager SHALL register the user as an OpenMeter subject
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

### Requirement 4: AgentGateway OTLP Configuration

**User Story:** As a platform operator, I want AgentGateway configured to emit OTLP traces to OpenMeter, so that all tenant API traffic is automatically metered.

#### Acceptance Criteria

1. THE Platform_Configuration SHALL configure AgentGateway (external Rust binary) to emit OTLP to OpenMeter endpoint
2. THE AgentGateway_Config SHALL specify OpenMeter ingestion endpoint URL in Hub cluster
3. THE AgentGateway SHALL extract tenant_id and user_id from JWT claims and include in OTLP span attributes
4. THE AgentGateway SHALL format subject as `{tenant_id}#{user_id}` in span attributes
5. THE AgentGateway SHALL emit meter events for: api_calls, storage_operations, custom_api_calls, developer_api_calls
6. THE kube-sbt documentation SHALL provide OTLP span attribute format specification
7. THE kube-sbt SHALL NOT implement OTLP emission (AgentGateway responsibility)

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

**User Story:** As a platform developer, I want tier quotas to be dynamically configurable, so that I can add new meters without code changes.

#### Acceptance Criteria

1. THE Tier_Config SHALL replace hardcoded TierQuotas fields with a dynamic map[string]int64 structure
2. THE Tier_Config SHALL map meter IDs (string keys) to quota limits (int64 values)
3. THE Entitlement_Checker SHALL compare current usage against configured limits
4. THE Entitlement_Checker SHALL support unlimited quotas represented by -1 value
5. WHEN a meter has no configured limit, THE Entitlement_Checker SHALL treat it as unlimited
6. THE Entitlement_Checker SHALL return entitlement status: hasAccess (bool), used (int64), limit (int64)

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
6. THE Spoke clusters SHALL run zero kube-sbt code (only tenant workloads, AgentGateway, PostgREST)
7. THE kube-sbt SHALL act as Backend-For-Frontend (BFF) preventing direct OpenMeter access from sbt-sdk

### Requirement 11: Zero-Trust Security Compliance

**User Story:** As a security engineer, I want all inter-service communication to use mTLS, so that the system maintains zero-trust security posture.

#### Acceptance Criteria

1. THE kube-sbt SHALL use mTLS when connecting to Ory Kratos endpoints
2. THE kube-sbt SHALL validate SPIFFE IDs for Ory service identities
3. THE kube-sbt SHALL use mTLS when connecting to OpenMeter API
4. THE kube-sbt SHALL validate SPIFFE IDs for OpenMeter service identity
5. WHEN certificate validation fails, THE Service SHALL reject the connection and log security events
6. THE Service SHALL rotate certificates automatically via cert-manager integration

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

### Requirement 14: Meter and Feature Management (IMetering)

**User Story:** As a SaaS builder, I want to define meters and features via kube-sbt APIs, so that I can configure usage-based billing without directly accessing OpenMeter.

#### Acceptance Criteria

1. THE IMetering interface SHALL provide CreateMeter(namespace, meterSpec) method
2. THE IMetering interface SHALL provide CreateFeature(namespace, featureSpec) method mapping features to meters
3. THE IMetering interface SHALL provide ListMeters(namespace) and GetMeter(namespace, meterID) methods
4. THE IMetering interface SHALL provide UpdateMeter and DeleteMeter methods
5. THE kube-sbt SHALL inject `OpenMeter-Namespace: {tenant_id}` header on all OpenMeter API calls
6. THE kube-sbt SHALL validate tenant JWT before proxying meter/feature requests to OpenMeter
7. WHEN namespace mismatch detected, THE kube-sbt SHALL return 403 Forbidden error

### Requirement 15: Plan and Rate Card Management (IMetering)

**User Story:** As a SaaS builder, I want to define pricing plans and rate cards via kube-sbt APIs, so that I can configure subscription tiers.

#### Acceptance Criteria

1. THE IMetering interface SHALL provide CreatePlan(namespace, planSpec) method
2. THE Plan specification SHALL support billing cadence (monthly, annual), currency, and rate cards
3. THE Rate card specification SHALL support pricing models: flat, usage-based, tiered-volume, tiered-graduated
4. THE IMetering interface SHALL provide ListPlans(namespace) and GetPlan(namespace, planID) methods
5. THE IMetering interface SHALL provide UpdatePlan and DeletePlan methods
6. THE kube-sbt SHALL enforce namespace isolation for all plan operations
7. WHEN plan references non-existent features, THE kube-sbt SHALL return validation error

### Requirement 16: Subscription Management (IBilling)

**User Story:** As a SaaS builder, I want to manage user subscriptions via kube-sbt APIs, so that I can assign plans to users and track billing lifecycle.

#### Acceptance Criteria

1. THE IBilling interface SHALL provide CreateSubscription(namespace, subjectID, planID) method
2. THE Subscription creation SHALL use GenerateSubjectID() helper for subject formatting
3. THE IBilling interface SHALL provide UpdateSubscription (plan changes, quantity updates) method
4. THE IBilling interface SHALL provide CancelSubscription(namespace, subscriptionID) method
5. THE IBilling interface SHALL provide GetSubscription and ListSubscriptions methods filtered by namespace
6. THE kube-sbt SHALL delegate subscription state management to OpenMeter
7. WHEN subscription creation fails, THE kube-sbt SHALL return OpenMeter error with context

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

**User Story:** As a platform operator, I want Stripe payment integration handled via OpenMeter's native Stripe App, so that kube-sbt delegates payment processing.

#### Acceptance Criteria

1. THE IBilling interface SHALL provide ConfigureStripeApp(namespace, stripeConfig) method
2. THE Stripe configuration SHALL include API keys, webhook secrets, and payment settings
3. THE kube-sbt SHALL expose webhook endpoint POST /api/v1/billing/stripe/webhook
4. WHEN Stripe webhook received, THE kube-sbt SHALL validate signature and translate to NATS event
5. THE kube-sbt SHALL publish `opensbt_billingSuccess` event to NATS JetStream on successful payment
6. THE kube-sbt SHALL NOT directly create Stripe subscriptions (delegated to OpenMeter)
7. THE kube-sbt SHALL query OpenMeter for Stripe sync status and payment details
