package models

import "time"

// User represents a user within a tenant context
type User struct {
	ID        string                 `json:"id"`
	Email     string                 `json:"email"`
	Name      string                 `json:"name"`
	TenantID  string                 `json:"tenant_id"`
	Roles     []string               `json:"roles"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Active    bool                   `json:"active"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// UserUpdates represents fields that can be updated on a user
type UserUpdates struct {
	Name     *string                 `json:"name,omitempty"`
	Roles    *[]string               `json:"roles,omitempty"`
	Metadata *map[string]interface{} `json:"metadata,omitempty"`
	Active   *bool                   `json:"active,omitempty"`
}

// UserFilters represents filters for listing users
type UserFilters struct {
	TenantID *string `json:"tenant_id,omitempty"`
	Active   *bool   `json:"active,omitempty"`
	Limit    int     `json:"limit,omitempty"`
	Offset   int     `json:"offset,omitempty"`
}

// Credentials represents authentication credentials
type Credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Token represents an authentication token
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int       `json:"expires_in"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// Claims represents JWT token claims
type Claims struct {
	UserID      string   `json:"sub"`
	TenantID    string   `json:"tenant_id"`
	TenantTier  string   `json:"tenant_tier"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	ExpiresAt   int64    `json:"exp"`
	IssuedAt    int64    `json:"iat"`
}

// CreateAdminUserProps represents properties for creating an admin user
type CreateAdminUserProps struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}
