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
