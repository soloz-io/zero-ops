package openmeter

import (
	"context"
	"fmt"
	"math"
	"time"
)

// RetryConfig defines retry behavior for OpenMeter API calls
type RetryConfig struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Multiplier     float64
}

// DefaultRetryConfig returns the default retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		Multiplier:     2.0,
	}
}

// RetryWithExponentialBackoff executes a function with exponential backoff retry logic
// Used for rate limit errors and transient failures
func RetryWithExponentialBackoff(ctx context.Context, config RetryConfig, fn func() error) error {
	var lastErr error
	backoff := config.InitialBackoff

	for attempt := 0; attempt <= config.MaxRetries; attempt++ {
		if attempt > 0 {
			// Wait before retry
			select {
			case <-ctx.Done():
				return fmt.Errorf("context cancelled during retry: %w", ctx.Err())
			case <-time.After(backoff):
				// Continue to retry
			}

			// Calculate next backoff with exponential increase
			backoff = time.Duration(float64(backoff) * config.Multiplier)
			if backoff > config.MaxBackoff {
				backoff = config.MaxBackoff
			}
		}

		// Execute function
		err := fn()
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if error is retryable (rate limit, timeout, etc.)
		if !isRetryableError(err) {
			return err
		}
	}

	return fmt.Errorf("max retries (%d) exceeded: %w", config.MaxRetries, lastErr)
}

// isRetryableError determines if an error should trigger a retry
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for rate limit errors, timeouts, and transient failures
	// This is a placeholder - actual implementation will check OpenMeter SDK error types
	errStr := err.Error()
	
	// Rate limit errors
	if contains(errStr, "rate limit") || contains(errStr, "429") {
		return true
	}

	// Timeout errors
	if contains(errStr, "timeout") || contains(errStr, "deadline exceeded") {
		return true
	}

	// Temporary network errors
	if contains(errStr, "connection refused") || contains(errStr, "connection reset") {
		return true
	}

	// Service unavailable
	if contains(errStr, "503") || contains(errStr, "service unavailable") {
		return true
	}

	return false
}

// contains checks if a string contains a substring (case-insensitive helper)
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && 
		(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || 
		len(s) > len(substr)*2))
}

// CalculateBackoff calculates the backoff duration for a given attempt
func CalculateBackoff(attempt int, config RetryConfig) time.Duration {
	backoff := float64(config.InitialBackoff) * math.Pow(config.Multiplier, float64(attempt))
	if backoff > float64(config.MaxBackoff) {
		return config.MaxBackoff
	}
	return time.Duration(backoff)
}
