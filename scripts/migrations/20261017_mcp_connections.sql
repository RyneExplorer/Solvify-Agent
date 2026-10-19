BEGIN;
CREATE TABLE IF NOT EXISTS app_schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
-- 已有库升级：重复执行不会覆盖用户后续编辑；旧表保留供有效checkpoint核对和回退排查。
CREATE TABLE IF NOT EXISTS mcp_connections (
 id varchar(36) PRIMARY KEY,
 user_id varchar(36) NOT NULL,
 template_id varchar(36) REFERENCES tool_providers(id) ON DELETE CASCADE,
 display_name varchar(100) NOT NULL,
 connection jsonb NOT NULL DEFAULT '{}'::jsonb,
 config jsonb NOT NULL DEFAULT '{}'::jsonb,
 is_enabled boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK (jsonb_typeof(connection) = 'object' AND jsonb_typeof(config) = 'object'),
 CHECK (template_id IS NULL OR connection = '{}'::jsonb)
);
CREATE INDEX IF NOT EXISTS idx_mcp_connections_user_id ON mcp_connections(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mcp_connections_user_template ON mcp_connections(user_id,template_id) WHERE template_id IS NOT NULL;
INSERT INTO mcp_connections (id,user_id,template_id,display_name,connection,config,is_enabled,created_at,updated_at)
 SELECT c.id,c.user_id,c.provider_id,COALESCE(NULLIF(c.display_name,''),p.name),'{}'::jsonb,
 CASE WHEN jsonb_typeof(c.config)='object' THEN c.config ELSE '{}'::jsonb END,c.is_enabled,c.created_at,c.updated_at
 FROM user_tool_configs c JOIN tool_providers p ON p.id=c.provider_id
 WHERE p.provider_type='mcp' AND NOT EXISTS (SELECT 1 FROM app_schema_migrations WHERE version='20261017_mcp_connections')
 ON CONFLICT (id) DO NOTHING;

INSERT INTO app_schema_migrations(version) VALUES ('20261017_mcp_connections') ON CONFLICT DO NOTHING;
COMMIT;
