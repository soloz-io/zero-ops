package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/lib/pq"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/soloz-io/zero-ops/operators/hub-operator/internal/embed"
)

// DirtyDatabaseError represents a permanent error requiring manual intervention
type DirtyDatabaseError struct {
	Version int
	Dirty   bool
	Err     error
}

func (e *DirtyDatabaseError) Error() string {
	return fmt.Sprintf("database is in dirty state (version=%d, dirty=%t): %v", e.Version, e.Dirty, e.Err)
}

// Migrator handles database migrations using golang-migrate
type Migrator struct {
	db     *sql.DB
	client client.Client
}

// NewMigrator creates a new Migrator instance
// Connects to platform-db-rw (primary service), NOT pooler
func NewMigrator(ctx context.Context, k8sClient client.Client, namespace string) (*Migrator, error) {
	logger := log.FromContext(ctx)

	// Read platform-db-superuser secret for connection credentials
	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, client.ObjectKey{
		Name:      "platform-db-superuser",
		Namespace: namespace,
	}, secret); err != nil {
		return nil, fmt.Errorf("failed to get platform-db-superuser secret: %w", err)
	}

	username := string(secret.Data["username"])
	password := string(secret.Data["password"])
	dbname := string(secret.Data["dbname"])

	// Handle CNPG wildcard database name
	// CNPG uses "*" to denote superuser access to all databases
	if dbname == "*" || dbname == "" {
		dbname = "postgres"
	}

	// Read platform-db-ca secret for TLS
	caSecret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, client.ObjectKey{
		Name:      "platform-db-ca",
		Namespace: namespace,
	}, caSecret); err != nil {
		return nil, fmt.Errorf("failed to get platform-db-ca secret: %w", err)
	}

	// Connect to primary service (platform-db-rw), NOT pooler
	// Requirement 5.2: Connect directly to CNPG primary service for migrations
	connStr := fmt.Sprintf(
		"host=platform-db-rw.%s.svc port=5432 user=%s password=%s dbname=%s sslmode=require",
		namespace, username, password, dbname,
	)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Test connection with timeout
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	logger.Info("Connected to platform-db-rw for migrations")

	return &Migrator{
		db:     db,
		client: k8sClient,
	}, nil
}

// RunMigrations executes all pending migrations
// Requirement 5.1: Execute migrations using golang-migrate/migrate
func (m *Migrator) RunMigrations(ctx context.Context) error {
	logger := log.FromContext(ctx)

	// Create iofs source from embedded filesystem
	// Requirement 5.4: Read migration files from embedded filesystem
	sourceDriver, err := iofs.New(embed.MigrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	// Create postgres database driver
	driver, err := postgres.WithInstance(m.db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("failed to create database driver: %w", err)
	}

	// Create migrate instance
	migrator, err := migrate.NewWithInstance("iofs", sourceDriver, "postgres", driver)
	if err != nil {
		return fmt.Errorf("failed to create migrator: %w", err)
	}
	defer migrator.Close()

	// Check current version and dirty state
	version, dirty, err := migrator.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("failed to get migration version: %w", err)
	}

	// Requirement 5.5: Detect dirty database state (permanent error)
	if dirty {
		logger.Error(nil, "Database is in dirty state", "version", version)
		return &DirtyDatabaseError{
			Version: int(version),
			Dirty:   dirty,
			Err:     fmt.Errorf("migration version %d failed and left database in dirty state", version),
		}
	}

	logger.Info("Running database migrations", "currentVersion", version)

	// Run migrations
	if err := migrator.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			logger.Info("No new migrations to apply")
			return nil
		}

		// Check if error is transient (connection timeout, network failure)
		// Requirement 5.6: Detect transient errors for retry
		if isTransientError(err) {
			logger.Info("Transient migration error, will retry", "error", err)
			return fmt.Errorf("transient migration error: %w", err)
		}

		// Check if database became dirty after failed migration
		version, dirty, vErr := migrator.Version()
		if vErr == nil && dirty {
			return &DirtyDatabaseError{
				Version: int(version),
				Dirty:   dirty,
				Err:     err,
			}
		}

		return fmt.Errorf("migration failed: %w", err)
	}

	newVersion, _, err := migrator.Version()
	if err != nil {
		return fmt.Errorf("failed to get new migration version: %w", err)
	}

	logger.Info("Migrations completed successfully", "newVersion", newVersion)
	return nil
}

// Close closes the database connection
func (m *Migrator) Close() error {
	if m.db != nil {
		return m.db.Close()
	}
	return nil
}

// isTransientError checks if an error is transient (connection timeout, network failure)
// Requirement 5.6: Distinguish transient errors from permanent errors
func isTransientError(err error) bool {
	if err == nil {
		return false
	}

	errStr := err.Error()

	// Connection timeouts
	if contains(errStr, "timeout") || contains(errStr, "deadline exceeded") {
		return true
	}

	// Network failures
	if contains(errStr, "connection refused") || contains(errStr, "connection reset") {
		return true
	}

	// Temporary DNS failures
	if contains(errStr, "no such host") || contains(errStr, "temporary failure in name resolution") {
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

// IsDirtyDatabaseError checks if an error is a DirtyDatabaseError
func IsDirtyDatabaseError(err error) bool {
	var dirtyErr *DirtyDatabaseError
	return errors.As(err, &dirtyErr)
}
