package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/soloz-io/zero-ops/internal/zero-ops-api/service"
)

func ErrorMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return
		}

		err := c.Errors.Last().Err

		if apiErr, ok := err.(*service.APIError); ok {
			c.JSON(apiErr.HTTPStatus, gin.H{"error": apiErr})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{
			"error": service.NewInternalError("An unexpected error occurred"),
		})
	}
}
