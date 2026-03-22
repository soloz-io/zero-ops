# Requirements Document

## Introduction

This specification defines the complete agentic enterprise onboarding journey, enabling tenant administrators to provision enterprise-tier environments through natural language commands in their IDE. The system handles OAuth2 Authorization Code Flow with PKCE authentication, authorization, tenant creation, encrypted credential storage via Infisical, and automated infrastructure provisioning via Crossplane + CAPI.

**Architecture Principles:**
- **Idempotent Operations**: All API operations are safe to retry infinitely. The system returns current state, not errors, for duplicate requests.
- **Declarative State Machine**: The system is not transactional. Each operation moves the tenant through states: AWAITING_CREDENTIALS → CREDENTIALS_READY → PROVISIONING → READY.
- **Eventual Consistency**: Crossplane reconciles continuously. There are no terminal failure states, only degraded states that self-heal when external issues resolve.
- **GitOps-First**: All infrastructure changes are committed to Git. Standard ArgoCD with ApplicationSets reconciles from Git repositories. Crossplane provisions infrastructure using CAPI as the underlying cluster provisioning engine.
- **Async Agent Pattern**: The Agent submits intents and exits immediately. It does NOT block or poll for long-running operations. Status is queried on-demand via conversational prompts or Platform Console.
- **Crossplane + CAPI Pattern**: Crossplane provides the declarative API layer (AINativeSaaS CRs) while CAPI handles the actual cluster provisioning underneath. This separation allows tenant-facing abstractions while leveraging battle-tested CAPI for infrastructure.
- **Secrets Management**: Self-hosted Infisical for secrets storage and Teleport for privileged access management (PAM). Credentials are stored encrypted in Infisical and injected into clusters via External Secrets Operator.
- **Observability**: VictoriaMetrics for centralized metrics, Grafana for dashboards, and Grafana Alloy agents on spoke clusters pushing telemetry to the hub. Tracks tenant count, cluster health, ArgoCD sync status, and resource utilization.

**GitOps Pattern**: Zero-Ops uses standard ArgoCD with ApplicationSets for GitOps reconciliation. Git serves as the source of truth, ArgoCD watches repositories and syncs manifests to clusters. Crossplane uses CAPI providers underneath to provision actual infrastructure.

**Hub-Spoke Architecture**: The hub cluster hosts all control-plane services: global identity (Ory Kratos, Hydra, Keto, auth-proxy), AgentGateway, ArgoCD, Crossplane, NATS JetStream Cluster, Hub Event Router, Hub Centralised DB (PostgreSQL — fleet state), Control Plane Shared DB (PostgreSQL — tenant config), VictoriaMetrics, Loki, Grafana. Spoke clusters host isolated tenant workloads and run Grafana Alloy agents that push metrics and logs to the hub asynchronously via remote_write. ClickHouse is deferred until PostgreSQL billing queries become a bottleneck (typically 10M+ rows).

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

- **AgentGateway**: Single authentication and authorization enforcement point. Validates JWTs, enforces RBAC via auth-proxy, and routes tool calls to backend MCP server
- **auth-proxy**: Go-based custom implementation that interfaces with the Ory stack (Hydra, Kratos, Keto) on behalf of AgentGateway
- **Cursor**: IDE client that invokes MCP tools on behalf of the Tenant_Admin
- **Tenant_Admin**: User with administrative privileges who initiates onboarding
- **Platform_Admin**: Operator managing the hub cluster and platform infrastructure
- **Hydra**: OAuth2 server that issues JWTs after Authorization Code Flow with PKCE (accessed via auth-proxy)
- **Kratos**: Identity provider that handles user authentication (accessed via auth-proxy)
- **Keto**: Authorization service that evaluates permission policies (accessed via auth-proxy)
- **Spoke Controller**: A controller-runtime operator running on each spoke cluster (both Pool and Silo). Watches Crossplane `AINativeSaaS` claim conditions on the spoke. Derives platform status (provisioning/ready/failed/deleting) from conditions and writes directly to Hub Centralised DB via Hub-side PostgREST (`POST /resource-status`, Bearer JWT auth, RLS enforces per-spoke write scope). Uses controller-runtime native retry with exponential backoff. Does NOT run on the hub. Does NOT write directly to PostgreSQL — writes via PostgREST only.
- **mcp-server**: Single backend MCP server that exposes ALL platform tools (tenant_create, environment_create, environment_status, etc.). Uses internal/opensbt toolkit for business logic. Receives pre-authenticated requests from AgentGateway with X-Auth-* headers.
- **Composition_B**: Crossplane composition for enterprise-tier infrastructure that uses CAPI providers underneath
- **PKCE**: Proof Key for Code Exchange - security extension for OAuth public clients
- **code_verifier**: Random string generated by client for PKCE flow
- **code_challenge**: SHA256 hash of code_verifier, sent in authorization request
- **JWT**: JSON Web Token used for authenticated API requests
- **AINativeSaaS_CR**: Custom resource defining tenant environment configuration
- **JWKS**: JSON Web Key Set used to validate JWT signatures
- **Idempotent**: Operation that can be safely retried infinitely with the same result
- **Eventual Consistency**: System state converges to desired state over time through continuous reconciliation
- **fleet-registry**: Global Git repository containing tenant descriptors that trigger ArgoCD ApplicationSet to watch new tenant control plane repositories
- **GitHub App Installation Token**: Short-lived JWT used by zero_ops_api to authenticate Git API operations (repo creation, commits)
- **Plan**: The billing entitlement associated with a tenant record in PostgreSQL (e.g., Starter, Enterprise). Determines which infrastructure tiers the tenant is authorized to provision
- **Tier**: The infrastructure topology specified in the AINativeSaaS XRD (e.g., starter, enterprise). Defines the actual resources provisioned by Crossplane
- **Hub_Cluster**: Central management cluster hosting control-plane services (VictoriaMetrics, Grafana, ArgoCD, Crossplane, identity services)
- **Spoke_Cluster**: Tenant-dedicated Kubernetes cluster provisioned via Crossplane + CAPI, hosting isolated tenant workloads
- **VictoriaMetrics**: Time-series database for centralized metrics storage on the hub cluster
- **Grafana**: Visualization and dashboarding platform for observability data
- **Grafana Alloy**: Lightweight telemetry agent deployed on spoke clusters that pushes metrics to the hub
- **Infisical**: Self-hosted secrets management platform for storing encrypted cloud provider credentials
- **Teleport**: Privileged access management (PAM) platform for audited, time-limited access to clusters
- **External Secrets Operator (ESO)**: Kubernetes operator that syncs secrets from Infisical to cluster namespaces
- **ClusterClass**: CAPI template defining cluster topology (control plane + worker nodes) for spoke cluster provisioning
- **CAPI**: Cluster API - Kubernetes-native declarative API for cluster lifecycle management (used by Crossplane underneath)
- **ClusterResourceSet (CRS)**: CAPI feature for automatic addon installation (CNI, CCM, CSI) during cluster bootstrap


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

**CRITICAL:** AgentGateway is the ONLY component that interacts with the Ory stack. The backend MCP server (mcp-server) receives pre-authenticated requests with user context and NEVER validates JWTs itself.

#### Acceptance Criteria

1. WHEN the Cursor retries tenant_create with a JWT, THE AgentGateway SHALL validate the JWT signature using cached JWKS (fetched from auth-proxy)
2. THE AgentGateway SHALL verify the JWT has not expired (exp claim > current time)
3. THE AgentGateway SHALL extract the subject claim, tenant_id claim, and scope claim from the JWT
4. THE AgentGateway SHALL verify the scope contains "tenant:write" for tenant_create operations
5. THE AgentGateway SHALL verify the scope contains "cluster:write" for environment_create operations
6. THE AgentGateway SHALL call auth-proxy to query Keto with the subject and required permission
7. THE auth-proxy SHALL return permission decision from Keto (allow/deny)
8. IF Keto denies permission, THEN THE AgentGateway SHALL return HTTP 403

**Implementation Note:** ACs 6, 7, and 8 (Keto permission checks) are deferred to Demo 2 (Req 3 — Authorization Enforcement per PRD section 5.3.3). Demo 1 implements ACs 1–5 (JWT validation + scope check) and AC 9 onwards. The Keto call in AC6 returns a pass-through allow until Demo 2.

9. IF JWT is expired, THEN THE AgentGateway SHALL return HTTP 401 with error "invalid_token" and error_description "Token expired"
10. WHEN authorization succeeds, THE AgentGateway SHALL forward the request to mcp-server with headers:
    - X-User-ID: {sub from JWT}
    - X-Tenant-ID: {tenant_id from JWT}
    - X-User-Email: {email from JWT}
    - X-User-Role: {role from JWT}
    - X-Scopes: {scope from JWT}
11. THE mcp-server SHALL trust the AgentGateway headers and SHALL NOT validate JWTs independently
12. THE mcp-server SHALL enforce network isolation (via Kubernetes NetworkPolicy) to exclusively accept incoming traffic from the AgentGateway namespace, ensuring JWT header trust is cryptographically secure at the network perimeter

### Requirement 4: Create Tenant Record (Idempotent)

**User Story:** As a Tenant_Admin, I want tenant metadata persisted in the database, so that the system can track organizational accounts.

**CRITICAL:** This operation is idempotent. Retrying tenant_create with the same tenant name returns the current state, not an error.

#### Acceptance Criteria

1. WHEN mcp-server receives a tenant_create request, IT SHALL check if the user already has an assigned tenant_id. IF the user has a tenant_id AND the requested tenant name matches their existing record, IT SHALL bypass creation and proceed to idempotency state checks. IF the requested name differs from their existing record, IT SHALL return HTTP 403 Forbidden with JSON body: {"error": "single_tenant_limit", "message": "Each user account may only be associated with one tenant.", "existing_tenant_id": "{tenant_id}"}
2. IF the tenant name does NOT exist, THE mcp-server SHALL insert a tenant record into PostgreSQL with name, plan, and region, and return HTTP 201 with tenant_id and status: AWAITING_CREDENTIALS
3. UPON successful DB insertion, THE mcp-server SHALL call the Kratos Admin API to update the user's identity traits with the new tenant_id, AND call Keto to create the relationship tuple tenant:{tenant_id}#admin@user:{sub}. IF invoked by a Platform Admin on behalf of a user, IT SHALL use the provided target_user_id parameter instead of the caller's sub
4. DURING tenant creation, THE mcp-server SHALL create an Infisical project for the tenant with path: /tenants/{tenant_id}/ for storing tenant-specific secrets
5. IF the Kratos trait update or Keto tuple creation fails after the database insert, THE mcp-server SHALL persist the tenant record with status INCOMPLETE_IDENTITY_SETUP and return HTTP 500. Retrying tenant_create SHALL idempotently re-attempt the missing identity steps. UPON successful completion of the identity steps during a retry, THE mcp-server SHALL automatically proceed to execute the Git provisioning steps (AC10-AC12) and return HTTP 201 with {"force_token_refresh": true, "tenant_id": "{tenant_id}"}
6. THE mcp-server SHALL return HTTP 201 with a JSON response body containing {"force_token_refresh": true, "tenant_id": "{tenant_id}"}. WHEN the Cursor client receives force_token_refresh: true, IT SHALL immediately execute the Token Refresh flow (Requirement 14) so the new tenant_id is populated in the JWT for subsequent calls
7. IF the tenant name exists AND credentials are missing, THE mcp-server SHALL return HTTP 200 with JSON body {"force_token_refresh": false, "tenant_id": "{tenant_id}", "phase": "AWAITING_CREDENTIALS"}
8. IF the tenant name exists AND credentials are present AND environment is not provisioned, THE mcp-server SHALL return HTTP 200 with JSON body {"force_token_refresh": false, "tenant_id": "{tenant_id}", "phase": "CREDENTIALS_READY"}
9. IF the tenant name exists AND environment is provisioned, THE mcp-server SHALL return HTTP 200 with JSON body {"force_token_refresh": false, "tenant_id": "{tenant_id}", "phase": "READY"}
10. WHEN mcp-server processes tenant creation, IT SHALL invoke the Git Provider API (GitHub or GitLab) to create a new repository named {tenant_id}-control-plane within the Zero-Ops organization
11. THE mcp-server SHALL initialize the repository with a default Kustomize structure:
   - base/kustomization.yaml (base manifests)
   - overlays/starter/ (destination for Starter tier CRs)
   - overlays/enterprise/ (destination for Enterprise tier CRs)
   - README.md (tenant onboarding documentation)
12. THE mcp-server SHALL commit a tenant descriptor to the global fleet-registry repository, which SHALL trigger the management cluster's ArgoCD ApplicationSet to begin watching the new tenant repository
13. IF the Git repository creation or the fleet-registry commit fails, THE mcp-server SHALL NOT rollback the PostgreSQL record. Instead, it SHALL persist the tenant record with status: INCOMPLETE_GIT_SETUP, emit a critical OpenSearch event for platform alerting, and return HTTP 500 with a JSON body containing {"error_code": "git_service_unavailable", "message": "Platform repository service is currently unavailable. Our engineering team has been notified. Please try again later."}
14. WHEN tenant_create is invoked for a tenant name that already exists in INCOMPLETE_GIT_SETUP state, THE mcp-server SHALL idempotently retry the missing Git provisioning steps (verify repo exists, create if missing, push scaffold, commit to fleet-registry). Upon success, it SHALL update status to AWAITING_CREDENTIALS and return HTTP 200
15. THE Cursor SHALL read the status field and proceed to the appropriate next step (credential submission, environment creation, or completion)
16. ALL tenant_create calls with the same tenant name SHALL be safe to retry infinitely
17. THE PostgreSQL tenants table SHALL enforce a UNIQUE constraint on the tenant_name column
18. IF a concurrent tenant_create causes a unique constraint violation, THE mcp-server SHALL catch the error and return HTTP 200 with the existing tenant state (not HTTP 500)

### Requirement 5: Collect Cloud Provider Credentials (Idempotent, Async)

**User Story:** As a Tenant_Admin, I want to securely provide cloud credentials, so that the system can provision infrastructure on my behalf.

**SECURITY PRINCIPLE:** Credentials MUST NEVER transit through the AI agent or IDE client. This is a critical security requirement validated across all MCP OAuth implementations.

**CRITICAL:** Credentials are stored in Infisical (self-hosted secrets management platform). This operation follows the Async Agent Pattern - the Agent displays the console URL and exits. The user resumes the flow conversationally after submitting credentials.

#### Acceptance Criteria

1. WHEN tenant_create returns status: AWAITING_CREDENTIALS, THE mcp-server SHALL return a Platform Console URL: https://console.nutgraf.in/tenants/{tenant_id}/credentials
2. THE Cursor SHALL display the console URL to the Tenant_Admin with instructions: "Please submit your Hetzner API credentials in the Platform Console: {console_url}. Once submitted, return here and ask about your environment status."
3. THE Cursor SHALL exit immediately after displaying the console URL (no polling, no blocking)
4. THE Tenant_Admin SHALL authenticate to the Platform Console using their existing Kratos session (same identity as MCP OAuth)
5. THE Platform Console SHALL verify the Tenant_Admin has permission to submit credentials for {tenant_id} by checking X-Tenant-ID claim from JWT
6. THE Tenant_Admin SHALL enter the Hetzner API token in the authenticated console form (NOT in the agent)
7. THE Platform Console SHALL submit credentials via HTTPS POST to mcp-server backend with JWT authentication
8. THE mcp-server backend SHALL store the credentials in Infisical under path: /tenants/{tenant_id}/credentials/hetzner with AES-256-GCM encryption
9. THE mcp-server SHALL tag the Infisical secret with metadata: tenant_id, created_at, created_by
10. THE mcp-server SHALL update the tenant status in PostgreSQL to CREDENTIALS_READY
11. THE tenant SHALL remain in AWAITING_CREDENTIALS state indefinitely until credentials are submitted (no automatic cleanup)
12. IF credential submission fails (Infisical API error), THE mcp-server SHALL return HTTP 500, and the Tenant_Admin MAY retry via the console form
13. WHEN the Tenant_Admin returns to Cursor and asks about environment status, THE Cursor SHALL invoke environment_status MCP tool (Requirement 17 handles resumption)
14. THE credential submission operation SHALL be idempotent - resubmitting updates the secret in Infisical. IF concurrent submissions occur, the mcp-server SHALL process them sequentially, applying a last-write-wins resolution where the final Infisical write contains the most recently submitted credentials
15. IF the Platform Console receives HTTP 401 Unauthorized during credential submission (JWT expiry), IT SHALL prompt the Tenant_Admin to re-authenticate WITHOUT clearing the entered Hetzner API token from the UI form, and automatically retry the submission upon successful re-authentication
16. ALL credential submission endpoints SHALL require HTTPS (TLS 1.2+)
17. THE Infisical audit log SHALL record all credential write operations with timestamp, user identity, and tenant_id

### Requirement 6: Initiate Environment Provisioning (Idempotent, Async)

**User Story:** As a Tenant_Admin, I want infrastructure provisioned automatically, so that I don't need to manually configure cloud resources.

**CRITICAL:** This operation is idempotent and asynchronous. The Agent submits the intent, receives acknowledgment, and exits. The Agent does NOT block or poll for 15 minutes.

#### Acceptance Criteria

1. WHEN environment_create is invoked, THE zero_ops_api SHALL verify the requested `tier` does not exceed the tenant's current billing `plan` entitlement
2. IF the requested `tier` exceeds the `plan` entitlement, THE zero_ops_api SHALL return HTTP 403 Forbidden with a JSON error body: `{"error": "entitlement_mismatch", "message": "Your current plan does not support this tier. Please upgrade your plan in the Platform Console."}`
3. IF HTTP 403 Forbidden is returned for entitlement mismatch, THE Cursor SHALL display: "Provisioning blocked: Your current plan (Starter) does not allow provisioning an Enterprise environment. Please upgrade your plan in the Platform Console: https://console.nutgraf.in/settings/billing"
4. THE environment_create MCP tool SHALL require an environment_suffix parameter (e.g., 'staging', 'production'). THE zero_ops_api SHALL construct a globally unique environment_id as {tenant_id}-{environment_suffix}
5. IF the generated environment_id already exists, THE zero_ops_api SHALL compare the requested tier, cloud, and region against the existing environment. IF ANY single parameter differs, THE zero_ops_api SHALL return HTTP 409 Conflict with a JSON body detailing the existing parameters to prevent silent overrides
6. THE zero_ops_api SHALL commit the AINativeSaaS_CR directly to the {tenant_id}-control-plane Git repository under overlays/{tier}/ directory, acting as the absolute source of truth for infrastructure.
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

**CRITICAL:** Crossplane reconciles continuously. There is no "timeout" or "max retries" - only eventual consistency. Crossplane uses CAPI providers underneath for actual cluster provisioning.

#### Acceptance Criteria

1. WHEN the AINativeSaaS_CR is committed, THE Crossplane SHALL detect the new resource
2. THE Crossplane SHALL select Composition_B based on the enterprise tier
3. THE Crossplane Composition SHALL create CAPI Cluster resources using the appropriate ClusterClass template (hetzner-spoke-prod-v1 or hetzner-spoke-staging-v1)
4. THE Crossplane Composition SHALL create an ExternalSecret resource that fetches the Hetzner API token from Infisical path: /tenants/{tenant_id}/credentials/hetzner
5. THE External Secrets Operator SHALL sync the credentials from Infisical to a Kubernetes Secret in the spoke cluster namespace
6. THE CAPI provider SHALL use the synced credentials to provision Hetzner infrastructure (VMs, networks, load balancers)
7. THE Composition_B SHALL typically complete within 15 minutes under normal conditions, including tenant cluster provisioning and ArgoCD bootstrap
8. WHEN provisioning completes successfully, THE Crossplane SHALL update the AINativeSaaS_CR status to Ready: True
9. THE Crossplane SHALL NEVER enter a terminal "failed" state - only Degraded or Unready states that allow continued reconciliation
10. THE Crossplane Composition SHALL install Grafana Alloy, Teleport agent, and External Secrets Operator on the provisioned spoke cluster via ClusterResourceSet

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
3. THE AgentGateway SHALL validate the JWT and query auth-proxy Keto to authorize read access for the specific tenant_id
4. THE zero_ops_api SHALL NOT query the Kubernetes API directly.
5. THE zero_ops_api SHALL fetch the environment status exclusively by querying the provisioning_status and provisioning_message columns from the Hub Centralised DB (populated asynchronously by the Spoke Controller running on the spoke cluster, writing via Hub-side PostgREST with per-spoke JWT auth and RLS enforcement).
6. THE zero_ops_api SHALL return a JSON response containing the fields defined in Requirement 16 AC4
7. THE Cursor SHALL display the phase and summary_message to the Tenant_Admin
8. IF phase is Ready, THE Cursor SHALL display provisioned resource endpoints (cluster endpoint, database connection reference, ArgoCD URL, Grafana URL)
9. IF phase is Degraded, THE Cursor SHALL display the error reason and suggest remediation (e.g., "Increase Hetzner quota or try a different region")
10. THE zero_ops_api SHALL expose an environments_list MCP tool mapped to GET /api/v1/tenants/{tenant_id}/environments. It SHALL route through AgentGateway enforcing JWT and Keto authorization identical to other endpoints
11. THE environments_list endpoint SHALL return an array of objects matching the environment status schema (defined in Req 16). IF the tenant has no environments, it SHALL return HTTP 200 with an empty array []
12. WHEN the Tenant_Admin asks generally about their environments without specifying an ID, THE Cursor SHALL invoke environments_list to fetch all environments and prompt the user to disambiguate which environment they are referring to

### Requirement 10: Cache JWKS for Performance

**User Story:** As a system operator, I want JWT validation to be fast, so that API requests have low latency.

**CRITICAL:** AgentGateway fetches JWKS from auth-proxy (which proxies Hydra), NOT directly from Hydra.

#### Acceptance Criteria

1. WHEN the AgentGateway starts, THE AgentGateway SHALL call auth-proxy to fetch the JWKS
2. THE auth-proxy SHALL fetch JWKS from Hydra and return it to AgentGateway
3. THE AgentGateway SHALL cache the JWKS in memory with a 1-hour TTL
4. WHEN validating a JWT, THE AgentGateway SHALL use the cached JWKS
5. IF the JWT signature fails validation, THEN THE AgentGateway SHALL call auth-proxy to refresh the JWKS cache once
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

**CRITICAL:** OAuth metadata is exposed by AgentGateway (proxying auth-proxy), NOT by individual MCP servers.

#### Acceptance Criteria

1. THE AgentGateway SHALL expose `GET /.well-known/oauth-protected-resource` returning:
   - `resource`: https://api.nutgraf.in
   - `authorization_servers`: ["https://auth.nutgraf.in"]
   - `bearer_methods_supported`: ["header"]
   - `scopes_supported`: ["tenant:read", "tenant:write", "cluster:read", "cluster:write"]

2. THE auth-proxy SHALL expose `GET /.well-known/oauth-authorization-server` (proxying Hydra) returning:
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
   - Step 3: Fetch authorization server metadata from auth-proxy to get endpoints
   - Step 4: Initiate PKCE flow using discovered endpoints

### Requirement 13: Client Registration (Pre-registered + CIMD)

**User Story:** As an MCP client, I want to authenticate without manual configuration, so that I can access the platform with zero setup.

**CRITICAL:** Per MCP Specification 2025-11-25, pre-registered credentials and Client ID Metadata Documents (CIMD) are the recommended approaches. Dynamic Client Registration (DCR) is NOT implemented due to security risks (unbounded database growth, open registration attacks).

#### Acceptance Criteria

**Pre-registered Client (Primary - Zero-Config):**

1. THE auth-proxy SHALL pre-register a public client with Hydra:
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

3. THE auth-proxy SHALL normalize redirect URIs during authorization:
   - IF client sends "localhost", ALSO accept "127.0.0.1" variant
   - IF client sends "127.0.0.1", ALSO accept "localhost" variant

**CIMD (Secondary - Future Clients):**

4. THE auth-proxy SHALL advertise CIMD support in authorization server metadata:
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

7. WHEN the MCP client sends authorization request with HTTPS URL as client_id, THE auth-proxy SHALL fetch the metadata document

8. THE auth-proxy SHALL validate:
   - client_id in document matches the URL exactly
   - Document is valid JSON with required fields
   - redirect_uris are valid per MCP spec

9. THE auth-proxy SHALL cache metadata respecting HTTP cache headers

10. THE auth-proxy SHALL implement SSRF protections:
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

**CRITICAL:** Token refresh is handled by auth-proxy (proxying Hydra). The 24-hour access token TTL covers the full 15-minute provisioning window (Requirement 7). However, for long-running operations or multi-day workflows, automatic token refresh is required.

#### Acceptance Criteria

1. WHEN the AgentGateway returns 401 with error "invalid_token" and error_description "Token expired", THE Cursor SHALL attempt token refresh transparently
2. THE Cursor SHALL send `POST {token_endpoint}` to auth-proxy with:
   - `grant_type=refresh_token`
   - `refresh_token={refresh_token}`
   - `client_id={client_id}`
   - `scope=tenant:read tenant:write cluster:read cluster:write offline_access`

3. THE auth-proxy SHALL forward the request to Hydra
4. THE Hydra SHALL validate the refresh token (30-day TTL)
5. THE Hydra SHALL issue a new access token with 24-hour TTL. THE auth-proxy SHALL configure Hydra to re-hydrate custom claims (tenant_id, email, role) from the latest Kratos identity traits during the refresh grant, ensuring newly assigned tenant_ids are successfully populated into the new token
6. THE Hydra SHALL rotate the refresh token (issue new refresh token, invalidate old one)
7. THE auth-proxy SHALL return the new tokens to the Cursor
8. THE Cursor SHALL update stored tokens in OS keychain
9. THE Cursor SHALL retry the original failed request with the new access token
10. IF refresh fails (expired/revoked), THE Cursor SHALL restart the authorization flow and notify the user
11. THE Cursor SHALL NOT show errors to the user during transparent token refresh (only on refresh failure)

**Scenario: Token Expiry Between tenant_create and environment_create**
- GIVEN the Tenant_Admin completes tenant_create at T=0
- AND the access token expires at T=24h
- WHEN the Tenant_Admin invokes environment_create at T=25h
- THEN the AgentGateway returns 401 "Token expired"
- AND the Cursor transparently refreshes the token via auth-proxy
- AND the Cursor retries environment_create with the new access token
- AND the operation succeeds without user intervention

### Requirement 15: Platform Git Authentication

**User Story:** As a system operator, I want the platform to securely authenticate with Git, so that GitOps reconciliation is secure and auditable.

**CRITICAL:** GitHub App Installation Tokens provide short-lived, scoped authentication for Git operations. Tokens are generated on-demand and cached for performance.

#### Acceptance Criteria

1. **AC 15.1 (Bootstrap):** THE management cluster SHALL contain a GitHub App Private Key Secret, stored in Infisical under path: /platform/github-app/private-key
2. **AC 15.2 (Secret Sync):** THE External Secrets Operator SHALL sync the GitHub App Private Key from Infisical to a Kubernetes Secret in the zero-ops-api namespace
3. **AC 15.3 (Token Generation):** WHEN `zero_ops_api` needs to commit to a tenant repository, IT SHALL dynamically generate a short-lived GitHub App Installation Token using the synced GitHub App Private Key
4. **AC 15.4 (Token Cache):** THE `zero_ops_api` SHALL cache the generated Installation Token in memory for up to 55 minutes (proactively expiring before the strict 1-hour GitHub TTL)
5. **AC 15.5 (Idempotent Refresh):** IF a Git commit operation returns HTTP 401 Unauthorized (indicating premature token expiry or revocation), THE `zero_ops_api` SHALL immediately invalidate the cached token, generate a fresh Installation Token, and retry the commit operation EXACTLY ONCE
6. **AC 15.6 (Terminal Failure):** IF the retry using a freshly generated token also returns HTTP 401, THE `zero_ops_api` SHALL abort the operation, return HTTP 500 to the Agent, and log a critical authorization error
7. **AC 15.7 (Disaster Recovery):** THE GitHub App Private Key SHALL be backed up securely in the Zero-Ops organization's enterprise password vault (e.g., 1Password, Bitwarden)
8. **AC 15.8 (Cluster Recreation):** IF the management cluster is destroyed, THE Platform Admin SHALL run `zero-ops mgmt bootstrap --name=shard-eu-1` to provision a new cluster, install Infisical, sync the GitHub App Private Key from the password vault to Infisical, and the entire platform SHALL auto-reconcile back into existence

**Rationale for Single Idempotent Retry:**
- GitHub App Installation Tokens have deterministic 1-hour expiry (not transient network errors)
- Proactive 55-minute cache prevents expiry under normal conditions
- Single retry handles edge cases: premature expiry, token revocation, clock skew
- Exponential backoff is inappropriate for deterministic authorization failures
- Fast failure (2 attempts max) provides clear signal for critical auth issues

### Requirement 16: Environment Status Schema and Phase Mapping

**User Story:** As a developer, I want a consistent status schema across all environments, so that I can build reliable integrations.

#### Acceptance Criteria

1. THE zero_ops_api SHALL expose the environment_status MCP tool, mapped to GET /api/v1/environments/{environment_id}/status. TO support pre-environment routing, THE zero_ops_api SHALL ALSO expose a tenant-level status endpoint mapped to GET /api/v1/tenants/{tenant_id}/status. This tenant endpoint SHALL handle all pre-environment phases (INCOMPLETE_IDENTITY_SETUP, INCOMPLETE_GIT_SETUP, AWAITING_CREDENTIALS, CREDENTIALS_READY)
2. THE endpoint SHALL require JWT authentication via AgentGateway
3. THE AgentGateway SHALL validate JWT and query auth-proxy Keto to authorize read access for the specific tenant_id
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
5. THE Spoke Controller (controller-runtime, runs on the spoke) SHALL derive phase from Crossplane Conditions and write to Hub Centralised DB via Hub-side PostgREST using these rules: Claim created = provisioning; Synced=False = failed; Ready=True = ready; DeletionTimestamp set = deleting. THE zero_ops_api SHALL read phase exclusively from PostgreSQL:
   - CREDENTIALS_READY: PostgreSQL tenant record exists with credentials submitted, but no environment provisioning intent recorded
   - Pending: The PostgreSQL DB confirms the environment intent exists (environment record inserted), but the Spoke Controller has not yet written any status to Hub Centralised DB (ArgoCD has not yet synced the CR to the cluster, so Crossplane has not started reconciling). Detected by: environment record present in PostgreSQL with no corresponding status row in Hub Centralised DB.
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
8. THE zero_ops_api SHALL cache the Hub Centralised DB response for 5 seconds to reduce database load during console polling (the 10-second console poll interval means a 5-second cache still provides fresh data while halving DB query rate)

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

### Requirement 20: Hub Observability Stack Installation

**User Story:** As a Platform_Admin, I want observability infrastructure installed on the hub cluster, so that I can monitor tenant environments and platform health centrally.

**CRITICAL:** Observability is a foundational requirement for Phase 1 MVP. VictoriaMetrics, Grafana, and Grafana Alloy form the telemetry backbone for the Hub-Spoke architecture.

#### Acceptance Criteria

1. THE hub bootstrap process SHALL install VictoriaMetrics using the victoria-metrics-k8s-stack Helm chart in the observability namespace
2. THE VictoriaMetrics installation SHALL include:
   - VictoriaMetrics single-node or cluster deployment (based on scale requirements)
   - VMAgent for metrics collection
   - VMAlert for alerting rules
   - Retention period of 30 days minimum
3. THE hub bootstrap process SHALL install Grafana in the observability namespace
4. THE Grafana installation SHALL be pre-configured with VictoriaMetrics as the default datasource
5. THE Grafana installation SHALL include pre-built dashboards for:
   - Platform health overview (tenant count, cluster count, sync status)
   - Tenant resource utilization (CPU, memory, storage per tenant)
   - ArgoCD sync status (applications in sync/out-of-sync/degraded)
   - Spoke cluster health (node status, pod health)
6. THE hub bootstrap process SHALL create ServiceMonitor resources for:
   - ArgoCD metrics (application sync status, reconciliation duration)
   - Crossplane metrics (resource provisioning status, reconciliation errors)
   - CAPI metrics (cluster provisioning status, machine health)
   - CloudNativePG metrics (database health, replication lag)
7. THE VictoriaMetrics SHALL expose a remote_write endpoint for spoke clusters to push metrics
8. THE VictoriaMetrics remote_write endpoint SHALL require mTLS authentication from spoke clusters
9. THE hub bootstrap process SHALL generate and store a CA certificate for spoke cluster authentication
10. THE observability stack SHALL be managed via ArgoCD Application manifests in the fleet-registry repository

### Requirement 21: Spoke Cluster Metrics Collection

**User Story:** As a Platform_Admin, I want spoke clusters to push metrics to the hub, so that I can monitor tenant resource usage and cluster health centrally.

**CRITICAL:** Grafana Alloy is the lightweight agent deployed on spoke clusters. It pushes metrics asynchronously to the hub with buffering for resilience.

#### Acceptance Criteria

1. WHEN a spoke cluster is provisioned, THE Crossplane Composition SHALL include a Grafana Alloy DaemonSet deployment
2. THE Grafana Alloy DaemonSet SHALL run on all worker nodes in the spoke cluster
3. THE Grafana Alloy configuration SHALL collect metrics from:
   - Kubernetes API server (cluster-level metrics)
   - kubelet (node and pod metrics)
   - cAdvisor (container resource usage)
   - kube-state-metrics (Kubernetes object state)
4. THE Grafana Alloy SHALL push metrics to the hub VictoriaMetrics remote_write endpoint every 30 seconds
5. THE Grafana Alloy SHALL use mTLS certificates for authentication to the hub
6. THE Grafana Alloy SHALL buffer metrics locally for up to 2 hours if the hub is unreachable
7. WHEN the hub becomes reachable, THE Grafana Alloy SHALL flush buffered metrics with idempotent batch ingestion
8. THE Grafana Alloy SHALL add labels to all metrics:
   - tenant_id: {tenant_id}
   - cluster_id: {spoke_cluster_name}
   - environment_id: {environment_id}
   - tier: {starter|enterprise}
9. THE Grafana Alloy SHALL NOT collect application-level logs in Phase 1 (defer to Phase 2)
10. THE Grafana Alloy configuration SHALL be stored in the tenant's control-plane Git repository and synced via ArgoCD

### Requirement 22: Tenant Count Metrics

**User Story:** As a Platform_Admin, I want to track the number of active tenants, so that I can monitor platform growth and capacity planning.

#### Acceptance Criteria

1. THE zero_ops_api SHALL expose Prometheus metrics at /metrics endpoint
2. THE zero_ops_api SHALL expose a gauge metric: zero_ops_tenants_total with labels:
   - plan: {free|shared|dedicated}
   - status: {AWAITING_CREDENTIALS|CREDENTIALS_READY|PROVISIONING|READY|DEGRADED}
3. THE zero_ops_api SHALL update tenant count metrics every 60 seconds by querying PostgreSQL
4. THE VictoriaMetrics VMAgent SHALL scrape the zero_ops_api /metrics endpoint every 30 seconds
5. THE Grafana platform health dashboard SHALL display:
   - Total tenant count (all statuses)
   - Tenant count by plan (pie chart)
   - Tenant count by status (bar chart)
   - Tenant growth over time (line chart)
6. THE dashboard SHALL refresh automatically every 30 seconds

### Requirement 23: Cluster Health Metrics

**User Story:** As a Platform_Admin, I want to monitor spoke cluster health, so that I can detect infrastructure failures and capacity issues.

#### Acceptance Criteria

1. THE Grafana Alloy on spoke clusters SHALL collect node health metrics:
   - node_status (Ready|NotReady|Unknown)
   - node_cpu_utilization_percent
   - node_memory_utilization_percent
   - node_disk_utilization_percent
2. THE Grafana Alloy SHALL collect control plane health metrics:
   - kube_apiserver_up (1=healthy, 0=down)
   - etcd_server_has_leader (1=has leader, 0=no leader)
   - kube_controller_manager_up
   - kube_scheduler_up
3. THE VictoriaMetrics SHALL aggregate cluster health metrics by tenant_id and cluster_id
4. THE Grafana cluster health dashboard SHALL display:
   - Cluster status (Ready|Degraded|Failed) per tenant
   - Node count and health status per cluster
   - Control plane component health per cluster
   - Cluster age (time since provisioning)
5. THE VictoriaMetrics SHALL define alerting rules:
   - ClusterNodeNotReady: Alert if any node is NotReady for >5 minutes
   - ClusterControlPlaneDown: Alert if any control plane component is down for >2 minutes
   - ClusterHighCPU: Alert if cluster-wide CPU utilization >80% for >10 minutes
   - ClusterHighMemory: Alert if cluster-wide memory utilization >80% for >10 minutes
6. THE VMAlert SHALL send alerts to the Platform Console notification system

### Requirement 24: ArgoCD Sync Status Metrics

**User Story:** As a Platform_Admin, I want to track ArgoCD sync status for all tenant applications, so that I can detect deployment failures and drift.

#### Acceptance Criteria

1. THE VictoriaMetrics VMAgent SHALL scrape ArgoCD metrics from the argocd-metrics service
2. THE ArgoCD metrics SHALL include:
   - argocd_app_info (application metadata with labels: name, namespace, project, sync_status, health_status)
   - argocd_app_sync_total (counter of sync operations)
   - argocd_app_reconcile_duration_seconds (histogram of reconciliation duration)
3. THE VictoriaMetrics SHALL aggregate sync status metrics by tenant_id (derived from application name pattern)
4. THE Grafana ArgoCD dashboard SHALL display:
   - Total applications count
   - Applications by sync status (Synced|OutOfSync|Unknown)
   - Applications by health status (Healthy|Progressing|Degraded|Suspended|Missing)
   - Sync failures over time (line chart)
   - Average reconciliation duration per tenant
5. THE VictoriaMetrics SHALL define alerting rules:
   - ArgoCDAppOutOfSync: Alert if application is OutOfSync for >10 minutes
   - ArgoCDAppDegraded: Alert if application health is Degraded for >5 minutes
   - ArgoCDSyncFailure: Alert on repeated sync failures (>3 in 15 minutes)
6. THE Grafana dashboard SHALL allow filtering by tenant_id and environment_id

### Requirement 25: Resource Utilization Metrics

**User Story:** As a Platform_Admin, I want to track resource utilization across all spoke clusters, so that I can optimize capacity and identify cost optimization opportunities.

#### Acceptance Criteria

1. THE Grafana Alloy SHALL collect resource utilization metrics:
   - container_cpu_usage_seconds_total (per container)
   - container_memory_working_set_bytes (per container)
   - container_fs_usage_bytes (per container)
   - kube_pod_container_resource_requests (CPU and memory requests)
   - kube_pod_container_resource_limits (CPU and memory limits)
2. THE VictoriaMetrics SHALL aggregate resource utilization by:
   - tenant_id
   - environment_id
   - namespace
   - workload (deployment, statefulset, daemonset)
3. THE Grafana resource utilization dashboard SHALL display:
   - CPU utilization per tenant (cores used vs requested)
   - Memory utilization per tenant (GB used vs requested)
   - Storage utilization per tenant (GB used)
   - Resource efficiency (used vs requested ratio)
   - Top 10 resource consumers (by tenant, by workload)
4. THE VictoriaMetrics SHALL calculate derived metrics:
   - tenant_cpu_utilization_percent = (cpu_used / cpu_requested) * 100
   - tenant_memory_utilization_percent = (memory_used / memory_requested) * 100
   - tenant_storage_utilization_percent = (storage_used / storage_capacity) * 100
5. THE VictoriaMetrics SHALL define alerting rules:
   - TenantHighCPU: Alert if tenant CPU utilization >80% for >15 minutes
   - TenantHighMemory: Alert if tenant memory utilization >80% for >15 minutes
   - TenantHighStorage: Alert if tenant storage utilization >85%
6. THE Grafana dashboard SHALL support time range selection (1h, 6h, 24h, 7d, 30d)
7. THE Grafana dashboard SHALL allow exporting resource utilization data as CSV for billing integration

### Requirement 26: Observability Data Retention and Backup

**User Story:** As a Platform_Admin, I want observability data retained for compliance and troubleshooting, so that I can investigate historical issues.

#### Acceptance Criteria

1. THE VictoriaMetrics SHALL retain metrics data for 30 days minimum
2. THE VictoriaMetrics SHALL use persistent volumes for data storage with daily snapshots
3. THE VictoriaMetrics snapshots SHALL be backed up to S3-compatible object storage daily
4. THE VictoriaMetrics SHALL implement data retention policies:
   - High-resolution metrics (15s interval): 7 days
   - Medium-resolution metrics (1m interval): 30 days
   - Low-resolution metrics (5m interval): 90 days (optional, for long-term trends)
5. THE Grafana dashboard configurations SHALL be stored in Git (fleet-registry repository)
6. THE Grafana dashboard changes SHALL be version-controlled and synced via ArgoCD
7. THE VictoriaMetrics SHALL expose backup/restore procedures in runbooks for disaster recovery
8. THE observability namespace SHALL have resource quotas to prevent unbounded growth:
   - CPU limit: 8 cores
   - Memory limit: 32 GB
   - Storage limit: 500 GB (adjustable based on tenant count)

### Requirement 27: Secrets Management with Infisical

**User Story:** As a Platform_Admin, I want cloud provider credentials stored securely in Infisical, so that secrets are encrypted at rest and access is audited.

**CRITICAL:** This requirement replaces KSOPS/Age pattern with self-hosted Infisical for centralized secrets management and Teleport for PAM.

#### Acceptance Criteria

1. THE hub bootstrap process SHALL install Infisical server in the secrets-management namespace
2. THE Infisical installation SHALL use CloudNativePG for its PostgreSQL backend
3. THE Infisical installation SHALL be configured with:
   - Encryption at rest using AES-256-GCM
   - TLS for all API communications
   - RBAC policies for tenant isolation
4. THE hub bootstrap process SHALL install External Secrets Operator (ESO) in the secrets-management namespace
5. THE ESO SHALL be configured with Infisical as a SecretStore backend
6. WHEN a tenant submits cloud provider credentials via the Platform Console, THE zero_ops_api SHALL:
   - Store the credentials in Infisical under path: /tenants/{tenant_id}/credentials/hetzner
   - Tag the secret with metadata: tenant_id, created_at, created_by
   - NOT commit credentials to Git (Infisical is the source of truth)
7. WHEN Crossplane provisions a spoke cluster, THE Composition SHALL create an ExternalSecret resource that:
   - References the Infisical SecretStore
   - Fetches credentials from /tenants/{tenant_id}/credentials/hetzner
   - Creates a Kubernetes Secret in the spoke cluster namespace
8. THE ExternalSecret SHALL refresh credentials every 5 minutes to detect rotation
9. THE Infisical SHALL maintain an audit log of all secret access operations including:
   - Timestamp
   - User/service account identity
   - Secret path accessed
   - Operation (read/write/delete)
10. THE Infisical audit logs SHALL be exported to OpenSearch for centralized security monitoring
11. THE Infisical SHALL support secret rotation workflows where updating a secret in Infisical automatically propagates to all ExternalSecrets within 5 minutes

### Requirement 28: Privileged Access Management with Teleport

**User Story:** As a Platform_Admin, I want privileged access to clusters managed via Teleport, so that all administrative actions are audited and time-limited.

**CRITICAL:** Teleport provides zero-trust access to Kubernetes clusters, SSH nodes, and databases with session recording and just-in-time access.

#### Acceptance Criteria

1. THE hub bootstrap process SHALL install Teleport cluster in the teleport namespace
2. THE Teleport installation SHALL be configured with:
   - PostgreSQL backend (CloudNativePG)
   - TLS certificates for all components
   - RBAC roles: platform_admin, tenant_admin, read_only
3. THE Teleport SHALL integrate with Ory Kratos for SSO authentication
4. THE Teleport SHALL register the hub cluster as a trusted cluster
5. WHEN a spoke cluster is provisioned, THE Crossplane Composition SHALL:
   - Install Teleport agent on the spoke cluster
   - Register the spoke cluster with the hub Teleport cluster
   - Configure RBAC to allow tenant_admin role access only to their tenant's clusters
6. THE Teleport SHALL enforce access policies:
   - platform_admin: Full access to all clusters
   - tenant_admin: Access only to clusters with matching tenant_id label
   - read_only: Read-only kubectl access
7. THE Teleport SHALL record all kubectl exec sessions and store recordings for 90 days
8. THE Teleport SHALL enforce just-in-time access:
   - Access requests require approval from platform_admin
   - Access grants expire after 8 hours
   - Emergency access (break-glass) requires multi-party approval
9. THE Teleport SHALL integrate with Slack/PagerDuty for access request notifications
10. THE Teleport audit logs SHALL be exported to OpenSearch for compliance reporting
11. THE Platform Console SHALL display Teleport access links for tenant admins to connect to their clusters

### Requirement 29: Spoke ClusterClass Templates

**User Story:** As a Platform_Admin, I want spoke-specific ClusterClass templates, so that tenant clusters are provisioned with appropriate sizing and configuration.

**CRITICAL:** Spoke clusters have different requirements than the management cluster (smaller, tenant-focused, cost-optimized).

#### Acceptance Criteria

1. THE hub bootstrap process SHALL deploy ClusterClass templates to the zero-ops-system namespace:
   - hetzner-spoke-prod-v1 (production workloads)
   - hetzner-spoke-staging-v1 (staging/dev workloads)
2. THE hetzner-spoke-prod-v1 ClusterClass SHALL define:
   - Control plane: 3 nodes, cx23 instance type (2 vCPU, 4 GB RAM)
   - Worker pool: Auto-scaling 2-10 nodes, cx33 instance type (4 vCPU, 8 GB RAM)
   - Kubernetes version: v1.31.6
   - CNI: Cilium 1.15.6
   - CCM: Hetzner Cloud Controller Manager
   - CSI: Hetzner CSI Driver
3. THE hetzner-spoke-staging-v1 ClusterClass SHALL define:
   - Control plane: 3 nodes, cx23 instance type (2 vCPU, 4 GB RAM)
   - Worker pool: Auto-scaling 1-5 nodes, cx23 instance type (2 vCPU, 4 GB RAM)
   - Kubernetes version: v1.31.6
   - CNI: Cilium 1.15.6
   - CCM: Hetzner Cloud Controller Manager
   - CSI: Hetzner CSI Driver
4. THE ClusterClass templates SHALL distribute nodes across 3 availability zones (Hetzner regions: fsn1, nbg1, hel1)
5. THE ClusterClass templates SHALL include ClusterResourceSet (CRS) for automatic addon installation:
   - Cilium CNI (via Helm)
   - Hetzner CCM (via Helm)
   - Hetzner CSI (via manifest)
   - Grafana Alloy (via Helm)
   - Teleport agent (via Helm)
6. THE ClusterClass templates SHALL be version-controlled in the fleet-registry Git repository
7. THE ClusterClass templates SHALL be deployed via ArgoCD Application manifests
8. THE Crossplane AINativeSaaS Composition SHALL reference these ClusterClass templates when provisioning spoke clusters
9. THE ClusterClass templates SHALL support in-place upgrades for Kubernetes version updates
10. THE ClusterClass templates SHALL include node taints and labels for workload isolation:
    - Taint: tenant={tenant_id}:NoSchedule (prevent cross-tenant pod scheduling)
    - Label: tenant_id={tenant_id}, tier={starter|enterprise}
