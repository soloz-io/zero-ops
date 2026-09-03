// Package logging provides tenant-aware structured logging (Tasks 20.1–20.6).
package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// Logger wraps logrus with automatic tenant context injection (20.1–20.5).
type Logger struct {
	log *logrus.Logger
	// OpenSearch config (20.6)
	openSearchURL   string
	openSearchIndex string
	httpClient      *http.Client
}

// New creates a Logger. level: "debug","info","warn","error" (20.5).
// openSearchURL is optional; empty disables OpenSearch forwarding (20.6).
func New(level, openSearchURL, openSearchIndex string) *Logger {
	l := logrus.New()
	l.SetFormatter(&logrus.JSONFormatter{TimestampFormat: time.RFC3339}) // 20.3
	if lvl, err := logrus.ParseLevel(level); err == nil {
		l.SetLevel(lvl)
	} else {
		l.SetLevel(logrus.InfoLevel)
	}
	return &Logger{
		log:             l,
		openSearchURL:   openSearchURL,
		openSearchIndex: openSearchIndex,
		httpClient:      &http.Client{Timeout: 5 * time.Second},
	}
}

// WithContext returns a logrus.Entry pre-populated with tenant fields (20.2).
func (l *Logger) WithContext(ctx context.Context) *logrus.Entry {
	return l.log.WithFields(logrus.Fields{
		"tenant_id":   tenantIDFromCtx(ctx),
		"tenant_tier": tenantTierFromCtx(ctx),
		"user_id":     userIDFromCtx(ctx),
	})
}

// Info logs at info level with tenant context (20.4).
func (l *Logger) Info(ctx context.Context, msg string, fields map[string]interface{}) {
	l.entry(ctx, fields).Info(msg)
	l.forward(ctx, "info", msg, fields)
}

// Error logs at error level with tenant context (20.4).
func (l *Logger) Error(ctx context.Context, msg string, err error, fields map[string]interface{}) {
	l.entry(ctx, fields).WithError(err).Error(msg)
	if fields == nil {
		fields = map[string]interface{}{}
	}
	if err != nil {
		fields["error"] = err.Error()
	}
	l.forward(ctx, "error", msg, fields)
}

// Warn logs at warn level with tenant context (20.4).
func (l *Logger) Warn(ctx context.Context, msg string, fields map[string]interface{}) {
	l.entry(ctx, fields).Warn(msg)
	l.forward(ctx, "warn", msg, fields)
}

func (l *Logger) entry(ctx context.Context, fields map[string]interface{}) *logrus.Entry {
	e := l.WithContext(ctx)
	for k, v := range fields {
		e = e.WithField(k, v)
	}
	return e
}

// forward ships a log entry to OpenSearch if configured (20.6).
func (l *Logger) forward(ctx context.Context, level, msg string, fields map[string]interface{}) {
	if l.openSearchURL == "" {
		return
	}
	doc := map[string]interface{}{
		"@timestamp":  time.Now().UTC().Format(time.RFC3339),
		"level":       level,
		"message":     msg,
		"tenant_id":   tenantIDFromCtx(ctx),
		"tenant_tier": tenantTierFromCtx(ctx),
		"user_id":     userIDFromCtx(ctx),
	}
	for k, v := range fields {
		doc[k] = v
	}
	data, _ := json.Marshal(doc)
	url := fmt.Sprintf("%s/%s/_doc", l.openSearchURL, l.openSearchIndex)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.httpClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

type ctxKey string

func tenantIDFromCtx(ctx context.Context) string   { v, _ := ctx.Value(ctxKey("tenant_id")).(string); return v }
func tenantTierFromCtx(ctx context.Context) string { v, _ := ctx.Value(ctxKey("tenant_tier")).(string); return v }
func userIDFromCtx(ctx context.Context) string     { v, _ := ctx.Value(ctxKey("user_id")).(string); return v }
