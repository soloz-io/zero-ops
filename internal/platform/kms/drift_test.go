package kms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The real manifest, so the tests compare against what Git actually declares rather than
// a fixture that can drift from it. A copy would pass while the shipped file was wrong,
// which is the failure this whole mechanism exists to catch one level up.
const desiredManifest = "../../../manifests/hub-core-services/crossplane/kms/hub-key.yaml"

type keyFake struct {
	rotation string
	destroy  string
	purpose  string
	primary  string
	status   int
}

func (k *keyFake) client(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if k.status != 0 {
			w.WriteHeader(k.status)
			_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"no"}}`))
			return
		}
		out := map[string]any{
			"name":                     "k",
			"purpose":                  k.purpose,
			"rotationPeriod":           k.rotation,
			"destroyScheduledDuration": k.destroy,
			"primary":                  map[string]any{"name": k.primary, "state": "ENABLED"},
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return &Client{
		http: srv.Client(), projectID: "nutgraf-510805",
		kmsHost: srv.URL, iamHost: srv.URL, serviceUsageHost: srv.URL,
	}
}

func matching(floor int) *keyFake {
	// strconv, not string(rune('0'+n)): that renders 10 as a colon and would quietly
	// build an unparseable version name the moment a key reached double digits.
	return &keyFake{
		rotation: "7776000s", destroy: "2592000s", purpose: "ENCRYPT_DECRYPT",
		primary: "projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/" +
			strconv.Itoa(floor),
	}
}

func TestTheShippedManifestParsesAndCarriesAFloor(t *testing.T) {
	// THE MANIFEST IS THE DESIRED STATE, so a manifest this cannot read is a drift check
	// that silently has nothing to compare against -- which passes. ADR-100 calls that
	// the failure worse than an obvious one.
	e, err := ParseExpected(desiredManifest, "nutgraf-hub")
	if err != nil {
		t.Fatalf("the shipped manifest does not parse: %v", err)
	}
	if e.VersionFloor < 1 {
		t.Fatalf("no %s on the shipped CryptoKey; the durable half of the no-reactivation "+
			"rule would not exist", VersionFloorAnnotation)
	}
	if e.RotationPeriod != "7776000s" {
		t.Errorf("rotationPeriod is %q, want the platform cryptoperiod 7776000s", e.RotationPeriod)
	}
	if !strings.Contains(e.KeyName, "nutgraf-hub") {
		t.Errorf("the cluster substitution did not happen: key name is %q", e.KeyName)
	}
}

func TestAManifestWithNoCryptoKeyIsRefused(t *testing.T) {
	// A parse that found nothing would compare live state against nothing and pass.
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(p, []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ParseExpected(p, "c")
	if err == nil {
		t.Fatal("a manifest with no CryptoKey was accepted")
	}
	if !strings.Contains(err.Error(), "worse than one that fails") {
		t.Fatalf("the error does not explain why an empty comparison is the danger: %v", err)
	}
}

func TestNoDriftWhenTheLiveKeyMatches(t *testing.T) {
	e, err := ParseExpected(desiredManifest, "nutgraf-hub")
	if err != nil {
		t.Fatal(err)
	}
	f := matching(e.VersionFloor)
	got, err := f.client(t).Drift(context.Background(), "k", e)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no findings, got %+v", got)
	}
}

func TestARegressedPrimaryVersionIsAnAlarmAndSaysNotToLowerTheFloor(t *testing.T) {
	// THE ONE THIS MECHANISM EXISTS FOR. The plugin cannot hold this invariant -- its
	// memory does not survive a restart -- so Git holds it and a regression becomes a
	// finding somebody must act on.
	e := &Expected{Source: "m.yaml", VersionFloor: 3, Purpose: "ENCRYPT_DECRYPT"}
	f := &keyFake{purpose: "ENCRYPT_DECRYPT",
		primary: "projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/2"}

	got, err := f.client(t).Drift(context.Background(), "k", e)
	if err != nil {
		t.Fatal(err)
	}
	if !Alarming(got) {
		t.Fatalf("a regressed primary version did not alarm: %+v", got)
	}
	var found *DriftFinding
	for i := range got {
		if strings.Contains(got[i].What, "REGRESSED") {
			found = &got[i]
		}
	}
	if found == nil {
		t.Fatal("no REGRESSED finding")
	}
	if !strings.Contains(found.Action, "must not be 'fixed' by lowering the floor") {
		t.Errorf("the action does not forbid the obvious wrong repair: %q", found.Action)
	}
	if !strings.Contains(found.Action, "rolling FORWARD") {
		t.Errorf("the action does not say what to do instead: %q", found.Action)
	}
}

func TestAnAdvancedPrimaryVersionIsANoticeAndNotAFailure(t *testing.T) {
	// A rotation is supposed to happen. Failing on one would train people to ignore this
	// command, and an ignored alarm is worse than none.
	e := &Expected{Source: "m.yaml", VersionFloor: 2, Purpose: "ENCRYPT_DECRYPT"}
	f := &keyFake{purpose: "ENCRYPT_DECRYPT",
		primary: "projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/5"}

	got, err := f.client(t).Drift(context.Background(), "k", e)
	if err != nil {
		t.Fatal(err)
	}
	if Alarming(got) {
		t.Fatalf("a normal rotation alarmed: %+v", got)
	}
	if len(got) != 1 || got[0].Severity != SeverityNotice {
		t.Fatalf("expected one NOTICE, got %+v", got)
	}
	// THE WORDING IS PART OF THE CONTRACT. This is the one outcome that is not a
	// problem, so it has to say what happened, that nothing is wrong, and the exact edit
	// — otherwise it reads as a failure and gets skipped next time.
	if got[0].What != "KMS primary version advanced from floor 2 to 5" {
		t.Errorf("the notice does not state the movement plainly: %q", got[0].What)
	}
	if !strings.Contains(got[0].Action, VersionFloorAnnotation) ||
		!strings.Contains(got[0].Action, `"5"`) {
		t.Errorf("the action does not give the line to commit: %q", got[0].Action)
	}
	if !strings.Contains(got[0].Action, "nothing has written it for you") {
		t.Errorf("the action does not make clear the tool did not self-advance: %q", got[0].Action)
	}
}

func TestConfigurationDriftAlarmsPerField(t *testing.T) {
	e := &Expected{
		Source: "m.yaml", VersionFloor: 1,
		RotationPeriod: "7776000s", DestroyScheduledDuration: "2592000s",
		Purpose: "ENCRYPT_DECRYPT",
	}
	f := &keyFake{
		rotation: "31536000s", // a year: past ADR-100's ceiling
		destroy:  "86400s",    // one day instead of thirty
		purpose:  "ENCRYPT_DECRYPT",
		primary:  "projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1",
	}
	got, err := f.client(t).Drift(context.Background(), "k", e)
	if err != nil {
		t.Fatal(err)
	}
	if !Alarming(got) {
		t.Fatal("configuration drift did not alarm")
	}
	var fields []string
	for _, d := range got {
		fields = append(fields, d.What)
	}
	for _, want := range []string{"rotationPeriod", "destroyScheduledDuration"} {
		if !strings.Contains(strings.Join(fields, ","), want) {
			t.Errorf("no finding for %s; got %v", want, fields)
		}
	}
}

func TestAMissingFloorIsItselfAnAlarm(t *testing.T) {
	// Without it there is no durable record, so there is nothing a regression could be
	// measured against -- and the check would pass forever.
	e := &Expected{Source: "m.yaml", Purpose: "ENCRYPT_DECRYPT"} // VersionFloor zero
	f := &keyFake{purpose: "ENCRYPT_DECRYPT",
		primary: "projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1"}
	got, err := f.client(t).Drift(context.Background(), "k", e)
	if err != nil {
		t.Fatal(err)
	}
	if !Alarming(got) {
		t.Fatal("a missing version floor did not alarm")
	}
}

func TestNothingInDriftMutatesAnything(t *testing.T) {
	// Detection, not reconciliation. A command that repaired drift would hand back the
	// mutation authority ADR-100 deliberately withheld from anything in-cluster, and
	// would use it where nobody is looking.
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"k","purpose":"ENCRYPT_DECRYPT","rotationPeriod":"7776000s",` +
			`"destroyScheduledDuration":"2592000s","primary":{"name":` +
			`"projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1","state":"ENABLED"}}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{http: srv.Client(), projectID: "p",
		kmsHost: srv.URL, iamHost: srv.URL, serviceUsageHost: srv.URL}

	e := &Expected{Source: "m.yaml", VersionFloor: 1, RotationPeriod: "7776000s",
		DestroyScheduledDuration: "2592000s", Purpose: "ENCRYPT_DECRYPT"}
	if _, err := c.Drift(context.Background(), "k", e); err != nil {
		t.Fatal(err)
	}
	for _, call := range calls {
		if !strings.HasPrefix(call, "GET ") {
			t.Errorf("drift made a non-GET call: %s", call)
		}
	}
	if len(calls) == 0 {
		t.Fatal("drift made no calls")
	}
}
