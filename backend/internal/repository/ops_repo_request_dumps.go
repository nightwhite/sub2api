package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func opsNullRawString(s string) any {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func (r *opsRepository) InsertRequestDump(ctx context.Context, input *service.OpsInsertRequestDumpInput) (int64, error) {
	if r == nil || r.db == nil {
		return 0, fmt.Errorf("nil ops repository")
	}
	if input == nil {
		return 0, fmt.Errorf("nil input")
	}

	// Marshal headers to JSON for JSONB column.
	var headersJSON *string
	if len(input.RequestHeaders) > 0 {
		raw, err := json.Marshal(input.RequestHeaders)
		if err != nil {
			return 0, err
		}
		s := string(raw)
		headersJSON = &s
		if input.RequestHeadersBytes <= 0 {
			input.RequestHeadersBytes = len(raw)
		}
	}

	if input.RequestBodyBytes <= 0 && input.RequestBody != "" {
		input.RequestBodyBytes = len(input.RequestBody)
	}
	if input.UpstreamRequestBodyBytes <= 0 && input.UpstreamRequestBody != "" {
		input.UpstreamRequestBodyBytes = len(input.UpstreamRequestBody)
	}

	q := `
INSERT INTO ops_request_dumps (
  request_id,
  client_request_id,
  user_id,
  api_key_id,
  account_id,
  group_id,
  client_ip,
  platform,
  model,
  request_path,
  method,
  stream,
  status_code,
  dump_type,
  request_headers,
  request_headers_bytes,
  request_body,
  request_body_bytes,
  upstream_request_body,
  upstream_request_body_bytes,
  created_at
) VALUES (
  $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21
) RETURNING id`

	var id int64
	err := r.db.QueryRowContext(
		ctx,
		q,
		opsNullString(input.RequestID),
		opsNullString(input.ClientRequestID),
		opsNullInt64(input.UserID),
		opsNullInt64(input.APIKeyID),
		opsNullInt64(input.AccountID),
		opsNullInt64(input.GroupID),
		opsNullString(input.ClientIP),
		opsNullString(input.Platform),
		opsNullString(input.Model),
		opsNullString(input.RequestPath),
		opsNullString(input.Method),
		input.Stream,
		opsNullInt(input.StatusCode),
		opsNullString(input.DumpType),
		opsNullString(headersJSON),
		opsNullInt(input.RequestHeadersBytes),
		opsNullRawString(input.RequestBody),
		opsNullInt(input.RequestBodyBytes),
		opsNullRawString(input.UpstreamRequestBody),
		opsNullInt(input.UpstreamRequestBodyBytes),
		input.CreatedAt,
	).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (r *opsRepository) GetRequestDumpByKey(ctx context.Context, key string) (*service.OpsRequestDump, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, sql.ErrNoRows
	}

	q := `
SELECT
  d.id,
  d.created_at,
  COALESCE(d.dump_type, ''),
  COALESCE(d.request_id, ''),
  COALESCE(d.client_request_id, ''),
  d.user_id,
  d.api_key_id,
  d.account_id,
  d.group_id,
  CASE WHEN d.client_ip IS NULL THEN NULL ELSE d.client_ip::text END,
  COALESCE(d.platform, ''),
  COALESCE(d.model, ''),
  COALESCE(d.request_path, ''),
  COALESCE(d.method, ''),
  COALESCE(d.stream, false),
  COALESCE(d.status_code, 0),
  d.request_headers,
  COALESCE(d.request_headers_bytes, 0),
  COALESCE(d.request_body, ''),
  COALESCE(d.request_body_bytes, 0),
  COALESCE(d.upstream_request_body, ''),
  COALESCE(d.upstream_request_body_bytes, 0)
FROM ops_request_dumps d
WHERE (COALESCE(d.request_id,'') = $1 OR COALESCE(d.client_request_id,'') = $1)
ORDER BY d.created_at DESC
LIMIT 1
`

	var (
		item service.OpsRequestDump

		userID    sql.NullInt64
		apiKeyID  sql.NullInt64
		accountID sql.NullInt64
		groupID   sql.NullInt64
		clientIP  sql.NullString

		headersBytes int
		bodyBytes    int
		upBytes      int

		headersRaw []byte
	)

	if err := r.db.QueryRowContext(ctx, q, key).Scan(
		&item.ID,
		&item.CreatedAt,
		&item.DumpType,
		&item.RequestID,
		&item.ClientRequestID,
		&userID,
		&apiKeyID,
		&accountID,
		&groupID,
		&clientIP,
		&item.Platform,
		&item.Model,
		&item.RequestPath,
		&item.Method,
		&item.Stream,
		&item.StatusCode,
		&headersRaw,
		&headersBytes,
		&item.RequestBody,
		&bodyBytes,
		&item.UpstreamRequestBody,
		&upBytes,
	); err != nil {
		return nil, err
	}

	item.RequestHeadersBytes = headersBytes
	item.RequestBodyBytes = bodyBytes
	item.UpstreamRequestBodyBytes = upBytes

	if userID.Valid {
		v := userID.Int64
		item.UserID = &v
	}
	if apiKeyID.Valid {
		v := apiKeyID.Int64
		item.APIKeyID = &v
	}
	if accountID.Valid {
		v := accountID.Int64
		item.AccountID = &v
	}
	if groupID.Valid {
		v := groupID.Int64
		item.GroupID = &v
	}
	if clientIP.Valid {
		v := clientIP.String
		item.ClientIP = &v
	}

	item.RequestHeaders = map[string][]string{}
	if len(headersRaw) > 0 {
		_ = json.Unmarshal(headersRaw, &item.RequestHeaders)
	}

	return &item, nil
}
