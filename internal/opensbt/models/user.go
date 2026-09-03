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
	Groups    []string               `json:"groups,omitempty"`
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
	Groups   *[]string               `json:"groups,omitempty"`
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

// CreateAdminUserProps is the bootstrap request for the platform's first
// administrator, mirroring sbt-aws's CreateAdminUserProps
// (src/control-plane/auth/auth-interface.ts).
//
// It carries no TenantID, and that absence is the point: a platform
// administrator must NOT belong to a tenant. Scope comes from Groups instead,
// which is the exemption the token validator honours (ADR-058) — a tenant would
// make the identity wrong, and no scope at all would make it unable to
// authenticate.
type CreateAdminUserProps struct {
	Email string
	Name  string
	// Role is a display/authorisation label. It is NOT the security scope;
	// Groups is. Kept because sbt-aws carries it and the bootstrap config
	// supplies it.
	Role string
	// Groups default to PlatformAdminGroups when empty. Naming them here lets a
	// caller bootstrap a differently-privileged operator without a code change.
	Groups []string
}
