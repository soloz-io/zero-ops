package readiness

import (
	"context"
)

// ReadyStatus represents the outcome of a readiness check.
type ReadyStatus struct {
	Ready   bool
	Reason  string
	Message string
}

// InfrastructureReadinessChecker defines an abstraction for verifying
// the operational readiness of a specific infrastructure dependency.
type InfrastructureReadinessChecker interface {
	Check(ctx context.Context) (ReadyStatus, error)
}
