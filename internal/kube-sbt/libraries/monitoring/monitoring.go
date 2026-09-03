// Package monitoring provides clients for VictoriaMetrics, OpenSearch,
// Grafana Alloy, and K8sGPT (Tasks 26.1–26.6).
package monitoring

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Config holds endpoints for all monitoring backends (26.1–26.4).
type Config struct {
	VictoriaMetricsURL string // e.g. http://victoria-metrics:8428
	OpenSearchURL      string // e.g. http://opensearch:9200
	GrafanaAlloyURL    string // e.g. http://alloy:12345
	K8sGPTURL          string // e.g. http://k8sgpt:8080
}

// Client integrates with all monitoring backends.
type Client struct {
	cfg  Config
	http *http.Client
}

// New creates a monitoring Client.
func New(cfg Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: 10 * time.Second}}
}

// ─── VictoriaMetrics (26.1) ───────────────────────────────────────────────────

// PushMetrics sends Prometheus exposition-format metrics to VictoriaMetrics (26.1).
func (c *Client) PushMetrics(ctx context.Context, tenantID, metricsText string) error {
	if c.cfg.VictoriaMetricsURL == "" {
		return nil
	}
	url := fmt.Sprintf("%s/api/v1/import/prometheus?extra_label=tenant_id=%s", c.cfg.VictoriaMetricsURL, tenantID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBufferString(metricsText))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("monitoring: victoria push: HTTP %d: %s", resp.StatusCode, body)
	}
	return nil
}

// ─── OpenSearch (26.2) ────────────────────────────────────────────────────────

// IndexLog ships a log document to a tenant-isolated OpenSearch index (26.2).
func (c *Client) IndexLog(ctx context.Context, tenantID string, doc map[string]interface{}) error {
	if c.cfg.OpenSearchURL == "" {
		return nil
	}
	doc["tenant_id"] = tenantID
	doc["@timestamp"] = time.Now().UTC().Format(time.RFC3339)
	data, _ := json.Marshal(doc)
	index := fmt.Sprintf("tenant-%s-logs", tenantID)
	url := fmt.Sprintf("%s/%s/_doc", c.cfg.OpenSearchURL, index)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// ─── Grafana Alloy (26.3) ─────────────────────────────────────────────────────

// PushAlloyMetrics forwards metrics to Grafana Alloy's OTLP endpoint (26.3).
func (c *Client) PushAlloyMetrics(ctx context.Context, payload []byte) error {
	if c.cfg.GrafanaAlloyURL == "" {
		return nil
	}
	url := fmt.Sprintf("%s/v1/metrics", c.cfg.GrafanaAlloyURL)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// ─── K8sGPT (26.4) ───────────────────────────────────────────────────────────

// DiagnosticReport holds K8sGPT analysis results.
type DiagnosticReport struct {
	TenantID  string        `json:"tenant_id"`
	Namespace string        `json:"namespace"`
	Issues    []K8sGPTIssue `json:"issues"`
	AnalyzedAt time.Time    `json:"analyzed_at"`
}

// K8sGPTIssue represents a single cluster issue found by K8sGPT.
type K8sGPTIssue struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Error     string `json:"error"`
	Details   string `json:"details"`
	Severity  string `json:"severity"`
}

// RunDiagnostics calls K8sGPT to analyse a tenant namespace (26.4).
func (c *Client) RunDiagnostics(ctx context.Context, tenantID string) (*DiagnosticReport, error) {
	if c.cfg.K8sGPTURL == "" {
		return &DiagnosticReport{TenantID: tenantID, AnalyzedAt: time.Now().UTC()}, nil
	}
	namespace := fmt.Sprintf("tenant-%s", tenantID)
	url := fmt.Sprintf("%s/v1/analyze?namespace=%s&explain=true", c.cfg.K8sGPTURL, namespace)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var raw struct {
		Results []struct {
			Kind    string `json:"kind"`
			Name    string `json:"name"`
			Error   []struct{ Text string } `json:"error"`
			Details string `json:"details"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}

	report := &DiagnosticReport{TenantID: tenantID, Namespace: namespace, AnalyzedAt: time.Now().UTC()}
	for _, r := range raw.Results {
		errText := ""
		if len(r.Error) > 0 {
			errText = r.Error[0].Text
		}
		report.Issues = append(report.Issues, K8sGPTIssue{
			Kind: r.Kind, Name: r.Name, Error: errText, Details: r.Details,
		})
	}
	return report, nil
}

// ─── Alert Configuration (26.6) ──────────────────────────────────────────────

// AlertRule defines a tenant-specific alerting rule for VictoriaMetrics Alertmanager.
type AlertRule struct {
	TenantID   string `json:"tenant_id"`
	Name       string `json:"name"`
	Expr       string `json:"expr"`       // PromQL expression
	For        string `json:"for"`        // e.g. "5m"
	Severity   string `json:"severity"`   // critical, warning, info
	Summary    string `json:"summary"`
}

// CreateAlertRule registers a tenant alert rule via VictoriaMetrics ruler API (26.6).
func (c *Client) CreateAlertRule(ctx context.Context, rule AlertRule) error {
	if c.cfg.VictoriaMetricsURL == "" {
		return nil
	}
	body := map[string]interface{}{
		"groups": []map[string]interface{}{
			{
				"name": fmt.Sprintf("tenant-%s", rule.TenantID),
				"rules": []map[string]interface{}{
					{
						"alert": rule.Name,
						"expr":  rule.Expr,
						"for":   rule.For,
						"labels": map[string]string{
							"severity":  rule.Severity,
							"tenant_id": rule.TenantID,
						},
						"annotations": map[string]string{"summary": rule.Summary},
					},
				},
			},
		},
	}
	data, _ := json.Marshal(body)
	url := fmt.Sprintf("%s/api/v1/rules", c.cfg.VictoriaMetricsURL)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
