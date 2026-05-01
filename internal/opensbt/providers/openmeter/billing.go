package openmeter

import (
	"context"
	"fmt"

	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// BillingProvider implements interfaces.IBilling using OpenMeter Go SDK
// CORRECTED: Namespace passed as explicit parameter to all SDK methods
// Reference: archived/billing-metering/openmeter/openmeter/subscription/service/service.go:L138-L180
type BillingProvider struct {
	baseURL  string
	retryMax int
	// client will be initialized with OpenMeter SDK once imported
}

// NewBillingProvider creates an OpenMeter-backed IBilling implementation
func NewBillingProvider(baseURL string) (*BillingProvider, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("openmeter: baseURL is required")
	}

	return &BillingProvider{
		baseURL:  baseURL,
		retryMax: 3,
	}, nil
}

// CreateSubscription creates a subscription in OpenMeter (Req 16.1)
// CORRECTED: Pass namespace explicitly to SDK methods
// Reference: archived/billing-metering/openmeter/openmeter/subscription/service/service.go:L138-L180
func (b *BillingProvider) CreateSubscription(ctx context.Context, namespace, subjectID, planID string, opts models.SubscriptionOptions) error {
	// Validate inputs
	if namespace == "" {
		return fmt.Errorf("openmeter: namespace is required")
	}
	if subjectID == "" {
		return fmt.Errorf("openmeter: subjectID is required")
	}
	if planID == "" {
		return fmt.Errorf("openmeter: planID is required")
	}

	// NOTE: Namespace passed as explicit parameter to SDK methods
	// Implementation will use OpenMeter SDK: client.CreateSubscriptionWithResponse(ctx, namespace, req)
	return fmt.Errorf("openmeter: CreateSubscription not yet implemented - requires OpenMeter SDK integration")
}

// GetSubscription retrieves a subscription by ID (Req 16.9)
// CORRECTED: Pass namespace explicitly to SDK methods
func (b *BillingProvider) GetSubscription(ctx context.Context, namespace, subscriptionID string) (*models.Subscription, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if subscriptionID == "" {
		return nil, fmt.Errorf("openmeter: subscriptionID is required")
	}

	// NOTE: Namespace passed as explicit parameter to SDK methods
	// Implementation will use OpenMeter SDK: client.GetSubscriptionWithResponse(ctx, namespace, subscriptionID)
	return nil, fmt.Errorf("openmeter: GetSubscription not yet implemented - requires OpenMeter SDK integration")
}

// UpdateSubscription modifies a subscription (Req 16.3)
// VERIFIED: OpenMeter supports proration via ProRatingConfig (opt-in)
// Must be enabled in Plan: ProRatingConfig{Enabled: true, Mode: "prorate_prices"}
// Reference: archived/billing-metering/openmeter/openmeter/productcatalog/pro_rating.go:L23-L29
func (b *BillingProvider) UpdateSubscription(ctx context.Context, namespace, subscriptionID string, updates models.SubscriptionUpdates) error {
	if namespace == "" {
		return fmt.Errorf("openmeter: namespace is required")
	}
	if subscriptionID == "" {
		return fmt.Errorf("openmeter: subscriptionID is required")
	}

	// NOTE: Namespace passed as explicit parameter to SDK methods
	// Implementation will use OpenMeter SDK: client.UpdateSubscriptionWithResponse(ctx, namespace, subscriptionID, req)
	return fmt.Errorf("openmeter: UpdateSubscription not yet implemented - requires OpenMeter SDK integration")
}

// CancelSubscription marks subscription as inactive (Req 16.7, 16.8)
// CORRECTED: Pass namespace explicitly to SDK methods
func (b *BillingProvider) CancelSubscription(ctx context.Context, namespace, subscriptionID string) error {
	if namespace == "" {
		return fmt.Errorf("openmeter: namespace is required")
	}
	if subscriptionID == "" {
		return fmt.Errorf("openmeter: subscriptionID is required")
	}

	// NOTE: Namespace passed as explicit parameter to SDK methods
	// Implementation will use OpenMeter SDK: client.CancelSubscriptionWithResponse(ctx, namespace, subscriptionID)
	return fmt.Errorf("openmeter: CancelSubscription not yet implemented - requires OpenMeter SDK integration")
}

// ListSubscriptions lists subscriptions with filters (Req 16.9)
// CORRECTED: Pass namespace explicitly to SDK methods
func (b *BillingProvider) ListSubscriptions(ctx context.Context, namespace string, filters models.SubscriptionFilters) ([]models.Subscription, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}

	// NOTE: Namespace passed as explicit parameter to SDK methods
	// Implementation will use OpenMeter SDK: client.ListSubscriptionsWithResponse(ctx, namespace, params)
	return nil, fmt.Errorf("openmeter: ListSubscriptions not yet implemented - requires OpenMeter SDK integration")
}

// MigrateSubscription transitions subscription to new plan (Req 16.12)
// CORRECTED: Pass namespace explicitly to SDK methods
func (b *BillingProvider) MigrateSubscription(ctx context.Context, namespace, subscriptionID, newPlanID string, prorationBehavior string) error {
	if namespace == "" {
		return fmt.Errorf("openmeter: namespace is required")
	}
	if subscriptionID == "" {
		return fmt.Errorf("openmeter: subscriptionID is required")
	}
	if newPlanID == "" {
		return fmt.Errorf("openmeter: newPlanID is required")
	}

	// Validate proration behavior
	validBehaviors := map[string]bool{
		"create_prorated_invoice": true,
		"none":                    true,
		"credit_next_invoice":     true,
	}
	if prorationBehavior != "" && !validBehaviors[prorationBehavior] {
		return fmt.Errorf("openmeter: invalid prorationBehavior: %s", prorationBehavior)
	}

	// NOTE: Namespace passed as explicit parameter to SDK methods
	// Implementation will use OpenMeter SDK: client.MigrateSubscriptionWithResponse(ctx, namespace, subscriptionID, req)
	return fmt.Errorf("openmeter: MigrateSubscription not yet implemented - requires OpenMeter SDK integration")
}

// PreviewInvoice generates invoice preview (Req 17.1)
// CORRECTED: Pass namespace explicitly to SDK methods
func (b *BillingProvider) PreviewInvoice(ctx context.Context, namespace, subjectID string) (*models.Invoice, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if subjectID == "" {
		return nil, fmt.Errorf("openmeter: subjectID is required")
	}

	// NOTE: Namespace passed as explicit parameter to SDK methods
	// Implementation will use OpenMeter SDK: client.InvoicePendingLinesWithResponse(ctx, namespace, subjectID)
	return nil, fmt.Errorf("openmeter: PreviewInvoice not yet implemented - requires OpenMeter SDK integration")
}

// GetInvoice retrieves an invoice by ID (Req 17.2)
// CORRECTED: Pass namespace explicitly to SDK methods
func (b *BillingProvider) GetInvoice(ctx context.Context, namespace, invoiceID string) (*models.Invoice, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if invoiceID == "" {
		return nil, fmt.Errorf("openmeter: invoiceID is required")
	}

	// NOTE: Namespace passed as explicit parameter to SDK methods
	// Implementation will use OpenMeter SDK: client.GetInvoiceWithResponse(ctx, namespace, invoiceID)
	return nil, fmt.Errorf("openmeter: GetInvoice not yet implemented - requires OpenMeter SDK integration")
}

// ListInvoices lists invoices with filters (Req 17.3)
// CORRECTED: Pass namespace explicitly to SDK methods
func (b *BillingProvider) ListInvoices(ctx context.Context, namespace string, filters models.InvoiceFilters) ([]models.Invoice, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}

	// NOTE: Namespace passed as explicit parameter to SDK methods
	// Implementation will use OpenMeter SDK: client.ListInvoicesWithResponse(ctx, namespace, params)
	return nil, fmt.Errorf("openmeter: ListInvoices not yet implemented - requires OpenMeter SDK integration")
}

// Ensure BillingProvider implements interfaces.IBilling
var _ interfaces.IBilling = (*BillingProvider)(nil)
