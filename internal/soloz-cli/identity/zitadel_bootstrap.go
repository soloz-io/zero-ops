// Package identity registers the OIDC client a cluster's API server names, and
// destroys the credential it used to do it.
//
// ADR-076 separates two planes. Cluster configuration -- which issuer the API
// server trusts -- is desired state in the tenant's repository. Administering the
// identity provider is a different operation with a different, far more
// privileged credential, and the running box must never hold one: a standing
// management credential on the hub would place the identity provider's
// administration inside the system it authenticates, and let any compromise of
// the box rewrite its own login.
//
// So the credential here is borrowed, not kept. It arrives as an input, is used
// once, and is revoked before this returns -- including when registration fails,
// which is the case that matters, because that is the path where a credential is
// most likely to be left behind.
//
// The v2 services are used deliberately. The older Management API endpoints for
// machine users and personal access tokens are deprecated, and v2 makes the
// property this design depends on structural rather than remembered: on
// `AddPersonalAccessToken` the expiration date is a required field, so a token
// without an expiry cannot be created by accident.
package identity

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

// OIDC application shape for a cluster client, from the v2 enums.
//
// NATIVE with auth method NONE is the public-client-with-PKCE combination. It is
// correct for kubectl for a reason worth stating: the client runs on every
// operator's laptop, so any secret it held would be copied onto each of them and
// could not be revoked for one person -- which is the property this whole ADR
// exists to obtain.
const (
	appTypeNative     = "OIDC_APP_TYPE_NATIVE"
	authMethodNone    = "OIDC_AUTH_METHOD_TYPE_NONE"
	grantAuthCode     = "OIDC_GRANT_TYPE_AUTHORIZATION_CODE"
	responseTypeCode  = "OIDC_RESPONSE_TYPE_CODE"
	createApplication = "/zitadel.application.v2.ApplicationService/CreateApplication"
)

// Bootstrap holds the borrowed credential and what it is for.
type Bootstrap struct {
	// Issuer is the identity provider's base URL.
	Issuer string
	// Token is the personal access token this operation borrows. It is revoked
	// before Run returns.
	Token string
	// UserID owns Token; TokenID identifies it. Both are needed to revoke, and a
	// bootstrap that cannot revoke is refused rather than run.
	UserID  string
	TokenID string
	// ProjectID receives the application.
	ProjectID string

	HTTP *http.Client
}

// Result is what survives the operation: public metadata, and nothing else.
type Result struct {
	ClientID      string
	ApplicationID string
}

// Run registers the application and revokes the credential.
//
// Revocation is deferred, so it happens on every path including a failed
// registration and a cancelled context. Its error is reported even when
// registration succeeded: a run that leaves a live management token behind has
// not done what it was asked, however good the application looks.
func (b *Bootstrap) Run(ctx context.Context, appName string, redirectURIs []string) (res Result, err error) {
	if err := b.validate(); err != nil {
		return Result{}, err
	}

	defer func() {
		// Revoked with a context of its own. The caller's may already be
		// cancelled -- which is exactly when a token would otherwise survive.
		revokeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if rerr := b.revoke(revokeCtx); rerr != nil {
			if err == nil {
				err = fmt.Errorf("the application was registered but its bootstrap token could not be revoked: %w.\n\n"+
					"Revoke it in the identity provider before this box is considered built:\n"+
					"  user %s, token %s", rerr, b.UserID, b.TokenID)
				return
			}
			err = fmt.Errorf("%w (and the bootstrap token could not be revoked either: %v)", err, rerr)
		}
	}()

	return b.register(ctx, appName, redirectURIs)
}

func (b *Bootstrap) validate() error {
	var missing []string
	for name, v := range map[string]string{
		"issuer": b.Issuer, "token": b.Token,
		"user id": b.UserID, "token id": b.TokenID, "project id": b.ProjectID,
	} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		// The token's identifiers are as required as the token. Without them the
		// credential cannot be revoked, and an unrevokable management token is
		// the one outcome this package exists to prevent -- so it refuses to
		// start rather than discovering it at the end.
		return fmt.Errorf("cannot bootstrap the cluster's OIDC client: missing %s", strings.Join(missing, ", "))
	}
	return nil
}

func (b *Bootstrap) register(ctx context.Context, appName string, redirectURIs []string) (Result, error) {
	body := map[string]any{
		"projectId": b.ProjectID,
		"name":      appName,
		"oidcRequest": map[string]any{
			"redirectUris":             redirectURIs,
			"responseTypes":            []string{responseTypeCode},
			"grantTypes":               []string{grantAuthCode},
			"appType":                  appTypeNative,
			"authMethodType":           authMethodNone,
			"devMode":                  false,
			"skipNativeAppSuccessPage": true,
		},
	}

	var out struct {
		ApplicationID     string `json:"applicationId"`
		OIDCConfiguration struct {
			ClientID string `json:"clientId"`
		} `json:"oidcConfiguration"`
	}
	if err := b.do(ctx, http.MethodPost, createApplication, body, &out); err != nil {
		return Result{}, fmt.Errorf("register the cluster's OIDC client: %w", err)
	}
	if out.OIDCConfiguration.ClientID == "" {
		// The client id is the only thing this operation produces. Returning
		// success without one would record an empty value in the topology, and
		// the API server would come up trusting an issuer for a client that does
		// not exist -- which fails every login rather than failing here.
		return Result{}, fmt.Errorf("the identity provider created application %q and returned no client id",
			out.ApplicationID)
	}
	return Result{ClientID: out.OIDCConfiguration.ClientID, ApplicationID: out.ApplicationID}, nil
}

// revoke removes the borrowed token. The service account is deliberately left in
// place: it is inert without a credential, and deleting it would also remove the
// record of what registered the application.
func (b *Bootstrap) revoke(ctx context.Context) error {
	path := fmt.Sprintf("/v2/users/%s/pats/%s", b.UserID, b.TokenID)
	return b.do(ctx, http.MethodDelete, path, nil, nil)
}

func (b *Bootstrap) do(ctx context.Context, method, path string, in, out any) error {
	var rdr io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(b.Issuer, "/")+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.Token)
	req.Header.Set("Content-Type", "application/json")

	client := b.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The body is included because the identity provider explains refusals
		// there -- a missing permission reads as a bare 403 otherwise, and the
		// fix (a role the service account lacks) is not guessable from the code.
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(payload)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(payload, out)
}
