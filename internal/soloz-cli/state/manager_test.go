package state

import (
	"path/filepath"
	"strings"
	"testing"
)

// A tenant's bootstrap state lives outside anything the cluster reconciles.
//
// It was under clusters/<name>/generated/, which an Application of its own
// applies as Kubernetes manifests -- so the state file was sitting in a
// directory whose contract is "objects to apply". ArgoCD ignores a file it
// cannot parse as one, which is why this was harmless and would not have stayed
// harmless.
func TestTenantStateIsOutsideWhatTheClusterReconciles(t *testing.T) {
	m := NewTenantStateManager("/repo", "acme-hub")

	if strings.Contains(m.statePath, "/generated/") {
		t.Errorf("bootstrap state must not sit in a reconciled directory, got %q",
			m.statePath)
	}
	if !strings.HasPrefix(m.statePath, filepath.Join("/repo", ".state", "bootstrap")) {
		t.Errorf("state belongs under .state/bootstrap, namespaced by the command "+
			"that writes it, got %q", m.statePath)
	}
	if !strings.HasSuffix(m.statePath, "acme-hub.json") {
		t.Errorf("state is per cluster, got %q", m.statePath)
	}
}
