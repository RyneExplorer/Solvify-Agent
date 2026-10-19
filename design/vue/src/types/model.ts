// ── 系统模型 ──

// 系统模型信息
export interface ModelInfo {
  capability_status?: 'pending' | 'text_only' | 'ready'
  config?: Record<string, unknown>
  id: string
  name: string
  provider: string
  model_id: string
  base_url?: string
  api_key: string
  is_enabled: boolean
  max_context_length: number
}

// 创建模型请求
export interface CreateModelRequest {
  config?: Record<string, unknown>
  provider: string
  model_id: string
  base_url: string
  api_key: string
  max_context_length: number
}

// 更新模型请求
export interface UpdateModelRequest {
  reconfirm_capabilities?: boolean
  config?: Record<string, unknown>
  provider?: string
  model_id?: string
  base_url?: string
  api_key?: string
  is_enabled?: boolean
  max_context_length?: number
}

// 模型列表响应
export interface ListModelsResponse {
  models: ModelInfo[]
}

// ── 用户模型配置 ──

// 用户模型配置信息
export interface UserModelConfigInfo {
  capability_status?: 'pending' | 'text_only' | 'ready'
  id: string
  display_name: string
  api_format: string
  model_id: string
  base_url: string
  api_key: string
  config?: Record<string, unknown>
  max_context_length: number
  created_at: string
  updated_at: string
}

// 创建用户模型配置请求
export interface CreateUserModelConfigRequest {
  api_format: string
  base_url: string
  model_id: string
  api_key: string
  config?: Record<string, unknown>
  max_context_length: number
}

// 更新用户模型配置请求
export interface UpdateUserModelConfigRequest {
  reconfirm_capabilities?: boolean
  api_format?: string
  base_url?: string
  model_id?: string
  api_key?: string
  config?: Record<string, unknown>
  max_context_length?: number
}

// 用户模型配置列表响应
export interface ListUserModelConfigsResponse {
  models: UserModelConfigInfo[]
}

// ── 模型测试结果 ──

// 模型连通性测试结果
export interface ModelTestResult {
  capability_url?: string
  capability_checked_at?: string
  suggested_config?: Record<string, unknown>
  connection_status?: string
  tool_calling_status?: string
  streaming_status?: string
  capability_source?: string
  detected_max_input_tokens?: number
  detected_max_output_tokens?: number
  success: boolean
  message: string
  error?: string
  response_time_ms: number
  details?: string
  detected_max_context_length?: number
}
