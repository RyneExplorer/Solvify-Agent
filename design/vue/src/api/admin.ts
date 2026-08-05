import { request } from './client'
import type { ModelInfo, ModelTestResult } from '@/types/model'
import type { ToolTypeInfo, ToolProviderInfo } from '@/types/tool'
import type { AdminUser } from '@/types/auth'
import type { AdminSession, MetricsSnapshot, TraceSummary, ChatTraceDetail } from '@/types/chat'

// ── 用户管理 ──

// 分页查询用户列表
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

// 创建用户
export function adminCreateUser(data: {
  username: string
  email: string
  password: string
  status: number
  role: number
}) {
  return request<AdminUser>('/admin/users', { method: 'POST', body: data })
}

// 更新用户信息
export function adminUpdateUser(id: string, data: Partial<{
  username: string
  email: string
  status: number
  role: number
}>) {
  return request<null>(`/admin/users/${id}`, { method: 'PUT', body: data })
}

// 删除用户
export function adminDeleteUser(id: string) {
  return request<null>(`/admin/users/${id}`, { method: 'DELETE' })
}

// 重置用户密码
export function adminResetUserPassword(id: string, data: { password: string }) {
  return request<null>(`/admin/users/${id}/reset-password`, { method: 'POST', body: data })
}

// ── 模型管理 ──

// 获取模型列表
export function adminListModels() {
  return request<{ models: ModelInfo[] }>('/models')
}

// 创建模型配置
export function adminCreateModel(data: {
  provider: string
  model_id: string
  base_url: string
  api_key: string
  max_context_length: number
}) {
  return request<ModelInfo>('/models', { method: 'POST', body: data })
}

// 更新模型配置
export function adminUpdateModel(
  id: string,
  data: Partial<{
    provider: string
    model_id: string
    base_url: string
    api_key: string
    is_enabled: boolean
    max_context_length: number
  }>,
) {
  return request<ModelInfo>(`/models/${id}`, { method: 'PUT', body: data })
}

// 删除模型
export function adminDeleteModel(id: string) {
  return request<null>(`/models/${id}`, { method: 'DELETE' })
}

// 测试模型连通性
export function adminTestModel(data: {
  provider: string
  model_id: string
  base_url: string
  api_key: string
  config?: Record<string, unknown>
}) {
  return request<ModelTestResult>('/models/test', { method: 'POST', body: data })
}

// ── 工具管理 ──

// 获取工具类型列表
export function adminListToolTypes() {
  return request<{ tool_types: ToolTypeInfo[] }>('/admin/tool-types')
}

// 创建工具类型
export function adminCreateToolType(data: {
  name: string
  tool_key: string
  description: string
  execution_mode: string
}) {
  return request<ToolTypeInfo>('/admin/tool-types', { method: 'POST', body: data })
}

// 更新工具类型
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

// 删除工具类型
export function adminDeleteToolType(id: string) {
  return request<null>(`/admin/tool-types/${id}`, { method: 'DELETE' })
}

// 获取指定工具类型下的供应商列表
export function adminListToolProviders(toolTypeId: string) {
  return request<{ providers: ToolProviderInfo[] }>(`/admin/tool-types/${toolTypeId}/providers`)
}

// 创建工具供应商
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

// 更新工具供应商
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

// 删除工具供应商
export function adminDeleteToolProvider(toolTypeId: string, providerId: string) {
  return request<null>(`/admin/tool-types/${toolTypeId}/providers/${providerId}`, {
    method: 'DELETE',
  })
}

// 测试工具连通性
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

// ── 会话管理 ──

// 分页查询会话列表
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

// 删除会话
export function adminDeleteSession(id: string) {
  return request<null>(`/admin/sessions/${id}`, { method: 'DELETE' })
}

// 清理过期会话
export function adminCleanupSessions() {
  return request<{ deleted: number }>('/admin/sessions/cleanup', { method: 'POST' })
}

// ── 可观测性（后台） ──

// 获取可观测性指标快照
export function adminGetObservabilityMetrics() {
  return request<MetricsSnapshot>('/admin/observability/metrics')
}

// 分页查询 Trace 列表
export function adminListTraces(params: {
  page?: number
  pageSize?: number
  session_id?: string
  status?: string
  rating?: number
}) {
  const query = new URLSearchParams()
  if (params.page !== undefined) query.set('page', String(params.page))
  if (params.pageSize !== undefined) query.set('pageSize', String(params.pageSize))
  if (params.session_id) query.set('session_id', params.session_id)
  if (params.status) query.set('status', params.status)
  if (params.rating !== undefined) query.set('rating', String(params.rating))
  return request<{ traces: TraceSummary[]; total: number }>(`/admin/observability/traces?${query.toString()}`)
}

// 获取 Trace 详情
export function adminGetTrace(traceId: string) {
  return request<ChatTraceDetail>(`/admin/observability/traces/${traceId}`)
}
