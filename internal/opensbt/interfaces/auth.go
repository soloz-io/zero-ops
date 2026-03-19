package interfaces

import (
	"context"

	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// IAuth provides authentication and authorization capabilities
type IAuth interface {
	// User Management
	CreateUser(ctx context.Context, user models.User) error
	GetUser(ctx context.Context, userID string) (*models.User, error)
	UpdateUser(ctx context.Context, userID string, updates models.UserUpdates) error
	DeleteUser(ctx context.Context, userID string) error
	DisableUser(ctx context.Context, userID string) error
	EnableUser(ctx context.Context, userID string) error
	ListUsers(ctx context.Context, filters models.UserFilters) ([]models.User, error)

	// Authentication
	AuthenticateUser(ctx context.Context, credentials models.Credentials) (*models.Token, error)
	ValidateToken(ctx context.Context, token string) (*models.Claims, error)
	RefreshToken(ctx context.Context, refreshToken string) (*models.Token, error)

	// Admin Operations
	CreateAdminUser(ctx context.Context, props models.CreateAdminUserProps) error

	// Token Configuration
	GetJWTIssuer() string
	GetJWTAudience() []string
	GetTokenEndpoint() string
	GetWellKnownEndpoint() string
}
