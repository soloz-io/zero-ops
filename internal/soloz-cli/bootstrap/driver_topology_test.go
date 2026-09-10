package bootstrap

import (
	"strings"
	"testing"

	"github.com/soloz-io/zero-ops/internal/soloz-cli/cluster"
)

// Hub topology is keyed on the PROVIDER. Choosing --provider=hybrid is itself the
// statement that worker capacity comes from home-lab hardware, so the hub runs a
// single Hetzner control-plane node and nothing else.
func TestHybridDriverProducesSingleNodeHub(t *testing.T) {
	d := &HybridDriver{
		Driver:            &HetznerDriver{Region: "hel1", OS: "ubuntu", NetworkCIDR: "10.0.0.0/16"},
		HomeWorkerEnabled: true,
	}

	var cfg cluster.Config
	d.PopulateClusterConfig(&cfg)

	if cfg.WorkerReplicas != 0 {
		t.Errorf("WorkerReplicas = %d, want 0 (hybrid hub workers are home-lab nodes)", cfg.WorkerReplicas)
	}
	// With home workers the control plane KEEPS its taint: ADR-046 §11 places every
	// platform workload on worker nodes, and the home-worker-join phase guarantees
	// one exists before boundary-01.
	if cfg.ControlPlaneSchedulable {
		t.Error("ControlPlaneSchedulable = true with home workers enabled: the control plane must keep its ADR-014 taint")
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
	homeWorkers := map[string]bool{"hybrid": true, "hetzner": false}
	for name, d := range map[string]interface {
		PopulateClusterConfig(*cluster.Config)
	}{
		"hybrid": &HybridDriver{
			Driver:            &HetznerDriver{Region: "hel1", OS: "ubuntu", NetworkCIDR: "10.0.0.0/16"},
			HomeWorkerEnabled: true,
		},
		"hetzner": &HetznerDriver{Region: "hel1", OS: "ubuntu", NetworkCIDR: "10.0.0.0/16"},
	} {
		var cfg cluster.Config
		d.PopulateClusterConfig(&cfg)
		if cfg.WorkerReplicas == 0 && !cfg.ControlPlaneSchedulable && !homeWorkers[name] {
			t.Errorf("%s: 0 Hetzner workers, a tainted control plane and no home workers — nothing can schedule", name)
		}
	}
}

// A hybrid cell with NO home workers has no other node at all, so the control
// plane has to carry the platform.
func TestHybridWithoutHomeWorkersUntaintsControlPlane(t *testing.T) {
	d := &HybridDriver{
		Driver:            &HetznerDriver{Region: "hel1", OS: "ubuntu", NetworkCIDR: "10.0.0.0/16"},
		HomeWorkerEnabled: false,
	}

	var cfg cluster.Config
	d.PopulateClusterConfig(&cfg)

	if cfg.WorkerReplicas != 0 {
		t.Errorf("WorkerReplicas = %d, want 0", cfg.WorkerReplicas)
	}
	if !cfg.ControlPlaneSchedulable {
		t.Error("ControlPlaneSchedulable = false with no workers of any kind: the hub could not schedule anything")
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
