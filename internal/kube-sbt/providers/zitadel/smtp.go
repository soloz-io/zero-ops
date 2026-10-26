package zitadel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// SMTP configuration for a LIVE instance.
//
// WHY THIS EXISTS
//
//	The chart configures SMTP through DefaultInstance.SMTPConfiguration, and
//	DefaultInstance is read exactly once — when the FIRST instance is created.
//	On any cluster whose instance already exists it configures nothing at all,
//	silently: helm reports success, the ConfigMap carries the block, and the
//	instance has no sender. So the declarative half makes a REBUILT box come up
//	with mail working and does nothing for the box already running.
//
//	This is the other half. It reconciles the same intent through the admin API
//	so an existing instance converges too, and so the result does not depend on
//	whether someone remembered to run a one-off curl.
//
// WHY IT MATTERS MORE THAN DELIVERABILITY
//
//	Without a sender the issuer cannot run its own initialisation or
//	password-reset flows (ADR-060, Amendment 2026-09-26). The platform then has
//	to invent a password for every provisioned owner, and a generated credential
//	that reaches no one is not a credential. A tenant owner created 2026-09-22
//	was still holding its bootstrap hash four days later with exactly one
//	successful password check in its entire history. Mail is what removes the
//	need for that password to exist.
//
// SCOPE
//
//	Instance-level, so no org header: SMTP belongs to the instance, and sending
//	it with x-zitadel-orgid would scope the write to an organisation that does
//	not own the setting.
type SMTPConfig struct {
	// Host MUST include the port — "smtp.resend.com:465". ZITADEL passes it
	// straight to net.Dial, so a bare hostname fails at connect time with a
	// message about the address rather than about the config.
	Host string
	// User and Password are the provider's SMTP credentials. For Resend that is
	// the literal username "resend" and an API key as the password.
	User     string
	Password string
	// TLS selects implicit TLS (tls.Dial) over plaintext. ZITADEL falls back to
	// STARTTLS on its own when the server answers in the clear, so true is
	// correct for both :465 and :587 and false is correct only for a relay that
	// offers neither.
	TLS bool
	// SenderAddress must sit on the instance's own domain unless
	// DomainPolicy.SMTPSenderAddressMatchesInstanceDomain has been relaxed. Using
	// an address on the instance domain removes the need to relax it.
	SenderAddress string
	SenderName    string
	ReplyTo       string
}

func (c SMTPConfig) validate() error {
	var missing []string
	if c.Host == "" {
		missing = append(missing, "host")
	} else if !strings.Contains(c.Host, ":") {
		return fmt.Errorf("zitadel: smtp host %q must include the port, e.g. smtp.resend.com:465", c.Host)
	}
	if c.Password == "" {
		missing = append(missing, "password")
	}
	if c.SenderAddress == "" {
		missing = append(missing, "senderAddress")
	}
	if len(missing) > 0 {
		return fmt.Errorf("zitadel: smtp config incomplete: %s", strings.Join(missing, ", "))
	}
	return nil
}

// smtpProviderDescription identifies the config this reconciler owns.
//
// ZITADEL allows several SMTP providers per instance and addresses them by an
// allocated id, not by host. Without a stable marker a second reconcile cannot
// tell "my config, drifted" from "someone else's config", and the safe reading of
// that ambiguity is to add another one — which is how an instance ends up with
// four senders and no way to know which is active. The description is the marker.
const smtpProviderDescription = "managed-by-kube-sbt"

type smtpProvider struct {
	ID            string `json:"id"`
	Description   string `json:"description"`
	Host          string `json:"host"`
	SenderAddress string `json:"senderAddress"`
	State         string `json:"state"`
}

// EnsureSMTP makes the instance's SMTP configuration match cfg.
//
// Idempotent by construction: it looks for the provider carrying this
// reconciler's description and updates it, creating one only when absent. The
// password is written on every pass because ZITADEL never returns it, so there is
// nothing to compare it against — a rotated API key would otherwise be accepted
// into the desired state and never reach the instance.
func (a *Auth) EnsureSMTP(ctx context.Context, cfg SMTPConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}

	existing, err := a.findManagedSMTP(ctx)
	if err != nil {
		return err
	}

	body := map[string]any{
		"senderAddress":  cfg.SenderAddress,
		"senderName":     cfg.SenderName,
		"tls":            cfg.TLS,
		"host":           cfg.Host,
		"user":           cfg.User,
		"password":       cfg.Password,
		"replyToAddress": cfg.ReplyTo,
		"description":    smtpProviderDescription,
	}

	if existing == nil {
		// AddSMTPConfig. The response carries the allocated id, which is not
		// persisted here: the description is the handle, so the id never has to be
		// remembered across processes.
		if err := a.api.do(ctx, http.MethodPost, "/admin/v1/smtp", "", body, nil); err != nil {
			return fmt.Errorf("zitadel: add smtp config: %w", err)
		}
		if err := a.activateSMTP(ctx, ""); err != nil {
			return err
		}
		return nil
	}

	body["id"] = existing.ID
	if err := a.api.do(ctx, http.MethodPut, "/admin/v1/smtp/"+existing.ID, "", body, nil); err != nil {
		return fmt.Errorf("zitadel: update smtp config %s: %w", existing.ID, err)
	}

	// A configured provider that is not ACTIVE sends nothing, and the state is
	// not part of the update body — so a config can be perfectly correct and
	// inert. Activating unconditionally is cheap and removes that failure.
	if !strings.EqualFold(existing.State, "SMTP_CONFIG_ACTIVE") {
		if err := a.activateSMTP(ctx, existing.ID); err != nil {
			return err
		}
	}
	return nil
}

// activateSMTP makes a provider the one the instance sends through.
//
// id may be empty immediately after a create, in which case the provider is
// re-read to find the id ZITADEL allocated.
func (a *Auth) activateSMTP(ctx context.Context, id string) error {
	if id == "" {
		found, err := a.findManagedSMTP(ctx)
		if err != nil {
			return err
		}
		if found == nil {
			return errors.New("zitadel: smtp config was created but cannot be found to activate")
		}
		id = found.ID
	}
	if err := a.api.do(ctx, http.MethodPost, "/admin/v1/smtp/"+id+"/_activate", "", map[string]any{}, nil); err != nil {
		// Already active is not a failure. ZITADEL reports it as a precondition
		// error, and treating that as fatal would make every reconcile after the
		// first one fail on a correctly configured instance.
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusBadRequest {
			return nil
		}
		return fmt.Errorf("zitadel: activate smtp config %s: %w", id, err)
	}
	return nil
}

// findManagedSMTP returns the provider this reconciler owns, or nil.
func (a *Auth) findManagedSMTP(ctx context.Context) (*smtpProvider, error) {
	var out struct {
		Result []smtpProvider `json:"result"`
	}
	if err := a.api.do(ctx, http.MethodPost, "/admin/v1/smtp/_search", "", map[string]any{}, &out); err != nil {
		// A fresh instance has no providers and may answer 404 rather than an
		// empty list. That is "none yet", not a failure to look.
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("zitadel: list smtp configs: %w", err)
	}
	for i := range out.Result {
		if out.Result[i].Description == smtpProviderDescription {
			return &out.Result[i], nil
		}
	}
	return nil, nil
}
