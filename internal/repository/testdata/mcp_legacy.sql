-- 升级历史fixture，不用于运行时建表。
CREATE TABLE tool_types(id varchar(36) PRIMARY KEY,tool_key text UNIQUE,name text,is_enabled boolean);
CREATE TABLE tool_providers(
 id varchar(36) PRIMARY KEY,tool_type_id varchar(36) REFERENCES tool_types(id),provider_key text,
 name text,description text,provider_type text,provider_config jsonb,config_schema jsonb,mcp_tool_manifest jsonb,
 is_enabled boolean,is_system boolean DEFAULT false,display_order integer DEFAULT 0,
 created_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now()
);
CREATE TABLE user_tool_configs(
 id varchar(36) PRIMARY KEY,user_id varchar(36),tool_type_id varchar(36),provider_id varchar(36) REFERENCES tool_providers(id),
 display_name text,config jsonb,is_enabled boolean,created_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now()
);
