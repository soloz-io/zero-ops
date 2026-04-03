package client

import (
	"context"
	"fmt"
	"reflect"

	"github.com/nats-io/nats.go"
	"sigs.k8s.io/controller-runtime/pkg/log"

	opsv1alpha1 "github.com/soloz-io/zero-ops/operators/hub-operator/api/v1alpha1"
)

// NATSClient wraps NATS JetStream operations
type NATSClient struct {
	nc *nats.Conn
	js nats.JetStreamContext
}

// NewNATSClient creates a new NATS client
// Requirement 8.1: Use NATS Go SDK
// Requirement 8.3: Connect to nats://nats.hub-platform-core.svc:4222
func NewNATSClient(url string) (*NATSClient, error) {
	if url == "" {
		url = "nats://nats.hub-platform-core.svc:4222"
	}

	// Connect to NATS
	nc, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Get JetStream context
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to get JetStream context: %w", err)
	}

	return &NATSClient{
		nc: nc,
		js: js,
	}, nil
}

// CreateOrUpdateStreams creates or updates all NATS streams from HubEnvironment CR
// Requirement 8.2: Read stream definitions from CR
// Requirement 8.4: Use idempotent stream creation with stream name as unique key
func (nc *NATSClient) CreateOrUpdateStreams(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
	logger := log.FromContext(ctx)

	for _, streamSpec := range hubEnv.Spec.NATS.Streams {
		logger.Info("Processing NATS stream", "stream", streamSpec.Name)

		// Requirement 8.4: Check if stream exists (idempotent)
		exists, err := nc.streamExists(ctx, streamSpec.Name)
		if err != nil {
			// Requirement 8.7-8.8: Handle NATS API errors
			if isNATSTransientError(err) {
				return fmt.Errorf("NATS API unreachable (transient error): %w", err)
			}
			return fmt.Errorf("failed to check if stream exists: %w", err)
		}

		if exists {
			// Requirement 8.5-8.6: Check for configuration drift and update
			needsUpdate, err := nc.streamNeedsUpdate(ctx, streamSpec)
			if err != nil {
				if isNATSTransientError(err) {
					return fmt.Errorf("NATS API unreachable (transient error): %w", err)
				}
				return fmt.Errorf("failed to check stream drift: %w", err)
			}

			if needsUpdate {
				if err := nc.updateStream(ctx, streamSpec); err != nil {
					if isNATSTransientError(err) {
						return fmt.Errorf("NATS API unreachable (transient error): %w", err)
					}
					return fmt.Errorf("failed to update stream %s: %w", streamSpec.Name, err)
				}
				logger.Info("Updated NATS stream", "stream", streamSpec.Name)
			}
		} else {
			// Create new stream
			if err := nc.createStream(ctx, streamSpec); err != nil {
				if isNATSTransientError(err) {
					return fmt.Errorf("NATS API unreachable (transient error): %w", err)
				}
				return fmt.Errorf("failed to create stream %s: %w", streamSpec.Name, err)
			}
			logger.Info("Created NATS stream", "stream", streamSpec.Name)
		}
	}

	// Requirement 8.10: Prune orphaned streams
	if err := nc.pruneOrphanedStreams(ctx, hubEnv); err != nil {
		if isNATSTransientError(err) {
			return fmt.Errorf("NATS API unreachable (transient error): %w", err)
		}
		return fmt.Errorf("failed to prune orphaned streams: %w", err)
	}

	return nil
}

// streamExists checks if a NATS stream exists
func (nc *NATSClient) streamExists(ctx context.Context, streamName string) (bool, error) {
	_, err := nc.js.StreamInfo(streamName)
	if err != nil {
		if err == nats.ErrStreamNotFound {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// streamNeedsUpdate checks if stream configuration has drifted
// Requirement 8.5: Compare existing configuration against CR spec
func (nc *NATSClient) streamNeedsUpdate(ctx context.Context, streamSpec opsv1alpha1.NATSStream) (bool, error) {
	info, err := nc.js.StreamInfo(streamSpec.Name)
	if err != nil {
		return false, err
	}

	// Check subjects drift
	if !reflect.DeepEqual(info.Config.Subjects, streamSpec.Subjects) {
		return true, nil
	}

	// Check retention policy drift
	desiredRetention := convertRetentionPolicy(streamSpec.Retention)
	if info.Config.Retention != desiredRetention {
		return true, nil
	}

	// Check storage type drift
	desiredStorage := convertStorageType(streamSpec.Storage)
	if info.Config.Storage != desiredStorage {
		return true, nil
	}

	return false, nil
}

// createStream creates a new NATS stream
// Requirement 8.3: Configure stream subjects from CR
func (nc *NATSClient) createStream(ctx context.Context, streamSpec opsv1alpha1.NATSStream) error {
	config := &nats.StreamConfig{
		Name:      streamSpec.Name,
		Subjects:  streamSpec.Subjects,
		Retention: convertRetentionPolicy(streamSpec.Retention),
		Storage:   convertStorageType(streamSpec.Storage),
	}

	_, err := nc.js.AddStream(config)
	return err
}

// updateStream updates an existing NATS stream
// Requirement 8.6: Execute UpdateStream API call on drift
func (nc *NATSClient) updateStream(ctx context.Context, streamSpec opsv1alpha1.NATSStream) error {
	config := &nats.StreamConfig{
		Name:      streamSpec.Name,
		Subjects:  streamSpec.Subjects,
		Retention: convertRetentionPolicy(streamSpec.Retention),
		Storage:   convertStorageType(streamSpec.Storage),
	}

	_, err := nc.js.UpdateStream(config)
	return err
}

// pruneOrphanedStreams deletes streams not in CR spec
// Requirement 8.10: Delete orphaned streams
func (nc *NATSClient) pruneOrphanedStreams(ctx context.Context, hubEnv *opsv1alpha1.HubEnvironment) error {
	logger := log.FromContext(ctx)

	// Build map of desired streams from CR spec
	desiredStreams := make(map[string]bool)
	for _, streamSpec := range hubEnv.Spec.NATS.Streams {
		desiredStreams[streamSpec.Name] = true
	}

	// List all streams from NATS
	// Note: We'll use a naming convention - streams managed by hub-operator start with "hub-"
	streamNames := nc.js.StreamNames()
	for streamName := range streamNames {
		// Only manage streams that start with "hub-" prefix
		if len(streamName) < 4 || streamName[:4] != "hub-" {
			continue
		}

		// Check if stream is in desired state
		if !desiredStreams[streamName] {
			if err := nc.deleteStream(ctx, streamName); err != nil {
				logger.Error(err, "Failed to delete orphaned stream", "stream", streamName)
				continue
			}
			logger.Info("Deleted orphaned stream", "stream", streamName)
		}
	}

	return nil
}

// deleteStream deletes a NATS stream
func (nc *NATSClient) deleteStream(ctx context.Context, streamName string) error {
	return nc.js.DeleteStream(streamName)
}

// Close closes the NATS connection
// Requirement 8.10: Implement Close() for connection cleanup
func (nc *NATSClient) Close() error {
	if nc.nc != nil {
		nc.nc.Close()
	}
	return nil
}

// convertRetentionPolicy converts CR retention policy to NATS retention policy
func convertRetentionPolicy(retention string) nats.RetentionPolicy {
	switch retention {
	case "limits":
		return nats.LimitsPolicy
	case "interest":
		return nats.InterestPolicy
	case "workqueue":
		return nats.WorkQueuePolicy
	default:
		return nats.LimitsPolicy
	}
}

// convertStorageType converts CR storage type to NATS storage type
func convertStorageType(storage string) nats.StorageType {
	switch storage {
	case "file":
		return nats.FileStorage
	case "memory":
		return nats.MemoryStorage
	default:
		return nats.FileStorage
	}
}

// isNATSTransientError checks if an error is transient (connection timeout, network failure)
// Requirement 8.8: Classify errors for retry logic
func isNATSTransientError(err error) bool {
	if err == nil {
		return false
	}

	// Connection errors
	if err == nats.ErrConnectionClosed || err == nats.ErrTimeout || err == nats.ErrNoServers {
		return true
	}

	// Check error string for common transient patterns
	errStr := err.Error()
	if contains(errStr, "timeout") || contains(errStr, "connection refused") || contains(errStr, "connection reset") {
		return true
	}

	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
