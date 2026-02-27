-- 057_add_ops_system_logs_request_id_trgm_index.sql
-- 为 ops_system_logs 的 request_id/client_request_id 模糊查询提供 trigram 索引（可选）。

DO $$
BEGIN
  BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_trgm;
  EXCEPTION WHEN OTHERS THEN
    -- 缺少权限或扩展不可用时跳过，不阻塞整体迁移。
    RAISE NOTICE 'pg_trgm extension not created: %', SQLERRM;
  END;

  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
    EXECUTE 'CREATE INDEX IF NOT EXISTS idx_ops_system_logs_request_id_trgm
             ON ops_system_logs USING gin (request_id gin_trgm_ops)';
    EXECUTE 'CREATE INDEX IF NOT EXISTS idx_ops_system_logs_client_request_id_trgm
             ON ops_system_logs USING gin (client_request_id gin_trgm_ops)';
  END IF;
END $$;
