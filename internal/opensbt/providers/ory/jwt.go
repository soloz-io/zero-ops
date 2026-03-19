package ory

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwks struct {
	Keys []jwk `json:"keys"`
}

type cachedKey struct {
	key       interface{}
	expiresAt time.Time
}

type jwtValidator struct {
	jwksURL         string
	audience        string
	cache           sync.Map
	mu              sync.Mutex
	lastRefresh     time.Time
	ttl             time.Duration
	fetchTimeout    time.Duration
	refreshInterval time.Duration
	client          *http.Client
}

func newJWTValidator(cfg Config) *jwtValidator {
	return &jwtValidator{
		jwksURL:         cfg.HydraPublicURL + "/.well-known/jwks.json",
		audience:        cfg.JWTAudience,
		ttl:             cfg.JWKSCacheTTL,
		fetchTimeout:    cfg.JWKSFetchTimeout,
		refreshInterval: cfg.JWKSRefreshInterval,
		client:          &http.Client{Timeout: cfg.JWKSFetchTimeout},
	}
}

func (v *jwtValidator) validate(tokenString string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("missing kid in token header")
		}
		return v.getKey(kid)
	}, jwt.WithValidMethods([]string{"RS256", "ES256"}))
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims format")
	}
	if v.audience != "" {
		aud, _ := token.Claims.GetAudience()
		found := false
		for _, a := range aud {
			if a == v.audience {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("invalid audience")
		}
	}
	return claims, nil
}

func (v *jwtValidator) getKey(kid string) (interface{}, error) {
	if c, ok := v.cache.Load(kid); ok {
		if ck := c.(cachedKey); time.Now().Before(ck.expiresAt) {
			return ck.key, nil
		}
		v.cache.Delete(kid)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if c, ok := v.cache.Load(kid); ok {
		if ck := c.(cachedKey); time.Now().Before(ck.expiresAt) {
			return ck.key, nil
		}
	}
	if time.Since(v.lastRefresh) < v.refreshInterval {
		return nil, fmt.Errorf("key %s not found (refresh rate limited)", kid)
	}
	keys, err := v.fetchJWKS()
	if err != nil {
		return nil, err
	}
	v.lastRefresh = time.Now()
	exp := time.Now().Add(v.ttl)
	for _, k := range keys.Keys {
		pub, err := jwkToRSA(k)
		if err != nil {
			continue
		}
		v.cache.Store(k.Kid, cachedKey{key: pub, expiresAt: exp})
	}
	if c, ok := v.cache.Load(kid); ok {
		return c.(cachedKey).key, nil
	}
	return nil, fmt.Errorf("key not found in JWKS: %s", kid)
}

func (v *jwtValidator) fetchJWKS() (*jwks, error) {
	ctx, cancel := context.WithTimeout(context.Background(), v.fetchTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS fetch failed: %d", resp.StatusCode)
	}
	var out jwks
	return &out, json.NewDecoder(resp.Body).Decode(&out)
}

func jwkToRSA(k jwk) (*rsa.PublicKey, error) {
	if k.Kty != "RSA" {
		return nil, fmt.Errorf("unsupported key type: %s", k.Kty)
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	var e uint64
	for _, b := range eBytes {
		e = e<<8 | uint64(b)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e)}, nil
}
