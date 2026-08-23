package authproxy

import (
	"log"
	"net/http"
	"strings"
	"time"
)

func (h *Handler) ValidateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract JWT from Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		http.Error(w, "Missing Authorization header", http.StatusUnauthorized)
		return
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		http.Error(w, "Invalid Authorization header format", http.StatusUnauthorized)
		return
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")

	// Validate JWT
	validator := NewJWTValidator(
		h.jwksURL,
		h.expectedAudience,
		time.Hour,     // 1-hour TTL
		5*time.Second, // fetch timeout
	)

	claims, err := validator.Validate(token)
	if err != nil {
		log.Printf("JWT validation failed: %v", err)
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	// Check scope for 403
	scope, _ := claims["scope"].(string)
	if !hasRequiredScope(scope, "tenant:read") {
		http.Error(w, "Insufficient scope", http.StatusForbidden)
		return
	}

	// Extract claims and set headers
	sub, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)
	role, _ := claims["role"].(string)
	tenantID, _ := claims["tenant_id"].(string)

	// Set headers (omit missing claims)
	if sub != "" {
		w.Header().Set("X-Auth-User-Id", sub)
	}
	if email != "" {
		w.Header().Set("X-Auth-Email", email)
	}
	if role != "" {
		w.Header().Set("X-Auth-Role", role)
	}
	if tenantID != "" {
		w.Header().Set("X-Auth-Tenant-Id", tenantID)
	}

	w.WriteHeader(http.StatusOK)
}

func hasRequiredScope(tokenScope, requiredScope string) bool {
	if tokenScope == "" {
		return false
	}
	scopes := strings.Split(tokenScope, " ")
	for _, s := range scopes {
		if s == requiredScope {
			return true
		}
	}
	return false
}
