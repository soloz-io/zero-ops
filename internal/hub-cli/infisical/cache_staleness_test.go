package infisical

import "testing"

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
