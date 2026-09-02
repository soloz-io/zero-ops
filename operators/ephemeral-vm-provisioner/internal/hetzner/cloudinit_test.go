package hetzner

import (
	"testing"
)

func TestGenerateCloudInit_ValidYAML(t *testing.T) {
	img := "ghcr.io/soloz-io/remotion-headless:latest"
	modelsVol := false
	cmd := []string{"node", "scripts/worker-entrypoint.js"}
	cb := "https://example.com/api/v1/webhooks/resume?token=testtoken"

	payload := JobPayload{
		Image:        &img,
		ModelsVolume: &modelsVol,
		Command:      cmd,
		CallbackURL:  &cb,
		Input: map[string]interface{}{
			"hello": "world",
		},
	}

	cloudInit, err := GenerateCloudInit(payload, "01TESTJOB")
	if err != nil {
		t.Fatalf("GenerateCloudInit returned error: %v", err)
	}

	if len(cloudInit) == 0 {
		t.Fatalf("GenerateCloudInit returned empty output")
	}

	t.Logf("Generated cloudInit:\n%s", cloudInit)
}
