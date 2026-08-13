# ADR 012: Declarative Billing Catalog Management via Operator Pattern

**Date:** 2026-05-01  
**Status:** Accepted  
**Authors:** Platform Engineering Team  

## Context

The Zero-Ops platform requires a production-grade billing and metering system to support multi-tenant SaaS applications. The system must manage two distinct categories of data:

The idea here is that the metering and usage metrics vary by each SAAS tenants. So the desigm must be architected in a way that these are configured or customizable for tenants and not hardcoded seperately in the PAAS platform. The subscriptions, plans, metrics are to be maintained along with tenants manifests so that its seperated and easily confiurable for tenants. The platform must provide infra in such a way that the feature and its billing are decided by the tenants and not hardocded by the infra. I want you to analyse the specs and suggest idiomatic enterpsie grade pattern so that it can be maintained along with tenant helm values in fleet registry or any other means.

1. **Static Billing Catalog**: Meters (usage metrics), Features (billable capabilities), and Plans (pricing tiers)
2. **Dynamic Runtime Data**: Users, Subjects, Subscriptions, Invoices, and Usage Queries

The platform integrates with OpenMeter (open-source usage metering and billing engine) as the backend system.

### The Core Question

**How should the platform manage the lifecycle of billing catalog resources (Meters, Features, Plans)?**

Billing catalog configuration has characteristics of infrastructure:
- Changes infrequently (pricing tiers, meter definitions)
- Requires audit trail for compliance
- Must be rollback-safe
- Affects revenue and legal obligations
- Needs version control

However, it could be managed either:
- **Imperatively**: Via REST API calls (like user registration)
- **Declaratively**: Via GitOps and Kubernetes Operators (like database provisioning)

### Industry Standard Pattern

Enterprise SaaS platforms (Stripe, AWS, Azure) separate billing catalog management from runtime operations:

- **Catalog Configuration**: Declarative, version-controlled, deployed via IaC/GitOps
- **Runtime Operations**: Imperative API calls for user subscriptions, usage tracking, invoicing

## Decision

We will manage billing catalog (Meters, Features, Plans) using **declarative Kubernetes Operators and GitOps**, not REST APIs.

### Architectural Separation

**Static Catalog (GitOps + Kubernetes Operators):**
- Meters, Features, Plans managed as Kubernetes Custom Resources
- Defined in tenant configuration files
- Reconciled by platform operators to OpenMeter
- Changes tracked in Git with full audit trail

**Dynamic Runtime Data (REST API):**
- User/Subject registration and deletion
- Subscription creation, updates, and cancellation
- Usage queries and entitlement checks
- Invoice operations

### Rationale

1. **GitOps Compliance**: Billing catalog is infrastructure configuration. All infrastructure in the platform is managed declaratively (databases, clusters, secrets). Billing catalog should follow the same pattern.

2. **Audit Trail**: Pricing changes have legal and compliance implications. Git provides immutable history of who changed what, when, and why.

3. **Rollback Safety**: Git revert instantly rolls back billing configuration changes. No need for complex compensating transactions.

4. **Separation of Concerns**: Clear boundary between infrastructure (GitOps) and runtime operations (REST API).

5. **Self-Healing**: Kubernetes operators continuously reconcile, fixing drift automatically.

6. **Idempotency Guarantee**: Kubernetes controllers are strictly idempotent by design.

## Ownership

| Resource Class | System of Record | Lifecycle Owner | Reconciler | Consumer | Phase |
|---|---|---|---|---|---|
| Billing Catalog | Git | Billing Operator | Billing Operator | OpenMeter, API | Day-1+ |

See ADR-039 for the complete ownership matrix.

## Consequences

### Positive

1. **GitOps Compliance**: All billing catalog changes tracked in Git with full audit trail
2. **Declarative Infrastructure**: Billing configuration follows same pattern as databases, clusters, secrets
3. **Self-Healing**: Operators continuously reconcile, fixing drift automatically
4. **Simplified Logic**: No need for complex compensating transactions for catalog mutations
5. **Rollback Safety**: Git revert instantly rolls back billing configuration changes
6. **Separation of Concerns**: Clear boundary between infrastructure (GitOps) and runtime (REST API)
7. **Idempotency Guarantee**: Kubernetes controllers are strictly idempotent by design
8. **Multi-Tenant Isolation**: Each tenant's billing config is namespaced and labeled

### Negative

1. **Learning Curve**: SaaS Builders must learn Kubernetes CR syntax instead of REST API
2. **Deployment Latency**: Billing catalog changes require Git commit → ArgoCD sync → Reconciliation (typically 1-3 minutes)
3. **Operator Complexity**: Requires maintaining CRDs and reconcilers in platform operator
4. **Limited Dynamic Updates**: Cannot change pricing plans via API calls (must go through Git)

### Mitigations

1. **Documentation**: Provide clear examples and templates in tenant configuration repository
2. **Validation Webhooks**: Add admission webhooks to validate CR syntax before apply
3. **Status Feedback**: CR Status.Conditions provide clear sync status and error messages
4. **Emergency Override**: For critical production issues, platform admins can manually patch CRs

## Alternatives Considered

### Alternative: Pure REST API
Manage billing catalog through REST API endpoints.

**Rejected because:**
- Violates GitOps principles (no version control)
- No audit trail for compliance
- Configuration drift risk
- Inconsistent with platform patterns (databases, clusters use operators)

## References

- **ADR 004**: Dual-Repo GitOps Pattern
- **ADR 005**: Unified Abstraction Layers in Crossplane
- **ADR 011**: Declarative Operator State over Imperative Jobs
- **OpenMeter Documentation**: https://openmeter.io/docs
- **Kubernetes Operator Pattern**: https://kubernetes.io/docs/concepts/extend-kubernetes/operator/
