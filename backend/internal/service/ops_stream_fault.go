package service

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// OpsStreamFaultKey stores a best-effort streaming fault marker on gin.Context.
// It is consumed by handler/ops_error_logger.go to persist non-HTTP streaming failures
// (e.g. client disconnect, upstream stream timeout) into ops_error_logs for UI debugging.
const OpsStreamFaultKey = "ops_stream_fault"

// OpsStreamFault describes an abnormal streaming termination that may not surface
// as an HTTP status code (because SSE already started and headers are sent).
//
// NOTE: Keep this struct small and avoid request/response bodies here.
// Use Message/Detail for small, sanitized troubleshooting hints only.
type OpsStreamFault struct {
	// phase: request|auth|routing|upstream|network|internal
	Phase string
	// type: arbitrary string used for ops_error_logs.error_type
	Type string

	// status_code: stored into ops_error_logs.status_code (synthetic allowed, e.g. 499)
	StatusCode int

	// message: short summary shown in ops list
	Message string
	// detail: best-effort troubleshooting detail shown in ops detail view (small text / json)
	Detail string

	// Optional overrides; if empty, ops_error_logger will derive from Phase.
	Owner  string
	Source string
}

func SetOpsStreamFault(c *gin.Context, fault OpsStreamFault) {
	if c == nil {
		return
	}
	if _, exists := c.Get(OpsStreamFaultKey); exists {
		return
	}

	fault.Phase = strings.TrimSpace(strings.ToLower(fault.Phase))
	fault.Type = strings.TrimSpace(fault.Type)
	fault.Message = strings.TrimSpace(fault.Message)
	fault.Detail = strings.TrimSpace(fault.Detail)
	fault.Owner = strings.TrimSpace(strings.ToLower(fault.Owner))
	fault.Source = strings.TrimSpace(strings.ToLower(fault.Source))

	if fault.Phase == "" {
		fault.Phase = "internal"
	}
	if fault.Type == "" {
		fault.Type = "stream_fault"
	}
	if fault.StatusCode <= 0 {
		// Make sure it shows up in request-errors lists (status_code>=400),
		// otherwise it can be invisible in UI even though the stream failed.
		fault.StatusCode = 499
	}

	c.Set(OpsStreamFaultKey, fault)
}
