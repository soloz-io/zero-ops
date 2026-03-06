package preflight

import (
	"context"
	"fmt"

	"github.com/soloz-io/zero-ops/pkg/state"
)

// IdempotencyValidator checks for existing cluster
type IdempotencyValidator struct {
	ClusterName string
	Upgrade     bool
}

func (v *IdempotencyValidator) Validate(ctx context.Context) error {
	mgr := state.NewStateManager(v.ClusterName)
	
	if !mgr.Exists() {
		return nil // No existing cluster
	}
	
	if !v.Upgrade {
		return fmt.Errorf("Management Cluster '%s' already exists. Use --upgrade to reconcile components", v.ClusterName)
	}
	
	// Upgrade mode - allow to proceed
	return nil
}
