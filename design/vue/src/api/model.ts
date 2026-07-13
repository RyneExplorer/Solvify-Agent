import { request } from './client'
import type {
  ModelInfo,
  CreateModelRequest,
  UpdateModelRequest,
  ListModelsResponse,
  UserModelConfigInfo,
  CreateUserModelConfigRequest,
  UpdateUserModelConfigRequest,
  ListUserModelConfigsResponse,
} from '@/types/model'

// ── System Models ──

export function listModels() {
  return request<ListModelsResponse>('/models')
}

export function getModel(id: string) {
  return request<ModelInfo>(`/models/${id}`)
}

export function createModel(data: CreateModelRequest) {
  return request<ModelInfo>('/models', { method: 'POST', body: data })
}

export function updateModel(id: string, data: UpdateModelRequest) {
  return request<ModelInfo>(`/models/${id}`, { method: 'PUT', body: data })
}

export function deleteModel(id: string) {
  return request<null>(`/models/${id}`, { method: 'DELETE' })
}

// ── User Model Configs ──

export function listUserModelConfigs() {
  return request<ListUserModelConfigsResponse>('/user/model-configs')
}

export function getUserModelConfig(id: string) {
  return request<UserModelConfigInfo>(`/user/model-configs/${id}`)
}

export function createUserModelConfig(data: CreateUserModelConfigRequest) {
  return request<UserModelConfigInfo>('/user/model-configs', {
    method: 'POST',
    body: data,
  })
}

export function updateUserModelConfig(
  id: string,
  data: UpdateUserModelConfigRequest,
) {
  return request<UserModelConfigInfo>(`/user/model-configs/${id}`, {
    method: 'PUT',
    body: data,
  })
}

export function deleteUserModelConfig(id: string) {
  return request<null>(`/user/model-configs/${id}`, { method: 'DELETE' })
}

export function testUserModelConfig(data: {
  provider: string
  model_id: string
  base_url: string
  api_key: string
  config?: Record<string, unknown>
}) {
  return request<{
    success: boolean
    message: string
    error?: string
    response_time_ms: number
    details?: string
  }>('/user/model-configs/test', { method: 'POST', body: data })
}
