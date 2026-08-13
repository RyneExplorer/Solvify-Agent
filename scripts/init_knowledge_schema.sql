-- Solvify-Agent 生产数据库初始化基线
-- 仅在空 PostgreSQL 数据卷首次启动时执行

\set ON_ERROR_STOP on

BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS vector;

-- ----------------------------
-- Table structure for agent_task_steps
-- ----------------------------
CREATE TABLE "public"."agent_task_steps" (
  "id" varchar(64) COLLATE "pg_catalog"."default" NOT NULL,
  "task_id" varchar(64) COLLATE "pg_catalog"."default" NOT NULL,
  "step_index" int4 NOT NULL DEFAULT 0,
  "started_at" timestamptz(6) NOT NULL,
  "ended_at" timestamptz(6),
  "thinking_summary" text COLLATE "pg_catalog"."default",
  "tool_name" varchar(128) COLLATE "pg_catalog"."default",
  "tool_input_masked" text COLLATE "pg_catalog"."default",
  "tool_result_summary" text COLLATE "pg_catalog"."default",
  "tool_status" varchar(32) COLLATE "pg_catalog"."default",
  "tool_error" text COLLATE "pg_catalog"."default",
  "latency_ms" int8 NOT NULL DEFAULT 0,
  "tokens_delta" int4 NOT NULL DEFAULT 0,
  "attrs" jsonb
)
;

-- ----------------------------
-- Table structure for agent_tasks
-- ----------------------------
CREATE TABLE "public"."agent_tasks" (
  "id" varchar(64) COLLATE "pg_catalog"."default" NOT NULL,
  "trace_id" varchar(128) COLLATE "pg_catalog"."default",
  "session_id" varchar(64) COLLATE "pg_catalog"."default",
  "user_id" varchar(64) COLLATE "pg_catalog"."default",
  "model_id" varchar(128) COLLATE "pg_catalog"."default",
  "search_mode" varchar(32) COLLATE "pg_catalog"."default",
  "started_at" timestamptz(6) NOT NULL,
  "ended_at" timestamptz(6),
  "total_steps" int4 NOT NULL DEFAULT 0,
  "tool_calls" int4 NOT NULL DEFAULT 0,
  "status" varchar(32) COLLATE "pg_catalog"."default",
  "abort_reason" varchar(128) COLLATE "pg_catalog"."default",
  "tokens_prompt" int4 NOT NULL DEFAULT 0,
  "tokens_completion" int4 NOT NULL DEFAULT 0,
  "total_cost" float8 NOT NULL DEFAULT 0,
  "error_summary" text COLLATE "pg_catalog"."default",
  "feedback_rating" int4
)
;

-- ----------------------------
-- Table structure for chat_messages
-- ----------------------------
CREATE TABLE "public"."chat_messages" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "session_id" uuid NOT NULL,
  "role" varchar(20) COLLATE "pg_catalog"."default" NOT NULL,
  "content" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::text,
  "model_id" varchar(36) COLLATE "pg_catalog"."default",
  "search_mode" varchar(20) COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'quick'::character varying,
  "knowledge_base_ids" jsonb NOT NULL DEFAULT '[]'::jsonb,
  "sources" jsonb,
  "metadata" jsonb NOT NULL DEFAULT '{}'::jsonb,
  "created_at" timestamptz(6)
)
;

-- ----------------------------
-- Table structure for chat_sessions
-- ----------------------------
CREATE TABLE "public"."chat_sessions" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NOT NULL,
  "title" varchar(200) COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::character varying,
  "model_id" varchar(36) COLLATE "pg_catalog"."default" NOT NULL,
  "status" varchar(20) COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'active'::character varying,
  "message_count" int8 NOT NULL DEFAULT 0,
  "created_at" timestamptz(6),
  "updated_at" timestamptz(6)
)
;

-- ----------------------------
-- Table structure for chat_summaries
-- ----------------------------
CREATE TABLE "public"."chat_summaries" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "session_id" uuid NOT NULL,
  "summary" text COLLATE "pg_catalog"."default" NOT NULL,
  "covered_count" int4 NOT NULL DEFAULT 0,
  "last_message_id" uuid,
  "created_at" timestamptz(6) NOT NULL DEFAULT now(),
  "updated_at" timestamptz(6) NOT NULL DEFAULT now()
)
;
COMMENT ON COLUMN "public"."chat_summaries"."session_id" IS '会话 ID';
COMMENT ON COLUMN "public"."chat_summaries"."summary" IS '摘要文本';
COMMENT ON COLUMN "public"."chat_summaries"."covered_count" IS '覆盖消息数';
COMMENT ON COLUMN "public"."chat_summaries"."last_message_id" IS '摘要覆盖到的最后一条消息 ID';
COMMENT ON TABLE "public"."chat_summaries" IS '会话摘要表，保存单一会话的摘要以替代早期原始消息';

-- ----------------------------
-- Table structure for chat_traces
-- ----------------------------
CREATE TABLE "public"."chat_traces" (
  "id" varchar(128) COLLATE "pg_catalog"."default" NOT NULL,
  "request_id" varchar(128) COLLATE "pg_catalog"."default",
  "user_id" varchar(64) COLLATE "pg_catalog"."default",
  "session_id" varchar(64) COLLATE "pg_catalog"."default",
  "sample_rate" float8 NOT NULL DEFAULT 0,
  "sampled" bool NOT NULL DEFAULT false,
  "duration_ms" int8 NOT NULL DEFAULT 0,
  "status" varchar(32) COLLATE "pg_catalog"."default",
  "error" text COLLATE "pg_catalog"."default",
  "attrs" jsonb,
  "span_tree" jsonb,
  "created_at" timestamptz(6) NOT NULL DEFAULT now()
)
;

-- ----------------------------
-- Table structure for dingtalk_user_bindings
-- ----------------------------
CREATE TABLE "public"."dingtalk_user_bindings" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NOT NULL,
  "ding_open_id" varchar(128) COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::character varying,
  "ding_union_id" varchar(128) COLLATE "pg_catalog"."default" NOT NULL,
  "corp_id" varchar(128) COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::character varying,
  "nickname" varchar(128) COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::character varying,
  "avatar" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::text,
  "created_at" timestamptz(6) NOT NULL DEFAULT now(),
  "updated_at" timestamptz(6) NOT NULL DEFAULT now()
)
;
COMMENT ON COLUMN "public"."dingtalk_user_bindings"."id" IS '绑定 ID';
COMMENT ON COLUMN "public"."dingtalk_user_bindings"."user_id" IS '系统用户 ID';
COMMENT ON COLUMN "public"."dingtalk_user_bindings"."ding_open_id" IS '钉钉 openid';
COMMENT ON COLUMN "public"."dingtalk_user_bindings"."ding_union_id" IS '钉钉 unionId';
COMMENT ON COLUMN "public"."dingtalk_user_bindings"."corp_id" IS '钉钉企业 ID';
COMMENT ON COLUMN "public"."dingtalk_user_bindings"."nickname" IS '钉钉用户昵称';
COMMENT ON COLUMN "public"."dingtalk_user_bindings"."avatar" IS '钉钉用户头像';
COMMENT ON COLUMN "public"."dingtalk_user_bindings"."created_at" IS '创建时间';
COMMENT ON COLUMN "public"."dingtalk_user_bindings"."updated_at" IS '更新时间';
COMMENT ON TABLE "public"."dingtalk_user_bindings" IS '钉钉用户绑定表，用于保存系统用户与钉钉身份的对应关系';

-- ----------------------------
-- Table structure for document_chunks
-- ----------------------------
CREATE TABLE "public"."document_chunks" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NOT NULL,
  "knowledge_base_id" uuid NOT NULL,
  "document_id" uuid NOT NULL,
  "version_id" uuid NOT NULL,
  "chunk_index" int8 NOT NULL,
  "section_title" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::text,
  "content" text COLLATE "pg_catalog"."default" NOT NULL,
  "token_count" int8 NOT NULL DEFAULT 0,
  "page_number" int8,
  "embedding_model" varchar(128) COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::character varying,
  "embedding" "public"."vector"(1024),
  "metadata" jsonb NOT NULL DEFAULT '{}'::jsonb,
  "created_at" timestamptz(6),
  "keywords" text[] COLLATE "pg_catalog"."default" DEFAULT '{}'::text[]
)
;
COMMENT ON COLUMN "public"."document_chunks"."id" IS '分块 ID';
COMMENT ON COLUMN "public"."document_chunks"."user_id" IS '所属用户 ID';
COMMENT ON COLUMN "public"."document_chunks"."knowledge_base_id" IS '所属知识库 ID';
COMMENT ON COLUMN "public"."document_chunks"."document_id" IS '所属文档 ID';
COMMENT ON COLUMN "public"."document_chunks"."version_id" IS '所属文档版本 ID';
COMMENT ON COLUMN "public"."document_chunks"."chunk_index" IS '分块序号';
COMMENT ON COLUMN "public"."document_chunks"."section_title" IS '分块所属章节标题';
COMMENT ON COLUMN "public"."document_chunks"."content" IS '分块文本内容';
COMMENT ON COLUMN "public"."document_chunks"."token_count" IS '分块 token 数';
COMMENT ON COLUMN "public"."document_chunks"."page_number" IS '来源页码';
COMMENT ON COLUMN "public"."document_chunks"."embedding_model" IS '向量模型名称';
COMMENT ON COLUMN "public"."document_chunks"."embedding" IS '分块向量数据';
COMMENT ON COLUMN "public"."document_chunks"."metadata" IS '分块扩展元数据';
COMMENT ON COLUMN "public"."document_chunks"."created_at" IS '创建时间';
COMMENT ON COLUMN "public"."document_chunks"."keywords" IS '分块关键词';
COMMENT ON TABLE "public"."document_chunks" IS '文档分块表，保存可检索文本片段和 embedding 向量';

-- ----------------------------
-- Table structure for document_processing_jobs
-- ----------------------------
CREATE TABLE "public"."document_processing_jobs" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NOT NULL,
  "document_id" uuid NOT NULL,
  "job_type" varchar(32) COLLATE "pg_catalog"."default" NOT NULL,
  "status" int8 NOT NULL DEFAULT 1,
  "error_message" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::text,
  "started_at" timestamptz(6),
  "finished_at" timestamptz(6),
  "created_at" timestamptz(6),
  "updated_at" timestamptz(6)
)
;
COMMENT ON COLUMN "public"."document_processing_jobs"."id" IS '任务 ID';
COMMENT ON COLUMN "public"."document_processing_jobs"."user_id" IS '所属用户 ID';
COMMENT ON COLUMN "public"."document_processing_jobs"."document_id" IS '所属文档 ID';
COMMENT ON COLUMN "public"."document_processing_jobs"."job_type" IS '任务类型，parse 解析，chunk 分块，embed 向量化，reindex 重建索引';
COMMENT ON COLUMN "public"."document_processing_jobs"."status" IS '任务状态，1 待处理，2 运行中，3 成功，4 失败';
COMMENT ON COLUMN "public"."document_processing_jobs"."error_message" IS '任务失败原因';
COMMENT ON COLUMN "public"."document_processing_jobs"."started_at" IS '开始时间';
COMMENT ON COLUMN "public"."document_processing_jobs"."finished_at" IS '完成时间';
COMMENT ON COLUMN "public"."document_processing_jobs"."created_at" IS '创建时间';
COMMENT ON COLUMN "public"."document_processing_jobs"."updated_at" IS '更新时间';
COMMENT ON TABLE "public"."document_processing_jobs" IS '文档处理任务表，记录解析、分块、向量化和重建索引状态';

-- ----------------------------
-- Table structure for document_versions
-- ----------------------------
CREATE TABLE "public"."document_versions" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NOT NULL,
  "document_id" uuid NOT NULL,
  "version_no" int8 NOT NULL DEFAULT 1,
  "content" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::text,
  "content_hash" varchar(128) COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::character varying,
  "change_summary" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::text,
  "created_at" timestamptz(6)
)
;
COMMENT ON COLUMN "public"."document_versions"."id" IS '版本 ID';
COMMENT ON COLUMN "public"."document_versions"."user_id" IS '所属用户 ID';
COMMENT ON COLUMN "public"."document_versions"."document_id" IS '所属文档 ID';
COMMENT ON COLUMN "public"."document_versions"."version_no" IS '版本号';
COMMENT ON COLUMN "public"."document_versions"."content" IS '版本正文内容';
COMMENT ON COLUMN "public"."document_versions"."content_hash" IS '版本内容哈希';
COMMENT ON COLUMN "public"."document_versions"."change_summary" IS '变更摘要';
COMMENT ON COLUMN "public"."document_versions"."created_at" IS '创建时间';
COMMENT ON TABLE "public"."document_versions" IS '文档版本表，用于记录原始解析内容和在线编辑历史';

-- ----------------------------
-- Table structure for documents
-- ----------------------------
CREATE TABLE "public"."documents" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NOT NULL,
  "knowledge_base_id" uuid NOT NULL,
  "title" varchar(255) COLLATE "pg_catalog"."default" NOT NULL,
  "file_name" varchar(255) COLLATE "pg_catalog"."default" NOT NULL,
  "file_type" varchar(32) COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::character varying,
  "file_size" int8 NOT NULL DEFAULT 0,
  "storage_path" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::text,
  "file_hash" varchar(128) COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::character varying,
  "source_type" varchar(32) COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'upload'::character varying,
  "status" int8 NOT NULL DEFAULT 1,
  "error_message" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::text,
  "ready_at" timestamptz(6),
  "created_at" timestamptz(6),
  "updated_at" timestamptz(6),
  "deleted_at" timestamptz(6),
  "delete_expired_at" timestamptz(6),
  "external_id" varchar(255) COLLATE "pg_catalog"."default" DEFAULT ''::character varying,
  "external_url" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::text,
  "source_updated_at" timestamptz(6)
)
;
COMMENT ON COLUMN "public"."documents"."id" IS '文档 ID';
COMMENT ON COLUMN "public"."documents"."user_id" IS '所属用户 ID';
COMMENT ON COLUMN "public"."documents"."knowledge_base_id" IS '所属知识库 ID';
COMMENT ON COLUMN "public"."documents"."title" IS '文档标题';
COMMENT ON COLUMN "public"."documents"."file_name" IS '原始文件名';
COMMENT ON COLUMN "public"."documents"."file_type" IS '文件类型';
COMMENT ON COLUMN "public"."documents"."file_size" IS '文件大小字节数';
COMMENT ON COLUMN "public"."documents"."storage_path" IS '文件存储路径';
COMMENT ON COLUMN "public"."documents"."file_hash" IS '原始文件内容指纹';
COMMENT ON COLUMN "public"."documents"."source_type" IS '文档来源类型，upload 上传，edit 编辑，sync 同步，web_search 联网搜索';
COMMENT ON COLUMN "public"."documents"."status" IS '文档状态，1 已上传，2 处理中，3 已就绪，4 处理失败，5 已删除';
COMMENT ON COLUMN "public"."documents"."error_message" IS '处理失败原因';
COMMENT ON COLUMN "public"."documents"."ready_at" IS '文档就绪时间';
COMMENT ON COLUMN "public"."documents"."created_at" IS '创建时间';
COMMENT ON COLUMN "public"."documents"."updated_at" IS '更新时间';
COMMENT ON COLUMN "public"."documents"."deleted_at" IS '删除时间';
COMMENT ON COLUMN "public"."documents"."delete_expired_at" IS '删除保留到期时间';
COMMENT ON COLUMN "public"."documents"."external_id" IS '外部平台文档 ID';
COMMENT ON COLUMN "public"."documents"."external_url" IS '外部平台文档链接';
COMMENT ON COLUMN "public"."documents"."source_updated_at" IS '外部平台更新时间';
COMMENT ON TABLE "public"."documents" IS '文档主表，记录知识库下的文件和处理状态';

-- ----------------------------
-- Table structure for knowledge_bases
-- ----------------------------
CREATE TABLE "public"."knowledge_bases" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NOT NULL,
  "name" varchar(128) COLLATE "pg_catalog"."default" NOT NULL,
  "category" varchar(128) COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::character varying,
  "description" text COLLATE "pg_catalog"."default",
  "source_type" varchar(32) COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'local'::character varying,
  "source_platform" varchar(32) COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::character varying,
  "document_count" int8 NOT NULL DEFAULT 0,
  "storage_bytes" int8 NOT NULL DEFAULT 0,
  "created_at" timestamptz(6),
  "updated_at" timestamptz(6),
  "status" int8 NOT NULL DEFAULT 1,
  "deleted_at" timestamptz(6),
  "delete_expired_at" timestamptz(6)
)
;
COMMENT ON COLUMN "public"."knowledge_bases"."id" IS '知识库 ID';
COMMENT ON COLUMN "public"."knowledge_bases"."user_id" IS '所属用户 ID';
COMMENT ON COLUMN "public"."knowledge_bases"."name" IS '知识库名称';
COMMENT ON COLUMN "public"."knowledge_bases"."category" IS '知识库分类';
COMMENT ON COLUMN "public"."knowledge_bases"."description" IS '知识库描述';
COMMENT ON COLUMN "public"."knowledge_bases"."source_type" IS '知识库来源类型，local 自建，sync 同步，web_search 联网搜索';
COMMENT ON COLUMN "public"."knowledge_bases"."source_platform" IS '同步来源平台';
COMMENT ON COLUMN "public"."knowledge_bases"."document_count" IS '文档数量';
COMMENT ON COLUMN "public"."knowledge_bases"."storage_bytes" IS '已占用存储字节数';
COMMENT ON COLUMN "public"."knowledge_bases"."created_at" IS '创建时间';
COMMENT ON COLUMN "public"."knowledge_bases"."updated_at" IS '更新时间';
COMMENT ON COLUMN "public"."knowledge_bases"."status" IS '知识库状态，1 正常，2 已删除';
COMMENT ON COLUMN "public"."knowledge_bases"."deleted_at" IS '删除时间';
COMMENT ON COLUMN "public"."knowledge_bases"."delete_expired_at" IS '删除保留到期时间';
COMMENT ON TABLE "public"."knowledge_bases" IS '知识库主表，所有知识库按用户隔离';

-- ----------------------------
-- Table structure for message_feedback
-- ----------------------------
CREATE TABLE "public"."message_feedback" (
  "id" varchar(64) COLLATE "pg_catalog"."default" NOT NULL,
  "message_id" varchar(64) COLLATE "pg_catalog"."default" NOT NULL,
  "user_id" varchar(64) COLLATE "pg_catalog"."default" NOT NULL,
  "session_id" varchar(64) COLLATE "pg_catalog"."default",
  "rating" int4 NOT NULL DEFAULT 0,
  "reason_tag" varchar(64) COLLATE "pg_catalog"."default",
  "comment" text COLLATE "pg_catalog"."default",
  "trace_id" varchar(128) COLLATE "pg_catalog"."default",
  "created_at" timestamptz(6) NOT NULL DEFAULT now()
)
;

-- ----------------------------
-- Table structure for models
-- ----------------------------
CREATE TABLE "public"."models" (
  "id" varchar(36) COLLATE "pg_catalog"."default" NOT NULL,
  "name" varchar(100) COLLATE "pg_catalog"."default" NOT NULL,
  "provider" varchar(50) COLLATE "pg_catalog"."default" NOT NULL,
  "model_id" varchar(100) COLLATE "pg_catalog"."default" NOT NULL,
  "is_enabled" bool DEFAULT true,
  "config" jsonb,
  "created_at" timestamptz(6),
  "updated_at" timestamptz(6),
  "base_url" varchar(500) COLLATE "pg_catalog"."default",
  "api_key" varchar(500) COLLATE "pg_catalog"."default",
  "max_context_length" int4 NOT NULL DEFAULT 8192
)
;
COMMENT ON COLUMN "public"."models"."max_context_length" IS '模型最大上下文 token 长度，用于计算历史消息和检索预算';
COMMENT ON TABLE "public"."models" IS '系统预置模型配置表';

-- ----------------------------
-- Table structure for role_templates
-- ----------------------------
CREATE TABLE "public"."role_templates" (
  "id" varchar(36) COLLATE "pg_catalog"."default" NOT NULL,
  "builtin_key" varchar(50) COLLATE "pg_catalog"."default",
  "name" varchar(100) COLLATE "pg_catalog"."default" NOT NULL,
  "description" varchar(255) COLLATE "pg_catalog"."default",
  "target_role" int2 NOT NULL DEFAULT 0,
  "department" varchar(100) COLLATE "pg_catalog"."default",
  "is_default" bool NOT NULL DEFAULT false,
  "system_prompt" text COLLATE "pg_catalog"."default" NOT NULL,
  "is_enabled" bool NOT NULL DEFAULT true,
  "display_order" int4 NOT NULL DEFAULT 0,
  "created_at" timestamp(6) NOT NULL DEFAULT now(),
  "updated_at" timestamp(6) NOT NULL DEFAULT now()
)
;
COMMENT ON COLUMN "public"."role_templates"."builtin_key" IS '内置模板唯一标识：default/engineer/hr/finance；自定义为空';
COMMENT ON COLUMN "public"."role_templates"."target_role" IS '适用角色：0通用/1普通用户/2管理员';
COMMENT ON COLUMN "public"."role_templates"."department" IS '适用部门；空=全部';
COMMENT ON COLUMN "public"."role_templates"."is_default" IS '是否为缺省模板；仅 1 条为 true';
COMMENT ON COLUMN "public"."role_templates"."system_prompt" IS '额外追加到默认系统提示词的角色专属内容';
COMMENT ON COLUMN "public"."role_templates"."is_enabled" IS '是否启用';
COMMENT ON COLUMN "public"."role_templates"."display_order" IS '展示顺序';
COMMENT ON TABLE "public"."role_templates" IS '角色模板：按用户角色/部门加载不同的 System Prompt';

-- ----------------------------
-- Table structure for storage_quotas
-- ----------------------------
CREATE TABLE "public"."storage_quotas" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NOT NULL,
  "max_storage_bytes" int8 NOT NULL DEFAULT '10737418240'::bigint,
  "used_storage_bytes" int8 NOT NULL DEFAULT 0,
  "created_at" timestamptz(6),
  "updated_at" timestamptz(6)
)
;
COMMENT ON COLUMN "public"."storage_quotas"."id" IS '配额记录 ID';
COMMENT ON COLUMN "public"."storage_quotas"."user_id" IS '所属用户 ID';
COMMENT ON COLUMN "public"."storage_quotas"."max_storage_bytes" IS '最大可用存储字节数';
COMMENT ON COLUMN "public"."storage_quotas"."used_storage_bytes" IS '已用存储字节数';
COMMENT ON COLUMN "public"."storage_quotas"."created_at" IS '创建时间';
COMMENT ON COLUMN "public"."storage_quotas"."updated_at" IS '更新时间';
COMMENT ON TABLE "public"."storage_quotas" IS '用户存储配额表，用于限制单用户知识库容量';

-- ----------------------------
-- Table structure for sync_items
-- ----------------------------
CREATE TABLE "public"."sync_items" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NOT NULL,
  "sync_source_id" uuid NOT NULL,
  "knowledge_base_id" uuid NOT NULL,
  "external_id" varchar(255) COLLATE "pg_catalog"."default" NOT NULL,
  "parent_external_id" varchar(255) COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::character varying,
  "name" varchar(255) COLLATE "pg_catalog"."default" NOT NULL,
  "item_type" varchar(32) COLLATE "pg_catalog"."default" NOT NULL,
  "category" varchar(64) COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::character varying,
  "extension" varchar(32) COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::character varying,
  "external_url" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::text,
  "file_size" int8 NOT NULL DEFAULT 0,
  "has_children" bool NOT NULL DEFAULT false,
  "source_updated_at" timestamptz(6),
  "local_document_id" uuid,
  "import_status" int4 NOT NULL DEFAULT 1,
  "error_message" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::text,
  "created_at" timestamptz(6) NOT NULL DEFAULT now(),
  "updated_at" timestamptz(6) NOT NULL DEFAULT now()
)
;
COMMENT ON COLUMN "public"."sync_items"."id" IS '同步目录项 ID';
COMMENT ON COLUMN "public"."sync_items"."user_id" IS '所属用户 ID';
COMMENT ON COLUMN "public"."sync_items"."sync_source_id" IS '同步源 ID';
COMMENT ON COLUMN "public"."sync_items"."knowledge_base_id" IS '绑定知识库 ID';
COMMENT ON COLUMN "public"."sync_items"."external_id" IS '外部节点 ID';
COMMENT ON COLUMN "public"."sync_items"."parent_external_id" IS '外部父节点 ID';
COMMENT ON COLUMN "public"."sync_items"."name" IS '节点名称';
COMMENT ON COLUMN "public"."sync_items"."item_type" IS '节点类型，FILE 或 FOLDER';
COMMENT ON COLUMN "public"."sync_items"."category" IS '钉钉节点分类';
COMMENT ON COLUMN "public"."sync_items"."extension" IS '文件扩展名';
COMMENT ON COLUMN "public"."sync_items"."external_url" IS '外部原文链接';
COMMENT ON COLUMN "public"."sync_items"."file_size" IS '文件大小';
COMMENT ON COLUMN "public"."sync_items"."has_children" IS '是否有子节点';
COMMENT ON COLUMN "public"."sync_items"."source_updated_at" IS '外部更新时间';
COMMENT ON COLUMN "public"."sync_items"."local_document_id" IS '已导入本地文档 ID';
COMMENT ON COLUMN "public"."sync_items"."import_status" IS '导入状态，1 未导入，2 导入中，3 已导入，4 导入失败';
COMMENT ON COLUMN "public"."sync_items"."error_message" IS '导入失败原因';
COMMENT ON COLUMN "public"."sync_items"."created_at" IS '创建时间';
COMMENT ON COLUMN "public"."sync_items"."updated_at" IS '更新时间';
COMMENT ON TABLE "public"."sync_items" IS '同步目录项表，记录外部知识库中的目录和文件元数据';

-- ----------------------------
-- Table structure for sync_jobs
-- ----------------------------
CREATE TABLE "public"."sync_jobs" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NOT NULL,
  "sync_source_id" uuid NOT NULL,
  "knowledge_base_id" uuid NOT NULL,
  "job_type" varchar(32) COLLATE "pg_catalog"."default" NOT NULL,
  "status" int8 NOT NULL DEFAULT 1,
  "total_count" int8 NOT NULL DEFAULT 0,
  "success_count" int8 NOT NULL DEFAULT 0,
  "failed_count" int8 NOT NULL DEFAULT 0,
  "error_message" text COLLATE "pg_catalog"."default" NOT NULL DEFAULT ''::text,
  "started_at" timestamptz(6),
  "finished_at" timestamptz(6),
  "created_at" timestamptz(6),
  "updated_at" timestamptz(6)
)
;
COMMENT ON COLUMN "public"."sync_jobs"."id" IS '同步任务 ID';
COMMENT ON COLUMN "public"."sync_jobs"."user_id" IS '所属用户 ID';
COMMENT ON COLUMN "public"."sync_jobs"."sync_source_id" IS '同步源 ID';
COMMENT ON COLUMN "public"."sync_jobs"."knowledge_base_id" IS '绑定知识库 ID';
COMMENT ON COLUMN "public"."sync_jobs"."job_type" IS '任务类型，manual 手动同步';
COMMENT ON COLUMN "public"."sync_jobs"."status" IS '任务状态，1 待同步，2 同步中，3 成功，4 失败';
COMMENT ON COLUMN "public"."sync_jobs"."total_count" IS '同步总数';
COMMENT ON COLUMN "public"."sync_jobs"."success_count" IS '同步成功数';
COMMENT ON COLUMN "public"."sync_jobs"."failed_count" IS '同步失败数';
COMMENT ON COLUMN "public"."sync_jobs"."error_message" IS '任务失败原因';
COMMENT ON COLUMN "public"."sync_jobs"."started_at" IS '开始时间';
COMMENT ON COLUMN "public"."sync_jobs"."finished_at" IS '完成时间';
COMMENT ON COLUMN "public"."sync_jobs"."created_at" IS '创建时间';
COMMENT ON COLUMN "public"."sync_jobs"."updated_at" IS '更新时间';
COMMENT ON TABLE "public"."sync_jobs" IS '同步任务表，记录外部平台同步执行状态';

-- ----------------------------
-- Table structure for sync_sources
-- ----------------------------
CREATE TABLE "public"."sync_sources" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NOT NULL,
  "knowledge_base_id" uuid NOT NULL,
  "name" varchar(128) COLLATE "pg_catalog"."default" NOT NULL,
  "platform" varchar(32) COLLATE "pg_catalog"."default" NOT NULL,
  "source_config" jsonb NOT NULL DEFAULT '{}'::jsonb,
  "status" int8 NOT NULL DEFAULT 1,
  "last_sync_at" timestamptz(6),
  "last_error_message" text COLLATE "pg_catalog"."default" DEFAULT ''::text,
  "created_at" timestamptz(6),
  "updated_at" timestamptz(6),
  "deleted_at" timestamptz(6)
)
;
COMMENT ON COLUMN "public"."sync_sources"."id" IS '同步源 ID';
COMMENT ON COLUMN "public"."sync_sources"."user_id" IS '所属用户 ID';
COMMENT ON COLUMN "public"."sync_sources"."knowledge_base_id" IS '绑定知识库 ID';
COMMENT ON COLUMN "public"."sync_sources"."name" IS '同步源名称';
COMMENT ON COLUMN "public"."sync_sources"."platform" IS '同步平台，当前支持 dingtalk';
COMMENT ON COLUMN "public"."sync_sources"."source_config" IS '非敏感同步配置';
COMMENT ON COLUMN "public"."sync_sources"."status" IS '同步源状态，1 正常，2 禁用，3 已删除';
COMMENT ON COLUMN "public"."sync_sources"."last_sync_at" IS '最近同步时间';
COMMENT ON COLUMN "public"."sync_sources"."last_error_message" IS '最近同步失败原因';
COMMENT ON COLUMN "public"."sync_sources"."created_at" IS '创建时间';
COMMENT ON COLUMN "public"."sync_sources"."updated_at" IS '更新时间';
COMMENT ON COLUMN "public"."sync_sources"."deleted_at" IS '删除时间';
COMMENT ON TABLE "public"."sync_sources" IS '同步源配置表，记录钉钉等外部平台同步入口';

-- ----------------------------
-- Table structure for tool_providers
-- ----------------------------
CREATE TABLE "public"."tool_providers" (
  "id" varchar(36) COLLATE "pg_catalog"."default" NOT NULL,
  "tool_type_id" varchar(36) COLLATE "pg_catalog"."default" NOT NULL,
  "provider_key" varchar(50) COLLATE "pg_catalog"."default" NOT NULL,
  "name" varchar(100) COLLATE "pg_catalog"."default" NOT NULL,
  "description" text COLLATE "pg_catalog"."default",
  "provider_type" varchar(20) COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'http'::character varying,
  "config_schema" jsonb,
  "input_schema" jsonb,
  "provider_config" jsonb,
  "admin_config" jsonb,
  "rate_limit" jsonb,
  "is_enabled" bool DEFAULT true,
  "display_order" int8 DEFAULT 0,
  "created_at" timestamptz(6),
  "updated_at" timestamptz(6)
)
;

-- ----------------------------
-- Table structure for tool_types
-- ----------------------------
CREATE TABLE "public"."tool_types" (
  "id" varchar(36) COLLATE "pg_catalog"."default" NOT NULL,
  "name" varchar(100) COLLATE "pg_catalog"."default" NOT NULL,
  "tool_key" varchar(50) COLLATE "pg_catalog"."default" NOT NULL,
  "description" text COLLATE "pg_catalog"."default",
  "execution_mode" varchar(20) COLLATE "pg_catalog"."default" DEFAULT 'sync'::character varying,
  "input_schema" jsonb,
  "is_enabled" bool DEFAULT true,
  "created_at" timestamptz(6),
  "updated_at" timestamptz(6)
)
;

-- ----------------------------
-- Table structure for user_memories
-- ----------------------------
CREATE TABLE "public"."user_memories" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NOT NULL,
  "memory_type" varchar(30) COLLATE "pg_catalog"."default" NOT NULL,
  "content" text COLLATE "pg_catalog"."default" NOT NULL,
  "source_session" uuid,
  "confidence" float8 NOT NULL DEFAULT 1.0,
  "is_active" bool NOT NULL DEFAULT true,
  "created_at" timestamptz(6) NOT NULL DEFAULT now(),
  "updated_at" timestamptz(6) NOT NULL DEFAULT now()
)
;
COMMENT ON COLUMN "public"."user_memories"."user_id" IS '所属用户 ID';
COMMENT ON COLUMN "public"."user_memories"."memory_type" IS '记忆类型：fact / preference / constraint / decision';
COMMENT ON COLUMN "public"."user_memories"."content" IS '记忆内容';
COMMENT ON COLUMN "public"."user_memories"."source_session" IS '来源会话 ID';
COMMENT ON COLUMN "public"."user_memories"."confidence" IS '置信度 0-1';
COMMENT ON COLUMN "public"."user_memories"."is_active" IS '是否有效';
COMMENT ON TABLE "public"."user_memories" IS '用户长期记忆表，保存跨会话的事实、偏好、约束和决策结论';

-- ----------------------------
-- Table structure for user_model_configs
-- ----------------------------
CREATE TABLE "public"."user_model_configs" (
  "id" varchar(36) COLLATE "pg_catalog"."default" NOT NULL,
  "user_id" varchar(36) COLLATE "pg_catalog"."default" NOT NULL,
  "display_name" varchar(100) COLLATE "pg_catalog"."default" NOT NULL,
  "api_format" varchar(20) COLLATE "pg_catalog"."default" NOT NULL,
  "base_url" varchar(500) COLLATE "pg_catalog"."default" NOT NULL,
  "model_id" varchar(100) COLLATE "pg_catalog"."default" NOT NULL,
  "api_key" varchar(500) COLLATE "pg_catalog"."default",
  "config" jsonb,
  "created_at" timestamptz(6),
  "updated_at" timestamptz(6),
  "max_context_length" int4 NOT NULL DEFAULT 8192
)
;
COMMENT ON COLUMN "public"."user_model_configs"."max_context_length" IS '用户自定义模型的最大上下文 token 长度';
COMMENT ON TABLE "public"."user_model_configs" IS '用户自定义模型配置表';

-- ----------------------------
-- Table structure for user_preferences
-- ----------------------------
CREATE TABLE "public"."user_preferences" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "user_id" uuid NOT NULL,
  "default_model_id" varchar(36) COLLATE "pg_catalog"."default",
  "preferred_kb_ids" jsonb,
  "answer_style" varchar(20) COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'balanced'::character varying,
  "auto_deep_mode" bool NOT NULL DEFAULT false,
  "auto_deep_threshold" int4 NOT NULL DEFAULT 2,
  "use_markdown_table" bool NOT NULL DEFAULT true,
  "citation_style" varchar(20) COLLATE "pg_catalog"."default" NOT NULL DEFAULT 'section_title'::character varying,
  "created_at" timestamp(6) NOT NULL DEFAULT now(),
  "updated_at" timestamp(6) NOT NULL DEFAULT now()
)
;
COMMENT ON COLUMN "public"."user_preferences"."default_model_id" IS '默认使用的模型ID（系统模型表ID，可空）';
COMMENT ON COLUMN "public"."user_preferences"."preferred_kb_ids" IS '常用知识库ID列表，JSON array[string]';
COMMENT ON COLUMN "public"."user_preferences"."answer_style" IS '回答风格：concise(简洁)/balanced(平衡)/detailed(详细)/step_by_step(分步)';
COMMENT ON COLUMN "public"."user_preferences"."auto_deep_mode" IS '是否自动切深度模式（true=复杂问题自动切；false=始终用户手动）';
COMMENT ON COLUMN "public"."user_preferences"."auto_deep_threshold" IS '自动切深度模式的信号阈值 1~5，越大越不容易切';
COMMENT ON COLUMN "public"."user_preferences"."use_markdown_table" IS '回答尽量用表格呈现结构化数据';
COMMENT ON COLUMN "public"."user_preferences"."citation_style" IS '引用格式：none(不标)/section_title(章节标题)/doc_title_only(仅文档名)';
COMMENT ON TABLE "public"."user_preferences" IS '用户偏好：常用模型、常用知识库、回答风格、是否自动深度模式';

-- ----------------------------
-- Table structure for user_tool_configs
-- ----------------------------
CREATE TABLE "public"."user_tool_configs" (
  "id" varchar(36) COLLATE "pg_catalog"."default" NOT NULL,
  "user_id" varchar(36) COLLATE "pg_catalog"."default" NOT NULL,
  "tool_type_id" varchar(36) COLLATE "pg_catalog"."default" NOT NULL,
  "provider_id" varchar(36) COLLATE "pg_catalog"."default" NOT NULL,
  "display_name" varchar(100) COLLATE "pg_catalog"."default",
  "config" jsonb NOT NULL,
  "is_enabled" bool DEFAULT true,
  "created_at" timestamptz(6),
  "updated_at" timestamptz(6)
)
;

-- ----------------------------
-- Table structure for users
-- ----------------------------
CREATE TABLE "public"."users" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "username" varchar(50) COLLATE "pg_catalog"."default" NOT NULL,
  "password" varchar(255) COLLATE "pg_catalog"."default" NOT NULL,
  "email" varchar(100) COLLATE "pg_catalog"."default",
  "avatar" varchar(255) COLLATE "pg_catalog"."default",
  "status" int2 DEFAULT 1,
  "role" int2 DEFAULT 1,
  "last_model" varchar(255) COLLATE "pg_catalog"."default",
  "created_at" timestamptz(6),
  "updated_at" timestamptz(6),
  "department" varchar(100) COLLATE "pg_catalog"."default",
  "position" varchar(100) COLLATE "pg_catalog"."default",
  "expertise" varchar(255) COLLATE "pg_catalog"."default",
  "preferred_language" varchar(20) COLLATE "pg_catalog"."default" DEFAULT 'zh-CN'::character varying,
  "timezone" varchar(50) COLLATE "pg_catalog"."default" DEFAULT 'Asia/Shanghai'::character varying,
  "role_template_id" varchar(36) COLLATE "pg_catalog"."default"
)
;
COMMENT ON COLUMN "public"."users"."id" IS '用户 ID';
COMMENT ON COLUMN "public"."users"."username" IS '用户名';
COMMENT ON COLUMN "public"."users"."password" IS '密码哈希';
COMMENT ON COLUMN "public"."users"."email" IS '邮箱';
COMMENT ON COLUMN "public"."users"."avatar" IS '头像';
COMMENT ON COLUMN "public"."users"."status" IS '用户状态，1 正常，2 禁用，3 注销，4 待验证';
COMMENT ON COLUMN "public"."users"."role" IS '角色:1普通用户, 2管理员';
COMMENT ON COLUMN "public"."users"."last_model" IS '上次使用的模型';
COMMENT ON COLUMN "public"."users"."created_at" IS '创建时间';
COMMENT ON COLUMN "public"."users"."updated_at" IS '更新时间';
COMMENT ON COLUMN "public"."users"."department" IS '部门';
COMMENT ON COLUMN "public"."users"."position" IS '职位';
COMMENT ON COLUMN "public"."users"."expertise" IS '擅长领域/业务方向，逗号分隔';
COMMENT ON COLUMN "public"."users"."preferred_language" IS '偏好回答语言：zh-CN/en-US/ja-JP 等';
COMMENT ON COLUMN "public"."users"."timezone" IS '时区 IANA，如 Asia/Shanghai';
COMMENT ON COLUMN "public"."users"."role_template_id" IS '关联角色模板ID（可为空=默认模板）';
COMMENT ON TABLE "public"."users" IS '用户基础表，用于隔离每个用户自己的知识库和文档';

-- ----------------------------
-- Indexes structure for table agent_task_steps
-- ----------------------------
CREATE INDEX "idx_agent_task_steps_started_at" ON "public"."agent_task_steps" USING btree (
  "started_at" "pg_catalog"."timestamptz_ops" DESC NULLS FIRST
);
CREATE INDEX "idx_agent_task_steps_task_id_step" ON "public"."agent_task_steps" USING btree (
  "task_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST,
  "step_index" "pg_catalog"."int4_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table agent_task_steps
-- ----------------------------
ALTER TABLE "public"."agent_task_steps" ADD CONSTRAINT "agent_task_steps_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table agent_tasks
-- ----------------------------
CREATE INDEX "idx_agent_tasks_session_id" ON "public"."agent_tasks" USING btree (
  "session_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);
CREATE INDEX "idx_agent_tasks_started_at" ON "public"."agent_tasks" USING btree (
  "started_at" "pg_catalog"."timestamptz_ops" DESC NULLS FIRST
);
CREATE INDEX "idx_agent_tasks_trace_id" ON "public"."agent_tasks" USING btree (
  "trace_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);
CREATE INDEX "idx_agent_tasks_user_id" ON "public"."agent_tasks" USING btree (
  "user_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table agent_tasks
-- ----------------------------
ALTER TABLE "public"."agent_tasks" ADD CONSTRAINT "agent_tasks_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table chat_messages
-- ----------------------------
CREATE INDEX "idx_chat_messages_session_id_created_at" ON "public"."chat_messages" USING btree (
  "session_id" "pg_catalog"."uuid_ops" ASC NULLS LAST,
  "created_at" "pg_catalog"."timestamptz_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table chat_messages
-- ----------------------------
ALTER TABLE "public"."chat_messages" ADD CONSTRAINT "chat_messages_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table chat_sessions
-- ----------------------------
CREATE INDEX "idx_chat_sessions_user_id_updated_at" ON "public"."chat_sessions" USING btree (
  "user_id" "pg_catalog"."uuid_ops" ASC NULLS LAST,
  "updated_at" "pg_catalog"."timestamptz_ops" DESC NULLS FIRST
);

-- ----------------------------
-- Primary Key structure for table chat_sessions
-- ----------------------------
ALTER TABLE "public"."chat_sessions" ADD CONSTRAINT "chat_sessions_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table chat_summaries
-- ----------------------------
CREATE INDEX "idx_chat_summaries_session_id" ON "public"."chat_summaries" USING btree (
  "session_id" "pg_catalog"."uuid_ops" ASC NULLS LAST
);

-- ----------------------------
-- Uniques structure for table chat_summaries
-- ----------------------------
ALTER TABLE "public"."chat_summaries" ADD CONSTRAINT "chat_summaries_session_unique" UNIQUE ("session_id");

-- ----------------------------
-- Primary Key structure for table chat_summaries
-- ----------------------------
ALTER TABLE "public"."chat_summaries" ADD CONSTRAINT "chat_summaries_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table chat_traces
-- ----------------------------
CREATE INDEX "idx_chat_traces_created_at" ON "public"."chat_traces" USING btree (
  "created_at" "pg_catalog"."timestamptz_ops" DESC NULLS FIRST
);
CREATE INDEX "idx_chat_traces_request_id" ON "public"."chat_traces" USING btree (
  "request_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);
CREATE INDEX "idx_chat_traces_session_id" ON "public"."chat_traces" USING btree (
  "session_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);
CREATE INDEX "idx_chat_traces_user_id" ON "public"."chat_traces" USING btree (
  "user_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table chat_traces
-- ----------------------------
ALTER TABLE "public"."chat_traces" ADD CONSTRAINT "chat_traces_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table dingtalk_user_bindings
-- ----------------------------
CREATE INDEX "idx_dingtalk_user_bindings_corp_id" ON "public"."dingtalk_user_bindings" USING btree (
  "corp_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);
CREATE INDEX "idx_dingtalk_user_bindings_user_id" ON "public"."dingtalk_user_bindings" USING btree (
  "user_id" "pg_catalog"."uuid_ops" ASC NULLS LAST
);

-- ----------------------------
-- Uniques structure for table dingtalk_user_bindings
-- ----------------------------
ALTER TABLE "public"."dingtalk_user_bindings" ADD CONSTRAINT "dingtalk_user_bindings_user_unique" UNIQUE ("user_id");
ALTER TABLE "public"."dingtalk_user_bindings" ADD CONSTRAINT "dingtalk_user_bindings_union_unique" UNIQUE ("ding_union_id");

-- ----------------------------
-- Primary Key structure for table dingtalk_user_bindings
-- ----------------------------
ALTER TABLE "public"."dingtalk_user_bindings" ADD CONSTRAINT "dingtalk_user_bindings_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table document_chunks
-- ----------------------------
CREATE INDEX "idx_document_chunks_document_id" ON "public"."document_chunks" USING btree (
  "document_id" "pg_catalog"."uuid_ops" ASC NULLS LAST
);
CREATE INDEX "idx_document_chunks_embedding" ON "public"."document_chunks" USING ivfflat (
  "embedding" "public"."vector_cosine_ops"
) WITH (lists = 100) WHERE embedding IS NOT NULL;
CREATE INDEX "idx_document_chunks_keywords_gin" ON "public"."document_chunks" USING gin (
  "keywords" COLLATE "pg_catalog"."default" "pg_catalog"."array_ops"
) WHERE keywords IS NOT NULL;
CREATE INDEX "idx_document_chunks_user_kb" ON "public"."document_chunks" USING btree (
  "user_id" "pg_catalog"."uuid_ops" ASC NULLS LAST,
  "knowledge_base_id" "pg_catalog"."uuid_ops" ASC NULLS LAST
);

-- ----------------------------
-- Uniques structure for table document_chunks
-- ----------------------------
ALTER TABLE "public"."document_chunks" ADD CONSTRAINT "document_chunks_version_index_unique" UNIQUE ("version_id", "chunk_index");

-- ----------------------------
-- Primary Key structure for table document_chunks
-- ----------------------------
ALTER TABLE "public"."document_chunks" ADD CONSTRAINT "document_chunks_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table document_processing_jobs
-- ----------------------------
CREATE INDEX "idx_document_processing_jobs_document_id" ON "public"."document_processing_jobs" USING btree (
  "document_id" "pg_catalog"."uuid_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table document_processing_jobs
-- ----------------------------
ALTER TABLE "public"."document_processing_jobs" ADD CONSTRAINT "document_processing_jobs_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table document_versions
-- ----------------------------
CREATE INDEX "idx_document_versions_document_id" ON "public"."document_versions" USING btree (
  "document_id" "pg_catalog"."uuid_ops" ASC NULLS LAST
);

-- ----------------------------
-- Uniques structure for table document_versions
-- ----------------------------
ALTER TABLE "public"."document_versions" ADD CONSTRAINT "document_versions_document_version_unique" UNIQUE ("document_id", "version_no");

-- ----------------------------
-- Primary Key structure for table document_versions
-- ----------------------------
ALTER TABLE "public"."document_versions" ADD CONSTRAINT "document_versions_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table documents
-- ----------------------------
CREATE INDEX "idx_documents_user_external" ON "public"."documents" USING btree (
  "user_id" "pg_catalog"."uuid_ops" ASC NULLS LAST,
  "source_type" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST,
  "external_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
) WHERE external_id::text <> ''::text;
CREATE INDEX "idx_documents_user_kb" ON "public"."documents" USING btree (
  "user_id" "pg_catalog"."uuid_ops" ASC NULLS LAST,
  "knowledge_base_id" "pg_catalog"."uuid_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table documents
-- ----------------------------
ALTER TABLE "public"."documents" ADD CONSTRAINT "documents_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table knowledge_bases
-- ----------------------------
CREATE INDEX "idx_knowledge_bases_user_id" ON "public"."knowledge_bases" USING btree (
  "user_id" "pg_catalog"."uuid_ops" ASC NULLS LAST
);
CREATE UNIQUE INDEX "knowledge_bases_user_name_normal_unique" ON "public"."knowledge_bases" USING btree (
  "user_id" "pg_catalog"."uuid_ops" ASC NULLS LAST,
  "name" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
) WHERE status = 1;

-- ----------------------------
-- Primary Key structure for table knowledge_bases
-- ----------------------------
ALTER TABLE "public"."knowledge_bases" ADD CONSTRAINT "knowledge_bases_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table message_feedback
-- ----------------------------
CREATE INDEX "idx_message_feedback_message_id" ON "public"."message_feedback" USING btree (
  "message_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);
CREATE INDEX "idx_message_feedback_session_id" ON "public"."message_feedback" USING btree (
  "session_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);
CREATE INDEX "idx_message_feedback_trace_id" ON "public"."message_feedback" USING btree (
  "trace_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);
CREATE INDEX "idx_message_feedback_user_id" ON "public"."message_feedback" USING btree (
  "user_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table message_feedback
-- ----------------------------
ALTER TABLE "public"."message_feedback" ADD CONSTRAINT "message_feedback_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table models
-- ----------------------------
CREATE INDEX "idx_models_model_id" ON "public"."models" USING btree (
  "model_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);
CREATE UNIQUE INDEX "idx_models_model_id_unique" ON "public"."models" USING btree (
  "model_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
) WHERE is_enabled = true;

-- ----------------------------
-- Primary Key structure for table models
-- ----------------------------
ALTER TABLE "public"."models" ADD CONSTRAINT "models_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table role_templates
-- ----------------------------
CREATE UNIQUE INDEX "idx_role_templates_builtin_key" ON "public"."role_templates" USING btree (
  "builtin_key" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
) WHERE builtin_key::text <> ''::text;
CREATE INDEX "idx_role_templates_is_enabled" ON "public"."role_templates" USING btree (
  "is_enabled" "pg_catalog"."bool_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table role_templates
-- ----------------------------
ALTER TABLE "public"."role_templates" ADD CONSTRAINT "role_templates_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table storage_quotas
-- ----------------------------
CREATE UNIQUE INDEX "storage_quotas_user_unique" ON "public"."storage_quotas" USING btree (
  "user_id" "pg_catalog"."uuid_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table storage_quotas
-- ----------------------------
ALTER TABLE "public"."storage_quotas" ADD CONSTRAINT "storage_quotas_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table sync_items
-- ----------------------------
CREATE INDEX "idx_sync_items_source_parent" ON "public"."sync_items" USING btree (
  "sync_source_id" "pg_catalog"."uuid_ops" ASC NULLS LAST,
  "parent_external_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);
CREATE INDEX "idx_sync_items_user_kb" ON "public"."sync_items" USING btree (
  "user_id" "pg_catalog"."uuid_ops" ASC NULLS LAST,
  "knowledge_base_id" "pg_catalog"."uuid_ops" ASC NULLS LAST
);

-- ----------------------------
-- Uniques structure for table sync_items
-- ----------------------------
ALTER TABLE "public"."sync_items" ADD CONSTRAINT "sync_items_user_source_external_unique" UNIQUE ("user_id", "sync_source_id", "external_id");

-- ----------------------------
-- Primary Key structure for table sync_items
-- ----------------------------
ALTER TABLE "public"."sync_items" ADD CONSTRAINT "sync_items_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table sync_jobs
-- ----------------------------
CREATE INDEX "idx_sync_jobs_source_id" ON "public"."sync_jobs" USING btree (
  "sync_source_id" "pg_catalog"."uuid_ops" ASC NULLS LAST
);
CREATE INDEX "idx_sync_jobs_user_kb" ON "public"."sync_jobs" USING btree (
  "user_id" "pg_catalog"."uuid_ops" ASC NULLS LAST,
  "knowledge_base_id" "pg_catalog"."uuid_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table sync_jobs
-- ----------------------------
ALTER TABLE "public"."sync_jobs" ADD CONSTRAINT "sync_jobs_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table sync_sources
-- ----------------------------
CREATE INDEX "idx_sync_sources_user_kb" ON "public"."sync_sources" USING btree (
  "user_id" "pg_catalog"."uuid_ops" ASC NULLS LAST,
  "knowledge_base_id" "pg_catalog"."uuid_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table sync_sources
-- ----------------------------
ALTER TABLE "public"."sync_sources" ADD CONSTRAINT "sync_sources_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table tool_providers
-- ----------------------------
CREATE INDEX "idx_tool_providers_is_enabled" ON "public"."tool_providers" USING btree (
  "is_enabled" "pg_catalog"."bool_ops" ASC NULLS LAST
);
CREATE INDEX "idx_tool_providers_tool_type_id" ON "public"."tool_providers" USING btree (
  "tool_type_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table tool_providers
-- ----------------------------
ALTER TABLE "public"."tool_providers" ADD CONSTRAINT "tool_providers_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table tool_types
-- ----------------------------
CREATE INDEX "idx_tool_types_is_enabled" ON "public"."tool_types" USING btree (
  "is_enabled" "pg_catalog"."bool_ops" ASC NULLS LAST
);
CREATE UNIQUE INDEX "idx_tool_types_tool_key" ON "public"."tool_types" USING btree (
  "tool_key" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table tool_types
-- ----------------------------
ALTER TABLE "public"."tool_types" ADD CONSTRAINT "tool_types_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table user_memories
-- ----------------------------
CREATE INDEX "idx_user_memories_user_active" ON "public"."user_memories" USING btree (
  "user_id" "pg_catalog"."uuid_ops" ASC NULLS LAST,
  "is_active" "pg_catalog"."bool_ops" ASC NULLS LAST,
  "updated_at" "pg_catalog"."timestamptz_ops" DESC NULLS FIRST
);

-- ----------------------------
-- Uniques structure for table user_memories
-- ----------------------------
ALTER TABLE "public"."user_memories" ADD CONSTRAINT "user_memories_user_content_unique" UNIQUE ("user_id", "content");

-- ----------------------------
-- Primary Key structure for table user_memories
-- ----------------------------
ALTER TABLE "public"."user_memories" ADD CONSTRAINT "user_memories_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table user_model_configs
-- ----------------------------
CREATE INDEX "idx_user_model_configs_user_id" ON "public"."user_model_configs" USING btree (
  "user_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);
CREATE INDEX "idx_user_model_configs_user_id_model_id" ON "public"."user_model_configs" USING btree (
  "user_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST,
  "model_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table user_model_configs
-- ----------------------------
ALTER TABLE "public"."user_model_configs" ADD CONSTRAINT "user_model_configs_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table user_preferences
-- ----------------------------
CREATE INDEX "idx_user_preferences_user_id" ON "public"."user_preferences" USING btree (
  "user_id" "pg_catalog"."uuid_ops" ASC NULLS LAST
);

-- ----------------------------
-- Uniques structure for table user_preferences
-- ----------------------------
ALTER TABLE "public"."user_preferences" ADD CONSTRAINT "user_preferences_user_unique" UNIQUE ("user_id");

-- ----------------------------
-- Primary Key structure for table user_preferences
-- ----------------------------
ALTER TABLE "public"."user_preferences" ADD CONSTRAINT "user_preferences_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table user_tool_configs
-- ----------------------------
CREATE INDEX "idx_user_tool_configs_user_id" ON "public"."user_tool_configs" USING btree (
  "user_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table user_tool_configs
-- ----------------------------
ALTER TABLE "public"."user_tool_configs" ADD CONSTRAINT "user_tool_configs_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Indexes structure for table users
-- ----------------------------
CREATE INDEX "idx_users_role_template_id" ON "public"."users" USING btree (
  "role_template_id" COLLATE "pg_catalog"."default" "pg_catalog"."text_ops" ASC NULLS LAST
);

-- ----------------------------
-- Primary Key structure for table users
-- ----------------------------
ALTER TABLE "public"."users" ADD CONSTRAINT "users_pkey" PRIMARY KEY ("id");

-- ----------------------------
-- Foreign Keys structure for table tool_providers
-- ----------------------------
ALTER TABLE "public"."tool_providers" ADD CONSTRAINT "fk_tool_providers_tool_type" FOREIGN KEY ("tool_type_id") REFERENCES "public"."tool_types" ("id") ON DELETE NO ACTION ON UPDATE NO ACTION;

-- ----------------------------
-- Foreign Keys structure for table user_tool_configs
-- ----------------------------
ALTER TABLE "public"."user_tool_configs" ADD CONSTRAINT "fk_user_tool_configs_tool_provider" FOREIGN KEY ("provider_id") REFERENCES "public"."tool_providers" ("id") ON DELETE NO ACTION ON UPDATE NO ACTION;
ALTER TABLE "public"."user_tool_configs" ADD CONSTRAINT "fk_user_tool_configs_tool_type" FOREIGN KEY ("tool_type_id") REFERENCES "public"."tool_types" ("id") ON DELETE NO ACTION ON UPDATE NO ACTION;

COMMIT;
