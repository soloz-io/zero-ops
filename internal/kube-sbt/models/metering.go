package models

import (
	"fmt"
	"strings"
	"time"
)

// MeterSpec defines a usage metric (Req 14)
// ARCHITECTURAL NOTE: Used by hub-operator CRD, not kube-sbt REST API
// CORRECTED (Gap 3.1): Tenant aggregation requires GroupBy attributes, not compound subject IDs
// Reference: archived/billing-metering/openmeter/openmeter/streaming/clickhouse/meter_query.go:L156-L180
// OpenMeter FilterSubject does NOT support wildcard/prefix matching
// Solution: AgentGateway emits tenant_id as OTLP attribute, meter uses GroupBy["tenant_id"]
type MeterSpec struct {
	Slug          string            `json:"slug"`
	Description   string            `json:"description"`
	Aggregation   string            `json:"aggregation"` // SUM, COUNT, MAX, etc.
	EventType     string            `json:"eventType"`
	ValueProperty string            `json:"valueProperty"`
	GroupBy       map[string]string `json:"groupBy,omitempty"` // MUST include "tenant_id": "$.tenant_id" for tenant aggregation
}

// Meter represents a configured meter in OpenMeter
type Meter struct {
	ID          string    `json:"id"`
	Namespace   string    `json:"namespace"`
	Slug        string    `json:"slug"`
	Description string    `json:"description"`
	Aggregation string    `json:"aggregation"`
	EventType   string    `json:"eventType"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// FeatureSpec defines a billable capability (Req 14)
// ARCHITECTURAL NOTE: Used by hub-operator CRD, not kube-sbt REST API
type FeatureSpec struct {
	Key        string   `json:"key"`
	Name       string   `json:"name"`
	MeterSlugs []string `json:"meterSlugs"`
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
	Key        string     `json:"key"`
	Name       string     `json:"name"`
	StartAfter string     `json:"startAfter"` // ISO 8601 duration
	RateCards  []RateCard `json:"rateCards"`
}

// RateCard defines pricing for a feature
type RateCard struct {
	FeatureKey      string  `json:"featureKey"`
	EntitlementType string  `json:"entitlementType"` // metered, static, boolean
	Price           *Price  `json:"price,omitempty"`
}

// Price defines pricing model
type Price struct {
	Type           string  `json:"type"` // flat, usage_based, tiered_volume, tiered_graduated
	Amount         float64 `json:"amount"`
	BillingCadence string  `json:"billingCadence"` // monthly, annual
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
	MeterSlug string     `json:"meterSlug"`
	SubjectID string     `json:"subjectId,omitempty"`
	Period    TimePeriod `json:"period"`
	GroupBy   []string   `json:"groupBy,omitempty"`
}

// TimePeriod defines a time range
type TimePeriod struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// UsageReport contains aggregated usage data
type UsageReport struct {
	MeterSlug string             `json:"meterSlug"`
	Namespace string             `json:"namespace"`
	Period    TimePeriod         `json:"period"`
	Value     float64            `json:"value"`
	GroupBy   map[string]float64 `json:"groupBy,omitempty"`
}

// TenantUsageData aggregates all meters for a tenant (Req 2)
type TenantUsageData struct {
	TenantID  string             `json:"tenantId"`
	Namespace string             `json:"namespace"`
	Period    TimePeriod         `json:"period"`
	Meters    map[string]float64 `json:"meters"` // meterSlug -> value
}

// UserUsageData aggregates usage for a specific user (Req 3)
type UserUsageData struct {
	SubjectID string             `json:"subjectId"`
	Namespace string             `json:"namespace"`
	Period    TimePeriod         `json:"period"`
	Meters    map[string]float64 `json:"meters"`
}

// EntitlementStatus represents quota check result (Req 6)
type EntitlementStatus struct {
	HasAccess  bool      `json:"hasAccess"`
	Used       int64     `json:"used"`
	Limit      int64     `json:"limit"`
	ResetTime  time.Time `json:"resetTime"`
	IsFallback bool      `json:"isFallback"` // True if OpenMeter unreachable (fail-open)
}

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

// Validate validates MeterSpec fields
func (m *MeterSpec) Validate() error {
	if m.Slug == "" {
		return fmt.Errorf("meter slug is required")
	}
	if m.Aggregation == "" {
		return fmt.Errorf("meter aggregation is required")
	}
	if m.EventType == "" {
		return fmt.Errorf("meter eventType is required")
	}
	return nil
}

// Validate validates FeatureSpec fields
func (f *FeatureSpec) Validate() error {
	if f.Key == "" {
		return fmt.Errorf("feature key is required")
	}
	if f.Name == "" {
		return fmt.Errorf("feature name is required")
	}
	if len(f.MeterSlugs) == 0 {
		return fmt.Errorf("feature must reference at least one meter")
	}
	return nil
}

// Validate validates PlanSpec fields
func (p *PlanSpec) Validate() error {
	if p.Key == "" {
		return fmt.Errorf("plan key is required")
	}
	if p.Name == "" {
		return fmt.Errorf("plan name is required")
	}
	if p.Currency == "" {
		return fmt.Errorf("plan currency is required")
	}
	if len(p.Phases) == 0 {
		return fmt.Errorf("plan must have at least one phase")
	}
	return nil
}

// Validate validates Subject fields
func (s *Subject) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("subject ID is required")
	}
	if s.Namespace == "" {
		return fmt.Errorf("subject namespace is required")
	}
	if s.Key == "" {
		return fmt.Errorf("subject key is required")
	}
	return nil
}

// Validate validates UsageFilter fields
func (u *UsageFilter) Validate() error {
	if u.MeterSlug == "" {
		return fmt.Errorf("meter slug is required")
	}
	if u.Period.Start.IsZero() {
		return fmt.Errorf("period start time is required")
	}
	if u.Period.End.IsZero() {
		return fmt.Errorf("period end time is required")
	}
	if u.Period.End.Before(u.Period.Start) {
		return fmt.Errorf("period end must be after start")
	}
	return nil
}
