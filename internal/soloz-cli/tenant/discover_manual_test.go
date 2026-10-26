package tenant

import (
	"context"
	"os"
	"testing"
)

// Opt-in: reaches GitHub. Run with SOLOZ_DISCOVER_ORG set.
func TestDiscoverAgainstGitHub(t *testing.T) {
	org := os.Getenv("SOLOZ_DISCOVER_ORG")
	gitops := os.Getenv("SOLOZ_DISCOVER_GITOPS")
	if org == "" || gitops == "" {
		t.Skip("set SOLOZ_DISCOVER_ORG and SOLOZ_DISCOVER_GITOPS")
	}
	repos, err := ReposNeedingGitopsToken(context.Background(), org, gitops)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("repositories needing the credential: %v", repos)
}
