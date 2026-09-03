package models

import (
	"fmt"
	"time"
)

// Subscription represents an active plan assignment (Req 16)
type Subscription struct {
	ID        string    `json:"id"`
	Namespace string    `json:"namespace"`
	SubjectID string    `json:"subjectId"`
	PlanID    string    `json:"planId"`
	Status    string    `json:"status"` // active, inactive, cancelled
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// SubscriptionOptions for creating subscriptions
type SubscriptionOptions struct {
	StartDate *time.Time        `json:"startDate,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// SubscriptionUpdates for modifying subscriptions
type SubscriptionUpdates struct {
	PlanID   *string           `json:"planId,omitempty"`
	Status   *string           `json:"status,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// SubscriptionFilters for listing subscriptions
type SubscriptionFilters struct {
	SubjectID *string `json:"subjectId,omitempty"`
	PlanID    *string `json:"planId,omitempty"`
	Status    *string `json:"status,omitempty"`
	Limit     int     `json:"limit,omitempty"`
	Offset    int     `json:"offset,omitempty"`
}

// Invoice represents a billing invoice (Req 17)
type Invoice struct {
	ID        string     `json:"id"`
	Namespace string     `json:"namespace"`
	SubjectID string     `json:"subjectId"`
	Period    TimePeriod `json:"period"`
	LineItems []LineItem `json:"lineItems"`
	Subtotal  float64    `json:"subtotal"`
	Tax       float64    `json:"tax"`
	Total     float64    `json:"total"`
	Currency  string     `json:"currency"`
	Status    string     `json:"status"` // draft, open, paid, void
	CreatedAt time.Time  `json:"createdAt"`
}

// LineItem represents an invoice line
type LineItem struct {
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unitPrice"`
	Amount      float64 `json:"amount"`
}

// InvoiceFilters for listing invoices
type InvoiceFilters struct {
	SubjectID *string `json:"subjectId,omitempty"`
	Status    *string `json:"status,omitempty"`
	Limit     int     `json:"limit,omitempty"`
	Offset    int     `json:"offset,omitempty"`
}

// StripeConfig for configuring Stripe App (Req 18)
type StripeConfig struct {
	APIKey        string `json:"apiKey"`
	WebhookSecret string `json:"webhookSecret"`
}

// Validate validates Subscription fields
func (s *Subscription) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("subscription ID is required")
	}
	if s.Namespace == "" {
		return fmt.Errorf("subscription namespace is required")
	}
	if s.SubjectID == "" {
		return fmt.Errorf("subscription subjectID is required")
	}
	if s.PlanID == "" {
		return fmt.Errorf("subscription planID is required")
	}
	return nil
}

// Validate validates Invoice fields
func (i *Invoice) Validate() error {
	if i.ID == "" {
		return fmt.Errorf("invoice ID is required")
	}
	if i.Namespace == "" {
		return fmt.Errorf("invoice namespace is required")
	}
	if i.SubjectID == "" {
		return fmt.Errorf("invoice subjectID is required")
	}
	if i.Currency == "" {
		return fmt.Errorf("invoice currency is required")
	}
	return nil
}
