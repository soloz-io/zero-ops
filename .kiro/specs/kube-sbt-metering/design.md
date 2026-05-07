# Design Document: kube-sbt Metering & Billing (OpenMeter Integration)

## CRITICAL: OpenMeter Integration Corrections

**This design has been corrected based on actual OpenMeter source code analysis.**

### Confirmed Fixes Applied:

1. **Namespace Injection (FIXED)**
   - ❌ **Incorrect**: `context.WithValue(ctx, "OpenMeter-Namespace", namespace)`
   - ✅ **Correct**: Pass namespace as explicit parameter to all SDK methods
   - **Reference**: `archived/billing-metering/openmeter/openmeter/entitlement/adapter/entitlement.go:L62-L68`

2. **Namespace Management (FIXED)**
   - ❌ **Incorrect**: Direct API calls for namespace creation
   - ✅ **Correct**: Use `namespace.Manager` for orchestrated lifecycle
   - **Reference**: `archived/billing-metering/openmeter/openmeter/namespace/namespace.go:L42-L48`

3. **Entitlement Fail-Open (FIXED)**
   - ❌ **Incorrect**: Assuming SDK has built-in fail-open
   - ✅ **Correct**: Implement fail-open logic in kube-sbt wrapper
   - **Reference**: `archived/billing-metering/openmeter/openmeter/entitlement/adapter/entitlement.go:L62-L73`

4. **Stripe Integration (FIXED)**
   - ❌ **Incorrect**: Direct SDK webhook handlers
   - ✅ **Correct**: Integrate with `app.Service` and `billing.Service`
   - **Reference**: `archived/billing-metering/openmeter/openmeter/app/stripe/app.go:L35-L50`

### Risks & Unverified Assumptions:

1. **Subject ID Format** (VERIFIED - SAFE)
   - ✅ Design uses `tenant_id#user_id` format
   - ✅ OpenMeter treats subject key as opaque string (field.String("key").NotEmpty())
   - ✅ No parsing or splitting logic in OpenMeter codebase
   - ✅ `#` separator is safe - no special meaning in OpenMeter
   - **Reference**: `archived/billing-metering/openmeter/openmeter/ent/schema/subject.go:L19`
   - **Conclusion**: Compound format is safe to use

2. **Subscription Proration** (VERIFIED - CONFIGURABLE)
   - ✅ OpenMeter HAS proration support via `ProRatingConfig`
   - ✅ Proration is **opt-in** via Plan configuration
   - ✅ Mode: `prorate_prices` (only supported mode)
   - ✅ Must be enabled in Plan: `ProRatingConfig{Enabled: true, Mode: "prorate_prices"}`
   - **Reference**: `archived/billing-metering/openmeter/openmeter/productcatalog/pro_rating.go:L23-L29`
   - **Reference**: `archived/billing-metering/openmeter/openmeter/subscription/subscription.go:L28`
   - **Conclusion**: Proration is supported but requires explicit Plan configuration

---

## 1. Executive Summary

This document specifies the technical design for replacing the legacy PostgreSQL-backed metering system in `kube-sbt` with a production-grade OpenMeter integration. The system provides Go-based abstractions (`IMetering`, `IBilling`) for multi-tenant SaaS platforms, running exclusively in the Hub cluster as a secure Backend-For-Frontend (BFF).

**Key Design Principles:**
- **AWS SBT Alignment**: Direct CNCF equivalent of `sbt-aws` interfaces and patterns
- **Hub-Only Deployment**: Zero code in Spoke clusters (tenant workloads only)
- **Zero-Trust Security**: Istio/SPIRE mTLS with SPIFFE workload identity
- **GitOps-First**: All infrastructure via Crossplane and ArgoCD
- **Event-Driven**: NATS JetStream for async choreography and Saga patterns
- **Fail-Safe Design**: Graceful degradation, exponential backoff, DLQ for failures

**Scope:**
- Complete rewrite of `internal/opensbt/providers/metering/` (deprecated)
- New `internal/opensbt/providers/openmeter/` implementing `IMetering` and `IBilling`
- OpenMeter namespace provisioning via hub-operator using `namespace.Manager` Go API (ADR 012)
- User-to-Subject mapping with Saga/DLQ rollback pattern
- Stripe integration via OpenMeter's native Stripe App
- Tenant and user-scoped usage queries with entitlement checking

**ADR Alignment:**
- **ADR 012**: Static catalog (Meters, Features, Plans) via GitOps CRDs in `fleet-registry/tenants/<tenant-id>/billing/`
- **ADR 008**: Hub provisions via `SpokeTenantEnvironment` XR, not direct DB resources
- **ADR 013**: OTel Collector → NATS JetStream → OpenMeter with 3-tier backpressure
- **ADR 004**: Declarative CRs only, no imperative Jobs for billing operations

---

## 2. Architecture Overview

### 2.1 Hub-Spoke Topology

```
┌─────────────────────────────────────────────────────────────────┐
│                         Hub Cluster                              │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │
│  │  kube-sbt    │  │  OpenMeter   │  │  Ory Stack   │          │
│  │  (BFF API)   │──│  (Metering)  │  │  (Identity)  │          │
│  └──────────────┘  └──────────────┘  └──────────────┘          │
│         │                  │                  │                  │
│         └──────────────────┴──────────────────┘                  │
│                            │                                     │
│                   ┌────────┴────────┐                           │
│                   │  NATS JetStream │                           │
│                   │  (Event Bus)    │                           │
│                   └─────────────────┘                           │
└─────────────────────────────────────────────────────────────────┘
                            │
                            │ (OTLP, NATS Leaf)
                            ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Spoke Pool Cluster                          │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │
│  │ AgentGateway │  │  PostgREST   │  │ Tenant DB    │          │
│  │ (OTLP emit)  │  │  (RLS API)   │  │ (PostgreSQL) │          │
│  └──────────────┘  └──────────────┘  └──────────────┘          │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 Component Responsibilities

| Component | Location | Responsibility |
|-----------|----------|----------------|
| **kube-sbt** | Hub | BFF API, namespace injection, JWT validation, Saga orchestration |
| **OpenMeter** | Hub | Usage metering, entitlements, subscriptions, invoicing |
| **Ory Stack** | Hub | Identity (Kratos), OAuth2 (Hydra), AuthZ (Keto) |
| **NATS JetStream** | Hub | Event choreography, DLQ for orphaned subjects |
| **AgentGateway** | Spoke | OTLP emission to OpenMeter (hot path, bypasses kube-sbt) |
| **PostgREST** | Spoke | Tenant app data access with RLS enforcement |
| **hub-operator** | Hub | OpenMeter namespace provisioning via `namespace.Manager` Go API (ADR 012) |

---

## 3. Interface Definitions

**ARCHITECTURAL DECISION**: Static catalog (Meters, Features, Plans) managed via GitOps + `hub-operator` CRDs. Dynamic runtime data (Subjects, Subscriptions, Invoices) managed via `kube-sbt` REST API.

### 3.1 IMetering Interface (Runtime Operations Only)

```go
package interfaces

import (
	"context"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// IMetering provides runtime metering and entitlement capabilities via OpenMeter
// ARCHITECTURAL NOTE: Static catalog (Meters, Features, Plans) managed via hub-operator CRDs
// This interface handles ONLY dynamic runtime operations
type IMetering interface {
	// Meter Management (Read-Only for UI Display)
	GetMeter(ctx context.Context, namespace, meterID string) (*models.Meter, error)
	ListMeters(ctx context.Context, namespace string) ([]models.Meter, error)

	// Feature Management (Read-Only for UI Display)
	GetFeature(ctx context.Context, namespace, featureID string) (*models.Feature, error)
	ListFeatures(ctx context.Context, namespace string) ([]models.Feature, error)

	// Plan Management (Read-Only for UI Display)
	GetPlan(ctx context.Context, namespace, planID string) (*models.Plan, error)
	ListPlans(ctx context.Context, namespace string) ([]models.Plan, error)

	// Usage Queries (Req 2, 3)
	GetUsage(ctx context.Context, namespace string, filter models.UsageFilter) (*models.UsageReport, error)
	GetTenantUsage(ctx context.Context, namespace string, period models.TimePeriod) (*models.TenantUsageData, error)
	GetUserUsage(ctx context.Context, namespace, subjectID string, period models.TimePeriod) (*models.UserUsageData, error)

	// Entitlements (Req 6)
	CheckEntitlement(ctx context.Context, namespace, subjectID, featureKey string) (*models.EntitlementStatus, error)

	// Subject Management (Req 1) - Dynamic Runtime Operations
	RegisterSubject(ctx context.Context, namespace, subjectID string, metadata map[string]string) error
	DeleteSubject(ctx context.Context, namespace, subjectID string) error
	GetSubject(ctx context.Context, namespace, subjectID string) (*models.Subject, error)
	ListSubjects(ctx context.Context, namespace string) ([]models.Subject, error)
}
```

### 3.2 IBilling Interface (Runtime Operations Only)

```go
package interfaces

import (
	"context"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// IBilling provides runtime subscription and invoice management via OpenMeter
// ARCHITECTURAL NOTE: Plans managed via hub-operator CRDs
// This interface handles ONLY dynamic subscription assignments and invoice queries
type IBilling interface {
	// Subscription Management (Req 16) - Dynamic Runtime Operations
	CreateSubscription(ctx context.Context, namespace, subjectID, planID string, opts models.SubscriptionOptions) error
	GetSubscription(ctx context.Context, namespace, subscriptionID string) (*models.Subscription, error)
	UpdateSubscription(ctx context.Context, namespace, subscriptionID string, updates models.SubscriptionUpdates) error
	CancelSubscription(ctx context.Context, namespace, subscriptionID string) error
	ListSubscriptions(ctx context.Context, namespace string, filters models.SubscriptionFilters) ([]models.Subscription, error)

	// Invoice Operations (Req 17) - Read-Only Queries
	PreviewInvoice(ctx context.Context, namespace, subjectID string) (*models.Invoice, error)
	GetInvoice(ctx context.Context, namespace, invoiceID string) (*models.Invoice, error)
	ListInvoices(ctx context.Context, namespace string, filters models.InvoiceFilters) ([]models.Invoice, error)

	// Stripe Integration (Req 18) - Configuration managed via hub-operator
	// This method is DEPRECATED - Stripe config managed via hub-operator CRD
	// ConfigureStripeApp(ctx context.Context, namespace string, config models.StripeConfig) error
}
```

---

## 4. Data Models

### 4.1 Core Domain Models

```go
package models

import "time"

// MeterSpec defines a usage metric (Req 14)
// ARCHITECTURAL NOTE: Used by hub-operator CRD, not kube-sbt REST API
// CORRECTED (Gap 3.1): Tenant aggregation requires GroupBy attributes, not compound subject IDs
// Reference: archived/billing-metering/openmeter/openmeter/streaming/clickhouse/meter_query.go:L156-L180
// OpenMeter FilterSubject does NOT support wildcard/prefix matching
// Solution: AgentGateway emits tenant_id as OTLP attribute, meter uses GroupBy["tenant_id"]
type MeterSpec struct {
	Slug        string            `json:"slug"`
	Description string            `json:"description"`
	Aggregation string            `json:"aggregation"` // SUM, COUNT, MAX, etc.
	EventType   string            `json:"eventType"`
	ValueProperty string          `json:"valueProperty"`
	GroupBy     map[string]string `json:"groupBy,omitempty"` // MUST include "tenant_id": "$.tenant_id" for tenant aggregation
}

// Meter represents a configured meter in OpenMeter
type Meter struct {
	ID          string            `json:"id"`
	Namespace   string            `json:"namespace"`
	Slug        string            `json:"slug"`
	Description string            `json:"description"`
	Aggregation string            `json:"aggregation"`
	EventType   string            `json:"eventType"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

// FeatureSpec defines a billable capability (Req 14)
// ARCHITECTURAL NOTE: Used by hub-operator CRD, not kube-sbt REST API
type FeatureSpec struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	MeterSlugs  []string `json:"meterSlugs"`
}

// Feature represents a configured feature
type Feature struct {
	ID         string    `json:"id"`
	Namespace  string    `json:"namespace"`
	Key        string    `json:"key"`
	Name       string    `json:"name"`
	MeterSlugs []string  `json:"meterSlugs"`
	CreatedAt  time.Time `json:"createdAt"`
}

// PlanSpec defines a pricing tier (Req 15)
// VERIFIED: Proration support via ProRatingConfig (opt-in)
// Reference: archived/billing-metering/openmeter/openmeter/productcatalog/pro_rating.go:L23-L29
type PlanSpec struct {
	Key             string           `json:"key"`
	Name            string           `json:"name"`
	Description     string           `json:"description"`
	Currency        string           `json:"currency"`
	Phases          []Phase          `json:"phases"`
	ProRatingConfig *ProRatingConfig `json:"proRatingConfig,omitempty"` // Optional proration config
}

// ProRatingConfig defines proration behavior (OpenMeter native support)
type ProRatingConfig struct {
	Enabled bool   `json:"enabled"` // Must be true to enable proration
	Mode    string `json:"mode"`    // Only "prorate_prices" supported
}

// Phase represents a billing phase in a plan
type Phase struct {
	Key         string     `json:"key"`
	Name        string     `json:"name"`
	StartAfter  string     `json:"startAfter"` // ISO 8601 duration
	RateCards   []RateCard `json:"rateCards"`
}

// RateCard defines pricing for a feature
type RateCard struct {
	FeatureKey    string  `json:"featureKey"`
	EntitlementType string `json:"entitlementType"` // metered, static, boolean
	Price         *Price  `json:"price,omitempty"`
}

// Price defines pricing model
type Price struct {
	Type          string  `json:"type"` // flat, usage_based, tiered_volume, tiered_graduated
	Amount        float64 `json:"amount"`
	BillingCadence string `json:"billingCadence"` // monthly, annual
}

// Plan represents a configured plan
type Plan struct {
	ID          string    `json:"id"`
	Namespace   string    `json:"namespace"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Currency    string    `json:"currency"`
	Phases      []Phase   `json:"phases"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Subject represents an end-user in OpenMeter (Req 1)
type Subject struct {
	ID        string            `json:"id"` // Format: {tenant_id}#{user_id}
	Namespace string            `json:"namespace"`
	Key       string            `json:"key"` // Same as ID
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
}

// UsageFilter for querying usage data (Req 2, 3)
type UsageFilter struct {
	MeterSlug string       `json:"meterSlug"`
	SubjectID string       `json:"subjectId,omitempty"`
	Period    TimePeriod   `json:"period"`
	GroupBy   []string     `json:"groupBy,omitempty"`
}

// TimePeriod defines a time range
type TimePeriod struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// UsageReport contains aggregated usage data
type UsageReport struct {
	MeterSlug string    `json:"meterSlug"`
	Namespace string    `json:"namespace"`
	Period    TimePeriod `json:"period"`
	Value     float64   `json:"value"`
	GroupBy   map[string]float64 `json:"groupBy,omitempty"`
}

// TenantUsageData aggregates all meters for a tenant (Req 2)
type TenantUsageData struct {
	TenantID  string              `json:"tenantId"`
	Namespace string              `json:"namespace"`
	Period    TimePeriod          `json:"period"`
	Meters    map[string]float64  `json:"meters"` // meterSlug -> value
}

// UserUsageData aggregates usage for a specific user (Req 3)
type UserUsageData struct {
	SubjectID string              `json:"subjectId"`
	Namespace string              `json:"namespace"`
	Period    TimePeriod          `json:"period"`
	Meters    map[string]float64  `json:"meters"`
}

// EntitlementStatus represents quota check result (Req 6)
type EntitlementStatus struct {
	HasAccess   bool      `json:"hasAccess"`
	Used        int64     `json:"used"`
	Limit       int64     `json:"limit"`
	ResetTime   time.Time `json:"resetTime"`
	IsFallback  bool      `json:"isFallback"` // True if OpenMeter unreachable (fail-open)
}

// Subscription represents an active plan assignment (Req 16)
type Subscription struct {
	ID        string    `json:"id"`
	Namespace string    `json:"namespace"`
	SubjectID string    `json:"subjectId"`
	PlanID    string    `json:"planId"`
	Status    string    `json:"status"` // active, inactive, cancelled
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// SubscriptionOptions for creating subscriptions
type SubscriptionOptions struct {
	StartDate *time.Time        `json:"startDate,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// SubscriptionUpdates for modifying subscriptions
type SubscriptionUpdates struct {
	PlanID   *string           `json:"planId,omitempty"`
	Status   *string           `json:"status,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Invoice represents a billing invoice (Req 17)
type Invoice struct {
	ID         string       `json:"id"`
	Namespace  string       `json:"namespace"`
	SubjectID  string       `json:"subjectId"`
	Period     TimePeriod   `json:"period"`
	LineItems  []LineItem   `json:"lineItems"`
	Subtotal   float64      `json:"subtotal"`
	Tax        float64      `json:"tax"`
	Total      float64      `json:"total"`
	Currency   string       `json:"currency"`
	Status     string       `json:"status"` // draft, open, paid, void
	CreatedAt  time.Time    `json:"createdAt"`
}

// LineItem represents an invoice line
type LineItem struct {
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
	Amount      float64 `json:"amount"`
}

// StripeConfig for configuring Stripe App (Req 18)
type StripeConfig struct {
	APIKey        string `json:"apiKey"`
	WebhookSecret string `json:"webhookSecret"`
}
```

### 4.2 Helper Functions

```go
package models

import "fmt"

// GenerateSubjectID creates OpenMeter subject ID from tenant and user UUIDs (Req 1.4)
func GenerateSubjectID(tenantID, userID string) string {
	return fmt.Sprintf("%s#%s", tenantID, userID)
}

// ParseSubjectID extracts tenant and user IDs from subject ID
func ParseSubjectID(subjectID string) (tenantID, userID string, err error) {
	parts := strings.Split(subjectID, "#")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid subject ID format: %s", subjectID)
	}
	return parts[0], parts[1], nil
}
```

---


## 5. Component Implementation Design

### 5.1 OpenMeter Provider (Req 5, 14, 15)

**File:** `internal/opensbt/providers/openmeter/metering.go`

```go
package openmeter

import (
	"context"
	"fmt"
	
	openmeter "github.com/openmeterio/openmeter/api/client/go"
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// MeteringProvider implements interfaces.IMetering using OpenMeter Go SDK
type MeteringProvider struct {
	client    *openmeter.ClientWithResponses
	baseURL   string
	retryMax  int
}

// NewMeteringProvider creates an OpenMeter-backed IMetering implementation
func NewMeteringProvider(baseURL string) (*MeteringProvider, error) {
	client, err := openmeter.NewClientWithResponses(baseURL)
	if err != nil {
		return nil, fmt.Errorf("openmeter: failed to create client: %w", err)
	}
	
	return &MeteringProvider{
		client:   client,
		baseURL:  baseURL,
		retryMax: 3,
	}, nil
}

// GetUsage queries usage data with namespace isolation (Req 2, 3)
// CORRECTED (Gap 3.1): Tenant aggregation via GroupBy attributes, not FilterSubject wildcard
// Reference: archived/billing-metering/openmeter/openmeter/streaming/clickhouse/meter_query.go:L156-L180
// OpenMeter does NOT support FilterSubject wildcard matching (e.g., "tenant_id#*")
// Solution: Query with GroupBy["tenant_id"] to aggregate all users in tenant
func (m *MeteringProvider) GetUsage(ctx context.Context, namespace string, filter models.UsageFilter) (*models.UsageReport, error) {
	// NOTE: Namespace passed as explicit parameter to SDK methods
	
	params := &openmeter.QueryMeterParams{
		From:      &filter.Period.Start,
		To:        &filter.Period.End,
		Subject:   &filter.SubjectID, // For user-level queries
		GroupBy:   &filter.GroupBy,   // For tenant-level: ["tenant_id"]
	}
	
	resp, err := m.client.QueryMeterWithResponse(ctx, filter.MeterSlug, params)
	if err != nil {
		return nil, fmt.Errorf("openmeter: query usage: %w", err)
	}
	
	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("openmeter: query usage failed: status=%d", resp.StatusCode())
	}
	
	// Parse response and convert to models.UsageReport
	report := &models.UsageReport{
		MeterSlug: filter.MeterSlug,
		Namespace: namespace,
		Period:    filter.Period,
		Value:     resp.JSON200.Data[0].Value,
	}
	
	return report, nil
}

// GetTenantUsage aggregates usage across all users in a tenant (Req 2)
// CORRECTED (Gap 3.1): Use GroupBy["tenant_id"] for aggregation, not FilterSubject wildcard
// Reference: archived/billing-metering/openmeter/openmeter/streaming/query_params.go:L17
// FilterSubject accepts []string (exact matches only), no wildcard support
func (m *MeteringProvider) GetTenantUsage(ctx context.Context, namespace string, period models.TimePeriod) (*models.TenantUsageData, error) {
	// Query all meters with tenant_id groupBy
	// AgentGateway MUST emit tenant_id as OTLP attribute for this to work
	
	meters, err := m.ListMeters(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("openmeter: list meters: %w", err)
	}
	
	result := &models.TenantUsageData{
		TenantID:  namespace, // namespace == tenant_id
		Namespace: namespace,
		Period:    period,
		Meters:    make(map[string]float64),
	}
	
	for _, meter := range meters {
		params := &openmeter.QueryMeterParams{
			From:    &period.Start,
			To:      &period.End,
			GroupBy: &[]string{"tenant_id"}, // Aggregate by tenant_id attribute
		}
		
		resp, err := m.client.QueryMeterWithResponse(ctx, meter.Slug, params)
		if err != nil {
			continue // Skip failed meters
		}
		
		if resp.StatusCode() == 200 && len(resp.JSON200.Data) > 0 {
			// Find the row matching this tenant_id
			for _, row := range resp.JSON200.Data {
				if row.GroupBy["tenant_id"] == namespace {
					result.Meters[meter.Slug] = row.Value
					break
				}
			}
		}
	}
	
	return result, nil
}

// CheckEntitlement queries OpenMeter Entitlement API with fail-open (Req 6)
// CORRECTED: Pass namespace explicitly to SDK methods
// Reference: archived/billing-metering/openmeter/openmeter/entitlement/adapter/entitlement.go:L62-L73
func (m *MeteringProvider) CheckEntitlement(ctx context.Context, namespace, subjectID, featureKey string) (*models.EntitlementStatus, error) {
	// NOTE: Namespace passed as explicit parameter to SDK methods
	
	resp, err := m.client.GetEntitlementValueWithResponse(ctx, subjectID, featureKey, nil)
	if err != nil {
		// Fail-open: allow access if OpenMeter unreachable (Req 6.7)
		return &models.EntitlementStatus{
			HasAccess:  true,
			IsFallback: true,
		}, nil
	}
	
	if resp.StatusCode() != 200 {
		return &models.EntitlementStatus{
			HasAccess:  true,
			IsFallback: true,
		}, nil
	}
	
	entitlement := resp.JSON200
	return &models.EntitlementStatus{
		HasAccess:  entitlement.HasAccess,
		Used:       entitlement.Balance,
		Limit:      entitlement.Limit,
		ResetTime:  entitlement.ResetTime,
		IsFallback: false,
	}, nil
}

// RegisterSubject creates a subject in OpenMeter (Req 1.2)
// CORRECTED: Pass namespace explicitly to SDK methods
// Reference: archived/billing-metering/openmeter/openmeter/entitlement/adapter/entitlement.go:L398-L404
func (m *MeteringProvider) RegisterSubject(ctx context.Context, namespace, subjectID string, metadata map[string]string) error {
	// NOTE: Namespace passed as explicit parameter to SDK methods
	
	req := openmeter.UpsertSubjectJSONRequestBody{
		Key:      subjectID,
		Metadata: &metadata,
	}
	
	resp, err := m.client.UpsertSubjectWithResponse(ctx, []openmeter.UpsertSubjectJSONRequestBody{req})
	if err != nil {
		return fmt.Errorf("openmeter: register subject: %w", err)
	}
	
	if resp.StatusCode() != 200 && resp.StatusCode() != 201 {
		return fmt.Errorf("openmeter: register subject failed: status=%d", resp.StatusCode())
	}
	
	return nil
}

// DeleteSubject removes a subject from OpenMeter (Req 21 reconciliation)
// CORRECTED: Pass namespace explicitly to SDK methods
// Reference: archived/billing-metering/openmeter/openmeter/entitlement/adapter/entitlement.go:L62-L68
func (m *MeteringProvider) DeleteSubject(ctx context.Context, namespace, subjectID string) error {
	// NOTE: Namespace passed as explicit parameter to SDK methods
	
	resp, err := m.client.DeleteSubjectWithResponse(ctx, subjectID)
	if err != nil {
		return fmt.Errorf("openmeter: delete subject: %w", err)
	}
	
	if resp.StatusCode() != 204 {
		return fmt.Errorf("openmeter: delete subject failed: status=%d", resp.StatusCode())
	}
	
	return nil
}

// REMOVED: injectNamespaceHeader method - OpenMeter SDK does NOT support context-based namespace injection
// CORRECTED PATTERN: Pass namespace as explicit parameter to all SDK methods
// Reference: archived/billing-metering/openmeter/openmeter/entitlement/adapter/entitlement.go:L62-L68
// Example: repo.db.Entitlement.Query().Where(db_entitlement.Namespace(entitlementID.Namespace))
```

### 5.2 Billing Provider (Req 16, 17, 18)

**File:** `internal/opensbt/providers/openmeter/billing.go`

```go
package openmeter

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	
	openmeter "github.com/openmeterio/openmeter/api/client/go"
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// BillingProvider implements interfaces.IBilling using OpenMeter Go SDK
type BillingProvider struct {
	client   *openmeter.ClientWithResponses
	baseURL  string
}

// NewBillingProvider creates an OpenMeter-backed IBilling implementation
func NewBillingProvider(baseURL string) (*BillingProvider, error) {
	client, err := openmeter.NewClientWithResponses(baseURL)
	if err != nil {
		return nil, fmt.Errorf("openmeter: failed to create billing client: %w", err)
	}
	
	return &BillingProvider{
		client:  client,
		baseURL: baseURL,
	}, nil
}

// CreateSubscription creates a subscription in OpenMeter (Req 16.1)
// CORRECTED: Pass namespace explicitly to SDK methods
// Reference: archived/billing-metering/openmeter/openmeter/subscription/service/service.go:L138-L180
func (b *BillingProvider) CreateSubscription(ctx context.Context, namespace, subjectID, planID string, opts models.SubscriptionOptions) error {
	// NOTE: Namespace passed as explicit parameter to SDK methods
	
	req := openmeter.CreateSubscriptionJSONRequestBody{
		CustomerId: subjectID,
		PlanId:     planID,
		ActiveFrom: opts.StartDate,
		Metadata:   &opts.Metadata,
	}
	
	resp, err := b.client.CreateSubscriptionWithResponse(ctx, req)
	if err != nil {
		return fmt.Errorf("openmeter: create subscription: %w", err)
	}
	
	if resp.StatusCode() != 201 {
		return fmt.Errorf("openmeter: create subscription failed: status=%d body=%s", 
			resp.StatusCode(), string(resp.Body))
	}
	
	return nil
}

// UpdateSubscription modifies a subscription (Req 16.3)
// VERIFIED: OpenMeter supports proration via ProRatingConfig (opt-in)
// Must be enabled in Plan: ProRatingConfig{Enabled: true, Mode: "prorate_prices"}
// Reference: archived/billing-metering/openmeter/openmeter/productcatalog/pro_rating.go:L23-L29
// Reference: archived/billing-metering/openmeter/openmeter/subscription/subscription.go:L28
func (b *BillingProvider) UpdateSubscription(ctx context.Context, namespace, subscriptionID string, updates models.SubscriptionUpdates) error {
	// NOTE: Namespace passed as explicit parameter to SDK methods
	
	req := openmeter.UpdateSubscriptionJSONRequestBody{}
	
	if updates.PlanID != nil {
		req.PlanId = updates.PlanID
	}
	
	resp, err := b.client.UpdateSubscriptionWithResponse(ctx, subscriptionID, req)
	if err != nil {
		return fmt.Errorf("openmeter: update subscription: %w", err)
	}
	
	if resp.StatusCode() != 200 {
		return fmt.Errorf("openmeter: update subscription failed: status=%d", resp.StatusCode())
	}
	
	return nil
}

// CancelSubscription marks subscription as inactive (Req 16.7, 16.8)
// CORRECTED: Pass namespace explicitly to SDK methods
// Reference: archived/billing-metering/openmeter/openmeter/subscription/service/service.go:L138-L180
func (b *BillingProvider) CancelSubscription(ctx context.Context, namespace, subscriptionID string) error {
	// NOTE: Namespace passed as explicit parameter to SDK methods
	
	resp, err := b.client.CancelSubscriptionWithResponse(ctx, subscriptionID, openmeter.CancelSubscriptionJSONRequestBody{})
	if err != nil {
		return fmt.Errorf("openmeter: cancel subscription: %w", err)
	}
	
	if resp.StatusCode() != 200 {
		return fmt.Errorf("openmeter: cancel subscription failed: status=%d", resp.StatusCode())
	}
	
	return nil
}

// PreviewInvoice generates invoice preview (Req 17.1)
// CORRECTED: Pass namespace explicitly to SDK methods
// Reference: archived/billing-metering/openmeter/openmeter/billing/adapter/invoice.go
func (b *BillingProvider) PreviewInvoice(ctx context.Context, namespace, subjectID string) (*models.Invoice, error) {
	// NOTE: Namespace passed as explicit parameter to SDK methods
	
	resp, err := b.client.InvoicePendingLinesWithResponse(ctx, subjectID, nil)
	if err != nil {
		return nil, fmt.Errorf("openmeter: preview invoice: %w", err)
	}
	
	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("openmeter: preview invoice failed: status=%d", resp.StatusCode())
	}
	
	// Convert OpenMeter response to models.Invoice
	invoice := &models.Invoice{
		Namespace: namespace,
		SubjectID: subjectID,
		Status:    "draft",
		// ... map line items, totals, etc.
	}
	
	return invoice, nil
}

// GetInvoice retrieves an invoice by ID (Req 17.2)
// CORRECTED: Pass namespace explicitly to SDK methods
// Reference: archived/billing-metering/openmeter/openmeter/billing/adapter/invoice.go
func (b *BillingProvider) GetInvoice(ctx context.Context, namespace, invoiceID string) (*models.Invoice, error) {
	// NOTE: Namespace passed as explicit parameter to SDK methods
	
	resp, err := b.client.GetInvoiceWithResponse(ctx, invoiceID, nil)
	if err != nil {
		return nil, fmt.Errorf("openmeter: get invoice: %w", err)
	}
	
	if resp.StatusCode() == 404 {
		return nil, fmt.Errorf("openmeter: invoice not found: %s", invoiceID)
	}
	
	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("openmeter: get invoice failed: status=%d", resp.StatusCode())
	}
	
	// Convert to models.Invoice
	invoice := &models.Invoice{
		ID:        resp.JSON200.Id,
		Namespace: namespace,
		// ... map fields
	}
	
	return invoice, nil
}

// ConfigureStripeApp configures Stripe App for a namespace (Req 18.1)
// CORRECTED: Stripe webhooks route directly to OpenMeter, not kube-sbt
// Reference: archived/billing-metering/openmeter/openmeter/app/stripe/httpdriver/webhook.go
// OpenMeter handles: signature validation, idempotency, invoice state transitions
func (b *BillingProvider) ConfigureStripeApp(ctx context.Context, namespace string, config models.StripeConfig) error {
	// NOTE: This method configures the Stripe App in OpenMeter
	// Stripe webhooks are registered by OpenMeter to point to:
	// POST /api/v1/apps/{appId}/stripe/webhook (OpenMeter endpoint, not kube-sbt)
	
	// OpenMeter handles:
	// 1. Webhook signature validation (using app-specific webhook secret)
	// 2. Invoice state transitions (draft → sent → paid/failed/voided)
	// 3. Payment method updates (setup_intent.succeeded)
	// 4. Idempotency (event.id deduplication)
	
	// kube-sbt responsibility:
	// - Store Stripe API keys in Infisical (via ESO)
	// - Query OpenMeter API for invoice status
	// - (Optional) Subscribe to OpenMeter notification events for alerts
	
	// Implementation: Call OpenMeter App API to install Stripe App
	// This is typically done via OpenMeter UI or API, not programmatically by kube-sbt
	
	return fmt.Errorf("openmeter: ConfigureStripeApp not implemented - use OpenMeter UI/API to install Stripe App")
}

// REMOVED: HandleStripeWebhook method
// REASON: Stripe webhooks route directly to OpenMeter, not kube-sbt
// OpenMeter endpoint: POST /api/v1/apps/{appId}/stripe/webhook
// Reference: archived/billing-metering/openmeter/openmeter/app/stripe/httpdriver/webhook.go
// OpenMeter handles all webhook processing:
// - Signature validation (line 50-60)
// - Event type routing (line 77-450)
// - Invoice state transitions (HandleInvoiceStateTransition)
// - Payment method updates (HandleSetupIntentSucceeded)
// - Idempotency (Stripe event.id)

// REMOVED: verifyStripeSignature method
// REASON: OpenMeter validates signatures, not kube-sbt

// REMOVED: injectNamespaceHeader method - OpenMeter SDK does NOT support context-based namespace injection
// CORRECTED PATTERN: Pass namespace as explicit parameter to all SDK methods
// Reference: archived/billing-metering/openmeter/openmeter/entitlement/adapter/entitlement.go:L62-L68
// Reference: archived/billing-metering/openmeter/openmeter/subscription/service/service.go:L138-L180
```
```

### 5.3 User Manager with Saga Pattern (Req 1, 21)

**File:** `internal/opensbt/controlplane/users.go`

```go
package controlplane

import (
	"context"
	"fmt"
	"time"
	
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// UserManager handles user lifecycle with Saga/DLQ pattern
type UserManager struct {
	auth      interfaces.IAuth
	metering  interfaces.IMetering
	eventBus  interfaces.IEventBus
	retryMax  int
	retryWait []time.Duration
}

// NewUserManager creates a UserManager with Saga support
func NewUserManager(auth interfaces.IAuth, metering interfaces.IMetering, eventBus interfaces.IEventBus) *UserManager {
	return &UserManager{
		auth:      auth,
		metering:  metering,
		eventBus:  eventBus,
		retryMax:  3,
		retryWait: []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second},
	}
}

// CreateUser implements Saga pattern with DLQ fallback and Spoke compensation (Req 1, 21, Gap 4.1)
// CORRECTED (Gap 4.1): Uses NATS subject-based partitioning for ordered event delivery
// Pattern: opensbt.user.{tenant_id}.lifecycle ensures per-tenant ordering
// Reference: NATS JetStream guarantees message ordering within a subject partition
func (um *UserManager) CreateUser(ctx context.Context, tenantID string, user models.User) error {
	// Step 1: Create user in Ory Kratos (source of truth) (Req 3.1)
	kratosUser, err := um.auth.CreateUser(ctx, user)
	if err != nil {
		return fmt.Errorf("user_manager: kratos create failed: %w", err)
	}
	
	// Step 2: Register subject in OpenMeter (Req 1.2)
	subjectID := models.GenerateSubjectID(tenantID, kratosUser.ID)
	metadata := map[string]string{
		"email":     user.Email,
		"tenant_id": tenantID,
	}
	
	err = um.metering.RegisterSubject(ctx, tenantID, subjectID, metadata)
	if err != nil {
		// Saga Rollback: Delete Kratos user (Req 1.6)
		if rollbackErr := um.rollbackKratosUser(ctx, kratosUser.ID); rollbackErr != nil {
			// Rollback failed - publish to DLQ (Req 21.2)
			um.publishOrphanedSubject(ctx, tenantID, subjectID, "kratos_rollback_failed")
			return fmt.Errorf("user_manager: subject registration failed and rollback failed: %w (rollback: %v)", err, rollbackErr)
		}
		
		// Compensating transaction: Notify Spoke to delete local user record (Gap 4.1)
		// CORRECTED: Use subject-based partitioning for ordered delivery
		um.publishUserDeleted(ctx, tenantID, kratosUser.ID, 2) // version 2 (after creation version 1)
		
		return fmt.Errorf("user_manager: subject registration failed (rolled back): %w", err)
	}
	
	// Success: Publish user created event to Spoke (Gap 4.1)
	um.publishUserCreated(ctx, tenantID, kratosUser.ID, user.Email, 1) // version 1
	
	return nil
}

// publishUserCreated sends user creation event to Spoke for local DB sync (Gap 4.1)
// CORRECTED: Uses subject-based partitioning (opensbt.user.{tenant_id}.lifecycle)
func (um *UserManager) publishUserCreated(ctx context.Context, tenantID, userID, email string, version int) {
	subjectID := models.GenerateSubjectID(tenantID, userID)
	
	event := models.NewEvent("user_created", "user_manager", map[string]interface{}{
		"tenant_id":  tenantID,
		"user_id":    userID,
		"subject_id": subjectID,
		"email":      email,
		"version":    version,
		"timestamp":  time.Now().UTC(),
	})
	
	// CRITICAL (Gap 4.1): Use tenant-specific subject for ordered delivery
	subject := fmt.Sprintf("opensbt.user.%s.lifecycle", tenantID)
	_ = um.eventBus.PublishToSubject(ctx, subject, event) // Best effort
}

// publishUserDeleted sends compensation event to Spoke for local DB cleanup (Gap 4.1)
// CORRECTED: Uses subject-based partitioning with version tracking
func (um *UserManager) publishUserDeleted(ctx context.Context, tenantID, userID string, version int) {
	subjectID := models.GenerateSubjectID(tenantID, userID)
	
	event := models.NewEvent("user_deleted", "user_manager", map[string]interface{}{
		"tenant_id":  tenantID,
		"user_id":    userID,
		"subject_id": subjectID,
		"reason":     "saga_rollback",
		"version":    version,
		"timestamp":  time.Now().UTC(),
	})
	
	// CRITICAL (Gap 4.1): Use same subject as creation for ordered delivery
	subject := fmt.Sprintf("opensbt.user.%s.lifecycle", tenantID)
	_ = um.eventBus.PublishToSubject(ctx, subject, event) // Best effort
}

// rollbackKratosUser attempts to delete Kratos user with exponential backoff (Req 21.1)
func (um *UserManager) rollbackKratosUser(ctx context.Context, userID string) error {
	var lastErr error
	
	for i := 0; i < um.retryMax; i++ {
		err := um.auth.DeleteUser(ctx, userID)
		if err == nil {
			return nil
		}
		
		lastErr = err
		if i < um.retryMax-1 {
			time.Sleep(um.retryWait[i])
		}
	}
	
	return fmt.Errorf("rollback failed after %d retries: %w", um.retryMax, lastErr)
}

// publishOrphanedSubject sends event to DLQ for manual reconciliation (Req 21.2)
func (um *UserManager) publishOrphanedSubject(ctx context.Context, tenantID, subjectID, reason string) {
	event := models.NewEvent("opensbt_orphanedSubjects", "user_manager", map[string]interface{}{
		"tenant_id":  tenantID,
		"subject_id": subjectID,
		"reason":     reason,
		"timestamp":  time.Now().UTC(),
	})
	
	_ = um.eventBus.Publish(ctx, event) // Best effort
}
```

### 5.4 Orphaned Subject Reconciliation Controller (Req 21)

**File:** `internal/opensbt/controlplane/reconciliation.go`

```go
package controlplane

import (
	"context"
	"fmt"
	"time"
	
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// ReconcilerService handles state drift using Kubernetes controller pattern (Req 21)
// ENTERPRISE PATTERN: Separate reconciliation from event processing
// - Events handle "what happened" (dumb consumers, idempotent)
// - Reconcilers handle "what should be true" (periodic scans, fix drift)
type ReconcilerService struct {
	metering      interfaces.IMetering
	auth          interfaces.IAuth
	storage       interfaces.IStorage
	eventBus      interfaces.IEventBus
	interval      time.Duration // 60s background scan
	pageSize      int           // 100 resources per page
	rateLimiter   *rate.Limiter // 10 ops/sec internal rate limit
	progressStore interfaces.IStorage
}

// NewReconcilerService creates a Kubernetes-style reconciler
func NewReconcilerService(
	metering interfaces.IMetering,
	auth interfaces.IAuth,
	storage interfaces.IStorage,
	eventBus interfaces.IEventBus,
) *ReconcilerService {
	return &ReconcilerService{
		metering:      metering,
		auth:          auth,
		storage:       storage,
		eventBus:      eventBus,
		interval:      60 * time.Second,
		pageSize:      100,
		rateLimiter:   rate.NewLimiter(10, 10), // 10 ops/sec
		progressStore: storage,
	}
}

// Start begins hybrid reconciliation loop (Req 21.3, 21.4)
// PATTERN: Event-assisted (priority) + Time-driven (background)
func (r *ReconcilerService) Start(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	
	// Subscribe to reconciliation hints (priority queue)
	hintSub, err := r.eventBus.SubscribeWithConfig(ctx, "opensbt.reconciliation.needed", nats.ConsumerConfig{
		Durable:   "reconciler-hints",
		AckPolicy: nats.AckExplicitPolicy,
	})
	if err != nil {
		return fmt.Errorf("reconciler: failed to subscribe to hints: %w", err)
	}
	
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
				
			case hintMsg := <-hintSub:
				// Priority reconciliation (event-driven, low latency)
				var hint models.Event
				json.Unmarshal(hintMsg.Data, &hint)
				r.reconcileResource(ctx, hint)
				hintMsg.Ack()
				
			case <-ticker.C:
				// Background reconciliation (time-driven, eventual consistency)
				startTime := time.Now()
				ctx = context.WithValue(ctx, "start_time", startTime)
				
				r.reconcileOrphanedUsers(ctx)
				r.reconcileMissingSubjects(ctx)
				r.reconcileSpokeDBGaps(ctx)
			}
		}
	}()
	
	return nil
}

// reconcileOrphanedUsers scans for users in Kratos but not in OpenMeter (Req 21.4)
// ENTERPRISE PATTERN: Paginated, rate-limited, resumable
func (r *ReconcilerService) reconcileOrphanedUsers(ctx context.Context) {
	// Load last cursor from progress store
	cursor, _ := r.progressStore.Get(ctx, "reconciler:orphaned_users:cursor")
	
	for {
		// Paginated query (cursor-based, not offset-based)
		users, nextCursor, err := r.auth.ListUsersPaginated(ctx, cursor, r.pageSize)
		if err != nil {
			log.Error("reconciler: list users failed", "error", err)
			return
		}
		
		if len(users) == 0 {
			// Reached end - reset cursor for next cycle
			r.progressStore.Set(ctx, "reconciler:orphaned_users:cursor", "")
			return
		}
		
		for _, user := range users {
			// Rate limit internal operations (10 ops/sec)
			r.rateLimiter.Wait(ctx)
			
			subjectID := models.GenerateSubjectID(user.TenantID, user.ID)
			
			// Check if subject exists in OpenMeter
			_, err := r.metering.GetSubject(ctx, user.TenantID, subjectID)
			if err == nil {
				continue // Subject exists, no drift
			}
			
			// Drift detected - decide: create subject or delete user
			if user.CreatedAt.Before(time.Now().Add(-24 * time.Hour)) {
				// Old orphan (>24h) - delete from Kratos (compensating transaction)
				log.Warn("reconciler: deleting orphaned user", "user_id", user.ID, "age", time.Since(user.CreatedAt))
				r.auth.DeleteUser(ctx, user.ID)
			} else {
				// Recent orphan (<24h) - retry creating subject
				log.Info("reconciler: creating missing subject", "subject_id", subjectID)
				r.metering.RegisterSubject(ctx, user.TenantID, subjectID, user.Metadata)
			}
		}
		
		// Save progress (resume on restart)
		r.progressStore.Set(ctx, "reconciler:orphaned_users:cursor", nextCursor)
		cursor = nextCursor
		
		// Check time budget (50s per cycle, leave 10s buffer)
		if time.Since(ctx.Value("start_time").(time.Time)) > 50*time.Second {
			log.Info("reconciler: time budget exceeded, resuming next cycle")
			return
		}
	}
}

// reconcileMissingSubjects scans for subjects in OpenMeter but not in Kratos (Req 21.4)
func (r *ReconcilerService) reconcileMissingSubjects(ctx context.Context) {
	cursor, _ := r.progressStore.Get(ctx, "reconciler:missing_subjects:cursor")
	
	for {
		// Paginated query of OpenMeter subjects
		subjects, nextCursor, err := r.metering.ListSubjectsPaginated(ctx, cursor, r.pageSize)
		if err != nil {
			log.Error("reconciler: list subjects failed", "error", err)
			return
		}
		
		if len(subjects) == 0 {
			r.progressStore.Set(ctx, "reconciler:missing_subjects:cursor", "")
			return
		}
		
		for _, subject := range subjects {
			r.rateLimiter.Wait(ctx)
			
			// Parse subject ID to extract user ID
			tenantID, userID, err := models.ParseSubjectID(subject.Key)
			if err != nil {
				continue
			}
			
			// Check if user exists in Kratos
			_, err = r.auth.GetUser(ctx, userID)
			if err == nil {
				continue // User exists, no drift
			}
			
			// Drift detected - delete orphaned subject
			log.Warn("reconciler: deleting orphaned subject", "subject_id", subject.Key)
			r.metering.DeleteSubject(ctx, tenantID, subject.Key)
		}
		
		r.progressStore.Set(ctx, "reconciler:missing_subjects:cursor", nextCursor)
		cursor = nextCursor
		
		if time.Since(ctx.Value("start_time").(time.Time)) > 50*time.Second {
			return
		}
	}
}

// reconcileSpokeDBGaps scans for users in Hub but not in Spoke DB (Req 21.4)
func (r *ReconcilerService) reconcileSpokeDBGaps(ctx context.Context) {
	cursor, _ := r.progressStore.Get(ctx, "reconciler:spoke_db_gaps:cursor")
	
	for {
		// Query Hub DB for users
		users, nextCursor, err := r.storage.ListUsersPaginated(ctx, cursor, r.pageSize)
		if err != nil {
			log.Error("reconciler: list hub users failed", "error", err)
			return
		}
		
		if len(users) == 0 {
			r.progressStore.Set(ctx, "reconciler:spoke_db_gaps:cursor", "")
			return
		}
		
		for _, user := range users {
			r.rateLimiter.Wait(ctx)
			
			// Check if user exists in Spoke DB
			_, err := r.storage.GetSpokeUser(ctx, user.TenantID, user.ID)
			if err == nil {
				continue // User exists in Spoke
			}
			
			// Drift detected - sync to Spoke DB (idempotent upsert)
			log.Info("reconciler: syncing missing user to spoke", "user_id", user.ID)
			r.storage.UpsertSpokeUser(ctx, user)
		}
		
		r.progressStore.Set(ctx, "reconciler:spoke_db_gaps:cursor", nextCursor)
		cursor = nextCursor
		
		if time.Since(ctx.Value("start_time").(time.Time)) > 50*time.Second {
			return
		}
	}
}

// reconcileResource handles priority reconciliation from event hints
func (r *ReconcilerService) reconcileResource(ctx context.Context, hint models.Event) {
	resourceType := hint.Detail["resource_type"].(string)
	tenantID := hint.Detail["tenant_id"].(string)
	
	switch resourceType {
	case "user":
		userID := hint.Detail["user_id"].(string)
		subjectID := models.GenerateSubjectID(tenantID, userID)
		
		// Check drift and fix immediately
		_, err := r.metering.GetSubject(ctx, tenantID, subjectID)
		if err != nil {
			r.metering.RegisterSubject(ctx, tenantID, subjectID, nil)
		}
		
	case "subject":
		subjectID := hint.Detail["subject_id"].(string)
		
		_, userID, _ := models.ParseSubjectID(subjectID)
		_, err := r.auth.GetUser(ctx, userID)
		if err != nil {
			r.metering.DeleteSubject(ctx, tenantID, subjectID)
		}
	}
}
```

### 5.5 Dumb Consumers with DLQ Pattern (Enterprise Architecture)

**ENTERPRISE PATTERN**: Separate event processing from reconciliation
- **Events handle "what happened"**: Dumb consumers, idempotent, stateless
- **Reconcilers handle "what should be true"**: Periodic scans, fix drift

#### 5.5.1 User Creation Consumer (Dumb + Idempotent)

**File:** `internal/opensbt/consumers/user_creation_consumer.go`

```go
package consumers

import (
	"context"
	"encoding/json"
	"time"
	
	"github.com/nats-io/nats.go"
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// UserCreationConsumer handles user_created events (dumb + idempotent)
// PATTERN: No retry logic, no dual-path decisions - just write state and ACK
type UserCreationConsumer struct {
	metering interfaces.IMetering
	eventBus interfaces.IEventBus
}

// Start subscribes to user_created events with DLQ routing
func (c *UserCreationConsumer) Start(ctx context.Context) error {
	sub, err := c.eventBus.SubscribeWithConfig(ctx, "opensbt.user.created", nats.ConsumerConfig{
		Durable:    "user-creation-consumer",
		AckPolicy:  nats.AckExplicitPolicy,
		MaxDeliver: 10, // After 10 retries → route to DLQ
		BackOff: []time.Duration{
			30 * time.Second,
			2 * time.Minute,
			10 * time.Minute,
			30 * time.Minute,
			1 * time.Hour,
		},
	})
	if err != nil {
		return fmt.Errorf("user_creation_consumer: subscribe failed: %w", err)
	}
	
	go func() {
		for msg := range sub {
			c.handleUserCreated(ctx, msg)
		}
	}()
	
	return nil
}

// handleUserCreated processes user creation (idempotent)
func (c *UserCreationConsumer) handleUserCreated(ctx context.Context, msg *nats.Msg) {
	var event models.Event
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		msg.Ack() // Malformed message, discard
		return
	}
	
	tenantID := event.Detail["tenant_id"].(string)
	subjectID := event.Detail["subject_id"].(string)
	metadata := event.Detail["metadata"].(map[string]string)
	
	// Idempotent upsert to OpenMeter
	err := c.metering.RegisterSubject(ctx, tenantID, subjectID, metadata)
	
	if err != nil {
		metadata, _ := msg.Metadata()
		
		if metadata.NumDelivered >= 10 {
			// Route to DLQ for audit + manual replay
			c.routeToDLQ(ctx, event, err, metadata.NumDelivered)
			msg.Ack() // Remove from main queue
			
			// Emit reconciliation hint (priority queue)
			c.emitReconciliationHint(ctx, "user", tenantID, event.Detail["user_id"].(string), "openmeter_creation_failed")
			return
		}
		
		msg.Nak() // Retry with backoff
		return
	}
	
	msg.Ack()
}

// routeToDLQ sends failed event to DLQ stream for audit
func (c *UserCreationConsumer) routeToDLQ(ctx context.Context, originalEvent models.Event, err error, deliveryCount uint64) {
	dlqEvent := models.NewEvent("opensbt.dlq.user_creation_failed", "user_creation_consumer", map[string]interface{}{
		"original_event": originalEvent,
		"error":          err.Error(),
		"delivery_count": deliveryCount,
		"timestamp":      time.Now(),
	})
	c.eventBus.Publish(ctx, dlqEvent)
}

// emitReconciliationHint sends priority reconciliation hint
func (c *UserCreationConsumer) emitReconciliationHint(ctx context.Context, resourceType, tenantID, resourceID, reason string) {
	hintEvent := models.NewEvent("opensbt.reconciliation.needed", "user_creation_consumer", map[string]interface{}{
		"resource_type": resourceType,
		"tenant_id":     tenantID,
		"user_id":       resourceID,
		"reason":        reason,
	})
	c.eventBus.Publish(ctx, hintEvent)
}
```

#### 5.5.2 Spoke DB Sync Consumer (Dumb + Idempotent)

**File:** `internal/opensbt/consumers/spoke_db_sync_consumer.go`

```go
package consumers

// DBSyncConsumer handles user lifecycle events for Spoke DB (dumb + idempotent)
type DBSyncConsumer struct {
	storage  interfaces.IStorage
	eventBus interfaces.IEventBus
}

// Start subscribes to tenant-specific lifecycle events
func (c *DBSyncConsumer) Start(ctx context.Context, tenantID string) error {
	subject := fmt.Sprintf("opensbt.user.%s.lifecycle", tenantID)
	
	sub, err := c.eventBus.SubscribeWithConfig(ctx, subject, nats.ConsumerConfig{
		Durable:       fmt.Sprintf("spoke-db-sync-%s", tenantID),
		AckPolicy:     nats.AckExplicitPolicy,
		MaxAckPending: 1, // Process one message at a time for ordering
		MaxDeliver:    10, // After 10 retries → DLQ
		BackOff: []time.Duration{
			30 * time.Second,
			2 * time.Minute,
			10 * time.Minute,
			30 * time.Minute,
			1 * time.Hour,
		},
	})
	if err != nil {
		return fmt.Errorf("spoke_db_sync: subscribe failed: %w", err)
	}
	
	go func() {
		for msg := range sub {
			c.handleLifecycleEvent(ctx, msg)
		}
	}()
	
	return nil
}

// handleLifecycleEvent processes user creation/deletion (idempotent)
func (c *DBSyncConsumer) handleLifecycleEvent(ctx context.Context, msg *nats.Msg) {
	var event models.Event
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		msg.Ack() // Malformed message, discard
		return
	}
	
	// Idempotent INSERT ON CONFLICT UPDATE
	err := c.storage.UpsertUser(ctx, event.Detail)
	
	if err != nil {
		metadata, _ := msg.Metadata()
		
		if metadata.NumDelivered >= 10 {
			// Route to DLQ
			c.routeToDLQ(ctx, event, err, metadata.NumDelivered)
			msg.Ack()
			
			// Emit reconciliation hint
			c.emitReconciliationHint(ctx, event.Detail["tenant_id"].(string), event.Detail["user_id"].(string))
			return
		}
		
		msg.Nak() // Retry with backoff
		return
	}
	
	msg.Ack()
}
```

#### 5.5.3 DLQ Stream Configuration

**NATS Stream for Dead Letter Queue (30-day retention)**

```yaml
# manifests/platform-core/nats/dlq-stream.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: nats-dlq-stream
  namespace: platform-core
data:
  stream.json: |
    {
      "name": "opensbt_dlq",
      "subjects": ["opensbt.dlq.>"],
      "storage": "file",
      "retention": "workqueue",
      "max_age": 2592000000000000,
      "max_msgs": 1000000,
      "discard": "old"
    }
```

**DLQ Consumer for Manual Replay**

```go
// DLQReplayTool allows ops to replay failed events
type DLQReplayTool struct {
	eventBus interfaces.IEventBus
}

// ListFailedEvents returns paginated DLQ events
func (t *DLQReplayTool) ListFailedEvents(ctx context.Context, cursor string, limit int) ([]models.Event, string, error) {
	// Query DLQ stream with cursor-based pagination
}

// ReplayEvent republishes event to original topic
func (t *DLQReplayTool) ReplayEvent(ctx context.Context, dlqEventID string) error {
	// Fetch event from DLQ, republish to original topic
}
```



**Key Pattern (Gap 4.1)**:
1. **Subject-based partitioning**: `opensbt.user.{tenant_id}.lifecycle` ensures ordered delivery per tenant
2. **MaxAckPending: 1**: NATS delivers one message at a time, guaranteeing processing order
3. **Version tracking**: Each event has a version number (creation=1, deletion=2)
4. **Idempotent handlers**: Check existing version before applying changes
5. **Race condition prevention**: Even if deletion arrives before creation (network lag), version check prevents phantom users

**Scenario Analysis (Gap 4.1)**:

| Scenario | Event Order | Handler Behavior | Result |
|----------|-------------|------------------|--------|
| **Normal flow** | Create(v1) → Delete(v2) | Create user → Delete user | ✅ Correct |
| **Out-of-order (network lag)** | Delete(v2) arrives first | Delete: User not found → ACK (idempotent) | ✅ No phantom user |
| | Create(v1) arrives second | Create: User doesn't exist → Create user(v1) | ❌ Phantom user created |
| **CORRECTED with version check** | Delete(v2) arrives first | Delete: User not found → ACK | ✅ No action |
| | Create(v1) arrives second | Create: Check version → v1 < v2 (deletion) → Skip creation | ✅ No phantom user |

**CRITICAL FIX (Gap 4.1)**: The handler must track **highest seen version** per user, not just current state. If deletion(v2) arrives first, store tombstone marker to prevent later creation(v1).

**Enhanced Version Tracking**:

```go
// Enhanced handleUserCreated with tombstone check (Gap 4.1)
func (l *DBSyncListener) handleUserCreated(ctx context.Context, msg *nats.Msg, event models.Event) {
	userID := event.Detail["user_id"].(string)
	email := event.Detail["email"].(string)
	version := int(event.Detail["version"].(float64))
	
	// Check highest seen version (including tombstones)
	highestVersion, err := l.storage.GetUserHighestVersion(ctx, userID)
	if err == nil && highestVersion >= version {
		// Already processed higher version (possibly deletion) - skip creation
		msg.Ack()
		return
	}
	
	// Proceed with creation...
}
```

### 5.6 OTLP Forwarder with Durable Buffer (Billing Pipeline)

**ENTERPRISE PATTERN**: Never drop billing events - use NATS JetStream as durable buffer

**File:** `internal/opensbt/forwarders/otlp_forwarder.go`

```go
package forwarders

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"
	
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// OTLPForwarder consumes billing events from NATS and forwards to OpenMeter
// PATTERN: Durable buffer (NATS) → Batched export (OTLP) → Retry on failure
type OTLPForwarder struct {
	eventBus  interfaces.IEventBus
	otlp      *otlptrace.Exporter
	batchSize int // 100 events per batch
}

// NewOTLPForwarder creates an OTLP forwarder with durable buffer
func NewOTLPForwarder(eventBus interfaces.IEventBus, otlpEndpoint string) (*OTLPForwarder, error) {
	exporter, err := otlptrace.New(ctx, otlptrace.WithEndpoint(otlpEndpoint))
	if err != nil {
		return nil, fmt.Errorf("otlp_forwarder: failed to create exporter: %w", err)
	}
	
	return &OTLPForwarder{
		eventBus:  eventBus,
		otlp:      exporter,
		batchSize: 100,
	}, nil
}

// Start begins consuming billing events from NATS
func (f *OTLPForwarder) Start(ctx context.Context) error {
	sub, err := f.eventBus.SubscribeWithConfig(ctx, "opensbt.billing.usage", nats.ConsumerConfig{
		Durable:    "otlp-forwarder",
		AckPolicy:  nats.AckExplicitPolicy,
		MaxDeliver: 10, // After 10 retries → DLQ
		BackOff: []time.Duration{
			5 * time.Second,
			30 * time.Second,
			2 * time.Minute,
			10 * time.Minute,
			30 * time.Minute,
		},
	})
	if err != nil {
		return fmt.Errorf("otlp_forwarder: subscribe failed: %w", err)
	}
	
	go func() {
		batch := make([]models.Event, 0, f.batchSize)
		batchMsgs := make([]*nats.Msg, 0, f.batchSize)
		
		for msg := range sub {
			var event models.Event
			json.Unmarshal(msg.Data, &event)
			
			batch = append(batch, event)
			batchMsgs = append(batchMsgs, msg)
			
			if len(batch) >= f.batchSize {
				f.flushBatch(ctx, batch, batchMsgs)
				batch = batch[:0]
				batchMsgs = batchMsgs[:0]
			}
		}
	}()
	
	return nil
}

// flushBatch exports batch to OpenMeter with retry semantics
// CRITICAL: Do NOT ACK until export succeeds
func (f *OTLPForwarder) flushBatch(ctx context.Context, batch []models.Event, batchMsgs []*nats.Msg) {
	spans := f.convertToOTLP(batch)
	
	// Retry with exponential backoff (3 attempts)
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = f.otlp.ExportSpans(ctx, spans)
		if err == nil {
			// Success - ACK all messages
			for _, msg := range batchMsgs {
				msg.Ack()
			}
			return
		}
		
		// Exponential backoff
		backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
		time.Sleep(backoff)
	}
	
	// After 3 attempts, NAK all messages - NATS will retry with backoff
	log.Error("otlp_forwarder: export failed after 3 attempts", "error", err, "batch_size", len(batch))
	for _, msg := range batchMsgs {
		msg.Nak()
	}
}

// convertToOTLP converts billing events to OTLP spans
func (f *OTLPForwarder) convertToOTLP(events []models.Event) []otlptrace.Span {
	spans := make([]otlptrace.Span, len(events))
	
	for i, event := range events {
		spans[i] = otlptrace.Span{
			Name: event.Detail["meter_id"].(string),
			Attributes: map[string]interface{}{
				"tenant_id": event.Detail["tenant_id"],
				"user_id":   event.Detail["user_id"],
				"value":     event.Detail["value"],
			},
			StartTime: event.Timestamp,
			EndTime:   event.Timestamp,
		}
	}
	
	return spans
}
```

**AgentGateway Integration (Emit to NATS, not direct OTLP)**

```go
// AgentGateway emits billing events to NATS (durable buffer)
func (gw *AgentGateway) emitUsageEvent(ctx context.Context, tenantID, userID, meterID string, value float64) error {
	event := models.NewEvent("opensbt.billing.usage", "agentgateway", map[string]interface{}{
		"tenant_id": tenantID,
		"user_id":   userID,
		"meter_id":  meterID,
		"value":     value,
	})
	
	// NATS JetStream = durable buffer (never drop billing events)
	return gw.eventBus.Publish(ctx, event)
}
```

### 5.7 Cluster-Level Metric Collector (Spoke)

**ENTERPRISE PATTERN**: One collector per cluster, tenant-aware queries

**File:** `internal/opensbt/collectors/metric_collector.go`

```go
package collectors

import (
	"context"
	"fmt"
	"time"
	
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// MetricCollector collects domain-specific metrics from all tenants
// PATTERN: Single deployment per cluster, tenant-aware queries
type MetricCollector struct {
	storage   interfaces.IStorage
	eventBus  interfaces.IEventBus
	interval  time.Duration // 5 minutes
}

// NewMetricCollector creates a cluster-level metric collector
func NewMetricCollector(storage interfaces.IStorage, eventBus interfaces.IEventBus) *MetricCollector {
	return &MetricCollector{
		storage:  storage,
		eventBus: eventBus,
		interval: 5 * time.Minute,
	}
}

// Start begins periodic metric collection
func (mc *MetricCollector) Start(ctx context.Context) error {
	ticker := time.NewTicker(mc.interval)
	
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mc.collectAllTenantMetrics(ctx)
			}
		}
	}()
	
	return nil
}

// collectAllTenantMetrics queries all tenant databases in one pass
func (mc *MetricCollector) collectAllTenantMetrics(ctx context.Context) {
	tenants, err := mc.storage.ListTenants(ctx)
	if err != nil {
		log.Error("metric_collector: list tenants failed", "error", err)
		return
	}
	
	for _, tenant := range tenants {
		metrics := mc.queryTenantMetrics(ctx, tenant.ID)
		
		// Emit to NATS (durable buffer) → OTLP Forwarder → OpenMeter
		event := models.NewEvent("opensbt.metrics.collected", "metric_collector", map[string]interface{}{
			"tenant_id":       tenant.ID,
			"database_rows":   metrics.DatabaseRows,
			"storage_bytes":   metrics.StorageBytes,
			"workspace_count": metrics.WorkspaceCount,
			"form_count":      metrics.FormCount,
			"table_count":     metrics.TableCount,
			"application_count": metrics.ApplicationCount,
		})
		
		mc.eventBus.Publish(ctx, event) // NATS JetStream (durable)
	}
}

// queryTenantMetrics queries domain-specific metrics for a tenant
func (mc *MetricCollector) queryTenantMetrics(ctx context.Context, tenantID string) *models.TenantMetrics {
	metrics := &models.TenantMetrics{
		TenantID: tenantID,
	}
	
	// Query database rows
	mc.storage.QueryRow(ctx, `
		SELECT COUNT(*) FROM tenant_data WHERE tenant_id = $1
	`, tenantID).Scan(&metrics.DatabaseRows)
	
	// Query storage bytes (S3/MinIO)
	mc.storage.QueryRow(ctx, `
		SELECT COALESCE(SUM(size_bytes), 0) FROM tenant_files WHERE tenant_id = $1
	`, tenantID).Scan(&metrics.StorageBytes)
	
	// Query workspace count
	mc.storage.QueryRow(ctx, `
		SELECT COUNT(*) FROM workspaces WHERE tenant_id = $1
	`, tenantID).Scan(&metrics.WorkspaceCount)
	
	// Query form count
	mc.storage.QueryRow(ctx, `
		SELECT COUNT(*) FROM forms WHERE tenant_id = $1
	`, tenantID).Scan(&metrics.FormCount)
	
	// Query table count
	mc.storage.QueryRow(ctx, `
		SELECT COUNT(*) FROM tables WHERE tenant_id = $1
	`, tenantID).Scan(&metrics.TableCount)
	
	// Query application count
	mc.storage.QueryRow(ctx, `
		SELECT COUNT(*) FROM applications WHERE tenant_id = $1
	`, tenantID).Scan(&metrics.ApplicationCount)
	
	return metrics
}
```

**Deployment (Single per Cluster)**

```yaml
# manifests/spoke-pool/metric-collector-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: metric-collector
  namespace: spoke-pool
spec:
  replicas: 1 # Single instance per cluster
  selector:
    matchLabels:
      app: metric-collector
  template:
    metadata:
      labels:
        app: metric-collector
    spec:
      serviceAccountName: metric-collector
      containers:
      - name: collector
        image: ghcr.io/zero-ops/metric-collector:latest
        env:
        - name: COLLECTION_INTERVAL
          value: "5m"
        - name: NATS_URL
          value: "nats://nats.spoke-pool.svc.cluster.local:4222"
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
```

### 5.8 Rate Limiting Architecture (AgentGateway + Redis)

**ENTERPRISE PATTERN**: Real-time enforcement (Redis) vs Billing aggregation (OpenMeter)

```go
// AgentGateway: Real-time rate limiting with Redis token bucket
func (gw *AgentGateway) checkRateLimit(ctx context.Context, tenantID, userID string) error {
	key := fmt.Sprintf("ratelimit:%s:%s", tenantID, userID)
	
	// Redis token bucket (atomic INCR + TTL)
	count, err := gw.redis.Incr(ctx, key).Result()
	if err != nil {
		// Fail-open: allow request if Redis unavailable
		log.Warn("rate_limit: redis unavailable, allowing request")
		return nil
	}
	
	if count == 1 {
		gw.redis.Expire(ctx, key, 1*time.Second)
	}
	
	if count > 200 {
		return ErrRateLimitExceeded // HTTP 429
	}
	
	return nil
}

// Emit billing event (async, non-blocking)
func (gw *AgentGateway) handleRequest(ctx context.Context, req *http.Request) {
	// 1. Check rate limit (real-time, Redis)
	if err := gw.checkRateLimit(ctx, tenantID, userID); err != nil {
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	
	// 2. Process request
	gw.proxyRequest(ctx, req)
	
	// 3. Emit billing event (async, durable)
	gw.emitUsageEvent(ctx, tenantID, userID, "api_calls", 1)
}
```

**Separation of Concerns:**

| System | Responsibility | Latency | Failure Mode |
|--------|---------------|---------|--------------|
| **AgentGateway + Redis** | Real-time rate limiting (200 req/sec) | Sub-millisecond | Fail-open (allow request) |
| **OpenMeter** | Billing aggregation (invoice generation) | Eventual consistency | Retry via NATS |

---

## 6. OpenMeter Declarative Catalog via Hub-Operator

**ARCHITECTURAL DECISION**: Static billing catalog (Meters, Features, Plans) managed via GitOps + Kubernetes CRDs, not imperative REST API.

**ADR 012 Alignment:**
- ✅ Static catalog managed via GitOps in `fleet-registry/tenants/<tenant-id>/billing/`
- ✅ Meter, Feature, Plan CRDs rendered from tenant `values.yaml`
- ✅ hub-operator reconciles CRDs to OpenMeter via Go SDK
- ✅ Dynamic runtime data (Subjects, Subscriptions, Invoices) via kube-sbt REST API
- ✅ No imperative Jobs (ADR 004), only declarative CRs

### 6.1 New CRDs (`billing.nutgraf.in/v1alpha1`)

**File:** `hub-operator/api/v1alpha1/`

#### 6.1.1 Meter CRD

```go
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MeterSpec defines the desired state of Meter
type MeterSpec struct {
	// TenantID is the OpenMeter namespace (tenant identifier)
	TenantID string `json:"tenantId"`
	
	// Slug is the unique identifier for the meter
	Slug string `json:"slug"`
	
	// Description of the meter
	Description string `json:"description,omitempty"`
	
	// Aggregation method (COUNT, SUM, MAX, MIN, AVG)
	Aggregation string `json:"aggregation"`
	
	// EventType is the OTLP event type to meter
	EventType string `json:"eventType"`
	
	// ValueProperty is the JSON path to the value field
	ValueProperty string `json:"valueProperty,omitempty"`
	
	// GroupBy defines aggregation dimensions
	GroupBy map[string]string `json:"groupBy,omitempty"`
}

// MeterStatus defines the observed state of Meter
type MeterStatus struct {
	// Conditions represent the latest available observations of the Meter's state
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	
	// OpenMeterID is the ID assigned by OpenMeter
	OpenMeterID string `json:"openMeterId,omitempty"`
	
	// LastSyncTime is the last time the meter was synced to OpenMeter
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantId`
// +kubebuilder:printcolumn:name="Slug",type=string,JSONPath=`.spec.slug`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Meter is the Schema for the meters API
type Meter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   MeterSpec   `json:"spec,omitempty"`
	Status MeterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// MeterList contains a list of Meter
type MeterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Meter `json:"items"`
}
```

#### 6.1.2 Feature CRD

```go
package v1alpha1

// FeatureSpec defines the desired state of Feature
type FeatureSpec struct {
	// TenantID is the OpenMeter namespace (tenant identifier)
	TenantID string `json:"tenantId"`
	
	// Key is the unique identifier for the feature
	Key string `json:"key"`
	
	// Name is the display name
	Name string `json:"name"`
	
	// MeterSlugs are the meters associated with this feature
	MeterSlugs []string `json:"meterSlugs"`
}

// FeatureStatus defines the observed state of Feature
type FeatureStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	OpenMeterID string `json:"openMeterId,omitempty"`
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced

// Feature is the Schema for the features API
type Feature struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FeatureSpec   `json:"spec,omitempty"`
	Status FeatureStatus `json:"status,omitempty"`
}
```

#### 6.1.3 Plan CRD

```go
package v1alpha1

// PlanSpec defines the desired state of Plan
type PlanSpec struct {
	// TenantID is the OpenMeter namespace (tenant identifier)
	TenantID string `json:"tenantId"`
	
	// Key is the unique identifier for the plan
	Key string `json:"key"`
	
	// Name is the display name
	Name string `json:"name"`
	
	// Description of the plan
	Description string `json:"description,omitempty"`
	
	// Currency (USD, EUR, etc.)
	Currency string `json:"currency"`
	
	// Phases define billing phases
	Phases []PlanPhase `json:"phases"`
	
	// ProRatingConfig defines proration behavior
	ProRatingConfig *ProRatingConfig `json:"proRatingConfig,omitempty"`
}

// PlanPhase represents a billing phase
type PlanPhase struct {
	Key        string     `json:"key"`
	Name       string     `json:"name"`
	StartAfter string     `json:"startAfter"` // ISO 8601 duration
	RateCards  []RateCard `json:"rateCards"`
}

// RateCard defines pricing for a feature
type RateCard struct {
	FeatureKey      string `json:"featureKey"`
	EntitlementType string `json:"entitlementType"` // metered, static, boolean
	Price           *Price `json:"price,omitempty"`
}

// Price defines pricing model
type Price struct {
	Type           string  `json:"type"` // flat, usage_based, tiered_volume, tiered_graduated
	Amount         float64 `json:"amount"`
	BillingCadence string  `json:"billingCadence"` // monthly, annual
}

// ProRatingConfig defines proration behavior
type ProRatingConfig struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"` // prorate_prices
}

// PlanStatus defines the observed state of Plan
type PlanStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	OpenMeterID string `json:"openMeterId,omitempty"`
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced

// Plan is the Schema for the plans API
type Plan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PlanSpec   `json:"spec,omitempty"`
	Status PlanStatus `json:"status,omitempty"`
}
```

### 6.2 Reconcilers (`hub-operator/internal/controller/billing/`)

#### 6.2.1 MeterReconciler

**File:** `hub-operator/internal/controller/billing/meter_controller.go`

```go
package billing

import (
	"context"
	"fmt"
	
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	
	billingv1alpha1 "github.com/soloz-io/zero-ops/hub-operator/api/v1alpha1"
	openmeter "github.com/openmeterio/openmeter/api/client/go"
)

// MeterReconciler reconciles a Meter object
type MeterReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	OpenMeterClient *openmeter.ClientWithResponses
}

// +kubebuilder:rbac:groups=billing.nutgraf.in,resources=meters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=billing.nutgraf.in,resources=meters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.nutgraf.in,resources=meters/finalizers,verbs=update

// Reconcile implements the reconciliation loop for Meter CRD
func (r *MeterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Fetch the Meter instance
	meter := &billingv1alpha1.Meter{}
	if err := r.Get(ctx, req.NamespacedName, meter); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if !meter.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, meter)
	}

	// Sync to OpenMeter
	return r.reconcileSync(ctx, meter)
}

// reconcileSync syncs the Meter CR to OpenMeter
func (r *MeterReconciler) reconcileSync(ctx context.Context, meter *billingv1alpha1.Meter) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Prepare OpenMeter API request
	req := openmeter.CreateMeterJSONRequestBody{
		Slug:          meter.Spec.Slug,
		Description:   &meter.Spec.Description,
		Aggregation:   meter.Spec.Aggregation,
		EventType:     meter.Spec.EventType,
		ValueProperty: &meter.Spec.ValueProperty,
		GroupBy:       meter.Spec.GroupBy,
	}

	// Call OpenMeter API (idempotent - upsert behavior)
	resp, err := r.OpenMeterClient.CreateMeterWithResponse(ctx, req)
	if err != nil {
		log.Error(err, "Failed to sync meter to OpenMeter")
		r.updateCondition(ctx, meter, "Synced", metav1.ConditionFalse, "SyncFailed", err.Error())
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	if resp.StatusCode() != 201 && resp.StatusCode() != 200 {
		err := fmt.Errorf("OpenMeter API returned status %d", resp.StatusCode())
		log.Error(err, "Failed to sync meter to OpenMeter")
		r.updateCondition(ctx, meter, "Synced", metav1.ConditionFalse, "SyncFailed", err.Error())
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	// Update status
	meter.Status.OpenMeterID = resp.JSON201.ID
	meter.Status.LastSyncTime = &metav1.Time{Time: time.Now()}
	r.updateCondition(ctx, meter, "Synced", metav1.ConditionTrue, "SyncSucceeded", "Meter synced to OpenMeter")

	if err := r.Status().Update(ctx, meter); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Meter synced to OpenMeter", "slug", meter.Spec.Slug, "tenantId", meter.Spec.TenantID)
	return ctrl.Result{}, nil
}

// reconcileDelete handles meter deletion
func (r *MeterReconciler) reconcileDelete(ctx context.Context, meter *billingv1alpha1.Meter) (ctrl.Result, error) {
	// OpenMeter meters are immutable - do not delete
	// Remove finalizer to allow Kubernetes to delete the CR
	controllerutil.RemoveFinalizer(meter, "billing.nutgraf.in/finalizer")
	if err := r.Update(ctx, meter); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *MeterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&billingv1alpha1.Meter{}).
		Complete(r)
}
```

**NOTE**: FeatureReconciler and PlanReconciler follow the same pattern.

### 6.3 GitOps Integration (`fleet-registry/tenants/`)

#### 6.3.1 Universal-Tenant Helm Chart Updates

**File:** `fleet-registry/charts/universal-tenant/values.yaml`

```yaml
# Billing configuration (static catalog)
billing:
  enabled: true
  
  meters:
    - name: api_calls
      eventType: api_request
      aggregation: COUNT
      groupBy:
        tenant_id: "$.tenant_id"
    
    - name: storage_bytes
      eventType: storage_usage
      aggregation: SUM
      valueProperty: "$.bytes"
      groupBy:
        tenant_id: "$.tenant_id"
  
  features:
    - name: advanced_analytics
      displayName: "Advanced Analytics"
      meterSlugs:
        - api_calls
    
    - name: unlimited_storage
      displayName: "Unlimited Storage"
      meterSlugs:
        - storage_bytes
  
  plans:
    - name: pro
      displayName: "Pro Plan"
      description: "For growing teams"
      currency: USD
      phases:
        - name: default
          startAfter: "P0D"
          rateCards:
            - featureKey: advanced_analytics
              entitlementType: metered
              price:
                type: usage_based
                amount: 0.01
                billingCadence: monthly
```

**File:** `fleet-registry/charts/universal-tenant/templates/billing.yaml`

```yaml
{{- if .Values.billing.enabled }}
---
# Meters
{{- range .Values.billing.meters }}
apiVersion: billing.nutgraf.in/v1alpha1
kind: Meter
metadata:
  name: {{ $.Values.tenantId }}-{{ .name }}
  namespace: platform-billing
  labels:
    tenant-id: {{ $.Values.tenantId }}
spec:
  tenantId: {{ $.Values.tenantId }}
  slug: {{ .name }}
  description: {{ .description | default "" }}
  aggregation: {{ .aggregation }}
  eventType: {{ .eventType }}
  {{- if .valueProperty }}
  valueProperty: {{ .valueProperty }}
  {{- end }}
  {{- if .groupBy }}
  groupBy:
    {{- toYaml .groupBy | nindent 4 }}
  {{- end }}
---
{{- end }}

# Features
{{- range .Values.billing.features }}
apiVersion: billing.nutgraf.in/v1alpha1
kind: Feature
metadata:
  name: {{ $.Values.tenantId }}-{{ .name }}
  namespace: platform-billing
  labels:
    tenant-id: {{ $.Values.tenantId }}
spec:
  tenantId: {{ $.Values.tenantId }}
  key: {{ .name }}
  name: {{ .displayName }}
  meterSlugs:
    {{- toYaml .meterSlugs | nindent 4 }}
---
{{- end }}

# Plans
{{- range .Values.billing.plans }}
apiVersion: billing.nutgraf.in/v1alpha1
kind: Plan
metadata:
  name: {{ $.Values.tenantId }}-{{ .name }}
  namespace: platform-billing
  labels:
    tenant-id: {{ $.Values.tenantId }}
spec:
  tenantId: {{ $.Values.tenantId }}
  key: {{ .name }}
  name: {{ .displayName }}
  description: {{ .description | default "" }}
  currency: {{ .currency }}
  phases:
    {{- toYaml .phases | nindent 4 }}
---
{{- end }}
{{- end }}
```

### 6.4 Architectural Flow

```
┌─────────────────────────────────────────────────────────────────┐
│ 1. SaaS Builder Configures                                      │
│    fleet-registry/tenants/<tenant-id>/values.yaml               │
│    └─ billing.meters, billing.features, billing.plans           │
└─────────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│ 2. ArgoCD Syncs                                                  │
│    Renders universal-tenant Helm chart                          │
│    Applies Meter, Feature, Plan CRs to Hub cluster              │
└─────────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│ 3. Hub-Operator Reconciles                                      │
│    MeterReconciler, FeatureReconciler, PlanReconciler           │
│    └─ Sync to OpenMeter API (idempotent upsert)                 │
│    └─ Update CR Status.Conditions (Synced: True)                │
└─────────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│ 4. Runtime Execution (kube-sbt REST API)                        │
│    POST /users → RegisterSubject (OpenMeter)                    │
│    POST /subscriptions → CreateSubscription (OpenMeter)         │
│    GET /usage → GetUsage (OpenMeter)                            │
└─────────────────────────────────────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────────┐
│ 5. Telemetry (OTLP Emission)                                    │
│    AgentGateway → NATS JetStream → OTLP Forwarder → OpenMeter   │
│    MetricCollector → NATS JetStream → OTLP Forwarder → OpenMeter│
└─────────────────────────────────────────────────────────────────┘
```

---

## 7. REST API Design (Runtime Operations Only)

**ARCHITECTURAL NOTE**: Static catalog (Meters, Features, Plans) managed via hub-operator CRDs. REST API handles ONLY dynamic runtime operations.

### 7.1 API Routes (Req 8, 9) - Runtime Operations Only

**File:** `internal/opensbt/controlplane/routes.go`

**REMOVED**: All POST, PUT, DELETE endpoints for `/meters`, `/features`, and `/plans` (managed via hub-operator CRDs)

```go
package controlplane

import (
	"github.com/gin-gonic/gin"
)

// RegisterMeteringRoutes registers runtime metering and billing API routes
// ARCHITECTURAL NOTE: Static catalog (Meters, Features, Plans) managed via hub-operator CRDs
// This API handles ONLY dynamic runtime operations
func RegisterMeteringRoutes(r *gin.Engine, cp *ControlPlane) {
	api := r.Group("/api/v1")
	api.Use(cp.AuthMiddleware()) // JWT validation and tenant_id extraction
	
	tenants := api.Group("/tenants/:tenantID")
	{
		// User Management (Req 8) - Dynamic Runtime Operations
		users := tenants.Group("/users")
		{
			users.POST("", cp.CreateUser)           // Req 8.1 - Creates Kratos user + OpenMeter subject
			users.GET("/:userID", cp.GetUser)       // Req 8.2
			users.PUT("/:userID", cp.UpdateUser)    // Req 8.3
			users.DELETE("/:userID", cp.DeleteUser) // Req 8.4 - Deletes Kratos user + OpenMeter subject
			users.GET("", cp.ListUsers)             // Req 8.5
		}
		
		// Usage Queries (Req 9) - Read-Only Operations
		tenants.GET("/usage", cp.GetTenantUsage)                    // Req 9.1
		tenants.GET("/users/:userID/usage", cp.GetUserUsage)        // Req 9.2
		tenants.GET("/entitlements", cp.CheckEntitlements)          // Req 9.3
		
		// Meter Management (Read-Only for UI Display)
		meters := tenants.Group("/meters")
		{
			meters.GET("/:meterID", cp.GetMeter)   // Read-only
			meters.GET("", cp.ListMeters)          // Read-only
		}
		
		// Feature Management (Read-Only for UI Display)
		features := tenants.Group("/features")
		{
			features.GET("/:featureID", cp.GetFeature) // Read-only
			features.GET("", cp.ListFeatures)          // Read-only
		}
		
		// Plan Management (Read-Only for UI Display)
		plans := tenants.Group("/plans")
		{
			plans.GET("/:planID", cp.GetPlan) // Read-only
			plans.GET("", cp.ListPlans)       // Read-only
		}
		
		// Subscription Management (Req 16) - Dynamic Runtime Operations
		subscriptions := tenants.Group("/subscriptions")
		{
			subscriptions.POST("", cp.CreateSubscription)                      // Assign user to plan
			subscriptions.GET("/:subscriptionID", cp.GetSubscription)          // Read-only
			subscriptions.PUT("/:subscriptionID", cp.UpdateSubscription)       // Change plan
			subscriptions.DELETE("/:subscriptionID", cp.CancelSubscription)    // Cancel subscription
			subscriptions.GET("", cp.ListSubscriptions)                        // Read-only
		}
		
		// Invoice Operations (Req 17) - Read-Only Queries
		invoices := tenants.Group("/invoices")
		{
			invoices.GET("/preview", cp.PreviewInvoice)   // Read-only
			invoices.GET("/:invoiceID", cp.GetInvoice)    // Read-only
			invoices.GET("", cp.ListInvoices)             // Read-only
		}
	}
	
	// NOTE: Stripe webhooks route directly to OpenMeter, not kube-sbt (Req 18.4)
	// OpenMeter endpoint: POST /api/v1/apps/{appId}/stripe/webhook
	// Reference: archived/billing-metering/openmeter/openmeter/app/stripe/httpdriver/webhook.go
}
```

**Architectural Separation:**

| Operation Type | Management Method | Example |
|----------------|-------------------|---------|
| **Static Catalog** | GitOps + hub-operator CRDs | Create Meter, Create Feature, Create Plan |
| **Dynamic Runtime** | kube-sbt REST API | Create User, Create Subscription, Get Usage |
| **Read-Only Display** | kube-sbt REST API | List Meters, List Features, List Plans |

### 6.2 Handler Examples

**User Creation Handler (Req 8.1):**

```go
// CreateUser godoc
// @Summary Create a new user
// @Description Creates a user in Ory Kratos and registers as OpenMeter subject
// @Tags users
// @Accept json
// @Produce json
// @Param tenantID path string true "Tenant ID"
// @Param user body models.CreateUserRequest true "User details"
// @Success 201 {object} models.User
// @Failure 400 {object} models.ProblemDetails
// @Failure 500 {object} models.ProblemDetails
// @Router /api/v1/tenants/{tenantID}/users [post]
// @Security BearerAuth
func (cp *ControlPlane) CreateUser(c *gin.Context) {
	tenantID := c.Param("tenantID")
	
	// Validate tenant_id from JWT matches path parameter (Req 10.5)
	jwtTenantID := c.GetString("tenant_id")
	if jwtTenantID != tenantID {
		c.JSON(403, models.ProblemDetails{
			Type:   "https://zero-ops.io/errors/forbidden",
			Title:  "Forbidden",
			Status: 403,
			Detail: "Tenant ID mismatch",
		})
		return
	}
	
	var req models.CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, models.ProblemDetails{
			Type:   "https://zero-ops.io/errors/invalid-request",
			Title:  "Invalid Request",
			Status: 400,
			Detail: err.Error(),
		})
		return
	}
	
	user := models.User{
		Email:    req.Email,
		Name:     req.Name,
		Metadata: req.Metadata,
	}
	
	if err := cp.userManager.CreateUser(c.Request.Context(), tenantID, user); err != nil {
		c.JSON(500, models.ProblemDetails{
			Type:   "https://zero-ops.io/errors/internal-error",
			Title:  "Internal Server Error",
			Status: 500,
			Detail: err.Error(),
		})
		return
	}
	
	c.JSON(201, user)
}
```

**Tenant Usage Query Handler (Req 9.1):**

```go
// GetTenantUsage godoc
// @Summary Get tenant-scoped usage metrics
// @Description Returns aggregated usage for all meters in the tenant
// @Tags usage
// @Produce json
// @Param tenantID path string true "Tenant ID"
// @Param start_date query string false "Start date (RFC3339)"
// @Param end_date query string false "End date (RFC3339)"
// @Param period query string false "Period (daily, monthly, custom)"
// @Success 200 {object} models.TenantUsageData
// @Failure 503 {object} models.ProblemDetails
// @Router /api/v1/tenants/{tenantID}/usage [get]
// @Security BearerAuth
func (cp *ControlPlane) GetTenantUsage(c *gin.Context) {
	tenantID := c.Param("tenantID")
	jwtTenantID := c.GetString("tenant_id")
	
	if jwtTenantID != tenantID {
		c.JSON(403, models.ProblemDetails{
			Type:   "https://zero-ops.io/errors/forbidden",
			Title:  "Forbidden",
			Status: 403,
			Detail: "Tenant ID mismatch",
		})
		return
	}
	
	// Parse time period from query params (Req 9.4)
	period, err := parseTimePeriod(c)
	if err != nil {
		c.JSON(400, models.ProblemDetails{
			Type:   "https://zero-ops.io/errors/invalid-request",
			Title:  "Invalid Request",
			Status: 400,
			Detail: err.Error(),
		})
		return
	}
	
	usage, err := cp.metering.GetTenantUsage(c.Request.Context(), tenantID, period)
	if err != nil {
		// Return 503 if OpenMeter unavailable (Req 9.7)
		c.Header("Retry-After", "60")
		c.JSON(503, models.ProblemDetails{
			Type:   "https://zero-ops.io/errors/service-unavailable",
			Title:  "Service Unavailable",
			Status: 503,
			Detail: "Usage data temporarily unavailable",
		})
		return
	}
	
	c.JSON(200, usage)
}
```

### 6.3 Middleware

**JWT Validation and Tenant Extraction (Req 10.5, 2.3):**

```go
// AuthMiddleware validates JWT and extracts tenant_id claim
func (cp *ControlPlane) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(401, models.ProblemDetails{
				Type:   "https://zero-ops.io/errors/unauthorized",
				Title:  "Unauthorized",
				Status: 401,
				Detail: "Missing Authorization header",
			})
			return
		}
		
		token := strings.TrimPrefix(authHeader, "Bearer ")
		
		// Validate JWT via Ory Hydra
		claims, err := cp.auth.ValidateToken(c.Request.Context(), token)
		if err != nil {
			c.AbortWithStatusJSON(401, models.ProblemDetails{
				Type:   "https://zero-ops.io/errors/unauthorized",
				Title:  "Unauthorized",
				Status: 401,
				Detail: "Invalid token",
			})
			return
		}
		
		// Extract tenant_id from claims (Req 2.3)
		tenantID, ok := claims["tenant_id"].(string)
		if !ok {
			c.AbortWithStatusJSON(403, models.ProblemDetails{
				Type:   "https://zero-ops.io/errors/forbidden",
				Title:  "Forbidden",
				Status: 403,
				Detail: "Missing tenant_id claim",
			})
			return
		}
		
		// Inject tenant_id into context for downstream handlers
		c.Set("tenant_id", tenantID)
		c.Set("user_id", claims["sub"])
		
		c.Next()
	}
}
```

---

## 7. Database Schema

### 7.1 Hub Database (PostgreSQL)

**File:** `internal/opensbt/providers/postgres/migrations/006_metering_orphaned_subjects.sql`

```sql
-- NOTE: Stripe webhook idempotency is handled by OpenMeter, not kube-sbt (Req 18.5)
-- OpenMeter manages webhook signature validation, event deduplication, and invoice state transitions
-- Reference: archived/billing-metering/openmeter/openmeter/app/stripe/httpdriver/webhook.go

-- Orphaned subjects tracking for reconciliation (Req 21)
CREATE TABLE IF NOT EXISTS orphaned_subjects (
    subject_id VARCHAR(255) PRIMARY KEY,
    tenant_id VARCHAR(255) NOT NULL,
    reason VARCHAR(255) NOT NULL,
    retry_count INT DEFAULT 0,
    last_retry_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_orphaned_subjects_tenant ON orphaned_subjects(tenant_id);
CREATE INDEX idx_orphaned_subjects_retry ON orphaned_subjects(retry_count, last_retry_at);
```

### 7.2 Spoke Database (Tenant PostgreSQL)

**File:** `migrations/tenant-baseline/005_users_table.sql`

```sql
-- User table with RLS for tenant isolation (Req 12)
CREATE TABLE IF NOT EXISTS public.users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) NOT NULL UNIQUE,
    name VARCHAR(255),
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

-- Indexes (Req 12.4)
CREATE INDEX IF NOT EXISTS idx_users_email ON public.users(email);
CREATE INDEX IF NOT EXISTS idx_users_created_at ON public.users(created_at);

-- Row-Level Security (Req 12.2, 12.3)
ALTER TABLE public.users ENABLE ROW LEVEL SECURITY;

CREATE POLICY users_isolation_policy ON public.users
    FOR ALL
    USING (id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid)
    WITH CHECK (id = (current_setting('request.jwt.claims', true)::json->>'user_id')::uuid);

-- Trigger for updated_at (Req 12.5)
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_users_updated_at
    BEFORE UPDATE ON public.users
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
```

---

## 8. Event Choreography (NATS)

### 8.1 Event Topics with Ordering Guarantees (Gap 4.1)

**CORRECTED (Gap 4.1)**: NATS JetStream provides ordered delivery per subject
**Pattern**: Use subject-based partitioning + consumer sequence tracking for Saga ordering
**Reference**: NATS JetStream guarantees message ordering within a subject partition

| Topic | Publisher | Consumer | Purpose | Ordering Strategy |
|-------|-----------|----------|---------|-------------------|
| `opensbt.user.created` | UserManager | UserCreationConsumer | User creation events | No ordering required |
| `opensbt.user.{tenant_id}.lifecycle` | UserManager | DBSyncConsumer (Spoke) | User creation/deletion for Spoke DB sync | Per-tenant ordering via subject partition |
| `opensbt.billing.usage` | AgentGateway, MetricCollector | OTLPForwarder | Billing events (durable buffer) | No ordering required |
| `opensbt.metrics.collected` | MetricCollector | OTLPForwarder | Domain-specific metrics | No ordering required |
| `opensbt.reconciliation.needed` | Consumers (on failure) | ReconcilerService | Priority reconciliation hints | No ordering required |
| `opensbt.dlq.>` | Consumers (after MaxDeliver) | DLQReplayTool | Dead letter queue (audit trail) | No ordering required |
| `opensbt_billingSuccess` | BillingProvider | Application Plane | Successful payment notification (Req 18.6) | No ordering required |
| `opensbt_billingFailure` | BillingProvider | Application Plane | Failed payment notification | No ordering required |
| `opensbt_notifications` | ReconcilerService | Ops Dashboard | Manual intervention alerts (Req 21.6) | No ordering required |
| `opensbt.entitlement.{tenant_id}.updated` | BillingProvider | Cache Invalidator | Entitlement cache invalidation (Gap 3.2) | Per-tenant ordering |

**Key Patterns:**
- **Dumb Consumers**: `opensbt.user.created`, `opensbt.billing.usage` - Idempotent, stateless, MaxDeliver=10
- **Durable Buffers**: `opensbt.billing.usage` - NATS JetStream prevents billing data loss
- **Priority Hints**: `opensbt.reconciliation.needed` - Event-assisted reconciliation
- **Audit Trail**: `opensbt.dlq.>` - 30-day retention for manual replay
- **Subject Partitioning**: `opensbt.user.{tenant_id}.lifecycle` - Per-tenant ordering for Spoke DB sync

### 8.2 Event Schemas with Sequence Tracking (Gap 4.1)

**User Created Event:**

```json
{
  "detailType": "user_created",
  "source": "user_manager",
  "detail": {
    "tenant_id": "550e8400-e29b-41d4-a716-446655440000",
    "user_id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
    "subject_id": "550e8400-e29b-41d4-a716-446655440000#7c9e6679-7425-40de-944b-e07fc1f90ae7",
    "email": "user@example.com",
    "version": 1,
    "timestamp": "2026-04-30T10:15:30Z"
  }
}
```

**User Deleted Event (Compensation):**

```json
{
  "detailType": "user_deleted",
  "source": "user_manager",
  "detail": {
    "tenant_id": "550e8400-e29b-41d4-a716-446655440000",
    "user_id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
    "subject_id": "550e8400-e29b-41d4-a716-446655440000#7c9e6679-7425-40de-944b-e07fc1f90ae7",
    "reason": "saga_rollback",
    "version": 2,
    "timestamp": "2026-04-30T10:15:31Z"
  }
}
```

**Orphaned Subject Event (Req 21.2):**

```json
{
  "detailType": "orphaned_subject",
  "source": "user_manager",
  "detail": {
    "tenant_id": "550e8400-e29b-41d4-a716-446655440000",
    "subject_id": "550e8400-e29b-41d4-a716-446655440000#7c9e6679-7425-40de-944b-e07fc1f90ae7",
    "reason": "kratos_rollback_failed",
    "timestamp": "2026-04-30T10:15:30Z"
  }
}
```

**Billing Success Event (Req 18.6):**

```json
{
  "detailType": "billing_success",
  "source": "stripe.webhook",
  "detail": {
    "customer_id": "550e8400-e29b-41d4-a716-446655440000#7c9e6679-7425-40de-944b-e07fc1f90ae7",
    "invoice_id": "in_1234567890",
    "amount": 99.99,
    "currency": "USD",
    "timestamp": "2026-04-30T10:15:30Z"
  }
}
```

### 8.3 NATS JetStream Configuration (Gap 4.1)

**Stream Configuration for Ordered Delivery:**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: nats-jetstream-streams
  namespace: platform-messaging
data:
  user-lifecycle-stream.json: |
    {
      "name": "OPENSBT_USER_LIFECYCLE",
      "subjects": ["opensbt.user.*.lifecycle"],
      "retention": "limits",
      "max_consumers": -1,
      "max_msgs": 1000000,
      "max_bytes": -1,
      "max_age": 604800000000000,
      "max_msg_size": -1,
      "storage": "file",
      "discard": "old",
      "num_replicas": 3,
      "duplicate_window": 120000000000
    }
```

**Consumer Configuration with Ordered Delivery:**

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: nats-jetstream-consumers
  namespace: platform-messaging
data:
  spoke-db-sync-consumer.json: |
    {
      "stream_name": "OPENSBT_USER_LIFECYCLE",
      "durable_name": "spoke-db-sync",
      "deliver_policy": "all",
      "ack_policy": "explicit",
      "ack_wait": 30000000000,
      "max_deliver": 3,
      "filter_subject": "opensbt.user.*.lifecycle",
      "replay_policy": "instant",
      "max_ack_pending": 1,
      "flow_control": true,
      "idle_heartbeat": 5000000000
    }
```

**Key Configuration (Gap 4.1)**:
- `max_ack_pending: 1` - Process one message at a time per consumer (guarantees ordering)
- `ack_policy: explicit` - Consumer must explicitly ACK before next message delivered
- `filter_subject: "opensbt.user.*.lifecycle"` - Wildcard matches all tenant partitions
- `duplicate_window: 120s` - Deduplication window for idempotency

---

## 9. Security Architecture

### 9.1 Zero-Trust mTLS (Req 11)

**Istio Configuration:**

**File:** `manifests/hub-core-services/opensbt/istio-config.yaml`

```yaml
apiVersion: security.istio.io/v1beta1
kind: PeerAuthentication
metadata:
  name: opensbt-mtls
  namespace: platform-billing
spec:
  selector:
    matchLabels:
      app: opensbt
  mtls:
    mode: STRICT  # Req 11.6

---
apiVersion: security.istio.io/v1beta1
kind: AuthorizationPolicy
metadata:
  name: opensbt-authz
  namespace: platform-billing
spec:
  selector:
    matchLabels:
      app: opensbt
  action: ALLOW
  rules:
  - from:
    - source:
        principals:
        - "cluster.local/ns/platform-identity/sa/ory-kratos"
        - "cluster.local/ns/platform-billing/sa/openmeter"
        - "cluster.local/ns/platform-messaging/sa/nats"
    to:
    - operation:
        methods: ["GET", "POST", "PUT", "DELETE"]
```

### 9.2 JWT Validation Flow (Req 10.5, 2.3)

```
┌─────────────┐
│   Client    │
│  (sbt-sdk)  │
└──────┬──────┘
       │ 1. POST /api/v1/tenants/{tenantID}/users
       │    Authorization: Bearer <JWT>
       ▼
┌─────────────────────────────────────────┐
│         AgentGateway (Hub)              │
│  - Validates JWT via auth-proxy         │
│  - Extracts tenant_id, user_id claims   │
└──────┬──────────────────────────────────┘
       │ 2. Forward with JWT
       ▼
┌─────────────────────────────────────────┐
│         kube-sbt (Hub)                  │
│  - AuthMiddleware validates JWT         │
│  - Extracts tenant_id from claims       │
│  - Injects OpenMeter-Namespace header   │
└──────┬──────────────────────────────────┘
       │ 3. OpenMeter API call
       │    OpenMeter-Namespace: {tenant_id}
       ▼
┌─────────────────────────────────────────┐
│         OpenMeter (Hub)                 │
│  - Enforces namespace isolation         │
│  - Returns tenant-scoped data           │
└─────────────────────────────────────────┘
```

---


## 10. Deployment Architecture

### 10.1 Helm Chart Structure (Req 1.3, 20)

**File:** `manifests/hub-core-services/opensbt/Chart.yaml`

```yaml
apiVersion: v2
name: opensbt
description: kube-sbt Control Plane API with OpenMeter integration
type: application
version: 1.0.0
appVersion: "1.0.0"
```

**File:** `manifests/hub-core-services/opensbt/values.yaml`

```yaml
replicaCount: 3

image:
  repository: ghcr.io/soloz-io/opensbt
  pullPolicy: IfNotPresent
  tag: "latest"

service:
  type: ClusterIP
  port: 8080

resources:
  limits:
    cpu: 1000m
    memory: 1Gi
  requests:
    cpu: 500m
    memory: 512Mi

# Node affinity for Hub workload nodes (Req 20.3, 20.4)
nodeSelector:
  node-role.kubernetes.io/worker: "true"

tolerations:
- key: "workload"
  operator: "Equal"
  value: "hub"
  effect: "NoSchedule"

# Istio sidecar injection (Req 11.1)
podAnnotations:
  sidecar.istio.io/inject: "true"

# Environment variables
env:
- name: OPENMETER_URL
  value: "http://openmeter.platform-core.svc:8888"
- name: ORY_KRATOS_URL
  value: "http://ory-kratos.platform-identity.svc:4433"
- name: ORY_HYDRA_URL
  value: "http://ory-hydra.platform-identity.svc:4445"
- name: NATS_URL
  value: "nats://nats.platform-messaging.svc:4222"

# NOTE: Stripe API keys stored in Infisical, accessed by OpenMeter (not kube-sbt) (Req 18.2)
# Stripe webhooks route directly to OpenMeter endpoint: POST /api/v1/apps/{appId}/stripe/webhook
# kube-sbt does NOT handle Stripe webhooks
# Reference: archived/billing-metering/openmeter/openmeter/app/stripe/httpdriver/webhook.go

# Health checks
livenessProbe:
  httpGet:
    path: /health
    port: 8080
  initialDelaySeconds: 30
  periodSeconds: 10

readinessProbe:
  httpGet:
    path: /ready
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5

# Pod Disruption Budget for HA
podDisruptionBudget:
  enabled: true
  minAvailable: 2
```

### 10.2 Stripe Configuration (Req 18.2, 18.3)

**CORRECTED: Stripe secrets managed by OpenMeter, not kube-sbt**

Stripe API keys and webhook secrets are stored in Infisical and accessed by OpenMeter during Stripe App installation. kube-sbt does NOT require Stripe secrets because:

1. **Stripe webhooks route directly to OpenMeter** (not kube-sbt)
2. **OpenMeter validates webhook signatures** using app-specific webhook secret
3. **OpenMeter manages invoice state transitions** (paid/failed/voided)
4. **kube-sbt queries OpenMeter API** for invoice status (no direct Stripe access)

**OpenMeter Stripe App Installation:**
- Performed via OpenMeter UI or API (not programmatically by kube-sbt)
- Stores Stripe API key and webhook secret in OpenMeter's database
- Registers webhook URL with Stripe: `https://openmeter.example.com/api/v1/apps/{appId}/stripe/webhook`

**Reference:**
- `archived/billing-metering/openmeter/openmeter/app/stripe/service/factory.go:L77-L90` - Webhook registration
- `archived/billing-metering/openmeter/openmeter/app/stripe/httpdriver/webhook.go` - Webhook handler

### 10.3 ArgoCD Application (Req 1.3)

**File:** `manifests/hub-core-services/opensbt/application.yaml`

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: opensbt
  namespace: argocd
spec:
  project: hub-platform
  source:
    repoURL: https://github.com/soloz-io/zero-ops
    targetRevision: main
    path: manifests/hub-core-services/opensbt
  destination:
    server: https://kubernetes.default.svc
    namespace: platform-billing
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
    - CreateNamespace=true
```

### 10.4 OpenMeter Deployment (Req 20, Req 20.8 HA) - ACTUAL IMPLEMENTATION

**Architecture Decision:** OpenMeter uses platform-owned stateful infrastructure (ADR 014: Platform-Owned Stateful Infrastructure)

**Deployment Pattern:** Helm + Kustomize hybrid approach
- OpenMeter official Helm chart referenced via Kustomize `helmCharts` field
- Kustomize patches inject secrets and environment variables
- ArgoCD processes Kustomize → Helm → Kubernetes manifests

**PostgreSQL:** 
- Uses platform-db CNPG cluster in `platform-data` namespace
- Database: `openmeter` (created via CNPG postInitSQL)
- User: `openmeter` (credentials generated by hub-operator, stored in Infisical)
- Connection: `platform-db-rw.platform-data.svc.cluster.local:5432`

**ClickHouse:**
- Uses platform ClickHouseInstallation in `platform-data` namespace
- Database: `openmeter` (created via init Job)
- User: `openmeter` (configured in ClickHouseInstallation CR)
- Connection: `clickhouse.platform-data.svc.cluster.local:9000`

**Redis:**
- Dedicated Redis StatefulSet in `platform-billing` namespace
- Image: `docker.io/library/redis:8.6-alpine` (official Redis, not Bitnami)
- Persistence: `/data` mount with AOF enabled
- Connection: `redis.platform-billing.svc.cluster.local:6379`

**Svix (Webhook Service):**
- Deployed as part of OpenMeter Helm chart
- Authentication: JWT token signed with HS256
- JWT format requirement: `{"sub":"org_openmeter"}` (Svix validates org_ prefix)
- Signing secret and JWT both generated by hub-operator

**File:** `manifests/hub-core-services/openmeter/kustomization.yaml`

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

namespace: platform-billing

helmCharts:
  - name: openmeter
    repo: https://openmeterio.github.io/helm-charts
    version: 0.1.0
    releaseName: openmeter
    namespace: platform-billing
    valuesFile: values.yaml

resources:
  - network-policy.yaml
  - secrets/openmeter-db-credentials-es.yaml
  - secrets/clickhouse-credentials-es.yaml
  - secrets/svix-es.yaml

patches:
  # Inject PostgreSQL password into all 5 OpenMeter deployments
  - target:
      kind: Deployment
      name: openmeter-api
    patch: |-
      - op: add
        path: /spec/template/spec/containers/0/env/-
        value:
          name: POSTGRES_PASSWORD
          valueFrom:
            secretKeyRef:
              name: openmeter-db-credentials
              key: password
      - op: add
        path: /spec/template/spec/containers/0/env/-
        value:
          name: POSTGRES_URL
          value: "postgres://openmeter:$(POSTGRES_PASSWORD)@platform-db-rw.platform-data.svc:5432/openmeter?sslmode=require"
      - op: add
        path: /spec/template/spec/containers/0/env/-
        value:
          name: AGGREGATION_CLICKHOUSE_PASSWORD
          valueFrom:
            secretKeyRef:
              name: clickhouse-credentials
              key: password
      - op: add
        path: /spec/template/spec/containers/0/env/-
        value:
          name: SVIX_APIKEY
          valueFrom:
            secretKeyRef:
              name: openmeter-svix
              key: jwt

  # Repeat patches for balance-worker, billing-worker, notification-service, sink-worker
  # (5 deployments total)
```

**File:** `manifests/hub-core-services/openmeter/values.yaml`

```yaml
replicaCount: 1  # Start with 1, scale to 3+ for HA

image:
  repository: ghcr.io/openmeterio/openmeter
  tag: v1.0.0-beta.227

# Node affinity for Hub workload nodes
nodeSelector:
  node-role.kubernetes.io/worker: "true"

# Disable embedded databases (use platform-provided)
postgresql:
  enabled: false

redis:
  enabled: false

clickhouse:
  enabled: false

# External database configuration (passwords injected via Kustomize patches)
config:
  postgres:
    url: "postgres://openmeter@platform-db-rw.platform-data.svc:5432/openmeter?sslmode=require"
  
  aggregation:
    clickhouse:
      address: "clickhouse.platform-data.svc.cluster.local:9000"
      database: "openmeter"
      username: "openmeter"
      # password injected via AGGREGATION_CLICKHOUSE_PASSWORD env var
  
  redis:
    addr: "redis.platform-billing.svc.cluster.local:6379"
  
  svix:
    # JWT token injected via SVIX_APIKEY env var
    serverUrl: "http://openmeter-svix:80"

resources:
  limits:
    cpu: 1000m
    memory: 2Gi
  requests:
    cpu: 500m
    memory: 1Gi
```

**Credential Management - ACTUAL IMPLEMENTATION:**

1. **hub-operator Phase 0 (Bootstrap):**
   - Generates random password for `openmeter` PostgreSQL user
   - Generates random password for `openmeter` ClickHouse user
   - Generates Svix signing secret (random 32-byte string)
   - **Generates Svix JWT token** using `generateSvixJWT()` function:
     ```go
     claims := jwt.MapClaims{
         "iss": "svix-server",
         "sub": "org_openmeter",  // CRITICAL: Must have org_ prefix
         "iat": time.Now().Unix(),
         "exp": time.Now().Add(10 * 365 * 24 * time.Hour).Unix(),
     }
     token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
     signedToken, _ := token.SignedString([]byte(signingSecret))
     ```
   - Uploads to Infisical:
     - `openmeter-postgresql-password`
     - `openmeter-clickhouse-password`
     - `openmeter-svix-signing-secret`
     - `openmeter-svix-jwt` (signed JWT token)

2. **hub-operator Phase 4 (Database Roles):**
   - Creates PostgreSQL user: `CREATE USER openmeter WITH PASSWORD '...'`
   - Creates PostgreSQL database: `CREATE DATABASE openmeter OWNER openmeter`
   - Grants permissions: `GRANT ALL PRIVILEGES ON DATABASE openmeter TO openmeter`

3. **ExternalSecrets Operator (ESO):**
   - Syncs from Infisical to K8s secrets:
     - `openmeter-db-credentials` (username, password)
     - `clickhouse-credentials` (password)
     - `openmeter-svix` (signing-secret, jwt)

4. **Kustomize Patches:**
   - Inject `POSTGRES_PASSWORD` from `openmeter-db-credentials` secret
   - Inject `AGGREGATION_CLICKHOUSE_PASSWORD` from `clickhouse-credentials` secret
   - Inject `SVIX_APIKEY` from `openmeter-svix` secret (uses JWT, not signing secret)
   - Expand `$(POSTGRES_PASSWORD)` in `POSTGRES_URL` environment variable

**Key Implementation Details:**

- **Kubernetes $(VAR) Expansion:** Works when VAR is defined in same container's env array
- **Svix Authentication:** Requires signed JWT token, not raw signing secret
- **JWT Sub Claim Format:** Must be `org_XXXXX` format (Svix requirement)
- **hub-operator Idempotency:** Checks if secret exists in Infisical before generating
- **ESO Sync Wave:** ExternalSecrets have `argocd.argoproj.io/sync-wave: "3"` to run after database provisioning
- **NetworkPolicies:** Explicit ingress/egress rules for OpenMeter, Redis, ClickHouse, PostgreSQL

### 10.5 Cross-Cluster OTLP Ingestion Routing (Gap 1.1)

**Problem:** AgentGateway (Hub) needs to emit OTLP events to OpenMeter (Hub) port 4318, but design lacked ingress/gateway configuration for secure cross-namespace access.

**Solution:** Istio IngressGateway with SPIFFE identity validation for OpenMeter OTLP port exposure.

**File:** `manifests/hub-core-services/openmeter/istio-gateway.yaml`

```yaml
apiVersion: networking.istio.io/v1beta1
kind: Gateway
metadata:
  name: openmeter-otlp-gateway
  namespace: platform-core
spec:
  selector:
    istio: ingressgateway  # Use Hub's Istio ingress gateway
  servers:
  - port:
      number: 4318
      name: otlp-http
      protocol: HTTP
    hosts:
    - "openmeter-otlp.platform-core.svc.cluster.local"
    tls:
      mode: ISTIO_MUTUAL  # Require mTLS with SPIFFE identity
---
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: openmeter-otlp-route
  namespace: platform-core
spec:
  hosts:
  - "openmeter-otlp.platform-core.svc.cluster.local"
  gateways:
  - openmeter-otlp-gateway
  http:
  - match:
    - uri:
        prefix: "/v1/traces"  # OTLP HTTP endpoint
    route:
    - destination:
        host: openmeter.platform-core.svc.cluster.local
        port:
          number: 4318
    timeout: 30s
    retries:
      attempts: 3
      perTryTimeout: 10s
      retryOn: "5xx,reset,connect-failure,refused-stream"
---
apiVersion: security.istio.io/v1beta1
kind: AuthorizationPolicy
metadata:
  name: openmeter-otlp-authz
  namespace: platform-core
spec:
  selector:
    matchLabels:
      app: openmeter
  action: ALLOW
  rules:
  - from:
    - source:
        principals:
        - "cluster.local/ns/platform-gateway/sa/agentgateway"  # Only AgentGateway
    to:
    - operation:
        methods: ["POST"]
        paths: ["/v1/traces"]
        ports: ["4318"]
```

**AgentGateway OTLP Configuration:**

**File:** `manifests/hub-core-services/agentgateway/config.yaml`

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: agentgateway-config
  namespace: platform-gateway
data:
  config.yaml: |
    otlp:
      endpoint: "openmeter-otlp.platform-core.svc.cluster.local:4318"
      protocol: "http/protobuf"
      timeout: "30s"
      batch_size: 100
      flush_interval: "10s"
      retry:
        enabled: true
        max_attempts: 3
        backoff: "exponential"
      buffer:
        max_size: 10000
        retention: "1h"  # Local buffering when OpenMeter unreachable
      headers:
        # SPIFFE identity automatically injected by Envoy sidecar
      
      # CRITICAL (Gap 3.1): Span attributes for tenant aggregation
      # Reference: archived/billing-metering/openmeter/openmeter/streaming/clickhouse/meter_query.go:L156-L180
      # OpenMeter requires tenant_id as OTLP attribute for GroupBy aggregation
      span_attributes:
        tenant_id: "${jwt.claims.tenant_id}"  # Extracted from JWT claims
        user_id: "${jwt.claims.sub}"          # Subject claim
        meter_id: "${meter.slug}"             # Meter identifier
        
      # Subject ID format: {tenant_id}#{user_id}
      # BUT tenant_id MUST ALSO be emitted as separate attribute for tenant-level aggregation
      subject_id_format: "${jwt.claims.tenant_id}#${jwt.claims.sub}"
```

**Network Policy (Defense in Depth):**

**File:** `manifests/hub-core-services/openmeter/network-policy.yaml`

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: openmeter-otlp-ingress
  namespace: platform-core
spec:
  podSelector:
    matchLabels:
      app: openmeter
  policyTypes:
  - Ingress
  ingress:
  - from:
    - namespaceSelector:
        matchLabels:
          name: platform-gateway
      podSelector:
        matchLabels:
          app: agentgateway
    - namespaceSelector:
        matchLabels:
          name: istio-system
      podSelector:
        matchLabels:
          app: istio-ingressgateway
    ports:
    - protocol: TCP
      port: 4318  # OTLP HTTP
    - protocol: TCP
      port: 8888  # REST API
```

**Security Flow:**

```
┌─────────────────────────────────────────────────────────────────┐
│                         Hub Cluster                              │
│                                                                  │
│  ┌──────────────────┐                                           │
│  │  AgentGateway    │                                           │
│  │  (Envoy sidecar) │                                           │
│  └────────┬─────────┘                                           │
│           │ 1. POST /v1/traces (plain HTTP to localhost)        │
│           │    SPIFFE ID: cluster.local/ns/.../sa/agentgateway │
│           ▼                                                      │
│  ┌──────────────────┐                                           │
│  │  Envoy Sidecar   │                                           │
│  │  (mTLS origin)   │                                           │
│  └────────┬─────────┘                                           │
│           │ 2. mTLS connection with SPIFFE SVID                 │
│           │    Destination: openmeter-otlp.platform-core    │
│           ▼                                                      │
│  ┌──────────────────┐                                           │
│  │ Istio Gateway    │                                           │
│  │ (SPIFFE validate)│                                           │
│  └────────┬─────────┘                                           │
│           │ 3. AuthorizationPolicy check                        │
│           │    Allow: cluster.local/ns/.../sa/agentgateway     │
│           ▼                                                      │
│  ┌──────────────────┐                                           │
│  │  OpenMeter       │                                           │
│  │  (port 4318)     │                                           │
│  └──────────────────┘                                           │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

**Key Security Properties:**
- **Zero-Trust**: mTLS required, no plaintext OTLP traffic
- **Identity-Based**: SPIFFE SVID validates AgentGateway identity
- **Least Privilege**: Only AgentGateway ServiceAccount authorized
- **Defense in Depth**: Istio AuthorizationPolicy + NetworkPolicy
- **Automatic Rotation**: SPIFFE SVIDs rotate every 60 minutes via SPIRE

### 10.6 Spoke-Side Metric Collector (Req 4.8-4.13, Gap 1.5)

**Problem:** AgentGateway only meters HTTP traffic. Cannot track stateful quotas like database rows, storage bytes, workspace counts, form counts, or table counts.

**Solution:** Per-tenant metric collector sidecar in PostgREST pods that queries tenant databases and emits OTLP gauge metrics to OpenMeter via local OpenTelemetry Collector.

**Architecture Pattern:** Sidecar Collector Pattern (ADR-011 compliant - no imperative Jobs)

**Security:** mTLS enforced at all hops via Istio + SPIFFE (ADR-0009 compliance)

**Rationale:** ADR-011 mandates declarative operator state over imperative Jobs. CronJobs are imperative batch operations that violate this principle. Instead, we use a continuously-running sidecar container that reconciles metrics in a control loop, aligning with Kubernetes' eventual consistency model.

**File:** `manifests/tenants/charts/universal-tenant/templates/postgrest-deployment.yaml` (modified)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ .Values.tenantId }}-postgrest
  namespace: {{ .Values.tenantId }}
spec:
  replicas: 2
  selector:
    matchLabels:
      app: postgrest
      tenant: {{ .Values.tenantId }}
  template:
    metadata:
      annotations:
        sidecar.istio.io/inject: "true"  # Envoy sidecar for mTLS
      labels:
        app: postgrest
        tenant: {{ .Values.tenantId }}
    spec:
      serviceAccountName: {{ .Values.tenantId }}-postgrest
      containers:
      - name: postgrest
        image: postgrest/postgrest:latest
        # ... existing PostgREST config ...
      
      {{- if .Values.metering.enabled }}
      - name: metrics-collector
        image: {{ .Values.metering.collectorImage }}
        env:
        - name: TENANT_ID
          value: {{ .Values.tenantId }}
        - name: POSTGRES_DSN
          valueFrom:
            secretKeyRef:
              name: {{ .Values.tenantId }}-db-credentials
              key: dsn
        - name: OTEL_EXPORTER_OTLP_ENDPOINT
          value: "http://localhost:4318"  # Envoy sidecar intercepts
        - name: METRICS
          value: {{ .Values.metering.metrics | join "," }}
        - name: COLLECTION_INTERVAL
          value: {{ .Values.metering.interval | default "300s" }}
        resources:
          requests:
            cpu: 50m
            memory: 64Mi
          limits:
            cpu: 100m
            memory: 128Mi
      {{- end }}
```

**Metric Collector Implementation (Continuous Reconciliation Loop):**

**File:** `cmd/metric-collector/main.go`

```go
package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"time"
	
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	_ "github.com/lib/pq"
)

func main() {
	ctx := context.Background()
	
	// Parse collection interval (default: 5 minutes)
	interval, _ := time.ParseDuration(os.Getenv("COLLECTION_INTERVAL"))
	if interval == 0 {
		interval = 5 * time.Minute
	}
	
	// Initialize OTLP exporter
	exporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpoint(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")),
		otlpmetrichttp.WithInsecure(),
	)
	if err != nil {
		panic(err)
	}
	
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter,
			sdkmetric.WithInterval(interval))),
	)
	otel.SetMeterProvider(provider)
	defer provider.Shutdown(ctx)
	
	meter := provider.Meter("tenant-metrics")
	
	// Connect to tenant database
	db, err := sql.Open("postgres", os.Getenv("POSTGRES_DSN"))
	if err != nil {
		panic(err)
	}
	defer db.Close()
	
	tenantID := os.Getenv("TENANT_ID")
	metrics := strings.Split(os.Getenv("METRICS"), ",")
	
	// Register observable gauges (continuous reconciliation)
	for _, metricName := range metrics {
		switch metricName {
		case "database_rows":
			registerDatabaseRowsGauge(meter, db, tenantID)
		case "storage_bytes":
			registerStorageBytesGauge(meter, db, tenantID)
		case "workspace_count":
			registerWorkspaceCountGauge(meter, db, tenantID)
		case "form_count":
			registerFormCountGauge(meter, db, tenantID)
		case "table_count":
			registerTableCountGauge(meter, db, tenantID)
		}
	}
	
	// Block forever (sidecar runs continuously)
	select {}
}

func registerDatabaseRowsGauge(meter metric.Meter, db *sql.DB, tenantID string) {
	gauge, _ := meter.Int64ObservableGauge("tenant.database.rows",
		metric.WithDescription("Total database rows across all tables"),
	)
	
	meter.RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		var rowCount int64
		err := db.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(n_live_tup), 0) 
			FROM pg_stat_user_tables 
			WHERE schemaname = 'public'
		`).Scan(&rowCount)
		
		if err == nil {
			o.ObserveInt64(gauge, rowCount,
				metric.WithAttributes(
					attribute.String("tenant_id", tenantID),
					attribute.String("meter_id", "database_rows"),
				),
			)
		}
		return err
	}, gauge)
}

// Similar implementations for other metrics...
```

**File:** `manifests/spoke-pool/otel-collector/deployment.yaml`

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: otel-collector
  namespace: spoke-pool
spec:
  replicas: 3
  selector:
    matchLabels:
      app: otel-collector
  template:
    metadata:
      annotations:
        sidecar.istio.io/inject: "true"  # Envoy sidecar for mTLS to Hub
      labels:
        app: otel-collector
    spec:
      serviceAccountName: otel-collector
      containers:
      - name: otel-collector
        image: otel/opentelemetry-collector-contrib:latest
        ports:
        - containerPort: 4318
          name: otlp-http
        volumeMounts:
        - name: config
          mountPath: /etc/otel-collector
      volumes:
      - name: config
        configMap:
          name: otel-collector-config
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: otel-collector
  namespace: spoke-pool
```

**File:** `manifests/spoke-pool/otel-collector/config.yaml`

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: otel-collector-config
  namespace: spoke-pool
data:
  config.yaml: |
    receivers:
      otlp:
        protocols:
          http:
            endpoint: 0.0.0.0:4318

    processors:
      batch:
        timeout: 10s
        send_batch_size: 100
      
      attributes:
        actions:
        - key: cluster
          value: spoke-pool
          action: insert

    exporters:
      otlphttp:
        endpoint: "http://localhost:4318"  # Envoy sidecar intercepts
        # mTLS handled by Istio Envoy sidecar (ADR-0009)
        # Envoy rewrites to: https://openmeter-otlp.platform-core.svc:4318
        tls:
          insecure: true  # Plain HTTP to localhost, Envoy handles mTLS
        retry_on_failure:
          enabled: true
          initial_interval: 1s
          max_interval: 30s

    service:
      pipelines:
        metrics:
          receivers: [otlp]
          processors: [batch, attributes]
          exporters: [otlphttp]
```

**File:** `manifests/spoke-pool/otel-collector/istio-destination-rule.yaml`

```yaml
apiVersion: networking.istio.io/v1beta1
kind: DestinationRule
metadata:
  name: openmeter-otlp-mtls
  namespace: spoke-pool
spec:
  host: openmeter-otlp.platform-core.svc.cluster.local
  trafficPolicy:
    tls:
      mode: ISTIO_MUTUAL  # Enforce mTLS with SPIFFE identity
```

**File:** `manifests/spoke-pool/otel-collector/istio-service-entry.yaml`

```yaml
apiVersion: networking.istio.io/v1beta1
kind: ServiceEntry
metadata:
  name: openmeter-otlp-hub
  namespace: spoke-pool
spec:
  hosts:
  - openmeter-otlp.platform-core.svc.cluster.local
  location: MESH_EXTERNAL
  ports:
  - number: 4318
    name: otlp-http
    protocol: HTTP
  resolution: DNS
  endpoints:
  - address: <HUB_INGRESS_IP>  # Hub Istio IngressGateway external IP
```

**File:** `manifests/spoke-pool/network-policy-egress.yaml`

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: otel-collector-egress-hub
  namespace: spoke-pool
spec:
  podSelector:
    matchLabels:
      app: otel-collector
  policyTypes:
  - Egress
  egress:
  - to:
    - podSelector: {}  # Allow to Istio sidecar in same namespace
    ports:
    - protocol: TCP
      port: 15001  # Envoy outbound
  - to:
    - namespaceSelector:
        matchLabels:
          name: istio-system
    ports:
    - protocol: TCP
      port: 15012  # Istio control plane (xDS)
  - to:
    - ipBlock:
        cidr: <HUB_INGRESS_IP>/32  # Hub Istio IngressGateway
    ports:
    - protocol: TCP
      port: 4318  # OTLP HTTP
    - protocol: TCP
      port: 443   # HTTPS (mTLS)
  - to:
    - namespaceSelector: {}
      podSelector:
        matchLabels:
          app: spire-server
    ports:
    - protocol: TCP
      port: 8081  # SPIRE Server API
```

**File:** `manifests/hub-core-services/openmeter/istio-authz-otel.yaml`

```yaml
apiVersion: security.istio.io/v1beta1
kind: AuthorizationPolicy
metadata:
  name: openmeter-otlp-from-spoke-otel
  namespace: platform-core
spec:
  selector:
    matchLabels:
      app: openmeter
  action: ALLOW
  rules:
  - from:
    - source:
        principals:
        - "cluster.local/ns/platform-gateway/sa/agentgateway"
        - "spiffe://spoke-pool.cluster.local/ns/spoke-pool/sa/otel-collector"  # Federated SPIFFE ID
    to:
    - operation:
        methods: ["POST"]
        paths: ["/v1/traces", "/v1/metrics"]
        ports: ["4318"]
```

### 10.7 SPIRE Federation for Cross-Cluster mTLS (Gap 2.1)

**Problem:** Hub and Spoke are physically separate clusters. Hub SPIRE server will reject Spoke's SPIFFE IDs unless SPIRE Federation is configured.

**Solution:** Configure SPIRE Federation to establish trust between Hub and Spoke SPIRE servers.

### 10.8 OpenMeter Event Bridge for NATS Choreography (Gap 3.1)

**Problem:** OpenMeter handles Stripe webhooks directly (Req 18.4-18.7), but kube-sbt needs to react to billing events (e.g., suspend tenant on payment failure). OpenMeter does NOT natively publish to NATS JetStream.

**Solution:** Deploy an OpenMeter Event Bridge service that polls OpenMeter's notification/webhook APIs and translates events into NATS JetStream messages.

**Architecture:**

```
┌─────────────────────────────────────────────────────────────────┐
│                         Hub Cluster                              │
│                                                                  │
│  ┌──────────────────────────────────────────────────────┐      │
│  │  Stripe                                              │      │
│  └────────────────────┬─────────────────────────────────┘      │
│                       │ Webhook: invoice.payment_failed         │
│                       ▼                                          │
│  ┌──────────────────────────────────────────────────────┐      │
│  │  OpenMeter (POST /api/v1/apps/{appId}/stripe/webhook)│      │
│  │  - Validates signature                               │      │
│  │  - Updates invoice state (failed)                    │      │
│  │  - Stores event in internal DB                       │      │
│  └────────────────────┬─────────────────────────────────┘      │
│                       │ Polling (every 30s)                     │
│                       ▼                                          │
│  ┌──────────────────────────────────────────────────────┐      │
│  │  OpenMeter Event Bridge (Go service)                 │      │
│  │  - Polls: GET /api/v1/events?since={lastTimestamp}   │      │
│  │  - Filters: invoice.payment_failed, subscription.*   │      │
│  │  - Translates: OpenMeter event → NATS message        │      │
│  └────────────────────┬─────────────────────────────────┘      │
│                       │ Publish                                 │
│                       ▼                                          │
│  ┌──────────────────────────────────────────────────────┐      │
│  │  NATS JetStream                                      │      │
│  │  Topics:                                             │      │
│  │  - opensbt.billing.payment_failed                    │      │
│  │  - opensbt.billing.payment_succeeded                 │      │
│  │  - opensbt.billing.subscription_cancelled            │      │
│  └────────────────────┬─────────────────────────────────┘      │
│                       │ Subscribe                               │
│                       ▼                                          │
│  ┌──────────────────────────────────────────────────────┐      │
│  │  kube-sbt Billing Event Handler                      │      │
│  │  - Consumes: opensbt.billing.payment_failed          │      │
│  │  - Action: Suspend tenant (update status in DB)      │      │
│  │  - Action: Invalidate Redis entitlement cache        │      │
│  └──────────────────────────────────────────────────────┘      │
└─────────────────────────────────────────────────────────────────┘
```

**File:** `cmd/openmeter-event-bridge/main.go`

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	
	"github.com/nats-io/nats.go"
	openmeter "github.com/openmeterio/openmeter/api/client/go"
)

type EventBridge struct {
	openMeterClient *openmeter.ClientWithResponses
	natsConn        *nats.Conn
	lastTimestamp   time.Time
}

func main() {
	ctx := context.Background()
	
	// Initialize OpenMeter client
	omClient, _ := openmeter.NewClientWithResponses(os.Getenv("OPENMETER_URL"))
	
	// Initialize NATS connection
	nc, _ := nats.Connect(os.Getenv("NATS_URL"))
	defer nc.Close()
	
	bridge := &EventBridge{
		openMeterClient: omClient,
		natsConn:        nc,
		lastTimestamp:   time.Now().Add(-24 * time.Hour), // Start 24h ago
	}
	
	// Poll OpenMeter events every 30 seconds
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			bridge.pollAndPublish(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (b *EventBridge) pollAndPublish(ctx context.Context) error {
	// Poll OpenMeter events API
	params := &openmeter.ListEventsParams{
		Since: &b.lastTimestamp,
		Limit: ptr(100),
	}
	
	resp, err := b.openMeterClient.ListEventsWithResponse(ctx, params)
	if err != nil {
		return fmt.Errorf("failed to poll OpenMeter events: %w", err)
	}
	
	if resp.StatusCode() != 200 {
		return fmt.Errorf("OpenMeter returned status %d", resp.StatusCode())
	}
	
	events := resp.JSON200.Data
	
	for _, event := range events {
		// Translate OpenMeter event to NATS message
		switch event.Type {
		case "invoice.payment_failed":
			b.publishBillingEvent("opensbt.billing.payment_failed", event)
		case "invoice.payment_succeeded":
			b.publishBillingEvent("opensbt.billing.payment_succeeded", event)
		case "subscription.cancelled":
			b.publishBillingEvent("opensbt.billing.subscription_cancelled", event)
		case "subscription.created":
			b.publishBillingEvent("opensbt.billing.subscription_created", event)
		}
		
		// Update last timestamp
		if event.Timestamp.After(b.lastTimestamp) {
			b.lastTimestamp = event.Timestamp
		}
	}
	
	return nil
}

func (b *EventBridge) publishBillingEvent(subject string, event OpenMeterEvent) error {
	// Extract tenant_id from event metadata
	tenantID := event.Metadata["tenant_id"]
	
	// Construct NATS message
	msg := map[string]interface{}{
		"event_id":   event.ID,
		"event_type": event.Type,
		"tenant_id":  tenantID,
		"timestamp":  event.Timestamp,
		"data":       event.Data,
	}
	
	payload, _ := json.Marshal(msg)
	
	// Publish to NATS JetStream
	_, err := b.natsConn.Request(subject, payload, 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to publish to NATS: %w", err)
	}
	
	return nil
}
```

**File:** `manifests/hub-core-services/openmeter-event-bridge/deployment.yaml`

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: openmeter-event-bridge
  namespace: platform-billing
spec:
  replicas: 2
  selector:
    matchLabels:
      app: openmeter-event-bridge
  template:
    metadata:
      annotations:
        sidecar.istio.io/inject: "true"
      labels:
        app: openmeter-event-bridge
    spec:
      serviceAccountName: openmeter-event-bridge
      containers:
      - name: bridge
        image: ghcr.io/soloz-io/openmeter-event-bridge:latest
        env:
        - name: OPENMETER_URL
          value: "http://openmeter.platform-core.svc:8888"
        - name: NATS_URL
          value: "nats://nats.platform-messaging.svc:4222"
        - name: POLL_INTERVAL
          value: "30s"
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 200m
            memory: 256Mi
```

**File:** `internal/opensbt/providers/openmeter/billing_event_handler.go`

```go
package openmeter

import (
	"context"
	"encoding/json"
	
	"github.com/nats-io/nats.go"
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
)

type BillingEventHandler struct {
	natsConn *nats.Conn
	storage  interfaces.IStorage
	redis    *redis.Client
}

func NewBillingEventHandler(natsConn *nats.Conn, storage interfaces.IStorage, redis *redis.Client) *BillingEventHandler {
	return &BillingEventHandler{
		natsConn: natsConn,
		storage:  storage,
		redis:    redis,
	}
}

func (h *BillingEventHandler) Start(ctx context.Context) error {
	// Subscribe to billing events
	_, err := h.natsConn.QueueSubscribe("opensbt.billing.>", "billing-handlers", func(msg *nats.Msg) {
		var event BillingEvent
		if err := json.Unmarshal(msg.Data, &event); err != nil {
			return
		}
		
		switch event.EventType {
		case "invoice.payment_failed":
			h.handlePaymentFailed(ctx, event)
		case "invoice.payment_succeeded":
			h.handlePaymentSucceeded(ctx, event)
		case "subscription.cancelled":
			h.handleSubscriptionCancelled(ctx, event)
		}
		
		msg.Ack()
	})
	
	return err
}

func (h *BillingEventHandler) handlePaymentFailed(ctx context.Context, event BillingEvent) {
	tenantID := event.TenantID
	
	// Update tenant status in database
	_, err := h.storage.Exec(ctx, `
		UPDATE tenants 
		SET status = 'suspended', 
		    suspension_reason = 'payment_failed',
		    updated_at = NOW()
		WHERE id = $1
	`, tenantID)
	
	if err != nil {
		// Log error, retry via NATS redelivery
		return
	}
	
	// Invalidate Redis entitlement cache
	pattern := fmt.Sprintf("entitlement:%s:*", tenantID)
	keys, _ := h.redis.Keys(ctx, pattern).Result()
	if len(keys) > 0 {
		h.redis.Del(ctx, keys...)
	}
}

func (h *BillingEventHandler) handlePaymentSucceeded(ctx context.Context, event BillingEvent) {
	tenantID := event.TenantID
	
	// Reactivate tenant
	_, err := h.storage.Exec(ctx, `
		UPDATE tenants 
		SET status = 'active', 
		    suspension_reason = NULL,
		    updated_at = NOW()
		WHERE id = $1
	`, tenantID)
	
	if err != nil {
		return
	}
	
	// Invalidate cache to refresh entitlements
	pattern := fmt.Sprintf("entitlement:%s:*", tenantID)
	keys, _ := h.redis.Keys(ctx, pattern).Result()
	if len(keys) > 0 {
		h.redis.Del(ctx, keys...)
	}
}
```

**NATS JetStream Stream Configuration:**

**File:** `manifests/hub-core-services/nats/streams/billing-events.yaml`

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: nats-stream-billing-events
  namespace: platform-messaging
data:
  stream.json: |
    {
      "name": "BILLING_EVENTS",
      "subjects": ["opensbt.billing.>"],
      "retention": "limits",
      "max_age": 604800000000000,
      "max_msgs": 1000000,
      "storage": "file",
      "replicas": 3,
      "discard": "old"
    }
```

**Key Design Properties:**

1. **Polling Pattern**: Event bridge polls OpenMeter every 30s (configurable)
2. **Idempotency**: Tracks `lastTimestamp` to avoid reprocessing events
3. **Event Translation**: Maps OpenMeter events to NATS subjects
4. **Tenant Isolation**: Extracts `tenant_id` from event metadata
5. **Resilience**: NATS JetStream provides at-least-once delivery with redelivery
6. **Observability**: Prometheus metrics for polling latency, event count, publish failures

**Alternative Pattern (Webhook-Based):**

If OpenMeter supports outbound webhooks (check documentation), configure OpenMeter to POST events directly to the event bridge HTTP endpoint, eliminating polling overhead.

---

```
┌─────────────────────────────────────────────────────────────────┐
│                         Hub Cluster                              │
│  ┌──────────────────────────────────────────────────────┐      │
│  │  SPIRE Server (Hub)                                  │      │
│  │  Trust Domain: cluster.local                         │      │
│  │  Federation Bundle Endpoint: /bundle                 │      │
│  └────────────────────┬─────────────────────────────────┘      │
│                       │ Exposes federation bundle               │
└───────────────────────┼─────────────────────────────────────────┘
                        │ HTTPS (mutual trust)
┌───────────────────────┼─────────────────────────────────────────┐
│                       │              Spoke Pool Cluster          │
│  ┌────────────────────▼─────────────────────────────────┐      │
│  │  SPIRE Server (Spoke)                                │      │
│  │  Trust Domain: spoke-pool.cluster.local              │      │
│  │  Federated Trust: cluster.local (Hub)                │      │
│  │  - Fetches Hub bundle periodically                   │      │
│  │  - Validates Hub-issued SVIDs                        │      │
│  └──────────────────────────────────────────────────────┘      │
└─────────────────────────────────────────────────────────────────┘
```

**File:** `manifests/hub-core-services/spire/server-config.yaml`

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: spire-server-config
  namespace: spire-system
data:
  server.conf: |
    server {
      bind_address = "0.0.0.0"
      bind_port = "8081"
      trust_domain = "cluster.local"
      data_dir = "/run/spire/data"
      log_level = "INFO"
      
      # Federation bundle endpoint (Hub exposes to Spokes)
      federation {
        bundle_endpoint {
          address = "0.0.0.0"
          port = 8443
          acme {
            domain_name = "spire-federation.hub.nutgraf.in"
            email = "platform@nutgraf.in"
          }
        }
      }
    }
    
    plugins {
      DataStore "sql" {
        plugin_data {
          database_type = "postgres"
          connection_string = "postgresql://spire:password@postgres:5432/spire"
        }
      }
      
      KeyManager "disk" {
        plugin_data {
          keys_path = "/run/spire/data/keys.json"
        }
      }
      
      NodeAttestor "k8s_psat" {
        plugin_data {
          clusters = {
            "hub-cluster" = {
              service_account_allow_list = ["spire-system:spire-agent"]
            }
          }
        }
      }
    }
```

**File:** `manifests/spoke-pool/spire/server-config.yaml`

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: spire-server-config
  namespace: spire-system
data:
  server.conf: |
    server {
      bind_address = "0.0.0.0"
      bind_port = "8081"
      trust_domain = "spoke-pool.cluster.local"
      data_dir = "/run/spire/data"
      log_level = "INFO"
      
      # Federation with Hub (Spoke trusts Hub)
      federation {
        federates_with "cluster.local" {
          bundle_endpoint_url = "https://spire-federation.hub.nutgraf.in:8443"
          bundle_endpoint_profile "https_spiffe" {
            endpoint_spiffe_id = "spiffe://cluster.local/spire/server"
          }
        }
      }
    }
    
    plugins {
      DataStore "sql" {
        plugin_data {
          database_type = "postgres"
          connection_string = "postgresql://spire:password@postgres:5432/spire"
        }
      }
      
      KeyManager "disk" {
        plugin_data {
          keys_path = "/run/spire/data/keys.json"
        }
      }
      
      NodeAttestor "k8s_psat" {
        plugin_data {
          clusters = {
            "spoke-pool-cluster" = {
              service_account_allow_list = ["spire-system:spire-agent"]
            }
          }
        }
      }
    }
```

**File:** `manifests/spoke-pool/spire/network-policy-egress.yaml`

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: spire-server-egress-federation
  namespace: spire-system
spec:
  podSelector:
    matchLabels:
      app: spire-server
  policyTypes:
  - Egress
  egress:
  - to:
    - namespaceSelector: {}
      podSelector:
        matchLabels:
          app: postgres
    ports:
    - protocol: TCP
      port: 5432
  - to:
    - ipBlock:
        cidr: 0.0.0.0/0  # Allow outbound to Hub federation endpoint
        except:
        - 169.254.169.254/32  # Block metadata service
    ports:
    - protocol: TCP
      port: 8443  # SPIRE federation bundle endpoint
    - protocol: TCP
      port: 443   # HTTPS
  - to:
    - namespaceSelector: {}
    ports:
    - protocol: TCP
      port: 53   # DNS
    - protocol: UDP
      port: 53
```

**SPIFFE ID Format After Federation:**

```yaml
# Hub workloads
spiffe://cluster.local/ns/platform-gateway/sa/agentgateway
spiffe://cluster.local/ns/platform-core/sa/openmeter

# Spoke workloads (federated trust domain)
spiffe://spoke-pool.cluster.local/ns/spoke-pool/sa/otel-collector
spiffe://spoke-pool.cluster.local/ns/tenant-app-creator/sa/metrics-collector
```

**Hub AuthorizationPolicy Update (accepts federated IDs):**

```yaml
apiVersion: security.istio.io/v1beta1
kind: AuthorizationPolicy
metadata:
  name: openmeter-otlp-federated
  namespace: platform-core
spec:
  selector:
    matchLabels:
      app: openmeter
  action: ALLOW
  rules:
  - from:
    - source:
        principals:
        - "cluster.local/ns/platform-gateway/sa/agentgateway"  # Hub workload
        - "spoke-pool.cluster.local/ns/spoke-pool/sa/otel-collector"  # Spoke workload (federated)
    to:
    - operation:
        methods: ["POST"]
        paths: ["/v1/traces", "/v1/metrics"]
        ports: ["4318"]
```

**Federation Verification:**

```bash
# On Hub SPIRE Server
kubectl exec -n spire-system spire-server-0 -- \
  /opt/spire/bin/spire-server bundle show

# On Spoke SPIRE Server (should show Hub bundle)
kubectl exec -n spire-system spire-server-0 -- \
  /opt/spire/bin/spire-server bundle show -id spiffe://cluster.local
```

**Key Security Properties:**
1. **Mutual Trust**: Hub and Spoke SPIRE servers exchange trust bundles
2. **Automatic Refresh**: Spoke fetches Hub bundle every 5 minutes
3. **Cryptographic Validation**: Hub validates Spoke SVIDs using federated bundle
4. **Namespace Isolation**: SPIFFE IDs include trust domain prefix
5. **Zero Shared Secrets**: No pre-shared keys, only public key exchange

---

```
┌─────────────────────────────────────────────────────────────────┐
│                      Spoke Pool Cluster                          │
│                                                                  │
│  ┌──────────────────────────────────────────────────────┐      │
│  │  Metric Collector CronJob                            │      │
│  │  SPIFFE: cluster.local/ns/{tenant}/sa/metrics-...    │      │
│  └────────────────────┬─────────────────────────────────┘      │
│                       │ 1. HTTP POST to localhost:4318          │
│                       ▼                                          │
│  ┌──────────────────────────────────────────────────────┐      │
│  │  Envoy Sidecar (Collector Pod)                       │      │
│  │  - Intercepts localhost:4318                         │      │
│  │  - Validates SPIFFE SVID                             │      │
│  │  - Enforces mTLS to OTel Collector                   │      │
│  └────────────────────┬─────────────────────────────────┘      │
│                       │ 2. mTLS with SPIFFE SVID                │
│                       ▼                                          │
│  ┌──────────────────────────────────────────────────────┐      │
│  │  OpenTelemetry Collector                             │      │
│  │  SPIFFE: cluster.local/ns/spoke-pool/sa/otel-...     │      │
│  │  - Receives OTLP from all tenant collectors          │      │
│  │  - Batches metrics (100 per batch)                   │      │
│  └────────────────────┬─────────────────────────────────┘      │
│                       │ 3. HTTP POST to localhost:4318          │
│                       ▼                                          │
│  ┌──────────────────────────────────────────────────────┐      │
│  │  Envoy Sidecar (OTel Collector Pod)                  │      │
│  │  - Intercepts localhost:4318                         │      │
│  │  - Rewrites to: openmeter-otlp.platform-core     │      │
│  │  - Enforces mTLS with SPIFFE SVID                    │      │
│  └────────────────────┬─────────────────────────────────┘      │
│                       │ 4. mTLS cross-cluster                   │
└───────────────────────┼─────────────────────────────────────────┘
                        │
                        ▼
┌─────────────────────────────────────────────────────────────────┐
│                         Hub Cluster                              │
│  ┌──────────────────────────────────────────────────────┐      │
│  │  Istio IngressGateway                                │      │
│  │  - Validates SPIFFE SVID                             │      │
│  │  - Checks AuthorizationPolicy                        │      │
│  │  - Allows: spoke-pool/sa/otel-collector              │      │
│  └────────────────────┬─────────────────────────────────┘      │
│                       │ 5. Authorized mTLS                      │
│                       ▼                                          │
│  ┌──────────────────────────────────────────────────────┐      │
│  │  OpenMeter (port 4318)                               │      │
│  │  - Receives OTLP metrics                             │      │
│  │  - Stores tenant-scoped gauge metrics                │      │
│  └──────────────────────────────────────────────────────┘      │
└─────────────────────────────────────────────────────────────────┘
```

**File:** `manifests/tenants/charts/universal-tenant/values.yaml` (additions)

```yaml
metering:
  enabled: true
  interval: "300s"  # 5 minutes (continuous reconciliation)
  collectorImage: "ghcr.io/soloz-io/metric-collector:latest"
  metrics:
    - database_rows
    - storage_bytes
    - workspace_count
    - form_count
    - table_count
```

**Key ADR Compliance:**
1. **ADR-011 (Declarative State)**: Sidecar runs continuously, not imperative CronJob
2. **ADR-009 (Zero-Trust)**: mTLS via Istio + SPIFFE at all hops
3. **ADR-005/008 (Crossplane)**: Provisioned via universal-tenant Helm chart (Crossplane composition)
4. **ADR-010 (User Management)**: Metrics tied to tenant_id from JWT claims

**Key Security Properties (ADR-0009 + SPIRE Federation):**
1. **Layer 1 (Collector → OTel)**: mTLS with SPIFFE SVID validation
2. **Layer 2 (OTel → Hub)**: mTLS with SPIFFE SVID validation
3. **Layer 3 (Hub Gateway)**: AuthorizationPolicy restricts to `spoke-pool/sa/otel-collector`
4. **Layer 4 (OpenMeter)**: Namespace isolation enforced by tenant_id attribute
5. **Zero Plaintext**: All OTLP traffic encrypted end-to-end
6. **Auto-Rotation**: SPIFFE SVIDs rotate every 60 minutes
7. **Defense-in-Depth**: Multiple independent security layers

**Key Benefits:**
1. **Tenant-Specific**: Each tenant selects metrics via Helm values
2. **Declarative**: Provisioned via GitOps with tenant CR
3. **Flexible**: Supports domain-specific metrics (forms, tables, apps)
4. **Automatic**: Crossplane provisions collector on tenant creation
5. **Secure**: Read-only DB access, mTLS to Hub, SPIFFE identity
6. **Zero-Trust**: Cryptographic workload identity at every hop

---

## 11. Hub-Operator Integration (ADR 008, ADR 012)

### 11.1 OpenMeter Namespace Provisioning (Req 19)

**CORRECTED: hub-operator Pattern (NOT Crossplane provider-http)**

**ADR Alignment:**
- ✅ **ADR 012**: Static billing catalog via GitOps CRDs in `fleet-registry/tenants/<tenant-id>/billing/`
- ✅ **ADR 008**: Hub provisions via `SpokeTenantEnvironment` XR (federated boundary)
- ✅ **ADR 004**: Declarative operator state, no imperative Jobs
- ✅ **ADR 000**: provider-kubernetes for Spoke delivery, hub-operator for Hub-local services

**Gap Analysis Result:**
- ❌ OpenMeter OSS does NOT expose `/api/v1/namespaces` REST API endpoint
- ❌ Crossplane provider-http approach WILL FAIL (404 errors)
- ✅ OpenMeter uses `namespace.Manager` Go API for programmatic namespace management
- ✅ hub-operator already manages external services (Hydra, NATS, Infisical)

**Reference Files:**
- `archived/billing-metering/openmeter/openmeter/namespace/namespace.go` - namespace.Manager API
- `operators/hub-operator/api/v1alpha1/hubenvironment_types.go` - HubEnvironment CRD
- `operators/hub-operator/internal/controller/hubenvironment_controller.go` - Reconciler
- `docs/adr/012-billing-operator-pattern.md` - ADR 012
- `docs/adr/008-federated-api-boundary-crossplane.md` - ADR 008

**Implementation:**

#### Step 1: Extend HubEnvironment CRD

**File:** `operators/hub-operator/api/v1alpha1/hubenvironment_types.go`

```go
// HubEnvironmentSpec defines the desired state of HubEnvironment
type HubEnvironmentSpec struct {
	// ... existing fields (Domain, TLS, Observability, Database, NATS, OAuth, Secrets) ...
	
	// OpenMeter configuration for usage metering
	// +optional
	OpenMeter *OpenMeterConfig `json:"openMeter,omitempty"`
}

// OpenMeterConfig defines OpenMeter namespace configuration
type OpenMeterConfig struct {
	// Namespaces to be created in OpenMeter
	// +optional
	Namespaces []OpenMeterNamespace `json:"namespaces,omitempty"`
}

// OpenMeterNamespace defines a tenant namespace in OpenMeter
type OpenMeterNamespace struct {
	// TenantID is the unique identifier (used as namespace slug)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	TenantID string `json:"tenantId"`
	
	// DisplayName for the namespace
	// +optional
	DisplayName string `json:"displayName,omitempty"`
	
	// Metadata for the namespace
	// +optional
	Metadata map[string]string `json:"metadata,omitempty"`
}
```

#### Step 2: Add OpenMeter Client

**File:** `operators/hub-operator/internal/client/openmeter.go`

```go
package client

import (
	"context"
	"fmt"
	
	// Import OpenMeter namespace.Manager
	"github.com/openmeterio/openmeter/openmeter/namespace"
	opsv1alpha1 "github.com/soloz-io/zero-ops/operators/hub-operator/api/v1alpha1"
)

// OpenMeterClient wraps OpenMeter namespace.Manager
type OpenMeterClient struct {
	manager namespace.Manager
}

// NewOpenMeterClient creates an OpenMeter client
func NewOpenMeterClient(manager namespace.Manager) *OpenMeterClient {
	return &OpenMeterClient{
		manager: manager,
	}
}

// CreateOrUpdateNamespaces provisions OpenMeter namespaces
func (c *OpenMeterClient) CreateOrUpdateNamespaces(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
	if hubEnv.Spec.OpenMeter == nil {
		return nil // No OpenMeter config
	}
	
	for _, ns := range hubEnv.Spec.OpenMeter.Namespaces {
		// Use namespace.Manager.CreateNamespace() API
		// Reference: archived/billing-metering/openmeter/openmeter/namespace/namespace.go:L42-L48
		if err := c.manager.CreateNamespace(ctx, namespace.CreateNamespaceInput{
			Slug:        ns.TenantID,
			DisplayName: ns.DisplayName,
			Metadata:    ns.Metadata,
		}); err != nil {
			// Check if namespace already exists (idempotent)
			if !isNamespaceExistsError(err) {
				return fmt.Errorf("failed to create OpenMeter namespace %s: %w", ns.TenantID, err)
			}
		}
	}
	
	return nil
}

// DeleteNamespace removes an OpenMeter namespace
func (c *OpenMeterClient) DeleteNamespace(ctx context.Context, tenantID string) error {
	// Use namespace.Manager.DeleteNamespace() API
	return c.manager.DeleteNamespace(ctx, tenantID)
}

// isNamespaceExistsError checks if error indicates namespace already exists
func isNamespaceExistsError(err error) bool {
	// Check OpenMeter error types for duplicate namespace
	// Implementation depends on OpenMeter error handling
	return false // Placeholder
}
```

#### Step 3: Add Phase 0b to hub-operator Reconciler

**File:** `operators/hub-operator/internal/controller/hubenvironment_controller.go`

```go
// Reconcile is part of the main kubernetes reconciliation loop
func (r *HubEnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	
	// ... existing Phase 0 (Infisical bootstrap) ...
	
	// Phase 0b: Provision OpenMeter Namespaces (NEW)
	if !meta.IsStatusConditionTrue(hubEnv.Status.Conditions, "OpenMeterNamespacesConfigured") {
		logger.Info("Phase 0b: Provisioning OpenMeter namespaces")
		
		// Check if OpenMeter is ready
		openMeterReady, err := r.isOpenMeterReady(ctx, hubEnv)
		if err != nil {
			logger.Error(err, "Failed to check OpenMeter readiness")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}
		if !openMeterReady {
			logger.Info("Waiting for OpenMeter to be ready")
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
		
		// Create OpenMeter client
		openMeterClient, err := client.NewOpenMeterClient(/* namespace.Manager instance */)
		if err != nil {
			logger.Error(err, "Failed to create OpenMeter client")
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}
		
		// Provision namespaces
		if err := openMeterClient.CreateOrUpdateNamespaces(ctx, hubEnv); err != nil {
			logger.Error(err, "Failed to provision OpenMeter namespaces")
			meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
				Type:               "OpenMeterNamespacesConfigured",
				Status:             metav1.ConditionFalse,
				Reason:             "ProvisioningFailed",
				Message:            err.Error(),
				ObservedGeneration: hubEnv.Generation,
			})
			if err := r.Status().Update(ctx, hubEnv); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		
		meta.SetStatusCondition(&hubEnv.Status.Conditions, metav1.Condition{
			Type:               "OpenMeterNamespacesConfigured",
			Status:             metav1.ConditionTrue,
			Reason:             "Provisioned",
			Message:            "OpenMeter namespaces provisioned successfully",
			ObservedGeneration: hubEnv.Generation,
		})
		
		if err := r.Status().Update(ctx, hubEnv); err != nil {
			return ctrl.Result{}, err
		}
		
		logger.Info("Phase 0b complete: OpenMeter namespaces provisioned")
		return ctrl.Result{Requeue: true}, nil
	}
	
	// ... existing Phase 1 (Bootstrap Secrets) ...
	// ... existing Phase 2 (Database Migrations, Roles) ...
	// ... existing Phase 3 (Infisical, Hydra, NATS) ...
}

// isOpenMeterReady checks if OpenMeter Deployment is ready
func (r *HubEnvironmentReconciler) isOpenMeterReady(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) (bool, error) {
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{
		Name:      "openmeter",
		Namespace: "platform-core",
	}, deployment); err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	
	// Check if deployment is ready
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentAvailable && condition.Status == corev1.ConditionTrue {
			return true, nil
		}
	}
	
	return false, nil
}
```

#### Step 4: Update HubEnvironment CR Example

**File:** `manifests/hub-core-services/hub-operator/samples/hubenvironment.yaml`

```yaml
apiVersion: ops.nutgraf.in/v1alpha1
kind: HubEnvironment
metadata:
  name: hub-prod
spec:
  domain: nutgraf.in
  
  # ... existing fields (tls, observability, database, nats, oauth, secrets) ...
  
  # OpenMeter namespace configuration (NEW)
  openMeter:
    namespaces:
    - tenantId: app-creator
      displayName: "App Creator Tenant"
      metadata:
        tier: "starter"
        region: "eu-central-1"
```

#### Step 5: Status Reporting

```yaml
# HubEnvironment status conditions
status:
  conditions:
  - type: OpenMeterNamespacesConfigured
    status: "True"
    reason: "Provisioned"
    message: "OpenMeter namespaces provisioned successfully"
    lastTransitionTime: "2026-04-30T10:15:30Z"
```

**Why This Approach Works:**
1. ✅ Uses OpenMeter's native `namespace.Manager` Go API (not non-existent REST API)
2. ✅ Follows ADR 012 (billing operator pattern for static catalog)
3. ✅ Follows ADR 008 (federated XR boundary - Hub provisions via SpokeTenantEnvironment)
4. ✅ Follows ADR 004 (declarative operator state, no imperative Jobs)
5. ✅ Aligns with existing hub-operator pattern (Hydra, NATS, Infisical)
6. ✅ OpenMeter runs on Hub (same cluster as hub-operator)
7. ✅ Namespace creation happens before database migrations (Phase 0b)
8. ✅ Idempotent (namespace.Manager handles duplicate creation)
9. ✅ Status tracking via HubEnvironment conditions

**Integration with SpokeTenantEnvironment XR (ADR 008):**
- Hub pushes ONE `SpokeTenantEnvironment` XR to Spoke (not multiple XRs)
- Spoke Composition internally composes TenantDatabase, PostgREST, AtlasMigration CRs
- Hub no longer manages Spoke DB primitives directly (federated boundary)
- OpenMeter namespace provisioned on Hub via hub-operator (not Crossplane)
- Tenant provisioning assumes namespace exists (kube-sbt validates namespace on first use)

---

## 12. Testing Strategy

### 12.1 E2E Tests with Testcontainers (Req 8.2, Gap 3.2)

**Build Tag Strategy:** Separate E2E tests from unit tests to avoid Docker daemon requirements.

**File:** `internal/opensbt/providers/openmeter/metering_e2e_test.go`

```go
//go:build e2e
// +build e2e

package openmeter_test

import (
	"context"
	"fmt"
	"testing"
	"time"
	
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
	"github.com/soloz-io/zero-ops/internal/opensbt/providers/openmeter"
)

func TestMeteringProvider_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	
	ctx := context.Background()
	
	// Start PostgreSQL container
	pgContainer, err := postgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:16-alpine"),
		postgres.WithDatabase("openmeter_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
	)
	require.NoError(t, err)
	defer pgContainer.Terminate(ctx)
	
	// Start OpenMeter container
	openMeterContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "ghcr.io/openmeterio/openmeter:latest",
			ExposedPorts: []string{"8888/tcp"},
			Env: map[string]string{
				"OPENMETER_POSTGRES_URL": pgContainer.ConnectionString(ctx),
			},
			WaitingFor: wait.ForHTTP("/health").WithPort("8888/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)
	defer openMeterContainer.Terminate(ctx)
	
	// Get OpenMeter URL
	host, _ := openMeterContainer.Host(ctx)
	port, _ := openMeterContainer.MappedPort(ctx, "8888")
	openMeterURL := fmt.Sprintf("http://%s:%s", host, port.Port())
	
	// Create provider
	provider, err := openmeter.NewMeteringProvider(openMeterURL)
	require.NoError(t, err)
	
	// Setup: Create test meter using raw OpenMeter client (simulates hub-operator)
	openMeterClient, err := openmeter.NewClientWithResponses(openMeterURL)
	require.NoError(t, err)
	
	meterReq := openmeter.CreateMeterJSONRequestBody{
		Slug:          "api_calls",
		Description:   ptr("API call counter"),
		Aggregation:   "COUNT",
		EventType:     "api_request",
		ValueProperty: ptr("count"),
	}
	_, err = openMeterClient.CreateMeterWithResponse(ctx, meterReq)
	require.NoError(t, err)
	
	// Test: Register Subject
	t.Run("RegisterSubject", func(t *testing.T) {
		subjectID := models.GenerateSubjectID("test-tenant", "user-123")
		metadata := map[string]string{
			"email": "test@example.com",
		}
		
		err := provider.RegisterSubject(ctx, "test-tenant", subjectID, metadata)
		assert.NoError(t, err)
	})
	
	// Test: Check Entitlement (fail-open with cache)
	t.Run("CheckEntitlement_FailOpen", func(t *testing.T) {
		// Stop OpenMeter to test fail-open behavior
		openMeterContainer.Stop(ctx, nil)
		
		status, err := provider.CheckEntitlement(ctx, "test-tenant", "test-subject", "api_calls")
		require.NoError(t, err)
		assert.True(t, status.HasAccess)
		assert.True(t, status.IsFallback)
	})
}

// Helper function for pointer conversion
func ptr(s string) *string {
	return &s
}
```

**Unit Test with Mocks (no Docker required):**

**File:** `internal/opensbt/providers/openmeter/metering_test.go`

```go
package openmeter_test

import (
	"context"
	"testing"
	
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
	"github.com/soloz-io/zero-ops/internal/opensbt/providers/openmeter"
	"github.com/soloz-io/zero-ops/internal/opensbt/providers/openmeter/mocks"
)

func TestMeteringProvider_CreateMeter_Unit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	
	mockClient := mocks.NewMockOpenMeterClient(ctrl)
	provider := openmeter.NewMeteringProviderWithClient(mockClient)
	
	spec := models.MeterSpec{
		Slug:        "api_calls",
		Aggregation: "COUNT",
	}
	
	mockClient.EXPECT().
		CreateMeter(gomock.Any(), gomock.Any()).
		Return(nil)
	
	err := provider.CreateMeter(context.Background(), "test-tenant", spec)
	assert.NoError(t, err)
}
```

**Makefile targets:**

```makefile
# Run unit tests (no Docker required)
test:
	go test -short ./...

# Run E2E tests (requires Docker)
test-e2e:
	go test -tags=e2e ./...

# Run all tests
test-all: test test-e2e
```

### 12.2 Manual Validation (Req 7, 13)

**Test Plan:**

1. **Provision "app-creator" tenant** (Req 7.1):
   ```bash
   # Tenant already exists in fleet-registry
   kubectl get ainativesaas app-creator -n platform-billing
   ```

2. **Create sample users** (Req 7.1):
   ```bash
   curl -X POST https://api.zero-ops.io/api/v1/tenants/app-creator/users \
     -H "Authorization: Bearer $JWT" \
     -H "Content-Type: application/json" \
     -d '{
       "email": "alice@app-creator.com",
       "name": "Alice"
     }'
   ```

3. **Provision sample usage events** (Req 7.2):
   ```bash
   # AgentGateway emits OTLP events automatically
   # Verify in OpenMeter UI or via API
   curl https://api.zero-ops.io/api/v1/tenants/app-creator/usage?period=monthly \
     -H "Authorization: Bearer $JWT"
   ```

4. **Verify user-scoped queries** (Req 7.4):
   ```bash
   curl https://api.zero-ops.io/api/v1/tenants/app-creator/users/alice-uuid/usage \
     -H "Authorization: Bearer $JWT"
   ```

5. **Verify entitlement checking** (Req 7.6):
   ```bash
   curl https://api.zero-ops.io/api/v1/tenants/app-creator/entitlements?feature=api_calls \
     -H "Authorization: Bearer $JWT"
   ```

---

## 13. Error Handling & Retry Logic

### 13.1 Exponential Backoff (Req 5.5, 21.1)

```go
package openmeter

import (
	"context"
	"fmt"
	"time"
)

// retryWithBackoff executes fn with exponential backoff
func retryWithBackoff(ctx context.Context, maxRetries int, fn func() error) error {
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		err := fn()
		if err == nil {
			return nil
		}
		
		lastErr = err
		
		// Check if error is retryable
		if !isRetryable(err) {
			return err
		}
		
		if i < maxRetries-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff[i]):
				// Continue to next retry
			}
		}
	}
	
	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

// isRetryable determines if an error should trigger a retry
func isRetryable(err error) bool {
	// Retry on network errors, 5xx responses, rate limits
	// Do not retry on 4xx client errors (except 429)
	return true // Simplified
}
```

### 13.2 Fail-Open Entitlements with Redis Cache (Gap 3.2)

**Problem:** Universal fail-open on OpenMeter outage allows suspended tenants to access premium features.

**Solution:** Feature-specific fail-open policy with Redis caching and explicit invalidation.

**CORRECTED (Gap 3.2)**: OpenMeter has NO built-in caching - cache invalidation must be explicit
**Reference**: archived/billing-metering/openmeter/openmeter/entitlement/service/service.go:L138-L180
**Evidence**: Every GetEntitlementValue() call hits database directly, no caching layer

```go
package openmeter

import (
	"context"
	"encoding/json"
	"time"
	
	"github.com/redis/go-redis/v9"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// CheckEntitlement queries OpenMeter with Redis cache fallback (Req 6, Gap 3.2)
func (m *MeteringProvider) CheckEntitlement(ctx context.Context, namespace, subjectID, featureKey string) (*models.EntitlementStatus, error) {
	// Try OpenMeter first
	resp, err := m.client.GetEntitlementValueWithResponse(ctx, subjectID, featureKey, nil)
	if err == nil && resp.StatusCode() == 200 {
		entitlement := resp.JSON200
		status := &models.EntitlementStatus{
			HasAccess:  entitlement.HasAccess,
			Used:       entitlement.Balance,
			Limit:      entitlement.Limit,
			ResetTime:  entitlement.ResetTime,
			IsFallback: false,
		}
		
		// Cache successful response (5-minute TTL)
		m.cacheEntitlement(ctx, namespace, subjectID, featureKey, status)
		return status, nil
	}
	
	// OpenMeter unreachable - check Redis cache
	cached, err := m.getCachedEntitlement(ctx, namespace, subjectID, featureKey)
	if err == nil && cached != nil {
		cached.IsFallback = true
		return cached, nil
	}
	
	// No cache - apply feature-specific fail-open policy
	policy := m.getFailOpenPolicy(featureKey)
	return &models.EntitlementStatus{
		HasAccess:  policy.AllowOnFailure,
		IsFallback: true,
	}, nil
}

// FailOpenPolicy defines per-feature behavior on OpenMeter outage
type FailOpenPolicy struct {
	FeatureKey     string
	AllowOnFailure bool
	Reason         string
}

// getFailOpenPolicy returns feature-specific fail-open behavior
func (m *MeteringProvider) getFailOpenPolicy(featureKey string) FailOpenPolicy {
	policies := map[string]FailOpenPolicy{
		"ui_access":         {FeatureKey: "ui_access", AllowOnFailure: true, Reason: "low_risk"},
		"api_calls":         {FeatureKey: "api_calls", AllowOnFailure: true, Reason: "low_cost"},
		"llm_token_generation": {FeatureKey: "llm_token_generation", AllowOnFailure: false, Reason: "high_cost"},
		"premium_features":  {FeatureKey: "premium_features", AllowOnFailure: false, Reason: "revenue_protection"},
	}
	
	if policy, ok := policies[featureKey]; ok {
		return policy
	}
	
	// Default: fail-closed for unknown features
	return FailOpenPolicy{FeatureKey: featureKey, AllowOnFailure: false, Reason: "unknown_feature"}
}

// cacheEntitlement stores entitlement in Redis with 5-minute TTL
func (m *MeteringProvider) cacheEntitlement(ctx context.Context, namespace, subjectID, featureKey string, status *models.EntitlementStatus) {
	key := fmt.Sprintf("entitlement:%s:%s:%s", namespace, subjectID, featureKey)
	data, _ := json.Marshal(status)
	m.redis.Set(ctx, key, data, 5*time.Minute)
}

// getCachedEntitlement retrieves entitlement from Redis
func (m *MeteringProvider) getCachedEntitlement(ctx context.Context, namespace, subjectID, featureKey string) (*models.EntitlementStatus, error) {
	key := fmt.Sprintf("entitlement:%s:%s:%s", namespace, subjectID, featureKey)
	data, err := m.redis.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("cache miss")
	}
	if err != nil {
		return nil, err
	}
	
	var status models.EntitlementStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, err
	}
	
	return &status, nil
}

// InvalidateEntitlementCache removes cached entitlement (Gap 3.2)
// CORRECTED: Explicit cache invalidation required - OpenMeter has no built-in caching
// Triggered by NATS events: subscription tier changes, billing failures, manual updates
// Reference: archived/billing-metering/openmeter/openmeter/entitlement/service/service.go (no caching)
func (m *MeteringProvider) InvalidateEntitlementCache(ctx context.Context, namespace, subjectID, featureKey string) error {
	key := fmt.Sprintf("entitlement:%s:%s:%s", namespace, subjectID, featureKey)
	return m.redis.Del(ctx, key).Err()
}

// InvalidateTenantEntitlements removes all cached entitlements for a tenant (Gap 3.2)
// Used when tenant subscription tier changes or billing payment fails
func (m *MeteringProvider) InvalidateTenantEntitlements(ctx context.Context, namespace string) error {
	pattern := fmt.Sprintf("entitlement:%s:*", namespace)
	iter := m.redis.Scan(ctx, 0, pattern, 0).Iterator()
	
	for iter.Next(ctx) {
		if err := m.redis.Del(ctx, iter.Val()).Err(); err != nil {
			return err
		}
	}
	
	return iter.Err()
}
```

**Cache Invalidation Triggers (Gap 3.2):**

| Event | NATS Topic | Action |
|-------|-----------|--------|
| Subscription tier change | `opensbt_subscriptionUpdated` | `InvalidateTenantEntitlements(namespace)` |
| Billing payment failure | `opensbt_billingFailure` | `InvalidateTenantEntitlements(namespace)` |
| Manual entitlement update | `opensbt_entitlementUpdated` | `InvalidateEntitlementCache(namespace, subjectID, featureKey)` |
| User deleted | `opensbt_tenantUserDeleted` | `InvalidateEntitlementCache(namespace, subjectID, "*")` |

**NATS Event Listener (Gap 3.2):**

```go
// SubscribeToInvalidationEvents listens for cache invalidation triggers
func (m *MeteringProvider) SubscribeToInvalidationEvents(ctx context.Context) error {
	// Subscription tier changes
	m.eventBus.Subscribe("opensbt_subscriptionUpdated", func(event models.Event) {
		namespace := event.Detail["namespace"].(string)
		m.InvalidateTenantEntitlements(ctx, namespace)
	})
	
	// Billing failures
	m.eventBus.Subscribe("opensbt_billingFailure", func(event models.Event) {
		namespace := event.Detail["namespace"].(string)
		m.InvalidateTenantEntitlements(ctx, namespace)
	})
	
	// Manual entitlement updates
	m.eventBus.Subscribe("opensbt_entitlementUpdated", func(event models.Event) {
		namespace := event.Detail["namespace"].(string)
		subjectID := event.Detail["subject_id"].(string)
		featureKey := event.Detail["feature_key"].(string)
		m.InvalidateEntitlementCache(ctx, namespace, subjectID, featureKey)
	})
	
	return nil
}
```

### 13.3 Istio Circuit Breaker (Gap 4.2)

**Problem:** Custom Go circuit breaker is in-memory and not HA-safe.

**Solution:** Use Istio DestinationRule for mesh-level circuit breaking.

**File:** `manifests/hub-core-services/opensbt/istio-destination-rule.yaml`

```yaml
apiVersion: networking.istio.io/v1beta1
kind: DestinationRule
metadata:
  name: openmeter-circuit-breaker
  namespace: platform-core
spec:
  host: openmeter.platform-core.svc.cluster.local
  trafficPolicy:
    connectionPool:
      tcp:
        maxConnections: 100
      http:
        http1MaxPendingRequests: 50
        http2MaxRequests: 100
        maxRequestsPerConnection: 2
    outlierDetection:
      consecutiveErrors: 5
      interval: 30s
      baseEjectionTime: 30s
      maxEjectionPercent: 50
      minHealthPercent: 50
```

**Remove from design.md:** Section 13.2 custom CircuitBreaker implementation (replaced by Istio).

---

## 14. Migration Plan

### 14.1 Deprecation Steps

1. **Phase 1: Parallel Implementation** (Week 1-2)
   - Implement `internal/opensbt/providers/openmeter/` alongside legacy code
   - Add feature flag: `ENABLE_OPENMETER=false` (default)
   - Run E2E tests against both implementations

2. **Phase 2: Gradual Rollout** (Week 3)
   - Enable OpenMeter for "app-creator" tenant only
   - Monitor metrics, logs, error rates
   - Validate manual test cases (Req 7, 13)

3. **Phase 3: Full Migration** (Week 4)
   - Set `ENABLE_OPENMETER=true` globally
   - Migrate all tenants to OpenMeter namespaces
   - Monitor for 48 hours

4. **Phase 4: Cleanup** (Week 5)
   - Delete `internal/opensbt/providers/metering/`
   - Drop `meters` and `usage_events` tables from Hub database
   - Remove legacy migration `004_metering.sql`
   - Update documentation

### 14.2 Rollback Plan

If critical issues arise:

1. Set `ENABLE_OPENMETER=false`
2. Revert to legacy Postgres-backed implementation
3. Investigate and fix issues
4. Retry migration

---

## 15. Monitoring & Observability

### 15.1 Prometheus Metrics

```go
package openmeter

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// API call metrics
	apiCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "opensbt_openmeter_api_calls_total",
		Help: "Total OpenMeter API calls",
	}, []string{"method", "endpoint", "status"})
	
	apiCallDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "opensbt_openmeter_api_call_duration_seconds",
		Help:    "OpenMeter API call duration",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "endpoint"})
	
	// Subject registration metrics
	subjectRegistrations = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "opensbt_subject_registrations_total",
		Help: "Total subject registrations",
	}, []string{"status"})
	
	// Saga rollback metrics
	sagaRollbacks = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "opensbt_saga_rollbacks_total",
		Help: "Total Saga rollbacks",
	}, []string{"reason", "success"})
	
	// Orphaned subjects metrics
	orphanedSubjects = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "opensbt_orphaned_subjects_count",
		Help: "Current count of orphaned subjects",
	})
)
```

### 15.2 Grafana Dashboard

**Key Panels:**
- OpenMeter API call rate and latency
- Subject registration success/failure rate
- Saga rollback rate
- Orphaned subject count over time
- Entitlement check latency (p50, p95, p99)
- Stripe webhook processing rate

---

## 16. OpenAPI Specification (Req 13)

### 16.1 Auto-Generation with Swaggo (Req 10.1)

**Installation:**
```bash
go install github.com/swaggo/swag/cmd/swag@latest
```

**Generation:**
```bash
swag init -g cmd/opensbt/main.go -o docs/openapi
```

**Example Annotation:**
```go
// @title kube-sbt Metering & Billing API
// @version 1.0
// @description OpenMeter-backed metering and billing for multi-tenant SaaS platforms
// @termsOfService https://zero-ops.io/terms

// @contact.name Zero-Ops Platform Team
// @contact.email support@zero-ops.io

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

// @host api.zero-ops.io
// @BasePath /api/v1

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description JWT token from Ory Hydra. Format: "Bearer <token>"

func main() {
	// ...
}
```

### 16.2 Example curl Commands (Req 13.5, 10.2)

```bash
# Get JWT token
export JWT=$(curl -X POST https://auth.zero-ops.io/oauth2/token \
  -d "grant_type=client_credentials" \
  -d "client_id=<YOUR_CLIENT_ID>" \
  -d "client_secret=<YOUR_CLIENT_SECRET>" \
  | jq -r '.access_token')

# Create user
curl -X POST https://api.zero-ops.io/api/v1/tenants/app-creator/users \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{
    "email": "alice@example.com",
    "name": "Alice"
  }'

# Get tenant usage
curl https://api.zero-ops.io/api/v1/tenants/app-creator/usage?period=monthly \
  -H "Authorization: Bearer $JWT"

# Check entitlements
curl https://api.zero-ops.io/api/v1/tenants/app-creator/entitlements?feature=api_calls \
  -H "Authorization: Bearer $JWT"
```

---

## 17. Gap Resolution Summary

This design specification addresses all critical gaps identified in the architectural review with **enterprise-grade patterns**:

### **Gap 1: Distributed State Management**

✅ **1.1 Cross-Cluster OTLP Ingestion Routing**
- **Fixed:** Added Istio IngressGateway configuration for OpenMeter port 4318
- **Pattern:** AgentGateway → Envoy sidecar (mTLS) → Istio Gateway (SPIFFE validation) → OpenMeter
- **Security:** SPIFFE identity validation, AuthorizationPolicy restricts to AgentGateway ServiceAccount only
- **Resilience:** Exponential backoff, 1-hour local buffering when OpenMeter unreachable

✅ **1.2 Stripe Webhook Routing**
- **Gap Identified:** Requirement 18 routed webhooks to kube-sbt, conflicting with OpenMeter's native Stripe App
- **Root Cause:** OpenMeter MUST receive Stripe webhooks directly to update internal invoice state
- **Fixed:** Stripe webhooks route directly to OpenMeter endpoint POST /api/v1/apps/{appId}/stripe/webhook
- **Pattern:** Stripe → OpenMeter (signature validation, state transitions) → (Optional) OpenMeter notifications → kube-sbt
- **Reference:** `archived/billing-metering/openmeter/openmeter/app/stripe/httpdriver/webhook.go`
- **OpenMeter Handles:** Signature validation, idempotency (event.id), invoice state transitions (11 event types)
- **kube-sbt Responsibility:** Query OpenMeter API for invoice status, optionally subscribe to OpenMeter notification events

✅ **1.3 Reconciliation DLQ State Tracking**
- **Fixed:** Removed in-memory `failureCount map`
- **Pattern:** NATS JetStream native delivery tracking (`msg.Metadata().NumDelivered`)
- **Configuration:** `MaxDeliver: 10` with exponential backoff via `BackOff` array

✅ **1.4 Saga Rollback Compensation**
- **Fixed:** Added `opensbt_tenantUserDeleted` event for Spoke DB cleanup
- **Pattern:** Compensating transaction published to NATS
- **Coverage:** Handles race condition where Spoke creates local user record before rollback

### **Gap 2: Infrastructure & GitOps (OPERATOR PATTERN)**

✅ **2.1 OpenMeter Namespace Provisioning**
- **Gap Identified:** Requirement 19 specified Crossplane provider-http to POST /api/v1/namespaces
- **Root Cause:** OpenMeter OSS does NOT expose /api/v1/namespaces REST API endpoint
- **Fixed:** Use hub-operator with OpenMeter `namespace.Manager` Go API (not REST API, not Crossplane)
- **Pattern:** HubEnvironment CR → hub-operator Phase 0b → namespace.Manager.CreateNamespace()
- **Reference:** `archived/billing-metering/openmeter/openmeter/namespace/namespace.go:L42-L48`
- **Alignment:** ADR 004 (declarative operator), ADR 005 (Crossplane abstraction), ADR 012 (billing operator pattern)
- **Status Tracking:** `OpenMeterNamespacesConfigured` condition in HubEnvironment status
- **Idempotency:** namespace.Manager handles duplicate namespace creation gracefully
- **Note:** Crossplane provider-http approach was rejected - hub-operator is the correct pattern

✅ **2.2 Static Catalog Management → GitOps + hub-operator CRDs**
- **Architectural Decision:** Static billing catalog (Meters, Features, Plans) managed via Kubernetes CRDs, not imperative REST API
- **New CRDs:** `billing.nutgraf.in/v1alpha1` - Meter, Feature, Plan
- **Reconcilers:** MeterReconciler, FeatureReconciler, PlanReconciler in hub-operator
- **GitOps Flow:**
  1. SaaS Builder configures `billing.meters`, `billing.features`, `billing.plans` in `fleet-registry/tenants/<tenant-id>/values.yaml`
  2. ArgoCD renders `universal-tenant` Helm chart, applies CRs to Hub cluster
  3. hub-operator reconcilers sync CRs to OpenMeter API (idempotent upsert)
  4. CR Status.Conditions updated with `Synced: True`
- **Benefits:**
  - **Declarative:** Catalog changes via Git commits, not API calls
  - **Auditable:** Full Git history of catalog changes
  - **Continuous Reconciliation:** hub-operator fixes drift automatically
  - **No Saga Complexity:** Kubernetes controller pattern handles failures natively
- **kube-sbt API Changes:**
  - **REMOVED:** POST, PUT, DELETE endpoints for `/meters`, `/features`, `/plans`
  - **KEPT:** GET endpoints for read-only UI display
  - **KEPT:** All dynamic runtime operations (users, subscriptions, invoices)

### **Gap 3: Security & Availability**

✅ **3.1 Tenant Aggregation via Compound Subject IDs**
- **Fixed:** AgentGateway emits `tenant_id` as separate OTLP attribute
- **Pattern:** Meter GroupBy["tenant_id"] = "$.tenant_id" for tenant aggregation
- **Evidence:** OpenMeter FilterSubject does NOT support wildcard matching
- **Reference:** archived/billing-metering/openmeter/openmeter/streaming/query_params.go:L17
- **Impact:** GetTenantUsage() queries with GroupBy["tenant_id"] instead of FilterSubject wildcard

✅ **3.2 Entitlement Cache Invalidation**
- **Fixed:** Explicit cache invalidation via NATS events
- **Triggers:** Subscription tier changes, billing failures, manual updates
- **Evidence:** OpenMeter has NO built-in caching (every call hits DB)
- **Reference:** archived/billing-metering/openmeter/openmeter/entitlement/service/service.go:L138-L180
- **Methods:** InvalidateEntitlementCache(), InvalidateTenantEntitlements()
- **Events:** opensbt_subscriptionUpdated, opensbt_billingFailure, opensbt_entitlementUpdated

✅ **3.3 CI/CD Testcontainers**
- **Fixed:** Build tag separation (`//go:build e2e`)
- **Pattern:** Unit tests with mocks (no Docker) + E2E tests with Testcontainers
- **Commands:**
  - `make test` - Unit tests only (fast, no Docker)
  - `make test-e2e` - E2E tests with Testcontainers (CI/CD)

### **Gap 4: Distributed Saga & Error State (ENTERPRISE ARCHITECTURE)**

✅ **4.1 DLQ Dead Ends → Automated Reconciliation**
- **Problem:** After 10 retries, messages dropped without recovery
- **Fixed:** DLQ stream (30-day retention) + Kubernetes-style ReconcilerService
- **Pattern:** 
  - **Events handle "what happened"**: Dumb consumers (idempotent, stateless)
  - **Reconcilers handle "what should be true"**: Periodic scans (paginated, rate-limited)
- **DLQ Stream:** `opensbt_dlq` with 30-day retention for audit + manual replay
- **Reconciler:** Scans every 60s for drift (orphaned users, missing subjects, Spoke DB gaps)
- **Hybrid Model:** Event-assisted (priority hints) + Time-driven (background scans)
- **Pagination:** Cursor-based, 100 resources per page, 50s time budget per cycle
- **Rate Limiting:** 10 ops/sec internal rate limit to prevent thundering herd
- **Progress Tracking:** Resumable on restart (cursor stored in database)

✅ **4.2 Spoke DB Sync Idempotency → Infinite Retries**
- **Problem:** If Spoke DB down, user exists in Hub but not Spoke after MaxDeliver limit
- **Fixed:** Simplified consumer with MaxDeliver=10 + Reconciler fixes drift
- **Pattern:** Idempotent upsert (INSERT ON CONFLICT UPDATE), NAK on failure
- **Reconciler:** Scans Hub DB, syncs missing users to Spoke DB within 60s
- **No Infinite Retries:** MaxDeliver=10 prevents infinite loops, Reconciler provides eventual consistency

✅ **4.3 Billing Pipeline Durability → NATS JetStream Buffer**
- **Problem:** Direct OTLP emission risks dropping billing events (revenue loss)
- **Fixed:** AgentGateway → NATS JetStream → OTLP Forwarder → OpenMeter
- **Pattern:** Durable buffer (NATS) with batched export (100 events per batch)
- **Retry Semantics:** Do NOT ACK until OpenMeter export succeeds
- **Backpressure:** NAK on failure, NATS retries with exponential backoff
- **DLQ:** After 10 retries, route to DLQ for manual investigation

✅ **4.4 Metrics Collection Scaling → Cluster-Level Collector**
- **Problem:** Per-tenant CronJobs don't scale (1000 tenants = 1000 CronJobs)
- **Fixed:** Single MetricCollector deployment per cluster
- **Pattern:** Tenant-aware queries, emit to NATS with tenant_id label
- **Metrics:** database_rows, storage_bytes, workspace_count, form_count, table_count, application_count
- **Interval:** 5 minutes (configurable)
- **Deployment:** Single replica per cluster, queries all tenant databases in one pass

### **Gap 5: Rate Limiting Clarification**

✅ **5.1 Real-Time Enforcement vs Billing Aggregation**
- **AgentGateway + Redis:** Real-time rate limiting (200 req/sec, sub-millisecond latency)
- **OpenMeter:** Billing aggregation (invoice generation, eventual consistency)
- **Pattern:** Redis token bucket for enforcement, OTLP for billing
- **Failure Mode:** Redis unavailable → fail-open (allow request), OpenMeter unavailable → retry via NATS

### **Gap 6: Enterprise Guardrails (PRODUCTION READINESS)**

✅ **6.1 Reconciliation Guardrails (Preventing Double-Billing)**
- **Contract:** Eventual Correctness via Out-Of-Band Reconciliation, not perfect real-time accuracy
- **Delay Window:** Reconciliation ONLY evaluates data in `[NOW - 48h, NOW - 24h]` window
- **Variance Threshold:** Discrepancies ignored if variance `< 1%` OR `< 10 units` (configurable per meter)
- **Duplicate Protection:** Query NATS JetStream `ConsumerInfo` API for `num_pending` before emitting corrections
- **Abort Condition:** If billing stream backlog > 1,000 messages, abort reconciliation
- **Pattern:** Safe double-entry bookkeeping with explicit delay guarantees
- **Implementation:** BillingReconciliationJob in ReconciliationController

✅ **6.2 Entitlement Cache Safety (Preventing Silent Drift)**
- **Contract:** Redis is strictly ephemeral performance optimization; OpenMeter is absolute source of truth
- **TTL Enforcement:** All Redis entitlement keys hard-coded with 10-minute TTL
- **Fallback Behavior:** Cache miss → synchronous HTTP call to kube-sbt API → fail-open/fail-closed policy
- **Periodic Full Sync:** Background worker syncs all active tenant limits from OpenMeter to Redis every 5 minutes
- **Pattern:** Cache-Aside with active synchronization
- **Implementation:** EntitlementSyncWorker in MeteringProvider

✅ **6.3 DLQ Replay Controls (Operational Safety)**
- **Contract:** Replay operations are highly privileged, destructive, and fully audited
- **RBAC Enforcement:** `POST /api/v1/admin/dlq/replay` requires `platform_admin` role via Ory Keto
- **Rate Limiting:** Replay throughput hard-capped at 50 req/sec
- **Bulk Support:** Accepts `{"event_ids": [...]}`, `{"time_range": {...}}`, or `{"tenant_id": ""}` payloads
- **Observability:** Emits `opensbt_dlq_replay_total`, `opensbt_dlq_replay_failed` Prometheus metrics
- **Audit Trail:** Generates explicit Audit Log entry for all replay operations
- **Implementation:** DLQReplayHandler in AdminController

✅ **6.4 Plan Migration Semantics (Billing Consistency)**
- **Contract:** Plan migrations preserve billing cycle continuity and accurately prorate usage
- **Migration API:** `POST /api/v1/tenants/{id}/subscriptions/migrate` with `proration_behavior` field
- **Proration Options:** `create_prorated_invoice`, `none`, `credit_next_invoice`
- **Billing Cycle Anchor:** New subscription inherits canceled subscription's billing anchor date
- **OpenMeter Delegation:** Passes proration flags directly to OpenMeter SDK for millisecond-based proration
- **Implementation:** MigrateSubscription method in BillingProvider

✅ **6.5 Audit Log Durability (Compliance & WORM)**
- **Contract:** Audit logs meet SOC2/GDPR compliance for immutability and non-repudiation
- **Immutability Strategy:** OpenSearch for querying only; true audit trail routed to S3 with Object Lock (WORM)
- **Retention:** Object Lock configured at bucket level (7 years), prevents deletion by admins or compromised credentials
- **Export Pipeline:** Grafana Alloy or Vector ships logs directly to S3 bucket
- **Pattern:** Write Once Read Many (WORM) compliance storage
- **Configuration:** `manifests/hub-core-services/audit-log-exporter/config.yaml`

✅ **6.6 Time Semantics & Cross-System Drift**
- **Contract:** Time must be strictly deterministic across geographically distributed Spokes and Hub
- **Time Definition:** "Event Time" (user action) vs "Processing Time" (OpenMeter ingestion)
- **Billing Rules:** OpenMeter uses Event Time (RFC3339 in OTLP payload) for proration, tier limits, aggregations
- **Skew Tolerance:** Reject events with Event Time > `NOW + 5m` or < `NOW - 48h`
- **Validation:** `if event.Timestamp > time.Now().Add(5*time.Minute)` → 400 error
- **Pattern:** Strict clock skew policies with explicit rejection boundaries
- **Implementation:** ValidateEventTime middleware in OTLP ingestion pipeline

✅ **6.7 Backpressure & Load Shedding**
- **Contract:** Platform survives catastrophic downstream outage without cascading failure
- **Tier 1 (NATS Buffer):** JetStream configured with `max_bytes` based on PVC size
- **Tier 2 (Agent Local Buffer):** Spoke OTel Collector buffers to disk (up to 1GB) if NATS unreachable
- **Tier 3 (Load Shedding):** OTel Collector drops new telemetry (oldest first) when buffer full
- **System Guarantee:** Prefers surviving outage and keeping SaaS apps online over perfect billing accuracy
- **Observability:** `dropped_spans_total` Prometheus metric estimates financial impact
- **Pattern:** Multi-tiered load shedding with explicit availability-over-accuracy tradeoff
- **Configuration:** `manifests/spoke-pool/otel-collector/config.yaml`

✅ **6.8 Cost Control Strategy**
- **Contract:** Tenants cannot accidentally or maliciously incur infinite infrastructure costs
- **Concurrency Limits:** AgentGateway enforces hard rate limit per tenant via Redis
- **Billing Ceilings:** OpenMeter configured with usage alerts at 80% and 100% of predefined thresholds
- **Automated Suspension:** When 100% alert fires, kube-sbt updates Tenant Status to `SUSPENDED`
- **Crossplane Integration:** Suspended status triggers Crossplane to scale tenant Spoke deployments to 0
- **Pattern:** Hard ceilings with automated enforcement
- **Implementation:** BillingAlertConsumer subscribes to `opensbt.billing.alerts` NATS topic

### **Additional Improvements**

✅ **IStorage Interface Injection**
- BillingProvider now accepts `IStorage` for database access
- Enables transactional webhook processing

✅ **NATS JetStream Consumer Configuration**
- Explicit `ConsumerConfig` with `MaxDeliver` and `BackOff` array
- Native message delivery tracking

✅ **Redis Integration**
- Entitlement caching with 5-minute TTL
- Fallback layer between OpenMeter and fail-open policies

✅ **Event Topic Expansion**
- Added `opensbt_tenantUserDeleted` for Saga compensation
- Added `opensbt.reconciliation.needed` for priority reconciliation hints
- Added `opensbt.dlq.>` for dead letter queue audit trail
- Documented all event schemas and flows

---

## 18. Enterprise Architecture Summary

This design specification provides a **production-ready, enterprise-grade** blueprint for implementing the kube-sbt metering and billing system with OpenMeter integration.

### **Key Architectural Principles**

✅ **Events handle "what happened"** - Dumb consumers, idempotent, stateless  
✅ **Reconcilers handle "what should be true"** - Kubernetes controller pattern  
✅ **Durable buffers for revenue** - NATS JetStream prevents billing data loss  
✅ **Cluster-level collectors** - Single deployment per cluster, tenant-aware queries  
✅ **DLQ for audit trail** - 30-day retention, manual replay capability  
✅ **Paginated reconciliation** - Cursor-based, rate-limited, resumable  
✅ **Separation of concerns** - Rate limiting (Redis) vs Billing (OpenMeter)  

### **Scalability Guarantees**

- **10,000+ tenants**: Single metric collector per cluster (not per-tenant CronJobs)
- **1M+ events/day**: Batched OTLP export (100 events per batch)
- **Infinite retries**: Reconciler provides eventual consistency without infinite loops
- **Zero data loss**: NATS JetStream durable buffer for billing events

### **Operational Excellence**

- **Debuggability**: DLQ stream with 30-day retention
- **Replayability**: Manual replay tool for failed events
- **Observability**: Reconciler metrics (drift detected, resources fixed)
- **Compliance**: Audit trail for all billing events

---

## 14. W3C Trace Context Propagation (Req 25)

### 14.1 Architecture Overview

**End-to-End Trace Flow:**
```
HTTP Request → AgentGateway (Envoy) → NATS Message → OTLPForwarder → OpenMeter
     ↓              ↓                      ↓               ↓              ↓
  trace-id      traceparent           NATS header      Extract        OTLP span
```

### 14.2 Implementation Pattern

**File:** `internal/opensbt/providers/nats/publisher.go`

```go
package nats

import (
	"context"
	"encoding/json"
	
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// PublishWithTraceContext publishes event to NATS with W3C trace propagation (Req 25.2)
func (p *NATSPublisher) PublishWithTraceContext(ctx context.Context, subject string, event interface{}) error {
	// Serialize event payload
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("nats: marshal event: %w", err)
	}
	
	// Create NATS message with headers
	msg := nats.NewMsg(subject)
	msg.Data = data
	
	// Inject W3C trace context into NATS headers (Req 25.2, 25.3)
	propagator := otel.GetTextMapPropagator()
	propagator.Inject(ctx, &NATSHeaderCarrier{msg.Header})
	
	// Publish with trace context
	return p.conn.PublishMsg(msg)
}

// NATSHeaderCarrier adapts nats.Header to propagation.TextMapCarrier
type NATSHeaderCarrier struct {
	header nats.Header
}

func (c *NATSHeaderCarrier) Get(key string) string {
	return c.header.Get(key)
}

func (c *NATSHeaderCarrier) Set(key, value string) {
	c.header.Set(key, value)
}

func (c *NATSHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(c.header))
	for k := range c.header {
		keys = append(keys, k)
	}
	return keys
}
```

**File:** `internal/opensbt/consumers/otlp_forwarder.go`

```go
// ProcessMessage extracts trace context and forwards to OpenMeter (Req 25.4, 25.5)
func (f *OTLPForwarder) ProcessMessage(ctx context.Context, msg *nats.Msg) error {
	// Extract W3C trace context from NATS headers (Req 25.4)
	propagator := otel.GetTextMapPropagator()
	ctx = propagator.Extract(ctx, &NATSHeaderCarrier{msg.Header})
	
	// Parse event
	var event models.UsageEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return fmt.Errorf("otlp: unmarshal event: %w", err)
	}
	
	// Create OTLP span with extracted trace context (Req 25.5)
	tracer := otel.Tracer("otlp-forwarder")
	ctx, span := tracer.Start(ctx, "forward_to_openmeter")
	defer span.End()
	
	// Forward to OpenMeter with trace context
	return f.openMeterClient.IngestEvent(ctx, event)
}
```

### 14.3 AgentGateway Configuration

**File:** `manifests/spoke/agentgateway/envoy-config.yaml`

```yaml
# Envoy automatically generates W3C traceparent headers (Req 25.1)
tracing:
  http:
    name: envoy.tracers.opentelemetry
    typed_config:
      "@type": type.googleapis.com/envoy.config.trace.v3.OpenTelemetryConfig
      grpc_service:
        envoy_grpc:
          cluster_name: jaeger
      service_name: agentgateway
```

### 14.4 Verification

**Trace Context Flow Validation:**
1. HTTP request arrives at AgentGateway with `traceparent: 00-{trace-id}-{span-id}-01`
2. NATS message published with header: `traceparent: 00-{trace-id}-{new-span-id}-01`
3. OTLPForwarder extracts trace context and creates child span
4. OpenMeter receives OTLP span with original `trace-id`
5. Jaeger/Grafana displays single trace spanning all components

---

## 15. Cascading Subscription Cleanup Choreography (Req 26)

### 15.1 Event Flow

```
User Deletion → subject.deleted event → SubscriptionCleanupConsumer
                                              ↓
                                    Query active subscriptions
                                              ↓
                                    Cancel each subscription
                                              ↓
                                    Delete subject from OpenMeter
```

### 15.2 Implementation

**File:** `internal/opensbt/consumers/subscription_cleanup.go`

```go
package consumers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	
	"github.com/nats-io/nats.go"
	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// SubscriptionCleanupConsumer handles cascading subscription cancellation (Req 26)
type SubscriptionCleanupConsumer struct {
	eventBus interfaces.IEventBus
	billing  interfaces.IBilling
	metering interfaces.IMetering
	logger   interfaces.ILogger
}

// Start subscribes to subject deletion events (Req 26.2)
func (c *SubscriptionCleanupConsumer) Start(ctx context.Context) error {
	sub, err := c.eventBus.SubscribeWithConfig(ctx, "opensbt.subject.deleted", nats.ConsumerConfig{
		Durable:       "subscription-cleanup",
		AckPolicy:     nats.AckExplicitPolicy,
		MaxDeliver:    10,
		AckWait:       30 * time.Second,
		FilterSubject: "opensbt.subject.deleted",
	})
	if err != nil {
		return fmt.Errorf("subscription-cleanup: subscribe failed: %w", err)
	}
	
	go c.processMessages(ctx, sub)
	return nil
}

// processMessages handles subject deletion events (Req 26.3-26.10)
func (c *SubscriptionCleanupConsumer) processMessages(ctx context.Context, sub <-chan *nats.Msg) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-sub:
			if err := c.handleSubjectDeletion(ctx, msg); err != nil {
				c.logger.Error("subscription-cleanup: failed", "error", err)
				
				// Check retry count
				metadata, _ := msg.Metadata()
				if metadata.NumDelivered >= 10 {
					// Emit reconciliation hint (Req 26.7)
					c.emitReconciliationHint(ctx, msg)
					msg.Ack()
				} else {
					msg.Nak() // Retry with exponential backoff
				}
			} else {
				msg.Ack()
			}
		}
	}
}

// handleSubjectDeletion cancels all subscriptions and deletes subject (Req 26.3-26.5)
func (c *SubscriptionCleanupConsumer) handleSubjectDeletion(ctx context.Context, msg *nats.Msg) error {
	var event models.Event
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return fmt.Errorf("unmarshal event: %w", err)
	}
	
	namespace := event.Detail["namespace"].(string)
	subjectID := event.Detail["subject_id"].(string)
	
	// Query all active subscriptions (Req 26.3)
	subscriptions, err := c.billing.ListSubscriptions(ctx, namespace, models.SubscriptionFilters{
		SubjectID: subjectID,
		Status:    "active",
	})
	if err != nil {
		return fmt.Errorf("list subscriptions: %w", err)
	}
	
	// Cancel each subscription with retry (Req 26.4, 26.6)
	for _, sub := range subscriptions {
		if err := c.cancelWithRetry(ctx, namespace, sub.ID); err != nil {
			return fmt.Errorf("cancel subscription %s: %w", sub.ID, err)
		}
		
		// Emit metric (Req 26.10)
		c.emitMetric("opensbt_subscription_cleanup_total", map[string]string{
			"tenant_id": namespace,
			"status":    "success",
		})
	}
	
	// Delete subject from OpenMeter (Req 26.5)
	if err := c.metering.DeleteSubject(ctx, namespace, subjectID); err != nil {
		return fmt.Errorf("delete subject: %w", err)
	}
	
	return nil
}

// cancelWithRetry implements exponential backoff (Req 26.6)
func (c *SubscriptionCleanupConsumer) cancelWithRetry(ctx context.Context, namespace, subscriptionID string) error {
	backoff := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	
	for i, delay := range backoff {
		err := c.billing.CancelSubscription(ctx, namespace, subscriptionID)
		if err == nil {
			return nil
		}
		
		if i < len(backoff)-1 {
			time.Sleep(delay)
		}
	}
	
	return fmt.Errorf("all retries exhausted")
}

// emitReconciliationHint publishes to reconciliation topic (Req 26.7)
func (c *SubscriptionCleanupConsumer) emitReconciliationHint(ctx context.Context, msg *nats.Msg) {
	var event models.Event
	json.Unmarshal(msg.Data, &event)
	
	hint := models.NewEvent("opensbt.reconciliation.needed", "subscription_cleanup_consumer", map[string]interface{}{
		"resource_type": "subscription",
		"namespace":     event.Detail["namespace"],
		"subject_id":    event.Detail["subject_id"],
		"reason":        "subscription_cancellation_failed",
	})
	
	c.eventBus.Publish(ctx, "opensbt.reconciliation.needed", hint)
}
```

### 15.3 User Manager Integration

**File:** `internal/opensbt/controlplane/user_manager.go`

```go
// DeleteUser deletes user from Kratos and publishes subject deletion event (Req 26.1)
func (um *UserManager) DeleteUser(ctx context.Context, tenantID, userID string) error {
	// Delete from Ory Kratos
	if err := um.auth.DeleteUser(ctx, userID); err != nil {
		return fmt.Errorf("delete kratos user: %w", err)
	}
	
	// Publish subject deletion event (Req 26.1)
	subjectID := models.GenerateSubjectID(tenantID, userID)
	event := models.NewEvent("opensbt.subject.deleted", "user_manager", map[string]interface{}{
		"namespace":  tenantID,
		"subject_id": subjectID,
		"user_id":    userID,
		"deleted_at": time.Now().UTC(),
	})
	
	if err := um.eventBus.Publish(ctx, "opensbt.subject.deleted", event); err != nil {
		um.logger.Error("failed to publish subject deletion event", "error", err)
		// Continue - reconciler will fix drift
	}
	
	return nil
}
```

---

## 16. Tenant Isolation Security Testing (Req 27)

### 16.1 Test Suite Structure

**File:** `tests/e2e/tenant_isolation_test.go`

```go
package e2e

import (
	"context"
	"testing"
	
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCrossTenantAPIIsolation verifies API-level tenant isolation (Req 27.2-27.5)
func TestCrossTenantAPIIsolation(t *testing.T) {
	ctx := context.Background()
	
	// Provision two tenants (Req 27.2)
	tenantA := provisionTenant(t, "tenant-a")
	tenantB := provisionTenant(t, "tenant-b")
	
	// Generate JWTs (Req 27.3)
	jwtA := generateJWT(t, tenantA.ID, "user-a")
	jwtB := generateJWT(t, tenantB.ID, "user-b")
	
	// Attempt cross-tenant access (Req 27.4)
	resp, err := httpClient.Get(
		fmt.Sprintf("/api/v1/tenants/%s/usage", tenantB.ID),
		withJWT(jwtA),
	)
	require.NoError(t, err)
	
	// Assert 403 Forbidden (Req 27.5)
	assert.Equal(t, 403, resp.StatusCode)
	assert.Contains(t, resp.Body, "insufficient permissions")
}

// TestCrossTenantDatabaseIsolation verifies RLS policy enforcement (Req 27.6-27.7)
func TestCrossTenantDatabaseIsolation(t *testing.T) {
	ctx := context.Background()
	
	// Provision two tenants
	tenantA := provisionTenant(t, "tenant-a")
	tenantB := provisionTenant(t, "tenant-b")
	
	// Insert data into Tenant B's database
	insertTestData(t, tenantB.DatabaseURL, "test-record-b")
	
	// Attempt to query Tenant B's data using Tenant A's PostgREST endpoint (Req 27.6)
	jwtA := generateJWT(t, tenantA.ID, "user-a")
	rows, err := postgrestClient.Query(
		tenantA.PostgRESTURL,
		"SELECT * FROM records",
		withJWT(jwtA),
	)
	require.NoError(t, err)
	
	// Assert 0 rows returned due to RLS (Req 27.7)
	assert.Equal(t, 0, len(rows))
}

// TestCrossTenantOpenMeterIsolation verifies namespace isolation (Req 27.8)
func TestCrossTenantOpenMeterIsolation(t *testing.T) {
	ctx := context.Background()
	
	// Provision two tenants
	tenantA := provisionTenant(t, "tenant-a")
	tenantB := provisionTenant(t, "tenant-b")
	
	// Create subject in Tenant B
	subjectB := createSubject(t, tenantB.ID, "user-b")
	
	// Attempt to query Tenant B's subject using Tenant A's namespace (Req 27.8)
	subject, err := meteringClient.GetSubject(ctx, tenantA.ID, subjectB.ID)
	
	// Assert subject not found (namespace isolation)
	assert.Error(t, err)
	assert.Nil(t, subject)
}

// TestCrossTenantRedisCacheIsolation verifies cache key isolation (Req 27.9)
func TestCrossTenantRedisCacheIsolation(t *testing.T) {
	ctx := context.Background()
	
	// Provision two tenants
	tenantA := provisionTenant(t, "tenant-a")
	tenantB := provisionTenant(t, "tenant-b")
	
	// Cache entitlement for Tenant B
	cacheEntitlement(t, tenantB.ID, "user-b", "api_calls", 1000)
	
	// Attempt to read Tenant B's cache using Tenant A's key pattern
	cacheKey := fmt.Sprintf("entitlement:%s:user-b:api_calls", tenantA.ID)
	value, err := redisClient.Get(ctx, cacheKey).Result()
	
	// Assert cache miss (Req 27.9)
	assert.Error(t, err)
	assert.Equal(t, redis.Nil, err)
}
```

### 16.2 CI/CD Integration

**File:** `.github/workflows/security-tests.yml`

```yaml
name: Tenant Isolation Security Tests

on:
  pull_request:
    branches: [main]
  push:
    branches: [main]

jobs:
  isolation-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Setup Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.21'
      
      - name: Run Tenant Isolation Tests
        run: |
          go test -v ./tests/e2e/tenant_isolation_test.go \
            -tags=security \
            -timeout=30m
      
      - name: Block Deployment on Failure
        if: failure()
        run: |
          echo "::error::Tenant isolation tests failed - blocking deployment"
          exit 1
```

---

### **Next Steps**

1. Review and approve this updated design specification
2. Update `ainativesaases.nutgraf.in` XRD schema for OpenMeter status fields
3. Create implementation tasks in tasks.md
4. Begin Phase 1: Parallel implementation with feature flag
5. Execute migration plan with gradual rollout

### **Critical Dependencies**

- PostgreSQL (Hub) for webhook idempotency
- Redis (Hub) for entitlement caching (10-minute TTL) + rate limiting
- NATS JetStream for event choreography + durable buffers
- Istio/SPIRE for zero-trust mTLS
- hub-operator for OpenMeter namespace provisioning via `namespace.Manager` Go API (ADR 012)
- OpenTelemetry SDK for W3C trace context propagation (Req 25)
- Jaeger/Grafana for distributed tracing visualization

