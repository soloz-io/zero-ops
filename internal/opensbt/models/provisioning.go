package models

import "time"

// ProvisionRequest represents a request to provision tenant resources
type ProvisionRequest struct {
	TenantID  string                 `json:"tenant_id"`
	Tier      string                 `json:"tier"`
	Name      string                 `json:"name"`
	Email     string                 `json:"email"`
	Config    map[string]interface{} `json:"config,omitempty"`
	Resources []ResourceSpec         `json:"resources,omitempty"`
}

// ProvisionResult represents the result of a provisioning operation
type ProvisionResult struct {
	TenantID      string            `json:"tenant_id"`
	Status        string            `json:"status"`
	Resources     []Resource        `json:"resources"`
	GitCommitHash string            `json:"git_commit_hash,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
}

// DeprovisionRequest represents a request to deprovision tenant resources
type DeprovisionRequest struct {
	TenantID string                 `json:"tenant_id"`
	Config   map[string]interface{} `json:"config,omitempty"`
}

// DeprovisionResult represents the result of a deprovisioning operation
type DeprovisionResult struct {
	TenantID  string    `json:"tenant_id"`
	Status    string    `json:"status"`
	DeletedAt time.Time `json:"deleted_at"`
}

// UpdateRequest represents a request to update tenant resources
type UpdateRequest struct {
	TenantID string           `json:"tenant_id"`
	Tier     string           `json:"tier,omitempty"`   // new tier for tier_change action
	Action   string           `json:"action,omitempty"` // activate, deactivate, tier_change
	Updates  []ResourceUpdate `json:"updates,omitempty"`
}

// ResourceUpdate represents an update to a specific resource
type ResourceUpdate struct {
	Type       string                 `json:"type"`
	Name       string                 `json:"name"`
	Parameters map[string]interface{} `json:"parameters,omitempty"`
}

// UpdateResult represents the result of an update operation
type UpdateResult struct {
	TenantID  string     `json:"tenant_id"`
	Status    string     `json:"status"`
	Resources []Resource `json:"resources"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// ResourceSpec represents a specification for a resource to be provisioned
type ResourceSpec struct {
	Type       string                 `json:"type"`
	Name       string                 `json:"name"`
	Parameters map[string]interface{} `json:"parameters,omitempty"`
}

// Resource represents a provisioned resource
type Resource struct {
	ID         string                 `json:"id"`
	Type       string                 `json:"type"`
	Name       string                 `json:"name"`
	Status     string                 `json:"status"`
	Properties map[string]interface{} `json:"properties,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
}

// ProvisioningStatus represents the current provisioning status of a tenant
type ProvisioningStatus struct {
	TenantID      string            `json:"tenant_id"`
	Status        string            `json:"status"` // synced, healthy, degraded, failed, progressing, syncing, not_found
	Resources     []Resource        `json:"resources"`
	GitCommitHash string            `json:"git_commit_hash,omitempty"`
	ErrorMessage  string            `json:"error_message,omitempty"`
	LastSyncTime  time.Time         `json:"last_sync_time"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// TenantConfig represents the GitOps configuration for a tenant
type TenantConfig struct {
	TenantID     string                 `json:"tenant_id"`
	Tier         string                 `json:"tier"`
	HelmValues   map[string]interface{} `json:"helm_values"`
	GitCommit    string                 `json:"git_commit,omitempty"`
	SyncStatus   string                 `json:"sync_status,omitempty"`
	LastSyncTime time.Time              `json:"last_sync_time,omitempty"`
}

// GitRepository represents a Git repository reference
type GitRepository struct {
	URL       string `json:"url"`
	Branch    string `json:"branch"`
	Path      string `json:"path"`
	CommitSHA string `json:"commit_sha"`
}

// WarmSlotResult represents the result of claiming a warm pool slot
type WarmSlotResult struct {
	SlotID    string            `json:"slot_id"`
	TenantID  string            `json:"tenant_id"`
	Tier      string            `json:"tier"`
	Resources []Resource        `json:"resources"`
	ClaimedAt time.Time         `json:"claimed_at"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// WarmPoolStatus represents the current status of the warm pool
type WarmPoolStatus struct {
	Tier           string    `json:"tier"`
	AvailableSlots int       `json:"available_slots"`
	TotalSlots     int       `json:"total_slots"`
	TargetSlots    int       `json:"target_slots"`
	LastRefill     time.Time `json:"last_refill"`
}
