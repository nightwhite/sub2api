package middleware

import (
	"context"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const requestIDHeader = "X-Request-ID"
const maxRequestIDLength = 128

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// RequestLogger 在请求入口注入 request-scoped logger。
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request == nil {
			c.Next()
			return
		}

		requestID := normalizeRequestID(c.GetHeader(requestIDHeader))
		c.Header(requestIDHeader, requestID)

		ctx := context.WithValue(c.Request.Context(), ctxkey.RequestID, requestID)
		clientRequestID, _ := ctx.Value(ctxkey.ClientRequestID).(string)

		fields := []zap.Field{
			zap.String("component", "http"),
			zap.String("request_id", requestID),
			zap.String("path", c.Request.URL.Path),
			zap.String("method", c.Request.Method),
		}
		if trimmed := strings.TrimSpace(clientRequestID); trimmed != "" {
			fields = append(fields, zap.String("client_request_id", trimmed))
		}
		requestLogger := logger.With(fields...)

		ctx = logger.IntoContext(ctx, requestLogger)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func normalizeRequestID(raw string) string {
	requestID := strings.TrimSpace(raw)
	if requestID == "" {
		return uuid.NewString()
	}
	if len(requestID) > maxRequestIDLength {
		return uuid.NewString()
	}
	if !requestIDPattern.MatchString(requestID) {
		return uuid.NewString()
	}
	return requestID
}
