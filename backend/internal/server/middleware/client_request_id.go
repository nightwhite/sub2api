package middleware

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ClientRequestID ensures every request has a unique client_request_id in request.Context().
//
// This is used by the Ops monitoring module for end-to-end request correlation.
func ClientRequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request == nil {
			c.Next()
			return
		}

		var id string
		if v := c.Request.Context().Value(ctxkey.ClientRequestID); v != nil {
			id, _ = v.(string)
		}
		if id == "" {
			id = uuid.New().String()
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ClientRequestID, id))
		}

		// Expose the correlation ID to clients for debugging (safe, no secrets).
		c.Writer.Header().Set("x-sub2api-request-id", id)
		c.Next()
	}
}
