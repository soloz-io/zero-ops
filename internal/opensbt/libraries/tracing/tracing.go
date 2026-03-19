// Package tracing provides OpenTelemetry distributed tracing with automatic
// tenant context propagation (Tasks 25.1–25.6).
package tracing

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Tracer wraps OpenTelemetry with automatic tenant attribute injection (25.1–25.3).
type Tracer struct {
	tracer   trace.Tracer
	provider *sdktrace.TracerProvider
}

// New initialises an OTel TracerProvider with an OTLP/HTTP exporter pointing
// at jaegerEndpoint (e.g. "http://jaeger:4318") (25.1, 25.5).
// Pass empty jaegerEndpoint to use a no-op exporter (useful in tests).
func New(ctx context.Context, serviceName, jaegerEndpoint string) (*Tracer, error) {
	var exp sdktrace.SpanExporter
	var err error

	if jaegerEndpoint != "" {
		exp, err = otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(jaegerEndpoint),
			otlptracehttp.WithInsecure(),
		)
		if err != nil {
			return nil, fmt.Errorf("tracing: create exporter: %w", err)
		}
	} else {
		exp = &noopExporter{}
	}

	res, _ := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(serviceName)),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return &Tracer{
		tracer:   tp.Tracer(serviceName),
		provider: tp,
	}, nil
}

// StartSpan starts a span and injects tenant attributes (25.2, 25.3, 25.6).
func (t *Tracer) StartSpan(ctx context.Context, operationName string) (context.Context, trace.Span) {
	ctx, span := t.tracer.Start(ctx, operationName)
	span.SetAttributes(
		attribute.String("tenant.id", tenantIDFromCtx(ctx)),
		attribute.String("tenant.tier", tenantTierFromCtx(ctx)),
		attribute.String("user.id", userIDFromCtx(ctx)),
	)
	return ctx, span
}

// GinMiddleware starts a root span per request and propagates tenant context (25.4).
func (t *Tracer) GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		op := fmt.Sprintf("%s %s", c.Request.Method, c.FullPath())
		ctx, span := t.StartSpan(c.Request.Context(), op)
		defer span.End()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
		span.SetAttributes(attribute.Int("http.status_code", c.Writer.Status()))
	}
}

// Shutdown flushes pending spans.
func (t *Tracer) Shutdown(ctx context.Context) error {
	return t.provider.Shutdown(ctx)
}

// noopExporter discards all spans (used when no endpoint is configured).
type noopExporter struct{}

func (n *noopExporter) ExportSpans(_ context.Context, _ []sdktrace.ReadOnlySpan) error { return nil }
func (n *noopExporter) Shutdown(_ context.Context) error                               { return nil }

type ctxKey string

func tenantIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey("tenant_id")).(string)
	return v
}
func tenantTierFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey("tenant_tier")).(string)
	return v
}
func userIDFromCtx(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey("user_id")).(string)
	return v
}
