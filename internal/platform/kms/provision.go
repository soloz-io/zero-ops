package kms

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/soloz-io/zero-ops/internal/assets"
)

// RoleEncrypterDecrypter is the ONE role the plugin's identity holds.
//
// It carries cryptoKeyVersions.useToEncrypt and useToDecrypt and nothing else -- in
// particular NOT cloudkms.cryptoKeys.get, which is why the plugin discovers its active
// key version by encrypting against the parent key rather than by reading
// CryptoKey.Primary. An earlier design did read it, and this ADR claimed this role
// covered that: it does not, and the policy described would not have worked.
const RoleEncrypterDecrypter = "roles/cloudkms.cryptoKeyEncrypterDecrypter"

// roleTokenCreator lets a principal mint short-lived tokens for a service account.
const roleTokenCreator = "roles/iam.serviceAccountTokenCreator"

// DefaultRotationPeriod is ADR-100's ceiling, not a suggestion.
//
// "At most 90 days, because the API server retains data keys in memory and a key's
// exposure window is otherwise unbounded." Set at creation so the key cannot exist in a
// state where nobody chose a rotation period -- which is the state every hand-created
// key starts in.
//
// IT IS THE SAME CONSTANT THE SECRETBOX PATH USES, not a matching number. The two
// providers protect the same data for the same reason; two cryptoperiods would let the
// weaker control legitimately outlive the stronger one. assets.EncryptionRotationWindow
// carries the reasoning and what the frameworks do and do not actually mandate.
const DefaultRotationPeriod = assets.EncryptionRotationWindow

// DefaultDestroyScheduledDuration is how long a destroy stays pending and recoverable.
//
// THIRTY DAYS, AND LONGER IS BETTER HERE THAN SHORTER. Destroying a key version makes
// every backup taken under it unreadable, which ADR-100 calls the same loss as losing
// the key. The pending window is the only thing between a mistaken destroy and that
// loss, so it is set explicitly rather than left to whatever the API defaults to.
const DefaultDestroyScheduledDuration = 30 * 24 * time.Hour

// KeySpec is one cluster's key, fully described.
//
// Every field that affects the key's security properties is here and none of them is
// optional at the API: a key created without a rotation period is a key nobody decided
// the rotation period for.
type KeySpec struct {
	// Location is the GCP location. ADR-100 proposes europe-west3 (Frankfurt),
	// regional: closest to the Hetzner sites, and keeps residency in the EU with the
	// clusters whose Secrets it protects.
	Location string
	// KeyRing groups a platform's keys. One ring, many keys.
	KeyRing string
	// Key is the CryptoKey id. ADR-100: one key per cluster, never copied.
	Key string

	RotationPeriod           time.Duration
	DestroyScheduledDuration time.Duration
}

func (s KeySpec) withDefaults() KeySpec {
	if s.RotationPeriod == 0 {
		s.RotationPeriod = DefaultRotationPeriod
	}
	if s.DestroyScheduledDuration == 0 {
		s.DestroyScheduledDuration = DefaultDestroyScheduledDuration
	}
	return s
}

// seconds renders a duration the way these APIs want it: "7776000s".
func seconds(d time.Duration) string {
	return fmt.Sprintf("%ds", int64(d.Seconds()))
}

// EnsureAPIEnabled turns on cloudkms.googleapis.com.
//
// IT REPORTS RATHER THAN GUESSES when it cannot. Service Usage addresses projects by
// NUMBER, not id, and enabling an API is a project-level mutation the operator may
// legitimately not be allowed to make. Both cases print the single command to run
// instead of failing with a 403 the reader has to decode.
func (c *Client) EnsureAPIEnabled(ctx context.Context) error {
	const service = "cloudkms.googleapis.com"
	if c.projectNumber == "" {
		return fmt.Errorf("the project NUMBER is required to enable %s (Service Usage does "+
			"not accept a project id on that path) and was not supplied. Either pass "+
			"--project-number, or enable it once with:\n\n  gcloud services enable %s "+
			"--project %s\n", service, service, c.projectID)
	}
	u := fmt.Sprintf("%s/v1/projects/%s/services/%s", c.serviceUsageHost, c.projectNumber, service)

	// Read first. Enabling an already-enabled service is a long-running operation that
	// succeeds, but checking is one cheap call and makes the common re-run silent.
	var state struct {
		State string `json:"state"`
	}
	if err := c.call(ctx, http.MethodGet, u, nil, &state); err == nil && state.State == "ENABLED" {
		return nil
	} else if err != nil && IsPermissionDenied(err) {
		return fmt.Errorf("cannot read whether %s is enabled on %s: %w\n\n"+
			"Someone with serviceusage.services.enable must run:\n\n"+
			"  gcloud services enable %s --project %s\n",
			service, c.projectID, err, service, c.projectID)
	}

	if err := c.call(ctx, http.MethodPost, u+":enable", map[string]any{}, nil); err != nil {
		return fmt.Errorf("enabling %s on %s: %w\n\n"+
			"If this is a permissions problem, someone with serviceusage.services.enable "+
			"must run:\n\n  gcloud services enable %s --project %s\n",
			service, c.projectID, err, service, c.projectID)
	}
	return nil
}

// EnsureKeyRing creates the key ring if it is absent, and is a no-op if it is present.
//
// A key ring cannot be deleted, ever, and that is a property rather than a limitation:
// the thing that holds a cluster's root encryption key should not be removable by a
// mistaken `delete`.
func (c *Client) EnsureKeyRing(ctx context.Context, location, keyRing string) (string, error) {
	name := c.KeyRingName(location, keyRing)
	parent := fmt.Sprintf("projects/%s/locations/%s", c.projectID, location)
	u := fmt.Sprintf("%s/v1/%s/keyRings?keyRingId=%s",
		c.kmsHost, parent, url.QueryEscape(keyRing))

	err := c.call(ctx, http.MethodPost, u, map[string]any{}, nil)
	if err == nil || alreadyExists(err) {
		return name, nil
	}
	return "", fmt.Errorf("creating key ring %s: %w", name, err)
}

// EnsureCryptoKey creates the key if it is absent, and VERIFIES it if it is present.
//
// VERIFIES RATHER THAN UPDATES, deliberately. This is bootstrap; ADR-100 puts ongoing
// reconciliation in Crossplane, which holds the rotation period and destroy protection
// to desired state afterwards. A bootstrap command that silently rewrote the properties
// of a key already holding data would be a second, competing reconciler -- and the
// failure mode of two reconcilers disagreeing about a key is not one worth having on
// this particular resource.
//
// So drift is REPORTED, naming the field and both values, and the caller decides.
func (c *Client) EnsureCryptoKey(ctx context.Context, spec KeySpec) (string, error) {
	spec = spec.withDefaults()
	name := c.CryptoKeyName(spec.Location, spec.KeyRing, spec.Key)
	parent := c.KeyRingName(spec.Location, spec.KeyRing)

	body := map[string]any{
		"purpose": "ENCRYPT_DECRYPT",
		"versionTemplate": map[string]any{
			"algorithm":       "GOOGLE_SYMMETRIC_ENCRYPTION",
			"protectionLevel": "SOFTWARE",
		},
		"rotationPeriod":           seconds(spec.RotationPeriod),
		"destroyScheduledDuration": seconds(spec.DestroyScheduledDuration),
		// nextRotationTime and rotationPeriod are set together. The API accepts a
		// rotation period alone and computes this, but stating it means the first
		// rotation is at a time somebody chose rather than at whatever the creation
		// moment implied.
		"nextRotationTime": time.Now().UTC().Add(spec.RotationPeriod).Format(time.RFC3339),
	}
	u := fmt.Sprintf("%s/v1/%s/cryptoKeys?cryptoKeyId=%s",
		c.kmsHost, parent, url.QueryEscape(spec.Key))

	err := c.call(ctx, http.MethodPost, u, body, nil)
	if err == nil {
		return name, nil
	}
	if !alreadyExists(err) {
		return "", fmt.Errorf("creating crypto key %s: %w", name, err)
	}

	// It exists. Confirm it is the key this spec describes.
	got, gerr := c.GetCryptoKey(ctx, name)
	if gerr != nil {
		return "", fmt.Errorf("crypto key %s exists but could not be read back: %w", name, gerr)
	}
	var drift []string
	if got.Purpose != "ENCRYPT_DECRYPT" {
		drift = append(drift, fmt.Sprintf("purpose is %q, want ENCRYPT_DECRYPT (fixed at "+
			"creation and not changeable)", got.Purpose))
	}
	if want := seconds(spec.RotationPeriod); got.RotationPeriod != want {
		drift = append(drift, fmt.Sprintf("rotationPeriod is %q, want %q",
			got.RotationPeriod, want))
	}
	if want := seconds(spec.DestroyScheduledDuration); got.DestroyScheduledDuration != want {
		drift = append(drift, fmt.Sprintf("destroyScheduledDuration is %q, want %q",
			got.DestroyScheduledDuration, want))
	}
	if len(drift) > 0 {
		return name, fmt.Errorf("crypto key %s already exists and does not match: %s.\n\n"+
			"Nothing was changed. This command establishes a key; it does not rewrite the "+
			"properties of one that may already hold data -- ADR-100 puts that in Crossplane. "+
			"Fix it in the reconciler's desired state, or destroy nothing and decide "+
			"deliberately", name, strings.Join(drift, "; "))
	}
	return name, nil
}

// CryptoKey is the subset of the resource this package reads back.
type CryptoKey struct {
	Name                     string `json:"name"`
	Purpose                  string `json:"purpose"`
	RotationPeriod           string `json:"rotationPeriod"`
	DestroyScheduledDuration string `json:"destroyScheduledDuration"`
	Primary                  struct {
		Name  string `json:"name"`
		State string `json:"state"`
	} `json:"primary"`
}

// GetCryptoKey reads a key. The OPERATOR may call this; the plugin's identity may not,
// and a test asserts that it cannot.
func (c *Client) GetCryptoKey(ctx context.Context, name string) (*CryptoKey, error) {
	var out CryptoKey
	if err := c.call(ctx, http.MethodGet, c.kmsHost+"/v1/"+name, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EnsureServiceAccount creates the plugin's identity.
//
// AND CREATES NO KEY FOR IT. ADR-100 prohibits a long-lived service-account key
// outright, so there is no flag here to produce one -- not because it would be
// inconvenient to offer, but because an option that exists gets used at 2am. The
// account is assumed: by impersonation today, by Workload Identity Federation once gate
// 3 decides how a static pod presents an external identity.
func (c *Client) EnsureServiceAccount(ctx context.Context, accountID, displayName string) (string, error) {
	email := c.ServiceAccountEmail(accountID)
	u := fmt.Sprintf("%s/v1/projects/%s/serviceAccounts", c.iamHost, c.projectID)
	body := map[string]any{
		"accountId": accountID,
		"serviceAccount": map[string]any{
			"displayName": displayName,
			"description": "ADR-100 KMS v2 plugin. Holds " + RoleEncrypterDecrypter +
				" on ONE CryptoKey and nothing else. No key file: assumed by federation.",
		},
	}
	err := c.call(ctx, http.MethodPost, u, body, nil)
	if err == nil || alreadyExists(err) {
		return email, nil
	}
	return "", fmt.Errorf("creating service account %s: %w", email, err)
}

// iamPolicy is the shape both setIamPolicy surfaces use.
type iamPolicy struct {
	Version  int          `json:"version,omitempty"`
	Bindings []iamBinding `json:"bindings,omitempty"`
	Etag     string       `json:"etag,omitempty"`
}

type iamBinding struct {
	Role    string   `json:"role"`
	Members []string `json:"members"`
}

// EnsureKeyBinding grants exactly one role to exactly one member, AT THE KEY.
//
// AT THE KEY AND NOT AT THE PROJECT, which is the whole claim. A project-level binding
// grants the plugin encrypt and decrypt on every key in the project, which is not least
// privilege and is the single easiest thing to get wrong by hand -- the project is what
// the console offers first.
//
// READ-MODIFY-WRITE WITH THE ETAG, because a blind setIamPolicy replaces the policy
// wholesale. Two concurrent runs, or one run against a key that already has another
// legitimate binding, would silently drop it. The etag makes a lost update a failed
// call instead.
func (c *Client) EnsureKeyBinding(ctx context.Context, keyName, role, member string) error {
	base := c.kmsHost + "/v1/" + keyName

	var policy iamPolicy
	if err := c.call(ctx, http.MethodGet, base+":getIamPolicy?options.requestedPolicyVersion=3",
		nil, &policy); err != nil {
		return fmt.Errorf("reading the IAM policy of %s: %w", keyName, err)
	}

	for i, b := range policy.Bindings {
		if b.Role != role {
			continue
		}
		for _, m := range b.Members {
			if m == member {
				return nil // already bound; a re-run is a no-op
			}
		}
		policy.Bindings[i].Members = append(b.Members, member)
		return c.setKeyPolicy(ctx, keyName, policy)
	}
	policy.Bindings = append(policy.Bindings, iamBinding{Role: role, Members: []string{member}})
	return c.setKeyPolicy(ctx, keyName, policy)
}

func (c *Client) setKeyPolicy(ctx context.Context, keyName string, policy iamPolicy) error {
	err := c.call(ctx, http.MethodPost, c.kmsHost+"/v1/"+keyName+":setIamPolicy",
		map[string]any{"policy": policy}, nil)
	if err != nil {
		return fmt.Errorf("setting the IAM policy of %s: %w", keyName, err)
	}
	return nil
}

// KeyBindings reports the key's policy as role -> members.
//
// This is the object ADR-100's least-privilege claim is about, so it is readable by a
// command rather than only by a console. `soloz kms verify` prints it.
func (c *Client) KeyBindings(ctx context.Context, keyName string) (map[string][]string, error) {
	var policy iamPolicy
	if err := c.call(ctx, http.MethodGet, c.kmsHost+"/v1/"+keyName+":getIamPolicy?options.requestedPolicyVersion=3",
		nil, &policy); err != nil {
		return nil, fmt.Errorf("reading the IAM policy of %s: %w", keyName, err)
	}
	out := map[string][]string{}
	for _, b := range policy.Bindings {
		out[b.Role] = append(out[b.Role], b.Members...)
	}
	return out, nil
}

// EnsureImpersonation lets a principal mint tokens for the service account.
//
// THIS IS AN ESCALATION PATH AND IS THEREFORE OPT-IN, never part of a default run. It
// exists for one reason: the least-privilege claim cannot be tested by an operator who
// is a project owner, because nothing would be denied. Granting the operator
// impersonation on the plugin's account lets the negative tests run AS the scoped
// identity, with no key file and nothing stored.
//
// It should be revoked when the evidence has been collected. `soloz kms verify` lists
// who holds it so that is a visible decision rather than a forgotten one.
func (c *Client) EnsureImpersonation(ctx context.Context, saEmail, member string) error {
	resource := fmt.Sprintf("projects/%s/serviceAccounts/%s", c.projectID, saEmail)
	base := fmt.Sprintf("%s/v1/%s", c.iamHost, resource)

	var policy iamPolicy
	if err := c.call(ctx, http.MethodPost, base+":getIamPolicy", map[string]any{}, &policy); err != nil {
		return fmt.Errorf("reading the IAM policy of %s: %w", saEmail, err)
	}
	for i, b := range policy.Bindings {
		if b.Role != roleTokenCreator {
			continue
		}
		for _, m := range b.Members {
			if m == member {
				return nil
			}
		}
		policy.Bindings[i].Members = append(b.Members, member)
		return c.setSAPolicy(ctx, base, policy)
	}
	policy.Bindings = append(policy.Bindings,
		iamBinding{Role: roleTokenCreator, Members: []string{member}})
	return c.setSAPolicy(ctx, base, policy)
}

func (c *Client) setSAPolicy(ctx context.Context, base string, policy iamPolicy) error {
	if err := c.call(ctx, http.MethodPost, base+":setIamPolicy",
		map[string]any{"policy": policy}, nil); err != nil {
		return fmt.Errorf("setting the service account IAM policy: %w", err)
	}
	return nil
}

// ServiceAccountBindings reports who can act as the service account.
func (c *Client) ServiceAccountBindings(ctx context.Context, saEmail string) (map[string][]string, error) {
	base := fmt.Sprintf("%s/v1/projects/%s/serviceAccounts/%s", c.iamHost, c.projectID, saEmail)
	var policy iamPolicy
	if err := c.call(ctx, http.MethodPost, base+":getIamPolicy", map[string]any{}, &policy); err != nil {
		return nil, fmt.Errorf("reading the IAM policy of %s: %w", saEmail, err)
	}
	out := map[string][]string{}
	for _, b := range policy.Bindings {
		out[b.Role] = append(out[b.Role], b.Members...)
	}
	return out, nil
}

// Provisioned is everything one run established, and is what gets written down.
type Provisioned struct {
	ProjectID    string
	KeyRing      string // full resource name
	CryptoKey    string // full resource name -- this is KMS_CRYPTO_KEY
	DenyProbeKey string // full resource name, or "" when not requested
	// CanaryKey carries the IDENTICAL IAM binding to the live key and holds no data.
	// Administrative denials are proven against it; see ProveLeastPrivilege.
	CanaryKey      string
	ServiceAccount string // email
}

// ProvisionRequest is one cluster's worth of provisioning.
type ProvisionRequest struct {
	Spec KeySpec
	// ServiceAccountID is the plugin identity, per cluster so a revocation is scoped
	// to one cluster the way the key is.
	ServiceAccountID string
	// ImpersonationMember, when set, may assume the service account. See
	// EnsureImpersonation.
	ImpersonationMember string
	// WithDenyProbe creates a SECOND key in the same ring that the service account is
	// deliberately NOT bound to.
	//
	// It exists so cross-key denial can be ASSERTED rather than assumed. "The identity
	// is scoped to one key" is not evidence; "this identity is denied on a key in the
	// same ring, and here is the 403" is. Without a second key there is nothing to be
	// denied on, and that half of ADR-100's acceptance evidence cannot be collected.
	WithDenyProbe bool
	// WithCanary creates a THIRD key that carries the SAME generated binding as the live
	// one -- same service account, same role, at the key -- and holds no data.
	//
	// IT IS NOT THE SAME THING AS THE DENY PROBE and the difference is the whole reason
	// both exist. The deny probe has NO binding, so it proves the identity cannot reach a
	// key it was never granted. The canary has the PRODUCTION binding, so it proves the
	// role itself does not carry rotate, disable, destroy, administer or IAM alteration --
	// denied for the production reason rather than for lack of any access.
	//
	// Administrative denials cannot honestly be proven any other way: attempting them on
	// the live key performs them when the policy is wrong, and testIamPermissions is
	// documented by Google as unsuitable for authorization checking and able to fail open.
	WithCanary bool
	// SkipAPIEnable is for a project where the API is known enabled and the operator
	// lacks serviceusage permission.
	SkipAPIEnable bool
}

// Provision establishes everything, in dependency order, idempotently.
//
// EVERY FAILURE IS RETURNED AND NOTHING IS PARTIALLY REPORTED AS SUCCESS. A half
// provisioned key -- created, unbound -- is worse than none: the plugin starts, fails
// every wrap with a permission error, and the control plane's encryption path is down
// for a reason that looks like a networking problem. Re-running is always safe, so the
// correct response to a failure here is to fix the cause and run it again.
func (c *Client) Provision(ctx context.Context, req ProvisionRequest) (*Provisioned, error) {
	out, err := c.provision(ctx, req)
	if err != nil {
		return nil, c.explain(err)
	}
	return out, nil
}

// explain turns the failures that are NOT this tooling's fault into the action that
// fixes them, because the raw API error sends the reader to the wrong place.
func (c *Client) explain(err error) error {
	if IsBillingDisabled(err) {
		return fmt.Errorf("%w\n\n"+
			"Cloud KMS requires an ACTIVE billing account on project %s before a key ring can\n"+
			"exist. A billing account that is linked but CLOSED does not satisfy this, which is\n"+
			"a state that reads as 'billing is configured' in the console.\n\n"+
			"Nothing was created. Link an active account and re-run this command -- it is\n"+
			"idempotent, so there is nothing to clean up first.", err, c.projectID)
	}
	if IsPermissionDenied(err) {
		return fmt.Errorf("%w\n\n"+
			"This needs permission to CREATE resources in project %s: a key ring, a crypto key,\n"+
			"a service account, and an IAM policy on the key. Confirm the authenticated account\n"+
			"holds roles/owner or an equivalent on that project before re-running.", err, c.projectID)
	}
	return err
}

func (c *Client) provision(ctx context.Context, req ProvisionRequest) (*Provisioned, error) {
	spec := req.Spec.withDefaults()

	if !req.SkipAPIEnable {
		if err := c.EnsureAPIEnabled(ctx); err != nil {
			return nil, err
		}
	}

	ring, err := c.EnsureKeyRing(ctx, spec.Location, spec.KeyRing)
	if err != nil {
		return nil, err
	}

	key, err := c.EnsureCryptoKey(ctx, spec)
	if err != nil {
		return nil, err
	}

	sa, err := c.EnsureServiceAccount(ctx, req.ServiceAccountID,
		"KMS v2 plugin ("+spec.Key+")")
	if err != nil {
		return nil, err
	}
	member := "serviceAccount:" + sa

	if err := c.EnsureKeyBinding(ctx, key, RoleEncrypterDecrypter, member); err != nil {
		return nil, err
	}

	out := &Provisioned{
		ProjectID:      c.projectID,
		KeyRing:        ring,
		CryptoKey:      key,
		ServiceAccount: sa,
	}

	if req.WithDenyProbe {
		probe := spec
		probe.Key = spec.Key + "-deny-probe"
		name, err := c.EnsureCryptoKey(ctx, probe)
		if err != nil {
			return nil, fmt.Errorf("creating the deny-probe key: %w", err)
		}
		// Deliberately NOT bound. That is the entire point of it: it proves the identity
		// cannot reach a key it was never granted.
		out.DenyProbeKey = name
	}

	if req.WithCanary {
		canary := spec
		canary.Key = spec.Key + canarySuffix
		name, err := c.EnsureCryptoKey(ctx, canary)
		if err != nil {
			return nil, fmt.Errorf("creating the canary key: %w", err)
		}
		// BOUND IDENTICALLY TO THE LIVE KEY, and that is what makes it evidence rather
		// than a throwaway. The same service account and the same single role, granted at
		// the key. A refusal on the canary is then a refusal the live key would also
		// produce -- which is what the deny probe cannot establish, because an identity
		// with no binding at all is refused for the wrong reason.
		if err := c.EnsureKeyBinding(ctx, name, RoleEncrypterDecrypter, member); err != nil {
			return nil, fmt.Errorf("binding the canary key identically to the live one: %w", err)
		}
		out.CanaryKey = name
	}

	if req.ImpersonationMember != "" {
		if err := c.EnsureImpersonation(ctx, sa, req.ImpersonationMember); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// KeyIDFor is the CryptoKey id for one cluster.
//
// IN ONE PLACE, because two derivations of this would be a real defect rather than
// duplication. ADR-100 decides one key per cluster, never copied; if `soloz kms init`
// and the bootstrap path computed the key name differently, a cluster would be
// provisioned against one key and bootstrapped against another -- and the second one
// would be created empty, so nothing would fail until a restore.
//
// The cluster name is IN the key name so two clusters cannot be handed the same key by
// a copy-paste without also being handed the same name.
func KeyIDFor(cluster string) string { return "etcd-kek-" + cluster }

// ServiceAccountIDFor is the plugin identity for one cluster, so a revocation is scoped
// to one cluster the way the key is.
//
// Google caps an account id at 30 characters, so a long cluster name is truncated here
// rather than at the API, where it fails with a message about a field nobody passed.
func ServiceAccountIDFor(cluster string) string {
	id := "kms-" + cluster
	if len(id) > 30 {
		id = id[:30]
	}
	return strings.TrimRight(id, "-")
}

// ── The data-path calls, used only to PROVE the IAM policy ─────────────────
//
// These exist for `soloz kms prove` and for nothing else. The plugin does not use
// them: it talks gRPC through the generated client, in its own module, because a
// wrapped DEK is not a thing to hand-roll a protocol for. What they establish is the
// one claim unit tests cannot reach -- that the IAM policy on a real project grants
// exactly what ADR-100 says and denies everything else.

// Encrypt wraps a plaintext under a key or a specific key version, and reports which
// version performed it.
func (c *Client) Encrypt(ctx context.Context, target string, plaintext []byte) (name string, ciphertext string, err error) {
	var out struct {
		Name       string `json:"name"`
		Ciphertext string `json:"ciphertext"`
	}
	in := map[string]any{"plaintext": base64.StdEncoding.EncodeToString(plaintext)}
	if err := c.call(ctx, http.MethodPost, c.kmsHost+"/v1/"+target+":encrypt", in, &out); err != nil {
		return "", "", err
	}
	return out.Name, out.Ciphertext, nil
}

// Decrypt unwraps a ciphertext. Addressed to the PARENT key, as the plugin does: the
// ciphertext embeds its version and Cloud KMS resolves it, which is what lets data
// written before any number of rotations keep decrypting.
func (c *Client) Decrypt(ctx context.Context, keyName, ciphertext string) ([]byte, error) {
	var out struct {
		Plaintext string `json:"plaintext"`
	}
	in := map[string]any{"ciphertext": ciphertext}
	if err := c.call(ctx, http.MethodPost, c.kmsHost+"/v1/"+keyName+":decrypt", in, &out); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(out.Plaintext)
}

// Check is one row of evidence.
type Check struct {
	// What is the claim, phrased so a FAILED row reads as the problem it is.
	What string
	// Want is the expected outcome: "allowed" or "denied".
	Want string
	// Got is what happened.
	Got string
	// OK is whether Got matched Want.
	OK bool
	// Detail carries the status code or the reason, never key material.
	Detail string
}

// ProveLeastPrivilege runs the positive and negative checks that ADR-100's identity
// claim rests on.
//
// IT MUST BE RUN AS THE PLUGIN'S OWN IDENTITY, which is why the Client it is given has
// to be impersonating. Run as a project owner every negative check passes trivially --
// nothing is denied -- and the result is worthless. ProveLeastPrivilege cannot detect
// that on its own, so the caller states it.
//
// The negative rows are the point. "The identity is scoped to one key" is not evidence;
// a 403 on a sibling key in the same ring is.
func (c *Client) ProveLeastPrivilege(ctx context.Context, keyName, canaryKey, denyProbeKey string) []Check {
	var checks []Check

	record := func(what, want string, err error) {
		got := "allowed"
		detail := ""
		switch {
		case err == nil:
		case IsPermissionDenied(err):
			got, detail = "denied", "PERMISSION_DENIED"
		case NotFound(err):
			got, detail = "not-found", "NOT_FOUND"
		default:
			got, detail = "error", err.Error()
		}
		checks = append(checks, Check{
			What: what, Want: want, Got: got, OK: got == want, Detail: detail,
		})
	}

	// ── positive: exactly what the plugin does, in the order it does it ────
	//
	// 1. discover the active version by encrypting against the PARENT
	versionName, _, err := c.Encrypt(ctx, keyName, []byte("kms-plugin probe"))
	record("discover the active version (Encrypt against the parent key)", "allowed", err)

	// 2. wrap against that EXACT version, and 3. unwrap byte-for-byte
	if err == nil && versionName != "" {
		// 32 bytes containing 0x00 and bytes above 0x7f, deliberately: a printable
		// plaintext can survive an encoding mistake and pass a careless check.
		dek := make([]byte, 32)
		for i := range dek {
			dek[i] = byte(i * 7)
		}
		used, ct, werr := c.Encrypt(ctx, versionName, dek)
		record("wrap a data encryption key under that exact version", "allowed", werr)
		if werr == nil {
			if used != versionName {
				checks = append(checks, Check{
					What: "the response names the version that was requested", Want: "allowed",
					Got: "error", Detail: fmt.Sprintf("asked for %s, got %s", versionName, used),
				})
			} else {
				checks = append(checks, Check{
					What: "the response names the version that was requested",
					Want: "allowed", Got: "allowed", OK: true,
				})
			}
			plain, derr := c.Decrypt(ctx, keyName, ct)
			record("unwrap it (Decrypt against the parent key)", "allowed", derr)
			if derr == nil {
				same := len(plain) == len(dek)
				for i := range plain {
					if i < len(dek) && plain[i] != dek[i] {
						same = false
					}
				}
				checks = append(checks, Check{
					What: "the data encryption key survives the round trip byte for byte",
					Want: "allowed",
					Got:  map[bool]string{true: "allowed", false: "error"}[same],
					OK:   same,
					Detail: map[bool]string{
						true: "", false: "the bytes differ -- a wrong DEK decrypts Secrets to garbage",
					}[same],
				})
			}
		}
	}

	// ── negative: the three denials the claim depends on ──────────────────
	_, gerr := c.GetCryptoKey(ctx, keyName)
	record("read the key's metadata (GetCryptoKey, needs cryptoKeys.get)", "denied", gerr)

	if denyProbeKey != "" {
		_, _, perr := c.Encrypt(ctx, denyProbeKey, []byte("x"))
		record("encrypt under a DIFFERENT key in the same ring", "denied", perr)
	} else {
		checks = append(checks, Check{
			What: "encrypt under a DIFFERENT key in the same ring", Want: "denied",
			Got: "not-tested",
			Detail: "no deny-probe key: re-run `soloz kms init --with-deny-probe`. " +
				"Without a second key there is nothing to be denied on",
		})
	}

	// ── the administrative surface, by REAL CALLS against the canary ──────
	//
	// The canary carries the IDENTICAL generated binding -- same service account, same
	// roles/cloudkms.cryptoKeyEncrypterDecrypter, at the key -- so a refusal here is a
	// refusal for the production reason, not for lack of any access. It holds no data.
	// Every call below refuses a non-canary target in the client, so this cannot be
	// re-aimed by an edit.
	if canaryKey != "" {
		cv := canaryKey + "/cryptoKeyVersions/1"
		record("re-point the primary version (updatePrimaryVersion, on the canary)",
			"denied", c.UpdatePrimaryVersion(ctx, canaryKey, "1"))
		record("alter the key's IAM policy (setIamPolicy, on the canary)",
			"denied", c.AlterIAM(ctx, canaryKey))
		record("add a key version (createCryptoKeyVersion, on the canary)",
			"denied", c.CreateKeyVersion(ctx, canaryKey))
		record("disable a key version (on the canary)",
			"denied", c.DisableKeyVersion(ctx, cv))
		record("destroy a key version (on the canary)",
			"denied", c.DestroyKeyVersion(ctx, cv))
	} else {
		for _, what := range []string{
			"re-point the primary version (updatePrimaryVersion, on the canary)",
			"alter the key's IAM policy (setIamPolicy, on the canary)",
			"add a key version (createCryptoKeyVersion, on the canary)",
			"disable a key version (on the canary)",
			"destroy a key version (on the canary)",
		} {
			checks = append(checks, Check{
				What: what, Want: "denied", Got: "not-tested",
				Detail: "no canary key. These are NEVER attempted against the live key -- a " +
					"probe that proves a denial by performing the action does the action when " +
					"the policy is wrong. Re-run `soloz kms init --with-canary`",
			})
		}
	}

	return checks
}

// RevokeImpersonation removes ONE member from the token-creator role on the service
// account, leaving every other binding intact.
//
// IT EXISTS BECAUSE THE ALTERNATIVE IS A WHOLESALE WRITE. The first time this grant was
// revoked it was done by hand, with a setIamPolicy carrying `bindings: []` -- which is
// correct only for a policy that has exactly one binding and silently destroys every
// other one otherwise. The grant is deliberately temporary (it exists to collect
// evidence and should not outlive it), so revoking it is a routine action, and a routine
// action that is destructive when the state is slightly different from last time is one
// that eventually destroys something.
//
// Read-modify-write with the etag, like EnsureKeyBinding: a concurrent change becomes a
// failed call rather than a lost binding. Removing a member that is not there is a
// no-op, so this is safe to run twice and safe to run speculatively.
func (c *Client) RevokeImpersonation(ctx context.Context, saEmail, member string) (bool, error) {
	resource := fmt.Sprintf("projects/%s/serviceAccounts/%s", c.projectID, saEmail)
	base := fmt.Sprintf("%s/v1/%s", c.iamHost, resource)

	var policy iamPolicy
	if err := c.call(ctx, http.MethodPost, base+":getIamPolicy", map[string]any{}, &policy); err != nil {
		return false, fmt.Errorf("reading the IAM policy of %s: %w", saEmail, err)
	}

	changed := false
	kept := make([]iamBinding, 0, len(policy.Bindings))
	for _, b := range policy.Bindings {
		if b.Role != roleTokenCreator {
			kept = append(kept, b)
			continue
		}
		members := make([]string, 0, len(b.Members))
		for _, m := range b.Members {
			if m == member {
				changed = true
				continue
			}
			members = append(members, m)
		}
		// A role with no members left is dropped rather than written back empty: an
		// empty binding is accepted and then shows up in every policy listing as a role
		// that is granted to nobody, which reads as a grant somebody forgot to finish.
		if len(members) > 0 {
			kept = append(kept, iamBinding{Role: b.Role, Members: members})
		}
	}
	if !changed {
		return false, nil
	}
	policy.Bindings = kept
	if err := c.setSAPolicy(ctx, base, policy); err != nil {
		return false, err
	}
	return true, nil
}

// ── The administrative calls, aimed ONLY at a canary key ──────────────────
//
// WHY THESE ARE REAL CALLS AGAIN, having twice been something else.
//
// They began as real calls against the live key, which is a probe that performs the
// action it is testing for in exactly the case it exists to detect. They were then
// replaced with testIamPermissions, which is worse: Google's own client documents it as
// "designed to be used for building permission-aware UIs and command-line tools, NOT for
// authorization checking", and says it "may 'fail open' without warning"
// (cloud.google.com/go/kms@v1.35.0 apiv1/key_management_client.go:1337). Fail-open is the
// wrong direction for a NEGATIVE assertion: it can report a permission as absent while
// the identity holds it, which is the one error this evidence must not make.
//
// THE CANARY RESOLVES WHAT LOOKED LIKE A TRADE-OFF. The objection to a throwaway key was
// that the identity has no binding on it, so a denial there proves nothing about the live
// key -- denied for lack of any access rather than because the role is narrow. The fix is
// to give the canary the IDENTICAL generated binding: the same service account, the same
// roles/cloudkms.cryptoKeyEncrypterDecrypter, at the key. Then a refused destroy is
// refused for the production reason, on a key that holds no data and can be deleted.
//
// It is also stronger than analysing the key's IAM policy, because it exercises the
// enforcement path: an inherited project-level grant that the key-level policy does not
// show would make these calls SUCCEED, and the row would fail.
//
// EVERY ONE OF THEM REFUSES A NON-CANARY TARGET. A comment saying "only call this with
// the canary" is a comment; mustBeCanary is a guard, and a future edit that passes the
// live key fails at runtime instead of destroying it.

// canarySuffix is what makes a key disposable, by name.
const canarySuffix = "-canary"

func mustBeCanary(resource string) error {
	key := resource
	if i := strings.Index(key, "/cryptoKeyVersions/"); i >= 0 {
		key = key[:i]
	}
	if !strings.HasSuffix(key, canarySuffix) {
		return fmt.Errorf("refusing to attempt an administrative call against %q: these probes "+
			"are only ever aimed at a %s key, which holds no data and carries the same IAM "+
			"binding as the live one. Aiming one at a key that holds data would perform the "+
			"action it is testing for whenever the policy is wrong", resource, canarySuffix)
	}
	return nil
}

// AlterIAM attempts to rewrite the canary's IAM policy. The body is a policy with no
// bindings: there is nothing to lose-update on a canary, and sending back an observed
// policy is what made the live-key version of this unsafe.
func (c *Client) AlterIAM(ctx context.Context, keyName string) error {
	if err := mustBeCanary(keyName); err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, c.kmsHost+"/v1/"+keyName+":setIamPolicy",
		map[string]any{"policy": iamPolicy{}}, nil)
}

// CreateKeyVersion attempts to add a version to the canary.
func (c *Client) CreateKeyVersion(ctx context.Context, keyName string) error {
	if err := mustBeCanary(keyName); err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, c.kmsHost+"/v1/"+keyName+"/cryptoKeyVersions",
		map[string]any{}, nil)
}

// UpdatePrimaryVersion attempts to re-point the canary's primary version. This is the
// call ADR-100's no-reactivation rule turns on.
func (c *Client) UpdatePrimaryVersion(ctx context.Context, keyName, version string) error {
	if err := mustBeCanary(keyName); err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, c.kmsHost+"/v1/"+keyName+":updatePrimaryVersion",
		map[string]any{"cryptoKeyVersionId": version}, nil)
}

// DisableKeyVersion attempts to move a canary version to DISABLED.
func (c *Client) DisableKeyVersion(ctx context.Context, versionName string) error {
	if err := mustBeCanary(versionName); err != nil {
		return err
	}
	return c.call(ctx, http.MethodPatch, c.kmsHost+"/v1/"+versionName+"?updateMask=state",
		map[string]any{"state": "DISABLED"}, nil)
}

// DestroyKeyVersion attempts to schedule a canary version for destruction.
func (c *Client) DestroyKeyVersion(ctx context.Context, versionName string) error {
	if err := mustBeCanary(versionName); err != nil {
		return err
	}
	return c.call(ctx, http.MethodPost, c.kmsHost+"/v1/"+versionName+":destroy",
		map[string]any{}, nil)
}
