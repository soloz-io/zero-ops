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
		Driver:        &HetznerDriver{Region: "hel1", OS: "ubuntu", NetworkCIDR: "10.0.0.0/16"},
		OnPremEnabled: true,
	}

	var cfg cluster.Config
	d.PopulateClusterConfig(&cfg)

	if cfg.WorkerReplicas != 0 {
		t.Errorf("WorkerReplicas = %d, want 0 (hybrid hub workers are home-lab nodes)", cfg.WorkerReplicas)
	}
	// With home workers the control plane KEEPS its taint: ADR-046 §11 places every
	// platform workload on worker nodes, and the on-prem-join phase guarantees
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
			Driver:        &HetznerDriver{Region: "hel1", OS: "ubuntu", NetworkCIDR: "10.0.0.0/16"},
			OnPremEnabled: true,
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
		Driver:        &HetznerDriver{Region: "hel1", OS: "ubuntu", NetworkCIDR: "10.0.0.0/16"},
		OnPremEnabled: false,
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

// A development box provisions no cloud workers (ADR-075): that environment's
// capacity comes from the tenant's own hardware. Staging and production keep
// theirs, because the argument is about what a disposable environment is worth,
// not about where production data belongs.
func TestHetznerWorkerReplicasByEnvironment(t *testing.T) {
	for env, want := range map[string]int{
		"dev":  0,
		"stg":  2,
		"prod": 2,
		// An unset environment is not a development box. Defaulting it to zero
		// would silently strip workers from any caller that had not been updated.
		"": 2,
	} {
		d := &HetznerDriver{Environment: env}
		if got := d.PlannedWorkerReplicas(); got != want {
			t.Errorf("environment %q: PlannedWorkerReplicas() = %d, want %d", env, got, want)
		}
	}
}

// Hybrid provisions none in every environment: the cell exists so capacity comes
// from the tenant's own hardware.
func TestHybridWorkerReplicasAlwaysZero(t *testing.T) {
	for _, env := range []string{"dev", "stg", "prod", ""} {
		d := &HybridDriver{Driver: &HetznerDriver{Environment: env}}
		if got := d.PlannedWorkerReplicas(); got != 0 {
			t.Errorf("environment %q: hybrid PlannedWorkerReplicas() = %d, want 0", env, got)
		}
	}
}

// A box with no cloud workers and no on-prem nodes has no worker capacity at all,
// and the control plane does not make up the difference: it keeps its taint, and
// every platform workload pins node-role.kubernetes.io/worker, which a
// control-plane node does not carry (ADR-014). Such a box comes up with its
// control plane Ready and everything else Pending, roughly ninety minutes in.
//
// The check used to key on the provider being hybrid. Development boxes now
// provision no cloud workers on hetzner too (ADR-075), so it keys on capacity.
func TestBootstrapRefusesABoxWithNoWorkerCapacity(t *testing.T) {
	cases := []struct {
		name     string
		provider Provider
		env      string
		wantErr  bool
	}{
		{
			name:     "dev hetzner without on-prem has nothing to schedule on",
			provider: NewCloudProvider(&HetznerDriver{Environment: "dev"}, "c", false),
			env:      "dev",
			wantErr:  true,
		},
		{
			name:     "prod hetzner provisions cloud workers",
			provider: NewCloudProvider(&HetznerDriver{Environment: "prod"}, "c", false),
			env:      "prod",
			wantErr:  false,
		},
		{
			name: "dev hetzner with on-prem has capacity",
			provider: NewCloudProvider(
				&HybridDriver{Driver: &HetznerDriver{Environment: "dev"}, OnPremEnabled: true}, "c", false),
			env:     "dev",
			wantErr: false,
		},
		{
			name: "hybrid without on-prem has nothing at all",
			provider: NewCloudProvider(
				&HybridDriver{Driver: &HetznerDriver{Environment: "dev"}}, "c", false),
			env:     "dev",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		o := &Orchestrator{Provider: tc.provider, EnvironmentSlug: tc.env}
		err := o.checkPlacementCapacityRequested()
		if tc.wantErr && err == nil {
			t.Errorf("%s: expected a refusal, got none", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: expected no refusal, got: %v", tc.name, err)
		}
	}
}

// An explicit count wins over the environment's default, including an explicit
// zero. This is what lets a tenant with no hardware run a dev box on cloud
// capacity, and a tenant with plenty run staging on none.
func TestExplicitWorkerCountOverridesTheEnvironment(t *testing.T) {
	zero, four := 0, 4
	if got := (&HetznerDriver{Environment: "prod", WorkerReplicas: &zero}).PlannedWorkerReplicas(); got != 0 {
		t.Errorf("explicit zero in prod = %d, want 0", got)
	}
	if got := (&HetznerDriver{Environment: "dev", WorkerReplicas: &four}).PlannedWorkerReplicas(); got != 4 {
		t.Errorf("explicit four in dev = %d, want 4", got)
	}
	// And a driver that never had the field set still gets the environment's own
	// answer. An int sentinel got this wrong: Go's zero value read as an explicit
	// zero, so every environment silently provisioned nothing.
	if got := (&HetznerDriver{Environment: "prod"}).PlannedWorkerReplicas(); got != 2 {
		t.Errorf("unset override in prod = %d, want 2", got)
	}
}
