# Zero-Ops Platform — E2E BDD Test Case Specification

**Version:** 1.0  
**PRD Reference:** Zero-Ops PRD v8.0 — SaaS Factory: AINativeSaaS Template, Platform Console & Tenant Onboarding  
**Status:** DRAFT — TDD Baseline  
**Classification:** CONFIDENTIAL — INTERNAL USE ONLY

---

## Document Purpose

This specification defines the complete behavior-driven, end-to-end test suite for Zero-Ops v8.0. Every test case maps directly to a user journey, scenario, or acceptance criterion in PRD v8.0. The test suite is sequenced to mirror the user journey order defined in the PRD: platform bootstrap → identity → tenant onboarding → provisioning → day-2 operations → observability → agent system → failure modes → security boundaries.

The product team uses this document as the TDD baseline: all test cases must have passing implementations before a feature is considered shippable.

---

## Test Case Conventions

```
Feature: <capability being tested>
  Background: <shared preconditions for the feature>
  Scenario: <specific behavior under test>
    Given <precondition>
    When  <action>
    Then  <observable outcome>
    And   <additional outcome>
```

**Test IDs** follow the pattern `TC-{DOMAIN}-{SEQUENCE}` where domains are:

| Domain | Coverage Area |
|---|---|
| `BOOT` | Platform Admin bootstrap & fleet shard lifecycle |
| `IDN` | Identity, authentication, and authorization (Ory stack + AgentGateway) |
| `ONB` | Tenant onboarding — agentic headless and console flows |
| `PROV` | AINativeSaaS provisioning — Starter and Enterprise compositions |
| `UPG` | Tier upgrade (Starter → Enterprise) |
| `PRE` | PR ephemeral environments |
| `D2` | Day-2 operations (scale, team, runbooks, keys) |
| `OBS` | Fleet observability — VictoriaMetrics, OpenSearch, Alloy, cnpg2monitor |
| `AGT` | Multi-agent system — Collaborator, Workers, MCP tool servers |
| `APL` | Autopilot mode — PR proposals, approval, rejection |
| `MIG` | Provider migration (blue-green, CNPG PITR) |
| `SEC` | Security boundaries — RBAC, tenant isolation, data visibility |
| `DES` | Destructive operations — human approval gate, "Resolve in Cursor" |
| `HA` | High availability — management cluster outage, tenant runtime continuity |
| `CLN` | Cleanup and decommission flows |

---

## BOOT — Platform Bootstrap & Fleet Shard Lifecycle

### TC-BOOT-01: Management cluster bootstrap succeeds end-to-end

```gherkin
Feature: Fleet Shard Bootstrap
  Background:
    Given a Hetzner account with sufficient quota in region fsn1
    And the Zero-Ops CLI is installed and authenticated

  Scenario: Platform Admin bootstraps first management shard
    Given no fleet shards are registered in zero-ops-api
    When the admin runs:
      """
      zero-ops mgmt bootstrap --name=shard-eu-1 --region=fsn1
      """
    Then a Ubuntu Kubernetes management cluster is provisioned on Hetzner within 15 minutes
    And the cluster runs CAPI core components
    And CAPH provider is installed and ready
    And Crossplane is installed and ready
    And ArgoCD is installed in management cluster
    And CNPG operator is installed
    And cnpg2monitor operator is installed with ClusterRole scope
    And Ory Kratos, Hydra, and Keto are deployed and healthy
    And AgentGateway is deployed and serving on its designated port
    And shard-eu-1 is registered in zero-ops-api PostgreSQL with status=active
    And shard-eu-1 begins emitting 30-second heartbeats to zero-ops-api
    And the platform console is accessible at https://console.zero-ops.io
```

### TC-BOOT-02: Shard heartbeat and health registry

```gherkin
Feature: Fleet Shard Health
  Background:
    Given shard-eu-1 is registered and active

  Scenario: Shard heartbeat updates fleet_current_state within tolerance
    Given shard-eu-1 is running normally
    When 30 seconds elapse
    Then the last_heartbeat timestamp for shard-eu-1 in fleet_current_state is updated
    And status remains active

  Scenario: Shard detected as unreachable after missed heartbeats
    Given shard-eu-1 is registered and last_heartbeat is recent
    When shard-eu-1 Kubernetes API becomes unreachable for 90 seconds
    Then zero-ops-api marks shard-eu-1 status as status_unknown
    And new provisioning intents are not routed to shard-eu-1
    And an alert-firing event is written to OpenSearch alert-firings index

  Scenario: Shard recovers and resumes normal routing
    Given shard-eu-1 is in status_unknown state
    When shard-eu-1 Kubernetes API becomes reachable again
    And a heartbeat is received
    Then shard-eu-1 status is updated to active
    And new provisioning intents are routed to shard-eu-1 again
```

### TC-BOOT-03: cnpg2monitor ClusterRole scope at bootstrap

```gherkin
Feature: cnpg2monitor Fleet-Wide Promotion
  Background:
    Given zero-ops mgmt bootstrap has completed

  Scenario: cnpg2monitor watches all namespaces from day one
    When cnpg2monitor is deployed
    Then it has a ClusterRole with get;list;watch on Namespaces
    And it has get;list;watch;patch on pods, podmonitors, clusters.postgresql.cnpg.io across all namespaces
    And its controller-runtime cache has no namespace restriction
    And it is not scoped to zero-ops-system namespace only
```

---

## IDN — Identity, Authentication & Authorization

### TC-IDN-01: Ory Kratos identity creation for new tenant admin

```gherkin
Feature: Platform Identity — Kratos Identity Management
  Background:
    Given Ory Kratos is deployed and healthy
    And Ory Hydra is deployed and healthy

  Scenario: New tenant admin account created with correct traits
    Given no identity exists for alice@acme-corp.com
    When zero-ops-api creates a new Kratos identity for alice@acme-corp.com
    Then the identity exists in Kratos with traits:
      | email     | alice@acme-corp.com |
      | tenant_id | acme-corp           |
      | role      | tenant_admin        |
    And the identity is associated with tenant acme-corp in Ory Keto
    And the Keto relationship tuple tenant:acme-corp#admin@user:alice-uuid is created
```

### TC-IDN-02: Hydra device auth flow for headless MCP client (Goose)

```gherkin
Feature: Device Auth Flow — Headless Agent Authentication
  Background:
    Given Ory Hydra device auth endpoint is available at https://auth.zero-ops.io/device
    And a Goose client session is started with no existing JWT

  Scenario: Goose authenticates via device auth and retries MCP call
    Given Goose sends a tenant_create MCP call to AgentGateway without a JWT
    When AgentGateway returns 401 Unauthorized with WWW-Authenticate header pointing to device auth endpoint
    Then Goose displays a device code and the device auth URL to the user
    And Goose begins polling the Hydra device auth endpoint every 5 seconds
    When the user completes browser login via Ory Kratos within the 5-minute window
    Then Hydra issues a signed JWT to Goose
    And Goose automatically retries the tenant_create MCP call with the JWT in the Authorization header
    And AgentGateway accepts the retry and routes the call

  Scenario: Device auth code expires before user completes login
    Given Goose has displayed a device code
    When 5 minutes elapse without user completing browser login
    Then Hydra expires the device code
    And Goose displays a new device code and restarts the polling loop
    And no error is returned to the user — the flow continues transparently
```

### TC-IDN-03: JWT claims contain required fields

```gherkin
Feature: JWT Token Claims
  Scenario: Hydra-issued JWT contains all required claims
    Given alice with tenant_id=acme-corp and role=tenant_admin authenticates
    When Hydra issues a JWT for alice
    Then the JWT payload contains:
      | claim     | value                                |
      | sub       | alice-uuid                           |
      | tenant_id | acme-corp                            |
      | role      | tenant_admin                         |
      | iss       | https://auth.zero-ops.io             |
    And the JWT is signed with Hydra's private key
    And the JWT can be validated against Hydra's JWKS endpoint
```

### TC-IDN-04: AgentGateway JWKS caching

```gherkin
Feature: AgentGateway JWT Validation Performance
  Scenario: AgentGateway caches JWKS at startup and does not fetch on every request
    Given AgentGateway has started and cached the JWKS from Hydra
    When 100 MCP calls arrive within 1 second each carrying a valid JWT
    Then AgentGateway validates all 100 JWTs using the cached JWKS
    And zero additional JWKS fetch calls are made to Hydra during this burst
    And the total JWT validation overhead per call is under 5ms

  Scenario: AgentGateway refreshes JWKS on validation failure
    Given AgentGateway has a cached JWKS
    When a JWT validation fails due to an unknown key ID (kid)
    Then AgentGateway fetches the JWKS from Hydra exactly once
    And retries validation with the refreshed JWKS
    And does not re-fetch again for the next 100 calls using the new key
```

### TC-IDN-05: AgentGateway is single auth enforcement point

```gherkin
Feature: AgentGateway Authorization Enforcement
  Scenario: MCP call without JWT is rejected at AgentGateway
    Given an MCP call is sent to AgentGateway without an Authorization header
    When AgentGateway processes the request
    Then it returns HTTP 401 Unauthorized
    And the MCP tool server receives no call

  Scenario: MCP call with invalid JWT is rejected at AgentGateway
    Given an MCP call is sent with an expired JWT
    When AgentGateway validates the JWT
    Then it returns HTTP 401 Unauthorized
    And the MCP tool server receives no call

  Scenario: MCP tool server contains no auth logic of its own
    Given fleet-state-mcp is running
    When fleet-state-mcp receives a call that has been routed by AgentGateway
    Then fleet-state-mcp does not validate any JWT
    And fleet-state-mcp uses tenant_id extracted from JWT by AgentGateway for data scoping
```

### TC-IDN-06: Keto RBAC — tenant isolation

```gherkin
Feature: Keto Relationship-Based Authorization
  Background:
    Given alice is tenant_admin of acme-corp
    And bob is tenant_admin of rival-corp
    And carol is platform_admin

  Scenario: Tenant admin can call MCP tools scoped to their own tenant
    Given alice's JWT has tenant_id=acme-corp and role=tenant_admin
    When alice calls crossplane-mcp:get for resource acme-corp-production
    Then AgentGateway calls Keto check: can user:alice-uuid read ainativesaas:acme-corp-production?
    And Keto returns allow
    And the MCP call succeeds

  Scenario: Tenant admin cannot call MCP tools scoped to another tenant
    Given alice's JWT has tenant_id=acme-corp
    When alice calls crossplane-mcp:get for resource rival-corp-production
    Then AgentGateway calls Keto check: can user:alice-uuid read ainativesaas:rival-corp-production?
    And Keto returns deny
    And AgentGateway returns HTTP 403 Forbidden
    And rival-corp-production data is not returned

  Scenario: Platform admin can call MCP tools across all tenants
    Given carol's JWT has role=platform_admin
    When carol calls fleet-state-mcp:list with no tenant filter
    Then Keto check returns allow for all tenant resources
    And the response includes resources from all tenants

  Scenario: Tenant viewer cannot perform write operations
    Given bob is a tenant_viewer (not tenant_admin) of rival-corp
    When bob calls crossplane-mcp:create for a new environment
    Then AgentGateway calls Keto check: can user:bob-uuid create ainativesaas:rival-corp?
    And Keto returns deny
    And AgentGateway returns HTTP 403 Forbidden
```

---

## ONB — Tenant Onboarding

### TC-ONB-01: Agentic headless Enterprise onboarding — happy path

```gherkin
Feature: Agentic Enterprise Tenant Onboarding
  Background:
    Given the Zero-Ops platform is running with shard-eu-1 active
    And no tenant with name acme-corp exists

  Scenario: Goose-driven Enterprise onboarding completes end-to-end
    Given Goose is started with no existing JWT
    When the user types: "Onboard Acme Corp on the enterprise plan in eu-central-1"
    Then Goose maps the intent to tenant_create and calls AgentGateway
    And AgentGateway returns 401 triggering device auth flow
    And after user completes Kratos login, Goose retries with a valid JWT
    And zero-ops-api creates a tenant record in PostgreSQL with tenant_id=acme-corp
    And Goose prompts the user to provide Hetzner API token via the console
    When the user enters their Hetzner API token in the console credentials settings
    Then zero-ops-api encrypts the token with the tenant Age key
    And stores the encrypted token in the tenant control plane repository via KSOPS
    And zero-ops-api commits an AINativeSaaS CR with spec.tier=enterprise to the tenant control plane repo
    And ArgoCD detects the commit within 3 minutes
    And Crossplane Composition B begins executing
    And within 15 minutes the AINativeSaaS CR status condition is Ready=True
    And the console shows acme-corp-production environment status as Ready
    And Goose displays the success summary containing:
      | field                        |
      | cluster API endpoint         |
      | PostgreSQL Secret name       |
      | ArgoCD dashboard URL         |
      | Grafana tenant URL           |
      | API token reference          |
    And the Hetzner API token value is never displayed in Goose output
```

### TC-ONB-02: Agentic onboarding — Hetzner quota exceeded

```gherkin
Feature: Enterprise Onboarding Failure Handling
  Scenario: Provisioning fails due to Hetzner quota and surfaces to tenant
    Given an AINativeSaaS CR has been committed for acme-corp in eu-central-1
    When CAPI resource provisioning fails with a Hetzner quota exceeded error
    Then Crossplane sets a Condition on the AINativeSaaS CR:
      | type   | Ready  |
      | status | False  |
      | reason | HetznerQuotaExceeded |
    And the Platform Console shows:
      "Provisioning Failed: Hetzner quota exceeded in eu-central-1"
    And Goose surfaces the error message to the user
    And Goose suggests: request quota increase or switch to a different region
    And no partial resources are left orphaned in the tenant Hetzner account
```

### TC-ONB-03: Hetzner credentials never transit the agent

```gherkin
Feature: Credential Security in Agentic Flow
  Scenario: Hetzner API token is entered via console, not through Goose
    Given the onboarding flow has reached the credential ingestion step
    When Goose prompts the user to enter credentials
    Then Goose directs the user to https://console.zero-ops.io/settings/credentials
    And Goose does not provide a credential input field itself
    And no Hetzner API token value appears in any Goose output, log, or MCP call payload
    And the token is only accepted via the Platform Console HTTPS form
```

### TC-ONB-04: Console-based Starter tenant onboarding

```gherkin
Feature: Starter Tenant Onboarding via Console
  Scenario: Solo developer provisions Starter environment through console
    Given a user is logged in to the Platform Console as a new tenant admin
    When they select "New Environment → Starter"
    Then the console displays a real-time cost estimate fetched from the Hetzner pricing API
    And the cost estimate is derived from the Hetzner API at request time, not from a pre-computed table
    When the tenant admin confirms provisioning
    Then zero-ops-api commits an AINativeSaaS CR with spec.tier=starter to the tenant control plane repo
    And Crossplane Composition A executes
    And within 60 seconds the environment status is Ready
    And the tenant namespace exists in the Zero-Ops shared cluster
    And a ResourceQuota and LimitRange are applied to the namespace
    And the tenant has a database in the shared CNPG instance with tenant_id-scoped RLS
    And a tenant-scoped Hetzner S3 prefix is allocated
    And the tenant can access their PostgREST endpoint
    And the tenant can access their ArgoCD application
    And the tenant can access their Grafana dashboard (tenant-scoped)
    And the agent conversation interface is available in the console
```

### TC-ONB-05: Duplicate tenant name rejected

```gherkin
Feature: Tenant Name Uniqueness
  Scenario: Creating a tenant with an existing name returns conflict
    Given a tenant with name acme-corp already exists
    When zero-ops-api receives a tenant_create intent with name=acme-corp
    Then it returns HTTP 409 Conflict
    And Goose surfaces the conflict to the user with suggested alternative names
    And no duplicate PostgreSQL record is created
    And no duplicate control plane repository is created
```

### TC-ONB-06: Cost estimate is fetched at request time

```gherkin
Feature: Real-Time Cost Estimation
  Scenario: Cost estimate call to Hetzner pricing API happens at form submission
    Given a tenant admin is on the New Environment form in the console
    When they click "Calculate Cost"
    Then a network call is made to the Hetzner pricing API at that moment
    And the returned estimate is displayed within 2 seconds
    And no cached pricing data from a previous session is used
```

---

## PROV — AINativeSaaS Provisioning

### TC-PROV-01: Enterprise Composition B provisions all required resources

```gherkin
Feature: Enterprise AINativeSaaS Composition
  Background:
    Given an AINativeSaaS CR with spec.tier=enterprise has been applied

  Scenario: Crossplane Composition B creates the complete resource set
    When Crossplane Composition B executes for acme-corp-production
    Then the following managed resources are created and reach Ready state:
      | Resource Type           | Expected Name / Pattern              |
      | CAPI Cluster            | acme-corp-production                 |
      | CNPG Cluster            | acme-corp-cnpg                       |
      | ArgoCD Application      | acme-corp-argocd-app                 |
      | Hetzner S3 bucket       | acme-corp-storage                    |
      | nginx Ingress controller | acme-corp-ingress                   |
      | cert-manager Certificate | acme-corp-tls                       |
      | AgentSandbox deployment  | acme-corp-sandbox                   |
      | LiteLLM deployment      | acme-corp-ai-gateway                 |
      | PostgREST deployment    | acme-corp-postgrest                  |
      | CNPG ScheduledBackup    | acme-corp-backup-schedule            |
      | external-dns config     | acme-corp-dns                        |
    And all resources are labelled with crossplane.io/composite=acme-corp-production
    And the total composition completes within 15 minutes
```

### TC-PROV-02: Starter Composition A provisions namespace resources only

```gherkin
Feature: Starter AINativeSaaS Composition
  Scenario: Crossplane Composition A creates namespace-scoped resources in shared cluster
    Given an AINativeSaaS CR with spec.tier=starter has been applied
    When Crossplane Composition A executes
    Then a Kubernetes namespace is created in the Zero-Ops shared cluster
    And a ResourceQuota is applied with spec matching starter tier limits
    And a LimitRange is applied to the namespace
    And a database is created in the shared CNPG instance
    And the database has tenant_id-scoped RLS policies
    And a Hetzner S3 prefix is allocated (not a dedicated bucket)
    And an AgentSandbox pool allocation is created (not a dedicated deployment)
    And no new CAPI Cluster is provisioned
    And no new VMs are created on Hetzner
    And the namespace reaches Ready status within 60 seconds
```

### TC-PROV-03: CNPG cluster has both databases with pgvector enabled

```gherkin
Feature: Tenant Database Architecture
  Scenario: Two CNPG databases provisioned per tenant with pgvector
    Given an Enterprise AINativeSaaS environment is Ready
    When inspecting the CNPG cluster for acme-corp
    Then two databases exist:
      | Database Name             | Purpose         |
      | acme-corp-controlplane    | Platform admin  |
      | acme-corp-dataplane       | App data        |
    And both databases have pgvector extension enabled
    And shared_preload_libraries includes pg_stat_statements and vector
    And PgBouncer is running with default_pool_size=20 for both databases
```

### TC-PROV-04: KSOPS Age key generated and stored correctly

```gherkin
Feature: KSOPS Secret Management Provisioning
  Scenario: Age keypair generated at onboarding and stored in correct locations
    Given a new tenant acme-corp is being onboarded
    When zero-ops-api generates the Age keypair for acme-corp
    Then the Age public key is stored at s3://acme-corp-secrets/age.pub in the tenant Hetzner S3 bucket
    And the Age private key is stored as a Kubernetes Secret in the management cluster
    And the management cluster private key Secret is sealed by the platform key
    And no plaintext Age private key appears in any Git commit or API response

  Scenario: KSOPS decrypts secrets at ArgoCD apply time
    Given a KSOPS-encrypted Secret exists in the tenant control plane repository
    When ArgoCD applies the tenant manifests using the KSOPS Kustomize plugin
    Then KSOPS retrieves the Age private key from the management cluster Secret
    And decrypts the Secret value
    And creates a Kubernetes Secret in the tenant namespace
    And no plaintext secret value is written to Git at any point
```

### TC-PROV-05: ClusterResourceSet injects all edge components into new tenant cluster

```gherkin
Feature: ClusterResourceSet Edge Component Injection
  Scenario: All edge-catalog components are injected into a new Enterprise tenant cluster
    Given a new CAPI cluster for acme-corp has been provisioned and is Ready
    When ClusterResourceSet applies to the new cluster
    Then the following components are installed within 5 minutes:
      | Component                |
      | Grafana Alloy (DaemonSet)|
      | kube-events-exporter     |
      | K8sGPT Operator          |
      | K8sGPT result exporter   |
      | fleet-heartbeat CronJob  |
      | Cilium CNI               |
      | CCM and CSI drivers      |
      | cnpg2monitor             |
      | cert-manager             |
      | external-dns             |
      | nginx ingress controller  |
      | LiteLLM AI Gateway       |
      | PostgREST                |
      | AgentSandbox deployment  |
    And ArgoCD is configured in OCI pull mode pointing to the platform OCI artifact store
```

### TC-PROV-06: ArgoCD pulls from OCI catalog, not from zero-ops-api

```gherkin
Feature: GitOps Catalog Independence
  Scenario: ArgoCD in tenant cluster pulls from OCI store during zero-ops-api downtime
    Given zero-ops-api is unavailable
    And the OCI artifact store is available
    When ArgoCD's polling interval fires in the tenant cluster
    Then ArgoCD successfully pulls the latest catalog version from the OCI store
    And applies any pending sync without contacting zero-ops-api
    And no ArgoCD sync failure occurs due to zero-ops-api unavailability
```

### TC-PROV-07: GitOps-first — no imperative Kubernetes API writes during provisioning

```gherkin
Feature: GitOps-First Provisioning Constraint
  Scenario: zero-ops-api commits CR to Git and does not write to Kubernetes directly
    Given a tenant_create intent is processed
    When zero-ops-api handles the AINativeSaaS provisioning flow
    Then zero-ops-api commits an AINativeSaaS CR to the tenant control plane Git repository
    And zero-ops-api does not make any direct kubectl or Kubernetes API calls to the management cluster
    And all cluster mutations happen exclusively through ArgoCD → Crossplane reconciliation
```

---

## UPG — Tier Upgrade (Starter → Enterprise)

### TC-UPG-01: Starter to Enterprise upgrade — happy path blue-green

```gherkin
Feature: Tier Upgrade — Starter to Enterprise
  Background:
    Given tenant acme-corp has a running Starter environment
    And acme-corp-dataplane database contains application data

  Scenario: Upgrade completes with no data loss and no downtime
    Given the tenant admin selects "Upgrade to Enterprise" in the Platform Console
    When the console shows the cost diff and 5–15 minute migration window
    And the tenant admin confirms
    Then zero-ops-api updates spec.tier=enterprise in the tenant control plane repository via PR
    And Crossplane detects the spec drift
    And a new dedicated CAPI cluster is provisioned on the tenant Hetzner account
    And a new dedicated CNPG cluster is created
    And the new CNPG cluster is bootstrapped via pg_dump restore from the shared CNPG instance
    And once the new CNPG primary is healthy, DNS is updated to point to the new cluster
    And the old Starter namespace in the shared cluster is retained for 2 hours rollback window
    And the tenant's services continue serving requests throughout the migration
    And after the rollback window expires, the old namespace is decommissioned
    And the new Enterprise cluster shows Ready in the Platform Console
    And no application data is lost

  Scenario: Upgrade rolls back after DNS cutover reveals new cluster is unhealthy
    Given the blue-green upgrade has completed DNS cutover to the Enterprise cluster
    And the new Enterprise cluster shows error conditions within the rollback window
    When the tenant admin clicks "Rollback to Starter" in the Platform Console
    Then DNS is reverted to the old Starter namespace within 2 minutes
    And the Starter namespace is restored from rollback-retained state
    And the Enterprise cluster is decommissioned
    And the tenant admin receives a notification that rollback completed successfully
```

### TC-UPG-02: Upgrade migration preserves agent memory and control plane DB

```gherkin
Feature: Control Plane DB Preservation During Upgrade
  Scenario: Agent memory and tenant config survive Starter to Enterprise migration
    Given the tenant has stored agent conversation memory in acme-corp-controlplane database on shared CNPG
    When the Starter to Enterprise upgrade completes
    Then the acme-corp-controlplane database on the new dedicated CNPG contains all prior agent memory records
    And pgvector embeddings are intact and queryable
    And no agent conversation context is lost after the migration
```

---

## PRE — PR Ephemeral Environments

### TC-PRE-01: PR environment created on branch open — happy path

```gherkin
Feature: PR Ephemeral Environment Lifecycle
  Background:
    Given tenant acme-corp has a running Enterprise environment
    And a staging CSI snapshot exists for acme-corp

  Scenario: PR environment provisioned when developer opens a pull request
    Given a developer opens a PR on branch feature/new-ai-pipeline in the tenant application repository
    When the GitHub webhook fires the branch creation event
    Then an Argo Workflow pr-env-create triggers within 30 seconds
    And a namespace pr-feature-new-ai-pipeline is created in the tenant cluster within 2 minutes
    And within the namespace:
      | Resource                        | Config                              |
      | CNPG Cluster (single node)      | Bootstrapped from staging CSI snapshot |
      | Application deployment          | Using branch feature/new-ai-pipeline image |
      | Hetzner S3 prefix               | Scoped to pr/feature-new-ai-pipeline/ |
      | AgentSandbox volume             | Restored from tenant sandbox snapshot |
    And the PR environment URL is posted as a GitHub status check on the PR
    And the PR environment is accessible via HTTPS within 2 minutes of namespace creation
```

### TC-PRE-02: PR environment torn down on PR close

```gherkin
Feature: PR Ephemeral Environment Teardown
  Scenario: All PR environment resources are removed when PR is closed
    Given a PR environment pr-feature-new-ai-pipeline is running
    When the developer closes or merges the PR
    Then the Argo Workflow pr-env-teardown triggers within 30 seconds
    And the namespace pr-feature-new-ai-pipeline is deleted within 2 minutes
    And the CNPG cluster inside the namespace is deleted
    And the Hetzner S3 prefix pr/feature-new-ai-pipeline/ is purged
    And the billing record for this PR environment is closed with end timestamp
    And no orphaned resources remain in the tenant cluster or Hetzner account
```

### TC-PRE-03: PR environment database isolation from other branches

```gherkin
Feature: PR Environment Database Isolation
  Scenario: Two concurrent PR branches have fully isolated databases
    Given PR environments pr-branch-a and pr-branch-b are both running
    When a schema migration is applied in pr-branch-a
    Then the schema in pr-branch-b is unchanged
    And a destructive operation in pr-branch-a does not affect data in pr-branch-b
```

### TC-PRE-04: PR environment fallback when CSI snapshot is unavailable

```gherkin
Feature: PR Environment Fallback — No Staging Snapshot
  Scenario: PR environment uses empty database when no staging snapshot exists
    Given no CSI snapshot exists for the tenant staging database
    When a PR is opened and the pr-env-create workflow fires
    Then the CNPG cluster is bootstrapped from an empty cluster with schema-only migration
    And a warning is posted to the PR as a GitHub status check:
      "PR environment using empty database — no staging data available."
    And the PR environment is still created and accessible
    And the CNPG cluster reaches Ready state
```

### TC-PRE-05: CNPG restore from CSI snapshot completes within target time

```gherkin
Feature: PR Environment Bootstrap Performance
  Scenario: CNPG restore from staging CSI snapshot completes within 90 seconds
    Given a staging CSI snapshot of typical size (under 10GB) exists
    When the pr-env-create workflow initiates CNPG bootstrap from the snapshot
    Then the CNPG cluster reaches Running state within 90 seconds of workflow start
```

---

## D2 — Day-2 Operations

### TC-D2-01: Scale CNPG instances via control plane repo PR

```gherkin
Feature: Day-2 CNPG Scaling
  Background:
    Given acme-corp has an Enterprise environment with CNPG instances=3

  Scenario: Tenant admin scales CNPG via PR to control plane repository
    When the tenant admin opens a PR updating spec.database.instances from 3 to 5
    And the PR is approved and merged
    Then ArgoCD detects the change in the tenant control plane repository
    And Crossplane updates the CNPG cluster spec
    And CNPG adds 2 new replica instances without downtime
    And VictoriaMetrics shows 5 CNPG pods healthy within 10 minutes
    And a change event is written to the OpenSearch infra-changes index
```

### TC-D2-02: Add team member to tenant

```gherkin
Feature: Team Management
  Scenario: Tenant admin adds a new team member via Platform Console
    Given alice is the tenant admin of acme-corp
    When alice navigates to the Team tab and invites bob@acme-corp.com as tenant_user
    Then a Kratos identity is created for bob with:
      | email     | bob@acme-corp.com |
      | tenant_id | acme-corp         |
      | role      | tenant_user       |
    And a Keto tuple tenant:acme-corp#viewer@user:bob-uuid is created
    And bob can log in to the Platform Console
    And bob can view acme-corp environments (read-only)
    And bob cannot create or delete environments
    And bob cannot approve destructive operation tickets
```

### TC-D2-03: Tenant uploads a runbook to tenant-scoped RAG corpus

```gherkin
Feature: Tenant-Scoped Runbook Management
  Scenario: Tenant admin uploads a custom runbook that DiagnosticsAgent can use
    Given acme-corp has an Enterprise environment
    When the tenant admin uploads a runbook named "vector-db-full-recovery.md" via the Runbooks tab
    Then the runbook is embedded and indexed into the acme-corp tenant RAG corpus
    And the platform SOP runbook corpus is unchanged
    When DiagnosticsAgent receives an alert for the acme-corp vector DB
    Then DiagnosticsAgent queries the acme-corp scoped RAG corpus
    And the returned context includes chunks from "vector-db-full-recovery.md"
    And no other tenant can access acme-corp's runbook corpus
```

### TC-D2-04: View Grafana dashboard from Platform Console

```gherkin
Feature: Grafana Dashboard Access
  Scenario: Tenant user accesses Grafana via console escape hatch
    Given a tenant user is on the Monitoring tab in the Platform Console
    When they click "View in Grafana" for environment acme-corp-production
    Then the browser navigates to the Grafana URL
    And the Grafana URL includes the query parameter var-tenant_id=acme-corp
    And the dashboard is pre-filtered to show only acme-corp-production metrics
    And no other tenant's metrics are visible
```

### TC-D2-05: Age key rotation

```gherkin
Feature: Age Encryption Key Rotation
  Background:
    Given acme-corp has secrets encrypted with their current Age keypair

  Scenario: Platform admin rotates tenant Age key without losing access to secrets
    When the platform admin initiates an Age key rotation for acme-corp
    Then a new Age keypair is generated
    And all existing KSOPS-encrypted secrets in the tenant control plane repository are re-encrypted with the new public key
    And the new Age public key is uploaded to s3://acme-corp-secrets/age.pub
    And the new Age private key is stored in the management cluster Secret
    And the old Age private key is deleted after a configurable grace period
    And ArgoCD can successfully decrypt and apply all re-encrypted secrets using the new key
    And no secrets become inaccessible during the rotation window
```

### TC-D2-06: Agent conversation is tenant-scoped with pgvector memory

```gherkin
Feature: Agent Conversation Memory
  Scenario: Agent remembers previous conversation context within tenant scope
    Given alice from acme-corp has a prior conversation with the agent about acme-corp-production
    When alice starts a new agent conversation session in the Platform Console
    Then the agent retrieves prior conversation embeddings from the acme-corp-controlplane pgvector database
    And the agent's responses demonstrate awareness of prior context
    And no conversation memory from rival-corp appears in alice's context

  Scenario: Agent memory is stored at session end
    When alice's agent conversation session ends
    Then the conversation is embedded and upserted into the acme-corp-controlplane pgvector store
    And the upsert happens once at session end, not on every message
```

---

## OBS — Fleet Observability

### TC-OBS-01: Grafana Alloy emits topology-labelled metrics within 60s of cluster boot

```gherkin
Feature: Fleet Observability — Alloy Metric Collection
  Background:
    Given a new tenant Enterprise cluster has been provisioned

  Scenario: Topology-labelled metrics arrive in VictoriaMetrics within 60 seconds of cluster boot
    When the tenant cluster boots and Grafana Alloy is injected via ClusterResourceSet
    Then within 60 seconds, metrics arrive in VictoriaMetrics vminsert
    And every metric carries the mandatory topology labels:
      | label            | value              |
      | cluster_id       | acme-corp-uuid     |
      | shard_id         | shard-eu-1-uuid    |
      | region           | eu-central-1       |
      | cloud_provider   | hetzner            |
      | availability_zone| hel1-dc2           |
      | cluster_class    | hetzner-prod       |
      | tenant_id        | acme-corp          |
    And the labels are injected by Alloy from cluster metadata, not scraped from the cluster
```

### TC-OBS-02: VictoriaMetrics cross-cluster PromQL query returns correct results

```gherkin
Feature: Fleet Observability — VictoriaMetrics Cross-Cluster Queries
  Scenario: PromQL query filters degraded clusters by region correctly
    Given 10 tenant clusters exist across regions eu-central-1 and us-east-1
    And 3 clusters in eu-central-1 have health_status=degraded
    When a PromQL query is executed:
      count by (cluster_id) (fleet_health_status{region="eu-central-1", health_status="degraded"})
    Then exactly 3 cluster_id values are returned
    And no us-east-1 clusters are included in the result
```

### TC-OBS-03: OpenSearch event timeline records all required event types

```gherkin
Feature: Fleet Observability — OpenSearch Event Timeline
  Scenario: All six event types are written to their correct indexes
    Given a tenant cluster is running normally
    When the following events occur:
      | Event                                    | Expected Index     |
      | Pod OOMKilled in tenant cluster          | k8s-events         |
      | K8sGPT Result CRD created                | k8sgpt-findings    |
      | ArgoCD sync completes                    | argocd-syncs       |
      | Argo Workflow scale_workers completes    | infra-changes      |
      | Catalog OCI artifact pushed              | catalog-pushes     |
      | VictoriaMetrics alert fires              | alert-firings      |
    Then each event appears in its designated OpenSearch index within 60 seconds
    And every event document contains:
      | field          |
      | timestamp      |
      | event_type     |
      | cluster_id     |
      | region         |
      | cloud_provider |
      | actor          |
      | summary        |
      | correlation_id |
```

### TC-OBS-04: cnpg2monitor patches PodMonitor within 30 seconds of CNPG Ready

```gherkin
Feature: cnpg2monitor — PodMonitor Lifecycle Management
  Scenario: cnpg2monitor patches PodMonitor with topology relabelings after CNPG provisioning
    Given a CNPG cluster acme-corp-cnpg is provisioned with label zero-ops.io/monitored=true
    When the CNPG cluster reaches Ready state
    Then within 30 seconds, cnpg2monitor patches the PodMonitor for acme-corp-cnpg
    And the PodMonitor spec.podMetricsEndpoints[port=metrics].relabelings contains topology labels
    And the annotation cnpg2monitor.zero-ops.io/last-config-generation is set on the PodMonitor
    And the CNPG Cluster CR itself is not patched or modified by cnpg2monitor

  Scenario: cnpg2monitor emits CNPGProvisioned event to OpenSearch
    When cnpg2monitor detects a new CNPG cluster reaching Ready state
    Then an event with event_type=CNPGProvisioned is written to the OpenSearch infra-changes index
    And the event contains cluster_id, tenant_id, and region fields
```

### TC-OBS-05: cnpg2monitor annotation state stored on PodMonitor, not CNPG CR

```gherkin
Feature: cnpg2monitor — Annotation Ownership
  Scenario: cnpg2monitor does not write annotations to CNPG Cluster CR
    Given cnpg2monitor has reconciled CNPG cluster acme-corp-cnpg
    When inspecting the CNPG Cluster CR for acme-corp-cnpg
    Then no cnpg2monitor.zero-ops.io/* annotations exist on the CNPG Cluster CR
    And all cnpg2monitor annotations exist on the PodMonitor for acme-corp-cnpg only
```

### TC-OBS-06: Alloy configuration propagates to all tenant clusters without redeployment

```gherkin
Feature: Fleet Observability — Alloy Centralized Config
  Scenario: Alloy config update propagates to all clusters without pod restart
    Given 100 tenant clusters each have Grafana Alloy running
    When zero-ops-api updates the Alloy central config (e.g. adds a new scrape target)
    Then within 5 minutes all 100 Alloy instances apply the updated config
    And no Alloy pod restarts are observed
    And metric collection continues uninterrupted during the config update
```

---

## AGT — Multi-Agent System

### TC-AGT-01: Collaborator routes cross-cluster alert through correct Worker Agent

```gherkin
Feature: Multi-Agent Routing
  Background:
    Given all Worker Agents and MCP tool servers are running
    And shard-eu-1 has 5 active tenant clusters

  Scenario: High CPU alert is routed from Collaborator to MetricsAgent then LifecycleAgent
    Given VictoriaMetrics fires a ClusterCPUHigh alert for cluster acme-corp-production
    When the alert webhook reaches zero-ops-api and is passed to the Collaborator Agent
    Then the Collaborator calls victoriametrics-mcp to get context for acme-corp-production
    And the Collaborator calls opensearch-mcp to check for infra changes in the prior 60 minutes
    And the Collaborator routes a scale intent to LifecycleAgent
    And LifecycleAgent checks the Policy Gate before submitting any Argo Workflow
    And the complete routing trace is recorded in the agent audit log
```

### TC-AGT-02: DiagnosticsAgent queries OpenSearch before runbook RAG for every alert

```gherkin
Feature: DiagnosticsAgent Reasoning Order
  Scenario: OpenSearch timeline query always precedes runbook RAG query
    Given any alert is received by DiagnosticsAgent
    When DiagnosticsAgent processes the alert
    Then the first external call is to opensearch-mcp for the event timeline
    And only after receiving the OpenSearch response does DiagnosticsAgent query the runbook RAG
    And this ordering is enforced for every alert, without exception
    And the agent audit log reflects: opensearch-mcp call timestamp < rag-query call timestamp
```

### TC-AGT-03: ProvisioningAgent creates XR claim via crossplane-mcp only

```gherkin
Feature: ProvisioningAgent — Crossplane-Only Provisioning Path
  Scenario: ProvisioningAgent never calls capi-mcp directly for environment creation
    Given a "create enterprise environment" intent reaches ProvisioningAgent
    When ProvisioningAgent processes the intent
    Then it calls crossplane-mcp:create with the AINativeSaaS claim spec
    And it does not call capi-mcp at any point during environment creation
    And the agent audit log shows zero capi-mcp calls in the ProvisioningAgent trace
    And the AINativeSaaS CR is created in the correct namespace
```

### TC-AGT-04: Policy Gate prevents out-of-quota operations

```gherkin
Feature: Safe Execution Layer — Policy Gate
  Scenario: LifecycleAgent is blocked by Policy Gate when tenant is at quota
    Given tenant acme-corp is at maximum cluster count as per their quota
    When LifecycleAgent attempts to submit a scale_workers Argo Workflow for acme-corp
    Then the Policy Gate evaluates the quota check and returns deny
    And no Argo Workflow is submitted
    And the denial is recorded in the agent audit log with reason=quota_exceeded
    And the Collaborator Agent surfaces the quota denial to the Platform Console
```

### TC-AGT-05: Runbook RAG does not invent remediation when no runbook matches

```gherkin
Feature: DiagnosticsAgent — Runbook Grounding
  Scenario: DiagnosticsAgent escalates to human when no matching runbook exists
    Given an alert fires for a novel anomaly with no matching runbook in the corpus
    When DiagnosticsAgent searches the runbook RAG corpus
    And no relevant runbook chunk is returned above similarity threshold
    Then DiagnosticsAgent does not propose a remediation step
    And it escalates to the Collaborator Agent with status=no_runbook_match
    And the Platform Console shows an alert with:
      "Anomaly detected. No matching runbook. Manual investigation required."
    And a "Resolve in Cursor" button is displayed with structured MCP context
```

### TC-AGT-06: Agent audit log captures every agent action

```gherkin
Feature: Agent Audit Log Completeness
  Scenario: Complete agent action trace is written to audit log
    Given a CPU alert triggers a Collaborator → MetricsAgent → LifecycleAgent → Workflow chain
    When the chain completes (workflow execution confirmed by VictoriaMetrics)
    Then the agent_audit_log in PostgreSQL contains entries for:
      | Event                                     |
      | Alert received by Collaborator            |
      | victoriametrics-mcp call + response       |
      | opensearch-mcp call + response            |
      | LifecycleAgent routing decision           |
      | Policy Gate evaluation result             |
      | Argo Workflow submission                  |
      | Argo Workflow completion                  |
      | OpenSearch infra-changes event written    |
    And each entry has a timestamp, actor, action, and correlation_id
    And all entries in the chain share the same correlation_id
```

---

## APL — Autopilot Mode

### TC-APL-01: Autopilot mode raises PR — never commits directly

```gherkin
Feature: Autopilot — PR-Based Change Proposal
  Background:
    Given autopilot mode is enabled for tenant acme-corp
    And VictoriaMetrics detects PgBouncer connection pool saturation at 85% for 20 minutes

  Scenario: Agent proposes PgBouncer increase via PR, not direct commit
    When DiagnosticsAgent identifies PgBouncer pool increase as the correct remediation
    And Collaborator classifies it as non-destructive
    Then ProvisioningAgent creates a PR on the tenant control plane repository
    And the PR branch is named autopilot/pgbouncer-pool-increase-{date}
    And the PR changes spec.pooler.parameters.default_pool_size from "20" to "40"
    And the PR description contains:
      | Field                           |
      | VictoriaMetrics metric graph link|
      | K8sGPT finding reference        |
      | Runbook reference               |
      | Proposed change diff            |
    And no direct commit to the main branch is made
    And the Platform Console Approval Queue shows the pending PR
```

### TC-APL-02: Tenant approves autopilot PR and change is applied

```gherkin
Feature: Autopilot — PR Approval and Application
  Scenario: Approved autopilot PR results in ArgoCD applying the change
    Given an autopilot PR for PgBouncer pool increase exists for acme-corp
    When the tenant admin approves and merges the PR
    Then ArgoCD detects the merge within 3 minutes
    And ArgoCD applies the updated CNPG pooler spec
    And PgBouncer restarts with default_pool_size=40
    And within 5 minutes VictoriaMetrics shows PgBouncer saturation metric normalised
    And a change event is written to OpenSearch infra-changes with actor=autopilot
    And the agent audit log reflects the full chain from alert to applied change
```

### TC-APL-03: Tenant rejects autopilot PR — no change is applied

```gherkin
Feature: Autopilot — PR Rejection
  Scenario: Rejected autopilot PR results in no infrastructure change
    Given an autopilot PR for PgBouncer pool increase exists for acme-corp
    When the tenant admin closes the PR without merging
    Then ArgoCD does not apply any change to the tenant environment
    And the PgBouncer pool size remains at its current value
    And the Platform Console Approval Queue shows the PR as rejected
    And the agent audit log records the rejection with timestamp and actor
```

### TC-APL-04: Autopilot mode OFF — alert displayed in console with Resolve in Cursor

```gherkin
Feature: Autopilot Disabled — Console Alert Flow
  Background:
    Given autopilot mode is disabled for tenant acme-corp (default state)
    And PgBouncer saturation alert fires for acme-corp-production

  Scenario: Alert appears in console with Resolve in Cursor button
    When DiagnosticsAgent identifies PgBouncer pool increase as remediation
    Then no PR is created in the tenant control plane repository
    And the Platform Console Approval Queue shows an alert for acme-corp-production
    And a "Resolve in Cursor" button is displayed in the alert
    And clicking "Resolve in Cursor" generates a structured MCP context payload containing:
      | Field                    |
      | Cluster state            |
      | K8sGPT findings          |
      | OpenSearch event timeline|
      | Proposed change diff     |
      | Available MCP tools      |
    And Cursor opens with this context pre-loaded
```

### TC-APL-05: Always-human-approval operations cannot be autopilot-executed

```gherkin
Feature: Autopilot Safety Invariants
  Background:
    Given autopilot mode is enabled for tenant acme-corp

  Scenario: Agent cannot autopilot-delete a CNPG cluster
    When an agent determines that CNPG cluster deletion is the correct action
    Then the Collaborator Agent classifies the operation as requires_human_approval
    And no autopilot PR is created for this operation
    And a CNPG ticket is created in the Platform Console Approval Queue instead
    And the operation waits for explicit human approval

  Scenario: Agent cannot autopilot-scale-down CNPG instances to zero
    When an agent proposes reducing spec.database.instances to 0
    Then the Policy Gate blocks the operation with reason=destructive_scale_down_prohibited
    And no PR or commit is created

  Scenario: Provider migration always requires human-approved PR
    Given autopilot mode is enabled
    When a cloud provider migration is proposed (spec.cloud change)
    Then zero-ops-api authors a PR and waits for the tenant admin to approve it
    And the ArgoCD sync does not proceed until the PR is merged by a human
```

---

## MIG — Provider Migration

### TC-MIG-01: Blue-green provider migration — happy path

```gherkin
Feature: Provider Migration — Blue-Green with CNPG PITR
  Background:
    Given acme-corp has a running Enterprise environment on Hetzner
    And acme-corp-dataplane database contains 5GB of application data

  Scenario: Tenant migrates from Hetzner to AWS with no data loss
    Given the tenant admin selects "Migrate Provider → AWS" in the Platform Console
    And provides AWS credentials via the console credentials form
    When zero-ops-api authors a provider migration PR updating spec.cloud=aws
    And the tenant admin approves and merges the PR
    Then Crossplane detects the spec.cloud drift
    And a new CAPI cluster is provisioned on AWS
    And CNPG takes a base backup on the Hetzner cluster
    And a new CNPG cluster on AWS is bootstrapped via PITR from the Hetzner backup
    And once the AWS CNPG primary is confirmed healthy
    Then DNS is updated to point to the AWS cluster ingress
    And the Hetzner cluster is marked standby and retained
    And the Platform Console shows: "Migration complete. AWS cluster is live."
    And the rollback window is shown with its expiry timestamp
    And all application data is present on the AWS CNPG cluster
    And no data is lost during the migration
```

### TC-MIG-02: Provider migration rollback during rollback window

```gherkin
Feature: Provider Migration Rollback
  Scenario: Tenant rolls back to Hetzner after detecting issues on AWS cluster
    Given AWS migration has completed DNS cutover
    And the rollback window has not expired
    When the tenant admin clicks "Rollback to Hetzner" in the Platform Console
    Then DNS is reverted to the Hetzner cluster ingress within 2 minutes
    And the Hetzner cluster is restored to active state
    And the AWS cluster is decommissioned
    And the tenant admin receives confirmation of successful rollback

  Scenario: Rollback option is unavailable after rollback window expires
    Given the rollback window for the AWS migration has expired
    When the tenant admin views the Platform Console
    Then no "Rollback to Hetzner" option is displayed
    And the Hetzner cluster has been decommissioned
```

### TC-MIG-03: Provider credentials never appear in agent output

```gherkin
Feature: Provider Migration — Credential Security
  Scenario: AWS credentials entered via console are never exposed to agents
    Given the migration flow has reached the AWS credential step
    When the tenant admin enters their AWS credentials in the Platform Console credentials form
    Then the AWS credentials are encrypted with the tenant Age key and stored via KSOPS
    And no AWS credential value appears in any MCP call payload
    And no AWS credential value appears in any agent output or conversation
    And no AWS credential value is written to any Git repository in plaintext
```

---

## SEC — Security Boundaries

### TC-SEC-01: Platform team cannot see tenant application data

```gherkin
Feature: Data Visibility Boundaries
  Scenario: Platform admin cannot query tenant data plane database through any platform interface
    Given acme-corp's data plane database contains sensitive application records
    When a platform admin navigates the Platform Console for acme-corp-production
    Then the console displays only infrastructure health metrics:
      | Visible                      | Not Visible                       |
      | CPU and memory usage         | Database table contents           |
      | CNPG cluster status          | Tenant application logs           |
      | ArgoCD sync state            | Tenant end-user data              |
      | K8sGPT infrastructure findings| Tenant SaaS product metrics      |
    And no MCP tool server provides a mechanism to query tenant data plane DB contents
    And the opensearch-mcp index contains only infrastructure events, not application data
```

### TC-SEC-02: Starter tier — tenant namespace network isolation

```gherkin
Feature: Starter Tier Network Isolation
  Scenario: Tenant A pods cannot reach Tenant B pods in the shared cluster
    Given tenants acme-corp and rival-corp both have Starter environments in the shared cluster
    When an acme-corp pod attempts to connect to a rival-corp pod IP
    Then the connection is blocked by Kubernetes NetworkPolicy
    And no traffic flows between the two namespaces
    And the NetworkPolicy applies to all pod-to-pod communication, not just external traffic
```

### TC-SEC-03: Starter tier — RLS prevents cross-tenant database access

```gherkin
Feature: Starter Tier Database Isolation via RLS
  Scenario: Tenant A cannot read Tenant B's rows in the shared CNPG instance
    Given acme-corp and rival-corp share a CNPG instance on Starter tier
    And both have rows in their respective databases with tenant_id set correctly
    When a query is executed using acme-corp's database credentials
    Then only rows with tenant_id=acme-corp are returned
    And no rows with tenant_id=rival-corp are returned
    And attempting to set tenant_id=rival-corp in a query is rejected by RLS policy
```

### TC-SEC-04: Secrets never appear in Git in plaintext

```gherkin
Feature: Secret Management Security
  Scenario: All secrets in the tenant control plane repository are KSOPS-encrypted
    Given any secret (API key, database password, cloud credential) is committed to the tenant control plane repo
    When the Git repository contents are inspected
    Then all secret values are encrypted SOPS blocks with Age recipient header
    And no plaintext secret values exist in any file in the repository
    And the Age private key is never committed to the repository
```

### TC-SEC-05: MCP tool server scope is bounded by tenant_id in JWT

```gherkin
Feature: MCP Tool Server Tenant Scoping
  Scenario: MCP tool servers use tenant_id from JWT to scope all responses
    Given alice has a JWT with tenant_id=acme-corp
    When alice calls opensearch-mcp with a query for all events
    Then the tool server constructs the OpenSearch query with a filter: tenant_id=acme-corp
    And only events from acme-corp are returned
    And events from other tenants are not included in the response
    And alice cannot override the tenant_id filter by passing a different value in the request body
```

---

## DES — Destructive Operations & Human Approval

### TC-DES-01: Destructive operation creates CNPG ticket in Platform Console

```gherkin
Feature: Destructive Operation Human Approval Gate
  Background:
    Given VictoriaMetrics detects DiskPressure on a node in acme-corp-production
    And K8sGPT identifies node drain as the required remediation

  Scenario: Node drain intent is classified as destructive and creates approval ticket
    When DiagnosticsAgent queries OpenSearch and finds no correlating change event
    And DiagnosticsAgent queries runbook RAG and identifies node drain as remediation
    And Collaborator Agent classifies the intent as destructive
    Then the Argo Workflow pauses before execution
    And a CNPG ticket is created in the Platform Console Approval Queue
    And the ticket contains:
      | Field                     |
      | Affected node name        |
      | Cluster name              |
      | Shard ID                  |
      | Functional domain         |
      | Requesting agent          |
      | Runbook reference         |
      | Correlated OpenSearch events|
      | Workflow ID               |
    And a "Resolve in Cursor" button is displayed in the ticket
    And no node drain action has been taken yet
```

### TC-DES-02: Resolve in Cursor button generates correct MCP context payload

```gherkin
Feature: Resolve in Cursor — MCP Context Generation
  Scenario: Clicking Resolve in Cursor opens Cursor with structured MCP context
    Given a destructive operation ticket exists for a node drain on acme-corp-production
    When the on-call engineer clicks "Resolve in Cursor"
    Then Cursor is opened with a structured MCP message containing:
      | MCP tool available       |
      | k8sgpt-mcp               |
      | opensearch-mcp           |
      | capi-mcp                 |
      | victoriametrics-mcp      |
    And the message includes the workflow ID for approval
    And the message includes the full ticket context (node, cluster, shard, events)
    And the engineer can approve or reject the workflow from within Cursor using the MCP tools
```

### TC-DES-03: Approved destructive operation executes safely

```gherkin
Feature: Destructive Operation Execution After Approval
  Scenario: Approved node drain executes with PDB-respecting eviction
    Given a node drain approval ticket has been approved by the on-call engineer
    When the Argo Workflow resumes
    Then the Safe Node Recycle workflow executes
    And the node is cordoned before any eviction
    And PodDisruptionBudgets are respected during pod eviction
    And pods are evicted gracefully
    And a replacement VM is triggered via CAPI
    And the workflow completion event is written to OpenSearch infra-changes
    And VictoriaMetrics confirms node health is restored after replacement
    And the ticket is marked as resolved in the Platform Console
```

### TC-DES-04: Rejected destructive operation ticket — no execution occurs

```gherkin
Feature: Destructive Operation Rejection
  Scenario: Rejected ticket results in no infrastructure action
    Given a node drain ticket is in the Approval Queue
    When the on-call engineer rejects the ticket with a reason
    Then the Argo Workflow is cancelled
    And no node drain action is taken
    And the rejection reason and actor are recorded in the agent audit log
    And the ticket status is updated to Rejected in the Platform Console
```

---

## HA — High Availability

### TC-HA-01: Tenant SaaS workload continues during management cluster outage

```gherkin
Feature: Tenant Runtime Independence from Management Cluster
  Scenario: Tenant application serves requests unaffected during 30-minute management cluster outage
    Given acme-corp's Enterprise environment is running and serving traffic
    And a load test is actively running against acme-corp's application endpoint
    When the Zero-Ops management cluster becomes completely unavailable
    Then the load test continues without increased error rate
    And p99 latency of acme-corp application requests remains within normal bounds
    And the CNPG cluster in the tenant cluster continues operating
    And ArgoCD in the tenant cluster continues operating (it cannot sync but services remain up)
    And the tenant's application pods continue running
    And no tenant-facing service is degraded for the 30-minute outage window
```

### TC-HA-02: Management cluster recovery — Crossplane resumes without manual intervention

```gherkin
Feature: Management Cluster Self-Healing After Outage
  Scenario: Crossplane resumes composition reconciliation after management cluster restart
    Given the management cluster was unavailable for 30 minutes
    And there was a pending AINativeSaaS provisioning request during the outage
    When the management cluster restarts and becomes healthy
    Then Crossplane resumes composition reconciliation within 60 seconds of ready
    And the pending provisioning request continues from its last known state
    And no manual intervention is required
    And the Crossplane controller logs show "resumed reconciliation" entries
```

### TC-HA-03: ArgoCD in tenant cluster does not call zero-ops-api during outage

```gherkin
Feature: ArgoCD OCI Independence
  Scenario: Tenant cluster ArgoCD syncs from OCI during management cluster outage
    Given zero-ops-api is unavailable
    When ArgoCD in the tenant cluster reaches its sync interval
    Then ArgoCD fetches the catalog OCI artifact directly from the OCI artifact store
    And applies any pending sync changes
    And no ArgoCD sync error is logged referencing zero-ops-api
    And the ArgoCD application status shows Synced after the interval
```

---

## CLN — Cleanup & Decommission

### TC-CLN-01: Enterprise environment decommission removes all resources

```gherkin
Feature: Enterprise Environment Decommission
  Background:
    Given acme-corp has a running Enterprise environment acme-corp-production
    And a human-approved PR to delete the environment has been merged

  Scenario: All cloud and Kubernetes resources are removed on decommission
    When the cluster-delete Argo Workflow executes
    Then the CAPI Cluster for acme-corp is deleted
    And all Hetzner VMs for acme-corp are deprovisioned
    And the CNPG cluster is deleted
    And the CNPG backups in Hetzner S3 are retained for the configured retention period
    And the ArgoCD Application is deleted
    And the tenant namespace is removed from the management cluster
    And the PostgreSQL tenant record is marked as decommissioned (not deleted)
    And a decommission event is written to OpenSearch infra-changes
    And the Platform Console shows the environment as Decommissioned

  Scenario: Decommission is blocked if a CNPG backup retention policy is active
    Given the tenant has a backup retention policy requiring 7-day retention
    When the cluster-delete workflow reaches the S3 cleanup step
    Then the S3 backup objects are not deleted immediately
    And the cleanup is scheduled for after the retention period expires
    And a note in the Approval Queue ticket references the retention schedule
```

### TC-CLN-02: Starter environment namespace teardown removes only tenant resources

```gherkin
Feature: Starter Environment Namespace Cleanup
  Scenario: Decommissioning a Starter tenant removes only their namespace resources
    Given acme-corp is on Starter tier sharing a cluster with 5 other tenants
    When the acme-corp Starter environment is decommissioned
    Then the namespace for acme-corp is deleted
    And the ResourceQuota and LimitRange for acme-corp are removed
    And the acme-corp database in the shared CNPG is dropped
    And the acme-corp Hetzner S3 prefix objects are deleted
    And the shared CNPG instance continues running for the remaining 5 tenants
    And no resources belonging to other tenants are affected
```

### TC-CLN-03: Tenant eject transfers control plane repo ownership

```gherkin
Feature: Tenant Eject — No Vendor Lock-In
  Scenario: Tenant can eject and take full ownership of their environment
    Given acme-corp has an Enterprise environment
    When the tenant admin initiates an eject via the console Settings → Eject option
    Then zero-ops-api transfers ownership of the tenant control plane repository to the tenant's Git account
    And Zero-Ops deploy keys are removed from the repository
    And ArgoCD in the tenant cluster is reconfigured to point to the tenant-owned repo
    And the tenant receives documentation on how to manage the repo independently
    And the Platform Console shows acme-corp as Ejected status
    And the tenant's running cluster and services remain fully operational after eject
    And Zero-Ops no longer has write access to any tenant infrastructure after eject
```

---

## Cross-Cutting Test Scenarios

### TC-XC-01: End-to-end onboarding to first agent conversation — full chain

```gherkin
Feature: Full Chain — Onboarding to Agent Conversation
  Scenario: New Enterprise tenant onboards and successfully converses with agent about their environment
    Given no tenant acme-corp exists
    When the full Journey A (agentic enterprise onboarding) completes successfully
    Then alice logs in to the Platform Console
    And navigates to the Agent Conversation interface
    And types: "What is the current status of acme-corp-production?"
    Then the agent retrieves VictoriaMetrics data for acme-corp-production via victoriametrics-mcp
    And retrieves recent events from OpenSearch via opensearch-mcp
    And responds with a summary of the current environment health
    And the response references data only from acme-corp (not from any other tenant)
    And the conversation is stored in the acme-corp-controlplane pgvector database
    And the conversation is retrievable in a subsequent session
```

### TC-XC-02: Autopilot PR chain — alert to applied change — full audit trail

```gherkin
Feature: Full Chain — Alert to Applied Change with Complete Audit Trail
  Scenario: Every step of the autopilot chain produces an auditable record
    Given autopilot is enabled for acme-corp
    And PgBouncer saturation exceeds 80% for 20 minutes
    When the full Journey F (autopilot PR approval) chain completes
    Then the following records exist in the specified stores:
      | Record                                    | Store                              |
      | ClusterCPUHigh or PoolSaturation alert    | OpenSearch alert-firings           |
      | DiagnosticsAgent opensearch-mcp call      | agent_audit_log PostgreSQL         |
      | DiagnosticsAgent rag-query call           | agent_audit_log PostgreSQL         |
      | Autopilot PR creation                     | Tenant control plane Git repo      |
      | PR approval by tenant admin               | agent_audit_log PostgreSQL         |
      | ArgoCD sync completion                    | OpenSearch argocd-syncs            |
      | PgBouncer config change applied           | OpenSearch infra-changes           |
      | VictoriaMetrics metric normalisation      | VictoriaMetrics (queryable via PromQL) |
    And all records share the same correlation_id
    And the full chain from alert to metric normalisation is reconstructable from audit records alone
```

### TC-XC-03: Operational semantics — no per-request external fetches

```gherkin
Feature: Operational Semantics Anti-Pattern Prevention
  Scenario: Hetzner pricing API is not called on every console page load
    Given a tenant admin has the Environments page open in the Platform Console
    When they reload the page 10 times in 60 seconds
    Then the Hetzner pricing API receives at most 1 call per explicit user action
    And no background polling calls to the Hetzner pricing API are made on page load

  Scenario: JWKS endpoint is not called on every MCP request
    Given 1000 MCP requests arrive within 60 seconds
    When AgentGateway processes all 1000 requests
    Then the Hydra JWKS endpoint receives at most 1 network call during this window
    And all 1000 JWTs are validated against the cached JWKS

  Scenario: Agent memory is written at session end, not on every message
    Given an agent conversation session has 20 message exchanges
    When the session ends
    Then exactly 1 upsert operation is made to the pgvector store for this session
    And no upsert or write is made to pgvector during the 20 message exchanges
```

---

## Test Coverage Matrix

| PRD Section | Test Cases Covering |
|---|---|
| Journey A — Agentic Enterprise Onboarding | TC-ONB-01, TC-ONB-02, TC-ONB-03, TC-IDN-02, TC-PROV-01, TC-XC-01 |
| Journey B — Fleet Shard Bootstrap | TC-BOOT-01, TC-BOOT-02, TC-BOOT-03 |
| Journey C — Starter Onboarding | TC-ONB-04, TC-ONB-06, TC-PROV-02, TC-PROV-03 |
| Journey D — Starter → Enterprise Upgrade | TC-UPG-01, TC-UPG-02 |
| Journey E — PR Ephemeral Environments | TC-PRE-01, TC-PRE-02, TC-PRE-03, TC-PRE-04, TC-PRE-05 |
| Journey F — Autopilot PR Approval | TC-APL-01, TC-APL-02, TC-APL-03, TC-APL-04, TC-APL-05, TC-XC-02 |
| Journey G — Provider Migration | TC-MIG-01, TC-MIG-02, TC-MIG-03 |
| Journey H — Destructive Operation Approval | TC-DES-01, TC-DES-02, TC-DES-03, TC-DES-04 |
| Day-2 Operations | TC-D2-01, TC-D2-02, TC-D2-03, TC-D2-04, TC-D2-05, TC-D2-06 |
| Identity — Kratos/Hydra/Keto/AgentGateway | TC-IDN-01 through TC-IDN-06 |
| AINativeSaaS XRD & Compositions | TC-PROV-01 through TC-PROV-07 |
| Fleet Observability Stack | TC-OBS-01 through TC-OBS-06 |
| Multi-Agent System | TC-AGT-01 through TC-AGT-06 |
| Security Boundaries | TC-SEC-01 through TC-SEC-05 |
| HA & Resilience | TC-HA-01, TC-HA-02, TC-HA-03 |
| Cleanup & Decommission | TC-CLN-01, TC-CLN-02, TC-CLN-03 |
| Operational Semantics Anti-Patterns | TC-XC-03, TC-IDN-04, TC-PROV-06 |

---

## Open Flows — Gaps Identified for Product Team Review

The following user flows are present in PRD v8.0 but not yet fully covered by E2E tests. These are flagged for the product team to decide whether to add test cases or explicitly mark as out of scope for v8.0 TDD baseline.

| Gap | PRD Reference | Recommended Action |
|---|---|---|
| Supabase Realtime opt-in via catalog | Section 4.2.1 optional catalog services | Add TC-OPT-01: tenant enables Supabase Realtime from catalog |
| AgentSandbox cold start latency measurement | Section 5.4 — target <30s for 5GB snapshots | Add TC-PRF-01: snapshot restore benchmark test |
| LiteLLM AI Gateway routing to multiple providers | Section 4.2.1 | Add TC-AI-01: LiteLLM routes OpenAI and Anthropic calls correctly |
| PostgREST auto-API JWT/RLS validation | Section 5.1, PRD baseline | Add TC-API-01: PostgREST rejects requests with wrong tenant_id JWT |
| Fleet-wide upgrade rolling window (10 clusters/hour) | v7.0 Journey E retained in v8.0 | Add TC-AGT-07: UpgradeAgent batching at rolling rate |
| Alloy config update propagation verification | TC-OBS-06 covers propagation, not rollback | Add TC-OBS-07: invalid Alloy config update is rejected before propagation |
| Tenant eject — ArgoCD reconfiguration verification | TC-CLN-03 covers ownership, not ArgoCD reconciliation | Extend TC-CLN-03 with ArgoCD repo reconfiguration assertion |

---

*Document Status: DRAFT — TDD Baseline for Zero-Ops v8.0*  
*Total Test Cases: 61 specified + 7 open gaps identified*  
*PRD Reference: zero-ops-prd-v8.md*
