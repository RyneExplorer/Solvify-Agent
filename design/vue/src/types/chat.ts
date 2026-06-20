// ── Session ──

export interface ChatSession {
  id: string
  title: string
  model_id: string
  status: string
  created_at: string
  updated_at: string
}

export interface CreateSessionRequest {
  title: string
  model_id: string
}

export interface UpdateSessionRequest {
  title: string
}

// ── Message ──

export interface SourceInfo {
  document_id: string
  knowledge_base_id: string
  title: string
  score: number
  chunks: ChunkSource[]
}

export interface ChunkSource {
  id: string
  quote?: string
  content: string
  score: number
}

export interface ReasoningStep {
  type: string
  content?: string
  detail?: string
  status?: string
}

export interface ChatMessage {
  id: string
  session_id: string
  role: 'user' | 'assistant'
  content: string
  model_id?: string
  search_mode?: string
  knowledge_base_ids?: string[]
  sources?: SourceInfo[]
  reasoning_steps?: ReasoningStep[]
  created_at: string
}

// ── Send Message ──

export interface SendMessageRequest {
  content: string
  knowledge_base_ids: string[]
  search_mode: string
  model_id: string
  model_type: string
}

// ── SSE Stream Event ──

export interface StreamEvent {
  type: string
  title?: string
  detail?: string
  status?: string
  content?: string
  sources?: SourceInfo[]
  citation_id?: string
  chunk_id?: string
  file_name?: string
  citation_content?: string
  message_id?: string
  done?: boolean
  error?: string
  retryable?: boolean
}

// ── List Responses ──

export interface ListSessionsResponse {
  sessions: ChatSession[]
}

export interface ListMessagesResponse {
  messages: ChatMessage[]
}
