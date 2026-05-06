package middleware

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

// RequestLogger logs HTTP requests with timing
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		// Process request
		c.Next()

		// Calculate latency
		latency := time.Since(start)

		// Get status code
		statusCode := c.Writer.Status()

		// Build log message
		if raw != "" {
			path = path + "?" + raw
		}

		fmt.Printf("[kube-sbt-api] %s | %3d | %13v | %15s | %-7s %s\n",
			start.Format("2006/01/02 - 15:04:05"),
			statusCode,
			latency,
			c.ClientIP(),
			c.Request.Method,
			path,
		)
	}
}
