package models

import (
	"fmt"
	"time"
)

// User represents a user in the system
type User struct {
	ID        string                 `json:"id"`
	Email     string                 `json:"email"`
	Name      string                 `json:"name,omitempty"`
	TenantID  string                 `json:"tenant_id,omitempty"`
	Roles     []string               `json:"roles,omitempty"`
	Password  string                 `json:"password,omitempty"` // Only used during creation
	Traits    map[string]interface{} `json:"traits"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Active    bool                   `json:"active"`
	CreatedAt time.Time              `json:"createdAt"`
	UpdatedAt time.Time              `json:"updatedAt"`
}

// UserUpdates for modifying users
type UserUpdates struct {
	Email    *string                 `json:"email,omitempty"`
	Name     *string                 `json:"name,omitempty"`
	Roles    *[]string               `json:"roles,omitempty"`
	Traits   map[string]interface{}  `json:"traits,omitempty"`
	Metadata *map[string]interface{} `json:"metadata,omitempty"`
}

// UserFilters for listing users
type UserFilters struct {
	TenantID *string `json:"tenant_id,omitempty"`
	Active   *bool   `json:"active,omitempty"`
	Limit    int     `json:"limit,omitempty"`
}

// Validate validates User fields
func (u *User) Validate() error {
	if u.Email == "" {
		return fmt.Errorf("user email is required")
	}
	return nil
}
