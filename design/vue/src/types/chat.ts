// ── Session ──

export interface PendingCheckpointInfo {
  checkpoint_id: string
  interrupt_id: string
  question?: string
  tool_name?: string
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

// ── Traces & Spans (Observability) ──

export type SpanStatus = 'ok' | 'error' | 'canceled' | 'unknown'
export type Component =
  | 'http'
  | 'service_chat'
  | 'service_context'
  | 'service_rag'
  | 'service_agent'
  | 'llm'
  | 'rag_retriever'
  | 'rag_reranker'
  | 'agent_engine'
  | 'tool'
  | 'db'
  | 'other'

export interface SpanEvent {
  name: string
  time: string
  attrs?: Record<string, unknown>
}

export interface ChatSpan {
  trace_id: string
  span_id: string
  parent_id?: string
  name: string
  component: Component | string
  start_at: string
  end_at?: string
  duration_ms?: number
  status: SpanStatus | string
  error?: string
  attrs?: Record<string, unknown>
  events?: SpanEvent[]
  children?: ChatSpan[]
}

export interface TraceSummary {
  id: string
  request_id?: string
  user_id?: string
  session_id?: string
  sample_rate: number
  sampled: boolean
  duration_ms: number
  status: SpanStatus | string
  error?: string
  attrs?: Record<string, unknown>
  created_at: string
  search_mode?: 'quick' | 'deep' | 'unknown' | string
}

export interface ChatTraceDetail extends TraceSummary {
  span_tree?: ChatSpan
}

// ── Metrics Snapshot ──

export interface MetricCounterSample {
  labels?: Record<string, string>
  value: number
}
export interface MetricGaugeSample {
  labels?: Record<string, string>
  value: number
}
export interface HistogramBucket {
  le: number
  count: number
}
export interface MetricHistogramSample {
  labels?: Record<string, string>
  count: number
  sum: number
  buckets: HistogramBucket[]
}

export interface MetricsSnapshot {
  ts: string
  counters: Array<{ name: string; help?: string; samples: MetricCounterSample[] }>
  gauges: Array<{ name: string; help?: string; samples: MetricGaugeSample[] }>
  histograms: Array<{ name: string; help?: string; samples: MetricHistogramSample[] }>
  sampling_rate: number
  label_cardinality_limit: number
  buffer_dropped_total: number
  pii_masked_total: number
  label_cardinality_dropped_total: number
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
