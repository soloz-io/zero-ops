package bootstrap

import (
	"context"

	"github.com/soloz-io/zero-ops/internal/hub-cli/capi"
	"github.com/soloz-io/zero-ops/internal/hub-cli/preflight"
)

type CloudProvider interface {
	Name() string

	PreflightValidators() []preflight.Validator

	CAPIProviders() []capi.CAPIProvider

	IsSelfProvisioning() bool

	ClusterClassPaths() []string

	// KindConfigPath returns an optional path to a Kind cluster configuration file
	// (e.g., for extraPortMappings ingress support in local CAPD).
	// Returns empty string if no custom config is needed.
	KindConfigPath() string

	OnCAPIInit(ctx context.Context, kubeconfig, context, namespace string) error

	PostBootComponents(ctx context.Context, kubeconfig string) error
}
