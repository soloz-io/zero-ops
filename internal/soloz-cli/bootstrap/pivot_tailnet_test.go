package bootstrap

import (
	"context"
	"os"
	"strings"
	"testing"
)

// The tailnet credential must be re-staged on the pivoted hub.
//
// It is written at capi-init into the BOOTSTRAP cluster, because the hub's own
// ClusterClass reads it through contentFrom.secret while the hub Cluster is
// created. `clusterctl move` carries CAPI-owned objects; a plain Secret is not
// one, so it does not survive the pivot -- and nothing notices, because the hub
// is already built.
//
// The first object to need it on the hub is the first SPOKE:
//
//	BootstrapReady False  secret not found: platform-capi/tailscale-hybrid-psk
//
// Every spoke machine then sits Pending for ever, control plane included, so the
// spoke has no API server and the burst autoscaler crash-loops against an
// endpoint that will never answer.
func TestTheTailnetCredentialIsRestagedAfterPivot(t *testing.T) {
	src, err := os.ReadFile("provider_cloud.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func (p *CloudProvider) PivotReady(")
	if start < 0 {
		t.Fatal("PivotReady is gone")
	}
	fn := body[start:]
	fn = fn[:strings.Index(fn, "\n}\n")]
	if !strings.Contains(fn, "stageTailnetCredentialOnHub") {
		t.Error("PivotReady does not re-stage the tailnet credential; every spoke " +
			"created on this hub will fail to render its KubeadmConfig")
	}
}

// Staged into platform-capi, which is where the ClusterClass resolves it.
func TestTheTailnetCredentialIsStagedIntoTheCAPINamespace(t *testing.T) {
	src, err := os.ReadFile("provider_cloud.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func (p *CloudProvider) stageTailnetCredentialOnHub(")
	if start < 0 {
		t.Fatal("stageTailnetCredentialOnHub is gone")
	}
	fn := body[start:]
	fn = fn[:strings.Index(fn, "\n}\n")]
	if !strings.Contains(fn, "constants.NamespaceCAPI") {
		t.Error("the credential is not staged into the CAPI namespace, which is the " +
			"only place the ClusterClass looks for it")
	}
}

// A driver with no tailnet story is not in error; it simply does not implement
// the method. Asserting the Hetzner driver does keeps the wiring honest.
func TestTheHetznerDriverExposesTheStager(t *testing.T) {
	var _ interface {
		StageTailscaleCredentials(context.Context, string, string) error
	} = (*HetznerDriver)(nil)
}
