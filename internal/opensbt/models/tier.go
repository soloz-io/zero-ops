package models

import "time"

// TierConfig represents a tier configuration with quotas and features
type TierConfig struct {
	Name        string                 `json:"name"`
	DisplayName string                 `json:"display_name"`
	Description string                 `json:"description"`
	Quotas      TierQuotas             `json:"quotas"`
	Features    []string               `json:"features"`
	Pricing     map[string]interface{} `json:"pricing,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// TierQuotas represents resource quotas for a tier
type TierQuotas struct {
	Users       int                    `json:"users"`        // -1 = unlimited
	StorageGB   int                    `json:"storage_gb"`   // -1 = unlimited
	APIRequests int                    `json:"api_requests"` // -1 = unlimited
	CPU         string                 `json:"cpu"`
	Memory      string                 `json:"memory"`
	Custom      map[string]interface{} `json:"custom,omitempty"`
}

// TierUpdates represents fields that can be updated on a tier
type TierUpdates struct {
	DisplayName *string                 `json:"display_name,omitempty"`
	Description *string                 `json:"description,omitempty"`
	Quotas      *TierQuotas             `json:"quotas,omitempty"`
	Features    *[]string               `json:"features,omitempty"`
	Pricing     *map[string]interface{} `json:"pricing,omitempty"`
	Metadata    *map[string]interface{} `json:"metadata,omitempty"`
}

// ResourceUsage represents current resource usage for quota validation
type ResourceUsage struct {
	Users       int                    `json:"users"`
	StorageGB   float64                `json:"storage_gb"`
	APIRequests int                    `json:"api_requests"`
	CPU         string                 `json:"cpu,omitempty"`
	Memory      string                 `json:"memory,omitempty"`
	Custom      map[string]interface{} `json:"custom,omitempty"`
}
