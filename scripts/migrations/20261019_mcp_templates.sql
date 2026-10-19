BEGIN;
SELECT pg_advisory_xact_lock(hashtextextended('20261019_mcp_templates', 0));
CREATE TABLE IF NOT EXISTS app_schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS mcp_templates (
 id varchar(36) PRIMARY KEY,
 template_key text NOT NULL UNIQUE,
 tool_prefix text NOT NULL,
 name varchar(100) NOT NULL,
 description text,
 connection jsonb NOT NULL,
 config_schema jsonb,
 mcp_tool_manifest jsonb,
 is_enabled boolean NOT NULL DEFAULT true,
 is_system boolean NOT NULL DEFAULT false,
 display_order integer NOT NULL DEFAULT 0,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK (jsonb_typeof(connection) = 'object')
);
-- 旧表仅为升级历史保留，运行代码不再读写；重复执行不覆盖编辑或重新导入删除项。
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM app_schema_migrations WHERE version='20261019_mcp_templates') THEN
  IF to_regclass('tool_providers') IS NOT NULL THEN
   INSERT INTO mcp_templates(id,template_key,tool_prefix,name,description,connection,config_schema,mcp_tool_manifest,is_enabled,is_system,display_order,created_at,updated_at)
   SELECT p.id,
    CASE WHEN t.tool_key='mcp' THEN p.provider_key ELSE 'legacy/'||p.id END,
    COALESCE(NULLIF(t.tool_key,''),'mcp') || CASE WHEN btrim(p.provider_key)='' THEN '' ELSE '_'||regexp_replace(btrim(p.provider_key), '[^a-zA-Z0-9_-]', '_', 'g') END,
    p.name,p.description,COALESCE(p.provider_config->'mcp',p.provider_config,'{}'::jsonb),
    p.config_schema,p.mcp_tool_manifest,p.is_enabled AND COALESCE(t.is_enabled,false),p.is_system,p.display_order,p.created_at,p.updated_at
   FROM tool_providers p LEFT JOIN tool_types t ON t.id=p.tool_type_id WHERE p.provider_type='mcp';
  END IF;
  -- 若有不属于MCP模板的异常绑定则主动失败，整个升级回滚，禁止静默丢数据。
  IF EXISTS(SELECT 1 FROM mcp_connections c LEFT JOIN mcp_templates t ON t.id=c.template_id WHERE c.template_id IS NOT NULL AND t.id IS NULL) THEN
   RAISE EXCEPTION '存在无法迁移的MCP模板绑定，请核对旧配置';
  END IF;
  -- 不依赖原约束名，兼容旧SQL及GORM建表。
  DECLARE fk record;
  BEGIN
   FOR fk IN SELECT conname FROM pg_constraint WHERE conrelid='mcp_connections'::regclass AND contype='f'
    AND conkey=ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid='mcp_connections'::regclass AND attname='template_id')]::smallint[]
   LOOP EXECUTE format('ALTER TABLE mcp_connections DROP CONSTRAINT %I',fk.conname); END LOOP;
  END;
  ALTER TABLE mcp_connections ADD CONSTRAINT fk_mcp_connections_template FOREIGN KEY(template_id) REFERENCES mcp_templates(id) ON DELETE CASCADE;
  INSERT INTO app_schema_migrations(version) VALUES('20261019_mcp_templates');
 END IF;
END $$;
COMMIT;
