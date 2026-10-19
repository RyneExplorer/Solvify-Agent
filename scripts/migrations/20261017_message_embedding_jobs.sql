BEGIN;
-- 历史消息保持none，不在升级或启动时触发向量化。
ALTER TABLE chat_messages
 ADD COLUMN IF NOT EXISTS embedding_status varchar(16) NOT NULL DEFAULT 'none',
 ADD COLUMN IF NOT EXISTS embedding_attempts integer NOT NULL DEFAULT 0,
 ADD COLUMN IF NOT EXISTS embedding_available_at timestamptz NOT NULL DEFAULT now(),
 ADD COLUMN IF NOT EXISTS embedding_lease_until timestamptz,
 ADD COLUMN IF NOT EXISTS embedding_lease_token uuid,
 ADD COLUMN IF NOT EXISTS embedding_error varchar(40) NOT NULL DEFAULT '';
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='chat_messages'::regclass AND conname='chat_messages_embedding_job_check') THEN
  ALTER TABLE chat_messages ADD CONSTRAINT chat_messages_embedding_job_check CHECK (
   embedding_status IN ('none','queued','running','succeeded','failed') AND embedding_attempts >= 0
   AND ((embedding_status='running' AND embedding_lease_until IS NOT NULL AND embedding_lease_token IS NOT NULL)
    OR (embedding_status<>'running' AND embedding_lease_until IS NULL AND embedding_lease_token IS NULL))
  );
 END IF;
END $$;
CREATE INDEX IF NOT EXISTS idx_message_embedding_queued ON chat_messages(embedding_available_at,id) WHERE embedding_status='queued';
CREATE INDEX IF NOT EXISTS idx_message_embedding_leased ON chat_messages(embedding_lease_until,id) WHERE embedding_status='running';
CREATE TABLE IF NOT EXISTS app_schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());
INSERT INTO app_schema_migrations(version) VALUES ('20261017_message_embedding_jobs') ON CONFLICT DO NOTHING;
COMMIT;
