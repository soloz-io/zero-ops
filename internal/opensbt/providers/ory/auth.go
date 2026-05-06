package ory

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// Auth implements interfaces.IAuth using the Ory stack:
//   - Ory Kratos  — identity management (users)
//   - Ory Keto    — relationship-based authorization
type Auth struct {
	cfg    Config
	kratos *kratosClient
	keto   *ketoClient
	jwt    *jwtValidator
}

// NewAuth creates a new Ory Auth provider.
func NewAuth(cfg Config) *Auth {
	cfg.defaults()
	hc := &http.Client{Timeout: 10 * time.Second}
	return &Auth{
		cfg:    cfg,
		kratos: &kratosClient{adminURL: cfg.KratosAdminURL, client: hc},
		keto:   &ketoClient{readURL: cfg.KetoReadURL, writeURL: cfg.KetoWriteURL, client: hc},
		jwt:    newJWTValidator(cfg),
	}
}

// NewAuthProvider creates a simplified Ory Auth provider for API server
func NewAuthProvider(kratosAdminURL string) (*Auth, error) {
	if kratosAdminURL == "" {
		return nil, fmt.Errorf("ory: kratosAdminURL is required")
	}

	cfg := Config{
		KratosAdminURL: kratosAdminURL,
		HydraPublicURL: "http://hydra-public.hub-platform-identity.svc.cluster.local:4444",
		HydraAdminURL:  "http://hydra-admin.hub-platform-identity.svc.cluster.local:4445",
		KetoReadURL:    "http://keto-read.hub-platform-identity.svc.cluster.local:4466",
		KetoWriteURL:   "http://keto-write.hub-platform-identity.svc.cluster.local:4467",
		JWTAudience:    "kube-sbt-api",
	}

	return NewAuth(cfg), nil
}

// ─── User Management ─────────────────────────────────────────────────────────

func (a *Auth) CreateUser(ctx context.Context, user models.User) (*models.User, error) {
	if err := a.kratos.createIdentity(ctx, user); err != nil {
		return nil, err
	}
	// Create tenant-user relationship in Keto
	_ = a.keto.createRelationship(ctx, ketoRelationship{
		Namespace: "tenants",
		Object:    user.TenantID,
		Relation:  "member",
		SubjectID: user.ID,
	})
	return a.kratos.getIdentity(ctx, user.ID)
}

func (a *Auth) GetUser(ctx context.Context, userID string) (*models.User, error) {
	return a.kratos.getIdentity(ctx, userID)
}

func (a *Auth) UpdateUser(ctx context.Context, userID string, updates models.UserUpdates) (*models.User, error) {
	if err := a.kratos.updateIdentity(ctx, userID, updates); err != nil {
		return nil, err
	}
	return a.kratos.getIdentity(ctx, userID)
}

func (a *Auth) DeleteUser(ctx context.Context, userID string) error {
	// Fetch user first to get tenantID for Keto cleanup
	user, err := a.kratos.getIdentity(ctx, userID)
	if err != nil {
		return err
	}
	// Remove Keto relationship
	_ = a.keto.deleteRelationship(ctx, ketoRelationship{
		Namespace: "tenants",
		Object:    user.TenantID,
		Relation:  "member",
		SubjectID: userID,
	})
	return a.kratos.deleteIdentity(ctx, userID)
}

func (a *Auth) ListUsers(ctx context.Context, tenantID, page, pageSize string) ([]models.User, error) {
	// TODO: Implement pagination properly
	filters := models.UserFilters{
		TenantID: &tenantID,
	}
	return a.kratos.listIdentities(ctx, filters)
}

// ─── Session Management ──────────────────────────────────────────────────────

func (a *Auth) ValidateSession(ctx context.Context, token string) (*models.Session, error) {
	// Validate JWT and extract claims
	raw, err := a.jwt.validate(token)
	if err != nil {
		return nil, err
	}

	userID, _ := raw["sub"].(string)
	tenantID, _ := raw["tenant_id"].(string)
	
	var roles []string
	if rolesRaw, ok := raw["roles"].([]interface{}); ok {
		for _, r := range rolesRaw {
			if s, ok := r.(string); ok {
				roles = append(roles, s)
			}
		}
	}

	var expiresAt time.Time
	if exp, ok := raw["exp"].(float64); ok {
		expiresAt = time.Unix(int64(exp), 0)
	}

	var issuedAt time.Time
	if iat, ok := raw["iat"].(float64); ok {
		issuedAt = time.Unix(int64(iat), 0)
	}

	return &models.Session{
		ID: userID,
		Identity: models.Identity{
			ID: userID,
			Traits: map[string]interface{}{
				"tenant_id": tenantID,
				"roles":     roles,
			},
		},
		ExpiresAt: expiresAt,
		IssuedAt:  issuedAt,
	}, nil
}

// Compile-time assertion
var _ interfaces.IAuth = (*Auth)(nil)
