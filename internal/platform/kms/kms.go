// Package kms provisions the Google Cloud KMS resources ADR-100 depends on.
//
// WHY THIS IS A CLI COMMAND AND NOT A RUNBOOK, which is the whole point of the
// package: a key that is created by hand is created differently each time. The
// rotation period gets forgotten, the IAM binding lands at the project instead of the
// key, the service account acquires a JSON key "just for testing", and none of it is
// visible until an audit or an incident. ADR-100's least-privilege claim is only as
// good as the policy somebody actually applied, and the way to make a claim like that
// hold is to stop a human from being the thing that applies it.
//
// So this is the escrow pattern (internal/platform/escrow/provision.go): the account
// is irreducibly manual, everything after it is not, and every Ensure* below is
// idempotent so re-running is a no-op that CONFIRMS state rather than a second attempt
// at creating it.
//
// WHERE THIS SITS IN THE DELIVERY MODEL. ADR-100 "Declarative delivery" decides that
// Crossplane on the hub reconciles these resources and that bootstrap is irreducibly
// first -- no reconciler can create the key the cluster it runs in depends on. This is
// that bootstrap. It is not a substitute for the reconciler: it establishes the
// resources and writes their names down, and Crossplane adopts them afterwards under
// Observe/Update.
//
// WHY REST AND NOT THE GENERATED CLIENT. Adding cloud.google.com/go/kms to this module
// upgraded grpc 1.79->1.83 and google.golang.org/api 0.247->0.287 across every other
// package in it. go.mod already records what that class of change costs here: pinning
// one dependency for one correct component downgraded every other operator. The KMS
// admin, IAM and Service Usage APIs are ordinary JSON over HTTPS, which is how this CLI
// already talks to Infisical, Hetzner and GitHub. The one dependency added is
// Application Default Credentials, because reimplementing credential discovery is
// exactly the kind of thing that works until it does not.
//
// The data path is different and stays different: operators/kms-plugin uses the
// generated gRPC client, in its own module, because a wrapped DEK is not a thing to
// hand-roll a protocol for.
package kms

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// The three API hosts. Overridable only for tests -- see newTestClient.
const (
	defaultKMSHost          = "https://cloudkms.googleapis.com"
	defaultIAMHost          = "https://iam.googleapis.com"
	defaultServiceUsageHost = "https://serviceusage.googleapis.com"
)

// scope is the single OAuth scope these calls need.
//
// cloud-platform rather than a narrower one because there is no narrower one for KMS
// administration. That is a property of the OPERATOR's credential, not of the plugin's:
// the plugin never authenticates with this, and its identity is scoped at the key. The
// distinction matters enough to say out loud, because "the KMS provisioner needs
// cloud-platform" reads like ADR-100's least-privilege claim being quietly broken.
const scope = "https://www.googleapis.com/auth/cloud-platform"

// Client talks to the three Google APIs this package needs.
type Client struct {
	http      *http.Client
	projectID string
	// projectNumber is required by Service Usage, which does not accept a project id
	// on the enable path. Empty is allowed: EnsureAPIEnabled then reports what to run
	// rather than guessing a number.
	projectNumber string

	kmsHost          string
	iamHost          string
	serviceUsageHost string
}

// projectIDPattern is Google's: 6-30 chars, lowercase letter first, letters digits and
// hyphens, not ending in a hyphen. Checked rather than trusted, because a typo here
// produces a 403 that reads as a permission problem and sends the reader to IAM.
var projectIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// New builds a Client from Application Default Credentials.
//
// ADC and not a service-account key file, deliberately and in both directions: ADR-100
// prohibits a long-lived service-account key for the PLUGIN, and the same reasoning
// applies to the operator running this -- `gcloud auth application-default login`
// produces a short-lived credential tied to a human, which is what an action that
// creates a cluster's root encryption key should require.
//
// It also accepts an impersonation target, which is how the scoped identity is tested
// without Workload Identity Federation existing yet: the operator holds
// roles/iam.serviceAccountTokenCreator on the plugin's service account and assumes it.
// Nothing is stored.
func New(ctx context.Context, projectID, projectNumber, impersonate string) (*Client, error) {
	if !projectIDPattern.MatchString(projectID) {
		return nil, fmt.Errorf("%q is not a Google project id (6-30 chars, starts with a "+
			"lowercase letter, letters digits and hyphens, does not end in a hyphen). A wrong "+
			"id answers 403, which reads as a permissions problem and sends you to IAM",
			projectID)
	}

	var ts oauth2.TokenSource
	creds, err := google.FindDefaultCredentials(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("no Application Default Credentials: %w\n\n"+
			"Run `gcloud auth application-default login`. A service-account key file is not "+
			"an acceptable substitute here (ADR-100)", err)
	}
	ts = creds.TokenSource

	if impersonate != "" {
		ts, err = impersonatedTokenSource(ctx, ts, impersonate)
		if err != nil {
			return nil, err
		}
	}

	return &Client{
		http:             oauth2.NewClient(ctx, ts),
		projectID:        projectID,
		projectNumber:    projectNumber,
		kmsHost:          defaultKMSHost,
		iamHost:          defaultIAMHost,
		serviceUsageHost: defaultServiceUsageHost,
	}, nil
}

// ProjectID is the project every resource below is created in.
func (c *Client) ProjectID() string { return c.projectID }

// KeyRingName and CryptoKeyName build the resource names Google uses. They are the
// values that end up in KMS_CRYPTO_KEY and in the reported key_id, so they are built in
// ONE place rather than formatted at each call site.
func (c *Client) KeyRingName(location, keyRing string) string {
	return fmt.Sprintf("projects/%s/locations/%s/keyRings/%s", c.projectID, location, keyRing)
}

func (c *Client) CryptoKeyName(location, keyRing, key string) string {
	return c.KeyRingName(location, keyRing) + "/cryptoKeys/" + key
}

// ServiceAccountEmail is the plugin identity's address.
func (c *Client) ServiceAccountEmail(accountID string) string {
	return fmt.Sprintf("%s@%s.iam.gserviceaccount.com", accountID, c.projectID)
}

// googleError is the error envelope every one of these APIs returns.
type googleError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// apiError carries the HTTP status so callers can tell ALREADY_EXISTS from a real
// failure without string-matching a message.
type apiError struct {
	status  int
	gstatus string
	message string
	url     string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s (HTTP %d %s): %s", e.url, e.status, e.gstatus, e.message)
}

// alreadyExists reports whether err is Google's "this resource is already there".
//
// THE WHOLE IDEMPOTENCY STORY RESTS ON THIS, so it checks the structured status and the
// HTTP code rather than the message text. A provisioner that treats a 409 as a failure
// cannot be re-run, and a provisioner that cannot be re-run is one people stop running.
func alreadyExists(err error) bool {
	var ae *apiError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.status == http.StatusConflict || ae.gstatus == "ALREADY_EXISTS"
}

// IsPermissionDenied reports whether err is a 403.
//
// Exported because the negative half of ADR-100's acceptance evidence is made of these:
// a scoped identity must be DENIED cryptoKeys.get, denied a different key, and denied
// every administrative call. A test that cannot tell 403 from 500 proves nothing.
func IsPermissionDenied(err error) bool {
	var ae *apiError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.status == http.StatusForbidden || ae.gstatus == "PERMISSION_DENIED"
}

// IsBillingDisabled reports whether err is Cloud KMS refusing because the project has
// no active billing account.
//
// IT GETS ITS OWN HELPER BECAUSE IT IS NOT A PERMISSIONS PROBLEM AND READS LIKE ONE.
// Google answers FAILED_PRECONDITION, which lands among a dozen other preconditions,
// and the operator's instinct on any 4xx from a cloud API is to go looking at IAM. It
// is also not fixable by anything in this repository: a billing account has to be
// linked, and an account that exists but is CLOSED does not count -- which is the state
// the first real project was found in.
func IsBillingDisabled(err error) bool {
	var ae *apiError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.gstatus == "FAILED_PRECONDITION" &&
		strings.Contains(strings.ToLower(ae.message), "billing")
}

// NotFound reports whether err is a 404.
func NotFound(err error) bool {
	var ae *apiError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.status == http.StatusNotFound || ae.gstatus == "NOT_FOUND"
}

// call performs one JSON request. out may be nil when the response is not needed.
func (c *Client) call(ctx context.Context, method, url string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encoding the request for %s: %w", url, err)
		}
		body = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("building the request for %s: %w", url, err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("reading the response from %s: %w", url, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var ge googleError
		_ = json.Unmarshal(raw, &ge)
		msg := strings.TrimSpace(ge.Error.Message)
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return &apiError{
			status:  resp.StatusCode,
			gstatus: ge.Error.Status,
			message: msg,
			url:     url,
		}
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decoding the response from %s: %w", url, err)
	}
	return nil
}

// impersonatedTokenSource exchanges the operator's token for the service account's.
//
// THIS IS WHAT MAKES THE LEAST-PRIVILEGE CLAIM TESTABLE BEFORE GATE 3 EXISTS, and it is
// worth being explicit about why it is needed at all. The operator running this is
// almost certainly a project owner, so a negative-permission test run as them denies
// NOTHING -- every assertion passes and the test is worthless. The claim is about the
// PLUGIN's identity, so the test has to be the plugin's identity.
//
// Impersonation gives exactly that, with no key file and nothing persisted: the operator
// holds roles/iam.serviceAccountTokenCreator on the account and asks IAM Credentials for
// a short-lived access token. When Workload Identity Federation lands, it changes only
// HOW this same account is assumed on a node.
func impersonatedTokenSource(ctx context.Context, base oauth2.TokenSource, email string) (oauth2.TokenSource, error) {
	if !strings.Contains(email, "@") || !strings.HasSuffix(email, ".iam.gserviceaccount.com") {
		return nil, fmt.Errorf("--impersonate %q is not a service account email; expected "+
			"NAME@PROJECT.iam.gserviceaccount.com", email)
	}
	return oauth2.ReuseTokenSource(nil, &impersonator{
		ctx:   ctx,
		base:  oauth2.NewClient(ctx, base),
		email: email,
	}), nil
}

type impersonator struct {
	ctx   context.Context
	base  *http.Client
	email string
}

func (i *impersonator) Token() (*oauth2.Token, error) {
	url := fmt.Sprintf(
		"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/%s:generateAccessToken",
		i.email)
	in := map[string]any{"scope": []string{scope}, "lifetime": "3600s"}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(i.ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := i.base.Do(req)
	if err != nil {
		return nil, fmt.Errorf("impersonating %s: %w", i.email, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var ge googleError
		_ = json.Unmarshal(body, &ge)
		return nil, fmt.Errorf("impersonating %s failed (HTTP %d %s): %s. The caller needs "+
			"roles/iam.serviceAccountTokenCreator on that account -- `soloz kms init "+
			"--grant-impersonation-to` grants it",
			i.email, resp.StatusCode, ge.Error.Status, strings.TrimSpace(ge.Error.Message))
	}

	var out struct {
		AccessToken string `json:"accessToken"`
		ExpireTime  string `json:"expireTime"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	exp, err := time.Parse(time.RFC3339, out.ExpireTime)
	if err != nil {
		// A token with no expiry would be reused forever by ReuseTokenSource.
		return nil, fmt.Errorf("impersonation returned an unparseable expiry %q: %w",
			out.ExpireTime, err)
	}
	return &oauth2.Token{AccessToken: out.AccessToken, Expiry: exp, TokenType: "Bearer"}, nil
}
