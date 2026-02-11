-- Create ops_request_dumps table for storing full request captures for Ops debugging.
--
-- WARNING: This table may contain sensitive information (full headers + body).
-- Use with caution and ensure admin access is protected.

SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

CREATE TABLE IF NOT EXISTS ops_request_dumps (
    id BIGSERIAL PRIMARY KEY,

    -- Correlation / identities
    request_id VARCHAR(64),
    client_request_id VARCHAR(64),
    user_id BIGINT,
    api_key_id BIGINT,
    account_id BIGINT,
    group_id BIGINT,
    client_ip inet,

    -- Dimensions for filtering
    platform VARCHAR(32),
    model VARCHAR(100),

    -- Request metadata
    request_path VARCHAR(256),
    method VARCHAR(16),
    stream BOOLEAN NOT NULL DEFAULT false,
    status_code INT,

    -- Classification
    dump_type VARCHAR(64),

    -- Full request payloads (can be very large)
    request_headers JSONB,
    request_headers_bytes INT,
    request_body TEXT,
    request_body_bytes INT,
    upstream_request_body TEXT,
    upstream_request_body_bytes INT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE ops_request_dumps IS 'Ops request dumps: full headers + bodies for debugging (sensitive).';

CREATE INDEX IF NOT EXISTS idx_ops_request_dumps_created_at
    ON ops_request_dumps (created_at DESC);

CREATE INDEX IF NOT EXISTS idx_ops_request_dumps_request_id
    ON ops_request_dumps (request_id);

CREATE INDEX IF NOT EXISTS idx_ops_request_dumps_client_request_id
    ON ops_request_dumps (client_request_id);

