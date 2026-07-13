// ── System Model ──

export interface ModelInfo {
  id: string
  provider: string
  model_id: string
  base_url?: string
  api_key: string
  is_enabled: boolean
}

export interface CreateModelRequest {
  provider: string
  model_id: string
  base_url: string
  api_key: string
}

export interface UpdateModelRequest {
  provider?: string
  model_id?: string
  base_url?: string
  api_key?: string
  is_enabled?: boolean
}

export interface ListModelsResponse {
  models: ModelInfo[]
}

// ── User Model Config ──

export interface UserModelConfigInfo {
  id: string
  display_name: string
  api_format: string
  model_id: string
  base_url: string
  api_key: string
  config?: Record<string, unknown>
  created_at: string
  updated_at: string
}

export interface CreateUserModelConfigRequest {
  api_format: string
  base_url: string
  model_id: string
  api_key: string
  config?: Record<string, unknown>
}

export interface UpdateUserModelConfigRequest {
  api_format?: string
  base_url?: string
  model_id?: string
  api_key?: string
  config?: Record<string, unknown>
}

export interface ListUserModelConfigsResponse {
  models: UserModelConfigInfo[]
}
