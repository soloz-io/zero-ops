package interfaces

import (
	"context"

	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// IBilling provides billing system integration capabilities
type IBilling interface {
	// Customer Management
	CreateCustomer(ctx context.Context, customer models.BillingCustomer) error
	GetCustomer(ctx context.Context, customerID string) (*models.BillingCustomer, error)
	UpdateCustomer(ctx context.Context, customerID string, updates models.CustomerUpdates) error
	DeleteCustomer(ctx context.Context, customerID string) error

	// Subscription Management
	CreateSubscription(ctx context.Context, subscription models.Subscription) error
	GetSubscription(ctx context.Context, subscriptionID string) (*models.Subscription, error)
	UpdateSubscription(ctx context.Context, subscriptionID string, updates models.SubscriptionUpdates) error
	CancelSubscription(ctx context.Context, subscriptionID string) error

	// Usage and Billing
	RecordUsage(ctx context.Context, usage models.UsageRecord) error
	GetUsage(ctx context.Context, customerID string, period models.TimePeriod) (*models.UsageReport, error)
	GenerateInvoice(ctx context.Context, customerID string, period models.TimePeriod) (*models.Invoice, error)

	// Webhook Handling
	HandleWebhook(ctx context.Context, payload []byte) error
}
