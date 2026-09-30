package zitadel

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// oidcSettings is the instance's token lifetimes, as the admin API models them.
//
// ALL FOUR FIELDS, always. The update is a PUT and Zitadel treats it as a full
// replace, so sending one field zeroes the other three -- the same shape as the
// project update that renamed every project it touched until it was made to read
// the current name first (see ensureRoleAssertion). Read, change one, write back.
//
// Durations are protobuf Durations, which marshal as a string of seconds.
type oidcSettings struct {
	AccessTokenLifetime        string `json:"accessTokenLifetime,omitempty"`
	IDTokenLifetime            string `json:"idTokenLifetime,omitempty"`
	RefreshTokenIdleExpiration string `json:"refreshTokenIdleExpiration,omitempty"`
	RefreshTokenExpiration     string `json:"refreshTokenExpiration,omitempty"`
}

const oidcSettingsPath = "/admin/v1/settings/oidc"

// EnsureAccessTokenLifetime sets how long an access token this instance issues
// stays usable (zero-ops ADR-094 requirement 8, ADR-095).
//
// INSTANCE-WIDE, because Zitadel offers nothing narrower: AccessTokenLifetime
// appears only on the admin OIDC settings API, and an application's own config
// carries an access token TYPE but no lifetime. A per-application lifetime is
// therefore not a choice this platform declined -- it is not expressible, and a
// design that assumed one would have to be rewritten rather than configured.
//
// It moves ACCESS tokens only, and that is what makes an aggressive value safe
// here. The two paths on this platform hold different kinds of token: a
// gateway-mediated browser session carries the ID token (ADR-095, "the exchange
// cannot serve a browser session"), and only a cross-application call carries an
// access token. Shortening this therefore bounds the credential that travels
// between applications without shortening anyone's login. IdTokenLifetime is
// read and written back untouched for exactly that reason.
//
// The caller decides the value. Nothing here defaults it, because a default in
// this function would be a security parameter chosen by the file that happened
// to implement the call.
func (b *Bootstrap) EnsureAccessTokenLifetime(ctx context.Context, lifetime time.Duration) error {
	if lifetime <= 0 {
		return fmt.Errorf("access token lifetime must be positive, got %s", lifetime)
	}
	api := b.client()

	// Read first. Absent settings are not an error: an instance that has never
	// had them explicitly set answers 404 here and takes a POST, while one that
	// has takes a PUT. Choosing on the response rather than on a flag means a
	// rebuilt box and an upgraded one follow the same path.
	var current struct {
		Settings oidcSettings `json:"settings"`
	}
	exists := true
	if err := api.do(ctx, http.MethodGet, oidcSettingsPath, "", nil, &current); err != nil {
		exists = false
	}

	want := fmt.Sprintf("%ds", int(lifetime.Seconds()))
	if exists && current.Settings.AccessTokenLifetime == want {
		return nil // already what we asked for; do not rewrite
	}

	body := current.Settings
	body.AccessTokenLifetime = want

	method := http.MethodPut
	if !exists {
		method = http.MethodPost
		// Nothing to preserve, so the issuer's own documented defaults stand in
		// for the three fields we are not deciding. Sending them empty on a
		// create would store zero, which is not "unset" -- it is a token that
		// expires immediately.
		body.IDTokenLifetime = "43200s"              // 12h
		body.RefreshTokenIdleExpiration = "2592000s" // 30d
		body.RefreshTokenExpiration = "7776000s"     // 90d
	}

	if err := api.do(ctx, method, oidcSettingsPath, "", body, nil); err != nil {
		return fmt.Errorf("set instance access token lifetime to %s: %w", lifetime, err)
	}
	return nil
}
