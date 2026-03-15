# Requirements Document

## Introduction

This specification defines the complete agentic enterprise onboarding journey, enabling tenant administrators to provision enterprise-tier environments through natural language commands in their IDE. The system handles OAuth2 Authorization Code Flow with PKCE authentication, authorization, tenant creation, encrypted credential storage via KSOPS, and automated infrastructure provisioning via Crossplane.

**Architecture Principles:**
- **Idempotent Operations**: All API operations are safe to retry infinitely. The system returns current state, not errors, for duplicate requests.
- **Declarative State Machine**: The system is not transactional. Each operation moves the tenant through states: AWAITING_CREDENTIALS → CREDENTIALS_READY → PROVISIONING → READY.
- **Eventual Consistency**: Crossplane reconciles continuously. There are no terminal failure states, only degraded states that self-heal when external issues resolve.
- **GitOps-First**: All infrastructure changes are committed to Git. CI renders manifests to OCI artifacts. ArgoCD Agent (from https://github.com/argoproj-labs/argocd-agent/) and Crossplane reconcile from OCI artifacts built from Git, not from direct API calls.
- **Async Agent Pattern**: The Agent submits intents and exits immediately. It does NOT block or poll for long-running operations. Status is queried on-demand via conversational prompts or Platform Console.

**GitOps Pattern**: Zero-Ops follows the enterprise Git+OCI pattern: Git (source of truth) → CI renders manifests → OCI registry artifact → ArgoCD Agent pulls OCI → Cluster reconciliation. Reference: https://argo-cd.readthedocs.io/en/latest/user-guide/oci/

## Tenant Lifecycle State Machine

```
┌─────────────────────┐
│  tenant_create      │
│  (MCP tool call)    │
└──────────┬──────────┘
           │
           ▼
┌─────────────────────────┐
│  AWAITING_CREDENTIALS   │ ◄──┐ (Retry tenant_create → returns 200 with this state)
│  INCOMPLETE_GIT_SETUP   │ ◄──┘ (Git failed, retry tenant_create → completes Git setup)
└──────────┬──────────────┘    │
           │                   │
           │ credential_submit │
           ▼                   │
┌─────────────────────────┐    │
│  CREDENTIALS_READY      │    │
└──────────┬──────────────┘    │
           │                   │
           │ environment_create│
           ▼                   │
┌─────────────────────────┐    │
│  Pending                │    │ (Git committed, CI building OCI, ArgoCD not synced)
└──────────┬──────────────┘    │
           │                   │
           ▼                   │
┌─────────────────────────┐    │
│  Provisioning           │ ◄──┘ (Crossplane reconciling)
└──────────┬──────────────┘
           │
           ├─────────────┐
           │             │
           ▼             ▼
    ┌──────────┐   ┌──────────┐
    │  Ready   │   │ Degraded │ (External error, continuous retry)
    └──────────┘   └─────┬────┘
                         │
                         │ (Issue resolved)
                         └──────────────────────────────────────┐
                                                                │
                                                                ▼
                                                         ┌──────────────┐
                                                         │ Provisioning │
                                                         └──────────────┘
```

**Key State Transitions:**
- All states are queryable via environment_status MCP tool
- Agent queries state on-demand, not via continuous polling
- Degraded state self-heals when external issues resolve (quota increased, token fixed)
- No terminal failure states - only eventual consistency

## Glossary

- **AgentGateway**: Single authentication and authorization enforcement point. Validates JWTs, enforces RBAC via identity-service, and routes tool calls to backend MCP servers
- **identity-service**: Python service layer that interfaces with Ory stack (Hydra, Kratos, Keto) on behalf of AgentGateway
- **Cursor**: IDE client that invokes MCP tools on behalf of the Tenant_Admin
- **Tenant_Admin**: User with administrative privileges who initiates onboarding
- **Hydra**: OAuth2 server that issues JWTs after Authorization Code Flow with PKCE (accessed via identity-service)
- **Kratos**: Identity provider that handles user authentication (accessed via identity-service)
- **Keto**: Authorization service that evaluates permission policies (accessed via identity-service)
- **zero_ops_api**: Backend MCP server that manages tenant and environment lifecycle (receives pre-authenticated requests from AgentGateway)
- **crossplane_mcp**: Backend MCP server that manages AINativeSaaS custom resources (receives pre-authenticated requests from AgentGateway)
- **Composition_B**: Crossplane composition for enterprise-tier infrastructure
- **PKCE**: Proof Key for Code Exchange - security extension for OAuth public clients
- **code_verifier**: Random string generated by client for PKCE flow
- **code_challenge**: SHA256 hash of code_verifier, sent in authorization request
- **JWT**: JSON Web Token used for authenticated API requests
- **AINativeSaaS_CR**: Custom resource defining tenant environment configuration
- **JWKS**: JSON Web Key Set used to validate JWT signatures
- **Age_Key**: Encryption keypair (public/private) used to protect cloud provider credentials via KSOPS
- **KSOPS**: Kustomize plugin that encrypts/decrypts Kubernetes Secrets using SOPS and Age
- **Idempotent**: Operation that can be safely retried infinitely with the same result
- **Eventual Consistency**: System state converges to desired state over time through continuous reconciliation
- **fleet-registry**: Global Git repository containing tenant descriptors that trigger ArgoCD ApplicationSet to watch new tenant control plane repositories. CI builds OCI artifacts from Git changes.
- **GitHub App Installation Token**: Short-lived JWT used by zero_ops_api to authenticate Git API operations (repo creation, commits)
- **Plan**: The billing entitlement associated with a tenant record in PostgreSQL (e.g., Starter, Enterprise). Determines which infrastructure tiers the tenant is authorized to provision
- **Tier**: The infrastructure topology specified in the AINativeSaaS XRD (e.g., starter, enterprise). Defines the actual resources provisioned by Crossplane

## Requirements

### Requirement 1: Initiate Tenant Creation

**User Story:** As a Tenant_Admin, I want to create a tenant through natural language commands, so that I can onboard organizations without manual API calls.

#### Acceptance Criteria

1. WHEN the Tenant_Admin types a tenant creation command, THE Cursor SHALL invoke the tenant_create MCP tool with tenant parameters
2. THE AgentGateway SHALL extract the JWT from the Authorization header
3. IF no JWT is present, THEN THE AgentGateway SHALL return HTTP 401 with WWW-Authenticate header containing resource_metadata URL
4. THE Cursor SHALL discover OAuth endpoints via metadata discovery (Requirement 12)
5. THE Cursor SHALL initiate Authorization Code Flow with PKCE (Requirement 2)

### Requirement 2: Complete Authorization Code Flow with PKCE

**User Story:** As a Tenant_Admin, I want to authenticate via browser with automatic redirect back to my IDE, so that I have a seamless authentication experience.

**CRITICAL:** This requirement uses OAuth 2.1 Authorization Code Flow with PKCE (RFC 7636) per MCP Specification 2025-11-25. PKCE prevents authorization code interception attacks for public clients (desktop apps, IDEs) that cannot securely store client secrets.

#### Acceptance Criteria

1. THE Cursor SHALL generate a cryptographically random code_verifier (43-128 characters, base64url-encoded)
2. THE Cursor SHALL compute code_challenge = BASE64URL(SHA256(code_verifier))
3. THE Cursor SHALL generate a random state parameter (32 bytes, hex-encoded) for CSRF protection
4. THE Cursor SHALL open the system browser to the authorization endpoint with parameters:
   - client_id: mcp-public-client (pre-registered with all port variants)
   - response_type: code
   - redirect_uri: http://127.0.0.1:{port}/callback (port selected from available: 54321, 18999, 3000)
   - code_challenge: {computed_challenge}
   - code_challenge_method: S256
   - state: {random_state}
   - scope: tenant:read tenant:write cluster:read cluster:write offline_access
   - resource: https://api.nutgraf.in (RFC 8707 Resource Indicators)

5. THE Tenant_Admin SHALL authenticate in the browser via Kratos (email/password or SSO)
6. THE Hydra SHALL validate the Kratos session and show consent screen (optional, can be skipped for trusted clients)
7. THE Hydra SHALL redirect to http://127.0.0.1:{port}/callback?code={authorization_code}&state={state}
8. THE Cursor SHALL validate the state parameter matches the original request
9. THE Cursor SHALL exchange the authorization code for tokens by sending POST to token endpoint:
   - grant_type: authorization_code
   - code: {authorization_code}
   - redirect_uri: http://127.0.0.1:{port}/callback (MUST match authorization request)
   - code_verifier: {original_verifier} (PKCE verification)
   - client_id: mcp-public-client
   - resource: https://api.nutgraf.in (RFC 8707 Resource Indicators)

10. THE Hydra SHALL verify SHA256(code_verifier) == code_challenge from the authorization request
11. THE Hydra SHALL issue a JWT access token with 24-hour TTL
12. THE Hydra SHALL issue a refresh token with 30-day TTL
13. THE JWT SHALL contain the standard sub claim and custom claims: tenant_id, email, role, and scope, strictly matching the Ory Kratos traits schema
14. THE JWT aud claim SHALL contain "https://api.nutgraf.in" per RFC 8707
15. THE token response SHALL include: access_token, refresh_token, expires_in (86400), token_type (Bearer), scope
16. THE Cursor SHALL store tokens securely in OS keychain (macOS Keychain, Windows Credential Manager, Linux Secret Service)
17. IF the authorization code expires (10 minutes), THE Hydra SHALL return error "invalid_grant"

**Note on Redirect URIs and Port Fallback:** Per MCP Specification 2025-11-25, clients use FIXED ports (54321, 18999, 3000) NOT ephemeral ports. THE Cursor SHALL attempt to bind callback listener to ports in priority order: 54321 → 18999 → 3000. THE Cursor SHALL use the first available port for the redirect_uri. IF all three ports are occupied, THE Cursor SHALL display error: "Authentication failed - ports 54321, 18999, 3000 are all in use. Close conflicting applications and retry." Redirect URIs must be pre-registered for all port variants. Clients register BOTH localhost AND 127.0.0.1 variants for each port.

### Requirement 3: Validate and Authorize Requests

**User Story:** As a system operator, I want all API requests authenticated and authorized at a single enforcement point, so that only permitted users can create tenants.

**CRITICAL:** AgentGateway is the ONLY component that interacts with the Ory stack. Backend MCP servers (zero_ops_api, crossplane_mcp) receive pre-authenticated requests with user context and NEVER validate JWTs themselves.

#### Acceptance Criteria

1. WHEN the Cursor retries tenant_create with a JWT, THE AgentGateway SHALL validate the JWT signature using cached JWKS (fetched from identity-service)
2. THE AgentGateway SHALL verify the JWT has not expired (exp claim > current time)
3. THE AgentGateway SHALL extract the subject claim, tenant_id claim, and scope claim from the JWT
4. THE AgentGateway SHALL verify the scope contains "tenant:write" for tenant_create operations
5. THE AgentGateway SHALL verify the scope contains "cluster:write" for environment_create operations
6. THE AgentGateway SHALL call identity-service to query Keto with the subject and required permission
7. THE identity-service SHALL return permission decision from Keto (allow/deny)
8. IF Keto denies permission, THEN THE AgentGateway SHALL return HTTP 403
9. IF JWT is expired, THEN THE AgentGateway SHALL return HTTP 401 with error "invalid_token" and error_description "Token expired"
10. WHEN authorization succeeds, THE AgentGateway SHALL forward the request to zero_ops_api with headers:
    - X-User-ID: {sub from JWT}
    - X-Tenant-ID: {tenant_id from JWT}
    - X-User-Email: {email from JWT}
    - X-User-Role: {role from JWT}
    - X-Scopes: {scope from JWT}
11. THE zero_ops_api SHALL trust the AgentGateway headers and SHALL NOT validate JWTs independently
12. THE zero_ops_api SHALL enforce network isolation (via Kubernetes NetworkPolicy) to exclusively accept incoming traffic from the AgentGateway namespace, ensuring JWT header trust is cryptographically secure at the network perimeter

### Requirement 4: Create Tenant Record (Idempotent)

**User Story:** As a Tenant_Admin, I want tenant metadata persisted in the database, so that the system can track organizational accounts.

**CRITICAL:** This operation is idempotent. Retrying tenant_create with the same tenant name returns the current state, not an error.

#### Acceptance Criteria

1. WHEN zero_ops_api receives a tenant_create request, IT SHALL check if the user already has an assigned tenant_id. IF the user has a tenant_id AND the requested tenant name matches their existing record, IT SHALL bypass creation and proceed to idempotency state checks. IF the requested name differs from their existing record, IT SHALL return HTTP 403 Forbidden with JSON body: {"error": "single_tenant_limit", "message": "Each user account may only be associated with one tenant.", "existing_tenant_id": "{tenant_id}"}
2. IF the tenant name does NOT exist, THE zero_ops_api SHALL insert a tenant record into PostgreSQL with name, plan, and region, and return HTTP 201 with tenant_id and status: AWAITING_CREDENTIALS
3. UPON successful DB insertion, THE zero_ops_api SHALL call the Kratos Admin API to update the user's identity traits with the new tenant_id, AND call Keto to create the relationship tuple tenant:{tenant_id}#admin@user:{sub}. IF invoked by a Platform Admin on behalf of a user, IT SHALL use the provided target_user_id parameter instead of the caller's sub
4. DURING tenant creation, THE zero_ops_api SHALL generate a new Age key pair for the tenant, store the private key as a Kubernetes Secret in the management cluster, backup the private key to the Zero-Ops Platform's internal disaster recovery S3 bucket, and embed the public key into the Git .sops.yaml scaffold
5. IF the Kratos trait update or Keto tuple creation fails after the database insert, THE zero_ops_api SHALL persist the tenant record with status INCOMPLETE_IDENTITY_SETUP and return HTTP 500. Retrying tenant_create SHALL idempotently re-attempt the missing identity steps. UPON successful completion of the identity steps during a retry, THE zero_ops_api SHALL automatically proceed to execute the Git provisioning steps (AC10-AC12) and return HTTP 201 with {"force_token_refresh": true, "tenant_id": "{tenant_id}"}
6. THE zero_ops_api SHALL return HTTP 201 with a JSON response body containing {"force_token_refresh": true, "tenant_id": "{tenant_id}"}. WHEN the Cursor client receives force_token_refresh: true, IT SHALL immediately execute the Token Refresh flow (Requirement 14) so the new tenant_id is populated in the JWT for subsequent calls
7. IF the tenant name exists AND credentials are missing, THE zero_ops_api SHALL return HTTP 200 with JSON body {"force_token_refresh": false, "tenant_id": "{tenant_id}", "phase": "AWAITING_CREDENTIALS"}
8. IF the tenant name exists AND credentials are present AND environment is not provisioned, THE zero_ops_api SHALL return HTTP 200 with JSON body {"force_token_refresh": false, "tenant_id": "{tenant_id}", "phase": "CREDENTIALS_READY"}
9. IF the tenant name exists AND environment is provisioned, THE zero_ops_api SHALL return HTTP 200 with JSON body {"force_token_refresh": false, "tenant_id": "{tenant_id}", "phase": "READY"}
10. WHEN zero_ops_api processes tenant creation, IT SHALL invoke the Git Provider API (GitHub or GitLab) to create a new repository named {tenant_id}-control-plane within the Zero-Ops organization
11. THE zero_ops_api SHALL initialize the repository with a default Kustomize structure:
   - .sops.yaml (defines Age public key encryption rule)
   - base/kustomization.yaml (configures KSOPS plugin)
   - base/secrets/ (destination for encrypted credentials)
   - overlays/starter/ (destination for Starter tier CRs)
   - overlays/enterprise/ (destination for Enterprise tier CRs)
12. THE zero_ops_api SHALL commit a tenant descriptor to the global fleet-registry repository, which SHALL trigger the management cluster's ArgoCD ApplicationSet to begin watching the new tenant repository
13. IF the Git repository creation or the fleet-registry commit fails, THE zero_ops_api SHALL NOT rollback the PostgreSQL record. Instead, it SHALL persist the tenant record with status: INCOMPLETE_GIT_SETUP, emit a critical OpenSearch event for platform alerting, and return HTTP 500 with a JSON body containing {"error_code": "git_service_unavailable", "message": "Platform repository service is currently unavailable. Our engineering team has been notified. Please try again later."}
14. WHEN tenant_create is invoked for a tenant name that already exists in INCOMPLETE_GIT_SETUP state, THE zero_ops_api SHALL idempotently retry the missing Git provisioning steps (verify repo exists, create if missing, push scaffold, commit to fleet-registry). Upon success, it SHALL update status to AWAITING_CREDENTIALS and return HTTP 200
15. THE Cursor SHALL read the status field and proceed to the appropriate next step (credential submission, environment creation, or completion)
16. ALL tenant_create calls with the same tenant name SHALL be safe to retry infinitely
17. THE PostgreSQL tenants table SHALL enforce a UNIQUE constraint on the tenant_name column
18. IF a concurrent tenant_create causes a unique constraint violation, THE zero_ops_api SHALL catch the error and return HTTP 200 with the existing tenant state (not HTTP 500)

### Requirement 5: Collect Cloud Provider Credentials (Idempotent, Async)

**User Story:** As a Tenant_Admin, I want to securely provide cloud credentials, so that the system can provision infrastructure on my behalf.

**SECURITY PRINCIPLE:** Credentials MUST NEVER transit through the AI agent or IDE client. This is a critical security requirement validated across all MCP OAuth implementations.

**CRITICAL:** Credentials are stored in Git using KSOPS (encrypted with Age). Only the private Age key is stored in S3. This operation follows the Async Agent Pattern - the Agent displays the console URL and exits. The user resumes the flow conversationally after submitting credentials.

#### Acceptance Criteria

1. WHEN tenant_create returns status: AWAITING_CREDENTIALS, THE zero_ops_api SHALL return a Platform Console URL: https://console.nutgraf.in/tenants/{tenant_id}/credentials
2. THE Cursor SHALL display the console URL to the Tenant_Admin with instructions: "Please submit your Hetzner API credentials in the Platform Console: {console_url}. Once submitted, return here and ask about your environment status."
3. THE Cursor SHALL exit immediately after displaying the console URL (no polling, no blocking)
4. THE Tenant_Admin SHALL authenticate to the Platform Console using their existing Kratos session (same identity as MCP OAuth)
5. THE Platform Console SHALL verify the Tenant_Admin has permission to submit credentials for {tenant_id} by checking X-Tenant-ID claim from JWT
6. THE Tenant_Admin SHALL enter the Hetzner API token in the authenticated console form (NOT in the agent)
7. THE Platform Console SHALL submit credentials via HTTPS POST to zero_ops_api backend with JWT authentication
8. THE zero_ops_api backend SHALL encrypt the API token using the tenant's Age public key
9. THE zero_ops_api SHALL commit the SOPS-encrypted Secret to the {tenant_id}-control-plane Git repository under base/secrets/ directory using a GitHub App Installation Token
10. THE zero_ops_api SHALL store the tenant's Age private key as a Kubernetes Secret in the management cluster for hub ArgoCD/KSOPS decryption of tenant infrastructure secrets from OCI artifacts built from Git
11. THE zero_ops_api SHALL backup the tenant's Age private key to S3 at path: s3://{tenant}-secrets/age-private-key for disaster recovery only
12. THE tenant SHALL remain in AWAITING_CREDENTIALS state indefinitely until credentials are submitted (no automatic cleanup)
13. IF credential submission fails (Git commit error), THE zero_ops_api SHALL return HTTP 500, and the Tenant_Admin MAY retry via the console form
14. WHEN the Tenant_Admin returns to Cursor and asks about environment status, THE Cursor SHALL invoke environment_status MCP tool (Requirement 17 handles resumption)
15. THE credential submission operation SHALL be idempotent - resubmitting updates the encrypted Secret in Git. IF concurrent submissions occur, the zero_ops_api SHALL process them sequentially, applying a last-write-wins resolution where the final Git commit contains the most recently submitted credentials
16. IF the Platform Console receives HTTP 401 Unauthorized during credential submission (JWT expiry), IT SHALL prompt the Tenant_Admin to re-authenticate WITHOUT clearing the entered Hetzner API token from the UI form, and automatically retry the submission upon successful re-authentication
17. ALL credential submission endpoints SHALL require HTTPS (TLS 1.2+)

### Requirement 6: Initiate Environment Provisioning (Idempotent, Async)

**User Story:** As a Tenant_Admin, I want infrastructure provisioned automatically, so that I don't need to manually configure cloud resources.

**CRITICAL:** This operation is idempotent and asynchronous. The Agent submits the intent, receives acknowledgment, and exits. The Agent does NOT block or poll for 15 minutes.

#### Acceptance Criteria

1. WHEN environment_create is invoked, THE zero_ops_api SHALL verify the requested `tier` does not exceed the tenant's current billing `plan` entitlement
2. IF the requested `tier` exceeds the `plan` entitlement, THE zero_ops_api SHALL return HTTP 403 Forbidden with a JSON error body: `{"error": "entitlement_mismatch", "message": "Your current plan does not support this tier. Please upgrade your plan in the Platform Console."}`
3. IF HTTP 403 Forbidden is returned for entitlement mismatch, THE Cursor SHALL display: "Provisioning blocked: Your current plan (Starter) does not allow provisioning an Enterprise environment. Please upgrade your plan in the Platform Console: https://console.nutgraf.in/settings/billing"
4. THE environment_create MCP tool SHALL require an environment_suffix parameter (e.g., 'staging', 'production'). THE zero_ops_api SHALL construct a globally unique environment_id as {tenant_id}-{environment_suffix}
5. IF the generated environment_id already exists, THE zero_ops_api SHALL compare the requested tier, cloud, and region against the existing environment. IF ANY single parameter differs, THE zero_ops_api SHALL return HTTP 409 Conflict with a JSON body detailing the existing parameters to prevent silent overrides
6. THE zero_ops_api SHALL commit the AINativeSaaS_CR to the {tenant_id}-control-plane Git repository under overlays/{tier}/ directory using a GitHub App Installation Token
7. IF the Git commit fails, THE zero_ops_api SHALL return HTTP 500, and the Cursor MAY retry environment_create
8. IF the Git commit succeeds, THE zero_ops_api SHALL return HTTP 202 with response body containing:
   - tenant_id
   - environment_id (e.g., acme-corp-production)
   - console_url (e.g., https://console.nutgraf.in/environments/{environment_id})
   - estimated_duration_minutes (15 for Enterprise, 1 for Starter)
9. IF environment_create is called again for the same environment_id with matching parameters, THE zero_ops_api SHALL return HTTP 200 (idempotent) returning the exact same JSON response body schema as the HTTP 202 response (tenant_id, environment_id, console_url, estimated_duration_minutes)
10. THE Cursor SHALL display the console_url and a message: "Provisioning started in the background. Track progress at: {console_url}"
11. THE Cursor SHALL NOT block user input or poll for provisioning completion
12. THE Cursor execution SHALL complete immediately after displaying the console_url

### Requirement 7: Execute Crossplane Composition (Eventual Consistency)

**User Story:** As a system operator, I want Crossplane to provision enterprise infrastructure, so that tenants receive consistent, compliant environments.

**CRITICAL:** Crossplane reconciles continuously. There is no "timeout" or "max retries" - only eventual consistency.

#### Acceptance Criteria

1. WHEN the AINativeSaaS_CR is committed, THE Crossplane SHALL detect the new resource
2. THE Crossplane SHALL select Composition_B based on the enterprise tier
3. THE Crossplane SHALL provision Hetzner resources using the decrypted API token (decrypted by KSOPS from Git using the tenant's Age private key stored in the management cluster)
4. THE Crossplane Composition B SHALL utilize a provider-kubernetes Object resource to securely copy the tenant's Age private key Secret from the management cluster directly into the provisioned tenant cluster's ArgoCD namespace. The provider-kubernetes controller SHALL operate using a least-privilege ServiceAccount restricted via RBAC to reading only Secrets labeled nutgraf.in/tenant-age-key=true
5. THE tenant cluster bootstrap SHALL deploy ArgoCD and KSOPS, configuring ArgoCD to use the injected Age private key Secret to automatically decrypt tenant application secrets from OCI artifacts built from Git
6. THE Composition_B SHALL typically complete within 15 minutes under normal conditions, including tenant cluster provisioning and ArgoCD bootstrap
7. WHEN provisioning completes successfully, THE Crossplane SHALL update the AINativeSaaS_CR status to Ready: True
8. THE Crossplane SHALL NEVER enter a terminal "failed" state - only Degraded or Unready states that allow continued reconciliation

### Requirement 8: Handle Provisioning Errors (Continuous Reconciliation)

**User Story:** As a Tenant_Admin, I want clear error messages when provisioning encounters issues, so that I can take corrective action.

**CRITICAL:** Crossplane continuously reconciles. Errors are surfaced as Conditions, not terminal failures. Provisioning resumes automatically when external issues are resolved.

#### Acceptance Criteria

1. IF Hetzner quota is exceeded, THE Crossplane SHALL update the AINativeSaaS_CR status with Condition: Ready: False, Reason: QuotaExceeded, Message: "Hetzner quota exceeded in {region}"
2. IF the API token is invalid, THE Crossplane SHALL update the AINativeSaaS_CR status with Condition: Ready: False, Reason: AuthenticationFailed, Message: "Invalid Hetzner API token"
3. WHEN the Tenant_Admin requests status via the Agent, THE zero_ops_api SHALL return the current AINativeSaaS_CR Conditions via the environment_status MCP tool (Requirement 9)
4. THE Cursor SHALL display the Condition message when the Tenant_Admin explicitly requests status
5. THE Crossplane SHALL continuously retry reconciliation with exponential backoff (no max retry limit)
6. WHEN the external issue is resolved (quota increased, token fixed), THE Crossplane SHALL automatically resume provisioning without manual intervention
7. IF the Tenant_Admin wants to abort provisioning, they MUST invoke environment_delete, which triggers Crossplane to garbage-collect all partially created resources
8. THE zero_ops_api SHALL retain the AINativeSaaS_CR in Git for audit purposes even after deletion (Git history)

### Requirement 9: Query Environment Status (On-Demand)

**User Story:** As a Tenant_Admin, I want to check my environment status at any time, so that I know when provisioning completes or if issues occur.

**CRITICAL:** Status is queried on-demand, not via continuous polling. The Agent queries status only when explicitly requested by the user.

#### Acceptance Criteria

1. WHEN the Tenant_Admin asks for environment status (e.g., "Is my environment ready?"), THE Cursor SHALL invoke the environment_status MCP tool
2. THE environment_status MCP tool SHALL route through AgentGateway with JWT authentication
3. THE AgentGateway SHALL validate the JWT and query identity-service Keto to authorize read access for the specific tenant_id
4. THE zero_ops_api SHALL fetch the AINativeSaaS_CR from the Kubernetes API
5. THE zero_ops_api SHALL map Crossplane Conditions to a normalized phase enum: Pending, Provisioning, Ready, or Degraded
6. THE zero_ops_api SHALL return a JSON response containing the fields defined in Requirement 16 AC4
7. THE Cursor SHALL display the phase and summary_message to the Tenant_Admin
8. IF phase is Ready, THE Cursor SHALL display provisioned resource endpoints (cluster endpoint, database connection reference, ArgoCD URL, Grafana URL)
9. IF phase is Degraded, THE Cursor SHALL display the error reason and suggest remediation (e.g., "Increase Hetzner quota or try a different region")
10. THE zero_ops_api SHALL expose an environments_list MCP tool mapped to GET /api/v1/tenants/{tenant_id}/environments. It SHALL route through AgentGateway enforcing JWT and Keto authorization identical to other endpoints
11. THE environments_list endpoint SHALL return an array of objects matching the environment status schema (defined in Req 16). IF the tenant has no environments, it SHALL return HTTP 200 with an empty array []
12. WHEN the Tenant_Admin asks generally about their environments without specifying an ID, THE Cursor SHALL invoke environments_list to fetch all environments and prompt the user to disambiguate which environment they are referring to

### Requirement 10: Cache JWKS for Performance

**User Story:** As a system operator, I want JWT validation to be fast, so that API requests have low latency.

**CRITICAL:** AgentGateway fetches JWKS from identity-service (which proxies Hydra), NOT directly from Hydra.

#### Acceptance Criteria

1. WHEN the AgentGateway starts, THE AgentGateway SHALL call identity-service to fetch the JWKS
2. THE identity-service SHALL fetch JWKS from Hydra and return it to AgentGateway
3. THE AgentGateway SHALL cache the JWKS in memory with a 1-hour TTL
4. WHEN validating a JWT, THE AgentGateway SHALL use the cached JWKS
5. IF the JWT signature fails validation, THEN THE AgentGateway SHALL call identity-service to refresh the JWKS cache once
6. IF validation fails after refresh, THEN THE AgentGateway SHALL return HTTP 401

### Requirement 11: Parse and Format Configuration (Internal - Library Quality)

**User Story:** As a developer, I want to parse AINativeSaaS_CR YAML, so that I can validate and manipulate tenant configurations.

**CRITICAL:** This is an internal library requirement for crossplane_mcp service, not a user-facing Journey A step. This requirement drives unit tests, not E2E tests.

#### Acceptance Criteria

1. WHEN an AINativeSaaS_CR is provided, THE Parser SHALL parse it into a Configuration object
2. WHEN an invalid AINativeSaaS_CR is provided, THE Parser SHALL return a descriptive error with line number
3. THE Pretty_Printer SHALL format Configuration objects back into valid YAML
4. FOR ALL valid Configuration objects, parsing then printing then parsing SHALL produce an equivalent object
5. THE Parser SHALL validate required fields: tier, cloud, region

### Requirement 12: OAuth Metadata Discovery

**User Story:** As an MCP client, I want to discover OAuth endpoints dynamically, so that I can adapt to different authorization server configurations.

**CRITICAL:** OAuth metadata is exposed by AgentGateway (proxying identity-service), NOT by individual MCP servers.

#### Acceptance Criteria

1. THE AgentGateway SHALL expose `GET /.well-known/oauth-protected-resource` returning:
   - `resource`: https://api.nutgraf.in
   - `authorization_servers`: ["https://auth.nutgraf.in"]
   - `bearer_methods_supported`: ["header"]
   - `scopes_supported`: ["tenant:read", "tenant:write", "cluster:read", "cluster:write"]

2. THE identity-service SHALL expose `GET /.well-known/oauth-authorization-server` (proxying Hydra) returning:
   - `issuer`: https://auth.nutgraf.in
   - `authorization_endpoint`: https://auth.nutgraf.in/oauth2/auth
   - `token_endpoint`: https://auth.nutgraf.in/oauth2/token
   - `jwks_uri`: https://auth.nutgraf.in/.well-known/jwks.json
   - `response_types_supported`: ["code"]
   - `grant_types_supported`: ["authorization_code", "refresh_token"]
   - `code_challenge_methods_supported`: ["S256"]
   - `token_endpoint_auth_methods_supported`: ["none"]
   - `client_id_metadata_document_supported`: true

3. THE MCP client SHALL discover endpoints by:
   - Step 1: Receive 401 from AgentGateway with WWW-Authenticate header containing resource_metadata URL
   - Step 2: Fetch resource metadata from AgentGateway to get authorization_servers array
   - Step 3: Fetch authorization server metadata from identity-service to get endpoints
   - Step 4: Initiate PKCE flow using discovered endpoints

### Requirement 13: Client Registration (Pre-registered + CIMD)

**User Story:** As an MCP client, I want to authenticate without manual configuration, so that I can access the platform with zero setup.

**CRITICAL:** Per MCP Specification 2025-11-25, pre-registered credentials and Client ID Metadata Documents (CIMD) are the recommended approaches. Dynamic Client Registration (DCR) is NOT implemented due to security risks (unbounded database growth, open registration attacks).

#### Acceptance Criteria

**Pre-registered Client (Primary - Zero-Config):**

1. THE identity-service SHALL pre-register a public client with Hydra:
   - `client_id`: mcp-public-client
   - `client_name`: Zero-Ops MCP Client
   - `redirect_uris`: [
       "http://127.0.0.1:54321/callback",
       "http://localhost:54321/callback",
       "http://127.0.0.1:18999/callback",
       "http://localhost:18999/callback",
       "http://127.0.0.1:3000/callback",
       "http://localhost:3000/callback",
       "cursor://anysphere.cursor-mcp/oauth/callback"
     ]
   - `grant_types`: ["authorization_code", "refresh_token"]
   - `response_types`: ["code"]
   - `token_endpoint_auth_method`: "none"

2. THE Cursor/Goose clients SHALL use `client_id: mcp-public-client` by default (hardcoded)

3. THE identity-service SHALL normalize redirect URIs during authorization:
   - IF client sends "localhost", ALSO accept "127.0.0.1" variant
   - IF client sends "127.0.0.1", ALSO accept "localhost" variant

**CIMD (Secondary - Future Clients):**

4. THE identity-service SHALL advertise CIMD support in authorization server metadata:
   - `client_id_metadata_document_supported`: true

5. THE MCP client MAY host client metadata at an HTTPS URL (e.g., https://newclient.com/.well-known/client-metadata.json)

6. THE client metadata document SHALL include:
   ```json
   {
     "client_id": "https://newclient.com/.well-known/client-metadata.json",
     "client_name": "New MCP Client",
     "client_uri": "https://newclient.com",
     "redirect_uris": [
       "http://127.0.0.1:3000/callback",
       "http://localhost:3000/callback"
     ],
     "grant_types": ["authorization_code", "refresh_token"],
     "response_types": ["code"],
     "token_endpoint_auth_method": "none"
   }
   ```

7. WHEN the MCP client sends authorization request with HTTPS URL as client_id, THE identity-service SHALL fetch the metadata document

8. THE identity-service SHALL validate:
   - client_id in document matches the URL exactly
   - Document is valid JSON with required fields
   - redirect_uris are valid per MCP spec

9. THE identity-service SHALL cache metadata respecting HTTP cache headers

10. THE identity-service SHALL implement SSRF protections:
    - Block private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.0/8)
    - Require HTTPS scheme only
    - Implement request timeouts (5 seconds)
    - Rate limit metadata fetches per client_id (10 requests per hour)

11. THE Hydra SHALL validate redirect URIs per MCP Specification 2025-11-25:
    - HTTPS URLs: Always allowed
    - HTTP loopback: Only localhost or 127.0.0.1 (exact ports, no wildcards)
    - Custom schemes: cursor://, goose:// (optional, for IDE compatibility)

**Priority Order (per MCP spec):**
1. Pre-registered `mcp-public-client` (zero-config for Cursor/Goose)
2. CIMD (for future clients with HTTPS metadata URLs)
3. Manual entry (if neither available)

### Requirement 14: Token Refresh

**User Story:** As an MCP client, I want to refresh expired access tokens automatically, so that users don't need to re-authenticate frequently.

**CRITICAL:** Token refresh is handled by identity-service (proxying Hydra). The 24-hour access token TTL covers the full 15-minute provisioning window (Requirement 7). However, for long-running operations or multi-day workflows, automatic token refresh is required.

#### Acceptance Criteria

1. WHEN the AgentGateway returns 401 with error "invalid_token" and error_description "Token expired", THE Cursor SHALL attempt token refresh transparently
2. THE Cursor SHALL send `POST {token_endpoint}` to identity-service with:
   - `grant_type=refresh_token`
   - `refresh_token={refresh_token}`
   - `client_id={client_id}`
   - `scope=tenant:read tenant:write cluster:read cluster:write offline_access`

3. THE identity-service SHALL forward the request to Hydra
4. THE Hydra SHALL validate the refresh token (30-day TTL)
5. THE Hydra SHALL issue a new access token with 24-hour TTL. THE identity-service SHALL configure Hydra to re-hydrate custom claims (tenant_id, email, role) from the latest Kratos identity traits during the refresh grant, ensuring newly assigned tenant_ids are successfully populated into the new token
6. THE Hydra SHALL rotate the refresh token (issue new refresh token, invalidate old one)
7. THE identity-service SHALL return the new tokens to the Cursor
8. THE Cursor SHALL update stored tokens in OS keychain
9. THE Cursor SHALL retry the original failed request with the new access token
10. IF refresh fails (expired/revoked), THE Cursor SHALL restart the authorization flow and notify the user
11. THE Cursor SHALL NOT show errors to the user during transparent token refresh (only on refresh failure)

**Scenario: Token Expiry Between tenant_create and environment_create**
- GIVEN the Tenant_Admin completes tenant_create at T=0
- AND the access token expires at T=24h
- WHEN the Tenant_Admin invokes environment_create at T=25h
- THEN the AgentGateway returns 401 "Token expired"
- AND the Cursor transparently refreshes the token via identity-service
- AND the Cursor retries environment_create with the new access token
- AND the operation succeeds without user intervention

### Requirement 15: Platform Git Authentication and Secret Bootstrap

**User Story:** As a system operator, I want the platform to securely authenticate with Git and automatically recover from cluster loss, so that GitOps reconciliation via Git → CI → OCI is secure and resilient.

**CRITICAL:** This requirement solves the "Secret Zero" problem in GitOps. The Master Platform Age Private Key is the single secret that must be injected imperatively during bootstrap. All other secrets (GitHub App Private Key, tenant credentials) are encrypted in Git and decrypted at apply-time using this master key.

**Industry Context:** All GitOps secret management tools (KSOPS, Sealed Secrets, External Secrets Operator) require bootstrap injection of a master secret. No operator eliminates this step - it is a fundamental security requirement. The pattern used here matches CNCF best practices for GitOps secret management with Git → CI → OCI artifact distribution. ArgoCD Agent (https://github.com/argoproj-labs/argocd-agent/) natively supports OCI artifacts as application sources (see [ArgoCD OCI Documentation](https://argo-cd.readthedocs.io/en/latest/user-guide/oci/)).

#### Acceptance Criteria

1. **AC 15.1 (Bootstrap):** THE management cluster SHALL contain a Master Platform Age Private Key, injected exclusively at cluster creation time via the `zero-ops mgmt bootstrap` CLI command
2. **AC 15.2 (Decryption):** THE `zero_ops_api` SHALL read the GitHub App Private Key from a mounted Kubernetes Secret. This Secret SHALL be synced from the `fleet-registry` Git repository and decrypted at apply-time by ArgoCD/KSOPS using the Master Platform Age Private Key
3. **AC 15.3 (Token Generation):** WHEN `zero_ops_api` needs to commit to a tenant repository, IT SHALL dynamically generate a short-lived GitHub App Installation Token using the mounted GitHub App Private Key
4. **AC 15.4 (Token Cache):** THE `zero_ops_api` SHALL cache the generated Installation Token in memory for up to 55 minutes (proactively expiring before the strict 1-hour GitHub TTL)
5. **AC 15.5 (Idempotent Refresh):** IF a Git commit operation returns HTTP 401 Unauthorized (indicating premature token expiry or revocation), THE `zero_ops_api` SHALL immediately invalidate the cached token, generate a fresh Installation Token, and retry the commit operation EXACTLY ONCE
6. **AC 15.6 (Terminal Failure):** IF the retry using a freshly generated token also returns HTTP 401, THE `zero_ops_api` SHALL abort the operation, return HTTP 500 to the Agent, and log a critical authorization error
7. **AC 15.7 (Disaster Recovery):** THE Master Platform Age Private Key SHALL be backed up securely off-cluster in the Zero-Ops organization's enterprise password vault (e.g., 1Password, Bitwarden, or offline secure vault)
8. **AC 15.8 (Cluster Recreation):** IF the management cluster is destroyed, THE Platform Admin SHALL run `zero-ops mgmt bootstrap --name=shard-eu-1 --master-age-key=$SECURE_VAULT_KEY` to provision a new cluster, install ArgoCD Agent (https://github.com/argoproj-labs/argocd-agent/), and inject the master key. ArgoCD Agent SHALL connect to Git, decrypt the GitHub App Private Key, and the entire platform SHALL auto-reconcile back into existence

**Rationale for Single Idempotent Retry:**
- GitHub App Installation Tokens have deterministic 1-hour expiry (not transient network errors)
- Proactive 55-minute cache prevents expiry under normal conditions
- Single retry handles edge cases: premature expiry, token revocation, clock skew
- Exponential backoff is inappropriate for deterministic authorization failures
- Fast failure (2 attempts max) provides clear signal for critical auth issues

**Tools Comparison:**
- KSOPS: Requires `kubectl create secret` with Age private key during bootstrap
- Sealed Secrets: Controller generates keypair, admin must backup private key manually
- External Secrets Operator: Requires `kubectl create secret` with cloud credentials during bootstrap
- **Zero-Ops approach**: Matches KSOPS pattern (industry standard for GitOps + SOPS + Age)

### Requirement 16: Environment Status Schema and Phase Mapping

**User Story:** As a developer, I want a consistent status schema across all environments, so that I can build reliable integrations.

#### Acceptance Criteria

1. THE zero_ops_api SHALL expose the environment_status MCP tool, mapped to GET /api/v1/environments/{environment_id}/status. TO support pre-environment routing, THE zero_ops_api SHALL ALSO expose a tenant-level status endpoint mapped to GET /api/v1/tenants/{tenant_id}/status. This tenant endpoint SHALL handle all pre-environment phases (INCOMPLETE_IDENTITY_SETUP, INCOMPLETE_GIT_SETUP, AWAITING_CREDENTIALS, CREDENTIALS_READY)
2. THE endpoint SHALL require JWT authentication via AgentGateway
3. THE AgentGateway SHALL validate JWT and query identity-service Keto to authorize read access for the specific tenant_id
4. BOTH endpoints SHALL return the identical JSON response schema (pre-environment states will return null for environment-specific fields):
```json
{
  "tenant_id": "string",
  "environment_name": "string | null",
  "tier": "starter | enterprise | null",
  "phase": "INCOMPLETE_IDENTITY_SETUP | INCOMPLETE_GIT_SETUP | AWAITING_CREDENTIALS | CREDENTIALS_READY | Pending | Provisioning | Ready | Degraded",
  "summary_message": "string",
  "duration_seconds": "integer | null",
  "console_url": "string | null",
  "crossplane_conditions": [
    {
      "type": "string",
      "status": "True | False | Unknown",
      "reason": "string",
      "message": "string",
      "lastTransitionTime": "RFC3339 timestamp"
    }
  ]
}
```
5. THE zero_ops_api SHALL derive phase from Crossplane Conditions using these rules:
   - CREDENTIALS_READY: PostgreSQL tenant record exists with credentials submitted, but no environment provisioning intent recorded
   - Pending: The PostgreSQL DB confirms the environment intent exists, BUT the Kubernetes API returns 404 for the AINativeSaaS_CR (ArgoCD has not yet synced)
   - Provisioning: Condition Ready: False with Reason: Creating, Syncing, or Reconciling
   - Ready: Condition Ready: True
   - Degraded: Condition Ready: False with Reason containing "Error", "Exceeded", "Failed", or "Invalid"
6. THE summary_message SHALL be a single-sentence human-readable interpretation of the current phase:
   - INCOMPLETE_IDENTITY_SETUP: "Platform encountered an error completing account setup. Retry tenant creation to resolve automatically."
   - INCOMPLETE_GIT_SETUP: "Platform repository service was unavailable during setup. Retry tenant creation to resolve automatically."
   - AWAITING_CREDENTIALS: "Account setup complete. Please submit your cloud provider credentials in the Platform Console."
   - CREDENTIALS_READY: "Credentials submitted successfully. Ready to provision infrastructure."
   - Pending: "Environment committed to Git, waiting for ArgoCD sync"
   - Provisioning: "Crossplane is provisioning infrastructure (estimated {tier_duration} minutes)"
   - Ready: "Environment is ready. All resources provisioned successfully."
   - Degraded: "{error_reason}. Crossplane will retry automatically."
7. IF phase is Degraded, THE summary_message SHALL include the specific error reason and suggested remediation
8. THE zero_ops_api SHALL cache the Kubernetes API response for 5 seconds to reduce API load during console polling

### Requirement 17: Conversational Resumability

**User Story:** As a Tenant_Admin, I want to resume onboarding from any point, so that I don't lose progress if my IDE closes.

#### Acceptance Criteria

1. IF the Cursor is closed during provisioning, THE tenant state SHALL persist in PostgreSQL and Git
2. WHEN the Tenant_Admin opens a new Cursor session and asks "What is the status of my environment?", THE Cursor SHALL first invoke environments_list to check if any environments exist. IF environments exist, THE Cursor SHALL invoke environment_status with the environment_id. IF no environments exist, THE Cursor SHALL invoke the tenant-level status endpoint
3. THE zero_ops_api SHALL return the current phase and allow the Agent to determine the next action
4. IF phase is INCOMPLETE_IDENTITY_SETUP or INCOMPLETE_GIT_SETUP, THE Cursor SHALL display: "There was a transient platform error during your account setup. Please ask me to retry tenant creation to resume." and await user confirmation to invoke tenant_create
5. IF phase is AWAITING_CREDENTIALS, THE Cursor SHALL prompt for credential submission
6. IF phase is CREDENTIALS_READY, THE Cursor SHALL prompt the Tenant_Admin to provide their desired tier, cloud provider, and region parameters, and upon receiving them, invoke environment_create
7. IF phase is Provisioning or Degraded, THE Cursor SHALL display the current status and console_url
8. IF phase is Ready, THE Cursor SHALL display the provisioned resource summary
9. THE Platform Console SHALL always reflect the current state regardless of Agent session

### Requirement 18: Platform Console Polling Strategy

**User Story:** As a Tenant_Admin viewing the Platform Console, I want real-time status updates, so that I can monitor provisioning progress.

**CRITICAL:** The Platform Console (UI) handles automated polling, NOT the Agent. The Agent only queries status on explicit user request.

#### Acceptance Criteria

1. THE Platform Console SHALL poll the environment_status endpoint every 10 seconds while phase is Pending or Provisioning
2. THE Platform Console SHALL use a fixed 10-second interval (no exponential backoff)
3. WHEN phase transitions to Ready or Degraded, THE Platform Console SHALL stop polling and display the final state
4. IF polling exceeds 20 minutes, THE Platform Console SHALL display a soft timeout message: "Provisioning is taking longer than expected. Crossplane is continuously working in the background. Refresh this page to check progress."
5. THE Platform Console SHALL continue to allow manual refresh even after the soft timeout
6. ALL polling requests SHALL include the JWT in the Authorization header and route through AgentGateway

### Requirement 19: Abort and Delete Environment

**User Story:** As a Tenant_Admin, I want to abort a degraded provisioning attempt or delete an existing environment, so that I can free up cloud quotas and halt billing.

**CRITICAL:** State-aware deletion policy balances UX (no approval for failed provisioning) with safety (approval required for environments that held data). Tenant Admin is the approver (BYOC model - Zero-Ops platform engineers are not in the approval loop for tenant-owned data).

#### Acceptance Criteria

1. WHEN the Tenant_Admin issues a deletion command, THE Cursor SHALL invoke the environment_delete MCP tool with the specific environment_id (e.g., acme-corp-production)
2. THE AgentGateway SHALL validate the JWT and query identity-service Keto to ensure the user has delete permissions for the specified environment_id
3. THE zero_ops_api SHALL evaluate the historical state of the AINativeSaaS_CR. IF the environment has NEVER achieved a Ready status (phase is Pending, Provisioning, or Degraded), THE zero_ops_api SHALL proceed with immediate deletion
4. IF the environment has previously achieved a Ready status, THE zero_ops_api SHALL reject immediate deletion to enforce data safety invariants (PRD 5.10.4). IT SHALL generate a Destructive Operation Approval Ticket assigned to the Tenant's administrators, and return HTTP 403 Forbidden with response body: {"error": "approval_required", "message": "Even though the environment may be Degraded, it previously held data. Deletion requires secondary confirmation to prevent data loss.", "approval_url": "https://console.nutgraf.in/approvals/{ticket_id}"}
5. IF an Approval Ticket is already pending for the requested environment_id, THE zero_ops_api SHALL idempotently return HTTP 403 Forbidden with the ticket_id and approval_url of the existing pending ticket
6. WHEN HTTP 403 is returned for deletion approval, THE Cursor SHALL display: "Deletion requires secondary confirmation to prevent data loss. Please review and approve the teardown ticket here: https://console.nutgraf.in/approvals/{ticket_id}"
7. THE pending deletion ticket MAY be approved by any user possessing the tenant_admin role for that tenant_id, OR by a user with the platform_admin role (for support overrides). Notifications SHALL be routed via the Platform Console
8. THE Tenant_Admin MAY approve OR cancel the pending deletion ticket via the Platform Console
9. IF the Approval Ticket is not actioned within 7 days, THE zero_ops_api SHALL automatically mark the ticket as Expired, leaving the environment untouched. The Platform Console SHALL display "Expired - Request New Deletion" and allow the Tenant_Admin to immediately re-invoke environment_delete to generate a fresh ticket with a new 7-day window
10. IF an Approval Ticket is marked Expired or Cancelled, the Tenant_Admin MAY immediately re-invoke the environment_delete MCP tool to generate a new 7-day approval ticket (restarting the clock). The Platform Console SHALL display the previous ticket state as "Expired - Request New Deletion"
11. FOR approved or immediate deletions, THE zero_ops_api SHALL commit the removal of the AINativeSaaS_CR manifest from the {tenant_id}-control-plane Git repository under overlays/{tier}/ directory
12. IF the Git commit fails, THE zero_ops_api SHALL return HTTP 500 with JSON body {"error_code": "git_service_unavailable", "message": "Platform repository service is currently unavailable. Our engineering team has been notified. Please try again later."}
13. WHEN the Git commit succeeds, THE zero_ops_api SHALL return HTTP 202 Accepted
12. THE Cursor SHALL NOT block or poll during deletion. THE Cursor SHALL display: "Teardown initiated. Crossplane is garbage-collecting resources (~3 minutes). Monitor at: {console_url}"
13. THE management cluster's ArgoCD SHALL detect the Git removal, prune the CR from Kubernetes, and trigger Crossplane finalizers to tear down the associated cloud infrastructure
14. THE Crossplane finalizers SHALL delete Hetzner resources (VMs, volumes, networks) and typically complete within 1-3 minutes
15. THE Git commit history SHALL serve as the immutable audit log of the deletion
16. THE OpenSearch SHALL capture K8s deletion events for the environment
17. IF environment_delete is invoked while the AINativeSaaS_CR is already deleted from Git but Crossplane teardown is actively running, THE zero_ops_api SHALL return HTTP 202 Accepted with message: "Deletion already in progress"
18. THE environment_delete operation SHALL be idempotent - deleting an already-deleted environment_id returns HTTP 200 with message: "Environment already deleted"
