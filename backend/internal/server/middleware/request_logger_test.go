package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestLogger_PreservesValidRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestLogger())
	router.GET("/test", func(c *gin.Context) {
		reqID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
		c.String(http.StatusOK, reqID)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(requestIDHeader, "safe-id_123.ABC")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "safe-id_123.ABC", rec.Header().Get(requestIDHeader))
	require.Equal(t, "safe-id_123.ABC", rec.Body.String())
}

func TestRequestLogger_ReplacesUnsafeRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(RequestLogger())
	router.GET("/test", func(c *gin.Context) {
		reqID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
		c.String(http.StatusOK, reqID)
	})

	tooLong := strings.Repeat("a", maxRequestIDLength+1)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(requestIDHeader, tooLong)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	generated := rec.Header().Get(requestIDHeader)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotEmpty(t, generated)
	require.NotEqual(t, tooLong, generated)
	require.LessOrEqual(t, len(generated), maxRequestIDLength)

	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.Header.Set(requestIDHeader, "bad id with spaces")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code)
	require.NotEqual(t, "bad id with spaces", rec2.Header().Get(requestIDHeader))
}
