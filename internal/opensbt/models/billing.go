package models

import "time"

// BillingCustomer represents a billing customer mapped to a tenant
type BillingCustomer struct {
	ID        string                 `json:"id"`
	TenantID  string                 `json:"tenant_id"`
	Email     string                 `json:"email"`
	Name      string                 `json:"name"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// CustomerUpdates represents fields that can be updated on a billing customer
type CustomerUpdates struct {
	Email    *string                 `json:"email,omitempty"`
	Name     *string                 `json:"name,omitempty"`
	Metadata *map[string]interface{} `json:"metadata,omitempty"`
}

// Subscription represents a billing subscription
type Subscription struct {
	ID         string                 `json:"id"`
	CustomerID string                 `json:"customer_id"`
	PlanID     string                 `json:"plan_id"`
	Status     string                 `json:"status"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
}

// SubscriptionUpdates represents fields that can be updated on a subscription
type SubscriptionUpdates struct {
	PlanID   *string                 `json:"plan_id,omitempty"`
	Status   *string                 `json:"status,omitempty"`
	Metadata *map[string]interface{} `json:"metadata,omitempty"`
}

// UsageRecord represents a usage record for billing
type UsageRecord struct {
	ID         string                 `json:"id"`
	CustomerID string                 `json:"customer_id"`
	MeterName  string                 `json:"meter_name"`
	Value      float64                `json:"value"`
	Timestamp  time.Time              `json:"timestamp"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// UsageReport represents a usage report for a billing period
type UsageReport struct {
	CustomerID  string                 `json:"customer_id"`
	Period      TimePeriod             `json:"period"`
	TotalUsage  float64                `json:"total_usage"`
	Records     []UsageRecord          `json:"records"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// Invoice represents a billing invoice
type Invoice struct {
	ID         string                 `json:"id"`
	CustomerID string                 `json:"customer_id"`
	Period     TimePeriod             `json:"period"`
	Amount     float64                `json:"amount"`
	Currency   string                 `json:"currency"`
	Status     string                 `json:"status"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
}

// TimePeriod represents a time period for billing/metering queries
type TimePeriod struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Meter represents a usage meter definition
type Meter struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Unit        string                 `json:"unit"`
	Type        string                 `json:"type"`
	Config      map[string]interface{} `json:"config,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// MeterUpdates represents fields that can be updated on a meter
type MeterUpdates struct {
	Name        *string                 `json:"name,omitempty"`
	Description *string                 `json:"description,omitempty"`
	Unit        *string                 `json:"unit,omitempty"`
	Config      *map[string]interface{} `json:"config,omitempty"`
}

// MeterFilters represents filters for listing meters
type MeterFilters struct {
	Type   *string `json:"type,omitempty"`
	Limit  int     `json:"limit,omitempty"`
	Offset int     `json:"offset,omitempty"`
}

// UsageEvent represents a usage event for metering
type UsageEvent struct {
	ID         string                 `json:"id"`
	TenantID   string                 `json:"tenant_id"`
	MeterID    string                 `json:"meter_id"`
	Value      float64                `json:"value"`
	Timestamp  time.Time              `json:"timestamp"`
	Properties map[string]interface{} `json:"properties,omitempty"`
}

// UsageData represents aggregated usage data
type UsageData struct {
	MeterID    string                 `json:"meter_id"`
	TenantID   string                 `json:"tenant_id"`
	Period     TimePeriod             `json:"period"`
	TotalUsage float64                `json:"total_usage"`
	EventCount int64                  `json:"event_count"`
	Breakdown  map[string]interface{} `json:"breakdown,omitempty"`
}

// TenantUsageData represents usage data aggregated per tenant
type TenantUsageData struct {
	TenantID string               `json:"tenant_id"`
	Period   TimePeriod           `json:"period"`
	Meters   map[string]UsageData `json:"meters"`
}

// AggregationRequest represents a request for usage aggregation
type AggregationRequest struct {
	TenantID string     `json:"tenant_id,omitempty"`
	MeterID  string     `json:"meter_id,omitempty"`
	Period   TimePeriod `json:"period"`
	GroupBy  []string   `json:"group_by,omitempty"`
}

// AggregationResult represents the result of a usage aggregation
type AggregationResult struct {
	Period     TimePeriod             `json:"period"`
	TotalUsage float64                `json:"total_usage"`
	Groups     map[string]interface{} `json:"groups,omitempty"`
}
