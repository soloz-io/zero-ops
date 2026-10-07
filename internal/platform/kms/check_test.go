package kms

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// THE ACCEPTANCE SCENARIOS, as a table, because the review named them as a set and a set
// is what must keep holding. Each row is one operational outcome the carrier routes on.

type scenarioServer struct {
	keyRotation  string
	keyDestroy   string
	keyPurpose   string
	keyPrimary   string
	keyStatus    int
	uploadedKids []string
	caPEM        []byte
	providerErr  int
	methods      []string
}

func (s *scenarioServer) client(t *testing.T) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.methods = append(s.methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path

		if strings.Contains(path, "/workloadIdentityPools/") {
			if s.providerErr != 0 {
				w.WriteHeader(s.providerErr)
				_, _ = w.Write([]byte(`{"error":{"code":500,"status":"INTERNAL","message":"boom"}}`))
				return
			}
			out := map[string]any{"name": "p"}
			if strings.Contains(path, "/providers/workloads") {
				out["oidc"] = map[string]any{"jwksJson": string(keySet(s.uploadedKids...))}
			} else {
				out["x509"] = map[string]any{"trustStore": map[string]any{
					"trustAnchors": []map[string]string{{"pemCertificate": string(s.caPEM)}}}}
			}
			_ = json.NewEncoder(w).Encode(out)
			return
		}

		if s.keyStatus != 0 {
			w.WriteHeader(s.keyStatus)
			_, _ = w.Write([]byte(`{"error":{"code":403,"status":"PERMISSION_DENIED","message":"no"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "k", "purpose": s.keyPurpose, "rotationPeriod": s.keyRotation,
			"destroyScheduledDuration": s.keyDestroy,
			"primary":                  map[string]any{"name": s.keyPrimary, "state": "ENABLED"},
		})
	}))
	t.Cleanup(srv.Close)
	return &Client{http: srv.Client(), projectID: "nutgraf-510805",
		kmsHost: srv.URL, iamHost: srv.URL, serviceUsageHost: srv.URL}
}

func version(n string) string {
	return "projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/" + n
}

func healthyKey() *scenarioServer {
	return &scenarioServer{
		keyRotation: "7776000s", keyDestroy: "2592000s", keyPurpose: "ENCRYPT_DECRYPT",
		keyPrimary: version("3"), uploadedKids: []string{"a"},
	}
}

func desired(floor int) *Expected {
	return &Expected{
		Source: "m.yaml", VersionFloor: floor, RotationPeriod: "7776000s",
		DestroyScheduledDuration: "2592000s", Purpose: "ENCRYPT_DECRYPT",
	}
}

func opts(t *testing.T, s *scenarioServer, d *Expected) CheckOptions {
	t.Helper()
	ca := newCA(t, "nutgraf-01")
	s.caPEM = ca
	return CheckOptions{
		Cluster: "nutgraf-01", KeyName: "k", Pool: "soloz-nutgraf-01", Provider: "workloads",
		X509Provider: "control-plane", Desired: d,
		LiveJWKS: keySet("a"), ClusterCA: ca,
	}
}

func TestTheAcceptanceScenarios(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutate    func(*scenarioServer, *CheckOptions)
		wantWorst Severity
		wantExit  int
		wantIn    string
	}{
		{
			name:      "healthy produces no alert",
			mutate:    func(*scenarioServer, *CheckOptions) {},
			wantWorst: "OK", wantExit: 0,
		},
		{
			name: "a forward rotation is a NOTICE and does not page",
			mutate: func(s *scenarioServer, o *CheckOptions) {
				s.keyPrimary = version("5") // floor is 3
			},
			wantWorst: SeverityNotice, wantExit: 0,
			wantIn: "advanced from floor 3 to 5",
		},
		{
			name: "configuration drift alarms",
			mutate: func(s *scenarioServer, o *CheckOptions) {
				s.keyRotation = "31536000s"
			},
			wantWorst: SeverityAlarm, wantExit: 1, wantIn: "rotationPeriod",
		},
		{
			name: "a regressed primary version alarms",
			mutate: func(s *scenarioServer, o *CheckOptions) {
				s.keyPrimary = version("1") // floor is 3
			},
			wantWorst: SeverityAlarm, wantExit: 1, wantIn: "REGRESSED",
		},
		{
			name: "a missing floor alarms",
			mutate: func(s *scenarioServer, o *CheckOptions) {
				o.Desired.VersionFloor = 0
			},
			wantWorst: SeverityAlarm, wantExit: 1, wantIn: "version floor",
		},
		{
			name:      "an unchanged JWKS produces no alert",
			mutate:    func(*scenarioServer, *CheckOptions) {},
			wantWorst: "OK", wantExit: 0,
		},
		{
			name: "a JWKS stale across a signing-key rotation alarms",
			mutate: func(s *scenarioServer, o *CheckOptions) {
				o.LiveJWKS = keySet("b") // the cluster rotated; the upload still says "a"
			},
			wantWorst: SeverityAlarm, wantExit: 1, wantIn: "STALE",
		},
		{
			name: "a provider that cannot be read is UNKNOWN, not a pass",
			mutate: func(s *scenarioServer, o *CheckOptions) {
				s.providerErr = http.StatusInternalServerError
			},
			wantWorst: SeverityUnknown, wantExit: 2, wantIn: "CHECK broken",
		},
		{
			name: "a key that cannot be read is UNKNOWN, not a pass",
			mutate: func(s *scenarioServer, o *CheckOptions) {
				s.keyStatus = http.StatusForbidden
			},
			wantWorst: SeverityUnknown, wantExit: 2, wantIn: "not a passing check",
		},
		{
			name: "an unreachable cluster leaves staleness UNKNOWN rather than OK",
			mutate: func(s *scenarioServer, o *CheckOptions) {
				o.LiveJWKS = nil
			},
			wantWorst: SeverityUnknown, wantExit: 2, wantIn: "maximum-age bound",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := healthyKey()
			o := opts(t, s, desired(3))
			tc.mutate(s, &o)
			rep := s.client(t).Run(context.Background(), o)

			if rep.Worst != tc.wantWorst {
				t.Errorf("worst = %q, want %q\n%+v", rep.Worst, tc.wantWorst, rep.Checks)
			}
			if rep.ExitCode() != tc.wantExit {
				t.Errorf("exit = %d, want %d", rep.ExitCode(), tc.wantExit)
			}
			if tc.wantIn != "" {
				var blob bytes.Buffer
				_ = rep.WriteJSON(&blob)
				if !strings.Contains(blob.String(), tc.wantIn) {
					t.Errorf("the report does not mention %q:\n%s", tc.wantIn, blob.String())
				}
			}
		})
	}
}

func TestAnUnrunnableCheckIsNeverReportedAsHealthy(t *testing.T) {
	// THE DISTINCTION THE WHOLE REPORT SHAPE EXISTS FOR. A credential that expired, a
	// network that failed, a provider that 500s -- each leaves the question unanswered,
	// and a carrier treating an unanswered question as a healthy answer is the
	// silent-failure shape this ADR keeps finding.
	s := healthyKey()
	s.keyStatus = http.StatusForbidden
	s.providerErr = http.StatusInternalServerError
	rep := s.client(t).Run(context.Background(), opts(t, s, desired(3)))

	if rep.ExitCode() == 0 {
		t.Fatal("a run where nothing could be checked exited 0")
	}
	if rep.ExitCode() != 2 {
		t.Errorf("exit = %d, want 2 — UNKNOWN must be distinguishable from ALARM so a "+
			"broken check is not routed as a security regression", rep.ExitCode())
	}
	for _, c := range rep.Checks {
		if c.Severity == "OK" {
			t.Errorf("check %q reported OK while unable to run", c.Check)
		}
	}
}

func TestTheReportNamesTheSubjectItFailedAbout(t *testing.T) {
	// An alert saying "KMS drift detected" without the cluster, project and key is an
	// alert somebody has to reproduce before they can act on it.
	s := healthyKey()
	s.keyPrimary = version("1")
	rep := s.client(t).Run(context.Background(), opts(t, s, desired(3)))

	var blob bytes.Buffer
	if err := rep.WriteJSON(&blob); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"nutgraf-01", "nutgraf-510805", "soloz-nutgraf-01", "checkedAt"} {
		if !strings.Contains(blob.String(), want) {
			t.Errorf("the report does not carry %q", want)
		}
	}
}

func TestTheCheckerOnlyReads(t *testing.T) {
	// THE BOUNDARY, ASSERTED. Detection and alerting may observe Cloud KMS and WIF state
	// and must never mutate a key, an IAM policy, a JWKS or an encryption configuration.
	s := healthyKey()
	_ = s.client(t).Run(context.Background(), opts(t, s, desired(3)))
	for _, m := range s.methods {
		if m != http.MethodGet {
			t.Errorf("the checker made a %s request; it must only observe", m)
		}
	}
	if len(s.methods) == 0 {
		t.Fatal("the checker made no requests at all")
	}
}
