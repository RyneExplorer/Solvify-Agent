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
  ModelTestResult,
} from '@/types/model'

// ── 系统模型 ──

// 获取模型列表
export function listModels() {
  return request<ListModelsResponse>('/models')
}

// 获取单个模型详情
export function getModel(id: string) {
  return request<ModelInfo>(`/models/${id}`)
}

// 创建模型
export function createModel(data: CreateModelRequest) {
  return request<ModelInfo>('/models', { method: 'POST', body: data })
}

// 更新模型
export function updateModel(id: string, data: UpdateModelRequest) {
  return request<ModelInfo>(`/models/${id}`, { method: 'PUT', body: data })
}

// 删除模型
export function deleteModel(id: string) {
  return request<null>(`/models/${id}`, { method: 'DELETE' })
}

// ── 用户模型配置 ──

// 获取当前用户的模型配置列表
export function listUserModelConfigs() {
  return request<ListUserModelConfigsResponse>('/user/model-configs')
}

// 获取用户模型配置详情
export function getUserModelConfig(id: string) {
  return request<UserModelConfigInfo>(`/user/model-configs/${id}`)
}

// 创建用户模型配置
export function createUserModelConfig(data: CreateUserModelConfigRequest) {
  return request<UserModelConfigInfo>('/user/model-configs', {
    method: 'POST',
    body: data,
  })
}

// 更新用户模型配置
export function updateUserModelConfig(
  id: string,
  data: UpdateUserModelConfigRequest,
) {
  return request<UserModelConfigInfo>(`/user/model-configs/${id}`, {
    method: 'PUT',
    body: data,
  })
}

// 删除用户模型配置
export function deleteUserModelConfig(id: string) {
  return request<null>(`/user/model-configs/${id}`, { method: 'DELETE' })
}

// 测试用户模型配置连通性
export function testUserModelConfig(data: {
  provider: string
  model_id: string
  base_url: string
  api_key: string
  config?: Record<string, unknown>
}) {
  return request<ModelTestResult>('/user/model-configs/test', { method: 'POST', body: data })
}
