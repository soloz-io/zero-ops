# Requirements Document

## Introduction

This document specifies requirements for the kube-sbt metering and user management system. The system provides Go-based abstractions for multi-tenant SaaS platforms to manage users and query usage metrics from OpenMeter. It supports both tenant-scoped and user-scoped usage queries, integrates with PostgreSQL-based tenant databases using Row-Level Security (RLS), and provides dynamic entitlements checking against tier quotas.

## Glossary

- **kube-sbt**: Kubernetes-based SaaS Builder Toolkit providing abstractions for multi-tenant platforms
- **Tenant**: An isolated customer environment with dedicated database and resources
- **User**: An individual identity within a tenant stored in the tenant database
- **OpenMeter**: Real-time usage metering and billing engine using OTLP protocol
- **Meter**: A usage metric definition (e.g., "api_calls", "storage_mb")
- **Entitlement**: A quota limit associated with a meter for a specific tier
- **RLS**: Row-Level Security enforcing data isolation via JWT claims
- **PostgREST**: Auto-generated REST API layer over PostgreSQL
- **AgentGateway**: Service that sends usage events directly to OpenMeter via OTLP
- **Spoke_Pool**: Shared multi-tenant cluster for Starter tier
- **Hub**: Management cluster running control plane services
- **OTLP**: OpenTelemetry Protocol for telemetry data transmission

## Requirements

### Requirement 1: Framework Renaming

**User Story:** As a platform developer, I want all `opensbt` references renamed to `kube-sbt`, so that the codebase reflects the correct project naming.

#### Acceptance Criteria

1. THE Renaming_Process SHALL update all package paths from `internal/opensbt` to `internal/kubesbt`
2. THE Renaming_Process SHALL update all import statements referencing `opensbt` packages
3. THE Renaming_Process SHALL update the binary path from `cmd/opensbt` to `cmd/kubesbt`
4. THE Renaming_Process SHALL update all documentation files containing `opensbt` references
5. THE Renaming_Process SHALL update all configuration files containing `opensbt` references

### Requirement 2: User Management Interface

**User Story:** As a tenant administrator, I want to manage users in my tenant database, so that I can control access to my SaaS application.

#### Acceptance Criteria

1. THE User_Manager SHALL provide an IUserManager interface for user operations
2. WHEN a user creation request is received, THE User_Manager SHALL insert a record into the tenant's public.users table
3. THE User_Manager SHALL enforce RLS policies using JWT claims (request.jwt.claims->>'user_id')
4. THE User_Manager SHALL support CRUD operations (Create, Read, Update, Delete) for users
5. THE User_Manager SHALL store user data with schema: id (UUID), email (VARCHAR), email_verified (BOOLEAN), created_at (TIMESTAMPTZ), updated_at (TIMESTAMPTZ), metadata (JSONB)
6. WHEN a user record is updated, THE User_Manager SHALL automatically update the updated_at timestamp

### Requirement 3: Tenant-Scoped Usage Metrics

**User Story:** As a tenant administrator, I want to view aggregate usage metrics for my entire tenant, so that I can monitor overall consumption against my plan limits.

#### Acceptance Criteria

1. THE Metering_Service SHALL provide a GetUsage(tenantID) method returning tenant-level aggregated metrics
2. THE Metering_Service SHALL query OpenMeter API for tenant-scoped usage data
3. THE Metering_Service SHALL return metrics for: Applications (count), Records (count), Storage (MB), Portal_Users (count)
4. THE Metering_Service SHALL support time-period filtering (daily, monthly, custom range)
5. THE Metering_Service SHALL return usage data paired with limits from tier configuration
6. WHEN OpenMeter API is unreachable, THE Metering_Service SHALL return an error with retry guidance

### Requirement 4: User-Scoped Usage Metrics

**User Story:** As a tenant administrator, I want to view usage metrics for individual users, so that I can track per-user consumption and identify high-usage accounts.

#### Acceptance Criteria

1. THE Metering_Service SHALL provide a GetUsage(tenantID, userID) method returning user-level metrics
2. THE Metering_Service SHALL query OpenMeter API with user-scoped filters
3. THE Metering_Service SHALL return metrics for: Custom_APIs (count), Developer_APIs (count), Batch_Blocks (count), AI_Calls (count)
4. THE Metering_Service SHALL support time-period filtering (daily, monthly, custom range)
5. THE Metering_Service SHALL return usage data paired with limits from tier configuration
6. WHEN a userID does not exist in the tenant, THE Metering_Service SHALL return an empty usage report

### Requirement 5: OpenMeter Integration

**User Story:** As a platform operator, I want the metering service to integrate with OpenMeter, so that usage data is centrally tracked and queryable.

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

### Requirement 10: Hub-Spoke Architecture Compliance

**User Story:** As a platform architect, I want the metering system to comply with hub-spoke architecture, so that it operates correctly in distributed deployments.

#### Acceptance Criteria

1. THE Metering_Service SHALL run in the Hub cluster as part of the mcp-server process
2. THE Metering_Service SHALL query OpenMeter running in the Hub cluster
3. THE User_Manager SHALL connect to tenant databases in Spoke_Pool clusters via PostgREST
4. THE User_Manager SHALL use per-tenant JWT tokens for authentication to PostgREST
5. WHEN Spoke_Pool is unreachable, THE User_Manager SHALL return errors without blocking other tenants
6. THE Metering_Service SHALL support querying usage for tenants across multiple Spoke_Pool clusters

### Requirement 11: Zero-Trust Security Compliance

**User Story:** As a security engineer, I want all inter-service communication to use mTLS, so that the system maintains zero-trust security posture.

#### Acceptance Criteria

1. THE User_Manager SHALL use mTLS when connecting to PostgREST endpoints
2. THE User_Manager SHALL validate SPIFFE IDs for PostgREST service identities
3. THE Metering_Service SHALL use mTLS when connecting to OpenMeter API
4. THE Metering_Service SHALL validate SPIFFE IDs for OpenMeter service identity
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
