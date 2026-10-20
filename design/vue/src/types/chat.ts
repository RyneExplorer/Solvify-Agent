// ── Session ──

export interface PendingCheckpointInfo {
  checkpoint_id: string
  interrupt_id: string
  question?: string
  tool_name?: string
  is_clarify?: boolean
  options?: string[]
  set_at: string
  execution_plan?: ExecutionPlan
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
  tool_call_id?: string
  tool_name?: string
  task_id?: string
  plan_id?: string
  plan_version?: number
  type: string
  content?: string
  detail?: string
  status?: string
}

export interface TaskProgress {
  id: string
  agent: string
  title: string
  depends_on?: string[]
  status: 'pending' | 'running' | 'success' | 'error' | 'canceled' | 'waiting'
  summary?: string
  added_version?: number
}

export interface ExecutionPlan {
  id: string
  version?: number
  phase?: string
  replan_reason?: string
  round?: number
  strategy: string
  status: string
  tasks: TaskProgress[]
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
  execution_plan?: ExecutionPlan
  execution_error?: string
  execution_retryable?: boolean
  created_at: string
}

// ── Send Message ──

export interface SendMessageRequest {
  content: string
  knowledge_base_ids: string[]
  search_mode: string
  model_id: string
  model_type: string
  /** 用户选中的 MCP 工具配置 ID 列表；空数组=本次不使用任何 MCP 工具。
   *  字段缺省（undefined）才表示「未做选择」，后端沿用历史行为=全部已启用。 */
  mcp_user_config_ids?: string[]
}

// ── SSE Stream Event ──

export interface ClarifyPayload {
  question: string
  options?: string[]
}

export interface StreamEvent {
  tool_call_id?: string
  tool_name?: string
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
  plan_id?: string
  plan_version?: number
  phase?: string
  replan_reason?: string
  round?: number
  task_id?: string
  parent_task_id?: string
  agent_name?: string
  task_status?: string
  task_index?: number
  task_total?: number
  plan?: ExecutionPlan
  task?: TaskProgress
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
