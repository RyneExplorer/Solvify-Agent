import { request } from './client'
import type { ModelInfo } from '@/types/model'
import type { ToolTypeInfo, ToolProviderInfo } from '@/types/tool'

// ── Admin Models ──

export function adminListModels() {
  return request<{ models: ModelInfo[] }>('/models')
}

export function adminCreateModel(data: {
  provider: string
  model_id: string
  base_url: string
  api_key: string
}) {
  return request<ModelInfo>('/models', { method: 'POST', body: data })
}

export function adminUpdateModel(
  id: string,
  data: Partial<{
    provider: string
    model_id: string
    base_url: string
    api_key: string
    is_enabled: boolean
  }>,
) {
  return request<ModelInfo>(`/models/${id}`, { method: 'PUT', body: data })
}

export function adminDeleteModel(id: string) {
  return request<null>(`/models/${id}`, { method: 'DELETE' })
}

// ── Admin Tools ──

export function adminListToolTypes() {
  return request<{ tool_types: ToolTypeInfo[] }>('/admin/tool-types')
}

export function adminCreateToolType(data: {
  name: string
  tool_key: string
  description: string
  execution_mode: string
}) {
  return request<ToolTypeInfo>('/admin/tool-types', { method: 'POST', body: data })
}

export function adminUpdateToolType(
  id: string,
  data: Partial<{
    name: string
    tool_key: string
    description: string
    execution_mode: string
    is_enabled: boolean
  }>,
) {
  return request<ToolTypeInfo>(`/admin/tool-types/${id}`, { method: 'PUT', body: data })
}

export function adminDeleteToolType(id: string) {
  return request<null>(`/admin/tool-types/${id}`, { method: 'DELETE' })
}

export function adminListToolProviders(toolTypeId: string) {
  return request<{ providers: ToolProviderInfo[] }>(`/admin/tool-types/${toolTypeId}/providers`)
}

export function adminCreateToolProvider(
  toolTypeId: string,
  data: {
    name: string
    provider_key: string
    provider_type: string
    description: string
    config_schema?: Record<string, unknown>
    provider_config?: Record<string, unknown>
    admin_config?: Record<string, unknown>
    rate_limit?: Record<string, unknown>
  },
) {
  return request<ToolProviderInfo>(`/admin/tool-types/${toolTypeId}/providers`, {
    method: 'POST',
    body: { ...data, tool_type_id: toolTypeId },
  })
}

export function adminUpdateToolProvider(
  toolTypeId: string,
  providerId: string,
  data: Partial<{
    name: string
    provider_key: string
    provider_type: string
    description: string
    config_schema: Record<string, unknown>
    provider_config: Record<string, unknown>
    admin_config: Record<string, unknown>
    rate_limit: Record<string, unknown>
    is_enabled: boolean
  }>,
) {
  return request<ToolProviderInfo>(`/admin/tool-types/${toolTypeId}/providers/${providerId}`, {
    method: 'PUT',
    body: data,
  })
}

export function adminDeleteToolProvider(toolTypeId: string, providerId: string) {
  return request<null>(`/admin/tool-types/${toolTypeId}/providers/${providerId}`, {
    method: 'DELETE',
  })
}
