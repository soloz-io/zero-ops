package nats

import "time"

// Config holds NATS connection configuration.
type Config struct {
	// URLs is a comma-separated list of NATS server URLs for clustering (7.1).
	// e.g. "nats://nats-0:4222,nats://nats-1:4222,nats://nats-2:4222"
	URLs string

	// Optional credentials
	NKeyFile  string
	CredsFile string

	// Reconnect settings
	MaxReconnects  int           // default -1 (unlimited)
	ReconnectWait  time.Duration // default 2s
	ConnectTimeout time.Duration // default 5s
}

func (c *Config) defaults() {
	if c.URLs == "" {
		c.URLs = "nats://localhost:4222"
	}
	if c.MaxReconnects == 0 {
		c.MaxReconnects = -1
	}
	if c.ReconnectWait == 0 {
		c.ReconnectWait = 2 * time.Second
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 5 * time.Second
	}
}
