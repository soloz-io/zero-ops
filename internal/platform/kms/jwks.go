package kms

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// JWKS synchronisation: the platform control that lets an unreachable cluster federate.
//
// WHY THIS EXISTS, and it corrects something this ADR asserted. The design said OIDC
// federation would require Google to fetch the API server's discovery document, and
// therefore a publicly reachable issuer -- and chose X.509 partly to avoid that. The
// premise was wrong. Verified in the pinned client:
//
//	google.golang.org/api@v0.287.1/iam/v1/iam-gen.go, Oidc.JwksJson:
//	  "Optional. OIDC JWKs in JSON String format... IF NOT SET, the `jwks_uri` from the
//	   discovery document that is fetched from the well-known path of the `issuer_uri`,
//	   will be used."
//
// Uploading the JWKS is what removes the fetch. The cluster never has to be reachable
// from the internet; Google validates the token's signature against keys it already
// holds.
//
// WHAT THAT COSTS, and it is the reason this file is a control rather than a one-off
// command: an uploaded JWKS is a COPY. Cluster service-account signing keys rotate, and
// when they do the copy is stale and every token fails validation -- which surfaces as a
// reconciler that cannot authenticate, with nothing in the cluster having changed. So the
// upload has to be re-run, idempotently, as a platform action rather than a setup step
// somebody did once.

// jwk is the subset needed to compare two key sets meaningfully.
type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
	Crv string `json:"crv"`
}

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

// MaxUploadedJWKs is Google's documented ceiling for an uploaded key set.
//
// Checked here rather than discovered from an API error, because the failure mode is
// specific: a cluster mid-rotation briefly publishes more keys than usual, and a sync
// that silently truncated would upload a set missing the key that signs current tokens.
const MaxUploadedJWKs = 8

// parseJWKS validates and normalises a key set.
//
// IT REFUSES AN EMPTY SET. Uploading zero keys is accepted by the API and means no token
// validates -- a change that looks like a successful sync and takes the reconciler's
// identity away.
func parseJWKS(raw []byte) (*jwkSet, error) {
	var set jwkSet
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, fmt.Errorf("the JWKS is not valid JSON: %w", err)
	}
	if len(set.Keys) == 0 {
		return nil, fmt.Errorf("the JWKS contains no keys. Uploading an empty set is accepted " +
			"and means nothing validates, which looks like a successful sync and removes the " +
			"identity it was meant to establish")
	}
	if len(set.Keys) > MaxUploadedJWKs {
		return nil, fmt.Errorf("the JWKS contains %d keys and Google accepts at most %d. "+
			"Truncating would risk dropping the key that signs current tokens",
			len(set.Keys), MaxUploadedJWKs)
	}
	for i, k := range set.Keys {
		if strings.TrimSpace(k.Kid) == "" {
			return nil, fmt.Errorf("key %d has no kid, so a rotation could not be tracked", i)
		}
		if strings.TrimSpace(k.Kty) == "" {
			return nil, fmt.Errorf("key %q has no kty", k.Kid)
		}
	}
	return &set, nil
}

// kids returns the sorted key identifiers, which is how two sets are compared.
//
// BY CONTENT, NOT BY STRING. The same key set re-serialised by a different tool differs
// in field order and whitespace, and a string comparison would PATCH on every run --
// which turns an idempotent control into a write loop against the identity provider.
func (s *jwkSet) kids() []string {
	out := make([]string, 0, len(s.Keys))
	for _, k := range s.Keys {
		out = append(out, k.Kid)
	}
	sort.Strings(out)
	return out
}

func sameKids(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// JWKSSync is what one synchronisation did.
type JWKSSync struct {
	Provider string
	// Changed is false when the provider already held this key set.
	Changed bool
	Before  []string
	After   []string
}

// SyncJWKS uploads a cluster's service-account signing keys to its federation provider.
//
// IDEMPOTENT BY CONTENT. It reads first, compares key identifiers, and writes only on a
// real difference -- so a scheduled run against an unchanged cluster is a single GET.
func (c *Client) SyncJWKS(ctx context.Context, pool, provider string, jwksRaw []byte) (*JWKSSync, error) {
	want, err := parseJWKS(jwksRaw)
	if err != nil {
		return nil, err
	}
	name := c.ProviderName(pool, provider)

	var current struct {
		Oidc *struct {
			JwksJson  string `json:"jwksJson"`
			IssuerUri string `json:"issuerUri"`
		} `json:"oidc"`
	}
	if err := c.call(ctx, http.MethodGet, c.iamHost+"/v1/"+name, nil, &current); err != nil {
		if NotFound(err) {
			return nil, fmt.Errorf("%s does not exist. The reconciler's federation provider has "+
				"to be established before its signing keys can be uploaded to it", name)
		}
		return nil, err
	}
	if current.Oidc == nil {
		return nil, fmt.Errorf("%s is not an OIDC provider, so it has no JWKS to synchronise. "+
			"The KMS plugin's provider is X.509 and is a different object -- check the "+
			"provider name", name)
	}

	out := &JWKSSync{Provider: name, After: want.kids()}
	if existing := strings.TrimSpace(current.Oidc.JwksJson); existing != "" {
		if have, err := parseJWKS([]byte(existing)); err == nil {
			out.Before = have.kids()
			if sameKids(out.Before, out.After) {
				return out, nil // unchanged: no write
			}
		}
	}

	// PATCH with an explicit updateMask, so this touches the JWKS and nothing else. A
	// full-resource write would silently revert an attributeMapping or an audience
	// somebody changed for a reason.
	patch := fmt.Sprintf("%s/v1/%s?updateMask=%s",
		c.iamHost, name, url.QueryEscape("oidc.jwks_json"))
	body := map[string]any{"oidc": map[string]any{"jwksJson": string(jwksRaw)}}
	if err := c.call(ctx, http.MethodPatch, patch, body, nil); err != nil {
		return nil, fmt.Errorf("uploading the JWKS to %s: %w", name, err)
	}
	out.Changed = true
	return out, nil
}
