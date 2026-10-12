BEGIN;
CREATE TABLE IF NOT EXISTS context_artifacts (
 id varchar(64) PRIMARY KEY, user_id varchar(36) NOT NULL,
 session_id uuid NOT NULL REFERENCES chat_sessions(id) ON DELETE CASCADE,
 run_id varchar(64) NOT NULL, agent_id varchar(100) NOT NULL, task_id varchar(100) NOT NULL,
 tool_call_id text NOT NULL, content text NOT NULL, hash varchar(64) NOT NULL,
 bytes bigint NOT NULL CHECK(bytes >= 0), version integer NOT NULL CHECK(version = 1),
 created_at timestamptz NOT NULL DEFAULT now(), expired_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_context_artifacts_user ON context_artifacts(user_id);
CREATE INDEX IF NOT EXISTS idx_context_artifacts_session ON context_artifacts(session_id);
CREATE INDEX IF NOT EXISTS idx_context_artifacts_run ON context_artifacts(run_id);
CREATE INDEX IF NOT EXISTS idx_context_artifacts_expiry ON context_artifacts(expired_at);
COMMIT;
