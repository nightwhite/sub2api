package service

import (
	"context"
	"strings"
)

// OpsRequestDebugBundle aggregates request-related records for troubleshooting.
//
// It intentionally does NOT include full prompt/response bodies.
// It is used by Ops UI to drill down into a single request id (or client_request_id).
type OpsRequestDebugBundle struct {
	Key string

	// UsageLogs are records from usage_logs with request_id == Key (if any).
	UsageLogs []*UsageLog
	// ErrorLogs are ops_error_logs where request_id == Key OR client_request_id == Key.
	ErrorLogs []*OpsErrorLog
}

func (s *OpsService) GetRequestDebugBundle(ctx context.Context, key string, limit int) (*OpsRequestDebugBundle, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return &OpsRequestDebugBundle{Key: "", UsageLogs: []*UsageLog{}, ErrorLogs: []*OpsErrorLog{}}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if s.opsRepo == nil {
		return &OpsRequestDebugBundle{Key: key, UsageLogs: []*UsageLog{}, ErrorLogs: []*OpsErrorLog{}}, nil
	}
	out, err := s.opsRepo.GetRequestDebugBundle(ctx, key, limit)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return &OpsRequestDebugBundle{Key: key, UsageLogs: []*UsageLog{}, ErrorLogs: []*OpsErrorLog{}}, nil
	}
	out.Key = key
	if out.UsageLogs == nil {
		out.UsageLogs = []*UsageLog{}
	}
	if out.ErrorLogs == nil {
		out.ErrorLogs = []*OpsErrorLog{}
	}
	return out, nil
}
