// Package zitadel implements the platform's identity provider interface against
// an issuer whose tenancy is STRUCTURAL: an organisation owns the user rather
// than describing it.
//
// That single property is why this provider exists and why it is shaped the way
// it is (ADR-059). Under an issuer where the tenant is an attribute written onto
// an identity, a tenant-less user is expressible, and the platform had to police
// it — hence a required tenant_id claim, a platform_admins exemption to excuse
// the admins that rule made unrepresentable, and a reconciler pushing group
// assignments into identity metadata. None of that is reproduced here. The
// provider's own model answers those questions:
//
//	tenant         → organisation (owns the user; cannot be absent)
//	authorisation  → project roles granted to a user WITHIN an organisation
//	delegation     → project grants between organisations
//
// ADR-041 assigns tenant identity lifecycle to this service, so provisioning a
// tenant's identity resources belongs here rather than in the Hub Operator,
// which the same matrix forbids from managing tenant credentials.
package zitadel

import (
	"fmt"
	"strings"
	"time"
)

// Config holds everything this provider needs to reach its issuer.
type Config struct {
	// Issuer is the public OIDC issuer URL, e.g. https://auth.example.com.
	// It is also the API base: the management, admin and v2 APIs are served from
	// the same origin.
	//
	// This MUST be the URL relying parties are configured with, because the
	// issuer is derived per-request from the Host header — a request arriving on
	// a different name mints tokens naming that name, which every client then
	// rejects as a spoofed issuer.
	Issuer string

	// ServiceToken authenticates this provider's API calls: a machine user's
	// personal access token with the privileges to manage organisations,
	// projects and users.
	//
	// Delivered as a secret, never configured inline. This provider does not
	// generate it — ADR-041 forbids this service from PKI and confines it to
	// uploading credentials it owns, not minting its own admission.
	ServiceToken string

	// PlatformOrgID is the organisation platform administrators belong to.
	//
	// A platform admin is an ordinary member of this organisation and carries it
	// as their tenant, which is what removes the need for a tenant-less identity
	// and the exemption that used to model one.
	PlatformOrgID string

	// ProjectName is the project whose roles express authorisation for a tenant.
	// One per organisation, created on demand.
	ProjectName string

	// HTTPTimeout bounds every call. Identity is on the login path, so a hung
	// request is a hung login.
	HTTPTimeout time.Duration
}

func (c *Config) defaults() {
	c.Issuer = strings.TrimRight(strings.TrimSpace(c.Issuer), "/")
	// Trimmed, and this is not cosmetic. A credential delivered through a file or
	// a Secret commonly carries a trailing newline, and Go refuses to build a
	// request with one:
	//
	//   net/http: invalid header field value for "Authorization"
	//
	// which names neither the credential nor its source, and looks like a
	// malformed token rather than an extra byte.
	c.ServiceToken = strings.TrimSpace(c.ServiceToken)
	c.PlatformOrgID = strings.TrimSpace(c.PlatformOrgID)
	if c.ProjectName == "" {
		c.ProjectName = "platform"
	}
	if c.HTTPTimeout == 0 {
		c.HTTPTimeout = 10 * time.Second
	}
}

func (c Config) validate() error {
	if c.Issuer == "" {
		return fmt.Errorf("zitadel: Issuer is required")
	}
	if !strings.HasPrefix(c.Issuer, "https://") && !strings.HasPrefix(c.Issuer, "http://") {
		return fmt.Errorf("zitadel: Issuer must be an absolute URL, got %q", c.Issuer)
	}
	if c.ServiceToken == "" {
		return fmt.Errorf("zitadel: ServiceToken is required")
	}
	return nil
}
