package zitadel

import (
	"context"
	"fmt"
	"net/http"
)

// EnsureRegistrationClosed disables self-service registration on the instance.
//
// Open registration is the issuer's default, and on this platform it is wrong in
// a specific way: a self-registered account is given no organisation, so the
// issuer places it in the DEFAULT one — which is the platform's own, where the
// administrators are. An unknown person does not merely get an account, they get
// membership of the tenant that operates the cluster.
//
// It is not an immediate escalation, because authentication still requires a
// role in a project and they have none. It is worse than that in one respect:
// the account looks legitimate, carries the platform organisation as its tenant
// in every token it could obtain, and nothing about it reads as unexpected.
//
// Tenants are onboarded by provisioning, which creates an organisation and its
// owner deliberately (ADR-041). Registration is therefore not a door that should
// exist here at all, rather than one that should be narrower.
//
// Enforced on every start rather than set once: it is the issuer's default, so a
// version upgrade or a policy reset restores it silently, and a check that only
// ran at installation would never notice.
func (a *Auth) EnsureRegistrationClosed(ctx context.Context) error {
	var current struct {
		Policy map[string]any `json:"policy"`
	}
	if err := a.api.do(ctx, http.MethodGet, "/admin/v1/policies/login", "", nil, &current); err != nil {
		return fmt.Errorf("zitadel: read the login policy: %w", err)
	}
	if current.Policy == nil {
		return fmt.Errorf("zitadel: the issuer returned no login policy")
	}
	if allow, ok := current.Policy["allowRegister"].(bool); ok && !allow {
		return nil
	}

	// The whole policy is sent back, not just the changed field: this endpoint
	// REPLACES rather than merges, so omitting a field resets it to its zero
	// value — which would silently turn off password login or external identity
	// providers while appearing to change only registration.
	body := map[string]any{}
	for k, v := range current.Policy {
		switch k {
		case "details", "isDefault":
			continue
		}
		body[k] = v
	}
	body["allowRegister"] = false

	if err := a.api.do(ctx, http.MethodPut, "/admin/v1/policies/login", "", body, nil); err != nil {
		return fmt.Errorf("zitadel: close self-service registration: %w", err)
	}
	return nil
}

// EnsureTenantSelfRegistration sets self-service signup for ONE organisation,
// leaving the instance default untouched.
//
// The two levels are what make this safe. The instance default stays closed, so
// a registration naming no organisation — the shape that put a stranger in the
// platform's own tenant — is still refused. This grants signup only inside an
// organisation the request has already been scoped to, which the tenant's
// gateway does by sending urn:zitadel:iam:org:id:<orgId> on every authorize.
//
// Reconciled rather than set once. Registration is a security boundary, and one
// that lives only in a console can be re-opened by an upgrade or a support
// session with nothing to notice it; asserting it on every pass repairs that.
//
// POST creates the organisation's own policy; PUT updates one that exists. An
// organisation with no policy of its own INHERITS the instance's, so the first
// write must create — a PUT against a policy that does not exist is rejected,
// and the two are not interchangeable.
func (a *Auth) EnsureTenantSelfRegistration(ctx context.Context, orgID string, allow bool) error {
	if orgID == "" {
		return fmt.Errorf("zitadel: orgID is required to set a registration policy")
	}

	var current struct {
		Policy map[string]any `json:"policy"`
	}
	if err := a.api.do(ctx, http.MethodGet, "/management/v1/policies/login", orgID, nil, &current); err != nil {
		return fmt.Errorf("zitadel: read the login policy for %q: %w", orgID, err)
	}
	if current.Policy == nil {
		return fmt.Errorf("zitadel: no login policy returned for organisation %q", orgID)
	}

	inherited, _ := current.Policy["isDefault"].(bool)
	if allowed, ok := current.Policy["allowRegister"].(bool); ok && allowed == allow && !inherited {
		return nil
	}
	// Nothing to do: the organisation inherits a default that already matches,
	// and creating its own copy would only add a policy to keep in step.
	if inherited && !allow {
		return nil
	}

	// The whole policy is sent because these endpoints REPLACE rather than merge:
	// an omitted field resets to its zero value, which would silently disable
	// password login while appearing to change only registration.
	body := map[string]any{}
	for k, v := range current.Policy {
		switch k {
		case "details", "isDefault":
			continue
		}
		body[k] = v
	}
	body["allowRegister"] = allow

	method := http.MethodPut
	if inherited {
		method = http.MethodPost
	}
	if err := a.api.do(ctx, method, "/management/v1/policies/login", orgID, body, nil); err != nil {
		return fmt.Errorf("zitadel: set registration=%v for organisation %q: %w", allow, orgID, err)
	}
	return nil
}

// InviteTenantUser is a deliberate stub for invitation-based onboarding.
//
// Self-service registration lets anyone reaching a tenant's hostname create an
// account there. That is right for verifying tenant assignment and wrong for a
// product with real customers, where the tenant should name the address and the
// issuer send a one-time link.
//
// The blocker is transport, not logic: the issuer has no SMTP sender configured,
// so an invitation would end at a link nobody receives. Zitadel supports this
// natively once mail exists (POST /v2/users/human/{userId}/invite_code), so the
// remaining work is configuring a sender and calling it — not building an
// invitation system.
//
// It returns an explicit error rather than doing nothing, because a caller that
// believed a user had been invited would wait for someone who was never told.
func (a *Auth) InviteTenantUser(ctx context.Context, orgID, email string) error {
	return fmt.Errorf("zitadel: invitations need an SMTP sender on the issuer, which is not configured; " +
		"use selfRegistration or provision the user directly")
}
