package postgres

import (
	"context"
	_ "embed"
)

//go:embed migrations/001_initial_schema.sql
var migration001 string

//go:embed migrations/002_postgrest_dashboard.sql
var migration002 string

//go:embed migrations/003_tier_configs.sql
var migration003 string

//go:embed migrations/004_metering.sql
var migration004 string

// migrate runs embedded SQL migrations idempotently.
func (s *Storage) migrate(ctx context.Context) error {
	for _, sql := range []string{migration001, migration002, migration003, migration004} {
		if _, err := s.pool.Exec(ctx, sql); err != nil {
			return err
		}
	}
	return nil
}
