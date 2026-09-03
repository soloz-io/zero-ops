package models

import "time"

// TenantStatus represents the Event-Driven State Machine states
type TenantStatus string

const (
	TenantStatusCreating      TenantStatus = "CREATING"
	TenantStatusGitCommitted  TenantStatus = "GIT_COMMITTED"
	TenantStatusSyncing       TenantStatus = "SYNCING"
	TenantStatusReady         TenantStatus = "READY"
	TenantStatusFailed        TenantStatus = "FAILED"
	TenantStatusSuspended     TenantStatus = "SUSPENDED"
	TenantStatusDeprovisioning TenantStatus = "DEPROVISIONING"
	TenantStatusDeleted       TenantStatus = "DELETED"
)

// Tenant represents a SaaS tenant with Event-Driven State Machine fields
type Tenant struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Tier string `json:"tier"` // basic, standard, premium, enterprise

	// Business Truth State Machine (PostgreSQL = Source of Truth)
	Status TenantStatus `json:"status"`

	// Infrastructure Observability (Pushed from ArgoCD Webhooks)
	ArgoSyncStatus   string `json:"argo_sync_status,omitempty"`   // Synced, OutOfSync
	ArgoHealthStatus string `json:"argo_health_status,omitempty"` // Healthy, Progressing, Degraded

	OwnerEmail     string                 `json:"owner_email"`
	Config         map[string]interface{} `json:"config,omitempty"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
	LastObservedAt time.Time              `json:"last_observed_at"` // Updated every ArgoCD webhook
}

// TenantUpdates represents fields that can be updated on a tenant
type TenantUpdates struct {
	Name             *string                 `json:"name,omitempty"`
	Tier             *string                 `json:"tier,omitempty"`
	Status           *TenantStatus           `json:"status,omitempty"`
	ArgoSyncStatus   *string                 `json:"argo_sync_status,omitempty"`
	ArgoHealthStatus *string                 `json:"argo_health_status,omitempty"`
	Config           *map[string]interface{} `json:"config,omitempty"`
}

// TenantFilters represents filters for listing tenants
type TenantFilters struct {
	Status   *TenantStatus `json:"status,omitempty"`
	Tier     *string       `json:"tier,omitempty"`
	Limit    int           `json:"limit,omitempty"`
	Offset   int           `json:"offset,omitempty"`
}

// TenantRegistration represents a tenant onboarding request
type TenantRegistration struct {
	ID        string                 `json:"id"`
	TenantID  string                 `json:"tenant_id,omitempty"`
	Status    string                 `json:"status"`
	Name      string                 `json:"name"`
	Email     string                 `json:"email"`
	Tier      string                 `json:"tier"`
	Config    map[string]interface{} `json:"config,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// RegistrationUpdates represents fields that can be updated on a registration
type RegistrationUpdates struct {
	TenantID *string                 `json:"tenant_id,omitempty"`
	Status   *string                 `json:"status,omitempty"`
	Config   *map[string]interface{} `json:"config,omitempty"`
}

// RegistrationFilters represents filters for listing registrations
type RegistrationFilters struct {
	Status *string `json:"status,omitempty"`
	Tier   *string `json:"tier,omitempty"`
	Limit  int     `json:"limit,omitempty"`
	Offset int     `json:"offset,omitempty"`
}
