package tenant

import (
	"fmt"
	"strings"
)

// tier says why a credential is asked for, which decides what happens when it
// is absent.
//
// A tenant should supply credentials only for dependencies that are inherently
// theirs, or that the platform cannot responsibly provide on their behalf.
// Everything else is an implementation detail of ours and must not become an
// onboarding requirement -- which is what happened when these were all made
// mandatory together on the grounds that the hub was not "healthy" without
// them. That reasoning turns whichever backend a component happens to be
// configured for into an architectural obligation on the tenant.
type tier int

const (
	// tierCapability: the tenant selected something that needs it. Refused,
	// because the capability cannot work without it and enabling it anyway
	// produces a box that looks configured and is not.
	tierCapability tier = iota
	// tierDestination: a capability the platform provides that has to send
	// somewhere the tenant chooses. Reported, never refused: the capability
	// still ships and still runs, it simply has no remote destination until one
	// is given, and a tenant may legitimately want none.
	//
	// Deliberately NOT a subscription boundary. No capability is withheld from a
	// tenant who does not pay: the platform is theirs to run in full, and what a
	// subscription buys is the platform being accountable for it. Support
	// telemetry is a separate channel with its own enrolment, and conflating the
	// two here would make a tenant's private monitoring account look like a
	// licence check.
	tierDestination
)

type platformCredential struct {
	// Secret is the name in the tenant's repository, the environment variable
	// Day-0 reads, and the key the operator uploads under. One name, because
	// three spellings is another place for them to disagree.
	Secret string
	Prompt string
	Tier   tier
	// Capability is what selecting it commits the tenant to, named in whichever
	// message the tier produces.
	Capability string
	Field      func(*Secrets) *string
	// Needed reports whether this box asks for it at all. A credential nobody's
	// configuration calls for is not missing.
	Needed func(Spec, Secrets) bool
}

func platformCredentials() []platformCredential {
	// Backups are the platform's responsibility because the platform ships the
	// database, and a backup inside the failure domain it protects is not one.
	// So the target is outside the box and therefore the tenant's to provide --
	// deliberately not in-cluster object storage, which would remove the
	// credential by removing the property that makes the backup worth having.
	//
	// Not in development or ephemeral environments: those boxes are created to
	// be destroyed, and requiring durable object storage for one is asking a
	// tenant to provision real infrastructure to run a test.
	backupsWanted := func(s Spec, _ Secrets) bool {
		return s.Environment != "dev" && s.Environment != "ephemeral"
	}
	// Only when the tenant runs images the platform cannot pull anonymously.
	// ADR-066 makes workloads theirs, so this credential is irreducibly theirs
	// too -- and equally, a tenant running public images should never be asked.
	privateImages := func(s Spec, _ Secrets) bool { return s.PrivateRegistry }
	always := func(Spec, Secrets) bool { return true }

	return []platformCredential{
		{
			Secret: "S3_ACCESS_KEY_ID", Tier: tierCapability,
			Capability: "backups of the platform database",
			Prompt:     "S3_ACCESS_KEY_ID (object storage for database backups): ",
			Field:      func(s *Secrets) *string { return &s.S3AccessKeyID },
			Needed:     backupsWanted,
		},
		{
			Secret: "S3_SECRET_ACCESS_KEY", Tier: tierCapability,
			Capability: "backups of the platform database",
			Prompt:     "S3_SECRET_ACCESS_KEY: ",
			Field:      func(s *Secrets) *string { return &s.S3SecretAccessKey },
			Needed:     backupsWanted,
		},
		{
			Secret: "GHCR_USERNAME", Tier: tierCapability,
			Capability: "pulling your own private images",
			Prompt:     "GHCR_USERNAME (to pull your private images): ",
			Field:      func(s *Secrets) *string { return &s.GHCRUsername },
			Needed:     privateImages,
		},
		{
			Secret: "GHCR_TOKEN", Tier: tierCapability,
			Capability: "pulling your own private images",
			Prompt:     "GHCR_TOKEN (a token with read:packages): ",
			Field:      func(s *Secrets) *string { return &s.GHCRToken },
			Needed:     privateImages,
		},
		{
			Secret: "GRAFANA_CLOUD_API_KEY", Tier: tierDestination,
			Capability: "shipping this box's metrics and logs to your own Grafana Cloud",
			Prompt:     "GRAFANA_CLOUD_API_KEY (optional; where YOUR metrics go): ",
			Field:      func(s *Secrets) *string { return &s.GrafanaCloudAPIKey },
			Needed:     always,
		},
		{
			Secret: "GRAFANA_CLOUD_PROMETHEUS_URL", Tier: tierDestination,
			Capability: "shipping this box's metrics and logs to your own Grafana Cloud",
			Prompt:     "GRAFANA_CLOUD_PROMETHEUS_URL: ",
			Field:      func(s *Secrets) *string { return &s.GrafanaCloudPrometheusURL },
			Needed:     always,
		},
		{
			Secret: "GRAFANA_CLOUD_PROMETHEUS_USER", Tier: tierDestination,
			Capability: "shipping this box's metrics and logs to your own Grafana Cloud",
			Prompt:     "GRAFANA_CLOUD_PROMETHEUS_USER: ",
			Field:      func(s *Secrets) *string { return &s.GrafanaCloudPrometheusUser },
			Needed:     always,
		},
		{
			Secret: "GRAFANA_CLOUD_LOKI_URL", Tier: tierDestination,
			Capability: "shipping this box's metrics and logs to your own Grafana Cloud",
			Prompt:     "GRAFANA_CLOUD_LOKI_URL: ",
			Field:      func(s *Secrets) *string { return &s.GrafanaCloudLokiURL },
			Needed:     always,
		},
		{
			Secret: "GRAFANA_CLOUD_LOKI_USER", Tier: tierDestination,
			Capability: "shipping this box's metrics and logs to your own Grafana Cloud",
			Prompt:     "GRAFANA_CLOUD_LOKI_USER: ",
			Field:      func(s *Secrets) *string { return &s.GrafanaCloudLokiUser },
			Needed:     always,
		},
	}
}

func (s Secrets) missingInTier(spec Spec, t tier) []platformCredential {
	var missing []platformCredential
	for _, c := range platformCredentials() {
		if c.Tier != t || !c.Needed(spec, s) {
			continue
		}
		if strings.TrimSpace(*c.Field(&s)) == "" {
			missing = append(missing, c)
		}
	}
	return missing
}

// RequireSelectedCapabilities refuses a box that selected something it cannot run.
//
// Only what this box's own configuration calls for: a development box is not
// asked for object storage, and a tenant running public images is never asked
// for a registry credential. A credential nobody's configuration needs is not
// missing, and asking for it anyway is how an implementation detail becomes an
// onboarding requirement.
func (s Secrets) RequireSelectedCapabilities(spec Spec) error {
	missing := s.missingInTier(spec, tierCapability)
	if len(missing) == 0 {
		return nil
	}
	var names, why []string
	seen := map[string]bool{}
	for _, c := range missing {
		names = append(names, c.Secret)
		if !seen[c.Capability] {
			why = append(why, "  - "+c.Capability)
			seen[c.Capability] = true
		}
	}
	return fmt.Errorf("this box selected capabilities it has no credentials for:\n\n%s\n\n"+
		"Missing: %s\n\n"+
		"These are not platform defaults you can ignore -- they are the\n"+
		"dependencies of something this box is configured to do. Either supply\n"+
		"them, or turn the capability off and scaffold without it.",
		strings.Join(why, "\n"), strings.Join(names, ", "))
}

// ObservabilityDestination says where this box's telemetry goes, if anywhere.
//
// Reported, never refused, and carrying no consequence beyond itself. The
// observability stack ships to every tenant and runs either way; without a
// destination it has nowhere remote to send, which is a configuration a tenant
// may well want and is not a degraded platform.
//
// It says nothing about support. A tenant pays for the platform to be
// accountable for their box, not for permission to use it, so no capability is
// conditioned on that relationship and this message must not imply one.
func (s Secrets) ObservabilityDestination(spec Spec) string {
	if len(s.missingInTier(spec, tierDestination)) == 0 {
		return "Observability: metrics and logs ship to your Grafana Cloud."
	}
	return "Observability: no remote destination configured.\n" +
		"The stack still runs and still collects; nothing is withheld. Add a\n" +
		"destination whenever you want it shipped somewhere you can read it."
}
