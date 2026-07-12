import { request } from './client'
import type {
  ToolTemplate,
  UserToolConfigInfo,
  CreateUserToolConfigRequest,
  UpdateUserToolConfigRequest,
  ListUserToolConfigsResponse,
} from '@/types/tool'

// ── Tool Templates (combined type + providers) ──

export function listToolTemplates() {
  return request<ToolTemplate[]>('/user/tools/templates')
}

// ── User Tool Configs ──

export function listUserToolConfigs() {
  return request<ListUserToolConfigsResponse>('/user/tool-configs')
}

export function createUserToolConfig(data: CreateUserToolConfigRequest) {
  return request<UserToolConfigInfo>('/user/tool-configs', {
    method: 'POST',
    body: data,
  })
}

export function updateUserToolConfig(
  id: string,
  data: UpdateUserToolConfigRequest,
) {
  return request<UserToolConfigInfo>(`/user/tool-configs/${id}`, {
    method: 'PUT',
    body: data,
  })
}

export function deleteUserToolConfig(id: string) {
  return request<null>(`/user/tool-configs/${id}`, { method: 'DELETE' })
}

export function testUserToolConfig(data: {
  provider_type: string
  provider_id?: string
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
  }>('/user/tool-configs/test', { method: 'POST', body: data })
}
