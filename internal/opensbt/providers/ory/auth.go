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
//   - Ory Hydra   — OAuth2/OIDC token issuance
//   - Ory Keto    — relationship-based authorization
type Auth struct {
	cfg    Config
	kratos *kratosClient
	hydra  *hydraClient
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
		hydra:  &hydraClient{publicURL: cfg.HydraPublicURL, adminURL: cfg.HydraAdminURL, client: hc},
		keto:   &ketoClient{readURL: cfg.KetoReadURL, writeURL: cfg.KetoWriteURL, client: hc},
		jwt:    newJWTValidator(cfg),
	}
}

// ─── User Management (6.4–6.9) ───────────────────────────────────────────────

func (a *Auth) CreateUser(ctx context.Context, user models.User) error {
	if err := a.kratos.createIdentity(ctx, user); err != nil {
		return err
	}
	// 6.16 — create tenant-user relationship in Keto
	return a.keto.createRelationship(ctx, ketoRelationship{
		Namespace: "tenants",
		Object:    user.TenantID,
		Relation:  "member",
		SubjectID: user.ID,
	})
}

func (a *Auth) GetUser(ctx context.Context, userID string) (*models.User, error) {
	return a.kratos.getIdentity(ctx, userID)
}

func (a *Auth) UpdateUser(ctx context.Context, userID string, updates models.UserUpdates) error {
	return a.kratos.updateIdentity(ctx, userID, updates)
}

func (a *Auth) DeleteUser(ctx context.Context, userID string) error {
	// Fetch user first to get tenantID for Keto cleanup
	user, err := a.kratos.getIdentity(ctx, userID)
	if err != nil {
		return err
	}
	// 6.16 — remove Keto relationship
	_ = a.keto.deleteRelationship(ctx, ketoRelationship{
		Namespace: "tenants",
		Object:    user.TenantID,
		Relation:  "member",
		SubjectID: userID,
	})
	return a.kratos.deleteIdentity(ctx, userID)
}

func (a *Auth) DisableUser(ctx context.Context, userID string) error {
	return a.kratos.setState(ctx, userID, "inactive")
}

func (a *Auth) EnableUser(ctx context.Context, userID string) error {
	return a.kratos.setState(ctx, userID, "active")
}

func (a *Auth) ListUsers(ctx context.Context, filters models.UserFilters) ([]models.User, error) {
	return a.kratos.listIdentities(ctx, filters)
}

// ─── Authentication (6.10–6.12) ──────────────────────────────────────────────

func (a *Auth) AuthenticateUser(ctx context.Context, creds models.Credentials) (*models.Token, error) {
	return a.hydra.authenticate(ctx, creds, "opensbt-internal", a.cfg.JWTAudience)
}

// ValidateToken validates a JWT and returns the claims (6.11, 6.14).
// Claims include: sub (user_id), tenant_id, tenant_tier, roles, email.
func (a *Auth) ValidateToken(ctx context.Context, tokenString string) (*models.Claims, error) {
	raw, err := a.jwt.validate(tokenString)
	if err != nil {
		return nil, err
	}
	claims := &models.Claims{}
	claims.UserID, _ = raw["sub"].(string)
	claims.TenantID, _ = raw["tenant_id"].(string)
	claims.TenantTier, _ = raw["tenant_tier"].(string)
	if exp, ok := raw["exp"].(float64); ok {
		claims.ExpiresAt = int64(exp)
	}
	if iat, ok := raw["iat"].(float64); ok {
		claims.IssuedAt = int64(iat)
	}
	if roles, ok := raw["roles"].([]interface{}); ok {
		for _, r := range roles {
			if s, ok := r.(string); ok {
				claims.Roles = append(claims.Roles, s)
			}
		}
	}
	return claims, nil
}

func (a *Auth) RefreshToken(ctx context.Context, refreshToken string) (*models.Token, error) {
	return a.hydra.refreshToken(ctx, refreshToken, "opensbt-internal")
}

// ─── Admin Operations (6.13) ─────────────────────────────────────────────────

func (a *Auth) CreateAdminUser(ctx context.Context, props models.CreateAdminUserProps) error {
	user := models.User{
		Email:    props.Email,
		Name:     props.Name,
		TenantID: "platform",
		Roles:    []string{"platform-admin"},
	}
	if err := a.kratos.createIdentity(ctx, user); err != nil {
		return fmt.Errorf("create admin identity: %w", err)
	}
	return nil
}

// ─── Token Configuration (6.15) ──────────────────────────────────────────────

func (a *Auth) GetJWTIssuer() string {
	return a.cfg.HydraPublicURL + "/"
}

func (a *Auth) GetJWTAudience() []string {
	if a.cfg.JWTAudience == "" {
		return nil
	}
	return []string{a.cfg.JWTAudience}
}

func (a *Auth) GetTokenEndpoint() string {
	return a.cfg.HydraPublicURL + "/oauth2/token"
}

func (a *Auth) GetWellKnownEndpoint() string {
	return a.cfg.HydraPublicURL + "/.well-known/openid-configuration"
}

// Compile-time assertion
var _ interfaces.IAuth = (*Auth)(nil)
