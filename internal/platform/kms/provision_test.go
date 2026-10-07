package kms

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeGoogle stands in for Cloud KMS, IAM and Service Usage.
//
// IT RECORDS EVERY PATH IT WAS CALLED ON, because the properties worth asserting here
// are mostly about WHERE a call went rather than what came back. "The binding is at the
// key, not at the project" is a claim about a URL.
type fakeGoogle struct {
	t *testing.T

	keyRings map[string]bool
	keys     map[string]*CryptoKey
	accounts map[string]bool
	// policies by resource name.
	policies map[string]*iamPolicy

	paths   []string
	creates int

	// apiEnabled drives the Service Usage read.
	apiEnabled bool
}

func newFake(t *testing.T) *fakeGoogle {
	return &fakeGoogle{
		t:          t,
		keyRings:   map[string]bool{},
		keys:       map[string]*CryptoKey{},
		accounts:   map[string]bool{},
		policies:   map[string]*iamPolicy{},
		apiEnabled: true,
	}
}

func (f *fakeGoogle) conflict(w http.ResponseWriter, what string) {
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code": 409, "status": "ALREADY_EXISTS", "message": what + " already exists",
		},
	})
}

func (f *fakeGoogle) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.paths = append(f.paths, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, "/v1/")

		switch {
		// ── Service Usage ────────────────────────────────────────────────
		case strings.Contains(path, "/services/cloudkms.googleapis.com"):
			if strings.HasSuffix(path, ":enable") {
				_, _ = w.Write([]byte(`{"name":"operations/x","done":true}`))
				return
			}
			state := "DISABLED"
			if f.apiEnabled {
				state = "ENABLED"
			}
			_, _ = fmt.Fprintf(w, `{"state":%q}`, state)
			return

		// ── IAM: service accounts ────────────────────────────────────────
		case strings.HasSuffix(path, "/serviceAccounts") && r.Method == http.MethodPost:
			var body struct {
				AccountID string `json:"accountId"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if f.accounts[body.AccountID] {
				f.conflict(w, "service account")
				return
			}
			f.accounts[body.AccountID] = true
			f.creates++
			_, _ = w.Write([]byte(`{}`))
			return

		// ── IAM policy, and THE TWO SURFACES DISAGREE ON THE HTTP METHOD ──
		//
		// Cloud KMS serves getIamPolicy as GET with the policy version in the query
		// string. The IAM service-account surface serves it as POST with a JSON body.
		// They look like the same call and are not, and the first version of this client
		// used POST for both -- which Google answered with a 404 from its frontend, a
		// status that reads as "no such key" rather than "wrong method".
		//
		// THE FAKE USED TO ACCEPT EITHER METHOD, so the test passed with the wrong one
		// and the defect was found on a real project instead. That is the actual lesson
		// here: a stand-in that is more permissive than the service it stands in for
		// cannot catch the class of bug it exists to catch. The method is asserted now.
		case strings.HasSuffix(path, ":getIamPolicy"):
			res := strings.TrimSuffix(path, ":getIamPolicy")

			wantMethod := http.MethodGet // Cloud KMS
			if strings.Contains(res, "/serviceAccounts/") {
				wantMethod = http.MethodPost // IAM
			}
			if r.Method != wantMethod {
				f.t.Errorf("getIamPolicy on %s used %s; that surface requires %s. Google answers "+
					"the wrong method with a 404, which reads as a missing resource",
					res, r.Method, wantMethod)
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":404,"status":"NOT_FOUND","message":"wrong method"}}`))
				return
			}
			// Cloud KMS needs the policy version in the QUERY STRING, not a body: a
			// conditional binding is invisible at version 1 and the read-modify-write
			// below would silently drop it.
			// r.URL.Query(), not the path: `path` is r.URL.Path, which excludes the
			// query string entirely -- the first version of this check read it there and
			// could therefore never pass.
			if wantMethod == http.MethodGet &&
				r.URL.Query().Get("options.requestedPolicyVersion") != "3" {
				f.t.Errorf("getIamPolicy on %s did not request policy version 3; a conditional "+
					"binding would be invisible and dropped by the next write", res)
			}

			p := f.policies[res]
			if p == nil {
				p = &iamPolicy{Etag: "e0"}
			}
			_ = json.NewEncoder(w).Encode(p)
			return

		case strings.HasSuffix(path, ":setIamPolicy"):
			// POST on both surfaces, unlike the read above.
			if r.Method != http.MethodPost {
				f.t.Errorf("setIamPolicy used %s, want POST", r.Method)
			}
			res := strings.TrimSuffix(path, ":setIamPolicy")
			var body struct {
				Policy iamPolicy `json:"policy"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.policies[res] = &body.Policy
			_ = json.NewEncoder(w).Encode(body.Policy)
			return

		// ── KMS: key rings ───────────────────────────────────────────────
		case strings.HasSuffix(path, "/keyRings") && r.Method == http.MethodPost:
			id := r.URL.Query().Get("keyRingId")
			name := path[:len(path)-len("/keyRings")] + "/keyRings/" + id
			if f.keyRings[name] {
				f.conflict(w, "key ring")
				return
			}
			f.keyRings[name] = true
			f.creates++
			_, _ = fmt.Fprintf(w, `{"name":%q}`, name)
			return

		// ── KMS: crypto keys ─────────────────────────────────────────────
		case strings.HasSuffix(path, "/cryptoKeys") && r.Method == http.MethodPost:
			id := r.URL.Query().Get("cryptoKeyId")
			name := path[:len(path)-len("/cryptoKeys")] + "/cryptoKeys/" + id
			if _, ok := f.keys[name]; ok {
				f.conflict(w, "crypto key")
				return
			}
			var body struct {
				Purpose                  string `json:"purpose"`
				RotationPeriod           string `json:"rotationPeriod"`
				DestroyScheduledDuration string `json:"destroyScheduledDuration"`
				NextRotationTime         string `json:"nextRotationTime"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.NextRotationTime == "" {
				f.t.Errorf("CreateCryptoKey sent no nextRotationTime for %s", name)
			}
			k := &CryptoKey{
				Name: name, Purpose: body.Purpose,
				RotationPeriod:           body.RotationPeriod,
				DestroyScheduledDuration: body.DestroyScheduledDuration,
			}
			k.Primary.Name = name + "/cryptoKeyVersions/1"
			k.Primary.State = "ENABLED"
			f.keys[name] = k
			f.creates++
			_ = json.NewEncoder(w).Encode(k)
			return

		case r.Method == http.MethodGet && strings.Contains(path, "/cryptoKeys/"):
			k, ok := f.keys[path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":404,"status":"NOT_FOUND","message":"no"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(k)
			return
		}

		f.t.Errorf("unexpected call: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	})
}

// client wires a Client to the fake, bypassing Application Default Credentials.
func (f *fakeGoogle) client(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return &Client{
		http:             srv.Client(),
		projectID:        "nutgraf-510805",
		projectNumber:    "1010595512891",
		kmsHost:          srv.URL,
		iamHost:          srv.URL,
		serviceUsageHost: srv.URL,
	}
}

func request() ProvisionRequest {
	return ProvisionRequest{
		Spec: KeySpec{
			Location: "europe-west3",
			KeyRing:  "soloz-etcd",
			Key:      "etcd-kek-nutgraf-01",
		},
		ServiceAccountID: "kms-nutgraf-01",
	}
}

func TestProvisionIsIdempotent(t *testing.T) {
	// THE REQUIREMENT THAT MADE THIS PACKAGE EXIST. A provisioner that cannot be
	// re-run is one people stop running, and then keys get made by hand again. The
	// second run must succeed, create nothing, and report the same names.
	f := newFake(t)
	c := f.client(t)
	ctx := context.Background()

	first, err := c.Provision(ctx, request())
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	afterFirst := f.creates

	second, err := c.Provision(ctx, request())
	if err != nil {
		t.Fatalf("second run: %v -- the command is not idempotent", err)
	}
	if f.creates != afterFirst {
		t.Fatalf("the second run created %d more resource(s); re-running must confirm state, "+
			"not create a second anything", f.creates-afterFirst)
	}
	if *first != *second {
		t.Fatalf("the two runs reported different resources:\n %+v\n %+v", first, second)
	}
	if !strings.HasSuffix(first.CryptoKey,
		"/locations/europe-west3/keyRings/soloz-etcd/cryptoKeys/etcd-kek-nutgraf-01") {
		t.Fatalf("unexpected key name %q", first.CryptoKey)
	}
}

func TestTheBindingIsAtTheKeyAndNotAtTheProject(t *testing.T) {
	// THE LEAST-PRIVILEGE CLAIM, AS AN ASSERTION ABOUT A URL.
	//
	// A project-level binding grants encrypt and decrypt on EVERY key in the project.
	// It is the single easiest thing to get wrong by hand, because the project is what
	// the console offers first, and it is invisible afterwards: the plugin works
	// identically either way.
	f := newFake(t)
	c := f.client(t)
	if _, err := c.Provision(context.Background(), request()); err != nil {
		t.Fatal(err)
	}

	var setCalls []string
	for _, p := range f.paths {
		if strings.Contains(p, ":setIamPolicy") {
			setCalls = append(setCalls, p)
		}
	}
	if len(setCalls) != 1 {
		t.Fatalf("expected exactly one setIamPolicy, got %d: %v", len(setCalls), setCalls)
	}
	if !strings.Contains(setCalls[0], "/cryptoKeys/etcd-kek-nutgraf-01:setIamPolicy") {
		t.Fatalf("the IAM policy was set on %q, which is not the key", setCalls[0])
	}

	res := "projects/nutgraf-510805/locations/europe-west3/keyRings/soloz-etcd/cryptoKeys/etcd-kek-nutgraf-01"
	policy := f.policies[res]
	if policy == nil {
		t.Fatal("no policy was written at the key")
	}
	if len(policy.Bindings) != 1 ||
		policy.Bindings[0].Role != RoleEncrypterDecrypter ||
		len(policy.Bindings[0].Members) != 1 ||
		policy.Bindings[0].Members[0] != "serviceAccount:kms-nutgraf-01@nutgraf-510805.iam.gserviceaccount.com" {
		t.Fatalf("the binding is not one role for one member: %+v", policy.Bindings)
	}
}

func TestAnExistingUnrelatedBindingIsNotDropped(t *testing.T) {
	// setIamPolicy REPLACES the policy wholesale, so a blind write silently removes
	// anything already there -- another cluster's identity, or an auditor's read role.
	// Read-modify-write is what makes that a preserved binding instead of a lost one.
	f := newFake(t)
	c := f.client(t)
	res := "projects/nutgraf-510805/locations/europe-west3/keyRings/soloz-etcd/cryptoKeys/etcd-kek-nutgraf-01"
	f.policies[res] = &iamPolicy{
		Etag: "e1",
		Bindings: []iamBinding{
			{Role: "roles/cloudkms.viewer", Members: []string{"user:auditor@example.com"}},
		},
	}

	if _, err := c.Provision(context.Background(), request()); err != nil {
		t.Fatal(err)
	}
	got := f.policies[res]
	if len(got.Bindings) != 2 {
		t.Fatalf("expected the existing binding to survive alongside the new one, got %+v",
			got.Bindings)
	}
	if got.Etag != "e1" {
		t.Errorf("the etag was not sent back, so a concurrent change would be silently "+
			"overwritten instead of rejected: %q", got.Etag)
	}
}

func TestRotationAndDestroyWindowAreSetAtCreation(t *testing.T) {
	// A key created without these is a key nobody decided them for, and nothing later
	// reveals that: it encrypts and decrypts perfectly while never rotating.
	f := newFake(t)
	c := f.client(t)
	if _, err := c.Provision(context.Background(), request()); err != nil {
		t.Fatal(err)
	}
	res := "projects/nutgraf-510805/locations/europe-west3/keyRings/soloz-etcd/cryptoKeys/etcd-kek-nutgraf-01"
	k := f.keys[res]
	if k == nil {
		t.Fatal("no key was created")
	}
	if k.RotationPeriod != "7776000s" { // 90 days
		t.Errorf("rotationPeriod is %q, want 7776000s (ADR-100's 90-day ceiling)", k.RotationPeriod)
	}
	if k.DestroyScheduledDuration != "2592000s" { // 30 days
		t.Errorf("destroyScheduledDuration is %q, want 2592000s", k.DestroyScheduledDuration)
	}
	if k.Purpose != "ENCRYPT_DECRYPT" {
		t.Errorf("purpose is %q", k.Purpose)
	}
}

func TestDriftOnAnExistingKeyIsReportedAndNothingIsChanged(t *testing.T) {
	// VERIFY, NOT REWRITE. ADR-100 puts ongoing reconciliation in Crossplane. A
	// bootstrap command that silently rewrote the properties of a key already holding
	// data would be a second, competing reconciler -- and two reconcilers disagreeing
	// about this particular resource is not a failure mode worth having.
	f := newFake(t)
	c := f.client(t)
	res := "projects/nutgraf-510805/locations/europe-west3/keyRings/soloz-etcd/cryptoKeys/etcd-kek-nutgraf-01"
	f.keys[res] = &CryptoKey{
		Name: res, Purpose: "ENCRYPT_DECRYPT",
		RotationPeriod:           "31536000s", // a year: well past the ceiling
		DestroyScheduledDuration: "2592000s",
	}

	_, err := c.EnsureCryptoKey(context.Background(), request().Spec)
	if err == nil {
		t.Fatal("drift was accepted silently")
	}
	if !strings.Contains(err.Error(), "rotationPeriod") ||
		!strings.Contains(err.Error(), "31536000s") {
		t.Fatalf("the error does not name the field and the actual value: %v", err)
	}
	if f.keys[res].RotationPeriod != "31536000s" {
		t.Fatal("the existing key was modified; this command must not rewrite one")
	}
}

func TestTheDenyProbeKeyIsCreatedAndDeliberatelyNotBound(t *testing.T) {
	// Cross-key denial cannot be ASSERTED without a second key to be denied on.
	// "Scoped to one key" is not evidence; a 403 on a sibling key is.
	f := newFake(t)
	c := f.client(t)
	req := request()
	req.WithDenyProbe = true

	out, err := c.Provision(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.DenyProbeKey, "/cryptoKeys/etcd-kek-nutgraf-01-deny-probe") {
		t.Fatalf("deny probe key is %q", out.DenyProbeKey)
	}
	probeRes := strings.TrimPrefix(out.DenyProbeKey, "")
	if f.policies[probeRes] != nil {
		t.Fatalf("the deny-probe key has an IAM policy (%+v); it must be unbound or it "+
			"proves nothing", f.policies[probeRes])
	}
}

func TestNoServiceAccountKeyIsEverCreated(t *testing.T) {
	// ADR-100 prohibits a long-lived service-account key outright. There is no flag to
	// create one, and this asserts no call goes near the endpoint that would -- an
	// option that exists gets used at 2am.
	f := newFake(t)
	c := f.client(t)
	req := request()
	req.WithDenyProbe = true
	req.ImpersonationMember = "user:someone@example.com"
	if _, err := c.Provision(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	for _, p := range f.paths {
		if strings.Contains(p, "/serviceAccounts/") && strings.Contains(p, "/keys") {
			t.Fatalf("a service-account key endpoint was called: %s", p)
		}
	}
}

func TestImpersonationIsGrantedOnTheAccountAndIsOptIn(t *testing.T) {
	// It is an escalation path, so a default run must not grant it.
	f := newFake(t)
	c := f.client(t)
	if _, err := c.Provision(context.Background(), request()); err != nil {
		t.Fatal(err)
	}
	for res := range f.policies {
		if strings.Contains(res, "/serviceAccounts/") {
			t.Fatalf("a default run granted impersonation on %s", res)
		}
	}

	f2 := newFake(t)
	c2 := f2.client(t)
	req := request()
	req.ImpersonationMember = "user:you@example.com"
	if _, err := c2.Provision(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	found := false
	for res, p := range f2.policies {
		if !strings.Contains(res, "/serviceAccounts/") {
			continue
		}
		for _, b := range p.Bindings {
			if b.Role == roleTokenCreator && b.Members[0] == "user:you@example.com" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("--grant-impersonation-to did not grant roles/iam.serviceAccountTokenCreator")
	}
}

func TestAnAlreadyEnabledAPIIsNotReEnabled(t *testing.T) {
	f := newFake(t)
	f.apiEnabled = true
	c := f.client(t)
	if err := c.EnsureAPIEnabled(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, p := range f.paths {
		if strings.Contains(p, ":enable") {
			t.Fatalf("an already-enabled API was re-enabled: %s", p)
		}
	}
}

func TestAMissingProjectNumberExplainsItselfRatherThanGuessing(t *testing.T) {
	f := newFake(t)
	c := f.client(t)
	c.projectNumber = ""
	err := c.EnsureAPIEnabled(context.Background())
	if err == nil {
		t.Fatal("a missing project number was accepted")
	}
	if !strings.Contains(err.Error(), "gcloud services enable") {
		t.Fatalf("the error does not give the one command to run instead: %v", err)
	}
}

func TestABadProjectIDIsRefusedBeforeAnyCall(t *testing.T) {
	// A wrong project id answers 403, which reads as a permissions problem and sends
	// the reader to IAM.
	for _, bad := range []string{"", "X-upper", "no", "ends-with-", "Nutgraf"} {
		if _, err := New(context.Background(), bad, "", ""); err == nil {
			t.Errorf("%q was accepted as a project id", bad)
		}
	}
}

func TestSecondsRendersTheFormatTheseAPIsWant(t *testing.T) {
	if got := seconds(90 * 24 * time.Hour); got != "7776000s" {
		t.Fatalf("got %q", got)
	}
}

func TestABillingDisabledProjectSaysWhatToDoAboutIt(t *testing.T) {
	// FAILED_PRECONDITION lands among a dozen unrelated preconditions, and the instinct
	// on any 4xx from a cloud API is to go looking at IAM. It is not an IAM problem and
	// nothing in this repository can fix it. The first real project was found with a
	// billing account LINKED AND CLOSED, which reads as configured in the console.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"status":"FAILED_PRECONDITION",` +
			`"message":"Billing is disabled for project 1010595512891."}}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{
		http: srv.Client(), projectID: "nutgraf-510805", projectNumber: "1010595512891",
		kmsHost: srv.URL, iamHost: srv.URL, serviceUsageHost: srv.URL,
	}

	req := request()
	req.SkipAPIEnable = true
	_, err := c.Provision(context.Background(), req)
	if err == nil {
		t.Fatal("a billing-disabled project was accepted")
	}
	for _, want := range []string{"ACTIVE billing account", "CLOSED", "nutgraf-510805", "idempotent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q:\n%v", want, err)
		}
	}
}

func TestAPermissionFailureNamesWhatAccessIsNeeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"nope"}}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{
		http: srv.Client(), projectID: "nutgraf-510805",
		kmsHost: srv.URL, iamHost: srv.URL, serviceUsageHost: srv.URL,
	}
	req := request()
	req.SkipAPIEnable = true
	_, err := c.Provision(context.Background(), req)
	if err == nil {
		t.Fatal("expected a permission failure")
	}
	if !strings.Contains(err.Error(), "roles/owner") {
		t.Errorf("the error does not say what access is needed:\n%v", err)
	}
}

func TestRevokingImpersonationLeavesEveryOtherBindingIntact(t *testing.T) {
	// THE DEFECT THIS COMMAND EXISTS TO PREVENT. The first revocation was done by hand
	// with a setIamPolicy carrying `bindings: []`, which is correct only for a policy
	// that has exactly one binding. Here the account also has a viewer and a SECOND
	// token-creator member, and both must survive.
	f := newFake(t)
	c := f.client(t)
	sa := "kms-nutgraf-01@nutgraf-510805.iam.gserviceaccount.com"
	res := "projects/nutgraf-510805/serviceAccounts/" + sa
	f.policies[res] = &iamPolicy{
		Etag: "e9",
		Bindings: []iamBinding{
			{Role: roleTokenCreator, Members: []string{
				"user:leaving@example.com", "user:staying@example.com",
			}},
			{Role: "roles/iam.serviceAccountViewer", Members: []string{"user:auditor@example.com"}},
		},
	}

	changed, err := c.RevokeImpersonation(context.Background(), sa, "user:leaving@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("the revocation reported no change")
	}

	got := f.policies[res]
	var tokenMembers, viewerMembers []string
	for _, b := range got.Bindings {
		switch b.Role {
		case roleTokenCreator:
			tokenMembers = b.Members
		case "roles/iam.serviceAccountViewer":
			viewerMembers = b.Members
		}
	}
	if len(tokenMembers) != 1 || tokenMembers[0] != "user:staying@example.com" {
		t.Errorf("the other token-creator member did not survive: %v", tokenMembers)
	}
	if len(viewerMembers) != 1 || viewerMembers[0] != "user:auditor@example.com" {
		t.Errorf("an unrelated binding was destroyed: %v", viewerMembers)
	}
	if got.Etag != "e9" {
		t.Errorf("the etag was not sent back, so a concurrent change would be overwritten "+
			"silently: %q", got.Etag)
	}
}

func TestRevokingTheLastMemberDropsTheRoleRatherThanLeavingItEmpty(t *testing.T) {
	// An empty binding is accepted and then appears in every listing as a role granted
	// to nobody, which reads as a grant somebody forgot to finish.
	f := newFake(t)
	c := f.client(t)
	sa := "kms-nutgraf-01@nutgraf-510805.iam.gserviceaccount.com"
	res := "projects/nutgraf-510805/serviceAccounts/" + sa
	f.policies[res] = &iamPolicy{
		Etag:     "e1",
		Bindings: []iamBinding{{Role: roleTokenCreator, Members: []string{"user:you@example.com"}}},
	}
	if _, err := c.RevokeImpersonation(context.Background(), sa, "user:you@example.com"); err != nil {
		t.Fatal(err)
	}
	if n := len(f.policies[res].Bindings); n != 0 {
		t.Fatalf("expected the role to be dropped, got %d binding(s): %+v",
			n, f.policies[res].Bindings)
	}
}

func TestRevokingAMemberThatIsNotThereChangesNothing(t *testing.T) {
	// Safe to run twice, and safe to run speculatively -- which is what makes it usable
	// as the last step of a procedure rather than a thing to check first.
	f := newFake(t)
	c := f.client(t)
	sa := "kms-nutgraf-01@nutgraf-510805.iam.gserviceaccount.com"
	changed, err := c.RevokeImpersonation(context.Background(), sa, "user:nobody@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("reported a change when the member was not bound")
	}
	for _, p := range f.paths {
		if strings.Contains(p, ":setIamPolicy") {
			t.Fatalf("wrote a policy when there was nothing to remove: %s", p)
		}
	}
}

// proveFake refuses everything with PERMISSION_DENIED, which is what a correctly scoped
// identity gets for every administrative call, and records what was attempted.
type proveFake struct {
	calls []string
	// allow is a substring; a call matching it SUCCEEDS, so a test can make one
	// administrative probe pass and check that its row fails.
	allow string
}

func (f *proveFake) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/v1/")
		f.calls = append(f.calls, call)
		w.Header().Set("Content-Type", "application/json")
		if f.allow != "" && strings.Contains(call, f.allow) {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"no"}}`))
	})
}

func proveClient(t *testing.T, f *proveFake) *Client {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	return &Client{
		http: srv.Client(), projectID: "nutgraf-510805",
		kmsHost: srv.URL, iamHost: srv.URL, serviceUsageHost: srv.URL,
	}
}

const liveKey = "projects/nutgraf-510805/locations/europe-west3/keyRings/soloz-etcd/cryptoKeys/etcd-kek-nutgraf-01"

func TestNoAdministrativeCallEverTouchesTheLiveKey(t *testing.T) {
	// THE PROPERTY THAT THREE DESIGNS FAILED TO GET RIGHT.
	//
	// First the administrative denials were real calls against the LIVE key -- a probe
	// that performs the action it tests for, in exactly the case it exists to detect.
	// Then two of them were aimed at an unbound throwaway, which bounded the damage and
	// weakened the semantics. Then all of them were replaced with testIamPermissions,
	// which Google documents as unsuitable for authorization checking and able to fail
	// open -- the wrong direction for a negative assertion.
	//
	// Now: real calls, against a canary carrying the IDENTICAL binding. This asserts the
	// aim, and mustBeCanary enforces it in the client so an edit cannot re-point them.
	f := &proveFake{}
	c := proveClient(t, f)
	canary := liveKey + canarySuffix
	_ = c.ProveLeastPrivilege(context.Background(), liveKey, canary, liveKey+"-deny-probe")

	for _, call := range f.calls {
		administrative := strings.HasPrefix(call, "PATCH ") ||
			strings.Contains(call, ":destroy") ||
			strings.Contains(call, ":setIamPolicy") ||
			strings.Contains(call, ":updatePrimaryVersion") ||
			strings.HasSuffix(call, "/cryptoKeyVersions")
		if administrative && !strings.Contains(call, canarySuffix) {
			t.Errorf("an administrative call was aimed at a non-canary key: %s", call)
		}
	}
	// And the positives went to the LIVE key, because that is the thing that has to work.
	sawLiveEncrypt := false
	for _, call := range f.calls {
		if strings.Contains(call, "etcd-kek-nutgraf-01:encrypt") ||
			strings.Contains(call, "etcd-kek-nutgraf-01/cryptoKeyVersions/1:encrypt") {
			sawLiveEncrypt = true
		}
	}
	if !sawLiveEncrypt {
		t.Error("no encrypt was attempted against the live key")
	}
}

func TestTheClientRefusesAnAdministrativeCallAgainstANonCanaryKey(t *testing.T) {
	// mustBeCanary is the guard that makes the aim structural rather than a convention.
	// A comment saying "only call this with the canary" is a comment.
	f := &proveFake{}
	c := proveClient(t, f)
	ctx := context.Background()
	liveVersion := liveKey + "/cryptoKeyVersions/1"

	for name, err := range map[string]error{
		"UpdatePrimaryVersion": c.UpdatePrimaryVersion(ctx, liveKey, "1"),
		"AlterIAM":             c.AlterIAM(ctx, liveKey),
		"CreateKeyVersion":     c.CreateKeyVersion(ctx, liveKey),
		"DisableKeyVersion":    c.DisableKeyVersion(ctx, liveVersion),
		"DestroyKeyVersion":    c.DestroyKeyVersion(ctx, liveVersion),
	} {
		if err == nil {
			t.Errorf("%s accepted the live key", name)
		} else if !strings.Contains(err.Error(), "only ever aimed at a -canary key") {
			t.Errorf("%s refused for the wrong reason: %v", name, err)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("the guard let calls reach the server: %v", f.calls)
	}
}

func TestEveryAdministrativeOperationHasARow(t *testing.T) {
	// The review named rotate, disable, destroy, administer and IAM alteration.
	f := &proveFake{}
	c := proveClient(t, f)
	checks := c.ProveLeastPrivilege(context.Background(), liveKey,
		liveKey+canarySuffix, liveKey+"-deny-probe")

	var covered string
	for _, k := range checks {
		covered += k.What + "\n"
	}
	for _, want := range []string{
		"updatePrimaryVersion", "setIamPolicy", "createCryptoKeyVersion",
		"disable a key version", "destroy a key version",
		"GetCryptoKey", "DIFFERENT key",
	} {
		if !strings.Contains(covered, want) {
			t.Errorf("no row covers %q", want)
		}
	}
}

func TestAnAdministrativeCallThatSUCCEEDSFailsItsRow(t *testing.T) {
	// The check must bite. A fake that refuses everything would pass whatever the
	// assertion was -- and because these are REAL calls on the enforcement path, a
	// success here is what an inherited project-level grant would actually produce.
	f := &proveFake{allow: ":destroy"}
	c := proveClient(t, f)
	checks := c.ProveLeastPrivilege(context.Background(), liveKey,
		liveKey+canarySuffix, liveKey+"-deny-probe")

	found := false
	for _, k := range checks {
		if strings.Contains(k.What, "destroy a key version") {
			found = true
			if k.OK {
				t.Error("a destroy that SUCCEEDED passed its row")
			}
			if k.Got != "allowed" {
				t.Errorf("row reports got=%q, want allowed", k.Got)
			}
		}
	}
	if !found {
		t.Fatal("no row for destroy")
	}
}

func TestWithoutACanaryTheAdministrativeRowsAreSkippedNotRedirected(t *testing.T) {
	// The wrong repair would be to aim them at the live key when no canary exists.
	f := &proveFake{}
	c := proveClient(t, f)
	checks := c.ProveLeastPrivilege(context.Background(), liveKey, "", "")

	for _, call := range f.calls {
		if strings.HasPrefix(call, "PATCH ") || strings.Contains(call, ":destroy") ||
			strings.Contains(call, ":setIamPolicy") ||
			strings.Contains(call, ":updatePrimaryVersion") {
			t.Fatalf("an administrative call was made with no canary: %s", call)
		}
	}
	skipped := 0
	for _, k := range checks {
		if k.Got == "not-tested" {
			skipped++
		}
	}
	if skipped != 6 { // five administrative rows plus the cross-key row
		t.Fatalf("expected 6 not-tested rows, got %d", skipped)
	}
}

func TestTheCanaryIsBoundIdenticallyToTheLiveKey(t *testing.T) {
	// THE REASON THE CANARY IS EVIDENCE AND THE DENY PROBE IS NOT.
	//
	// An identity with no binding at all is refused for the wrong reason. The canary
	// carries the same service account and the same single role, at the key, so a refusal
	// on it is a refusal the live key would also produce.
	f := newFake(t)
	c := f.client(t)
	req := request()
	req.WithCanary = true
	req.WithDenyProbe = true

	out, err := c.Provision(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.CanaryKey, canarySuffix) {
		t.Fatalf("canary key is %q", out.CanaryKey)
	}

	live := f.policies["projects/nutgraf-510805/locations/europe-west3/keyRings/soloz-etcd/cryptoKeys/etcd-kek-nutgraf-01"]
	canary := f.policies[out.CanaryKey]
	if canary == nil {
		t.Fatal("the canary has no IAM policy; it would be denied for lack of access rather " +
			"than because the role is narrow, which proves nothing about the live key")
	}
	if len(live.Bindings) != len(canary.Bindings) {
		t.Fatalf("binding counts differ: live %d, canary %d", len(live.Bindings), len(canary.Bindings))
	}
	for i := range live.Bindings {
		if live.Bindings[i].Role != canary.Bindings[i].Role {
			t.Errorf("role differs: live %q, canary %q", live.Bindings[i].Role, canary.Bindings[i].Role)
		}
		if strings.Join(live.Bindings[i].Members, ",") != strings.Join(canary.Bindings[i].Members, ",") {
			t.Errorf("members differ: live %v, canary %v",
				live.Bindings[i].Members, canary.Bindings[i].Members)
		}
	}

	// The deny probe must remain UNBOUND: it exists to prove a different thing.
	if f.policies[out.DenyProbeKey] != nil {
		t.Error("the deny-probe key is bound; it must not be, or cross-key denial proves nothing")
	}
}
