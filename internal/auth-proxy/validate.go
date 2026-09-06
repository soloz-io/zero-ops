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
	validator := NewJWTValidatorWithHost(
		h.jwksURL,
		h.IssuerHost(),
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

	// The tenant is read through the claim contract rather than from a literal
	// "tenant_id". Zitadel expresses tenancy as the user's resource owner, so a
	// direct read returned "" for every real token and the header was silently
	// omitted — which downstream reads as an untenanted request, not as an error.
	tenantID := tenantFromClaims(claims)
	if tenantID == "" {
		// An untenanted token is not something the provider can mint (ADR-060):
		// an Organization owns every user. Reaching here means the token was not
		// issued by the configured issuer, or was requested without the scope
		// that mints the claim. Either way it cannot be authorised against a
		// tenant, and admitting it would mean admitting it to all of them.
		log.Printf("JWT carries no tenant claim; rejecting")
		http.Error(w, "Invalid token", http.StatusUnauthorized)
		return
	}

	sub, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)

	// Roles are those granted WITHIN this tenant, never the whole claim: the same
	// role can be granted in several tenants and taking all of them would leak a
	// grant made elsewhere into this request.
	roles := rolesGrantedInTenant(claims, tenantID)

	// Set headers (omit missing claims)
	if sub != "" {
		w.Header().Set("X-Auth-User-Id", sub)
	}
	if email != "" {
		w.Header().Set("X-Auth-Email", email)
	}
	if len(roles) > 0 {
		w.Header().Set("X-Auth-Roles", strings.Join(roles, ","))
	}
	w.Header().Set("X-Auth-Tenant-Id", tenantID)

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
