package bootstrap

import (
	"os"
	"strings"
	"testing"

	"github.com/soloz-io/zero-ops/internal/soloz-cli/cluster"
)

// W4 / T36: Gate the pivot CA copy on encryption mode, not Secret existence.
//
// Under kms-v2, the KMS CA secret MUST exist on the bootstrap cluster and be copied
// to the hub before the bootstrap kind cluster is destroyed.
// Both pivot.Orchestrator construction sites in provider_cloud.go must explicitly
// populate Encryption so that copyKMSCASecret knows whether to skip (secretbox)
// or hard-fail on missing CA (kms-v2).

func TestBothPivotOrchestratorConstructionSitesSetEncryption(t *testing.T) {
	src, err := os.ReadFile("provider_cloud.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	// Check PivotMove construction site (~line 294)
	pivotMoveIdx := strings.Index(body, "func (p *CloudProvider) PivotMove(")
	if pivotMoveIdx < 0 {
		t.Fatal("PivotMove is gone")
	}
	pivotMoveBody := body[pivotMoveIdx:]
	pivotMoveBody = pivotMoveBody[:strings.Index(pivotMoveBody, "\n}\n")]

	if !strings.Contains(pivotMoveBody, "Encryption:") {
		t.Error("PivotMove does not set Encryption on pivot.Orchestrator; under kms-v2 CA copy would not be gated properly")
	}

	// Check PivotReady construction site (~line 316)
	pivotReadyIdx := strings.Index(body, "func (p *CloudProvider) PivotReady(")
	if pivotReadyIdx < 0 {
		t.Fatal("PivotReady is gone")
	}
	pivotReadyBody := body[pivotReadyIdx:]
	pivotReadyBody = pivotReadyBody[:strings.Index(pivotReadyBody, "\n}\n")]

	if !strings.Contains(pivotReadyBody, "Encryption:") {
		t.Error("PivotReady does not set Encryption on pivot.Orchestrator; under kms-v2 CA copy would not be gated properly")
	}
}

func TestProvisionManagementClusterRecordsEncryption(t *testing.T) {
	src, err := os.ReadFile("provider_cloud.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)

	pmcIdx := strings.Index(body, "func (p *CloudProvider) ProvisionManagementCluster(")
	if pmcIdx < 0 {
		t.Fatal("ProvisionManagementCluster is gone")
	}
	pmcBody := body[pmcIdx:]
	pmcBody = pmcBody[:strings.Index(pmcBody, "\n}\n")]

	if !strings.Contains(pmcBody, "p.encryption = cfg.Encryption") {
		t.Error("ProvisionManagementCluster does not record cfg.Encryption on CloudProvider")
	}
}

func TestPivotConfigCarriesEncryption(t *testing.T) {
	cfg := &PivotConfig{
		ClusterName: "test-cluster",
		Encryption:  cluster.EncryptionKMSv2,
	}

	if cfg.Encryption != cluster.EncryptionKMSv2 {
		t.Errorf("expected PivotConfig to carry EncryptionKMSv2, got %v", cfg.Encryption)
	}
}

func TestCloudProviderSetEncryption(t *testing.T) {
	cp := &CloudProvider{
		clusterName: "test-cluster",
	}
	cp.SetEncryption(cluster.EncryptionKMSv2)
	if cp.Encryption() != cluster.EncryptionKMSv2 {
		t.Errorf("expected EncryptionKMSv2, got %v", cp.Encryption())
	}
}
