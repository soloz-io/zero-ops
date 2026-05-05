package openmeter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/soloz-io/zero-ops/internal/opensbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/opensbt/models"
)

// BillingProvider implements interfaces.IBilling using OpenMeter HTTP API with retry logic
// CORRECTED: Namespace passed as explicit parameter to all API calls with exponential backoff retry
// Reference: archived/billing-metering/openmeter/openmeter/subscription/service/service.go:L138-L180
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

// CreateSubscription creates a subscription in OpenMeter (Req 16.1)
// CORRECTED: Pass namespace explicitly via query parameter with retry logic (Req 5.5)
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

	// Build request body
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

	// Execute with retry logic (Req 5.5: exponential backoff)
	return RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		// Build URL with namespace query parameter
		apiURL := fmt.Sprintf("%s/api/v1/subscriptions?namespace=%s", b.baseURL, url.QueryEscape(namespace))

		// Create HTTP request
		req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")

		// Execute request
		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		// Check response status
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: create subscription failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		return nil
	})
}

// GetSubscription retrieves a subscription by ID (Req 16.9)
// CORRECTED: Pass namespace explicitly via query parameter with retry logic
func (b *BillingProvider) GetSubscription(ctx context.Context, namespace, subscriptionID string) (*models.Subscription, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if subscriptionID == "" {
		return nil, fmt.Errorf("openmeter: subscriptionID is required")
	}

	var subscription *models.Subscription

	err := RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		// Build URL with namespace query parameter
		apiURL := fmt.Sprintf("%s/api/v1/subscriptions/%s?namespace=%s", b.baseURL, url.PathEscape(subscriptionID), url.QueryEscape(namespace))

		// Create HTTP request
		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		// Execute request
		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		// Check response status
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("openmeter: subscription not found: %s", subscriptionID)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: get subscription failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		// Parse response
		var respData map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
			return fmt.Errorf("openmeter: failed to decode response: %w", err)
		}

		// Map to internal model
		subscription = &models.Subscription{
			ID:        respData["id"].(string),
			Namespace: namespace,
			SubjectID: respData["customerId"].(string),
			Status:    respData["status"].(string),
		}

		// Parse plan reference
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

	// Build request body
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
		// Build URL with namespace query parameter
		apiURL := fmt.Sprintf("%s/api/v1/subscriptions/%s?namespace=%s", b.baseURL, url.PathEscape(subscriptionID), url.QueryEscape(namespace))

		// Create HTTP request
		req, err := http.NewRequestWithContext(ctx, "PATCH", apiURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")

		// Execute request
		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		// Check response status
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: update subscription failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		return nil
	})
}

// CancelSubscription marks subscription as inactive (Req 16.7, 16.8)
// CORRECTED: Pass namespace explicitly via query parameter with retry logic
func (b *BillingProvider) CancelSubscription(ctx context.Context, namespace, subscriptionID string) error {
	if namespace == "" {
		return fmt.Errorf("openmeter: namespace is required")
	}
	if subscriptionID == "" {
		return fmt.Errorf("openmeter: subscriptionID is required")
	}

	// Build request body
	reqBody := map[string]interface{}{
		"effectiveDate": time.Now().Format(time.RFC3339),
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("openmeter: failed to marshal request: %w", err)
	}

	return RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		// Build URL with namespace query parameter
		apiURL := fmt.Sprintf("%s/api/v1/subscriptions/%s/cancel?namespace=%s", b.baseURL, url.PathEscape(subscriptionID), url.QueryEscape(namespace))

		// Create HTTP request
		req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")

		// Execute request
		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		// Check response status
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: cancel subscription failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		return nil
	})
}

// ListSubscriptions lists subscriptions with filters (Req 16.9)
// CORRECTED: Pass namespace explicitly via query parameter with retry logic
func (b *BillingProvider) ListSubscriptions(ctx context.Context, namespace string, filters models.SubscriptionFilters) ([]models.Subscription, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}

	var subscriptions []models.Subscription

	err := RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		// Build URL with query parameters
		params := url.Values{}
		params.Set("namespace", namespace)
		
		if filters.SubjectID != nil {
			params.Set("customerId", *filters.SubjectID)
		}
		
		if filters.PlanID != nil {
			params.Set("planKey", *filters.PlanID)
		}
		
		if filters.Status != nil {
			params.Set("status", *filters.Status)
		}
		
		if filters.Limit > 0 {
			params.Set("limit", fmt.Sprintf("%d", filters.Limit))
		}
		
		if filters.Offset > 0 {
			params.Set("offset", fmt.Sprintf("%d", filters.Offset))
		}

		apiURL := fmt.Sprintf("%s/api/v1/subscriptions?%s", b.baseURL, params.Encode())

		// Create HTTP request
		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		// Execute request
		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		// Check response status
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: list subscriptions failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		// Parse response
		var respData []map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
			return fmt.Errorf("openmeter: failed to decode response: %w", err)
		}

		// Map to internal models
		subscriptions = make([]models.Subscription, 0, len(respData))
		for _, item := range respData {
			subscription := models.Subscription{
				ID:        item["id"].(string),
				Namespace: namespace,
				SubjectID: item["customerId"].(string),
				Status:    item["status"].(string),
			}

			// Parse plan reference
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

// MigrateSubscription transitions subscription to new plan (Req 16.12)
// CORRECTED: Pass namespace explicitly via query parameter with retry logic
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

	// Build request body
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
		// Build URL with namespace query parameter
		apiURL := fmt.Sprintf("%s/api/v1/subscriptions/%s/change?namespace=%s", b.baseURL, url.PathEscape(subscriptionID), url.QueryEscape(namespace))

		// Create HTTP request
		req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")

		// Execute request
		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		// Check response status
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: migrate subscription failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		return nil
	})
}

// PreviewInvoice generates invoice preview (Req 17.1)
// CORRECTED: Pass namespace explicitly via query parameter with retry logic
func (b *BillingProvider) PreviewInvoice(ctx context.Context, namespace, subjectID string) (*models.Invoice, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if subjectID == "" {
		return nil, fmt.Errorf("openmeter: subjectID is required")
	}

	var invoice *models.Invoice

	err := RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		// Build URL with namespace query parameter
		apiURL := fmt.Sprintf("%s/api/v1/customers/%s/invoices/simulate?namespace=%s", b.baseURL, url.PathEscape(subjectID), url.QueryEscape(namespace))

		// Create HTTP request
		req, err := http.NewRequestWithContext(ctx, "POST", apiURL, nil)
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")

		// Execute request
		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		// Check response status
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: preview invoice failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		// Parse response
		var respData map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
			return fmt.Errorf("openmeter: failed to decode response: %w", err)
		}

		// Map to internal model
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

		// Parse totals
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

		// Parse line items
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

// GetInvoice retrieves an invoice by ID (Req 17.2)
// CORRECTED: Pass namespace explicitly via query parameter with retry logic
func (b *BillingProvider) GetInvoice(ctx context.Context, namespace, invoiceID string) (*models.Invoice, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if invoiceID == "" {
		return nil, fmt.Errorf("openmeter: invoiceID is required")
	}

	var invoice *models.Invoice

	err := RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		// Build URL with namespace query parameter
		apiURL := fmt.Sprintf("%s/api/v1/invoices/%s?namespace=%s&expand=lines", b.baseURL, url.PathEscape(invoiceID), url.QueryEscape(namespace))

		// Create HTTP request
		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		// Execute request
		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		// Check response status
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("openmeter: invoice not found: %s", invoiceID)
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: get invoice failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		// Parse response
		var respData map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
			return fmt.Errorf("openmeter: failed to decode response: %w", err)
		}

		// Map to internal model
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

		// Parse totals
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

		// Parse line items
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

// ListInvoices lists invoices with filters (Req 17.3)
// CORRECTED: Pass namespace explicitly via query parameter with retry logic
func (b *BillingProvider) ListInvoices(ctx context.Context, namespace string, filters models.InvoiceFilters) ([]models.Invoice, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}

	var invoices []models.Invoice

	err := RetryWithExponentialBackoff(ctx, b.retryConfig, func() error {
		// Build URL with query parameters
		params := url.Values{}
		params.Set("namespace", namespace)
		
		if filters.SubjectID != nil {
			params.Set("customers", *filters.SubjectID)
		}
		
		if filters.Status != nil {
			params.Set("statuses", *filters.Status)
		}
		
		if filters.Limit > 0 {
			params.Set("limit", fmt.Sprintf("%d", filters.Limit))
		}
		
		if filters.Offset > 0 {
			params.Set("offset", fmt.Sprintf("%d", filters.Offset))
		}

		apiURL := fmt.Sprintf("%s/api/v1/invoices?%s", b.baseURL, params.Encode())

		// Create HTTP request
		req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		if err != nil {
			return fmt.Errorf("openmeter: failed to create request: %w", err)
		}

		// Execute request
		resp, err := b.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("openmeter: request failed: %w", err)
		}
		defer resp.Body.Close()

		// Check response status
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("openmeter: list invoices failed: status=%d body=%s", resp.StatusCode, string(body))
		}

		// Parse response
		var respData map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
			return fmt.Errorf("openmeter: failed to decode response: %w", err)
		}

		// Parse items array
		if items, ok := respData["items"].([]interface{}); ok {
			invoices = make([]models.Invoice, 0, len(items))
			for _, item := range items {
				if itemMap, ok := item.(map[string]interface{}); ok {
					invoice := models.Invoice{
						ID:        itemMap["id"].(string),
						Namespace: namespace,
					}

					if customer, ok := itemMap["customer"].(map[string]interface{}); ok {
						if id, ok := customer["id"].(string); ok {
							invoice.SubjectID = id
						}
					}

					if currency, ok := itemMap["currency"].(string); ok {
						invoice.Currency = currency
					}

					if status, ok := itemMap["status"].(map[string]interface{}); ok {
						if statusStr, ok := status["status"].(string); ok {
							invoice.Status = statusStr
						}
					}

					// Parse totals
					if totals, ok := itemMap["totals"].(map[string]interface{}); ok {
						if total, ok := totals["total"].(float64); ok {
							invoice.Total = total
						}
					}

					if createdAt, ok := itemMap["createdAt"].(string); ok {
						if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
							invoice.CreatedAt = t
						}
					}

					invoices = append(invoices, invoice)
				}
			}
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
