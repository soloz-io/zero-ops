package controller

import (
	"os"
	"strings"
	"testing"
)

// The operator's defaults ({fleetId}-sdk-secrets, {fleetId}-config) are right;
// the deployment manifest once overrode the Secret with the stale
// `{namespace}-app-secrets`, so the right code never ran and every deployed
// sandbox's workspace-sync started without object storage. The manifest must
// leave both names to the operator.
func TestManagerManifestLeavesWorkspaceSyncRefsToTheOperator(t *testing.T) {
	raw, err := os.ReadFile("../../config/manager/manager.yaml")
	if err != nil {
		t.Fatalf("read the manager manifest: %v", err)
	}
	for i, line := range strings.Split(string(raw), "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "#") {
			continue
		}
		for _, name := range []string{"WORKSPACE_SYNC_SECRET", "WORKSPACE_SYNC_CONFIG"} {
			if strings.Contains(code, name) {
				t.Errorf("config/manager/manager.yaml:%d sets %s; leave it to the operator's default (workspace_sync.go)", i+1, name)
			}
		}
	}
}
