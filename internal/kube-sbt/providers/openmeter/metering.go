package openmeter

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	openmeter "github.com/openmeterio/openmeter/api/client/go"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/interfaces"
	"github.com/soloz-io/zero-ops/internal/kube-sbt/models"
)

// MeteringProvider implements interfaces.IMetering using OpenMeter Go SDK
// CORRECTED: Namespace passed via OpenMeter-Namespace HTTP header for all requests
// Reference: archived/billing-metering/openmeter/openmeter/entitlement/adapter/entitlement.go:L62-L68
type MeteringProvider struct {
	client   *openmeter.ClientWithResponses
	baseURL  string
	retryMax int
}

// NewMeteringProvider creates an OpenMeter-backed IMetering implementation
func NewMeteringProvider(baseURL string) (*MeteringProvider, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("openmeter: baseURL is required")
	}

	// Create client with request editor to inject namespace header
	client, err := openmeter.NewClientWithResponses(baseURL)
	if err != nil {
		return nil, fmt.Errorf("openmeter: failed to create client: %w", err)
	}

	return &MeteringProvider{
		client:   client,
		baseURL:  baseURL,
		retryMax: 3,
	}, nil
}

// withNamespace creates a request editor that injects the OpenMeter-Namespace header
func (m *MeteringProvider) withNamespace(namespace string) openmeter.RequestEditorFn {
	return func(ctx context.Context, req *http.Request) error {
		req.Header.Set("OpenMeter-Namespace", namespace)
		return nil
	}
}

// RegisterSubject creates a subject in OpenMeter (Req 1.2)
// CORRECTED: Pass namespace via OpenMeter-Namespace HTTP header
// Reference: archived/billing-metering/openmeter/openmeter/entitlement/adapter/entitlement.go:L398-L404
func (m *MeteringProvider) RegisterSubject(ctx context.Context, namespace, subjectID string, metadata map[string]string) error {
	if namespace == "" {
		return fmt.Errorf("openmeter: namespace is required")
	}
	if subjectID == "" {
		return fmt.Errorf("openmeter: subjectID is required")
	}

	// Prepare request body
	req := openmeter.SubjectUpsert{
		Key: subjectID,
	}
	if metadata != nil {
		metadataInterface := make(map[string]interface{}, len(metadata))
		for k, v := range metadata {
			metadataInterface[k] = v
		}
		req.Metadata = &metadataInterface
	}

	// Execute with retry logic and namespace header
	var lastErr error
	for attempt := 0; attempt < m.retryMax; attempt++ {
		resp, err := m.client.UpsertSubjectWithResponse(ctx, openmeter.UpsertSubjectJSONRequestBody{req}, m.withNamespace(namespace))
		if err != nil {
			lastErr = fmt.Errorf("openmeter: RegisterSubject API call failed: %w", err)
			if attempt < m.retryMax-1 {
				time.Sleep(time.Duration(1<<uint(attempt)) * time.Second) // Exponential backoff: 1s, 2s, 4s
				continue
			}
			return lastErr
		}

		if resp.StatusCode() == 200 || resp.StatusCode() == 201 {
			return nil
		}

		lastErr = fmt.Errorf("openmeter: RegisterSubject failed: status=%d", resp.StatusCode())
		if resp.StatusCode() >= 500 && attempt < m.retryMax-1 {
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}
		return lastErr
	}

	return lastErr
}

// DeleteSubject removes a subject from OpenMeter (Req 21 reconciliation)
// CORRECTED: Pass namespace via OpenMeter-Namespace HTTP header
// Reference: archived/billing-metering/openmeter/openmeter/entitlement/adapter/entitlement.go:L62-L68
func (m *MeteringProvider) DeleteSubject(ctx context.Context, namespace, subjectID string) error {
	if namespace == "" {
		return fmt.Errorf("openmeter: namespace is required")
	}
	if subjectID == "" {
		return fmt.Errorf("openmeter: subjectID is required")
	}

	// Execute with retry logic and namespace header
	var lastErr error
	for attempt := 0; attempt < m.retryMax; attempt++ {
		resp, err := m.client.DeleteSubjectWithResponse(ctx, subjectID, m.withNamespace(namespace))
		if err != nil {
			lastErr = fmt.Errorf("openmeter: DeleteSubject API call failed: %w", err)
			if attempt < m.retryMax-1 {
				time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				continue
			}
			return lastErr
		}

		if resp.StatusCode() == 204 || resp.StatusCode() == 404 {
			return nil // 404 is acceptable - subject already deleted
		}

		lastErr = fmt.Errorf("openmeter: DeleteSubject failed: status=%d", resp.StatusCode())
		if resp.StatusCode() >= 500 && attempt < m.retryMax-1 {
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}
		return lastErr
	}

	return lastErr
}

// GetSubject retrieves a subject from OpenMeter
func (m *MeteringProvider) GetSubject(ctx context.Context, namespace, subjectID string) (*models.Subject, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if subjectID == "" {
		return nil, fmt.Errorf("openmeter: subjectID is required")
	}

	// Execute with retry logic and namespace header
	var lastErr error
	for attempt := 0; attempt < m.retryMax; attempt++ {
		resp, err := m.client.GetSubjectWithResponse(ctx, subjectID, m.withNamespace(namespace))
		if err != nil {
			lastErr = fmt.Errorf("openmeter: GetSubject API call failed: %w", err)
			if attempt < m.retryMax-1 {
				time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode() == 200 && resp.JSON200 != nil {
			subject := &models.Subject{
				ID:        resp.JSON200.Key,
				Namespace: namespace,
				Key:       resp.JSON200.Key,
				CreatedAt: time.Now(), // OpenMeter SDK may not provide this
			}
			if resp.JSON200.Metadata != nil {
				// Convert map[string]interface{} to map[string]string
				metadata := make(map[string]string)
				for k, v := range *resp.JSON200.Metadata {
					if str, ok := v.(string); ok {
						metadata[k] = str
					}
				}
				subject.Metadata = metadata
			}
			return subject, nil
		}

		if resp.StatusCode() == 404 {
			return nil, fmt.Errorf("openmeter: subject not found: %s", subjectID)
		}

		lastErr = fmt.Errorf("openmeter: GetSubject failed: status=%d", resp.StatusCode())
		if resp.StatusCode() >= 500 && attempt < m.retryMax-1 {
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}
		return nil, lastErr
	}

	return nil, lastErr
}

// ListSubjects lists all subjects in a namespace
func (m *MeteringProvider) ListSubjects(ctx context.Context, namespace string) ([]models.Subject, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}

	// Execute with retry logic and namespace header
	var lastErr error
	for attempt := 0; attempt < m.retryMax; attempt++ {
		resp, err := m.client.ListSubjectsWithResponse(ctx, m.withNamespace(namespace))
		if err != nil {
			lastErr = fmt.Errorf("openmeter: ListSubjects API call failed: %w", err)
			if attempt < m.retryMax-1 {
				time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode() == 200 && resp.JSON200 != nil {
			subjects := make([]models.Subject, 0, len(*resp.JSON200))
			for _, s := range *resp.JSON200 {
				subject := models.Subject{
					ID:        s.Key,
					Namespace: namespace,
					Key:       s.Key,
					CreatedAt: time.Now(),
				}
				if s.Metadata != nil {
					// Convert map[string]interface{} to map[string]string
					metadata := make(map[string]string)
					for k, v := range *s.Metadata {
						if str, ok := v.(string); ok {
							metadata[k] = str
						}
					}
					subject.Metadata = metadata
				}
				subjects = append(subjects, subject)
			}
			return subjects, nil
		}

		lastErr = fmt.Errorf("openmeter: ListSubjects failed: status=%d", resp.StatusCode())
		if resp.StatusCode() >= 500 && attempt < m.retryMax-1 {
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}
		return nil, lastErr
	}

	return nil, lastErr
}

// GetMeter retrieves a meter by ID (read-only for UI display)
func (m *MeteringProvider) GetMeter(ctx context.Context, namespace, meterID string) (*models.Meter, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if meterID == "" {
		return nil, fmt.Errorf("openmeter: meterID is required")
	}

	// Execute with retry logic and namespace header
	var lastErr error
	for attempt := 0; attempt < m.retryMax; attempt++ {
		resp, err := m.client.GetMeterWithResponse(ctx, meterID, m.withNamespace(namespace))
		if err != nil {
			lastErr = fmt.Errorf("openmeter: GetMeter API call failed: %w", err)
			if attempt < m.retryMax-1 {
				time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode() == 200 && resp.JSON200 != nil {
			meter := &models.Meter{
				ID:          resp.JSON200.Id,
				Namespace:   namespace,
				Slug:        resp.JSON200.Slug,
				Description: *resp.JSON200.Description,
				Aggregation: string(resp.JSON200.Aggregation),
				EventType:   resp.JSON200.EventType,
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			}
			return meter, nil
		}

		if resp.StatusCode() == 404 {
			return nil, fmt.Errorf("openmeter: meter not found: %s", meterID)
		}

		lastErr = fmt.Errorf("openmeter: GetMeter failed: status=%d", resp.StatusCode())
		if resp.StatusCode() >= 500 && attempt < m.retryMax-1 {
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}
		return nil, lastErr
	}

	return nil, lastErr
}

// ListMeters lists all meters in a namespace (read-only for UI display)
func (m *MeteringProvider) ListMeters(ctx context.Context, namespace string) ([]models.Meter, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}

	// Execute with retry logic and namespace header
	var lastErr error
	for attempt := 0; attempt < m.retryMax; attempt++ {
		resp, err := m.client.ListMetersWithResponse(ctx, &openmeter.ListMetersParams{}, m.withNamespace(namespace))
		if err != nil {
			lastErr = fmt.Errorf("openmeter: ListMeters API call failed: %w", err)
			if attempt < m.retryMax-1 {
				time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode() == 200 && resp.JSON200 != nil {
			meters := make([]models.Meter, 0, len(*resp.JSON200))
			for _, m := range *resp.JSON200 {
				meter := models.Meter{
					ID:          m.Id,
					Namespace:   namespace,
					Slug:        m.Slug,
					Description: *m.Description,
					Aggregation: string(m.Aggregation),
					EventType:   m.EventType,
					CreatedAt:   time.Now(),
					UpdatedAt:   time.Now(),
				}
				meters = append(meters, meter)
			}
			return meters, nil
		}

		lastErr = fmt.Errorf("openmeter: ListMeters failed: status=%d", resp.StatusCode())
		if resp.StatusCode() >= 500 && attempt < m.retryMax-1 {
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}
		return nil, lastErr
	}

	return nil, lastErr
}

// GetFeature retrieves a feature by ID (read-only for UI display)
func (m *MeteringProvider) GetFeature(ctx context.Context, namespace, featureID string) (*models.Feature, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if featureID == "" {
		return nil, fmt.Errorf("openmeter: featureID is required")
	}

	// Execute with retry logic and namespace header
	var lastErr error
	for attempt := 0; attempt < m.retryMax; attempt++ {
		resp, err := m.client.GetFeatureWithResponse(ctx, featureID, m.withNamespace(namespace))
		if err != nil {
			lastErr = fmt.Errorf("openmeter: GetFeature API call failed: %w", err)
			if attempt < m.retryMax-1 {
				time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode() == 200 && resp.JSON200 != nil {
			feature := &models.Feature{
				ID:        resp.JSON200.Id,
				Namespace: namespace,
				Key:       resp.JSON200.Key,
				Name:      resp.JSON200.Name,
				CreatedAt: time.Now(),
			}
			if resp.JSON200.MeterSlug != nil {
				feature.MeterSlugs = []string{*resp.JSON200.MeterSlug}
			}
			return feature, nil
		}

		if resp.StatusCode() == 404 {
			return nil, fmt.Errorf("openmeter: feature not found: %s", featureID)
		}

		lastErr = fmt.Errorf("openmeter: GetFeature failed: status=%d", resp.StatusCode())
		if resp.StatusCode() >= 500 && attempt < m.retryMax-1 {
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}
		return nil, lastErr
	}

	return nil, lastErr
}

// ListFeatures lists all features in a namespace (read-only for UI display)
func (m *MeteringProvider) ListFeatures(ctx context.Context, namespace string) ([]models.Feature, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}

	// Execute with retry logic and namespace header
	var lastErr error
	for attempt := 0; attempt < m.retryMax; attempt++ {
		resp, err := m.client.ListFeaturesWithResponse(ctx, &openmeter.ListFeaturesParams{}, m.withNamespace(namespace))
		if err != nil {
			lastErr = fmt.Errorf("openmeter: ListFeatures API call failed: %w", err)
			if attempt < m.retryMax-1 {
				time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode() == 200 && resp.JSON200 != nil {
			// Try to extract paginated response, fallback to empty list on error
			paginatedResp, err := resp.JSON200.AsFeaturePaginatedResponse()
			if err != nil {
				// Return empty list if no features configured
				return []models.Feature{}, nil
			}

			features := make([]models.Feature, 0, len(paginatedResp.Items))
			for _, f := range paginatedResp.Items {
				feature := models.Feature{
					ID:        f.Id,
					Namespace: namespace,
					Key:       f.Key,
					Name:      f.Name,
					CreatedAt: time.Now(),
				}
				if f.MeterSlug != nil {
					feature.MeterSlugs = []string{*f.MeterSlug}
				}
				features = append(features, feature)
			}
			return features, nil
		}

		lastErr = fmt.Errorf("openmeter: ListFeatures failed: status=%d", resp.StatusCode())
		if resp.StatusCode() >= 500 && attempt < m.retryMax-1 {
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}
		return nil, lastErr
	}

	return nil, lastErr
}

// GetPlan retrieves a plan by ID (read-only for UI display)
func (m *MeteringProvider) GetPlan(ctx context.Context, namespace, planID string) (*models.Plan, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if planID == "" {
		return nil, fmt.Errorf("openmeter: planID is required")
	}

	// Execute with retry logic and namespace header
	var lastErr error
	for attempt := 0; attempt < m.retryMax; attempt++ {
		resp, err := m.client.GetPlanWithResponse(ctx, planID, &openmeter.GetPlanParams{}, m.withNamespace(namespace))
		if err != nil {
			lastErr = fmt.Errorf("openmeter: GetPlan API call failed: %w", err)
			if attempt < m.retryMax-1 {
				time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode() == 200 && resp.JSON200 != nil {
			plan := &models.Plan{
				ID:          resp.JSON200.Id,
				Namespace:   namespace,
				Key:         resp.JSON200.Key,
				Name:        resp.JSON200.Name,
				Description: *resp.JSON200.Description,
				Currency:    string(resp.JSON200.Currency),
				CreatedAt:   time.Now(),
			}
			// Note: Phases mapping would require more complex logic
			// For now, returning basic plan info
			return plan, nil
		}

		if resp.StatusCode() == 404 {
			return nil, fmt.Errorf("openmeter: plan not found: %s", planID)
		}

		lastErr = fmt.Errorf("openmeter: GetPlan failed: status=%d", resp.StatusCode())
		if resp.StatusCode() >= 500 && attempt < m.retryMax-1 {
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}
		return nil, lastErr
	}

	return nil, lastErr
}

// ListPlans lists all plans in a namespace (read-only for UI display)
func (m *MeteringProvider) ListPlans(ctx context.Context, namespace string) ([]models.Plan, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}

	// Execute with retry logic and namespace header
	var lastErr error
	for attempt := 0; attempt < m.retryMax; attempt++ {
		resp, err := m.client.ListPlansWithResponse(ctx, &openmeter.ListPlansParams{}, m.withNamespace(namespace))
		if err != nil {
			lastErr = fmt.Errorf("openmeter: ListPlans API call failed: %w", err)
			if attempt < m.retryMax-1 {
				time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode() == 200 && resp.JSON200 != nil {
			plans := make([]models.Plan, 0, len(resp.JSON200.Items))
			for _, p := range resp.JSON200.Items {
				plan := models.Plan{
					ID:          p.Id,
					Namespace:   namespace,
					Key:         p.Key,
					Name:        p.Name,
					Description: *p.Description,
					Currency:    string(p.Currency),
					CreatedAt:   time.Now(),
				}
				plans = append(plans, plan)
			}
			return plans, nil
		}

		lastErr = fmt.Errorf("openmeter: ListPlans failed: status=%d", resp.StatusCode())
		if resp.StatusCode() >= 500 && attempt < m.retryMax-1 {
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}
		return nil, lastErr
	}

	return nil, lastErr
}

// GetUsage queries usage data with namespace isolation (Req 2, 3)
// CORRECTED (Gap 3.1): Tenant aggregation via GroupBy attributes, not FilterSubject wildcard
// Reference: archived/billing-metering/openmeter/openmeter/streaming/clickhouse/meter_query.go:L156-L180
func (m *MeteringProvider) GetUsage(ctx context.Context, namespace string, filter models.UsageFilter) (*models.UsageReport, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if filter.MeterSlug == "" {
		return nil, fmt.Errorf("openmeter: meterSlug is required")
	}

	// Prepare query parameters
	params := &openmeter.QueryMeterParams{
		From: &filter.Period.Start,
		To:   &filter.Period.End,
	}
	if filter.SubjectID != "" {
		subjects := []string{filter.SubjectID}
		params.Subject = &subjects
	}
	if len(filter.GroupBy) > 0 {
		params.GroupBy = &filter.GroupBy
	}

	// Execute with retry logic and namespace header
	var lastErr error
	for attempt := 0; attempt < m.retryMax; attempt++ {
		resp, err := m.client.QueryMeterWithResponse(ctx, filter.MeterSlug, params, m.withNamespace(namespace))
		if err != nil {
			lastErr = fmt.Errorf("openmeter: GetUsage API call failed: %w", err)
			if attempt < m.retryMax-1 {
				time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				continue
			}
			return nil, lastErr
		}

		if resp.StatusCode() == 200 && resp.JSON200 != nil && len(resp.JSON200.Data) > 0 {
			report := &models.UsageReport{
				MeterSlug: filter.MeterSlug,
				Namespace: namespace,
				Period:    filter.Period,
				Value:     resp.JSON200.Data[0].Value,
			}
			if resp.JSON200.Data[0].GroupBy != nil {
				// Convert map[string]*string to map[string]float64
				groupBy := make(map[string]float64)
				for k, v := range resp.JSON200.Data[0].GroupBy {
					if v != nil {
						// Parse string value to float64
						if floatVal, err := strconv.ParseFloat(*v, 64); err == nil {
							groupBy[k] = floatVal
						} else {
							groupBy[k] = 0 // Default to 0 if parsing fails
						}
					}
				}
				report.GroupBy = groupBy
			}
			return report, nil
		}

		if resp.StatusCode() == 200 {
			// No data found, return zero usage
			return &models.UsageReport{
				MeterSlug: filter.MeterSlug,
				Namespace: namespace,
				Period:    filter.Period,
				Value:     0,
			}, nil
		}

		lastErr = fmt.Errorf("openmeter: GetUsage failed: status=%d", resp.StatusCode())
		if resp.StatusCode() >= 500 && attempt < m.retryMax-1 {
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}
		return nil, lastErr
	}

	return nil, lastErr
}

// GetTenantUsage aggregates usage across all users in a tenant (Req 2)
// CORRECTED (Gap 3.1): Use GroupBy["tenant_id"] for aggregation, not FilterSubject wildcard
func (m *MeteringProvider) GetTenantUsage(ctx context.Context, namespace string, period models.TimePeriod) (*models.TenantUsageData, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}

	// Query all meters with tenant_id groupBy
	// AgentGateway MUST emit tenant_id as OTLP attribute for this to work
	meters, err := m.ListMeters(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("openmeter: failed to list meters: %w", err)
	}

	result := &models.TenantUsageData{
		TenantID:  namespace, // namespace == tenant_id
		Namespace: namespace,
		Period:    period,
		Meters:    make(map[string]float64),
	}

	for _, meter := range meters {
		params := &openmeter.QueryMeterParams{
			From:    &period.Start,
			To:      &period.End,
			GroupBy: &[]string{"tenant_id"}, // Aggregate by tenant_id attribute
		}

		resp, err := m.client.QueryMeterWithResponse(ctx, meter.Slug, params, m.withNamespace(namespace))
		if err != nil {
			continue // Skip failed meters
		}

		if resp.StatusCode() == 200 && resp.JSON200 != nil && len(resp.JSON200.Data) > 0 {
			// Find the row matching this tenant_id
			for _, row := range resp.JSON200.Data {
				if row.GroupBy != nil {
					if tenantIDPtr, ok := row.GroupBy["tenant_id"]; ok && tenantIDPtr != nil && *tenantIDPtr == namespace {
						result.Meters[meter.Slug] = row.Value
						break
					}
				}
			}
		}
	}

	return result, nil
}

// GetUserUsage retrieves usage for a specific user (Req 3)
func (m *MeteringProvider) GetUserUsage(ctx context.Context, namespace, subjectID string, period models.TimePeriod) (*models.UserUsageData, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if subjectID == "" {
		return nil, fmt.Errorf("openmeter: subjectID is required")
	}

	// Query all meters with subject filter
	meters, err := m.ListMeters(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("openmeter: failed to list meters: %w", err)
	}

	result := &models.UserUsageData{
		SubjectID: subjectID,
		Namespace: namespace,
		Period:    period,
		Meters:    make(map[string]float64),
	}

	for _, meter := range meters {
		subjects := []string{subjectID}
		params := &openmeter.QueryMeterParams{
			From:    &period.Start,
			To:      &period.End,
			Subject: &subjects,
		}

		resp, err := m.client.QueryMeterWithResponse(ctx, meter.Slug, params, m.withNamespace(namespace))
		if err != nil {
			continue // Skip failed meters
		}

		if resp.StatusCode() == 200 && resp.JSON200 != nil && len(resp.JSON200.Data) > 0 {
			result.Meters[meter.Slug] = resp.JSON200.Data[0].Value
		}
	}

	return result, nil
}

// CheckEntitlement queries OpenMeter Entitlement API with fail-open (Req 6)
// CORRECTED: Pass namespace explicitly to SDK methods
// Reference: archived/billing-metering/openmeter/openmeter/entitlement/adapter/entitlement.go:L62-L73
func (m *MeteringProvider) CheckEntitlement(ctx context.Context, namespace, subjectID, featureKey string) (*models.EntitlementStatus, error) {
	if namespace == "" {
		return nil, fmt.Errorf("openmeter: namespace is required")
	}
	if subjectID == "" {
		return nil, fmt.Errorf("openmeter: subjectID is required")
	}
	if featureKey == "" {
		return nil, fmt.Errorf("openmeter: featureKey is required")
	}

	// Execute with retry logic and namespace header
	for attempt := 0; attempt < m.retryMax; attempt++ {
		resp, err := m.client.GetEntitlementValueWithResponse(ctx, subjectID, featureKey, &openmeter.GetEntitlementValueParams{}, m.withNamespace(namespace))
		if err != nil {
			if attempt < m.retryMax-1 {
				time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
				continue
			}
			// Fail-open: allow access if OpenMeter unreachable after all retries (Req 6.7)
			return &models.EntitlementStatus{
				HasAccess:  true,
				IsFallback: true,
				ResetTime:  time.Now().Add(24 * time.Hour),
			}, nil
		}

		if resp.StatusCode() == 200 && resp.JSON200 != nil {
			entitlement := resp.JSON200
			status := &models.EntitlementStatus{
				HasAccess:  entitlement.HasAccess,
				IsFallback: false,
			}
			if entitlement.Balance != nil {
				status.Used = int64(*entitlement.Balance)
			}
			// Note: OpenMeter SDK may not provide Limit and ResetTime in all cases
			// These would need to be extracted from the entitlement configuration
			return status, nil
		}

		if resp.StatusCode() >= 500 && attempt < m.retryMax-1 {
			time.Sleep(time.Duration(1<<uint(attempt)) * time.Second)
			continue
		}

		// For non-500 errors, fail-open
		return &models.EntitlementStatus{
			HasAccess:  true,
			IsFallback: true,
			ResetTime:  time.Now().Add(24 * time.Hour),
		}, nil
	}

	// Fail-open: allow access if all retries exhausted (Req 6.7)
	return &models.EntitlementStatus{
		HasAccess:  true,
		IsFallback: true,
		ResetTime:  time.Now().Add(24 * time.Hour),
	}, nil
}

// Ensure MeteringProvider implements interfaces.IMetering
var _ interfaces.IMetering = (*MeteringProvider)(nil)
