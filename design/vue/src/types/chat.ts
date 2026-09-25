// ── Session ──

export interface PendingCheckpointInfo {
  checkpoint_id: string
  interrupt_id: string
  question?: string
  tool_name?: string
  is_clarify?: boolean
  options?: string[]
  set_at: string
}

export interface ChatSession {
  id: string
  title: string
  model_id: string
  status: string
  pending_checkpoint?: PendingCheckpointInfo | null
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
  /** 用户启用的 MCP 工具配置 ID 列表（可多选，空表示使用全部已启用的） */
  mcp_user_config_ids?: string[]
}

// ── SSE Stream Event ──

export interface ClarifyPayload {
  question: string
  options?: string[]
}

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
  // clarify 事件字段：追问
  clarify?: ClarifyPayload
  // interrupt 事件字段：中断等待用户审批
  checkpoint_id?: string
  interrupt_id?: string
  interrupt_info?: Record<string, unknown>
}

// ── 审批请求状态（前端本地） ──

export interface PendingApproval {
  checkpoint_id: string
  interrupt_id: string
  title: string
  detail: string
  tool_name?: string
  target_ref?: string
  reason?: string
  options?: string[]
  is_clarify?: boolean
}

// ── List Responses ──

export interface ListSessionsResponse {
  sessions: ChatSession[]
}

export interface ListMessagesResponse {
  messages: ChatMessage[]
}

// ── Message Feedback ──

export type FeedbackRating = 1 | -1

export interface FeedbackRequest {
  rating: FeedbackRating
  reasons?: string[]
  comment?: string
  is_quick_reply?: boolean
}

export interface FeedbackInfo {
  id: string
  message_id: string
  session_id: string
  user_id: string
  rating: FeedbackRating
  reasons: string[]
  comment?: string
  is_quick_reply: boolean
  created_at: string
}

// ── Admin Session ──

export interface AdminSession {
  id: string
  user_id: string
  username: string
  title: string
  model_id: string
  status: string
  message_count: number
  created_at: string
  updated_at: string
}
