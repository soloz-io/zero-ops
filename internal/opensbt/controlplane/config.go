package controlplane

import (
	"fmt"
	"time"

	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
)

// Config holds all dependencies and settings for the Control Plane.
// Billing, Metering, TierManager, and SecretManager are optional.
type Config struct {
	// Required
	Auth     interfaces.IAuth
	EventBus interfaces.IEventBus
	Storage  interfaces.IStorage

	// Optional
	Billing       interfaces.IBilling
	Metering      interfaces.IMetering
	TierManager   interfaces.ITierManager
	SecretManager interfaces.ISecretManager
	SystemAdmin   interfaces.ISystemAdmin

	// HTTP server
	APIPort int // default 8080

	// System admin bootstrap (created on first Start if non-empty)
	SystemAdminEmail string
	SystemAdminName  string

	// CORS — allowed origins; empty means same-origin only
	AllowedOrigins []string

	// Graceful shutdown timeout
	ShutdownTimeout time.Duration // default 30s
}

func (c *Config) validate() error {
	if c.Auth == nil {
		return fmt.Errorf("controlplane: Auth is required")
	}
	if c.EventBus == nil {
		return fmt.Errorf("controlplane: EventBus is required")
	}
	if c.Storage == nil {
		return fmt.Errorf("controlplane: Storage is required")
	}
	return nil
}

func (c *Config) defaults() {
	if c.APIPort == 0 {
		c.APIPort = 8080
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = 30 * time.Second
	}
}
