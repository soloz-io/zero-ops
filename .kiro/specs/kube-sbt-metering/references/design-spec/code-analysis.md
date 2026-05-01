Here is the **Technical Implementation Design Specification** based on the updated requirements. This document provides the engineering blueprint for refactoring the `open-sbt` codebase to support OpenMeter, Crossplane `provider-http`, Istio mTLS, and the Saga/DLQ pattern.

---

# Technical Design Specification: Kube-SBT Metering & Billing (OpenMeter)

## 1. Executive Summary
This document defines the technical implementation for completely replacing the legacy Postgres-backed metering and billing modules in `open-sbt` with a robust, CNCF-native integration using **OpenMeter**, **Stripe**, **Ory**, and **NATS JetStream**. `open-sbt` acts strictly as a Hub-based Backend-For-Frontend (BFF), orchestrating Tenant and User lifecycles while delegating all hot-path telemetry to an external AgentGateway and all quota enforcement to OpenMeter's Entitlements API.

## 2. Architectural Overview

### Hub Cluster (Control Plane)
*   **`open-sbt`**: Go-based API server (Gin framework). Deployed with an Istio Envoy sidecar for transparent mTLS.
*   **Ory Stack (Kratos, Hydra, Keto)**: Identity and AuthZ.
*   **OpenMeter**: Deployed via official Helm chart on `node-role.kubernetes.io/worker=true` nodes.
*   **NATS JetStream**: Event bus for choreography and Saga/DLQ patterns.
*   **Crossplane**: Uses `provider-http` to initialize OpenMeter namespaces.
*   **Infisical + ESO**: Securely injects Stripe Webhook secrets into `open-sbt`.

### Spoke Cluster (Application Plane)
*   **AgentGateway**: Rust binary. Emits OTLP metrics directly to the Hub's OpenMeter endpoint.
*   **PostgREST**: Serves tenant applications.
*   **Tenant DB**: PostgreSQL database holding `public.users` with Row-Level Security (RLS).

---

## 3. Interface Design (Go)

The existing `IMetering` and `IBilling` interfaces in `internal/opensbt/interfaces/` MUST be updated to reflect OpenMeter's capabilities.

### 3.1 `IMetering` Interface
```go
package interfaces

import (
	"context"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

type IMetering interface {
	// Meter & Feature Management
	CreateMeter(ctx context.Context, namespace string, meter models.MeterSpec) error
	CreateFeature(ctx context.Context, namespace string, feature models.FeatureSpec) error
	ListMeters(ctx context.Context, namespace string) ([]models.Meter, error)
	GetMeter(ctx context.Context, namespace, meterID string) (*models.Meter, error)
	UpdateMeter(ctx context.Context, namespace, meterID string, updates models.MeterUpdates) error
	DeleteMeter(ctx context.Context, namespace, meterID string) error

	// Plan & Rate Card Management
	CreatePlan(ctx context.Context, namespace string, plan models.PlanSpec) error
	ListPlans(ctx context.Context, namespace string) ([]models.Plan, error)
	GetPlan(ctx context.Context, namespace, planID string) (*models.Plan, error)
	UpdatePlan(ctx context.Context, namespace, planID string, updates models.PlanUpdates) error
	DeletePlan(ctx context.Context, namespace, planID string) error

	// Usage Queries
	GetUsage(ctx context.Context, namespace string, filter models.UsageFilter) (*models.UsageReport, error)
	
	// Entitlements
	CheckEntitlement(ctx context.Context, namespace, subjectID, featureKey string) (*models.EntitlementStatus, error)

	// Subject Management
	RegisterSubject(ctx context.Context, namespace string, subjectID string) error
	DeleteSubject(ctx context.Context, namespace string, subjectID string) error
}
```

### 3.2 `IBilling` Interface
```go
package interfaces

import (
	"context"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

type IBilling interface {
	// Subscriptions
	CreateSubscription(ctx context.Context, namespace, subjectID, planID string) error
	UpdateSubscription(ctx context.Context, namespace, subscriptionID string, updates models.SubscriptionUpdates) error
	CancelSubscription(ctx context.Context, namespace, subscriptionID string) error
	GetSubscription(ctx context.Context, namespace, subscriptionID string) (*models.Subscription, error)
	ListSubscriptions(ctx context.Context, namespace string) ([]models.Subscription, error)

	// Invoices
	PreviewInvoice(ctx context.Context, namespace, subjectID string) (*models.Invoice, error)
	GetInvoice(ctx context.Context, namespace, invoiceID string) (*models.Invoice, error)
	ListInvoices(ctx context.Context, namespace string, filters models.InvoiceFilters) ([]models.Invoice, error)

	// Stripe App Integration
	ConfigureStripeApp(ctx context.Context, namespace string, config models.StripeConfig) error
	HandleStripeWebhook(ctx context.Context, payload []byte, signature string) error
}
```

---

## 4. Component Design

### 4.1 Subject Saga & DLQ Pattern (Req 1 & 21)
**File:** `internal/opensbt/controlplane/users.go` (or similar UserManager implementation)

1.  **Creation:** When `POST /api/v1/tenants/:tenantID/users` is called, `open-sbt` creates the user in Ory Kratos.
2.  **OpenMeter Registration:** It calls `IMetering.RegisterSubject(ctx, tenantID, fmt.Sprintf("%s#%s", tenantID, kratosUser.ID))`.
3.  **Saga Rollback:** If OpenMeter fails, it attempts to delete the Kratos user.
4.  **DLQ Escalation (Req 21):** If the rollback to Kratos fails after 3 exponential backoff retries (1s, 2s, 4s), publish a NATS event to `opensbt_orphanedSubjects`.
    ```json
    {
      "detailType": "opensbt_orphanedSubjects",
      "source": "zerosbt.control.plane",
      "detail": {
        "tenant_id": "uuid",
        "subject_id": "tenant_id#user_uuid",
        "reason": "kratos_rollback_failed"
      }
    }
    ```
5.  **Reconciliation Controller:** A background goroutine listens to `opensbt_orphanedSubjects`, attempting to reconcile (delete from OpenMeter/Kratos) every 5 minutes. Fails 3 times -> publishes to `opensbt_notifications` for ops intervention.

### 4.2 OpenMeter API Integration (Req 5 & 6)
**File:** `internal/opensbt/providers/openmeter/metering.go`

*   **Authentication:** The client will authenticate to the internal Hub endpoint (`http://openmeter.hub-platform-core.svc:8888`).
*   **Header Injection:** All requests MUST include the header: `OpenMeter-Namespace: <namespace>`.
*   **Fail-Open Entitlements:**
    ```go
    func (m *OpenMeterProvider) CheckEntitlement(ctx context.Context, namespace, subjectID, featureKey string) (*models.EntitlementStatus, error) {
        // ... execute HTTP request to OpenMeter ...
        if isTransientError(err) {
            log.Warnf("OpenMeter unreachable, failing open for subject %s", subjectID)
            return &models.EntitlementStatus{HasAccess: true, IsFallback: true}, nil
        }
        // ... return exact OpenMeter status
    }
    ```

### 4.3 Stripe Webhook Handling (Req 18)
**File:** `internal/opensbt/providers/openmeter/billing.go`

*   **Webhook Secret:** Loaded into the pod via environment variables injected by External Secrets Operator (ESO) from Infisical.
*   **Idempotency:** Implements the Transactional Inbox pattern using the local Postgres database (`processed_events` table).
    ```go
    func (b *BillingProvider) HandleStripeWebhook(ctx context.Context, payload []byte, signature string) error {
        // 1. Verify Stripe signature using ESO-injected secret
        // 2. Extract stripe_event_id
        // 3. Attempt INSERT INTO processed_events (event_id) VALUES (stripe_event_id) ON CONFLICT DO ERROR
        // 4. Translate payload and publish `opensbt_billingSuccess` to NATS
    }
    ```

### 4.4 REST API Layer (Req 8, 9, 13)
**File:** `internal/opensbt/controlplane/routes.go`

*   **Middleware:** Extract JWT, parse `tenant_id`, validate scopes, and inject `tenant_id` into the Gin `context`.
*   **RFC 7807 Errors:** Ensure all `c.JSON()` error responses conform to `application/problem+json`.
*   **OpenAPI 3.0:** Generate or manually write the OpenAPI spec in `docs/openapi.yaml`. Include examples for the Postman validation (Req 13).

---

## 5. Infrastructure & GitOps (Crossplane & Helm)

### 5.1 OpenMeter Deployment (Req 20)
**File:** `manifests/hub-core-services/openmeter/application.yaml` (ArgoCD App)
*   Deploys the official OpenMeter Helm chart to the `hub-platform-core` namespace.
*   **Node Affinity/Tolerations:** Ensure values.yaml includes:
    ```yaml
    nodeSelector:
      node-role.kubernetes.io/worker: "true"
    ```

### 5.2 Namespace Provisioning via Crossplane (Req 19)
**File:** `manifests/hub-core-services/crossplane/tenant-platform/compositions/ainativesaas-starter-hetzner.yaml`
*   Add a new `provider-http` resource to the pipeline to call OpenMeter's REST API during tenant creation.
    ```yaml
    - name: openmeter-namespace
      base:
        apiVersion: http.crossplane.io/v1alpha2
        kind: Request
        spec:
          forProvider:
            url: "http://openmeter.hub-platform-core.svc:8888/api/v1/namespaces"
            method: POST
            payload: '{"slug": "placeholder"}' # Patched with tenantId
            # Mapping DELETE for cleanup
            rollback:
              method: DELETE
              url: "http://openmeter.hub-platform-core.svc:8888/api/v1/namespaces/placeholder"
    ```

### 5.3 Istio & mTLS (Req 11)
*   **No Go Code Changes:** The `open-sbt` HTTP clients (e.g., to Ory or OpenMeter) will use plain HTTP (`http://ory-kratos:80`).
*   **Envoy Sidecar:** Add the `istio-injection: enabled` label to the `hub-platform-ops` namespace where `open-sbt` runs.
*   **PeerAuthentication:** Ensure STRICT mTLS is enforced in the namespace via Istio CRDs. SPIRE will deliver the SVID directly to the Envoy sidecar.

### 5.4 Secret Management (Req 18)
*   **Infisical:** Store the `STRIPE_WEBHOOK_SECRET`.
*   **ESO:** Create an `ExternalSecret` targeting `hub-platform-ops` to mount this as an environment variable in the `hub-operator` / `open-sbt` deployment.

---

## 6. Database Design (PostgreSQL)

**File:** `internal/opensbt/providers/postgres/migrations/005_tenant_users.sql`

This migration runs on the **Spoke Cluster** (Tenant DB) via Atlas migrations (Req 12).
```sql
CREATE TABLE IF NOT EXISTS public.users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email VARCHAR(255) NOT NULL UNIQUE,
  created_at TIMESTAMPTZ DEFAULT NOW(),
  updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_email ON public.users(email);
CREATE INDEX IF NOT EXISTS idx_users_created_at ON public.users(created_at);

-- RLS Policy (Enforces JWT mapping)
ALTER TABLE public.users ENABLE ROW LEVEL SECURITY;
CREATE POLICY users_isolation_policy ON public.users
  FOR ALL
  USING (id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid)
  WITH CHECK (id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid);

-- Trigger for updated_at
CREATE TRIGGER update_users_updated_at
  BEFORE UPDATE ON public.users
  FOR EACH ROW
  EXECUTE FUNCTION public.update_updated_at_column();
```

---

## 7. Event Choreography (NATS)

New NATS topics to be created in `internal/opensbt/models/events.go`:
*   `opensbt_orphanedSubjects`: Handled by the Reconciliation Controller.
*   `opensbt_notifications`: Alerts for Ops (consumed by alerting manager).
*   `opensbt_billingSuccess` / `opensbt_billingFailure`: Emitted by the Stripe Webhook handler.

---

## 8. Deprecation & Migration Plan

1.  **Delete Legacy Code:** Remove `internal/opensbt/providers/metering/metering.go` completely.
2.  **Delete Legacy Migrations:** Remove `004_metering.sql` from the Postgres migrations directory. Drop `meters` and `usage_events` tables from existing databases if applicable.
3.  **Implement New Providers:** Create `internal/opensbt/providers/openmeter/` containing `metering.go` and `billing.go` implementing the new interfaces.
4.  **Wiring:** Update `cmd/opensbt/main.go` to inject `openmeter.NewMeteringProvider()` and `openmeter.NewBillingProvider()` into the ControlPlane configuration.

---

## 9. Review Against Edge Cases

*   **AgentGateway OTLP Emission vs Hub Registration:** The AgentGateway documentation (Req 4) must clearly state that if OTLP spans arrive for a `Subject` that does not exist in OpenMeter yet, they might be dropped or recorded against an unknown entity depending on OpenMeter's configuration. The SDK/PaaS documentation must enforce calling `POST /api/v1/users` *before* the user can generate billable actions.
*   **Proration:** Explicitly documented that `UpdateSubscription` delegates proration mathematics completely to OpenMeter and Stripe (Req 16).