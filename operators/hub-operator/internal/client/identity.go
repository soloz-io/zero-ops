package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// IdentityClient asks the Tenant Identity Service to provision a tenant's
// identity resources.
//
// The operator ORCHESTRATES; it does not create identity resources itself.
// ADR-041 assigns tenant identity lifecycle to that service and forbids this
// operator from "acting as a secret manager for tenant or application secrets",
// so the client id a tenant's gateway needs is allocated and published there.
// Calling across that boundary is the point: it keeps one component able to talk
// to the issuer, rather than two with their own credentials and their own idea
// of what a tenant looks like.
type IdentityClient struct {
	baseURL string
	http    *http.Client
}

// NewIdentityClient returns nil when no address is configured.
//
// A nil client means "this environment does not provision tenant identity",
// which is a legitimate state — an environment whose issuer has no notion of a
// tenant has nothing to provision. Callers skip on nil rather than failing, so
// enabling this is a configuration change and not a code path that must exist
// everywhere.
func NewIdentityClient(baseURL string) *IdentityClient {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &IdentityClient{
		baseURL: baseURL,
		// Generous but bounded. Provisioning creates several objects at the
		// issuer in sequence, so it is slower than a health check — but this runs
		// inside a reconcile, and an unbounded call would wedge the worker rather
		// than fail and requeue.
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// TenantIdentity is what the identity service reports back.
type TenantIdentity struct {
	TenantRef  string `json:"tenantRef"`
	ProjectRef string `json:"projectRef"`
	ClientID   string `json:"clientId"`

	// OwnerPassword is set only on the call that created the owner's account.
	// Empty means no new credential exists — not that the password is unknown —
	// so it must never be persisted as an empty value over a stored one.
	OwnerPassword string `json:"ownerPassword,omitempty"`
}

// EnsureTenantIdentity is idempotent, so it is safe on every reconcile.
func (c *IdentityClient) EnsureTenantIdentity(ctx context.Context, tenantID, ownerEmail string, redirectURIs, postLogoutURIs []string) (*TenantIdentity, error) {
	body, err := json.Marshal(map[string]any{
		"redirectUris":   redirectURIs,
		"postLogoutUris": postLogoutURIs,
		"ownerEmail":     ownerEmail,
	})
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/internal/tenants/%s/identity", c.baseURL, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("identity service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)

	// 501 means the configured provider cannot provision tenants. That is a
	// configuration answer, not a transient one, so it is distinguishable here —
	// a caller that retried it would spin forever against a service behaving
	// exactly as configured.
	if resp.StatusCode == http.StatusNotImplemented {
		return nil, ErrIdentityProvisioningUnsupported
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("identity service: POST %s -> %d: %s", url, resp.StatusCode, msg)
	}

	var out TenantIdentity
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("identity service: decode response: %w", err)
	}
	if out.ClientID == "" {
		return nil, fmt.Errorf("identity service: reported success with no client id for tenant %q", tenantID)
	}
	return &out, nil
}

// ErrIdentityProvisioningUnsupported is returned when the configured identity
// provider has no tenant to provision. Sentinel so a caller can treat it as a
// steady state rather than a failure to retry.
var ErrIdentityProvisioningUnsupported = fmt.Errorf("identity service: provider does not provision tenant identities")
