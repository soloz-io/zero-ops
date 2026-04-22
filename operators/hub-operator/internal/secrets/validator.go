package secrets

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// ValidateEncryptionKey ensures the key meets Infisical requirements
func ValidateEncryptionKey(key string) error {
	// Must be 32 hex characters (16 bytes)
	if len(key) != 32 {
		return fmt.Errorf("ENCRYPTION_KEY must be 32 characters, got %d", len(key))
	}

	// Must be valid hex string
	if _, err := hex.DecodeString(key); err != nil {
		return fmt.Errorf("ENCRYPTION_KEY must be valid hex string: %w", err)
	}

	// Must not be all zeros (invalid key)
	if key == strings.Repeat("0", 32) {
		return fmt.Errorf("ENCRYPTION_KEY cannot be all zeros")
	}

	return nil
}

// ValidateAuthSecret ensures the secret meets JWT signing requirements
func ValidateAuthSecret(secret string) error {
	// Must be 32 hex characters (16 bytes)
	if len(secret) != 32 {
		return fmt.Errorf("AUTH_SECRET must be 32 characters, got %d", len(secret))
	}

	// Must be valid hex string
	if _, err := hex.DecodeString(secret); err != nil {
		return fmt.Errorf("AUTH_SECRET must be valid hex string: %w", err)
	}

	// Must not be all zeros (invalid secret)
	if secret == strings.Repeat("0", 32) {
		return fmt.Errorf("AUTH_SECRET cannot be all zeros")
	}

	return nil
}
