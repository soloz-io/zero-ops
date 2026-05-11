# ADR 023: Unified Declarative Database Management

## Status
Accepted
*Supersedes: ADR-002 (Hub Operator Database Management)*

## Context
Previously (per ADR-002), the custom `hub-operator` contained imperative Go logic to execute database migrations (via `golang-migrate`) and provision database roles (via raw SQL queries) for the Hub cluster. Concurrently, the Spoke clusters utilized declarative operators (Atlas Operator and Crossplane `provider-sql`) to achieve the same goals.

This resulted in:
1. **Platform Asymmetry:** The Hub and Spokes managed stateful resources in entirely different ways.
2. **Split-Brain Conditions:** If Crossplane was introduced to the Hub, it would fight the `hub-operator` over role ownership.
3. **Operator Bloat:** The `hub-operator` was acting as an infrastructure script runner rather than a high-level business control plane.

## Decision
We will unify our database management strategy and enforce strict, declarative ownership boundaries across both Hub and Spoke environments.

1. **Remove Imperative Logic:** The `hub-operator` is stripped of all DDL (migrations) and DCL (roles/grants) responsibilities.
2. **Tri-State Ownership Contract:**
   - **CloudNativePG** strictly owns the physical cluster (Pods, PVCs, Replication, Superuser).
   - **Crossplane (`provider-sql`)** strictly owns logical databases, roles, and grants.
   - **Atlas Operator** strictly owns schema migrations and drift detection.
3. **Operator Scope:** The `hub-operator` will now exclusively manage external API orchestration (Infisical, Ory Hydra, NATS) and track high-level platform status (`Provisioning`, `Available`, `Degraded`, `Failed`) by observing the `Ready` conditions of Crossplane and Atlas resources.

## Consequences
* **Positive:** Symmetrical architecture across Hub and Spokes, reducing cognitive load for platform engineers.
* **Positive:** The `hub-operator` codebase becomes significantly lighter, safer, and focused purely on SaaS business logic.
* **Positive:** Eliminates race conditions between custom operators and standardized declarative tools.