# Requirements Document

## Introduction

This specification defines the complete agentic enterprise onboarding journey, enabling tenant administrators to provision enterprise-tier environments through natural language commands in their IDE. The system handles OAuth2 device flow authentication, authorization, tenant creation, encrypted credential storage, and automated infrastructure provisioning via Crossplane.

## Glossary

- **AgentGateway**: MCP server that validates JWTs and routes tool calls to backend services
- **Cursor**: IDE client that invokes MCP tools on behalf of the Tenant_Admin
- **Tenant_Admin**: User with administrative privileges who initiates onboarding
- **Hydra**: OAuth2 server that issues JWTs after device flow authentication
- **Kratos**: Identity provider that handles user authentication
- **Keto**: Authorization service that evaluates permission policies
- **zero_ops_api**: Backend service that manages tenant and environment lifecycle
- **crossplane_mcp**: MCP server that manages AINativeSaaS custom resources
- **Composition_B**: Crossplane composition for enterprise-tier infrastructure
- **Device_Code**: Short-lived code displayed to user for browser authentication
- **JWT**: JSON Web Token used for authenticated API requests
- **AINativeSaaS_CR**: Custom resource defining tenant environment configuration
- **JWKS**: JSON Web Key Set used to validate JWT signatures
- **Age_Key**: Encryption key used to protect cloud provider credentials

## Requirements

### Requirement 1: Initiate Tenant Creation

**User Story:** As a Tenant_Admin, I want to create a tenant through natural language commands, so that I can onboard organizations without manual API calls.

#### Acceptance Criteria

1. WHEN the Tenant_Admin types a tenant creation command, THE Cursor SHALL invoke the tenant_create MCP tool with tenant parameters
2. THE AgentGateway SHALL extract the JWT from the Authorization header
3. IF no JWT is present, THEN THE AgentGateway SHALL return HTTP 401 with WWW-Authenticate header containing device_code_url
4. THE AgentGateway SHALL generate a Device_Code with 5-minute expiration
5. THE Cursor SHALL display the Device_Code and authentication URL to the Tenant_Admin

### Requirement 2: Complete Device Flow Authentication

**User Story:** As a Tenant_Admin, I want to authenticate via browser using a device code, so that my IDE can access protected APIs without storing passwords.

#### Acceptance Criteria

1. WHEN the Device_Code is displayed, THE Cursor SHALL poll the Hydra token endpoint every 5 seconds
2. THE Tenant_Admin SHALL navigate to the authentication URL in a browser
3. THE Tenant_Admin SHALL enter the Device_Code in the browser
4. THE Kratos SHALL authenticate the Tenant_Admin credentials
5. WHEN authentication succeeds, THE Hydra SHALL issue a JWT to the Cursor
6. IF 5 minutes elapse without authentication, THEN THE AgentGateway SHALL invalidate the Device_Code

### Requirement 3: Validate and Authorize Requests

**User Story:** As a system operator, I want all API requests authenticated and authorized, so that only permitted users can create tenants.

#### Acceptance Criteria

1. WHEN the Cursor retries tenant_create with a JWT, THE AgentGateway SHALL validate the JWT signature using cached JWKS
2. THE AgentGateway SHALL extract the subject claim from the JWT
3. THE AgentGateway SHALL query Keto with the subject and tenant_create permission
4. IF Keto denies permission, THEN THE AgentGateway SHALL return HTTP 403
5. WHEN authorization succeeds, THE AgentGateway SHALL forward the request to zero_ops_api

### Requirement 4: Create Tenant Record

**User Story:** As a Tenant_Admin, I want tenant metadata persisted in the database, so that the system can track organizational accounts.

#### Acceptance Criteria

1. WHEN zero_ops_api receives a tenant_create request, THE zero_ops_api SHALL validate the tenant name is unique
2. IF the tenant name exists, THEN THE zero_ops_api SHALL return HTTP 409 with a conflict error
3. THE zero_ops_api SHALL insert a tenant record into PostgreSQL with name, plan, and region
4. THE zero_ops_api SHALL return HTTP 201 with the tenant_id
5. THE Cursor SHALL display the tenant_id to the Tenant_Admin

### Requirement 5: Collect Cloud Provider Credentials

**User Story:** As a Tenant_Admin, I want to securely provide cloud credentials, so that the system can provision infrastructure on my behalf.

#### Acceptance Criteria

1. WHEN tenant creation succeeds, THE Cursor SHALL prompt the Tenant_Admin for a Hetzner API token
2. THE Tenant_Admin SHALL enter the API token via console input
3. THE Cursor SHALL encrypt the API token using the Age_Key
4. THE Cursor SHALL upload the encrypted token to S3 with the path credentials/{tenant_id}/hetzner
5. THE Cursor SHALL invoke environment_create with tier, cloud, and region parameters

### Requirement 6: Initiate Environment Provisioning

**User Story:** As a Tenant_Admin, I want infrastructure provisioned automatically, so that I don't need to manually configure cloud resources.

#### Acceptance Criteria

1. WHEN environment_create is invoked, THE zero_ops_api SHALL validate the tier matches the tenant plan
2. THE zero_ops_api SHALL generate an AINativeSaaS_CR with the specified tier, cloud, and region
3. THE zero_ops_api SHALL commit the AINativeSaaS_CR to the tenant control plane Git repository
4. THE zero_ops_api SHALL return HTTP 202 with a status_url for provisioning progress
5. THE Cursor SHALL display the status_url to the Tenant_Admin

### Requirement 7: Execute Crossplane Composition

**User Story:** As a system operator, I want Crossplane to provision enterprise infrastructure, so that tenants receive consistent, compliant environments.

#### Acceptance Criteria

1. WHEN the AINativeSaaS_CR is committed, THE Crossplane SHALL detect the new resource
2. THE Crossplane SHALL select Composition_B based on the enterprise tier
3. THE Crossplane SHALL provision Hetzner resources using the decrypted API token
4. WHEN provisioning completes, THE Crossplane SHALL update the AINativeSaaS_CR status to Ready
5. THE Composition_B SHALL complete within 15 minutes

### Requirement 8: Handle Provisioning Errors

**User Story:** As a Tenant_Admin, I want clear error messages when provisioning fails, so that I can take corrective action.

#### Acceptance Criteria

1. IF Hetzner quota is exceeded, THEN THE Crossplane SHALL update the AINativeSaaS_CR status with a quota error message
2. IF the API token is invalid, THEN THE Crossplane SHALL update the AINativeSaaS_CR status with an authentication error message
3. WHEN the Cursor polls the status_url, THE zero_ops_api SHALL return the current provisioning status
4. IF provisioning fails, THEN THE Cursor SHALL display the error message to the Tenant_Admin
5. THE zero_ops_api SHALL retain failed AINativeSaaS_CR resources for debugging

### Requirement 9: Display Provisioning Summary

**User Story:** As a Tenant_Admin, I want a summary of provisioned resources, so that I know what infrastructure is available.

#### Acceptance Criteria

1. WHEN the AINativeSaaS_CR status becomes Ready, THE Cursor SHALL retrieve the final status
2. THE Cursor SHALL display the tenant_id, region, and provisioned resource endpoints
3. THE Cursor SHALL display the total provisioning duration
4. FOR ALL successful provisions, the duration SHALL be less than 15 minutes
5. THE Cursor SHALL format the summary for readability in the IDE console

### Requirement 10: Cache JWKS for Performance

**User Story:** As a system operator, I want JWT validation to be fast, so that API requests have low latency.

#### Acceptance Criteria

1. WHEN the AgentGateway starts, THE AgentGateway SHALL fetch the JWKS from Hydra
2. THE AgentGateway SHALL cache the JWKS in memory with a 1-hour TTL
3. WHEN validating a JWT, THE AgentGateway SHALL use the cached JWKS
4. IF the JWT signature fails validation, THEN THE AgentGateway SHALL refresh the JWKS cache once
5. IF validation fails after refresh, THEN THE AgentGateway SHALL return HTTP 401

### Requirement 11: Parse and Format Configuration

**User Story:** As a developer, I want to parse AINativeSaaS_CR YAML, so that I can validate and manipulate tenant configurations.

#### Acceptance Criteria

1. WHEN an AINativeSaaS_CR is provided, THE Parser SHALL parse it into a Configuration object
2. WHEN an invalid AINativeSaaS_CR is provided, THE Parser SHALL return a descriptive error with line number
3. THE Pretty_Printer SHALL format Configuration objects back into valid YAML
4. FOR ALL valid Configuration objects, parsing then printing then parsing SHALL produce an equivalent object
5. THE Parser SHALL validate required fields: tier, cloud, region
