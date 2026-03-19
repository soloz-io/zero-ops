// Package metering provides a PostgreSQL-backed IMetering implementation
// (Tasks 28.1–28.8).
package metering

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// Metering implements interfaces.IMetering backed by PostgreSQL (28.1–28.8).
type Metering struct {
	pool         *pgxpool.Pool
	eventsTotal  prometheus.Counter
	errorsTotal  prometheus.Counter
}

// New creates a Metering provider using an existing pgxpool (28.8).
func New(pool *pgxpool.Pool) *Metering {
	return &Metering{
		pool: pool,
		eventsTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "opensbt_metering_events_total",
			Help: "Total usage events ingested",
		}),
		errorsTotal: promauto.NewCounter(prometheus.CounterOpts{
			Name: "opensbt_metering_errors_total",
			Help: "Total metering errors",
		}),
	}
}

// ─── Meter Management (28.1) ──────────────────────────────────────────────────

func (m *Metering) CreateMeter(ctx context.Context, meter models.Meter) error {
	now := time.Now().UTC()
	_, err := m.pool.Exec(ctx,
		`INSERT INTO meters (id, name, description, unit, type, config, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		meter.ID, meter.Name, meter.Description, meter.Unit, meter.Type, meter.Config, now, now,
	)
	return err
}

func (m *Metering) GetMeter(ctx context.Context, meterID string) (*models.Meter, error) {
	row := m.pool.QueryRow(ctx,
		`SELECT id, name, description, unit, type, config, created_at, updated_at FROM meters WHERE id=$1`,
		meterID,
	)
	var meter models.Meter
	if err := row.Scan(&meter.ID, &meter.Name, &meter.Description, &meter.Unit, &meter.Type,
		&meter.Config, &meter.CreatedAt, &meter.UpdatedAt); err != nil {
		return nil, fmt.Errorf("metering: get meter: %w", err)
	}
	return &meter, nil
}

func (m *Metering) UpdateMeter(ctx context.Context, meterID string, u models.MeterUpdates) error {
	_, err := m.pool.Exec(ctx,
		`UPDATE meters SET
		   name        = COALESCE($2, name),
		   description = COALESCE($3, description),
		   unit        = COALESCE($4, unit),
		   config      = COALESCE($5, config),
		   updated_at  = NOW()
		 WHERE id = $1`,
		meterID, u.Name, u.Description, u.Unit, u.Config,
	)
	return err
}

func (m *Metering) DeleteMeter(ctx context.Context, meterID string) error {
	_, err := m.pool.Exec(ctx, `DELETE FROM meters WHERE id=$1`, meterID)
	return err
}

func (m *Metering) ListMeters(ctx context.Context, f models.MeterFilters) ([]models.Meter, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	rows, err := m.pool.Query(ctx,
		`SELECT id, name, description, unit, type, config, created_at, updated_at
		 FROM meters
		 WHERE ($1::text IS NULL OR type = $1)
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		f.Type, limit, f.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var meters []models.Meter
	for rows.Next() {
		var meter models.Meter
		if err := rows.Scan(&meter.ID, &meter.Name, &meter.Description, &meter.Unit, &meter.Type,
			&meter.Config, &meter.CreatedAt, &meter.UpdatedAt); err != nil {
			return nil, err
		}
		meters = append(meters, meter)
	}
	return meters, rows.Err()
}

// ─── Usage Ingestion (28.2–28.3) ─────────────────────────────────────────────

func (m *Metering) IngestUsageEvent(ctx context.Context, e models.UsageEvent) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	_, err := m.pool.Exec(ctx,
		`INSERT INTO usage_events (id, tenant_id, meter_id, value, timestamp, properties)
		 VALUES ($1,$2,$3,$4,$5,$6)
		 ON CONFLICT (id) DO NOTHING`,
		e.ID, e.TenantID, e.MeterID, e.Value, e.Timestamp, e.Properties,
	)
	if err != nil {
		m.errorsTotal.Inc()
		return err
	}
	m.eventsTotal.Inc()
	return nil
}

// IngestUsageEventBatch ingests events in a single transaction (28.3).
func (m *Metering) IngestUsageEventBatch(ctx context.Context, events []models.UsageEvent) error {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, e := range events {
		if e.Timestamp.IsZero() {
			e.Timestamp = time.Now().UTC()
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO usage_events (id, tenant_id, meter_id, value, timestamp, properties)
			 VALUES ($1,$2,$3,$4,$5,$6)
			 ON CONFLICT (id) DO NOTHING`,
			e.ID, e.TenantID, e.MeterID, e.Value, e.Timestamp, e.Properties,
		); err != nil {
			m.errorsTotal.Inc()
			return err
		}
		m.eventsTotal.Inc()
	}
	return tx.Commit(ctx)
}

// ─── Usage Queries (28.4–28.5) ───────────────────────────────────────────────

func (m *Metering) GetUsage(ctx context.Context, meterID string, period models.TimePeriod) (*models.UsageData, error) {
	row := m.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(value),0), COUNT(*) FROM usage_events
		 WHERE meter_id=$1 AND timestamp BETWEEN $2 AND $3 AND cancelled=FALSE`,
		meterID, period.Start, period.End,
	)
	var data models.UsageData
	data.MeterID = meterID
	data.Period = period
	if err := row.Scan(&data.TotalUsage, &data.EventCount); err != nil {
		return nil, fmt.Errorf("metering: get usage: %w", err)
	}
	return &data, nil
}

// GetTenantUsage returns per-meter usage for a tenant (28.5).
func (m *Metering) GetTenantUsage(ctx context.Context, tenantID string, period models.TimePeriod) (*models.TenantUsageData, error) {
	rows, err := m.pool.Query(ctx,
		`SELECT meter_id, COALESCE(SUM(value),0), COUNT(*) FROM usage_events
		 WHERE tenant_id=$1 AND timestamp BETWEEN $2 AND $3 AND cancelled=FALSE
		 GROUP BY meter_id`,
		tenantID, period.Start, period.End,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := &models.TenantUsageData{
		TenantID: tenantID,
		Period:   period,
		Meters:   make(map[string]models.UsageData),
	}
	for rows.Next() {
		var ud models.UsageData
		if err := rows.Scan(&ud.MeterID, &ud.TotalUsage, &ud.EventCount); err != nil {
			return nil, err
		}
		ud.TenantID = tenantID
		ud.Period = period
		result.Meters[ud.MeterID] = ud
	}
	return result, rows.Err()
}

// AggregateUsage performs flexible aggregation (28.4).
func (m *Metering) AggregateUsage(ctx context.Context, req models.AggregationRequest) (*models.AggregationResult, error) {
	row := m.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(value),0) FROM usage_events
		 WHERE ($1::text IS NULL OR tenant_id=$1)
		   AND ($2::text IS NULL OR meter_id=$2)
		   AND timestamp BETWEEN $3 AND $4
		   AND cancelled=FALSE`,
		nilIfEmpty(req.TenantID), nilIfEmpty(req.MeterID), req.Period.Start, req.Period.End,
	)
	var total float64
	if err := row.Scan(&total); err != nil {
		return nil, fmt.Errorf("metering: aggregate: %w", err)
	}
	return &models.AggregationResult{Period: req.Period, TotalUsage: total}, nil
}

// ─── Usage Management (28.6–28.7) ────────────────────────────────────────────

// CancelUsageEvents soft-deletes events by ID (28.6).
func (m *Metering) CancelUsageEvents(ctx context.Context, eventIDs []string) error {
	_, err := m.pool.Exec(ctx,
		`UPDATE usage_events SET cancelled=TRUE WHERE id = ANY($1::text[])`,
		eventIDs,
	)
	return err
}

// PurgeOldEvents deletes cancelled or expired events older than retentionDays (28.7).
func (m *Metering) PurgeOldEvents(ctx context.Context, retentionDays int) error {
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)
	_, err := m.pool.Exec(ctx,
		`DELETE FROM usage_events WHERE timestamp < $1 OR cancelled=TRUE`,
		cutoff,
	)
	return err
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
