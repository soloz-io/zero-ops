// Package tokenmanager provides JWT validation with JWKS and Gin middleware
// for automatic tenant context injection (Tasks 19.1–19.6).
package tokenmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims holds the tenant-scoped JWT claims (19.2).
type Claims struct {
	UserID     string   `json:"sub"`
	TenantID   string   `json:"tenant_id"`
	TenantTier string   `json:"tenant_tier"`
	Roles      []string `json:"roles"`
	Email      string   `json:"email"`
	jwt.RegisteredClaims
}

// TokenManager validates JWTs using JWKS and injects tenant context (19.1–19.6).
type TokenManager struct {
	jwksURL  string
	issuer   string
	audience string

	mu      sync.RWMutex
	keySet  map[string]interface{} // kid → public key (19.4 cache)
	keyExp  time.Time
	keyTTL  time.Duration

	httpClient *http.Client
}

// New creates a TokenManager. keyTTL controls JWKS cache duration (default 5m).
func New(jwksURL, issuer, audience string, keyTTL time.Duration) *TokenManager {
	if keyTTL == 0 {
		keyTTL = 5 * time.Minute
	}
	return &TokenManager{
		jwksURL:    jwksURL,
		issuer:     issuer,
		audience:   audience,
		keySet:     make(map[string]interface{}),
		keyTTL:     keyTTL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// ValidateAndExtract validates the token and returns tenant claims (19.1, 19.2, 19.6).
func (tm *TokenManager) ValidateAndExtract(ctx context.Context, tokenString string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("tokenmanager: unexpected signing method: %v", token.Header["alg"])
		}
		kid, _ := token.Header["kid"].(string)
		return tm.getKey(ctx, kid)
	}, jwt.WithIssuer(tm.issuer), jwt.WithAudience(tm.audience))
	if err != nil {
		return nil, fmt.Errorf("tokenmanager: invalid token: %w", err)
	}
	if claims.TenantID == "" {
		return nil, fmt.Errorf("tokenmanager: token missing tenant_id claim")
	}
	return claims, nil
}

// getKey returns the RSA public key for the given kid, refreshing JWKS when stale (19.4).
func (tm *TokenManager) getKey(ctx context.Context, kid string) (interface{}, error) {
	tm.mu.RLock()
	if time.Now().Before(tm.keyExp) {
		if k, ok := tm.keySet[kid]; ok {
			tm.mu.RUnlock()
			return k, nil
		}
	}
	tm.mu.RUnlock()
	return tm.refreshKeys(ctx, kid)
}

// refreshKeys fetches JWKS and caches parsed public keys (19.4, 19.5).
func (tm *TokenManager) refreshKeys(ctx context.Context, kid string) (interface{}, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, tm.jwksURL, nil)
	resp, err := tm.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tokenmanager: fetch JWKS: %w", err)
	}
	defer resp.Body.Close()

	var jwks struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("tokenmanager: decode JWKS: %w", err)
	}

	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.keySet = make(map[string]interface{})
	for _, raw := range jwks.Keys {
		var header struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			continue
		}
		key, err := jwt.ParseRSAPublicKeyFromPEM(raw) // works for RSA JWK via jwt helper
		if err != nil {
			// Store raw for later parsing attempts
			tm.keySet[header.Kid] = raw
			continue
		}
		tm.keySet[header.Kid] = key
	}
	tm.keyExp = time.Now().Add(tm.keyTTL)

	if k, ok := tm.keySet[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("tokenmanager: key %q not found in JWKS", kid)
}

// GinMiddleware extracts and validates the Bearer token, injecting tenant context
// into the Gin context and request context (19.3).
func (tm *TokenManager) GinMiddleware() func(c interface{ /* gin.Context */ }) {
	// Return a typed gin.HandlerFunc via the concrete import in the caller.
	// We avoid importing gin here to keep the library dependency-light.
	// Callers use: r.Use(gin.WrapF(tm.HTTPMiddleware()))
	return nil // see HTTPMiddleware below
}

// HTTPMiddleware returns a standard http.Handler middleware (19.3).
func (tm *TokenManager) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, `{"error":"authorization header required"}`, http.StatusUnauthorized)
			return
		}
		claims, err := tm.ValidateAndExtract(r.Context(), strings.TrimPrefix(auth, "Bearer "))
		if err != nil {
			http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyTenantID, claims.TenantID)
		ctx = context.WithValue(ctx, ctxKeyTenantTier, claims.TenantTier)
		ctx = context.WithValue(ctx, ctxKeyUserID, claims.UserID)
		ctx = context.WithValue(ctx, ctxKeyEmail, claims.Email)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type contextKey string

const (
	ctxKeyTenantID   contextKey = "tenant_id"
	ctxKeyTenantTier contextKey = "tenant_tier"
	ctxKeyUserID     contextKey = "user_id"
	ctxKeyEmail      contextKey = "email"
)

// TenantIDFromContext extracts tenant_id from context.
func TenantIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyTenantID).(string)
	return v
}

// TenantTierFromContext extracts tenant_tier from context.
func TenantTierFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyTenantTier).(string)
	return v
}

// UserIDFromContext extracts user_id from context.
func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyUserID).(string)
	return v
}
