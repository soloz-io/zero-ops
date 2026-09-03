package zitadel

import (
	"context"
	"fmt"
	"net/http"

	"github.com/soloz-io/zero-ops/internal/kube-sbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/models"
)

// Role keys granted within a tenant's project.
//
// Deliberately few and generic. These are the platform's authorisation
// vocabulary, not an application's: a fleet needing finer permissions expresses
// them in its own model rather than by multiplying roles here, because every
// role added here has to be granted, revoked and audited for every tenant.
// defaultUserRole is granted to a user created without an explicit role.
//
// The least privilege that still permits authentication. It cannot be "no role":
// projectRoleCheck denies sign-in to an ungranted user, so an unspecified role
// has to mean the smallest one rather than none.
const defaultUserRole = "member"

var defaultRoles = []struct{ Key, Display string }{
	{"admin", "Admin"},
	{"member", "Member"},
	{"viewer", "Viewer"},
}

// EnsureTenant makes a tenant's identity resources exist and returns them.
//
// Idempotent by construction: every step is find-or-create, so it can run on
// every reconcile. That matters more than it looks — this runs against an
// external system with no transaction, so a partial failure must leave the next
// attempt able to finish rather than to conflict.
//
// The order is a dependency chain, not a preference: an application belongs to a
// project, a project belongs to an organisation, and a role belongs to a
// project.
func (a *Auth) EnsureTenantIdentity(ctx context.Context, tenantID string, redirectURIs, postLogoutURIs []string) (*models.TenantIdentity, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("zitadel: tenantID is required")
	}

	orgID, err := a.ensureOrg(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("ensure organisation for %q: %w", tenantID, err)
	}

	projectID, err := a.ensureProject(ctx, orgID, a.cfg.ProjectName)
	if err != nil {
		return nil, fmt.Errorf("ensure project for %q: %w", tenantID, err)
	}

	// Roles must exist before any grant can reference them, and the project must
	// assert them before any token carries them. Asserting is a project setting
	// rather than a per-app one: the application flag alone is not sufficient,
	// which is only discoverable by finding tokens that carry no roles despite
	// the scope being requested and the grant existing.
	if err := a.ensureRoles(ctx, orgID, projectID); err != nil {
		return nil, fmt.Errorf("ensure roles for %q: %w", tenantID, err)
	}
	if err := a.ensureRoleAssertion(ctx, orgID, projectID); err != nil {
		return nil, fmt.Errorf("ensure role assertion for %q: %w", tenantID, err)
	}

	clientID, err := a.ensureOIDCApp(ctx, orgID, projectID, tenantID, redirectURIs, postLogoutURIs)
	if err != nil {
		return nil, fmt.Errorf("ensure application for %q: %w", tenantID, err)
	}

	return &models.TenantIdentity{TenantRef: orgID, ProjectRef: projectID, ClientID: clientID}, nil
}

func (a *Auth) ensureOrg(ctx context.Context, name string) (string, error) {
	var found struct {
		Result []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	q := map[string]any{
		"queries": []any{map[string]any{"nameQuery": map[string]any{"name": name, "method": "TEXT_QUERY_METHOD_EQUALS"}}},
	}
	if err := a.api.do(ctx, http.MethodPost, "/admin/v1/orgs/_search", "", q, &found); err != nil {
		return "", err
	}
	for _, o := range found.Result {
		if o.Name == name {
			return o.ID, nil
		}
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := a.api.do(ctx, http.MethodPost, "/management/v1/orgs", "", map[string]any{"name": name}, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

func (a *Auth) ensureProject(ctx context.Context, orgID, name string) (string, error) {
	var found struct {
		Result []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	q := map[string]any{
		"queries": []any{map[string]any{"nameQuery": map[string]any{"name": name, "method": "TEXT_QUERY_METHOD_EQUALS"}}},
	}
	if err := a.api.do(ctx, http.MethodPost, "/management/v1/projects/_search", orgID, q, &found); err != nil {
		return "", err
	}
	for _, p := range found.Result {
		if p.Name == name {
			return p.ID, nil
		}
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := a.api.do(ctx, http.MethodPost, "/management/v1/projects", orgID, projectSettings(name), &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

// projectSettings is the tenancy policy, enforced BY THE ISSUER.
//
// All three matter, and the two checks are what make this multi-tenant natively
// rather than by convention:
//
//	projectRoleAssertion  put the granted roles in the token
//	projectRoleCheck      "deny authentication if the user has no roles assigned
//	                      to this project"
//	hasProjectCheck       "verified that their affiliated organization has been
//	                      granted access to this project. Authentication is not
//	                      permitted for users from unauthorized organizations"
//
// With these on, tenant isolation is enforced BEFORE a token is minted. A user
// of another organisation is refused at the authorization endpoint, so the
// application never sees a token it has to reason about — which is a stronger
// guarantee than checking a tenant claim after the fact, because a check that
// lives in an application is one an application can forget to make.
//
// The owning organisation always passes hasProjectCheck (auth_request.go
// compares the project's resource owner against the user's organisation), so
// this costs a tenant nothing for its own project and denies everyone else by
// default. Cross-tenant access is then an explicit project GRANT rather than an
// absence of enforcement — see GrantProjectToOrg.
//
// The consequence is deliberate and worth stating: a user with no role cannot
// log in at all. That is why provisioning grants a role when it creates a user;
// an ungranted user is not a user with reduced access, it is a user who is
// refused.
func projectSettings(name string) map[string]any {
	return map[string]any{
		"name":                   name,
		"projectRoleAssertion":   true,
		"projectRoleCheck":       true,
		"hasProjectCheck":        true,
		"privateLabelingSetting": "PRIVATE_LABELING_SETTING_UNSPECIFIED",
	}
}

// ensureRoleAssertion turns on role assertion for an EXISTING project.
//
// Separate from creation because a project provisioned before this setting
// existed would otherwise never gain it, and the symptom is silent: tokens carry
// no roles even though the scope was requested and the grant is in place.
func (a *Auth) ensureRoleAssertion(ctx context.Context, orgID, projectID string) error {
	return a.api.do(ctx, http.MethodPut, "/management/v1/projects/"+projectID, orgID,
		projectSettings(a.cfg.ProjectName), nil)
}

// GrantProjectToOrg delegates a project to ANOTHER organisation.
//
// This is the provider's own answer to cross-tenant access, and the reason
// hasProjectCheck can be left on: access from outside the owning organisation is
// expressed as a grant that someone made, rather than as enforcement nobody
// enabled. Idempotent, so it is safe to reconcile.
func (a *Auth) GrantProjectToOrg(ctx context.Context, ownerOrgID, projectID, grantedOrgID string, roles []string) error {
	var existing struct {
		Result []struct {
			GrantID      string `json:"grantId"`
			GrantedOrgID string `json:"grantedOrgId"`
		} `json:"result"`
	}
	if err := a.api.do(ctx, http.MethodPost,
		"/management/v1/projects/"+projectID+"/grants/_search", ownerOrgID,
		map[string]any{"query": map[string]any{"limit": 100}}, &existing); err != nil && !isNotFound(err) {
		return err
	}
	for _, g := range existing.Result {
		if g.GrantedOrgID == grantedOrgID {
			return a.api.do(ctx, http.MethodPut,
				"/management/v1/projects/"+projectID+"/grants/"+g.GrantID, ownerOrgID,
				map[string]any{"roleKeys": roles}, nil)
		}
	}
	return a.api.do(ctx, http.MethodPost,
		"/management/v1/projects/"+projectID+"/grants", ownerOrgID,
		map[string]any{"grantedOrgId": grantedOrgID, "roleKeys": roles}, nil)
}

// EnsureOrgDomain gives a tenant a domain of its own.
//
// This is what lets the hosted login discover a tenant from the user's address
// instead of the platform having to ask which tenant they meant, and it is the
// provider's mechanism rather than one the platform builds. Verification is not
// requested: the domain is derived from the instance's own zone, so it is
// already ours.
func (a *Auth) EnsureOrgDomain(ctx context.Context, orgID, domain string) error {
	err := a.api.do(ctx, http.MethodPost, "/management/v1/orgs/me/domains", orgID,
		map[string]any{"domain": domain}, nil)
	if err == nil {
		return nil
	}
	// Already present is the normal case on every reconcile after the first.
	var ae *apiError
	if asAPIError(err, &ae) && (ae.Status == http.StatusConflict || ae.Status == http.StatusPreconditionFailed) {
		return nil
	}
	return err
}

// EnsureOrgOwner makes a user an administrator OF THEIR OWN organisation.
//
// Delegated tenant administration, using the provider's org membership model
// rather than a platform-specific "tenant admin" notion. The authority is scoped
// to the organisation, so a tenant administrator cannot reach another tenant —
// which is the property a platform-level admin flag would have to re-implement
// and could get wrong.
func (a *Auth) EnsureOrgOwner(ctx context.Context, orgID, userID string) error {
	err := a.api.do(ctx, http.MethodPost, "/management/v1/orgs/me/members", orgID,
		map[string]any{"userId": userID, "roles": []string{"ORG_OWNER"}}, nil)
	if err == nil {
		return nil
	}
	var ae *apiError
	if asAPIError(err, &ae) && (ae.Status == http.StatusConflict || ae.Status == http.StatusPreconditionFailed) {
		return a.api.do(ctx, http.MethodPut, "/management/v1/orgs/me/members/"+userID, orgID,
			map[string]any{"roles": []string{"ORG_OWNER"}}, nil)
	}
	return err
}

func (a *Auth) ensureRoles(ctx context.Context, orgID, projectID string) error {
	var existing struct {
		Result []struct {
			Key string `json:"key"`
		} `json:"result"`
	}
	if err := a.api.do(ctx, http.MethodPost,
		"/management/v1/projects/"+projectID+"/roles/_search", orgID,
		map[string]any{"query": map[string]any{"limit": 100}}, &existing); err != nil {
		return err
	}
	have := map[string]bool{}
	for _, r := range existing.Result {
		have[r.Key] = true
	}

	for _, want := range defaultRoles {
		if have[want.Key] {
			continue
		}
		body := map[string]any{"roleKey": want.Key, "displayName": want.Display, "group": a.cfg.ProjectName}
		if err := a.api.do(ctx, http.MethodPost,
			"/management/v1/projects/"+projectID+"/roles", orgID, body, nil); err != nil {
			return err
		}
	}
	return nil
}

func (a *Auth) ensureOIDCApp(ctx context.Context, orgID, projectID, tenantID string, redirectURIs, postLogoutURIs []string) (string, error) {
	appName := tenantID + "-public-client"

	var existing struct {
		Result []struct {
			Name       string `json:"name"`
			OIDCConfig struct {
				ClientID string `json:"clientId"`
			} `json:"oidcConfig"`
		} `json:"result"`
	}
	if err := a.api.do(ctx, http.MethodPost,
		"/management/v1/projects/"+projectID+"/apps/_search", orgID,
		map[string]any{"query": map[string]any{"limit": 100}}, &existing); err != nil {
		return "", err
	}
	for _, app := range existing.Result {
		if app.Name == appName && app.OIDCConfig.ClientID != "" {
			return app.OIDCConfig.ClientID, nil
		}
	}

	// A PUBLIC client: authMethodType NONE, no secret, PKCE carries the proof.
	// Anything shipped to a browser is readable, so a secret here would be a
	// published secret.
	//
	// Role assertion is set on the app as well as the project because both gate
	// the claim, and only the pair produces a token carrying roles.
	body := map[string]any{
		"name":                     appName,
		"redirectUris":             redirectURIs,
		"postLogoutRedirectUris":   postLogoutURIs,
		"responseTypes":            []string{"OIDC_RESPONSE_TYPE_CODE"},
		"grantTypes":               []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE", "OIDC_GRANT_TYPE_REFRESH_TOKEN"},
		"appType":                  "OIDC_APP_TYPE_WEB",
		"authMethodType":           "OIDC_AUTH_METHOD_TYPE_NONE",
		"accessTokenType":          "OIDC_TOKEN_TYPE_JWT",
		"accessTokenRoleAssertion": true,
		"idTokenRoleAssertion":     true,
		"idTokenUserinfoAssertion": true,
		"devMode":                  false,
	}
	var created struct {
		ClientID string `json:"clientId"`
	}
	if err := a.api.do(ctx, http.MethodPost,
		"/management/v1/projects/"+projectID+"/apps/oidc", orgID, body, &created); err != nil {
		return "", err
	}
	if created.ClientID == "" {
		return "", fmt.Errorf("zitadel: application %q created without a client id", appName)
	}
	return created.ClientID, nil
}

// GrantRole gives a user a role within a tenant's project.
//
// Idempotent: an existing grant is updated rather than rejected, so re-running
// reconciliation does not fail on work already done.
func (a *Auth) GrantRole(ctx context.Context, orgID, projectID, userID string, roles []string) error {
	var existing struct {
		Result []struct {
			ID        string `json:"id"`
			ProjectID string `json:"projectId"`
		} `json:"result"`
	}
	if err := a.api.do(ctx, http.MethodPost, "/management/v1/users/grants/_search", orgID,
		map[string]any{"queries": []any{map[string]any{"userIdQuery": map[string]any{"userId": userID}}}},
		&existing); err != nil && !isNotFound(err) {
		return err
	}
	for _, g := range existing.Result {
		if g.ProjectID == projectID {
			return a.api.do(ctx, http.MethodPut,
				"/management/v1/users/"+userID+"/grants/"+g.ID, orgID,
				map[string]any{"roleKeys": roles}, nil)
		}
	}
	return a.api.do(ctx, http.MethodPost, "/management/v1/users/"+userID+"/grants", orgID,
		map[string]any{"projectId": projectID, "roleKeys": roles}, nil)
}

var _ interfaces.ITenantIdentityProvisioner = (*Auth)(nil)
