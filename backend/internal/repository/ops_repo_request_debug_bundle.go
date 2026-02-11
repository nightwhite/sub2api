package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *opsRepository) GetRequestDebugBundle(ctx context.Context, key string, limit int) (*service.OpsRequestDebugBundle, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return &service.OpsRequestDebugBundle{Key: "", UsageLogs: []*service.UsageLog{}, ErrorLogs: []*service.OpsErrorLog{}}, nil
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	// 1) usage_logs by request_id
	usageLogs := make([]*service.UsageLog, 0, 2)
	usageSQL := "SELECT " + usageLogSelectColumns + " FROM usage_logs WHERE request_id = $1 ORDER BY created_at DESC LIMIT $2"
	usageRows, err := r.db.QueryContext(ctx, usageSQL, key, limit)
	if err != nil {
		return nil, err
	}
	for usageRows.Next() {
		log, err := scanUsageLog(usageRows)
		if err != nil {
			_ = usageRows.Close()
			return nil, err
		}
		usageLogs = append(usageLogs, log)
	}
	if err := usageRows.Err(); err != nil {
		_ = usageRows.Close()
		return nil, err
	}
	_ = usageRows.Close()

	// 2) ops_error_logs by request_id OR client_request_id (include recovered upstream events too)
	errors := make([]*service.OpsErrorLog, 0, 8)
	errSQL := `
SELECT
  e.id,
  e.created_at,
  e.error_phase,
  e.error_type,
  COALESCE(e.error_owner, ''),
  COALESCE(e.error_source, ''),
  e.severity,
  COALESCE(e.upstream_status_code, e.status_code, 0),
  COALESCE(e.platform, ''),
  COALESCE(e.model, ''),
  COALESCE(e.is_retryable, false),
  COALESCE(e.retry_count, 0),
  COALESCE(e.resolved, false),
  e.resolved_at,
  e.resolved_by_user_id,
  COALESCE(u2.email, ''),
  e.resolved_retry_id,
  COALESCE(e.client_request_id, ''),
  COALESCE(e.request_id, ''),
  COALESCE(e.error_message, ''),
  e.user_id,
  COALESCE(u.email, ''),
  e.api_key_id,
  e.account_id,
  COALESCE(a.name, ''),
  e.group_id,
  COALESCE(g.name, ''),
  CASE WHEN e.client_ip IS NULL THEN NULL ELSE e.client_ip::text END,
  COALESCE(e.request_path, ''),
  e.stream
FROM ops_error_logs e
LEFT JOIN accounts a ON e.account_id = a.id
LEFT JOIN groups g ON e.group_id = g.id
LEFT JOIN users u ON e.user_id = u.id
LEFT JOIN users u2 ON e.resolved_by_user_id = u2.id
WHERE (COALESCE(e.request_id,'') = $1 OR COALESCE(e.client_request_id,'') = $1)
ORDER BY e.created_at DESC
LIMIT $2
`

	errRows, err := r.db.QueryContext(ctx, errSQL, key, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = errRows.Close() }()

	toTimePtr := func(v sql.NullTime) *time.Time {
		if !v.Valid {
			return nil
		}
		t := v.Time
		return &t
	}
	toInt64Ptr := func(v sql.NullInt64) *int64 {
		if !v.Valid {
			return nil
		}
		i := v.Int64
		return &i
	}
	toStringPtr := func(v sql.NullString) *string {
		if !v.Valid {
			return nil
		}
		s := v.String
		return &s
	}

	for errRows.Next() {
		var item service.OpsErrorLog
		var statusCode sql.NullInt64
		var clientIP sql.NullString
		var userID sql.NullInt64
		var apiKeyID sql.NullInt64
		var accountID sql.NullInt64
		var accountName string
		var groupID sql.NullInt64
		var groupName string
		var userEmail string
		var resolvedAt sql.NullTime
		var resolvedBy sql.NullInt64
		var resolvedByName string
		var resolvedRetryID sql.NullInt64
		if err := errRows.Scan(
			&item.ID,
			&item.CreatedAt,
			&item.Phase,
			&item.Type,
			&item.Owner,
			&item.Source,
			&item.Severity,
			&statusCode,
			&item.Platform,
			&item.Model,
			&item.IsRetryable,
			&item.RetryCount,
			&item.Resolved,
			&resolvedAt,
			&resolvedBy,
			&resolvedByName,
			&resolvedRetryID,
			&item.ClientRequestID,
			&item.RequestID,
			&item.Message,
			&userID,
			&userEmail,
			&apiKeyID,
			&accountID,
			&accountName,
			&groupID,
			&groupName,
			&clientIP,
			&item.RequestPath,
			&item.Stream,
		); err != nil {
			return nil, err
		}
		item.ResolvedAt = toTimePtr(resolvedAt)
		item.ResolvedByUserID = toInt64Ptr(resolvedBy)
		item.ResolvedByUserName = resolvedByName
		item.ResolvedRetryID = toInt64Ptr(resolvedRetryID)

		item.StatusCode = int(statusCode.Int64)
		if !statusCode.Valid {
			item.StatusCode = 0
		}

		item.ClientIP = toStringPtr(clientIP)
		item.UserID = toInt64Ptr(userID)
		item.UserEmail = userEmail
		item.APIKeyID = toInt64Ptr(apiKeyID)
		item.AccountID = toInt64Ptr(accountID)
		item.AccountName = accountName
		item.GroupID = toInt64Ptr(groupID)
		item.GroupName = groupName

		// Ensure request ids are never empty to keep UI stable.
		item.RequestID = strings.TrimSpace(item.RequestID)
		item.ClientRequestID = strings.TrimSpace(item.ClientRequestID)
		if item.RequestID == "" && item.ClientRequestID != "" {
			item.RequestID = item.ClientRequestID
		}

		errors = append(errors, &item)
	}
	if err := errRows.Err(); err != nil {
		return nil, err
	}

	return &service.OpsRequestDebugBundle{
		Key:       key,
		UsageLogs: usageLogs,
		ErrorLogs: errors,
	}, nil
}
