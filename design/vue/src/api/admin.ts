import { request } from './client'
import type { ModelInfo, ModelTestResult } from '@/types/model'
import type { MCPTemplateAdminInfo, MCPTestResult, MCPConfig } from '@/types/mcp'
import type { AdminUser } from '@/types/auth'
import type { AdminSession } from '@/types/chat'

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
  config?: Record<string, unknown>
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
    config: Record<string, unknown>
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
  max_context_length?: number
  operation?: 'test' | 'capabilities'
}) {
  return request<ModelTestResult>('/models/test', { method: 'POST', body: data })
}

// ── MCP预置管理 ──
export function adminListMCPTemplates() { return request<MCPTemplateAdminInfo[]>('/admin/mcp-templates') }
export function adminGetMCPTemplate(id: string) { return request<MCPTemplateAdminInfo>(`/admin/mcp-templates/${id}`) }
export function adminCreateMCPTemplate(data: {key:string;name:string;description:string;connection:MCPConfig;config_schema:Record<string,unknown>}) {
 return request<MCPTemplateAdminInfo>('/admin/mcp-templates', {method:'POST',body:data})
}
export function adminUpdateMCPTemplate(id:string,data:Partial<{name:string;description:string;connection:Partial<MCPConfig>;config_schema:Record<string,unknown>;is_enabled:boolean}>) {
 return request<MCPTemplateAdminInfo>(`/admin/mcp-templates/${id}`, {method:'PUT',body:data})
}
export function adminDeleteMCPTemplate(id:string) { return request<null>(`/admin/mcp-templates/${id}`, {method:'DELETE'}) }
export function adminProbeMCPTemplate(data:{id?:string;connection?:MCPConfig}) {
 return request<MCPTestResult>('/admin/mcp-templates/probe', {method:'POST',body:data})
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

// 仅管理员编辑时读取完整凭据。
export function adminGetModelForEdit(id: string) {
  return request<ModelInfo>(`/models/${id}/edit`)
}
