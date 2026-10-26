package controller

import "testing"

// The names the sidecar's S3 configuration is read from have been wrong three
// times, and every one of them failed SILENTLY: each reference the operator
// injects is optional, so a name that resolves to nothing leaves the sidecar
// reporting "object storage not configured" and no-opping every checkpoint. No
// pod crashed, no event fired, and workspace persistence was simply absent.
//
//	hcloud-token              a KEY, not a Secret name
//	hetzner-credentials       a real Secret, but platform infrastructure; it
//	                          carries S3 keys only in the local Kind manifest
//	{namespace}-app-secrets   no Secret of that name exists in ANY tenant
//	                          namespace
//
// So this asserts the resolved names against what a fleet actually declares,
// rather than against the template's own reasoning. The expectations below were
// read off a running spoke (a live spoke): the SDK binds S3_ACCESS_KEY_ID and
// S3_SECRET_ACCESS_KEY from Secret a fleet Secret, and S3_ENDPOINT_URL
// and S3_BUCKET_NAME from ConfigMap a fleet ConfigMap.
//
// A fourth wrong value should fail here, not in production six weeks later when
// someone notices a restored workspace is empty.
func TestWorkspaceSyncRefsResolveToWhatFleetsDeclare(t *testing.T) {
	cases := []struct {
		name       string
		namespace  string
		fleetID    string
		wantSecret string
		wantConfig string
	}{
		{
			// The live case. Note the namespace is NOT the fleet id after
			// ADR-088 -- it is tenant-<tenantId>-<appId> -- while the fleet's own
			// objects are named from the appId alone. Keying on {namespace} would
			// give tenant-org-fleet-a-sdk-secrets, a fourth name that does
			// not exist.
			name:       "a fleet on one tenant",
			namespace:  "tenant-org-fleet-a",
			fleetID:    "fleet-a",
			wantSecret: "fleet-a-sdk-secrets",
			wantConfig: "fleet-a-config",
		},
		{
			// A second tenant running a different app. The point of keying on
			// appId is that this needs no per-tenant configuration.
			name:       "a different fleet on another tenant",
			namespace:  "tenant-otherorg-fleet-b",
			fleetID:    "fleet-b",
			wantSecret: "fleet-b-sdk-secrets",
			wantConfig: "fleet-b-config",
		},
		{
			// An absent appId leaves the placeholder visible rather than
			// producing "-sdk-secrets". A reference to a Secret literally named
			// "-sdk-secrets" is another silent miss; an unexpanded {appId} shows
			// up in `kubectl describe` and is the better failure.
			// A real appId: a ULID, uppercase by specification. A reference
			// carrying it verbatim is rejected by the API server as not an
			// RFC 1123 subdomain, and the rejection lands on the pod, so the
			// EphemeralJob sits with an empty status looking like it is still
			// provisioning. The objects can only carry the lowercase form.
			name:       "uppercase ULID appId is lowercased to a valid reference",
			namespace:  "tenant-org-FLEET-A",
			fleetID:    "FLEET-A",
			wantSecret: "fleet-a-sdk-secrets",
			wantConfig: "fleet-a-config",
		},
		{
			// NO FALLBACK. An empty appId renders a bare suffix rather than
			// leaving `{appId}` in place, and that is deliberate: the guard that
			// produced the unexpanded form was a fallback wearing the costume of
			// safety, and either way the reference resolves to nothing. appId is
			// required by the CRD (MinLength=1), so this is not a state to degrade
			// into -- it is asserted only so the guard is not reintroduced as an
			// apparent oversight.
			name:       "an unresolvable fleet id renders bare, with no fallback",
			namespace:  "tenant-org-fleet-a",
			fleetID:    "",
			wantSecret: "-sdk-secrets",
			wantConfig: "-config",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveWorkspaceSyncSecret(tc.namespace, tc.fleetID); got != tc.wantSecret {
				t.Errorf("secret name = %q, want %q", got, tc.wantSecret)
			}
			if got := resolveWorkspaceSyncConfig(tc.namespace, tc.fleetID); got != tc.wantConfig {
				t.Errorf("configmap name = %q, want %q", got, tc.wantConfig)
			}
		})
	}
}

// A literal override must pass through untouched. A spoke with one fleet and a
// non-conventional name sets WORKSPACE_SYNC_SECRET directly, and expansion on a
// string with no placeholder has to be a no-op.
func TestWorkspaceSyncRefsPassLiteralsThrough(t *testing.T) {
	if got := expandWorkspaceSyncRef("some-literal-name", "tenant-x-y", "y"); got != "some-literal-name" {
		t.Errorf("literal was rewritten to %q", got)
	}
}
