package health

// This file defines the BootstrapReadinessCheck interface (declared in
// waiter.go) implementations for each platform service layer. These
// are the canonical phase-readiness profiles used by bootstrap
// orchestrators and the init-secrets command.
//
// Why pre-defined phases?
//   - A new check (e.g., a new dependency) means adding one struct
//     to the appropriate phase, not editing multiple wait loops.
//   - Phase order encodes inter-service dependencies (CNPG →
//     PgBouncer → Infisical) so callers don't have to know which
//     checks depend on which.

import (
	"github.com/soloz-io/zero-ops/internal/hub-cli/constants"
)

// DataLayerReadiness is the readiness profile for the data layer
// (PostgreSQL via CloudNativePG). External services that depend on
// PostgreSQL connectivity should wait for this phase to pass before
// they start (or restart) their own pods.
//
// Order encodes dependencies:
//  1. CNPG cluster: primary instance is ready
//  2. PgBouncer pooler: connection-brokering layer is available
//
// Add new data-layer dependencies here, in the order they must be
// ready before downstream services start.
type DataLayerReadiness struct {
	// CNPGClusterName is the name of the CloudNativePG Cluster CR.
	// Default: "platform-db".
	CNPGClusterName string
	// PoolerName is the name of the CNPG Pooler CR.
	// Default: "platform-db-pooler".
	PoolerName string
	// Namespace is the namespace both CRs live in.
	// Default: platform-data.
	Namespace string
}

// PhaseName returns the phase identifier.
func (d *DataLayerReadiness) PhaseName() string { return "Data Layer" }

// Checkers returns the ordered list of HealthChecker instances.
func (d *DataLayerReadiness) Checkers() []HealthChecker {
	clusterName := d.CNPGClusterName
	if clusterName == "" {
		clusterName = "platform-db"
	}
	poolerName := d.PoolerName
	if poolerName == "" {
		poolerName = "platform-db-pooler"
	}
	namespace := d.Namespace
	if namespace == "" {
		namespace = constants.NamespaceData
	}
	cnpg := NewCNPGClusterHealth(clusterName, namespace)
	// The Application that creates this cluster. Naming it lets the check
	// distinguish "not created yet" from "nothing is going to create it": an
	// Application ArgoCD reports Synced and Healthy while managing no resources
	// renders nothing, and waiting thirty minutes ends where it began.
	cnpg.OwnerApplication = "platform-database"
	cnpg.OwnerNamespace = constants.NamespaceOps

	return []HealthChecker{
		cnpg,
		NewPgBouncerPoolerHealth(poolerName, namespace),
	}
}

// InfisicalReadiness is the readiness profile for the Infisical
// platform service. It depends on the data layer being ready first —
// this is the contract that closes the CNPG boot race observed during
// Day-0 choreography (see RCA: Infisical pods crash-looped on
// ENOTFOUND platform-db-pooler because the Pooler service didn't
// exist when Infisical started).
//
// Callers (Cobra commands, orchestrators) compose phases:
//
//	health.HealthWaiter{}.Wait(ctx, kubeconfig)  // for the data layer
//	health.HealthWaiter{}.Wait(ctx, kubeconfig)  // for Infisical
type InfisicalReadiness struct {
	// Namespace where Infisical is deployed. Default: platform-security.
	Namespace string
	// WorkloadName is the StatefulSet/Deployment name.
	// Default: "infisical-standalone-infisical".
	WorkloadName string
}

// PhaseName returns the phase identifier.
func (i *InfisicalReadiness) PhaseName() string { return "Infisical Platform" }

// Checkers returns the ordered list of HealthChecker instances.
func (i *InfisicalReadiness) Checkers() []HealthChecker {
	ns := i.Namespace
	if ns == "" {
		ns = constants.NamespaceSecurity
	}
	name := i.WorkloadName
	if name == "" {
		name = "infisical-standalone-infisical"
	}
	return []HealthChecker{
		NewWorkloadHealth(name, ns),
	}
}

// CAPIProvidersReadiness is the readiness profile for the CAPI core
// providers on a bootstrap cluster. Used by the pivot orchestrator
// after the move to verify providers came up cleanly on the
// management cluster.
type CAPIProvidersReadiness struct {
	Namespace string
	// OSType selects the bootstrap/control-plane provider names
	// ("kubeadm" for ubuntu, "talos" for talos).
	OSType string
}

// PhaseName returns the phase identifier.
func (c *CAPIProvidersReadiness) PhaseName() string { return "CAPI Providers" }

// Checkers returns the ordered list of HealthChecker instances.
func (c *CAPIProvidersReadiness) Checkers() []HealthChecker {
	ns := c.Namespace
	if ns == "" {
		ns = constants.NamespaceCAPI
	}
	bootstrapProvider := "kubeadm"
	controlPlaneProvider := "kubeadm"
	if c.OSType == "talos" {
		bootstrapProvider = "talos"
		controlPlaneProvider = "talos"
	}
	return []HealthChecker{
		NewCAPIResourceReadyHealth("CoreProvider", "cluster-api", ns),
		NewCAPIResourceReadyHealth("BootstrapProvider", bootstrapProvider, ns),
		NewCAPIResourceReadyHealth("ControlPlaneProvider", controlPlaneProvider, ns),
		NewCAPIResourceReadyHealth("InfrastructureProvider", "hetzner", ns),
	}
}

// OperatorsReadiness is the readiness profile for the platform
// operators on the hub cluster. Used by the bootstrap orchestrator
// to gate the platform-deploy phase.
type OperatorsReadiness struct{}

// PhaseName returns the phase identifier.
func (o *OperatorsReadiness) PhaseName() string { return "Platform Operators" }

// Checkers returns the ordered list of HealthChecker instances.
func (o *OperatorsReadiness) Checkers() []HealthChecker {
	return []HealthChecker{
		// Webhooks and CRDs come up before pods are fully ready.
		NewValidatingWebhookHealth("capi", "cert-manager", "cnpg", "externalsecret"),
		NewCRDRegisteredHealth(
			"externalsecrets.external-secrets.io",
			"clusters.postgresql.cnpg.io",
		),
		// Pods being Running is the final pre-condition.
		NewOperatorPodsHealth("cnpg-system", "app.kubernetes.io/name=cloudnative-pg"),
		NewOperatorPodsHealth("platform-ops", "app.kubernetes.io/name=external-secrets"),
	}
}
