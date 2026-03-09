package middleware

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "zero_ops_api_http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "zero_ops_api_http_request_duration_seconds",
			Help:    "HTTP request latency",
			Buckets: []float64{0.005, 0.010, 0.025, 0.050, 0.075, 0.100, 0.150, 0.200, 0.500},
		},
		[]string{"method", "path"},
	)
)

func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.FullPath()
		if path == "" {
			path = "unknown"
		}

		timer := prometheus.NewTimer(httpRequestDuration.WithLabelValues(c.Request.Method, path))
		c.Next()
		timer.ObserveDuration()

		status := strconv.Itoa(c.Writer.Status())
		httpRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
	}
}
