-- 056_add_ops_system_logs_extra_search_index.sql
-- 为 ops_system_logs.extra 的全文检索提供索引，加速查询关键字过滤。

CREATE INDEX IF NOT EXISTS idx_ops_system_logs_extra_search
  ON ops_system_logs USING GIN (to_tsvector('simple', COALESCE(extra::text, '')));
