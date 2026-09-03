package openmeter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/soloz-io/zero-ops/internal/kube-sbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/models"
)

// BillingProvider implements interfaces.IBilling using OpenMeter HTTP API with retry logic
// CORRECTED: Uses /openmeter/* paths and OpenMeter-Namespace header for v1.0.0-beta.227
type BillingProvider struct {
	baseURL     string
	httpClient  *http.Client
	retryConfig RetryConfig
}

// NewBillingProvider creates an OpenMeter-backed IBilling implementation
func NewBillingProvider(baseURL string) (*BillingProvider, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("openmeter: baseURL is required")
	}

	return &BillingProvider{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		retryConfig: DefaultRetryConfig(),
	}, nil
}

// CreateSubscription creates a subscription in OpenMeter
func (b *BillingProvider) CreateSubscription(ctx context.Context, namespace, subjectID, planID string, opts models.SubscriptionOptions) error {
	if namespace == "" {
		return fmt.Errorf("openmeter: namespace is required")
	}
	if subjectID == "" {
		return fmt.Errorf("openmeter: subjectID is required")
	}
	if planID == "" {
		return fmt.Errorf("openmeter: planID is required")
	}

	reqBody := map[string]interface{}{
		"customerId": subjectID,
		"plan": map[string]string{
			"key": planID,
		},
	}
	
	if opts.StartDate != nil {
		reqBody["activeFrom"] = opts.StartDate.Format(time.RFC3339)
	}
	
	if opts.Metadata != nil && len(opts.Metadata) > 0 {
		metadata := make(map[string]interface{})
		for k, v := range opts.Metadata {
			metadata[k] = v
		}
		reqBody["metadata"] = metadata
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("openmeter: failed to marshal request: %w", err)
	}

	return RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		apiURL := fmt.Sprintf("%s/openmeter/subscriptions", b.baseURL)

		req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("OpenMeter-Namespace", namespace)

		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: create subscription failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		return nil
	})
}

// GetSubscription retrieves a subscription by ID
func (b *BillingProvider) GetSubscription(ctx context.Context, namespace, subscriptionID string) (*models.Subscription, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if subscriptionID == "" {
		return nil, fmt.Errorf("openmeter: subscriptionID is required")
	}

	var subscription *models.Subscription

	err := RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		apiURL := fmt.Sprintf("%s/openmeter/subscriptions/%s", b.baseURL, subscriptionID)

		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		req.Header.Set("OpenMeter-Namespace", namespace)

		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("openmeter: subscription not found: %s", subscriptionID)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: get subscription failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		var respData map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
			return fmt.Errorf("openmeter: failed to decode response: %w", err)
		}

		subscription = &models.Subscription{
			ID:        respData["id"].(string),
			Namespace: namespace,
			SubjectID: respData["customerId"].(string),
			Status:    respData["status"].(string),
		}

		if plan, ok := respData["plan"].(map[string]interface{}); ok {
			if key, ok := plan["key"].(string); ok {
				subscription.PlanID = key
			}
		}

		if createdAt, ok := respData["createdAt"].(string); ok {
			if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
				subscription.CreatedAt = t
			}
		}

		if updatedAt, ok := respData["updatedAt"].(string); ok {
			if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
				subscription.UpdatedAt = t
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return subscription, nil
}

// UpdateSubscription modifies a subscription
func (b *BillingProvider) UpdateSubscription(ctx context.Context, namespace, subscriptionID string, updates models.SubscriptionUpdates) error {
	if namespace == "" {
		return fmt.Errorf("openmeter: namespace is required")
	}
	if subscriptionID == "" {
		return fmt.Errorf("openmeter: subscriptionID is required")
	}

	reqBody := make(map[string]interface{})
	
	if updates.PlanID != nil {
		reqBody["plan"] = map[string]string{
			"key": *updates.PlanID,
		}
	}
	
	if updates.Status != nil {
		reqBody["status"] = *updates.Status
	}
	
	if updates.Metadata != nil && len(updates.Metadata) > 0 {
		metadata := make(map[string]interface{})
		for k, v := range updates.Metadata {
			metadata[k] = v
		}
		reqBody["metadata"] = metadata
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("openmeter: failed to marshal request: %w", err)
	}

	return RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		apiURL := fmt.Sprintf("%s/openmeter/subscriptions/%s", b.baseURL, subscriptionID)

		req, err := http.NewRequestWithContext(ctx, "PATCH", apiURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("OpenMeter-Namespace", namespace)

		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: update subscription failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		return nil
	})
}

// CancelSubscription marks subscription as inactive
func (b *BillingProvider) CancelSubscription(ctx context.Context, namespace, subscriptionID string) error {
	if namespace == "" {
		return fmt.Errorf("openmeter: namespace is required")
	}
	if subscriptionID == "" {
		return fmt.Errorf("openmeter: subscriptionID is required")
	}

	reqBody := map[string]interface{}{
		"effectiveDate": time.Now().Format(time.RFC3339),
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("openmeter: failed to marshal request: %w", err)
	}

	return RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		apiURL := fmt.Sprintf("%s/openmeter/subscriptions/%s/cancel", b.baseURL, subscriptionID)

		req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("OpenMeter-Namespace", namespace)

		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: cancel subscription failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		return nil
	})
}

// ListSubscriptions lists subscriptions with filters
func (b *BillingProvider) ListSubscriptions(ctx context.Context, namespace string, filters models.SubscriptionFilters) ([]models.Subscription, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}

	var subscriptions []models.Subscription

	err := RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		apiURL := fmt.Sprintf("%s/openmeter/subscriptions", b.baseURL)

		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		req.Header.Set("OpenMeter-Namespace", namespace)

		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: list subscriptions failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		// Parse paginated response
		var respData struct {
			Items      []map[string]interface{} `json:"items"`
			TotalCount int                      `json:"totalCount"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
			return fmt.Errorf("openmeter: failed to decode response: %w", err)
		}

		subscriptions = make([]models.Subscription, 0, len(respData.Items))
		for _, item := range respData.Items {
			subscription := models.Subscription{
				ID:        item["id"].(string),
				Namespace: namespace,
				SubjectID: item["customerId"].(string),
				Status:    item["status"].(string),
			}

			if plan, ok := item["plan"].(map[string]interface{}); ok {
				if key, ok := plan["key"].(string); ok {
					subscription.PlanID = key
				}
			}

			if createdAt, ok := item["createdAt"].(string); ok {
				if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
					subscription.CreatedAt = t
				}
			}

			if updatedAt, ok := item["updatedAt"].(string); ok {
				if t, err := time.Parse(time.RFC3339, updatedAt); err == nil {
					subscription.UpdatedAt = t
				}
			}

			subscriptions = append(subscriptions, subscription)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return subscriptions, nil
}

// MigrateSubscription transitions subscription to new plan
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

	validBehaviors := map[string]bool{
		"create_prorated_invoice": true,
		"none":                    true,
		"credit_next_invoice":     true,
	}
	if prorationBehavior != "" && !validBehaviors[prorationBehavior] {
		return fmt.Errorf("openmeter: invalid prorationBehavior: %s", prorationBehavior)
	}

	reqBody := map[string]interface{}{
		"plan": map[string]string{
			"key": newPlanID,
		},
	}
	
	if prorationBehavior != "" {
		reqBody["prorationBehavior"] = prorationBehavior
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("openmeter: failed to marshal request: %w", err)
	}

	return RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		apiURL := fmt.Sprintf("%s/openmeter/subscriptions/%s/change", b.baseURL, subscriptionID)

		req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("OpenMeter-Namespace", namespace)

		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: migrate subscription failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		return nil
	})
}

// PreviewInvoice generates invoice preview
func (b *BillingProvider) PreviewInvoice(ctx context.Context, namespace, subjectID string) (*models.Invoice, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if subjectID == "" {
		return nil, fmt.Errorf("openmeter: subjectID is required")
	}

	var invoice *models.Invoice

	err := RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		apiURL := fmt.Sprintf("%s/openmeter/customers/%s/invoices/simulate", b.baseURL, subjectID)

		req, err := http.NewRequestWithContext(ctx, "POST", apiURL, nil)
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("OpenMeter-Namespace", namespace)

		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: preview invoice failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		var respData map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
			return fmt.Errorf("openmeter: failed to decode response: %w", err)
		}

		invoice = &models.Invoice{
			Namespace: namespace,
			SubjectID: subjectID,
			Status:    "draft",
		}

		if id, ok := respData["id"].(string); ok {
			invoice.ID = id
		}

		if currency, ok := respData["currency"].(string); ok {
			invoice.Currency = currency
		}

		if totals, ok := respData["totals"].(map[string]interface{}); ok {
			if total, ok := totals["total"].(float64); ok {
				invoice.Total = total
			}
			if subtotal, ok := totals["subtotal"].(float64); ok {
				invoice.Subtotal = subtotal
			}
			if tax, ok := totals["tax"].(float64); ok {
				invoice.Tax = tax
			}
		}

		if lines, ok := respData["lines"].(map[string]interface{}); ok {
			if data, ok := lines["data"].([]interface{}); ok {
				invoice.LineItems = make([]models.LineItem, 0, len(data))
				for _, item := range data {
					if itemMap, ok := item.(map[string]interface{}); ok {
						lineItem := models.LineItem{}
						if name, ok := itemMap["name"].(string); ok {
							lineItem.Description = name
						}
						if qty, ok := itemMap["quantity"].(float64); ok {
							lineItem.Quantity = qty
						}
						if amount, ok := itemMap["amount"].(float64); ok {
							lineItem.Amount = amount
						}
						invoice.LineItems = append(invoice.LineItems, lineItem)
					}
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return invoice, nil
}

// GetInvoice retrieves an invoice by ID
func (b *BillingProvider) GetInvoice(ctx context.Context, namespace, invoiceID string) (*models.Invoice, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if invoiceID == "" {
		return nil, fmt.Errorf("openmeter: invoiceID is required")
	}

	var invoice *models.Invoice

	err := RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		apiURL := fmt.Sprintf("%s/openmeter/invoices/%s?expand=lines", b.baseURL, invoiceID)

		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		req.Header.Set("OpenMeter-Namespace", namespace)

		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("openmeter: invoice not found: %s", invoiceID)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: get invoice failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		var respData map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
			return fmt.Errorf("openmeter: failed to decode response: %w", err)
		}

		invoice = &models.Invoice{
			ID:        respData["id"].(string),
			Namespace: namespace,
		}

		if customer, ok := respData["customer"].(map[string]interface{}); ok {
			if id, ok := customer["id"].(string); ok {
				invoice.SubjectID = id
			}
		}

		if currency, ok := respData["currency"].(string); ok {
			invoice.Currency = currency
		}

		if status, ok := respData["status"].(map[string]interface{}); ok {
			if statusStr, ok := status["status"].(string); ok {
				invoice.Status = statusStr
			}
		}

		if totals, ok := respData["totals"].(map[string]interface{}); ok {
			if total, ok := totals["total"].(float64); ok {
				invoice.Total = total
			}
			if subtotal, ok := totals["subtotal"].(float64); ok {
				invoice.Subtotal = subtotal
			}
			if tax, ok := totals["tax"].(float64); ok {
				invoice.Tax = tax
			}
		}

		if createdAt, ok := respData["createdAt"].(string); ok {
			if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
				invoice.CreatedAt = t
			}
		}

		if lines, ok := respData["lines"].(map[string]interface{}); ok {
			if data, ok := lines["data"].([]interface{}); ok {
				invoice.LineItems = make([]models.LineItem, 0, len(data))
				for _, item := range data {
					if itemMap, ok := item.(map[string]interface{}); ok {
						lineItem := models.LineItem{}
						if name, ok := itemMap["name"].(string); ok {
							lineItem.Description = name
						}
						if qty, ok := itemMap["quantity"].(float64); ok {
							lineItem.Quantity = qty
						}
						if amount, ok := itemMap["amount"].(float64); ok {
							lineItem.Amount = amount
						}
						invoice.LineItems = append(invoice.LineItems, lineItem)
					}
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return invoice, nil
}

// ListInvoices lists invoices with filters
func (b *BillingProvider) ListInvoices(ctx context.Context, namespace string, filters models.InvoiceFilters) ([]models.Invoice, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}

	var invoices []models.Invoice

	err := RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		apiURL := fmt.Sprintf("%s/openmeter/invoices", b.baseURL)

		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		req.Header.Set("OpenMeter-Namespace", namespace)

		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: list invoices failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		var respData struct {
			Items      []map[string]interface{} `json:"items"`
			TotalCount int                      `json:"totalCount"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
			return fmt.Errorf("openmeter: failed to decode response: %w", err)
		}

		invoices = make([]models.Invoice, 0, len(respData.Items))
		for _, item := range respData.Items {
			invoice := models.Invoice{
				ID:        item["id"].(string),
				Namespace: namespace,
			}

			if customer, ok := item["customer"].(map[string]interface{}); ok {
				if id, ok := customer["id"].(string); ok {
					invoice.SubjectID = id
				}
			}

			if currency, ok := item["currency"].(string); ok {
				invoice.Currency = currency
			}

			if status, ok := item["status"].(map[string]interface{}); ok {
				if statusStr, ok := status["status"].(string); ok {
					invoice.Status = statusStr
				}
			}

			if totals, ok := item["totals"].(map[string]interface{}); ok {
				if total, ok := totals["total"].(float64); ok {
					invoice.Total = total
				}
			}

			if createdAt, ok := item["createdAt"].(string); ok {
				if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
					invoice.CreatedAt = t
				}
			}

			invoices = append(invoices, invoice)
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return invoices, nil
}

// Ensure BillingProvider implements interfaces.IBilling
var _ interfaces.IBilling = (*BillingProvider)(nil)
