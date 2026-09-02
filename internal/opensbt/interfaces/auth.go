package interfaces

import (
	"context"

	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// IAuth provides authentication and authorization capabilities
type IAuth interface {
	// User Management
	CreateUser(ctx context.Context, user models.User) (*models.User, error)
	GetUser(ctx context.Context, userID string) (*models.User, error)
	UpdateUser(ctx context.Context, userID string, updates models.UserUpdates) (*models.User, error)
	DeleteUser(ctx context.Context, userID string) error
	ListUsers(ctx context.Context, tenantID, page, pageSize string) ([]models.User, error)
	SetUserGroups(ctx context.Context, email string, groups []string) error

	// Session Management
	ValidateSession(ctx context.Context, token string) (*models.Session, error)
}
