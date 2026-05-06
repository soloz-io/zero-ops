package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RFC7807ErrorHandler converts errors to RFC 7807 Problem Details format
func RFC7807ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		// Check if there are any errors
		if len(c.Errors) > 0 {
			err := c.Errors.Last()

			// Determine status code
			status := c.Writer.Status()
			if status == http.StatusOK {
				status = http.StatusInternalServerError
			}

			// Build RFC 7807 Problem Details response
			problem := gin.H{
				"type":   getProblemType(status),
				"title":  http.StatusText(status),
				"status": status,
				"detail": err.Error(),
			}

			// Add instance if available
			if c.Request != nil {
				problem["instance"] = c.Request.URL.Path
			}

			c.JSON(status, problem)
		}
	}
}

// getProblemType returns RFC 7807 problem type URI
func getProblemType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "https://kube-sbt.io/problems/bad-request"
	case http.StatusUnauthorized:
		return "https://kube-sbt.io/problems/unauthorized"
	case http.StatusForbidden:
		return "https://kube-sbt.io/problems/forbidden"
	case http.StatusNotFound:
		return "https://kube-sbt.io/problems/not-found"
	case http.StatusConflict:
		return "https://kube-sbt.io/problems/conflict"
	case http.StatusTooManyRequests:
		return "https://kube-sbt.io/problems/rate-limit-exceeded"
	case http.StatusServiceUnavailable:
		return "https://kube-sbt.io/problems/service-unavailable"
	default:
		return "https://kube-sbt.io/problems/internal-error"
	}
}
