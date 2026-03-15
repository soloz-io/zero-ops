# Zero-Ops Platform — E2E BDD Test Case Specification

**Version:** 2.0 — Strict E2E (User-Observable Outcomes Only)
**PRD Reference:** Zero-Ops PRD v8.0 — SaaS Factory
**Status:** DRAFT — TDD Baseline
**Classification:** CONFIDENTIAL — INTERNAL USE ONLY

---

## What Qualifies as an E2E Test in This Document

Every scenario in this file satisfies all three criteria:

1. **Starts from a real user action** — a CLI command, a console click, a Git event, a Goose prompt, a webhook, or an API call. Not a mocked component call.
2. **Exercises the full stack** — the action travels through all real system layers (Auth → API → Git → ArgoCD → Crossplane/CAPI → Cloud → Observability → Console) without stubbing any layer in between.
3. **Asserts only user-observable outcomes** — what the user sees in the console, what infrastructure exists in their cloud account, what endpoints are reachable, what notifications they receive. Never asserts internal component state (RBAC rule contents, JWT field values, cache hit counts, annotation keys, audit log internal entries).

Tests that inspect Kubernetes resource field values, internal API contracts between services, JWT claim schemas, JWKS cache behaviour, or agent-internal routing decisions belong in the integration or unit test suite. They are explicitly excluded here.

---

## Test Conventions

```gherkin
Feature: <end-user capability>
  Background:
    Given <real-world preconditions visible to the user>

  Scenario: <user-observable behaviour under test>
    Given <user-observable starting state>
    When  <real user action>
    Then  <user-observable outcome>
    And   <additional user-observable outcome>
```

**Test IDs:** `TC-{DOMAIN}-{N}`

| Domain | User Journey Covered |
|---|---|
| `BOOT` | Platform Admin bootstraps and manages fleet shards |
| `AUTH` | User authentication — login, device auth, session, access denial |
| `ONB` | Tenant onboarding — Goose-driven and console-driven |
| `PROV` | AINativeSaaS environment provisioning — Starter and Enterprise |
| `UPG` | Tier upgrade and migration |
| `PRE` | PR ephemeral environment lifecycle |
| `D2` | Day-2 operations — scaling, team, runbooks, secrets |
| `OBS` | Observability — metrics, alerts, event timeline surfaced to users |
| `AGT` | Multi-agent flows — diagnosis, remediation, escalation |
| `APL` | Autopilot mode — PR proposals, approvals, rejections |
| `MIG` | Provider migration — blue-green, rollback |
| `SEC` | Security boundaries — cross-tenant access denial, data visibility |
| `DES` | Destructive operations — human approval gate |
| `HA` | High availability — tenant workload during platform outage |
| `CLN` | Decommission and eject |
| `CHAIN` | Full lifecycle chain scenarios |

---

## BOOT — Platform Bootstrap & Fleet Shard Lifecycle

### TC-BOOT-01: Management cluster bootstrap produces a reachable platform

```gherkin
Feature: Platform Admin bootstraps the first fleet shard
  Background:
    Given a Hetzner account with sufficient quota in region fsn1
    And the Zero-Ops CLI is installed and the admin has Hetzner API credentials

  Scenario: Full bootstrap results in a reachable platform console and API
    When the admin runs:
      """
      zero-ops mgmt bootstrap --name=shard-eu-1 --region=fsn1
      """
    Then within 20 minutes the command exits with success
    And the Platform Console is reachable at https://console.nutgraf.in
    And the platform admin can log in to the console
    And the console shows shard-eu-1 listed as an active fleet shard
    And the zero-ops-api health endpoint returns HTTP 200
```

### TC-BOOT-02: Second shard added to fleet increases available capacity

```gherkin
Feature: Fleet capacity scales with additional shards
  Background:
    Given shard-eu-1 is active and the platform is running

  Scenario: Adding a second shard makes it available for new tenant provisioning
    When the admin runs:
      """
      zero-ops mgmt bootstrap --name=shard-eu-2 --region=hel1
      """
    Then within 20 minutes the command exits with success
    And the Platform Console shows two active fleet shards: shard-eu-1 and shard-eu-2
    And subsequent Enterprise tenant provisioning requests can be routed to shard-eu-2
```

### TC-BOOT-03: Unreachable shard is not offered for new tenant provisioning

```gherkin
Feature: Fleet routing excludes unhealthy shards
  Background:
    Given shard-eu-1 and shard-eu-2 are both active

  Scenario: New tenant provisioning avoids a shard whose management cluster is unreachable
    Given shard-eu-1 has been unreachable for more than 90 seconds
    When a new Enterprise tenant provisioning request is submitted
    Then the environment is provisioned on shard-eu-2
    And the Platform Console shows shard-eu-1 in a degraded or unknown status
    And the new tenant's environment shows shard-eu-2 as its host shard in the console
```

### TC-BOOT-04: Shard recovery restores normal routing after reconnection

```gherkin
Feature: Fleet self-heals when a shard reconnects
  Background:
    Given shard-eu-1 is in a degraded state after an outage

  Scenario: Shard resumes accepting provisioning after recovery
    When shard-eu-1's management cluster becomes reachable again
    Then within 2 minutes the Platform Console shows shard-eu-1 as active again
    And the next Enterprise tenant provisioning request may be routed to shard-eu-1
```

---

## AUTH — Authentication & Access Control

### TC-AUTH-01: New user logs in to the Platform Console via browser

```gherkin
Feature: User authentication via Platform Console
  Background:
    Given alice@acme-corp.com has been invited as tenant admin for acme-corp
    And alice has not yet logged in

  Scenario: Alice logs in for the first time and lands on her tenant dashboard
    When alice navigates to https://console.nutgraf.in
    And completes the login flow with her email and password
    Then she is redirected to the Environments tab scoped to acme-corp
    And she sees only acme-corp's environments, not any other tenant's
    And the console navigation shows Team, Billing, Monitoring, Runbooks, and Settings tabs
```

### TC-AUTH-02: Goose triggers device auth when session is missing

```gherkin
Feature: Headless agent authentication via device auth
  Background:
    Given Goose is running with no active session

  Scenario: Goose prompts the user with a device code and completes auth automatically
    When the user types: "Onboard Acme Corp on the enterprise plan in eu-central-1"
    Then Goose displays a device code and the URL https://auth.nutgraf.in/device
    And Goose does not proceed with the onboarding action yet
    When the user opens the URL in a browser and completes login
    Then Goose automatically retries the onboarding intent without the user retyping the command
    And the onboarding flow proceeds to the next step
```

### TC-AUTH-03: Device auth code expiry is handled transparently

```gherkin
Feature: Device auth code refresh on expiry
  Background:
    Given Goose has displayed a device code and is waiting for login

  Scenario: Expired device code is replaced with a new one without user error
    When 5 minutes pass without the user completing login
    Then Goose displays a new device code and a new device auth URL
    And Goose does not show an error or require a restart
    And when the user completes login with the new code, the original intent resumes
```

### TC-AUTH-04: Unauthenticated user cannot access console or API

```gherkin
Feature: Unauthenticated access is denied
  Scenario: Unauthenticated browser request to the console redirects to login
    When an unauthenticated user navigates to https://console.nutgraf.in/environments
    Then they are redirected to the login page
    And no environment data is shown before login completes

  Scenario: Unauthenticated API call returns 401
    When an HTTP request is made to zero-ops-api without an Authorization header
    Then the response is HTTP 401 Unauthorized
    And no data is returned
```

### TC-AUTH-05: Tenant user cannot see or modify another tenant's environments

```gherkin
Feature: Cross-tenant access is denied
  Background:
    Given alice is logged in as tenant_admin of acme-corp
    And rival-corp has a running environment rival-corp-production

  Scenario: Alice cannot navigate to rival-corp's environment in the console
    When alice attempts to navigate to the console URL for rival-corp-production
    Then the console shows an access denied message or redirects to her own dashboard
    And rival-corp-production data is never displayed to alice

  Scenario: Alice cannot call the API for rival-corp's resources
    When alice makes an API call referencing rival-corp-production
    Then the response is HTTP 403 Forbidden
    And no rival-corp data is returned
```

### TC-AUTH-06: Platform admin can see all tenants; tenant admin sees only their own

```gherkin
Feature: RBAC-scoped console views
  Background:
    Given carol is a platform admin
    And alice is a tenant admin of acme-corp

  Scenario: Platform admin console shows all tenants
    When carol navigates to the Environments tab
    Then she sees environments from all tenants including acme-corp and rival-corp
    And she can filter by tenant, region, and shard

  Scenario: Tenant admin console shows only their own environments
    When alice navigates to the Environments tab
    Then she sees only environments belonging to acme-corp
    And no other tenant's environment names, regions, or statuses are visible
```

### TC-AUTH-07: Tenant user role cannot approve destructive tickets

```gherkin
Feature: Approval gate requires tenant_admin role
  Background:
    Given bob is a tenant_user (not tenant_admin) of acme-corp
    And a destructive operation ticket is pending approval for acme-corp-production

  Scenario: Tenant user cannot approve a destructive operation ticket
    When bob logs in and navigates to the Approval Queue in the console
    Then the Approve button is disabled or absent for bob
    And attempting to approve via the API returns HTTP 403
    And the ticket status remains pending
```

---

## ONB — Tenant Onboarding

### TC-ONB-01: Goose-driven Enterprise onboarding — full journey

```gherkin
Feature: Agentic Enterprise tenant onboarding
  Background:
    Given the platform is running with at least one active fleet shard
    And no tenant named acme-corp exists

  Scenario: A new SaaS company onboards via Goose and gets a ready environment
    Given Goose is started with no active session
    When the user types: "Onboard Acme Corp on the enterprise plan in eu-central-1"
    Then Goose prompts for device auth and the user completes browser login
    And Goose confirms the onboarding intent has been accepted
    And Goose prompts the user to enter their Hetzner API token at https://console.nutgraf.in/settings/credentials
    When the user enters their Hetzner API token in the console
    Then Goose confirms credential acceptance and reports provisioning has started
    And within 15 minutes Goose displays a success summary containing:
      | Item                                  |
      | Kubernetes cluster API endpoint       |
      | PostgreSQL Secret name (not value)    |
      | ArgoCD dashboard URL                  |
      | Grafana tenant dashboard URL          |
    And the Platform Console shows acme-corp-production with status Ready
    And the cluster endpoint is reachable via kubectl with the provided kubeconfig
    And the ArgoCD dashboard URL is accessible in a browser
    And the Grafana dashboard URL is accessible and scoped to acme-corp metrics
```

### TC-ONB-02: Hetzner credentials are never exposed in Goose output

```gherkin
Feature: Credential security in agentic onboarding
  Scenario: The Hetzner API token never appears in any Goose message
    Given the onboarding flow has reached the credential step
    When the user enters their Hetzner API token in the console
    Then at no point does the Hetzner API token value appear in any Goose output
    And at no point does it appear in the Goose session log
    And Goose's credential prompt directs the user to the console only
```

### TC-ONB-03: Onboarding fails gracefully when Hetzner quota is exceeded

```gherkin
Feature: Provisioning failure surfaces to the user
  Scenario: Tenant sees a clear error when their Hetzner quota is exceeded
    Given a tenant's Hetzner account has no remaining quota in eu-central-1
    When the tenant initiates Enterprise provisioning in eu-central-1
    Then within 15 minutes the Platform Console shows:
      "Provisioning Failed: Hetzner quota exceeded in eu-central-1"
    And Goose surfaces the same error message
    And the console suggests: request a quota increase or choose a different region
    And no partial environment is left visible in the console as "Ready"
    And the tenant Hetzner account shows no orphaned VMs or volumes
```

### TC-ONB-04: Duplicate tenant name is rejected with a clear message

```gherkin
Feature: Tenant name uniqueness enforcement
  Background:
    Given a tenant named acme-corp already exists and has a running environment

  Scenario: Attempting to create a second tenant with the same name fails clearly
    When the user asks Goose: "Onboard Acme Corp on the starter plan"
    Then Goose reports that the tenant name acme-corp is already taken
    And Goose suggests alternative name options
    And the existing acme-corp tenant's environment is unaffected
```

### TC-ONB-05: Console-based Starter onboarding — cost shown before confirmation

```gherkin
Feature: Starter tenant onboarding via Platform Console
  Background:
    Given a user is logged in to the console as a new tenant admin

  Scenario: Tenant sees real-time cost estimate before confirming Starter provisioning
    When the tenant admin selects "New Environment → Starter"
    Then the console displays a monthly cost estimate in dollars before any confirmation step
    And the estimate reflects current Hetzner pricing
    When the tenant admin confirms provisioning
    Then within 60 seconds the console shows the environment status as Ready
    And the tenant can access their PostgREST API endpoint
    And the tenant can access their ArgoCD application
    And the tenant can access their Grafana dashboard scoped to their metrics
    And the agent conversation interface is available in the console
```

### TC-ONB-06: Starter tenant environment is ready in under 60 seconds

```gherkin
Feature: Starter provisioning speed
  Scenario: Starter environment reaches Ready without provisioning new VMs
    Given a Starter tenant provisioning request is confirmed
    When provisioning begins
    Then the environment status in the Platform Console reaches Ready within 60 seconds
    And no new virtual machines appear in the Zero-Ops Hetzner account during this window
```

---

## PROV — AINativeSaaS Provisioning

### TC-PROV-01: Enterprise environment has all baseline services reachable after provisioning

```gherkin
Feature: Enterprise environment baseline service availability
  Background:
    Given acme-corp-production has been provisioned and shows Ready in the console

  Scenario: All baseline services are reachable from the tenant's perspective
    When a tenant admin inspects their environment from the console
    Then the following endpoints are reachable:
      | Endpoint                    | Verification                           |
      | Kubernetes API endpoint     | kubectl get nodes returns Ready nodes  |
      | ArgoCD dashboard            | HTTPS 200, scoped to acme-corp apps    |
      | Grafana dashboard           | HTTPS 200, pre-filtered to acme-corp   |
      | PostgREST API               | HTTP 200 on health check               |
      | LiteLLM AI Gateway          | HTTP 200 on health check               |
    And the PostgreSQL connection string Secret is listed by name (not value) in the console
    And HTTPS is valid on all endpoints with a trusted TLS certificate
```

### TC-PROV-02: Enterprise environment metrics appear in Grafana within 5 minutes of Ready

```gherkin
Feature: Observability available after provisioning
  Scenario: Tenant can see cluster metrics in Grafana within 5 minutes of environment Ready
    Given acme-corp-production just reached Ready status
    When 5 minutes elapse
    And the tenant admin opens their Grafana dashboard via the console
    Then the Grafana dashboard shows live CPU, memory, and node metrics for acme-corp-production
    And the metrics are not empty or zero
    And the metrics are not showing data from any other tenant
```

### TC-PROV-03: Tenant cannot push commits directly to their control plane repository

```gherkin
Feature: Control plane repository is Zero-Ops owned
  Background:
    Given acme-corp has an Enterprise environment

  Scenario: Tenant admin cannot push commits to the control plane repository directly
    When the tenant admin attempts to push a commit to the control plane repository using their own Git credentials
    Then the push is rejected with a permission denied error
    And the tenant admin can read (clone) the repository contents
    And only Zero-Ops tooling can commit to the repository
```

### TC-PROV-04: Secret values are never exposed in the console or API responses

```gherkin
Feature: Secret values are never displayed to users
  Scenario: PostgreSQL password and cloud credentials are never shown in plain text
    Given acme-corp's environment is provisioned and the tenant admin is logged in
    When the tenant admin views the environment details in the console
    Then the PostgreSQL connection string shows only the Secret name, not the password value
    And the Hetzner API token field in Settings shows only a masked placeholder
    And no API response from zero-ops-api contains any plaintext credential value
```

### TC-PROV-05: Tenant environment continues operating when platform control plane is unavailable

```gherkin
Feature: Tenant runtime independence from management cluster
  Background:
    Given acme-corp-production is running and serving HTTP traffic
    And a synthetic load test is running against the tenant's application endpoint

  Scenario: Tenant workload is unaffected by a 30-minute management cluster outage
    When the Zero-Ops management cluster becomes completely unavailable
    Then the load test continues with no increase in error rate
    And the tenant application endpoint remains reachable throughout the 30-minute window
    And the tenant's database remains operational
    When the management cluster recovers
    Then the Platform Console becomes accessible again
    And the console shows acme-corp-production in the same state as before the outage
    And no manual action is required by the tenant or platform team
```

---

## UPG — Tier Upgrade

### TC-UPG-01: Starter to Enterprise upgrade completes with no downtime

```gherkin
Feature: Starter to Enterprise tier upgrade
  Background:
    Given acme-corp has a running Starter environment with application data in the database
    And a synthetic load test is running against the acme-corp application endpoint

  Scenario: Tenant upgrades to Enterprise and the application stays online throughout
    When the tenant admin selects "Upgrade to Enterprise" in the Platform Console
    And the console displays the cost difference and estimated 5–15 minute migration window
    And the tenant admin confirms the upgrade
    Then the load test shows no increase in error rate during the entire migration
    And within 15 minutes the console shows acme-corp's environment as Enterprise tier
    And the new dedicated cluster endpoint is reachable
    And all application data from the Starter database is present and queryable on the new cluster
    And the old Starter namespace is shown in the console as "pending cleanup" during the rollback window
    And after the rollback window the console shows no remnant of the Starter environment
```

### TC-UPG-02: Upgrade rollback within rollback window restores Starter state

```gherkin
Feature: Upgrade rollback
  Background:
    Given the Starter to Enterprise blue-green migration has completed DNS cutover
    And the rollback window has not expired

  Scenario: Tenant rolls back to Starter after detecting an issue
    When the tenant admin clicks "Rollback to Starter" in the Platform Console
    Then within 5 minutes the application endpoint resolves back to the Starter environment
    And the console shows acme-corp on Starter tier
    And the Enterprise cluster is no longer visible in the console
    And application data is intact on the Starter database
```

### TC-UPG-03: Rollback option is not available after the rollback window expires

```gherkin
Feature: Rollback window expiry
  Scenario: Rollback button disappears after the rollback window expires
    Given the Starter to Enterprise upgrade completed 3 hours ago
    And the rollback window is configured to 2 hours
    When the tenant admin views the environment details in the console
    Then no "Rollback to Starter" option is visible or accessible
    And the old Starter namespace is no longer listed anywhere in the console
```

---

## PRE — PR Ephemeral Environments

### TC-PRE-01: PR environment is created and accessible when a branch is opened

```gherkin
Feature: PR ephemeral environment lifecycle — creation
  Background:
    Given acme-corp has a running Enterprise environment
    And the tenant application repository is connected to Zero-Ops

  Scenario: Developer opens a PR and gets a live preview environment
    When the developer opens a pull request on branch feature/new-ai-pipeline
    Then within 2 minutes a GitHub status check appears on the PR with a preview URL
    And the preview URL is reachable via HTTPS
    And the preview environment shows the application built from the feature/new-ai-pipeline branch image
    And the preview environment has its own isolated database (changes do not affect staging)
```

### TC-PRE-02: Two concurrent PR environments are fully isolated from each other

```gherkin
Feature: PR environment database isolation
  Background:
    Given PR environments pr-branch-a and pr-branch-b are both running for acme-corp

  Scenario: A schema migration in one PR branch does not affect the other
    When the developer in pr-branch-a runs a destructive schema migration against their database
    Then the database in pr-branch-b shows no schema changes
    And the pr-branch-b application continues operating normally
    And no data from pr-branch-a is visible in pr-branch-b
```

### TC-PRE-03: PR environment is fully removed when the PR is closed

```gherkin
Feature: PR ephemeral environment lifecycle — teardown
  Scenario: Closing a PR removes the preview environment completely
    Given the PR environment for feature/new-ai-pipeline is running and reachable
    When the developer closes or merges the pull request
    Then within 2 minutes the preview URL returns HTTP 404 or connection refused
    And the GitHub status check on the PR is updated to reflect the environment is gone
    And no resources for this PR environment appear in the tenant's Hetzner account
```

### TC-PRE-04: PR environment falls back gracefully when no staging snapshot exists

```gherkin
Feature: PR environment graceful fallback
  Scenario: PR environment is still created even when no staging database snapshot exists
    Given no staging CSI snapshot exists for acme-corp
    When a developer opens a pull request
    Then a PR environment is still created and reachable via HTTPS
    And a warning appears on the GitHub PR status check:
      "PR environment using empty database — no staging data available."
    And the application starts up without errors using the empty database
```

---

## D2 — Day-2 Operations

### TC-D2-01: CNPG scale-up via control plane repository PR takes effect without downtime

```gherkin
Feature: Day-2 CNPG scaling via GitOps
  Background:
    Given acme-corp has an Enterprise environment with 3 CNPG instances
    And the application is serving live traffic

  Scenario: Tenant admin scales PostgreSQL from 3 to 5 instances with no downtime
    When the tenant admin opens and merges a PR to the control plane repository
    increasing spec.database.instances from 3 to 5
    Then within 15 minutes the console shows 5 healthy PostgreSQL instances for acme-corp
    And the application endpoint remains reachable throughout the scale operation
    And no database connections are dropped during the scale-up
```

### TC-D2-02: New team member is added and can log in with correct access level

```gherkin
Feature: Team management
  Background:
    Given alice is the tenant admin of acme-corp

  Scenario: Alice invites bob as a team member and bob can log in with read-only access
    When alice navigates to the Team tab and invites bob@acme-corp.com as a tenant_user
    Then bob receives an invitation email
    When bob follows the invitation link and completes account setup
    Then bob can log in to the Platform Console
    And bob can view acme-corp's environments, monitoring, and agent conversation
    And bob cannot create or delete environments
    And bob cannot approve destructive operation tickets
    And bob cannot access any other tenant's environments
```

### TC-D2-03: Tenant uploads a runbook and the agent references it in diagnosis

```gherkin
Feature: Tenant-scoped runbook management
  Background:
    Given acme-corp has an Enterprise environment

  Scenario: Agent uses a tenant-uploaded runbook when diagnosing an acme-corp alert
    When the tenant admin uploads a custom runbook "vector-db-recovery.md" via the Runbooks tab
    And a VictoriaMetrics alert fires for acme-corp's vector database
    Then the DiagnosticsAgent's response in the Platform Console references guidance from "vector-db-recovery.md"
    And the agent's proposed remediation reflects the content of the tenant's uploaded runbook
    And no other tenant sees this runbook referenced in their own agent interactions
```

### TC-D2-04: Grafana "View in Grafana" link opens tenant-scoped dashboard

```gherkin
Feature: Grafana dashboard access via console
  Scenario: Tenant user opens Grafana and sees only their own metrics
    Given a tenant user is viewing the Monitoring tab for acme-corp-production
    When they click "View in Grafana"
    Then a new browser tab opens showing the Grafana dashboard
    And the dashboard displays metrics for acme-corp-production only
    And no other tenant's cluster metrics are visible in the dashboard
    And the dashboard requires no additional login
```

### TC-D2-05: Agent conversation persists context between sessions

```gherkin
Feature: Agent memory across sessions
  Background:
    Given alice has had a prior conversation with the agent about acme-corp-production CNPG alerts

  Scenario: Agent recalls previous conversation context in a new session
    When alice starts a new agent conversation session in the Platform Console
    And types: "Any updates on the database issue we discussed?"
    Then the agent responds with context referencing the prior conversation about CNPG alerts
    And the response does not require alice to re-explain the prior context
    And no context from any other tenant's conversations appears in the agent's response
```

---

## OBS — Fleet Observability (User-Surfaced)

### TC-OBS-01: Alert fires and is visible in the Platform Console within 2 minutes

```gherkin
Feature: Alert surfacing to tenant console
  Background:
    Given acme-corp-production is running normally

  Scenario: A high CPU condition triggers a visible alert in the tenant's console
    When the tenant's cluster CPU exceeds 85% for 10 continuous minutes
    Then within 2 minutes an alert appears in the acme-corp tenant console
    And the alert shows the affected environment name and a human-readable description
    And a "Resolve in Cursor" button is visible on the alert
```

### TC-OBS-02: K8sGPT findings appear in the agent conversation interface

```gherkin
Feature: K8sGPT findings surfaced to tenant
  Scenario: K8sGPT finding appears in the agent conversation without the tenant asking
    Given a K8sGPT finding is raised for a misconfigured deployment in acme-corp-production
    When the tenant admin views the Agent Conversation tab in the console
    Then the finding is surfaced proactively in the conversation feed
    And the finding includes the affected resource name and a plain-language description
    And a suggested next step is offered
```

### TC-OBS-03: Agent answers "what changed in the last hour" using the event timeline

```gherkin
Feature: Event timeline accessible to tenant via agent
  Scenario: Tenant asks the agent about recent changes and receives the event timeline
    Given several infrastructure events have occurred on acme-corp-production in the last hour
    When the tenant admin types in the agent conversation:
      "What changed on acme-corp-production in the last hour?"
    Then the agent responds with a list of infrastructure events from the past hour
    And the events include ArgoCD syncs, workflow completions, and K8sGPT findings
    And the events are scoped to acme-corp only
    And the timestamps and descriptions are human-readable
```

### TC-OBS-04: CNPG backup status is visible in the console

```gherkin
Feature: Automated CNPG backup confirmation
  Background:
    Given acme-corp has an Enterprise environment

  Scenario: Tenant can confirm that automated backups are running from the console
    When the tenant admin views the environment details in the console
    Then the console shows the timestamp of the last successful CNPG backup
    And the next scheduled backup time is shown
    And the backup status shows "Completed" for the most recent run
```

---

## AGT — Multi-Agent Flows

### TC-AGT-01: Agent diagnoses a cluster alert and surfaces a remediation proposal to the console

```gherkin
Feature: End-to-end agent diagnosis and surfacing
  Background:
    Given autopilot mode is disabled for acme-corp
    And VictoriaMetrics detects PgBouncer connection pool saturation at 85% for 20 minutes

  Scenario: Agent diagnoses the issue and surfaces a remediation proposal to the console
    When the alert fires
    Then within 5 minutes the Platform Console shows an alert for acme-corp-production
    And the alert includes:
      | Field                                   |
      | Human-readable description of the issue |
      | A proposed remediation action           |
      | A link to the relevant runbook          |
      | A "Resolve in Cursor" button            |
    And the proposed remediation matches the guidance in the platform SOP runbook for this alert type
```

### TC-AGT-02: Agent escalates to human when no runbook matches

```gherkin
Feature: Agent escalation when runbook is absent
  Scenario: Agent surfaces a manual investigation alert when no matching runbook exists
    Given a novel alert fires for which no platform or tenant runbook exists
    When the alert is processed
    Then within 5 minutes the Platform Console shows an alert for the affected environment
    And the alert message states no automated remediation was found
    And the alert includes a "Resolve in Cursor" button with full infrastructure context
    And no automated remediation PR or action is taken
```

### TC-AGT-03: Agent surfaces correlated root cause from a catalog push to a regional outage

```gherkin
Feature: Agent cross-cluster root cause correlation
  Background:
    Given a new catalog OCI artifact was pushed at 14:28 containing a Cilium upgrade
    And at 14:32 five clusters in eu-central-1 show degraded status in the console

  Scenario: Agent surfaces the catalog push as the correlated cause
    When the Collaborator Agent processes the degradation alerts
    Then within 10 minutes the Platform Console shows an incident summary for the affected clusters
    And the summary identifies the Cilium upgrade catalog push at 14:28 as the correlated event
    And the summary proposes a rollback intent for the on-call engineer's review
    And a "Resolve in Cursor" button is shown with the rollback context pre-loaded
```

---

## APL — Autopilot Mode

### TC-APL-01: Autopilot enabled — agent proposes a change via PR, does not act directly

```gherkin
Feature: Autopilot PR proposal
  Background:
    Given autopilot mode is enabled for acme-corp
    And PgBouncer saturation has been at 85% for 20 minutes

  Scenario: Agent raises a PR for the tenant admin to approve, does not change infrastructure silently
    When the agent determines a PgBouncer pool size increase is needed
    Then within 5 minutes a pull request appears in the tenant control plane repository
    And the PR title describes the proposed change in plain language
    And the PR description includes the metric evidence and runbook reference
    And the Platform Console Approval Queue shows a new pending item linked to the PR
    And the PgBouncer pool size is unchanged until the tenant approves the PR
```

### TC-APL-02: Approved autopilot PR results in the change being applied

```gherkin
Feature: Autopilot PR approval applies the change
  Background:
    Given an autopilot PR increasing PgBouncer pool size exists for acme-corp
    And the PgBouncer saturation alert is still active

  Scenario: Tenant admin approves the PR and the change takes effect
    When the tenant admin approves and merges the PR
    Then within 10 minutes the PgBouncer pool size in the environment reflects the new value
    And within 15 minutes the PgBouncer saturation metric drops below the alert threshold
    And the alert in the Platform Console resolves automatically
    And the Approval Queue item is marked as completed
```

### TC-APL-03: Rejected autopilot PR results in no change to infrastructure

```gherkin
Feature: Autopilot PR rejection leaves infrastructure unchanged
  Scenario: Tenant admin rejects the PR and nothing changes
    Given an autopilot PR for PgBouncer pool increase exists for acme-corp
    When the tenant admin closes the PR without merging
    Then the PgBouncer pool size remains at its previous value
    And no automated change is made to the infrastructure
    And the Approval Queue item is marked as rejected
```

### TC-APL-04: Autopilot disabled — alert appears in console with Resolve in Cursor only

```gherkin
Feature: Autopilot disabled — console alert flow
  Background:
    Given autopilot mode is disabled for acme-corp (default)
    And PgBouncer saturation alert fires for acme-corp-production

  Scenario: With autopilot off, the agent shows an alert but creates no PR
    When the agent processes the alert
    Then the Platform Console shows an alert for acme-corp-production
    And a "Resolve in Cursor" button is visible
    And no pull request appears in the tenant control plane repository
```

### TC-APL-05: CNPG cluster deletion always requires explicit human approval regardless of autopilot mode

```gherkin
Feature: Autopilot safety invariant — destructive operations always require human approval
  Background:
    Given autopilot mode is enabled for acme-corp

  Scenario: Agent cannot autopilot-delete a CNPG cluster
    When an agent scenario determines that CNPG cluster deletion is the correct action
    Then no PR is automatically created for this action
    And a CNPG ticket appears in the Platform Console Approval Queue requiring explicit human review
    And the CNPG cluster is untouched until a human approves the ticket
    And the ticket displays a clear warning that this action is destructive and irreversible

  Scenario: Provider migration always requires human-approved PR regardless of autopilot setting
    When a provider migration is needed
    Then the Platform Console shows a pending PR awaiting human approval
    And the tenant's infrastructure on the current provider remains unchanged
    And no new cloud resources are provisioned until the tenant merges the PR
```

---

## MIG — Provider Migration

### TC-MIG-01: Tenant migrates from Hetzner to another provider with no data loss

```gherkin
Feature: Blue-green provider migration
  Background:
    Given acme-corp has a running Enterprise environment on Hetzner
    And the database contains application data
    And a synthetic load test is running

  Scenario: Full provider migration completes with no data loss and service continuity
    When the tenant admin selects "Migrate Provider → AWS" in the Platform Console
    And provides AWS credentials via the credentials form
    And approves the migration PR created by the platform
    Then within 30 minutes the Platform Console shows acme-corp-production as running on AWS
    And the load test shows no increase in error rate during the cutover
    And all application data that existed on Hetzner is present and queryable on the AWS cluster
    And the tenant's application endpoint resolves to the AWS cluster after cutover
    And the console shows the Hetzner cluster as "retained for rollback" with an expiry timestamp
```

### TC-MIG-02: Provider migration rollback within the window restores the original provider

```gherkin
Feature: Provider migration rollback
  Background:
    Given the AWS migration has completed and the rollback window is still active

  Scenario: Tenant rolls back to Hetzner without data loss
    When the tenant admin clicks "Rollback to Hetzner" in the Platform Console
    Then within 5 minutes the application endpoint resolves back to the Hetzner cluster
    And the console shows acme-corp-production as running on Hetzner
    And the AWS cluster is no longer listed in the console
    And all application data is intact on the Hetzner database

  Scenario: Cloud credentials entered during migration are never visible in console or Goose
    When the tenant admin completes the migration flow
    Then at no point do the cloud credentials appear in any Goose message, console display, or API response
```

### TC-MIG-03: Rollback is unavailable after the window expires

```gherkin
Feature: Provider migration rollback window expiry
  Scenario: Rollback option disappears after the configured rollback window expires
    Given the provider migration to AWS completed 3 hours ago
    And the rollback window is 2 hours
    When the tenant admin views the console
    Then no "Rollback to Hetzner" option is visible
    And the Hetzner cluster is no longer listed in the console
```

---

## SEC — Security Boundaries

### TC-SEC-01: Platform team cannot see tenant application data through any console feature

```gherkin
Feature: Data visibility boundary — platform team vs tenant data
  Background:
    Given acme-corp's data plane database contains sensitive application records

  Scenario: Platform admin can see infrastructure health but not application data
    When a platform admin views acme-corp-production in the console
    Then they can see cluster CPU, memory, CNPG cluster status, and ArgoCD sync state
    And they cannot see database row contents, tenant application logs, or tenant end-user records
    And no console feature exposes a query interface into the tenant data plane database
    And the agent conversation interface for the platform admin does not surface tenant application data
```

### TC-SEC-02: Starter tier tenants cannot reach each other's application endpoints

```gherkin
Feature: Starter tier network isolation
  Background:
    Given acme-corp and rival-corp both have Starter environments in the shared cluster

  Scenario: acme-corp application cannot reach rival-corp application over the cluster network
    When an acme-corp application pod attempts an HTTP request to rival-corp's cluster-internal service
    Then the request times out or is refused
    And rival-corp's application receives no traffic from acme-corp

  Scenario: acme-corp cannot read rival-corp's database rows via the shared database instance
    When the acme-corp application queries its database using its own credentials
    Then the query returns only rows belonging to acme-corp
    And no rival-corp rows are returned even if the SQL query contains no explicit tenant filter
```

### TC-SEC-03: All secrets in the control plane repository are encrypted — no plaintext in Git

```gherkin
Feature: Secret management — GitOps secret security
  Background:
    Given acme-corp's control plane repository has been set up with KSOPS

  Scenario: Browsing the repository reveals no plaintext secret values
    When the tenant admin or a reviewer browses the control plane repository file contents
    Then no file contains plaintext API keys, database passwords, or cloud credentials
    And the ArgoCD sync still succeeds in deploying the encrypted secrets to the tenant cluster
    And the deployed Kubernetes Secrets in the cluster contain the correct decrypted values

  Scenario: A newly committed secret is encrypted before it reaches Git
    When zero-ops-api adds a new credential to the tenant control plane repository
    Then the committed file contains only the encrypted form of the credential
    And no plaintext value appears in the Git diff or commit history
```

---

## DES — Destructive Operations & Human Approval

### TC-DES-01: Destructive operation creates an approval ticket and waits for human sign-off

```gherkin
Feature: Human approval gate for destructive operations
  Background:
    Given VictoriaMetrics detects DiskPressure on a node in acme-corp-production
    And the agent has determined node drain is the correct remediation

  Scenario: Node drain does not execute until a human approves the ticket
    When the agent completes its diagnosis
    Then a ticket appears in the Platform Console Approval Queue for acme-corp-production
    And the ticket includes the affected node, cluster name, proposed action, and runbook reference
    And a "Resolve in Cursor" button is visible on the ticket
    And at this point no node has been drained or cordoned
    And the Argo Workflow is shown as paused in the console
```

### TC-DES-02: Approved destructive operation executes and the environment recovers

```gherkin
Feature: Destructive operation execution after approval
  Scenario: On-call engineer approves the ticket and the node is safely recycled
    Given the node drain approval ticket exists in the Approval Queue
    When the on-call engineer approves the ticket in the console (or via Cursor)
    Then the node is drained without disrupting running workloads
    And a replacement node joins the cluster
    And within 15 minutes the Platform Console shows all nodes healthy for acme-corp-production
    And the alert that triggered the node drain resolves
    And the ticket is marked as resolved in the Approval Queue
```

### TC-DES-03: Rejected destructive operation leaves the environment unchanged

```gherkin
Feature: Destructive operation rejection
  Scenario: Engineer rejects the ticket and the node is left as-is
    Given a node drain ticket is pending in the Approval Queue
    When the on-call engineer rejects the ticket
    Then the node is not drained
    And the ticket is marked as rejected in the Approval Queue
    And the environment continues operating in its current state
    And no partial node recycle operation is left in progress
```

### TC-DES-04: Resolve in Cursor opens Cursor with the full infrastructure context

```gherkin
Feature: Resolve in Cursor — MCP context handoff
  Scenario: Clicking Resolve in Cursor opens Cursor with the alert context pre-loaded
    Given a destructive operation ticket is in the Approval Queue
    When the on-call engineer clicks "Resolve in Cursor"
    Then Cursor opens (or a deep-link URL is shown if Cursor is not installed)
    And Cursor's agent conversation is pre-populated with the infrastructure context from the ticket
    And the engineer can approve or reject the pending workflow from within Cursor
```

---

## HA — High Availability

### TC-HA-01: Tenant application serves traffic during a full management cluster outage

```gherkin
Feature: Tenant runtime independence
  Background:
    Given acme-corp-production is running and a load test is active against its endpoint

  Scenario: Tenant workload is unaffected by a 30-minute management cluster outage
    When the Zero-Ops management cluster is shut down completely
    Then the load test error rate remains at its baseline with no increase
    And the tenant's application endpoint continues returning HTTP 200
    And the tenant's database continues accepting queries
    When the management cluster is restored after 30 minutes
    Then the Platform Console becomes accessible
    And acme-corp-production shows the same health status as before the outage
    And no tenant action was required during or after the outage
```

### TC-HA-02: Queued provisioning requests resume automatically after management cluster recovery

```gherkin
Feature: Provisioning queue self-heals after outage
  Background:
    Given a new Enterprise provisioning request was submitted while the management cluster was down

  Scenario: The queued provisioning request completes automatically after recovery
    When the management cluster recovers
    Then the queued provisioning request resumes without operator intervention
    And within 15 minutes of recovery the environment reaches Ready status in the console
    And no duplicate environments are created
```

---

## CLN — Decommission & Eject

### TC-CLN-01: Enterprise environment decommission removes all visible infrastructure

```gherkin
Feature: Enterprise environment decommission
  Background:
    Given acme-corp-production is a running Enterprise environment
    And a human-approved decommission request has been confirmed

  Scenario: All environment resources are removed and the console reflects decommissioned state
    When the decommission workflow completes
    Then the Platform Console shows acme-corp-production as Decommissioned
    And no virtual machines for this environment appear in the tenant's Hetzner account
    And the environment's endpoints (cluster API, ArgoCD, Grafana, PostgREST) are no longer reachable
    And CNPG backup objects in S3 are retained for the configured retention period (not immediately deleted)
    And the tenant admin can still view the decommissioned environment in console history (read-only)
```

### TC-CLN-02: Starter environment decommission does not affect other Starter tenants

```gherkin
Feature: Starter environment namespace cleanup
  Background:
    Given acme-corp and rival-corp both have Starter environments in the shared cluster

  Scenario: Decommissioning acme-corp Starter environment leaves rival-corp unaffected
    When the acme-corp Starter environment is decommissioned
    Then rival-corp's application endpoint remains reachable
    And rival-corp's database continues operating
    And rival-corp's console shows no change in their environment status
```

### TC-CLN-03: Tenant eject gives full ownership and removes Zero-Ops access

```gherkin
Feature: Tenant eject — no vendor lock-in
  Background:
    Given acme-corp has an Enterprise environment and wishes to self-manage it

  Scenario: Ejected tenant can manage their environment independently
    When the tenant admin initiates eject via Settings → Eject in the Platform Console
    And confirms the eject action
    Then the Platform Console shows acme-corp as Ejected status
    And acme-corp's running cluster, database, and services continue operating without interruption
    And the tenant admin receives documentation on managing their environment independently
    And zero-ops-api can no longer commit to the tenant control plane repository
    And the Platform Console no longer shows acme-corp in the active tenants list
    And the ejected tenant can push their own commits to the control plane repository without restriction
```

---

## CHAIN — Full Lifecycle Chain Scenarios

These scenarios exercise the complete system across multiple user journeys in sequence. They represent the most comprehensive E2E test and must pass as part of every release gate.

### TC-CHAIN-01: New tenant onboards, provisions, opens a PR, merges, PR env tears down

```gherkin
Feature: Full developer lifecycle — onboarding to PR merge
  Scenario: Complete new tenant journey from zero to merged PR
    Given no tenant named chain-test-corp exists
    When a user onboards chain-test-corp via Goose on the Enterprise plan
    And credentials are provided and the environment reaches Ready
    And a developer opens a PR on branch feature/test-feature
    Then a PR environment is created and reachable via HTTPS
    When the developer merges the PR
    Then the PR environment is torn down within 2 minutes of the merge
    And the preview URL returns HTTP 404 or connection refused
    And the main application environment is unchanged and still reachable
    And chain-test-corp shows exactly one environment in the console (the main environment)
```

### TC-CHAIN-02: Alert fires, agent diagnoses, autopilot PR raised, approved, metric resolves

```gherkin
Feature: Full agentic remediation chain with autopilot
  Background:
    Given autopilot mode is enabled for acme-corp
    And acme-corp-production is running with a monitored database

  Scenario: End-to-end alert-to-resolution chain produces visible outcomes at every step
    Given PgBouncer saturation exceeds 80% for 20 continuous minutes
    Then within 5 minutes a PR appears in the tenant control plane repository describing the proposed fix
    And the Approval Queue in the console shows the pending item
    When the tenant admin approves and merges the PR
    Then within 15 minutes the PgBouncer saturation metric drops below the alert threshold
    And the console alert resolves automatically
    And the Approval Queue item is marked completed
    And the agent conversation feed shows a summary of what was done and why
```

### TC-CHAIN-03: Full environment lifecycle — Starter → Enterprise → provider migration → decommission

```gherkin
Feature: Full environment lifecycle from start to finish
  Background:
    Given a new tenant corp-lifecycle-test starts on Starter tier

  Scenario: Environment progresses through the full platform lifecycle with no data loss at any stage
    Given corp-lifecycle-test has a running Starter environment with data in the database
    When the tenant upgrades to Enterprise
    Then the console shows Enterprise tier and data is intact
    When the tenant migrates from Hetzner to a second provider
    Then the console shows the new provider and data is intact
    When the tenant decommissions the environment after confirming
    Then the console shows Decommissioned status
    And no orphaned infrastructure remains in any cloud account
    And the complete lifecycle transition history is visible in the console environment history
```

---

## Test Coverage Matrix

| PRD Journey | E2E Test Cases |
|---|---|
| Journey A — Agentic Enterprise Onboarding | TC-AUTH-02, TC-AUTH-03, TC-ONB-01, TC-ONB-02, TC-ONB-03, TC-PROV-01, TC-PROV-02 |
| Journey B — Fleet Shard Bootstrap | TC-BOOT-01, TC-BOOT-02, TC-BOOT-03, TC-BOOT-04 |
| Journey C — Starter Onboarding | TC-ONB-04, TC-ONB-05, TC-ONB-06 |
| Journey D — Starter → Enterprise Upgrade | TC-UPG-01, TC-UPG-02, TC-UPG-03 |
| Journey E — PR Ephemeral Environments | TC-PRE-01, TC-PRE-02, TC-PRE-03, TC-PRE-04 |
| Journey F — Autopilot PR Approval | TC-APL-01, TC-APL-02, TC-APL-03, TC-APL-04, TC-APL-05 |
| Journey G — Provider Migration | TC-MIG-01, TC-MIG-02, TC-MIG-03 |
| Journey H — Destructive Operation Approval | TC-DES-01, TC-DES-02, TC-DES-03, TC-DES-04 |
| Day-2 Operations | TC-D2-01, TC-D2-02, TC-D2-03, TC-D2-04, TC-D2-05 |
| Auth & Access Control | TC-AUTH-01 through TC-AUTH-07 |
| Observability (user-surfaced) | TC-OBS-01, TC-OBS-02, TC-OBS-03, TC-OBS-04 |
| Agent Flows (user-surfaced outcomes) | TC-AGT-01, TC-AGT-02, TC-AGT-03 |
| Security Boundaries | TC-SEC-01, TC-SEC-02, TC-SEC-03 |
| HA & Resilience | TC-HA-01, TC-HA-02 |
| Decommission & Eject | TC-CLN-01, TC-CLN-02, TC-CLN-03 |
| Full Lifecycle Chains | TC-CHAIN-01, TC-CHAIN-02, TC-CHAIN-03 |

**Total: 59 E2E test cases + 3 full-lifecycle chain scenarios = 62 scenarios.**

---

## What Was Removed Versus v1.0 (and Why)

| Removed Test | Reason |
|---|---|
| TC-BOOT-03 (cnpg2monitor ClusterRole rules) | Asserts internal RBAC manifest content — unit/config test |
| TC-IDN-01 (Kratos identity trait schema) | Asserts internal Kratos API response fields — integration test |
| TC-IDN-03 (JWT claim field values) | Asserts token payload internals — integration test against Hydra |
| TC-IDN-04 (JWKS cache hit count) | Asserts internal AgentGateway behaviour — unit/performance test |
| TC-IDN-05 (MCP tool server has no auth logic) | Asserts code architecture, not user outcome — unit test |
| TC-PROV-04 (Age key S3 storage path) | Asserts internal secret storage path — integration test |
| TC-OBS-04/05 (PodMonitor annotation keys) | Asserts Kubernetes resource fields directly — integration test |
| TC-AGT-03 (ProvisioningAgent never calls capi-mcp) | Asserts internal agent routing via audit log — integration test |
| TC-XC-03 (cache call counts, pgvector write frequency) | Asserts internal system timing and call counts — performance/unit test |

All removed tests remain valid tests — they belong in the integration and unit test layers, not in E2E.

---

*Document Status: DRAFT — Strict E2E TDD Baseline for Zero-Ops v8.0*
*Total: 62 E2E scenarios (59 feature-level + 3 chain scenarios)*
*PRD Reference: zero-ops-prd-v8.md*
