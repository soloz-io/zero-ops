package interfaces

import (
	"context"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/models"
)

// IBilling provides runtime subscription and invoice management via OpenMeter
// ARCHITECTURAL NOTE: Plans managed via hub-operator CRDs
// This interface handles ONLY dynamic subscription assignments and invoice queries
type IBilling interface {
	// Subscription Management (Req 16) - Dynamic Runtime Operations
	CreateSubscription(ctx context.Context, namespace, subjectID, planID string, opts models.SubscriptionOptions) error
	GetSubscription(ctx context.Context, namespace, subscriptionID string) (*models.Subscription, error)
	UpdateSubscription(ctx context.Context, namespace, subscriptionID string, updates models.SubscriptionUpdates) error
	CancelSubscription(ctx context.Context, namespace, subscriptionID string) error
	ListSubscriptions(ctx context.Context, namespace string, filters models.SubscriptionFilters) ([]models.Subscription, error)
	MigrateSubscription(ctx context.Context, namespace, subscriptionID, newPlanID string, prorationBehavior string) error

	// Invoice Operations (Req 17) - Read-Only Queries
	PreviewInvoice(ctx context.Context, namespace, subjectID string) (*models.Invoice, error)
	GetInvoice(ctx context.Context, namespace, invoiceID string) (*models.Invoice, error)
	ListInvoices(ctx context.Context, namespace string, filters models.InvoiceFilters) ([]models.Invoice, error)
}
