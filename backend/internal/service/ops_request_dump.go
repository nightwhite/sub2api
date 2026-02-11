package service

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"
)

// OpsRequestDump stores full request capture for troubleshooting.
//
// WARNING: This struct may contain sensitive information (full headers + body).
// Only expose it to trusted admins.
type OpsRequestDump struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`

	DumpType string `json:"dump_type"`

	RequestID       string `json:"request_id"`
	ClientRequestID string `json:"client_request_id"`

	UserID    *int64  `json:"user_id,omitempty"`
	APIKeyID  *int64  `json:"api_key_id,omitempty"`
	AccountID *int64  `json:"account_id,omitempty"`
	GroupID   *int64  `json:"group_id,omitempty"`
	ClientIP  *string `json:"client_ip,omitempty"`

	Platform    string `json:"platform"`
	Model       string `json:"model"`
	RequestPath string `json:"request_path"`
	Method      string `json:"method"`
	Stream      bool   `json:"stream"`
	StatusCode  int    `json:"status_code"`

	RequestHeaders           map[string][]string `json:"request_headers"`
	RequestHeadersBytes      int                 `json:"request_headers_bytes"`
	RequestBody              string              `json:"request_body"`
	RequestBodyBytes         int                 `json:"request_body_bytes"`
	UpstreamRequestBody      string              `json:"upstream_request_body"`
	UpstreamRequestBodyBytes int                 `json:"upstream_request_body_bytes"`
}

type OpsInsertRequestDumpInput struct {
	RequestID       string
	ClientRequestID string

	UserID    *int64
	APIKeyID  *int64
	AccountID *int64
	GroupID   *int64
	ClientIP  *string

	Platform    string
	Model       string
	RequestPath string
	Method      string
	Stream      bool
	StatusCode  int
	DumpType    string

	RequestHeaders           map[string][]string
	RequestHeadersBytes      int
	RequestBody              string
	RequestBodyBytes         int
	UpstreamRequestBody      string
	UpstreamRequestBodyBytes int

	CreatedAt time.Time
}

func (s *OpsService) RecordRequestDump(ctx context.Context, input *OpsInsertRequestDumpInput) error {
	if input == nil {
		return nil
	}
	if !s.IsMonitoringEnabled(ctx) {
		return nil
	}
	if s.opsRepo == nil {
		return nil
	}

	input.RequestID = strings.TrimSpace(input.RequestID)
	input.ClientRequestID = strings.TrimSpace(input.ClientRequestID)
	if input.RequestID == "" && input.ClientRequestID == "" {
		return nil
	}

	input.DumpType = strings.TrimSpace(input.DumpType)
	if input.DumpType == "" {
		input.DumpType = "request_dump"
	}

	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now()
	}

	if _, err := s.opsRepo.InsertRequestDump(ctx, input); err != nil {
		// Never bubble up to gateway; best-effort logging.
		log.Printf("[Ops] RecordRequestDump failed: %v", err)
		return err
	}
	return nil
}

func (s *OpsService) GetRequestDumpByKey(ctx context.Context, key string) (*OpsRequestDump, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("invalid request key")
	}
	if s.opsRepo == nil {
		return nil, sql.ErrNoRows
	}
	item, err := s.opsRepo.GetRequestDumpByKey(ctx, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, err
	}
	return item, nil
}
