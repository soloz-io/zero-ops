// Package pki holds the platform's certificate-profile definitions.
//
// This package exists because the same list was previously hardcoded twice — once
// in the CLI (PKI_READY, which ADR-042 makes CLI-owned) and once in the
// hub-operator's self-healing reconcile — and the two drifted apart until they
// shared exactly ONE entry:
//
//	CLI                       operator
//	argocd-bootstrap          infrastructure-services
//	infrastructure-services   argocd-principals
//	database-clients          argocd-agents
//	service-mesh
//	human-access
//	signing-keys
//
// The operator's comment claimed the lists matched. The consequences were real and
// pointed nowhere near PKI: the operator never created `signing-keys`, so a rebuilt
// Infisical could not issue the argocd-agent-jwt certificate, and
// argocd-agent-principal sat in ContainerCreating on a missing Secret while
// cert-manager reported only "Certificate template with name signing-keys not
// found". Meanwhile `argocd-principals` and `argocd-agents` are referenced by no
// issuer, no Certificate and no ADR — the operator was creating profiles nothing
// consumes.
//
// Both consumers now derive from RequiredProfiles. They still use their own clients
// to talk to Infisical; only the data is shared, which is the seam that was missing.
package pki

// Profile is one certificate profile in the Fleet Intermediate CA.
//
// TTLDays is a SERVER-SIDE CAP on the lifetime of any certificate issued through
// the profile. It is not the certificate's lifetime — cert-manager's
// Certificate.spec.duration decides that, subject to this cap. A cap below the
// requested duration does not fail loudly at apply time; it silently shortens or
// refuses issuance later, at renewal.
type Profile struct {
	Slug    string
	TTLDays int
}

// RequiredProfiles is the authoritative set. Every profile named by an
// infisical-issuer ClusterIssuer MUST appear here, or certificates using that
// issuer cannot be issued at all.
//
// Currently consumed by an issuer:
//   - infrastructure-services  infisical-fleet-issuer   (hub + spoke)
//   - signing-keys             infisical-signing-issuer (argocd-agent-jwt)
//
// The remainder are mandated by ADR-035's profile table and are provisioned ahead
// of the issuers that will use them.
//
// NOTE — TTL conflict with ADR-035, deliberately unresolved here:
// ADR-035 caps Infrastructure Services at 24h, but the three argocd-principal
// certificates request `duration: 2160h` (90 days) through that profile and are
// live on the hub today with a full 90-day validity window. 90 is therefore the
// value that keeps the platform working, and lowering it to 1 would break renewal
// of certificates that are currently healthy. Reconciling the ADR's intent
// (short-lived certificates) with the manifests' 90-day requests is a security
// posture decision, not a refactor — see the ADR-035 note.
var RequiredProfiles = []Profile{
	{Slug: "argocd-bootstrap", TTLDays: 3},         // 72h bootstrap exception (ADR-035)
	{Slug: "infrastructure-services", TTLDays: 90}, // see TTL conflict note above
	{Slug: "database-clients", TTLDays: 1},         // 4h cap, rounded up (ADR-035)
	{Slug: "service-mesh", TTLDays: 1},             // 1h cap, rounded up (ADR-035)
	{Slug: "human-access", TTLDays: 1},             // 15m cap, rounded up (ADR-035)
	{Slug: "signing-keys", TTLDays: 3650},          // long-lived asymmetric JWT signing key
}
