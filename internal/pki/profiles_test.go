package pki

import "testing"

// Every profile named by an infisical-issuer ClusterIssuer must exist, or
// certificates using that issuer cannot be issued at all. These two are live on the
// hub today; signing-keys being absent from the operator's old list is exactly what
// left argocd-agent-principal unable to start.
func TestIssuerReferencedProfilesArePresent(t *testing.T) {
	for _, slug := range []string{"infrastructure-services", "signing-keys"} {
		found := false
		for _, p := range RequiredProfiles {
			if p.Slug == slug {
				found = true
			}
		}
		if !found {
			t.Errorf("profile %q is named by a ClusterIssuer but missing from RequiredProfiles", slug)
		}
	}
}

// The TTL is a server-side cap. argocd-agent-principal-tls,
// argocd-principal-internal-tls and argocd-agent-resource-proxy-tls all request
// duration 2160h (90 days) through infrastructure-services and are live with a full
// 90-day window. A cap below that does not fail at apply time — it breaks renewal
// later, which is how this would escape review.
func TestInfrastructureServicesCapCoversNinetyDayCertificates(t *testing.T) {
	for _, p := range RequiredProfiles {
		if p.Slug == "infrastructure-services" && p.TTLDays < 90 {
			t.Errorf("infrastructure-services TTL cap is %d days; the argocd-principal certificates request 90", p.TTLDays)
		}
	}
}

// argocd-agent-jwt requests duration 43800h (5 years).
func TestSigningKeysCapCoversFiveYearJWTKey(t *testing.T) {
	for _, p := range RequiredProfiles {
		if p.Slug == "signing-keys" && p.TTLDays < 1825 {
			t.Errorf("signing-keys TTL cap is %d days; argocd-agent-jwt requests ~1825", p.TTLDays)
		}
	}
}

func TestNoDuplicateSlugs(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range RequiredProfiles {
		if seen[p.Slug] {
			t.Errorf("duplicate profile slug %q", p.Slug)
		}
		seen[p.Slug] = true
	}
}
