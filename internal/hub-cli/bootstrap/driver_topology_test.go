package bootstrap

import (
	"strings"
	"testing"

	"github.com/soloz-io/zero-ops/internal/hub-cli/cluster"
)

// Hub topology is keyed on the PROVIDER. Choosing --provider=hybrid is itself the
// statement that worker capacity comes from home-lab hardware, so the hub runs a
// single Hetzner control-plane node and nothing else.
func TestHybridDriverProducesSingleNodeHub(t *testing.T) {
	d := &HybridDriver{Driver: &HetznerDriver{Region: "hel1", OS: "ubuntu", NetworkCIDR: "10.0.0.0/16"}}

	var cfg cluster.Config
	d.PopulateClusterConfig(&cfg)

	if cfg.WorkerReplicas != 0 {
		t.Errorf("WorkerReplicas = %d, want 0 (hybrid hub workers are home-lab nodes)", cfg.WorkerReplicas)
	}
	if !cfg.ControlPlaneSchedulable {
		t.Error("ControlPlaneSchedulable = false with 0 workers: the hub would have nowhere to schedule platform workloads")
	}
	if cfg.CiliumOperatorReplicas != 1 {
		t.Errorf("CiliumOperatorReplicas = %d, want 1 (hostPorts stop two replicas sharing one node)", cfg.CiliumOperatorReplicas)
	}
}

// The pure-Hetzner path is the tested one and must be untouched by any of the
// hybrid work: real workers, a tainted control plane, and no operator rescaling.
func TestHetznerDriverKeepsMultiNodeHub(t *testing.T) {
	d := &HetznerDriver{Region: "hel1", OS: "ubuntu", NetworkCIDR: "10.0.0.0/16"}

	var cfg cluster.Config
	d.PopulateClusterConfig(&cfg)

	if cfg.WorkerReplicas != 2 {
		t.Errorf("WorkerReplicas = %d, want 2 (unchanged Hetzner hub)", cfg.WorkerReplicas)
	}
	if cfg.ControlPlaneSchedulable {
		t.Error("ControlPlaneSchedulable = true: the Hetzner hub must keep its control-plane taint")
	}
	if cfg.CiliumOperatorReplicas != 0 {
		t.Errorf("CiliumOperatorReplicas = %d, want 0 (leave the shared addon at its HA default of 2)", cfg.CiliumOperatorReplicas)
	}
}

// Zero workers and a tainted control plane is the combination that hangs a
// bootstrap with every pod Pending, so the two settings must never drift apart.
func TestZeroWorkersImpliesSchedulableControlPlane(t *testing.T) {
	for name, d := range map[string]interface {
		PopulateClusterConfig(*cluster.Config)
	}{
		"hybrid":  &HybridDriver{Driver: &HetznerDriver{Region: "hel1", OS: "ubuntu", NetworkCIDR: "10.0.0.0/16"}},
		"hetzner": &HetznerDriver{Region: "hel1", OS: "ubuntu", NetworkCIDR: "10.0.0.0/16"},
	} {
		var cfg cluster.Config
		d.PopulateClusterConfig(&cfg)
		if cfg.WorkerReplicas == 0 && !cfg.ControlPlaneSchedulable {
			t.Errorf("%s: 0 workers with a tainted control plane — the hub cannot schedule anything", name)
		}
	}
}

// The rescale must hit the cilium-operator Deployment only. The addon also
// carries the cilium DaemonSet and other resources; a blanket replacement would
// corrupt them.
func TestScaleCiliumOperatorTargetsOnlyTheOperator(t *testing.T) {
	const manifest = `apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: cilium
spec:
  selector:
    matchLabels:
      k8s-app: cilium
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cilium-operator
spec:
  replicas: 2
  selector:
    matchLabels:
      io.cilium/app: operator
`

	got := cluster.ScaleCiliumOperatorForTest(manifest, 1)

	if !strings.Contains(got, "replicas: 1") {
		t.Errorf("operator was not rescaled:\n%s", got)
	}
	if strings.Contains(got, "replicas: 2") {
		t.Errorf("a replicas: 2 survived the rewrite:\n%s", got)
	}
	if !strings.Contains(got, "kind: DaemonSet") || !strings.Contains(got, "k8s-app: cilium") {
		t.Errorf("the DaemonSet was damaged:\n%s", got)
	}
}

// replicas <= 0 means "leave it alone" — the multi-node default.
func TestScaleCiliumOperatorNoOpOnZero(t *testing.T) {
	const manifest = "kind: Deployment\nmetadata:\n  name: cilium-operator\nspec:\n  replicas: 2\n"
	if got := cluster.ScaleCiliumOperatorForTest(manifest, 0); got != manifest {
		t.Errorf("manifest changed when replicas was 0:\n%s", got)
	}
}
