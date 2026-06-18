CREATE
EXTENSION IF NOT EXISTS pgcrypto;
CREATE
EXTENSION IF NOT EXISTS vector;

-- 用户表作为当前阶段的数据隔离边界
CREATE TABLE IF NOT EXISTS users
(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(), -- 用户 ID
    username VARCHAR(64)  NOT NULL,                -- 用户名
    email    VARCHAR(128) NOT NULL,                -- 邮箱
    password TEXT         NOT NULL DEFAULT '',     -- 密码哈希
    status   INT          NOT NULL DEFAULT 1,      -- 用户状态，1 正常，2 禁用，3 注销，4 待验证
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- 创建时间
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- 更新时间

    CONSTRAINT users_username_unique UNIQUE (username),
    CONSTRAINT users_email_unique UNIQUE (email)
);

COMMENT ON TABLE users IS '用户基础表，用于隔离每个用户自己的知识库和文档';
COMMENT ON COLUMN users.id IS '用户 ID';
COMMENT ON COLUMN users.username IS '用户名';
COMMENT ON COLUMN users.email IS '邮箱';
COMMENT ON COLUMN users.password IS '密码哈希';
COMMENT ON COLUMN users.status IS '用户状态，1 正常，2 禁用，3 注销，4 待验证';
COMMENT ON COLUMN users.created_at IS '创建时间';
COMMENT ON COLUMN users.updated_at IS '更新时间';

-- 知识库表保存用户自建、同步和联网搜索知识库
CREATE TABLE IF NOT EXISTS knowledge_bases
(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),         -- 知识库 ID
    user_id UUID NOT NULL,                                 -- 所属用户 ID
    name            VARCHAR(128) NOT NULL,                 -- 知识库名称
    category        VARCHAR(64)  NOT NULL DEFAULT '',      -- 知识库分类
    description     TEXT                  DEFAULT '',      -- 知识库描述
    source_type     VARCHAR(32)  NOT NULL DEFAULT 'local', -- 知识库来源类型，local 自建，sync 同步，web_search 联网搜索
    source_platform VARCHAR(32)  NOT NULL DEFAULT '',      -- 同步来源平台
    document_count  INT          NOT NULL DEFAULT 0,       -- 文档数量
    storage_bytes   BIGINT       NOT NULL DEFAULT 0,       -- 已占用存储字节数
    status          INT          NOT NULL DEFAULT 1,       -- 知识库状态，1 正常，2 已删除
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),         -- 创建时间
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),         -- 更新时间
    deleted_at TIMESTAMPTZ,                                -- 删除时间
    delete_expired_at TIMESTAMPTZ,                         -- 删除保留到期时间

    CONSTRAINT knowledge_bases_user_name_unique UNIQUE (user_id, name)
);

COMMENT ON TABLE knowledge_bases IS '知识库主表，所有知识库按用户隔离';
COMMENT ON COLUMN knowledge_bases.id IS '知识库 ID';
COMMENT ON COLUMN knowledge_bases.user_id IS '所属用户 ID';
COMMENT ON COLUMN knowledge_bases.name IS '知识库名称';
COMMENT ON COLUMN knowledge_bases.category IS '知识库分类';
COMMENT ON COLUMN knowledge_bases.description IS '知识库描述';
COMMENT ON COLUMN knowledge_bases.source_type IS '知识库来源类型，local 自建，sync 同步，web_search 联网搜索';
COMMENT ON COLUMN knowledge_bases.source_platform IS '同步来源平台';
COMMENT ON COLUMN knowledge_bases.document_count IS '文档数量';
COMMENT ON COLUMN knowledge_bases.storage_bytes IS '已占用存储字节数';
COMMENT ON COLUMN knowledge_bases.status IS '知识库状态，1 正常，2 已删除';
COMMENT ON COLUMN knowledge_bases.created_at IS '创建时间';
COMMENT ON COLUMN knowledge_bases.updated_at IS '更新时间';
COMMENT ON COLUMN knowledge_bases.deleted_at IS '删除时间';
COMMENT ON COLUMN knowledge_bases.delete_expired_at IS '删除保留到期时间';

-- 文档表记录上传文件和处理状态
CREATE TABLE IF NOT EXISTS documents
(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),        -- 文档 ID
    user_id UUID NOT NULL,                                -- 所属用户 ID
    knowledge_base_id UUID NOT NULL,                      -- 所属知识库 ID
    title         VARCHAR(255) NOT NULL,                  -- 文档标题
    file_name     VARCHAR(255) NOT NULL,                  -- 原始文件名
    file_type     VARCHAR(32)  NOT NULL DEFAULT '',       -- 文件类型
    file_size     BIGINT       NOT NULL DEFAULT 0,        -- 文件大小字节数
    storage_path  TEXT         NOT NULL DEFAULT '',       -- 文件存储路径
    file_hash     VARCHAR(128) NOT NULL DEFAULT '',       -- 原始文件内容指纹
    source_type   VARCHAR(32)  NOT NULL DEFAULT 'upload', -- 文档来源类型，upload 上传，edit 编辑，sync 同步，web_search 联网搜索
    external_id   VARCHAR(255) NOT NULL DEFAULT '',       -- 外部平台文档 ID
    external_url  TEXT         NOT NULL DEFAULT '',       -- 外部平台文档链接
    source_updated_at TIMESTAMPTZ,                        -- 外部平台更新时间
    status        INT          NOT NULL DEFAULT 1,        -- 文档状态，1 已上传，2 处理中，3 已就绪，4 处理失败，5 已删除
    error_message TEXT         NOT NULL DEFAULT '',       -- 处理失败原因
    ready_at TIMESTAMPTZ,                                 -- 文档就绪时间
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),        -- 创建时间
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),        -- 更新时间
    deleted_at TIMESTAMPTZ,                               -- 删除时间
    delete_expired_at TIMESTAMPTZ                         -- 删除保留到期时间

);

COMMENT ON TABLE documents IS '文档主表，记录知识库下的文件和处理状态';
COMMENT ON COLUMN documents.id IS '文档 ID';
COMMENT ON COLUMN documents.user_id IS '所属用户 ID';
COMMENT ON COLUMN documents.knowledge_base_id IS '所属知识库 ID';
COMMENT ON COLUMN documents.title IS '文档标题';
COMMENT ON COLUMN documents.file_name IS '原始文件名';
COMMENT ON COLUMN documents.file_type IS '文件类型';
COMMENT ON COLUMN documents.file_size IS '文件大小字节数';
COMMENT ON COLUMN documents.storage_path IS '文件存储路径';
COMMENT ON COLUMN documents.file_hash IS '原始文件内容指纹';
COMMENT ON COLUMN documents.source_type IS '文档来源类型，upload 上传，edit 编辑，sync 同步，web_search 联网搜索';
COMMENT ON COLUMN documents.external_id IS '外部平台文档 ID';
COMMENT ON COLUMN documents.external_url IS '外部平台文档链接';
COMMENT ON COLUMN documents.source_updated_at IS '外部平台更新时间';
COMMENT ON COLUMN documents.status IS '文档状态，1 已上传，2 处理中，3 已就绪，4 处理失败，5 已删除';
COMMENT ON COLUMN documents.error_message IS '处理失败原因';
COMMENT ON COLUMN documents.ready_at IS '文档就绪时间';
COMMENT ON COLUMN documents.deleted_at IS '删除时间';
COMMENT ON COLUMN documents.delete_expired_at IS '删除保留到期时间';
COMMENT ON COLUMN documents.created_at IS '创建时间';
COMMENT ON COLUMN documents.updated_at IS '更新时间';

ALTER TABLE documents
    ADD COLUMN IF NOT EXISTS external_id VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS external_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS source_updated_at TIMESTAMPTZ;

-- 文档版本表支持在线编辑和重新向量化
CREATE TABLE IF NOT EXISTS document_versions
(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),   -- 版本 ID
    user_id UUID NOT NULL,                           -- 所属用户 ID
    document_id UUID NOT NULL,                       -- 所属文档 ID
    version_no     INT          NOT NULL DEFAULT 1,  -- 版本号
    content        TEXT         NOT NULL DEFAULT '', -- 版本正文内容
    content_hash   VARCHAR(128) NOT NULL DEFAULT '', -- 版本内容哈希
    change_summary TEXT         NOT NULL DEFAULT '', -- 变更摘要
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),   -- 创建时间

    CONSTRAINT document_versions_document_version_unique UNIQUE (document_id, version_no)
);

COMMENT ON TABLE document_versions IS '文档版本表，用于记录原始解析内容和在线编辑历史';
COMMENT ON COLUMN document_versions.id IS '版本 ID';
COMMENT ON COLUMN document_versions.user_id IS '所属用户 ID';
COMMENT ON COLUMN document_versions.document_id IS '所属文档 ID';
COMMENT ON COLUMN document_versions.version_no IS '版本号';
COMMENT ON COLUMN document_versions.content IS '版本正文内容';
COMMENT ON COLUMN document_versions.content_hash IS '版本内容哈希';
COMMENT ON COLUMN document_versions.change_summary IS '变更摘要';
COMMENT ON COLUMN document_versions.created_at IS '创建时间';

-- 文档分块表保存文本分块和向量数据
CREATE TABLE IF NOT EXISTS document_chunks
(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),    -- 分块 ID
    user_id UUID NOT NULL,                            -- 所属用户 ID
    knowledge_base_id UUID NOT NULL,                  -- 所属知识库 ID
    document_id UUID NOT NULL,                        -- 所属文档 ID
    version_id UUID NOT NULL,                         -- 所属文档版本 ID
    chunk_index     INT          NOT NULL,            -- 分块序号
    section_title   TEXT         NOT NULL DEFAULT '', -- 分块所属章节标题
    content         TEXT         NOT NULL,            -- 分块文本内容
    token_count     INT          NOT NULL DEFAULT 0,  -- 分块 token 数
    page_number     INT,                              -- 来源页码
    embedding_model VARCHAR(128) NOT NULL DEFAULT '', -- 向量模型名称
    embedding vector(1024),                           -- 分块向量数据
    keywords TEXT[] NOT NULL DEFAULT '{}'::text[],    -- 分块关键词
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,      -- 分块扩展元数据
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),    -- 创建时间

    CONSTRAINT document_chunks_version_index_unique UNIQUE (version_id, chunk_index)
);

COMMENT ON TABLE document_chunks IS '文档分块表，保存可检索文本片段和 embedding 向量';
COMMENT ON COLUMN document_chunks.id IS '分块 ID';
COMMENT ON COLUMN document_chunks.user_id IS '所属用户 ID';
COMMENT ON COLUMN document_chunks.knowledge_base_id IS '所属知识库 ID';
COMMENT ON COLUMN document_chunks.document_id IS '所属文档 ID';
COMMENT ON COLUMN document_chunks.version_id IS '所属文档版本 ID';
COMMENT ON COLUMN document_chunks.chunk_index IS '分块序号';
COMMENT ON COLUMN document_chunks.section_title IS '分块所属章节标题';
COMMENT ON COLUMN document_chunks.content IS '分块文本内容';
COMMENT ON COLUMN document_chunks.token_count IS '分块 token 数';
COMMENT ON COLUMN document_chunks.page_number IS '来源页码';
COMMENT ON COLUMN document_chunks.embedding_model IS '向量模型名称';
COMMENT ON COLUMN document_chunks.embedding IS '分块向量数据';
COMMENT ON COLUMN document_chunks.keywords IS '分块关键词';
COMMENT ON COLUMN document_chunks.metadata IS '分块扩展元数据';
COMMENT ON COLUMN document_chunks.created_at IS '创建时间';

-- 文档处理任务表记录解析、分块、向量化和重建索引状态
CREATE TABLE IF NOT EXISTS document_processing_jobs
(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(), -- 任务 ID
    user_id UUID NOT NULL,                         -- 所属用户 ID
    document_id UUID NOT NULL,                     -- 所属文档 ID
    job_type      VARCHAR(32) NOT NULL,            -- 任务类型，parse 解析，chunk 分块，embed 向量化，reindex 重建索引
    status        INT         NOT NULL DEFAULT 1,  -- 任务状态，1 待处理，2 运行中，3 成功，4 失败
    error_message TEXT        NOT NULL DEFAULT '', -- 任务失败原因
    started_at TIMESTAMPTZ,                        -- 开始时间
    finished_at TIMESTAMPTZ,                       -- 完成时间
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- 创建时间
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()  -- 更新时间

);

COMMENT ON TABLE document_processing_jobs IS '文档处理任务表，记录解析、分块、向量化和重建索引状态';
COMMENT ON COLUMN document_processing_jobs.id IS '任务 ID';
COMMENT ON COLUMN document_processing_jobs.user_id IS '所属用户 ID';
COMMENT ON COLUMN document_processing_jobs.document_id IS '所属文档 ID';
COMMENT ON COLUMN document_processing_jobs.job_type IS '任务类型，parse 解析，chunk 分块，embed 向量化，reindex 重建索引';
COMMENT ON COLUMN document_processing_jobs.status IS '任务状态，1 待处理，2 运行中，3 成功，4 失败';
COMMENT ON COLUMN document_processing_jobs.error_message IS '任务失败原因';
COMMENT ON COLUMN document_processing_jobs.started_at IS '开始时间';
COMMENT ON COLUMN document_processing_jobs.finished_at IS '完成时间';
COMMENT ON COLUMN document_processing_jobs.created_at IS '创建时间';
COMMENT ON COLUMN document_processing_jobs.updated_at IS '更新时间';

-- 同步源表记录钉钉等外部平台同步配置
CREATE TABLE IF NOT EXISTS sync_sources
(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),        -- 同步源 ID
    user_id UUID NOT NULL,                                -- 所属用户 ID
    knowledge_base_id UUID NOT NULL,                      -- 绑定知识库 ID
    name          VARCHAR(128) NOT NULL,                  -- 同步源名称
    platform      VARCHAR(32)  NOT NULL,                  -- 同步平台，当前支持 dingtalk
    source_config JSONB        NOT NULL DEFAULT '{}'::jsonb, -- 非敏感同步配置
    status        INT          NOT NULL DEFAULT 1,        -- 同步源状态，1 正常，2 禁用，3 已删除
    last_sync_at TIMESTAMPTZ,                             -- 最近同步时间
    last_error_message TEXT NOT NULL DEFAULT '',          -- 最近同步失败原因
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),        -- 创建时间
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),        -- 更新时间
    deleted_at TIMESTAMPTZ                                -- 删除时间
);

COMMENT ON TABLE sync_sources IS '同步源配置表，记录钉钉等外部平台同步入口';
COMMENT ON COLUMN sync_sources.id IS '同步源 ID';
COMMENT ON COLUMN sync_sources.user_id IS '所属用户 ID';
COMMENT ON COLUMN sync_sources.knowledge_base_id IS '绑定知识库 ID';
COMMENT ON COLUMN sync_sources.name IS '同步源名称';
COMMENT ON COLUMN sync_sources.platform IS '同步平台，当前支持 dingtalk';
COMMENT ON COLUMN sync_sources.source_config IS '非敏感同步配置';
COMMENT ON COLUMN sync_sources.status IS '同步源状态，1 正常，2 禁用，3 已删除';
COMMENT ON COLUMN sync_sources.last_sync_at IS '最近同步时间';
COMMENT ON COLUMN sync_sources.last_error_message IS '最近同步失败原因';
COMMENT ON COLUMN sync_sources.created_at IS '创建时间';
COMMENT ON COLUMN sync_sources.updated_at IS '更新时间';
COMMENT ON COLUMN sync_sources.deleted_at IS '删除时间';

-- 同步任务表记录每次手动触发同步的执行状态
CREATE TABLE IF NOT EXISTS sync_jobs
(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(), -- 同步任务 ID
    user_id UUID NOT NULL,                         -- 所属用户 ID
    sync_source_id UUID NOT NULL,                  -- 同步源 ID
    knowledge_base_id UUID NOT NULL,               -- 绑定知识库 ID
    job_type      VARCHAR(32) NOT NULL,            -- 任务类型，manual 手动同步
    status        INT         NOT NULL DEFAULT 1,  -- 任务状态，1 待同步，2 同步中，3 成功，4 失败
    total_count   INT         NOT NULL DEFAULT 0,  -- 同步总数
    success_count INT         NOT NULL DEFAULT 0,  -- 同步成功数
    failed_count  INT         NOT NULL DEFAULT 0,  -- 同步失败数
    error_message TEXT        NOT NULL DEFAULT '', -- 任务失败原因
    started_at TIMESTAMPTZ,                        -- 开始时间
    finished_at TIMESTAMPTZ,                       -- 完成时间
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(), -- 创建时间
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()  -- 更新时间
);

COMMENT ON TABLE sync_jobs IS '同步任务表，记录外部平台同步执行状态';
COMMENT ON COLUMN sync_jobs.id IS '同步任务 ID';
COMMENT ON COLUMN sync_jobs.user_id IS '所属用户 ID';
COMMENT ON COLUMN sync_jobs.sync_source_id IS '同步源 ID';
COMMENT ON COLUMN sync_jobs.knowledge_base_id IS '绑定知识库 ID';
COMMENT ON COLUMN sync_jobs.job_type IS '任务类型，manual 手动同步';
COMMENT ON COLUMN sync_jobs.status IS '任务状态，1 待同步，2 同步中，3 成功，4 失败';
COMMENT ON COLUMN sync_jobs.total_count IS '同步总数';
COMMENT ON COLUMN sync_jobs.success_count IS '同步成功数';
COMMENT ON COLUMN sync_jobs.failed_count IS '同步失败数';
COMMENT ON COLUMN sync_jobs.error_message IS '任务失败原因';
COMMENT ON COLUMN sync_jobs.started_at IS '开始时间';
COMMENT ON COLUMN sync_jobs.finished_at IS '完成时间';
COMMENT ON COLUMN sync_jobs.created_at IS '创建时间';
COMMENT ON COLUMN sync_jobs.updated_at IS '更新时间';

-- 存储配额表记录用户存储上限和已用容量
CREATE TABLE IF NOT EXISTS storage_quotas
(
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),          -- 配额记录 ID
    user_id UUID NOT NULL,                                  -- 所属用户 ID
    max_storage_bytes  BIGINT NOT NULL DEFAULT 10737418240, -- 最大可用存储字节数
    used_storage_bytes BIGINT NOT NULL DEFAULT 0,           -- 已用存储字节数
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),          -- 创建时间
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),          -- 更新时间

    CONSTRAINT storage_quotas_user_unique UNIQUE (user_id)
);

COMMENT ON TABLE storage_quotas IS '用户存储配额表，用于限制单用户知识库容量';
COMMENT ON COLUMN storage_quotas.id IS '配额记录 ID';
COMMENT ON COLUMN storage_quotas.user_id IS '所属用户 ID';
COMMENT ON COLUMN storage_quotas.max_storage_bytes IS '最大可用存储字节数';
COMMENT ON COLUMN storage_quotas.used_storage_bytes IS '已用存储字节数';
COMMENT ON COLUMN storage_quotas.created_at IS '创建时间';
COMMENT ON COLUMN storage_quotas.updated_at IS '更新时间';

-- 常用查询索引
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_user_id
    ON knowledge_bases(user_id);

CREATE INDEX IF NOT EXISTS idx_documents_user_kb
    ON documents(user_id, knowledge_base_id);

CREATE INDEX IF NOT EXISTS idx_documents_user_external
    ON documents(user_id, source_type, external_id)
    WHERE external_id <> '';

CREATE INDEX IF NOT EXISTS idx_document_versions_document_id
    ON document_versions(document_id);

CREATE INDEX IF NOT EXISTS idx_document_chunks_user_kb
    ON document_chunks(user_id, knowledge_base_id);

CREATE INDEX IF NOT EXISTS idx_document_chunks_document_id
    ON document_chunks(document_id);

CREATE INDEX IF NOT EXISTS idx_document_processing_jobs_document_id
    ON document_processing_jobs(document_id);

CREATE INDEX IF NOT EXISTS idx_sync_sources_user_kb
    ON sync_sources(user_id, knowledge_base_id);

CREATE INDEX IF NOT EXISTS idx_sync_jobs_source_id
    ON sync_jobs(sync_source_id);

CREATE INDEX IF NOT EXISTS idx_sync_jobs_user_kb
    ON sync_jobs(user_id, knowledge_base_id);

CREATE INDEX IF NOT EXISTS idx_document_chunks_embedding
    ON document_chunks
    USING ivfflat (embedding vector_cosine_ops)
    WITH (lists = 100)
    WHERE embedding IS NOT NULL;
