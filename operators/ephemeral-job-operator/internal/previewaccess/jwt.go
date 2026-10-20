package previewaccess

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Verifier checks the session credential the tenant gateway forwards on the
// codes and state endpoints (ADR-108 §3).
//
// The gateway has already run the login and enforced the tenant-organisation
// binding (ADR-050, ADR-094); this re-verifies that what arrived is a genuine,
// unexpired token from the platform's issuer, so a request that reached this
// service some other way carries no identity. RS256 only: it is what the issuer
// signs with, and accepting an algorithm nobody uses is attack surface.
type Verifier struct {
	Issuer  string
	JWKSURL string
	HTTP    *http.Client

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
}

// Claims are the parts of a verified token this service uses.
type Claims struct {
	Subject string
}

const (
	clockSkew         = 60 * time.Second
	jwksMinRefetchGap = 30 * time.Second
)

// Verify returns the token's claims, or why it is refused.
func (v *Verifier) Verify(ctx context.Context, raw string) (Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Claims{}, errors.New("not a compact JWS")
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &hdr); err != nil {
		return Claims{}, fmt.Errorf("header: %w", err)
	}
	if hdr.Alg != "RS256" {
		return Claims{}, fmt.Errorf("algorithm %q is not accepted", hdr.Alg)
	}
	key, err := v.key(ctx, hdr.Kid)
	if err != nil {
		return Claims{}, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, fmt.Errorf("signature encoding: %w", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return Claims{}, errors.New("signature does not verify")
	}

	var c struct {
		Iss string   `json:"iss"`
		Sub string   `json:"sub"`
		Exp *float64 `json:"exp"`
		Nbf *float64 `json:"nbf"`
	}
	if err := decodeSegment(parts[1], &c); err != nil {
		return Claims{}, fmt.Errorf("claims: %w", err)
	}
	now := time.Now()
	switch {
	case c.Iss != v.Issuer:
		return Claims{}, fmt.Errorf("issuer %q is not the platform's", c.Iss)
	case c.Sub == "":
		return Claims{}, errors.New("token names no subject")
	case c.Exp == nil:
		return Claims{}, errors.New("token has no expiry")
	case now.After(time.Unix(int64(*c.Exp), 0).Add(clockSkew)):
		return Claims{}, errors.New("token has expired")
	case c.Nbf != nil && now.Add(clockSkew).Before(time.Unix(int64(*c.Nbf), 0)):
		return Claims{}, errors.New("token is not valid yet")
	}
	return Claims{Subject: c.Sub}, nil
}

// key returns the signing key for kid, refetching the key set once when the
// kid is unknown (a rotation), but no more often than jwksMinRefetchGap so a
// stream of forged kids cannot turn this service into a load on the issuer.
func (v *Verifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	if time.Since(v.fetchedAt) < jwksMinRefetchGap && v.keys != nil {
		return nil, fmt.Errorf("signing key %q is not in the issuer's key set", kid)
	}
	keys, err := v.fetch(ctx)
	v.fetchedAt = time.Now()
	if err != nil {
		return nil, fmt.Errorf("fetching the issuer's key set: %w", err)
	}
	v.keys = keys
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("signing key %q is not in the issuer's key set", kid)
}

func (v *Verifier) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.JWKSURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := v.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d", v.JWKSURL, resp.StatusCode)
	}
	var set struct {
		Keys []struct {
			Kty, Kid, Use, N, E string
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, err
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	if len(out) == 0 {
		return nil, errors.New("the key set holds no RSA signing key")
	}
	return out, nil
}

func decodeSegment(seg string, into any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, into)
}
