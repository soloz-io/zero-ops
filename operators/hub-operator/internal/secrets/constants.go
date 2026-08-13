package secrets

const (
	// EncryptionKeyProtectionFinalizer prevents accidental deletion of critical Infisical secrets
	// This finalizer is added to infisical-secrets and infisical-redis-credentials to ensure
	// they cannot be deleted without explicit removal of the finalizer.
	//
	// REQ-9: Add finalizers to prevent accidental deletion
	// REQ-10: Implement finalizer controller for graceful teardown
	EncryptionKeyProtectionFinalizer = "ops.nutgraf.in/encryption-key-protection"
)
