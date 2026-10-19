-- 在应用使用的 schema 中执行；可重复执行，不改窗口、密钥及协议兼容信息。
-- 未验证的旧模型不新增工具能力声明；执行策略不代表能力已通过实测。
BEGIN;
UPDATE models
SET config = (COALESCE(config, '{}'::jsonb) - 'max_completion_tokens' - 'temperature' - 'timeout' - 'reasoning_effort')
    || '{"execution_policy":"managed_v1"}'::jsonb
WHERE config IS NULL OR jsonb_typeof(config) = 'object';
UPDATE user_model_configs
SET config = (COALESCE(config, '{}'::jsonb) - 'max_completion_tokens' - 'temperature' - 'timeout' - 'reasoning_effort')
    || '{"execution_policy":"managed_v1"}'::jsonb
WHERE config IS NULL OR jsonb_typeof(config) = 'object';
COMMIT;
