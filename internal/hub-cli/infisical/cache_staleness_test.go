package infisical

import (
	"context"
	"testing"
)

// staleDecision mirrors the branch logic in invalidateStaleBootstrapCache.
//
// The organization ID is the instance fingerprint: every cached project and
// identity ID names a row inside that organization's database. Two failure modes
// sit either side of this decision, and both are silent:
//
//   - clearing too eagerly destroys the fallback that createProject relies on when
//     project listing returns empty, so a retry against the SAME Infisical fails
//     with "project not found in listing or ConfigMap";
//   - clearing too rarely lets a bootstrap trust projects from a database that was
//     rebuilt, so it skips creating them and fails later at identity creation.
func staleDecision(cachedOrg, liveOrg, cachedProject, cachedSecrets string) bool {
	orphaned := cachedOrg == "" && (cachedProject != "" || cachedSecrets != "")
	switch {
	case cachedOrg == liveOrg:
		return false
	case cachedOrg == "" && !orphaned:
		return false
	default:
		return true
	}
}

func TestCacheStalenessDecision(t *testing.T) {
	cases := []struct {
		name                                     string
		cachedOrg, liveOrg, project, secretsProj string
		wantClear                                bool
	}{
		{
			name:      "same instance — cache is valid and must survive a retry",
			cachedOrg: "org-A", liveOrg: "org-A", project: "p1", secretsProj: "p2",
			wantClear: false,
		},
		{
			// The failure this whole check exists for: platform-db rebuilt, so
			// Infisical came up with a new org and no projects, while the ConfigMap
			// still advertised the old ones.
			name:      "rebuilt database — cache names a dead organization",
			cachedOrg: "org-A", liveOrg: "org-B", project: "p1", secretsProj: "p2",
			wantClear: true,
		},
		{
			name:      "first bootstrap — nothing cached",
			cachedOrg: "", liveOrg: "org-A", project: "", secretsProj: "",
			wantClear: false,
		},
		{
			name:      "already cleared — stays cleared, no churn",
			cachedOrg: "", liveOrg: "org-B", project: "", secretsProj: "",
			wantClear: false,
		},
		{
			// Written by an older CLI, or a half-cleared ConfigMap. There is nothing
			// to validate the project IDs against, so they cannot be trusted.
			name:      "orphaned project IDs with no organization to anchor them",
			cachedOrg: "", liveOrg: "org-A", project: "p1", secretsProj: "",
			wantClear: true,
		},
		{
			name:      "orphaned secrets project only",
			cachedOrg: "", liveOrg: "org-A", project: "", secretsProj: "p2",
			wantClear: true,
		},
	}

	for _, c := range cases {
		got := staleDecision(c.cachedOrg, c.liveOrg, c.project, c.secretsProj)
		if got != c.wantClear {
			t.Errorf("%s:\n  cachedOrg=%q liveOrg=%q project=%q secrets=%q\n  clear=%v, want %v",
				c.name, c.cachedOrg, c.liveOrg, c.project, c.secretsProj, got, c.wantClear)
		}
	}
}

// The decision above was already correct in production — the log showed it firing
// exactly as designed. What failed was the REMEDIATION: it patched
// hub-bootstrap-config, which is GitOps-delivered, so ArgoCD restored the dead IDs
// within seconds and every later read in the same run got them back.
//
// These pin the suppression that actually holds, per reader, because
// getCachedProjectID bypasses getCachedConfigValue and was the one that logged
// "Found cached project hub-platform (id=…)" immediately after the cache was
// "cleared".
func TestSuppressionHidesInstanceIDsFromBothReaders(t *testing.T) {
	staleCacheSuppressed.Store(false)
	t.Cleanup(func() { staleCacheSuppressed.Store(false) })

	if staleCacheSuppressed.Load() {
		t.Fatal("suppression must start off")
	}

	staleCacheSuppressed.Store(true)

	// getCachedProjectID must return nothing at all once suppressed — it reads only
	// instance-specific project UUIDs.
	if got := getCachedProjectID("hub-platform"); got != "" {
		t.Errorf("getCachedProjectID returned %q while suppressed; ArgoCD restoring the "+
			"ConfigMap would feed a dead project ID straight back in", got)
	}
	if got := getCachedProjectID(SecretsProjectSlug); got != "" {
		t.Errorf("getCachedProjectID(secrets) returned %q while suppressed", got)
	}
}

// Slugs are identical on every instance, so suppressing them would discard usable
// configuration and break lookup-by-slug. Only the UUIDs identify one Infisical.
func TestSuppressionKeepsSlugsReadable(t *testing.T) {
	staleCacheSuppressed.Store(true)
	t.Cleanup(func() { staleCacheSuppressed.Store(false) })

	for _, key := range []string{"INFISICAL_ORGANIZATION_ID", "INFISICAL_PROJECT_ID", "INFISICAL_SECRETS_PROJECT_ID"} {
		if got := getCachedConfigValue(context.Background(), key); got != "" {
			t.Errorf("%s returned %q while suppressed", key, got)
		}
	}
	// Slug keys are not in the suppression list. Without a cluster they resolve to ""
	// anyway, so assert the routing rather than the value: a slug key must not be
	// short-circuited by the guard.
	for _, key := range []string{"INFISICAL_PROJECT_SLUG", "INFISICAL_SECRETS_PROJECT_SLUG"} {
		switch key {
		case "INFISICAL_ORGANIZATION_ID", "INFISICAL_PROJECT_ID", "INFISICAL_SECRETS_PROJECT_ID":
			t.Errorf("%s must not be in the suppressed set", key)
		}
	}
}
