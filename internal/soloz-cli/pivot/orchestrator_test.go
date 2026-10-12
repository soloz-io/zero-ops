package pivot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soloz-io/zero-ops/internal/platform/kms"
	"github.com/soloz-io/zero-ops/internal/soloz-cli/cluster"
)

// createMockKubectl creates an executable mock kubectl script in a temporary directory
// and prepends it to PATH for the duration of the test.
func createMockKubectl(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	kubectlPath := filepath.Join(dir, "kubectl")
	content := fmt.Sprintf("#!/bin/sh\n%s\n", script)
	if err := os.WriteFile(kubectlPath, []byte(content), 0755); err != nil {
		t.Fatalf("failed to write mock kubectl: %v", err)
	}
	t.Setenv("PATH", dir+string(filepath.ListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestCopyKMSCASecret_SecretboxSkipsCleanly(t *testing.T) {
	// In secretbox mode, copyKMSCASecret MUST return nil immediately without executing kubectl.
	// If kubectl is called, the mock script exits 1 and touches a canary file.
	canary := filepath.Join(t.TempDir(), "kubectl_was_called")
	createMockKubectl(t, fmt.Sprintf(`touch "%s"; exit 1`, canary))

	orch := &Orchestrator{
		ClusterName:         "test-cluster",
		Namespace:           "platform-capi",
		Encryption:          cluster.EncryptionSecretbox,
		BootstrapKubeconfig: "/dev/null",
	}

	err := orch.copyKMSCASecret(context.Background(), "/dev/null")
	if err != nil {
		t.Fatalf("expected nil error for secretbox mode, got: %v", err)
	}

	if _, statErr := os.Stat(canary); !os.IsNotExist(statErr) {
		t.Fatalf("kubectl was executed under secretbox mode, but should have been skipped immediately")
	}
}

func TestCopyKMSCASecret_DefaultEmptySkipsCleanly(t *testing.T) {
	// Empty encryption mode defaults to secretbox and must also skip cleanly without executing kubectl.
	canary := filepath.Join(t.TempDir(), "kubectl_was_called")
	createMockKubectl(t, fmt.Sprintf(`touch "%s"; exit 1`, canary))

	orch := &Orchestrator{
		ClusterName:         "test-cluster",
		Namespace:           "platform-capi",
		Encryption:          "",
		BootstrapKubeconfig: "/dev/null",
	}

	err := orch.copyKMSCASecret(context.Background(), "/dev/null")
	if err != nil {
		t.Fatalf("expected nil error for empty encryption mode, got: %v", err)
	}

	if _, statErr := os.Stat(canary); !os.IsNotExist(statErr) {
		t.Fatalf("kubectl was executed under empty encryption mode, but should have been skipped immediately")
	}
}

func TestCopyKMSCASecret_KMSv2MissingSecretHardFails(t *testing.T) {
	// Under kms-v2, if the KMS CA secret is NotFound, copyKMSCASecret MUST hard-fail
	// with a clear error indicating the refusal to pivot and unrenewable leaf after LeafValidity.
	createMockKubectl(t, `
case "$*" in
  *"get secret"*)
    echo 'Error from server (NotFound): secrets "test-cluster-kms-ca" not found' >&2
    exit 1
    ;;
  *)
    echo "unexpected command: $@" >&2
    exit 1
    ;;
esac
`)

	orch := &Orchestrator{
		ClusterName: "test-cluster",
		Namespace:   "platform-capi",
		Encryption:  cluster.EncryptionKMSv2,
	}

	err := orch.copyKMSCASecret(context.Background(), "/dev/null")
	if err == nil {
		t.Fatalf("expected error when KMS CA secret is not found under kms-v2, got nil")
	}

	errMsg := err.Error()
	expectedPhrases := []string{
		"KMS CA secret test-cluster-kms-ca not found on bootstrap cluster",
		"refusing to pivot because the hub would have no CA and the leaf would be unrenewable",
		fmt.Sprintf("%v", kms.LeafValidity),
	}

	for _, phrase := range expectedPhrases {
		if !strings.Contains(errMsg, phrase) {
			t.Errorf("error message missing expected phrase %q\ngot error: %s", phrase, errMsg)
		}
	}
}

func TestCopyKMSCASecret_KMSv2Success(t *testing.T) {
	// Under kms-v2, if the KMS CA secret exists, copyKMSCASecret cleans metadata and applies to mgmt cluster.
	secretJSON := `{
		"apiVersion": "v1",
		"kind": "Secret",
		"metadata": {
			"name": "test-cluster-kms-ca",
			"namespace": "platform-capi",
			"resourceVersion": "9999",
			"uid": "test-uid-1234"
		},
		"data": {
			"tls.crt": "dGVzdC1jcnQ=",
			"tls.key": "dGVzdC1rZXk="
		}
	}`

	appliedPayloadFile := filepath.Join(t.TempDir(), "applied_secret.json")

	createMockKubectl(t, fmt.Sprintf(`
case "$*" in
  *"get secret"*)
    cat << 'EOF'
%s
EOF
    exit 0
    ;;
  *"apply"*)
    cat > "%s"
    exit 0
    ;;
  *)
    echo "unexpected command: $@" >&2
    exit 1
    ;;
esac
`, secretJSON, appliedPayloadFile))

	orch := &Orchestrator{
		ClusterName: "test-cluster",
		Namespace:   "platform-capi",
		Encryption:  cluster.EncryptionKMSv2,
	}

	err := orch.copyKMSCASecret(context.Background(), "/dev/null")
	if err != nil {
		t.Fatalf("expected successful copy under kms-v2, got: %v", err)
	}

	appliedData, err := os.ReadFile(appliedPayloadFile)
	if err != nil {
		t.Fatalf("failed to read applied secret: %v", err)
	}

	appliedStr := string(appliedData)
	if strings.Contains(appliedStr, "resourceVersion") {
		t.Errorf("expected resourceVersion to be stripped from applied secret, but was present")
	}
	if strings.Contains(appliedStr, "test-uid-1234") {
		t.Errorf("expected uid to be stripped from applied secret, but was present")
	}
	if !strings.Contains(appliedStr, "dGVzdC1jcnQ=") {
		t.Errorf("expected tls.crt data to be present in applied secret")
	}
}
