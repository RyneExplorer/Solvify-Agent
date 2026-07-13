import { request } from './client'
import type {
  CreateKnowledgeBaseRequest,
  KnowledgeBase,
  KnowledgeBaseStats,
  UpdateKnowledgeBaseRequest,
} from '@/types/knowledge'

// 查询知识库列表
export function listKnowledgeBases() {
  return request<KnowledgeBase[]>('/knowledge-bases')
}

// 查询知识库详情
export function getKnowledgeBase(id: string) {
  return request<KnowledgeBase>(`/knowledge-bases/${id}`)
}

// 创建知识库
export function createKnowledgeBase(data: CreateKnowledgeBaseRequest) {
  return request<KnowledgeBase>('/knowledge-bases', { method: 'POST', body: data })
}

// 更新知识库
export function updateKnowledgeBase(id: string, data: UpdateKnowledgeBaseRequest) {
  return request<KnowledgeBase>(`/knowledge-bases/${id}`, { method: 'PUT', body: data })
}

// 删除知识库
export function deleteKnowledgeBase(id: string) {
  return request<{ deleted: boolean }>(`/knowledge-bases/${id}`, { method: 'DELETE' })
}

// 查询知识库统计
export function getKnowledgeBaseStats(id: string) {
  return request<KnowledgeBaseStats>(`/knowledge-bases/${id}/stats`)
}
