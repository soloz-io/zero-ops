# ADR 021: Boundary-Driven GitOps and Day-0 Choreography

## Status
Accepted

## Context
Our initial GitOps deployment strategy relied on a monolithic "App-of-Apps" pattern, using ArgoCD `sync-wave` annotations (ranging from -2 to 11) to artificially sequence the deployment of infrastructure, databases, and services. 

This approach proved brittle:
1. **Global Deadlocks:** A transient failure in a high-level application could halt the synchronization of core infrastructure.
2. **Secret Zero Paradox:** The CLI attempted to inject fundamental trust roots (CA certificates, master keys) into a cluster that was simultaneously attempting to boot workloads that required those secrets, creating a race condition.
3. **Abuse of Sync-Waves:** Using sync-waves as a runtime orchestration engine violates the principles of Kubernetes Eventual Consistency.

## Decision
We will separate our deployment architecture into **Day-0 Imperative Choreography** and **Day-1+ Declarative Continuous Reconciliation**.

1. **Independent Boundaries:** We will split the monolithic App-of-Apps into mathematically independent ArgoCD applications:
   - `01-platform-infra`: Core controllers, Operators (Crossplane, CNPG, Atlas), and CRDs.
   - `02-platform-data`: Stateful workloads (CNPG Clusters, NATS, Redis).
   - `03-platform-services`: Secret Providers and Core Platform Services (Infisical, HubEnvironment, API Gateway).
   - `04-tenant-services`: Secret Consumers, Identity, and Billing Services (Ory, OpenMeter, Auth Proxy).
2. **Day-0 CLI Choreography:** The `hub` CLI will imperatively gate these boundaries during initial bootstrap:
   - Apply `01-platform-infra` and wait for webhooks.
   - Apply `02-platform-data` and wait for CNPG readiness.
   - Bootstap Secret Infrastructure and apply `03-platform-services`.
   - Bootstap Infisical API (establishing secret hierarchy).
   - Apply `04-tenant-services` (secret consumers).
   - The CLI will then exit permanently.
3. **Ban Application Sync-Waves:** We will purge `argocd.argoproj.io/sync-wave` annotations from all application workloads. Sync-waves are restricted exclusively to ordering CRDs before the operators that own them in the `01-platform-infra` boundary.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Kubernetes Resources | Git | ArgoCD | ArgoCD | Platform, Tenants | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences
* **Positive:** Complete blast-radius isolation. An application failure will no longer block infrastructure reconciliation.
* **Positive:** Secret Zero injection is deterministic and race-condition-free.
* **Positive:** Clear architectural boundary between Secret Providers (`03`) and Secret Consumers (`04`), eliminating circular dependencies during bootstrap.
* **Negative:** Applications in `04-tenant-services` will attempt to start before their databases in `02-platform-data` are fully provisioned, requiring robust internal retry logic (See ADR-022).