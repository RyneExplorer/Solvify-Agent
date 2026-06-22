export interface KnowledgeBase {
  id: string
  name: string
  category: string
  description: string
  source_type: string
  source_platform: string
  document_count: number
  storage_bytes: number
  status: number
  created_at: string
  updated_at: string
}

export interface KnowledgeBaseStats {
  knowledge_base_id: string
  document_count: number
  storage_bytes: number
  retrievable_chunk_count: number
}

export interface CreateKnowledgeBaseRequest {
  name: string
  category?: string
  description?: string
}

export interface UpdateKnowledgeBaseRequest {
  name: string
  category?: string
  description?: string
}
