-- 在应用所用 schema 上执行。可重复执行；不猜测、不重写已有能力配置。
BEGIN;
ALTER TABLE models ALTER COLUMN max_context_length DROP DEFAULT;
ALTER TABLE user_model_configs ALTER COLUMN max_context_length DROP DEFAULT;
COMMIT;
