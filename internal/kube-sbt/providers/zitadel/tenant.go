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
func (a *Auth) EnsureTenantIdentity(ctx context.Context, tenantID, ownerEmail string, selfRegistration bool, redirectURIs, postLogoutURIs []string) (*models.TenantIdentity, error) {
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

	clientID, err := a.ensureOIDCApp(ctx, orgID, projectID, tenantID+"-public-client", redirectURIs, postLogoutURIs)
	if err != nil {
		return nil, fmt.Errorf("ensure application for %q: %w", tenantID, err)
	}

	// Grant the owner, or the tenant has no one who can enter it.
	//
	// projectRoleCheck denies authentication to a user holding no role in the
	// project, so provisioning that stops at the resources produces a tenant
	// whose every login fails with a grant error. The owner is made an
	// administrator of their own organisation as well, so they can add the next
	// user without the platform doing it for them — delegated tenant
	// administration is the provider's own model, not something built on top.
	//
	// Best-effort and non-fatal: the identity resources are correct either way,
	// and the next reconcile repeats this. Failing the whole call would discard a
	// client id that was already allocated.
	// Everyone in this organisation gets a role, or they cannot sign in.
	//
	// Self-service registration creates the account and nothing else, so without
	// this a person registers successfully and is then refused with
	// GrantRequired the moment they try to use it.
	if selfRegistration {
		if gerr := a.grantUngrantedMembers(ctx, orgID, projectID); gerr != nil {
			_ = gerr
		}
	}

	// Reconciled on every pass so a policy changed by hand converges back.
	// Non-fatal: the tenant's identity is correct either way, and failing here
	// would discard a client id that was already allocated.
	if rerr := a.EnsureTenantSelfRegistration(ctx, orgID, selfRegistration); rerr != nil {
		_ = rerr
	}

	out := &models.TenantIdentity{TenantRef: orgID, ProjectRef: projectID, ClientID: clientID}
	if ownerEmail == "" {
		return out, nil
	}

	owner, err := a.findUserByEmail(ctx, ownerEmail)
	if err != nil && !isNotFound(err) {
		// Could not tell whether the owner exists. Creating one now risks a
		// duplicate account, so the tenant is reported without an owner and the
		// next reconcile retries.
		return out, nil
	}

	if owner == nil {
		// The owner does not exist yet, which is the NORMAL case for a new
		// tenant. Creating them here is what makes a freshly provisioned tenant
		// usable: the issuer refuses anyone holding no role, so a tenant with no
		// owner account is a tenant whose every login fails.
		//
		// A password is generated because the issuer has no mail transport
		// configured here, so an invitation cannot be delivered. It is returned
		// exactly once, for the caller to persist where an operator can retrieve
		// it; it is never reset on a later reconcile.
		pw, perr := generateInitialPassword()
		if perr != nil {
			return out, nil
		}
		created, cerr := a.CreateUser(ctx, models.User{
			Email:    ownerEmail,
			Name:     ownerEmail,
			TenantID: orgID,
			Password: pw,
			Roles:    []string{"admin"},
		})
		if cerr != nil {
			return out, nil
		}
		owner = created
		out.OwnerPassword = pw
	}

	// Idempotent, and repeated on every reconcile so a grant removed by hand
	// converges back rather than leaving the owner locked out of their own
	// tenant.
	_ = a.GrantRole(ctx, orgID, projectID, owner.ID, []string{"admin"})
	_ = a.EnsureOrgOwner(ctx, orgID, owner.ID)

	return out, nil
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

func (a *Auth) ensureOIDCApp(ctx context.Context, orgID, projectID, appName string, redirectURIs, postLogoutURIs []string) (string, error) {

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

// EnsurePlatformApp provisions an OIDC application in the PLATFORM's own
// organisation and returns its client id.
//
// Separate from EnsureTenantIdentity because it is not a tenant. A tenant gets
// its own organisation so its users are owned by it; the platform's own clients
// — the Kubernetes API server's among them — belong where platform
// administrators already are, or those administrators could not authenticate to
// them at all: the issuer refuses a user whose organisation was never granted
// the project.
//
// Idempotent, like everything else here: it is called on every reconcile.
func (a *Auth) EnsurePlatformApp(ctx context.Context, appName string, redirectURIs, postLogoutURIs []string) (string, error) {
	if appName == "" {
		return "", fmt.Errorf("zitadel: appName is required")
	}
	orgID, err := a.platformOrg(ctx)
	if err != nil {
		return "", err
	}

	projectID, err := a.ensureProject(ctx, orgID, a.cfg.ProjectName)
	if err != nil {
		return "", fmt.Errorf("ensure platform project: %w", err)
	}
	if err := a.ensureRoles(ctx, orgID, projectID); err != nil {
		return "", fmt.Errorf("ensure platform roles: %w", err)
	}
	if err := a.ensureRoleAssertion(ctx, orgID, projectID); err != nil {
		return "", fmt.Errorf("ensure platform role assertion: %w", err)
	}

	clientID, err := a.ensureOIDCApp(ctx, orgID, projectID, appName, redirectURIs, postLogoutURIs)
	if err != nil {
		return "", fmt.Errorf("ensure platform application %q: %w", appName, err)
	}
	return clientID, nil
}

// platformOrg resolves the organisation the platform's own clients belong to.
//
// Configured when set, DISCOVERED otherwise: the service credential this
// provider authenticates with belongs to the instance's own organisation, which
// is where platform administrators are, so asking the issuer who we are answers
// the question without a second value to keep in step.
//
// That matters beyond tidiness. The id is allocated by the issuer at
// installation, so any configured copy is transcribed by hand from a UI — and a
// wrong one does not fail loudly. It creates the platform's applications inside
// some other organisation, where the administrators who need them are refused
// because their organisation was never granted the project.
func (a *Auth) platformOrg(ctx context.Context) (string, error) {
	if a.cfg.PlatformOrgID != "" {
		return a.cfg.PlatformOrgID, nil
	}
	var resp struct {
		Org struct {
			ID string `json:"id"`
		} `json:"org"`
	}
	if err := a.api.do(ctx, http.MethodGet, "/management/v1/orgs/me", "", nil, &resp); err != nil {
		return "", fmt.Errorf("zitadel: resolve the platform organisation: %w", err)
	}
	if resp.Org.ID == "" {
		return "", fmt.Errorf("zitadel: the issuer reported no organisation for this credential")
	}
	return resp.Org.ID, nil
}

// grantUngrantedMembers gives the default role to every member of a tenant's
// organisation that holds none.
//
// Needed because self-service registration creates a user and stops there. The
// tenant's project denies authentication to anyone holding no role
// (projectRoleCheck), and the issuer has no hook that grants one on signup — so
// a person completes registration, is created in the right organisation, and is
// then refused at the moment they try to use the account:
//
//	Errors.User.GrantRequired
//
// which names no remedy and arrives after they have already chosen a password.
//
// Reconciled rather than event-driven, because there is no event to hook. That
// makes the grant eventually-consistent: someone who registers between two
// reconciles is refused until the next one. Accepted deliberately — the
// alternative is turning projectRoleCheck off, which would admit users of OTHER
// organisations to this tenant, and a brief delay is a far better failure than a
// silent hole in tenant isolation.
//
// Only users with NO grant are touched. Anyone already holding a role keeps
// exactly what they were given, so an operator who granted admin by hand does
// not have it replaced by member on the next pass.
func (a *Auth) grantUngrantedMembers(ctx context.Context, orgID, projectID string) error {
	var users struct {
		Result []struct {
			UserID string `json:"userId"`
		} `json:"result"`
	}
	q := map[string]any{
		"query":   map[string]any{"limit": 500},
		"queries": []any{map[string]any{"organizationIdQuery": map[string]any{"organizationId": orgID}}},
	}
	if err := a.api.do(ctx, http.MethodPost, "/v2/users", orgID, q, &users); err != nil {
		return fmt.Errorf("list members of %q: %w", orgID, err)
	}

	var grants struct {
		Result []struct {
			UserID    string `json:"userId"`
			ProjectID string `json:"projectId"`
		} `json:"result"`
	}
	if err := a.api.do(ctx, http.MethodPost, "/management/v1/users/grants/_search", orgID,
		map[string]any{"query": map[string]any{"limit": 500}}, &grants); err != nil && !isNotFound(err) {
		return fmt.Errorf("list grants in %q: %w", orgID, err)
	}
	granted := map[string]bool{}
	for _, g := range grants.Result {
		if g.ProjectID == projectID {
			granted[g.UserID] = true
		}
	}

	for _, u := range users.Result {
		if granted[u.UserID] {
			continue
		}
		// Best effort per user: one failure must not stop the rest, or a single
		// bad account would keep every later registrant locked out.
		_ = a.GrantRole(ctx, orgID, projectID, u.UserID, []string{defaultUserRole})
	}
	return nil
}
