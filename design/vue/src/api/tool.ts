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
