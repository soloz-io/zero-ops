package zitadel

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

type jwksDoc struct {
	Keys []jwk `json:"keys"`
}

// jwtValidator verifies tokens against the issuer's published signing keys.
//
// The JWKS URL is DISCOVERED, never assembled. Appending /.well-known/jwks.json
// to an issuer is a widespread convention and not a specification — the
// authority is the jwks_uri field of the discovery document, and issuers exist
// that publish elsewhere and return 404 for the conventional path. A guess fails
// as "cannot reach the key set", which reads like an unreachable issuer rather
// than a wrong path, and that misreading costs hours.
type jwtValidator struct {
	cfg    Config
	client *http.Client

	mu       sync.Mutex
	keys     map[string]interface{}
	fetched  time.Time
	fetchErr error
}

func newJWTValidator(cfg Config) *jwtValidator {
	return &jwtValidator{cfg: cfg, client: &http.Client{Timeout: cfg.HTTPTimeout}}
}

// keyTTL bounds how long a key set — or a failure to fetch one — is reused.
//
// The failure is cached deliberately. Without it an issuer that is briefly down
// is retried on every request, turning a blip into a stampede against a service
// that is already struggling.
const keyTTL = 5 * time.Minute

func (v *jwtValidator) keyFor(ctx context.Context, kid string) (interface{}, error) {
	v.mu.Lock()
	fresh := time.Since(v.fetched) < keyTTL
	if fresh {
		if v.fetchErr != nil {
			err := v.fetchErr
			v.mu.Unlock()
			return nil, err
		}
		if k, ok := v.keys[kid]; ok {
			v.mu.Unlock()
			return k, nil
		}
	}
	v.mu.Unlock()

	keys, err := v.fetchKeys(ctx)

	v.mu.Lock()
	v.fetched = time.Now()
	v.fetchErr = err
	if err == nil {
		v.keys = keys
	}
	v.mu.Unlock()

	if err != nil {
		return nil, err
	}
	k, ok := keys[kid]
	if !ok {
		// A signing key rotated in after this fetch would land here. Naming the
		// kid makes that distinguishable from a token signed by a different
		// issuer entirely, which produces the same symptom.
		return nil, fmt.Errorf("zitadel: no signing key for kid %q", kid)
	}
	return k, nil
}

func (v *jwtValidator) fetchKeys(ctx context.Context) (map[string]interface{}, error) {
	url, err := v.discoverJWKS(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zitadel: fetch key set %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zitadel: key set %s returned %d", url, resp.StatusCode)
	}
	var doc jwksDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("zitadel: decode key set: %w", err)
	}

	out := make(map[string]interface{}, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		pub, err := rsaPublicKey(k)
		if err != nil {
			continue
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("zitadel: key set %s contained no usable RSA keys", url)
	}
	return out, nil
}

func (v *jwtValidator) discoverJWKS(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		v.cfg.Issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return "", err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("zitadel: discovery: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("zitadel: discovery returned %d", resp.StatusCode)
	}
	var doc struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", fmt.Errorf("zitadel: decode discovery: %w", err)
	}
	// The issuer must describe itself as the URL we hold. A mismatch means the
	// request reached a host serving a DIFFERENT issuer's document, and trusting
	// its keys would validate tokens no relying party would accept.
	if doc.Issuer != v.cfg.Issuer {
		return "", fmt.Errorf("zitadel: discovery issuer %q does not match configured %q", doc.Issuer, v.cfg.Issuer)
	}
	if doc.JWKSURI == "" {
		return "", fmt.Errorf("zitadel: discovery document has no jwks_uri")
	}
	return doc.JWKSURI, nil
}

func rsaPublicKey(k jwk) (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	eb, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	e := 0
	for _, b := range eb {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, fmt.Errorf("zitadel: key %q has a zero exponent", k.Kid)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}, nil
}

func (v *jwtValidator) validate(ctx context.Context, tokenString string) (map[string]interface{}, error) {
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("zitadel: token header has no kid")
		}
		return v.keyFor(ctx, kid)
	},
		jwt.WithIssuer(v.cfg.Issuer),
		jwt.WithValidMethods([]string{"RS256", "ES256"}),
	)
	if err != nil {
		return nil, fmt.Errorf("zitadel: token validation failed: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("zitadel: token is not valid")
	}
	return claims, nil
}
