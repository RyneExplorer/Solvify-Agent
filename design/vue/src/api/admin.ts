import { request } from './client'
import type { ModelInfo } from '@/types/model'
import type { ToolTypeInfo, ToolProviderInfo } from '@/types/tool'
import type { AdminUser } from '@/types/auth'
import type { AdminSession } from '@/types/chat'

// ── Admin Users ──

export function adminListUsers(params: {
  page: number
  pageSize: number
  username?: string
  email?: string
  status?: number
  role?: number
}) {
  const query = new URLSearchParams()
  query.set('page', String(params.page))
  query.set('pageSize', String(params.pageSize))
  if (params.username) query.set('username', params.username)
  if (params.email) query.set('email', params.email)
  if (params.status !== undefined) query.set('status', String(params.status))
  if (params.role !== undefined) query.set('role', String(params.role))
  return request<{ list: AdminUser[]; total: number; page: number; pageSize: number; pages: number }>(`/admin/users?${query.toString()}`)
}

export function adminCreateUser(data: {
  username: string
  email: string
  password: string
  status: number
  role: number
}) {
  return request<AdminUser>('/admin/users', { method: 'POST', body: data })
}

export function adminUpdateUser(id: string, data: Partial<{
  username: string
  email: string
  status: number
  role: number
}>) {
  return request<null>(`/admin/users/${id}`, { method: 'PUT', body: data })
}

export function adminDeleteUser(id: string) {
  return request<null>(`/admin/users/${id}`, { method: 'DELETE' })
}

export function adminResetUserPassword(id: string, data: { password: string }) {
  return request<null>(`/admin/users/${id}/reset-password`, { method: 'POST', body: data })
}

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

export function adminTestModel(data: {
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
  }>('/models/test', { method: 'POST', body: data })
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

export function adminTestTool(data: {
  provider_type: string
  provider_config?: Record<string, unknown>
  user_config?: Record<string, unknown>
  admin_config?: Record<string, unknown>
  tool_input?: Record<string, unknown>
}) {
  return request<{
    success: boolean
    message: string
    error?: string
    response_time_ms: number
    details?: string
  }>('/admin/tools/test', { method: 'POST', body: data })
}

// ── Admin Sessions ──

export function adminListSessions(params: {
  page: number
  pageSize: number
  keyword?: string
  status?: string
}) {
  const query = new URLSearchParams()
  query.set('page', String(params.page))
  query.set('pageSize', String(params.pageSize))
  if (params.keyword) query.set('keyword', params.keyword)
  if (params.status) query.set('status', params.status)
  return request<{ list: AdminSession[]; total: number; page: number; pageSize: number; pages: number }>(`/admin/sessions?${query.toString()}`)
}

export function adminDeleteSession(id: string) {
  return request<null>(`/admin/sessions/${id}`, { method: 'DELETE' })
}

export function adminCleanupSessions() {
  return request<{ deleted: number }>('/admin/sessions/cleanup', { method: 'POST' })
}
