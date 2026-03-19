// Package billing provides a mock IBilling implementation suitable for testing
// and as a reference for production billing integrations (Tasks 27.1–27.10).
package billing

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// MockBilling is an in-memory IBilling implementation (27.1).
// It publishes opensbt_billingSuccess / opensbt_billingFailure events via the
// provided IEventBus when usage is recorded (27.10).
type MockBilling struct {
	mu            sync.RWMutex
	customers     map[string]*models.BillingCustomer     // customerID → customer
	subscriptions map[string]*models.Subscription        // subscriptionID → subscription
	usage         map[string][]models.UsageRecord        // customerID → records
	eventBus      interfaces.IEventBus
}

// New creates a MockBilling. Pass nil eventBus to skip event publishing.
func New(eventBus interfaces.IEventBus) *MockBilling {
	return &MockBilling{
		customers:     make(map[string]*models.BillingCustomer),
		subscriptions: make(map[string]*models.Subscription),
		usage:         make(map[string][]models.UsageRecord),
		eventBus:      eventBus,
	}
}

// ─── Customer Management (27.2–27.5) ─────────────────────────────────────────

func (b *MockBilling) CreateCustomer(_ context.Context, c models.BillingCustomer) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c.ID == "" {
		return fmt.Errorf("billing: customer ID required")
	}
	now := time.Now().UTC()
	c.CreatedAt, c.UpdatedAt = now, now
	b.customers[c.ID] = &c
	return nil
}

func (b *MockBilling) GetCustomer(_ context.Context, customerID string) (*models.BillingCustomer, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	c, ok := b.customers[customerID]
	if !ok {
		return nil, fmt.Errorf("billing: customer %q not found", customerID)
	}
	cp := *c
	return &cp, nil
}

func (b *MockBilling) UpdateCustomer(_ context.Context, customerID string, u models.CustomerUpdates) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	c, ok := b.customers[customerID]
	if !ok {
		return fmt.Errorf("billing: customer %q not found", customerID)
	}
	if u.Email != nil {
		c.Email = *u.Email
	}
	if u.Name != nil {
		c.Name = *u.Name
	}
	if u.Metadata != nil {
		c.Metadata = *u.Metadata
	}
	c.UpdatedAt = time.Now().UTC()
	return nil
}

func (b *MockBilling) DeleteCustomer(_ context.Context, customerID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.customers[customerID]; !ok {
		return fmt.Errorf("billing: customer %q not found", customerID)
	}
	delete(b.customers, customerID)
	delete(b.usage, customerID)
	return nil
}

// ─── Subscription Management (27.6) ──────────────────────────────────────────

func (b *MockBilling) CreateSubscription(_ context.Context, s models.Subscription) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s.ID == "" {
		return fmt.Errorf("billing: subscription ID required")
	}
	now := time.Now().UTC()
	s.CreatedAt, s.UpdatedAt = now, now
	b.subscriptions[s.ID] = &s
	return nil
}

func (b *MockBilling) GetSubscription(_ context.Context, subscriptionID string) (*models.Subscription, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	s, ok := b.subscriptions[subscriptionID]
	if !ok {
		return nil, fmt.Errorf("billing: subscription %q not found", subscriptionID)
	}
	cp := *s
	return &cp, nil
}

func (b *MockBilling) UpdateSubscription(_ context.Context, subscriptionID string, u models.SubscriptionUpdates) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.subscriptions[subscriptionID]
	if !ok {
		return fmt.Errorf("billing: subscription %q not found", subscriptionID)
	}
	if u.PlanID != nil {
		s.PlanID = *u.PlanID
	}
	if u.Status != nil {
		s.Status = *u.Status
	}
	if u.Metadata != nil {
		s.Metadata = *u.Metadata
	}
	s.UpdatedAt = time.Now().UTC()
	return nil
}

func (b *MockBilling) CancelSubscription(_ context.Context, subscriptionID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.subscriptions[subscriptionID]
	if !ok {
		return fmt.Errorf("billing: subscription %q not found", subscriptionID)
	}
	cancelled := "cancelled"
	s.Status = cancelled
	s.UpdatedAt = time.Now().UTC()
	return nil
}

// ─── Usage and Billing (27.7–27.8, 27.10) ────────────────────────────────────

// RecordUsage stores a usage record and publishes a billingSuccess event (27.7, 27.10).
func (b *MockBilling) RecordUsage(ctx context.Context, u models.UsageRecord) error {
	b.mu.Lock()
	if u.Timestamp.IsZero() {
		u.Timestamp = time.Now().UTC()
	}
	b.usage[u.CustomerID] = append(b.usage[u.CustomerID], u)
	b.mu.Unlock()

	b.publishEvent(ctx, "opensbt_billingSuccess", map[string]interface{}{
		"customer_id": u.CustomerID,
		"meter_name":  u.MeterName,
		"value":       u.Value,
	})
	return nil
}

func (b *MockBilling) GetUsage(_ context.Context, customerID string, period models.TimePeriod) (*models.UsageReport, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	var records []models.UsageRecord
	var total float64
	for _, r := range b.usage[customerID] {
		if !r.Timestamp.Before(period.Start) && !r.Timestamp.After(period.End) {
			records = append(records, r)
			total += r.Value
		}
	}
	return &models.UsageReport{
		CustomerID: customerID,
		Period:     period,
		TotalUsage: total,
		Records:    records,
	}, nil
}

// GenerateInvoice produces a simple invoice from recorded usage (27.8).
func (b *MockBilling) GenerateInvoice(ctx context.Context, customerID string, period models.TimePeriod) (*models.Invoice, error) {
	report, err := b.GetUsage(ctx, customerID, period)
	if err != nil {
		return nil, err
	}
	invoice := &models.Invoice{
		ID:         fmt.Sprintf("inv-%s-%d", customerID, time.Now().UnixNano()),
		CustomerID: customerID,
		Period:     period,
		Amount:     report.TotalUsage,
		Currency:   "USD",
		Status:     "draft",
		CreatedAt:  time.Now().UTC(),
	}
	return invoice, nil
}

// HandleWebhook processes an incoming billing webhook payload (27.9).
// It publishes billingSuccess or billingFailure based on the event type.
func (b *MockBilling) HandleWebhook(ctx context.Context, payload []byte) error {
	var event struct {
		Type string                 `json:"type"`
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("billing: invalid webhook payload: %w", err)
	}
	detailType := "opensbt_billingSuccess"
	if event.Type == "payment.failed" || event.Type == "invoice.payment_failed" {
		detailType = "opensbt_billingFailure"
	}
	b.publishEvent(ctx, detailType, event.Data)
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func (b *MockBilling) publishEvent(ctx context.Context, detailType string, detail map[string]interface{}) {
	if b.eventBus == nil {
		return
	}
	_ = b.eventBus.Publish(ctx, models.NewEvent(detailType, b.eventBus.GetControlPlaneEventSource(), detail))
}
