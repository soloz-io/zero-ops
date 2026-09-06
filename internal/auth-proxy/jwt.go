package authproxy

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type JWKS struct {
	Keys []JWK `json:"keys"`
}

type cachedKey struct {
	key       interface{}
	expiresAt time.Time
}

type JWTValidator struct {
	jwksURL          string
	expectedAudience string
	cache            *sync.Map
	lastRefresh      time.Time
	ttl              time.Duration
	fetchTimeout     time.Duration
	refreshInterval  time.Duration
	client           *http.Client
	// jwksHost overrides the Host header on the JWKS fetch. Empty means "use the
	// URL's host", which is the conventional behaviour.
	jwksHost string
	mu       sync.RWMutex
}

// NewJWTValidator builds a validator that fetches signing keys from jwksURL.
//
// jwksHost, when set, is sent as the Host header on that fetch. It exists
// because Zitadel resolves its instance from the request origin: keys are
// fetched over the in-cluster Service address, which matches no instance, so the
// header must carry the public issuer or the fetch answers 404 in plain text.
// Empty leaves Go's default (the URL's own host), which is correct for an issuer
// that does not multiplex on Host.
func NewJWTValidatorWithHost(jwksURL, jwksHost, expectedAudience string, ttl, fetchTimeout time.Duration) *JWTValidator {
	v := NewJWTValidator(jwksURL, expectedAudience, ttl, fetchTimeout)
	v.jwksHost = jwksHost
	return v
}

func NewJWTValidator(jwksURL, expectedAudience string, ttl, fetchTimeout time.Duration) *JWTValidator {
	return &JWTValidator{
		jwksURL:          jwksURL,
		expectedAudience: expectedAudience,
		cache:            &sync.Map{},
		ttl:              ttl,
		fetchTimeout:     fetchTimeout,
		refreshInterval:  10 * time.Second,
		client: &http.Client{
			Timeout: fetchTimeout,
		},
	}
}

func (v *JWTValidator) Validate(tokenString string) (map[string]interface{}, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("missing kid in token header")
		}

		key, err := v.getKey(kid)
		if err != nil {
			return nil, fmt.Errorf("failed to get key: %w", err)
		}

		return key, nil
	}, jwt.WithValidMethods([]string{"RS256", "ES256"}))

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	// Validate audience
	aud, err := token.Claims.GetAudience()
	if err != nil {
		return nil, fmt.Errorf("failed to get audience: %w", err)
	}

	audienceValid := false
	for _, a := range aud {
		if a == v.expectedAudience {
			audienceValid = true
			break
		}
	}
	if !audienceValid {
		return nil, fmt.Errorf("invalid audience: %v, expected: %s", aud, v.expectedAudience)
	}

	// Extract claims
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims format")
	}

	return claims, nil
}

func (v *JWTValidator) getKey(kid string) (interface{}, error) {
	// Check cache first
	if cached, ok := v.cache.Load(kid); ok {
		if cachedKey, ok := cached.(cachedKey); ok && time.Now().Before(cachedKey.expiresAt) {
			return cachedKey.key, nil
		}
		// Expired, remove from cache
		v.cache.Delete(kid)
	}

	// Cache miss or expired, fetch JWKS
	v.mu.Lock()
	defer v.mu.Unlock()

	// Double-check after acquiring lock
	if cached, ok := v.cache.Load(kid); ok {
		if cachedKey, ok := cached.(cachedKey); ok && time.Now().Before(cachedKey.expiresAt) {
			return cachedKey.key, nil
		}
	}

	// Rate limit: enforce minimum 10s between refreshes
	if time.Since(v.lastRefresh) < v.refreshInterval {
		return nil, fmt.Errorf("key %s not found (refresh rate limited)", kid)
	}

	// Fetch fresh JWKS
	jwks, err := v.fetchJWKS()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch JWKS: %w", err)
	}
	v.lastRefresh = time.Now()

	// Cache all keys from JWKS with TTL
	now := time.Now()
	for _, jwk := range jwks.Keys {
		key, err := jwkToPublicKey(jwk)
		if err != nil {
			log.Printf("Failed to convert JWK to public key: %v", err)
			continue
		}
		v.cache.Store(jwk.Kid, cachedKey{
			key:       key,
			expiresAt: now.Add(v.ttl),
		})
	}

	// Try to get the key again
	if cached, ok := v.cache.Load(kid); ok {
		if cachedKey, ok := cached.(cachedKey); ok {
			return cachedKey.key, nil
		}
	}

	return nil, fmt.Errorf("key not found in JWKS: %s", kid)
}

func (v *JWTValidator) fetchJWKS() (*JWKS, error) {
	ctx, cancel := context.WithTimeout(context.Background(), v.fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", v.jwksURL, nil)
	if err != nil {
		return nil, err
	}
	// See NewJWTValidatorWithHost: the issuer may key its instance off this.
	if v.jwksHost != "" {
		req.Host = v.jwksHost
	}

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("failed to fetch JWKS: %d", resp.StatusCode)
	}

	var jwks JWKS
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, err
	}

	return &jwks, nil
}

func jwkToPublicKey(jwk JWK) (interface{}, error) {
	if jwk.Kty != "RSA" {
		return nil, fmt.Errorf("unsupported key type: %s", jwk.Kty)
	}

	// Decode base64url-encoded modulus and exponent
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("failed to decode modulus: %w", err)
	}

	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("failed to decode exponent: %w", err)
	}

	// Convert exponent bytes to integer
	var eInt uint64
	for _, b := range eBytes {
		eInt = eInt<<8 | uint64(b)
	}

	// Create RSA public key
	pubKey := &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(eInt),
	}

	return pubKey, nil
}
