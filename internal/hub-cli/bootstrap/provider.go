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

	OnCAPIInit(ctx context.Context, kubeconfig, context, namespace string) error

	PostBootComponents(ctx context.Context, kubeconfig string) error
}
