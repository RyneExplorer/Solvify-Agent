-- 测试数据种子脚本
-- 使用前请确保已执行 init_knowledge_schema.sql 创建表和扩展

-- 测试用户 ID（替换为实际用户 ID）
-- 如果使用 JWT，可以从 token 中获取真实的 user_id
\set user_id '550e8400-e29b-41d4-a716-446655440000'

-- 1. 创建知识库
INSERT INTO knowledge_bases (id, user_id, name, category, description, source_type, status)
VALUES (
    '00000000-0000-0000-0000-000000000001',
    :'user_id',
    'Solvify 技术文档',
    '技术',
    'Solvify Agent 项目的技术文档知识库',
    'local',
    1
) ON CONFLICT (id) DO NOTHING;

-- 2. 创建文档
INSERT INTO documents (id, user_id, knowledge_base_id, title, file_name, file_type, file_size, status)
VALUES
    ('00000000-0000-0000-0000-000000000011', :'user_id', '00000000-0000-0000-0000-000000000001', 'RAG 技术介绍', 'rag_intro.md', 'md', 2048, 3),
    ('00000000-0000-0000-0000-000000000012', :'user_id', '00000000-0000-0000-0000-000000000001', 'pgvector 使用指南', 'pgvector_guide.md', 'md', 3072, 3),
    ('00000000-0000-0000-0000-000000000013', :'user_id', '00000000-0000-0000-0000-000000000001', 'Go 语言最佳实践', 'go_best_practices.md', 'md', 4096, 3)
ON CONFLICT (id) DO NOTHING;

-- 3. 插入文档分块（含向量）
-- 注意：embedding 是 1024 维随机向量，仅用于功能验证
-- 实际使用时需要通过 Embedding API 生成真实向量

-- RAG 技术介绍 - chunk 1
INSERT INTO document_chunks (id, user_id, knowledge_base_id, document_id, version_id, chunk_index, section_title, content, token_count, embedding_model, embedding, metadata)
VALUES (
    '00000000-0000-0000-0000-000000000021', :'user_id', '00000000-0000-0000-0000-000000000001',
    '00000000-0000-0000-0000-000000000011', '00000000-0000-0000-0000-000000000000',
    0, '什么是 RAG',
    'RAG（Retrieval-Augmented Generation）是一种结合信息检索和文本生成的技术。它通过从外部知识库中检索相关文档，将其作为上下文提供给大语言模型，从而生成更准确、更有依据的回答。RAG 的核心优势在于能够让模型访问最新的、特定领域的知识，而无需重新训练模型。',
    120, 'text-embedding-3-small',
    '[' || array_to_string(array(select random() from generate_series(1, 1024)), ',') || ']',
    '{"source": "rag_intro.md"}'::jsonb
) ON CONFLICT (id) DO NOTHING;

-- RAG 技术介绍 - chunk 2
INSERT INTO document_chunks (id, user_id, knowledge_base_id, document_id, version_id, chunk_index, section_title, content, token_count, embedding_model, embedding, metadata)
VALUES (
    '00000000-0000-0000-0000-000000000022', :'user_id', '00000000-0000-0000-0000-000000000001',
    '00000000-0000-0000-0000-000000000011', '00000000-0000-0000-0000-000000000000',
    1, 'RAG 工作流程',
    'RAG 的工作流程分为三个阶段：首先，用户提出问题；其次，系统将问题转换为向量，在知识库中进行相似度检索，找到最相关的文档片段；最后，将检索到的文档片段与用户问题一起作为 Prompt 发送给 LLM，模型基于这些上下文生成回答。这种架构确保了回答的可追溯性，每个回答都能找到对应的来源。',
    150, 'text-embedding-3-small',
    '[' || array_to_string(array(select random() from generate_series(1, 1024)), ',') || ']',
    '{"source": "rag_intro.md"}'::jsonb
) ON CONFLICT (id) DO NOTHING;

-- pgvector 使用指南 - chunk 1
INSERT INTO document_chunks (id, user_id, knowledge_base_id, document_id, version_id, chunk_index, section_title, content, token_count, embedding_model, embedding, metadata)
VALUES (
    '00000000-0000-0000-0000-000000000023', :'user_id', '00000000-0000-0000-0000-000000000001',
    '00000000-0000-0000-0000-000000000012', '00000000-0000-0000-0000-000000000000',
    0, 'pgvector 简介',
    'pgvector 是 PostgreSQL 的向量数据库扩展，它为 PostgreSQL 添加了向量相似度搜索能力。通过 pgvector，你可以在 PostgreSQL 中存储向量（embedding），并使用余弦相似度（cosine distance）、L2 距离或内积进行高效检索。安装方法：在 PostgreSQL 中执行 CREATE EXTENSION vector; 即可启用。',
    130, 'text-embedding-3-small',
    '[' || array_to_string(array(select random() from generate_series(1, 1024)), ',') || ']',
    '{"source": "pgvector_guide.md"}'::jsonb
) ON CONFLICT (id) DO NOTHING;

-- pgvector 使用指南 - chunk 2
INSERT INTO document_chunks (id, user_id, knowledge_base_id, document_id, version_id, chunk_index, section_title, content, token_count, embedding_model, embedding, metadata)
VALUES (
    '00000000-0000-0000-0000-000000000024', :'user_id', '00000000-0000-0000-0000-000000000001',
    '00000000-0000-0000-0000-000000000012', '00000000-0000-0000-0000-000000000000',
    1, '向量索引',
    'pgvector 支持两种索引类型：IVFFlat 和 HNSW。IVFFlat 适合大数据集，通过将向量空间分割为多个聚类来加速搜索；HNSW 基于分层可导航小世界图，提供更快的查询速度但占用更多内存。创建索引示例：CREATE INDEX ON document_chunks USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);',
    140, 'text-embedding-3-small',
    '[' || array_to_string(array(select random() from generate_series(1, 1024)), ',') || ']',
    '{"source": "pgvector_guide.md"}'::jsonb
) ON CONFLICT (id) DO NOTHING;

-- Go 最佳实践 - chunk 1
INSERT INTO document_chunks (id, user_id, knowledge_base_id, document_id, version_id, chunk_index, section_title, content, token_count, embedding_model, embedding, metadata)
VALUES (
    '00000000-0000-0000-0000-000000000025', :'user_id', '00000000-0000-0000-0000-000000000001',
    '00000000-0000-0000-0000-000000000013', '00000000-0000-0000-0000-000000000000',
    0, '错误处理',
    'Go 语言推荐使用显式错误处理而非异常。通过返回 error 接口，调用方可以决定如何处理错误。最佳实践包括：使用 fmt.Errorf 包装错误以保留上下文、使用 errors.Is 和 errors.As 进行错误判断、避免忽略返回的 error 值。在生产代码中，应该记录错误日志并提供有意义的错误信息给用户。',
    135, 'text-embedding-3-small',
    '[' || array_to_string(array(select random() from generate_series(1, 1024)), ',') || ']',
    '{"source": "go_best_practices.md"}'::jsonb
) ON CONFLICT (id) DO NOTHING;

-- Go 最佳实践 - chunk 2
INSERT INTO document_chunks (id, user_id, knowledge_base_id, document_id, version_id, chunk_index, section_title, content, token_count, embedding_model, embedding, metadata)
VALUES (
    '00000000-0000-0000-0000-000000000026', :'user_id', '00000000-0000-0000-0000-000000000001',
    '00000000-0000-0000-0000-000000000013', '00000000-0000-0000-0000-000000000000',
    1, '并发编程',
    'Go 的并发模型基于 goroutine 和 channel。goroutine 是轻量级线程，由 Go 运行时调度；channel 用于 goroutine 之间的通信。最佳实践包括：使用 sync.WaitGroup 等待一组 goroutine 完成、使用 context 传递取消信号和超时、避免 goroutine 泄漏（确保所有 goroutine 都能退出）、使用 select 处理多个 channel 操作。',
    145, 'text-embedding-3-small',
    '[' || array_to_string(array(select random() from generate_series(1, 1024)), ',') || ']',
    '{"source": "go_best_practices.md"}'::jsonb
) ON CONFLICT (id) DO NOTHING;

-- 验证数据
SELECT 'knowledge_bases' as table_name, count(*) as count FROM knowledge_bases WHERE id = '00000000-0000-0000-0000-000000000001'
UNION ALL
SELECT 'documents', count(*) FROM documents WHERE knowledge_base_id = '00000000-0000-0000-0000-000000000001'
UNION ALL
SELECT 'document_chunks', count(*) FROM document_chunks WHERE knowledge_base_id = '00000000-0000-0000-0000-000000000001';
