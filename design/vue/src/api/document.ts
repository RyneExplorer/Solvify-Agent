import { formRequest, request } from './client'
import type {
  CreateDocumentVersionRequest,
  Document,
  DocumentProcessingJob,
  DocumentVersionDetail,
  DocumentVersionListItem,
  UploadDocumentResponse,
} from '@/types/document'

// 查询知识库下文档列表
export function listDocuments(kbId: string) {
  return request<Document[]>(`/knowledge-bases/${kbId}/documents`)
}

// 上传文档并由后端自动执行解析、切块、向量化和索引入库
export function uploadDocument(kbId: string, file: File) {
  const formData = new FormData()
  formData.append('file', file)
  return formRequest<UploadDocumentResponse>(`/knowledge-bases/${kbId}/documents`, {
    body: formData,
  })
}

// 查询文档详情
export function getDocument(id: string) {
  return request<Document>(`/documents/${id}`)
}

// 删除文档
export function deleteDocument(id: string) {
  return request<{ deleted: boolean }>(`/documents/${id}`, { method: 'DELETE' })
}

// 手动重试失败的文档处理任务
export function processDocument(id: string) {
  return request<DocumentProcessingJob>(`/documents/${id}/process`, { method: 'POST' })
}

// 查询文档处理任务历史
export function listDocumentJobs(id: string) {
  return request<DocumentProcessingJob[]>(`/documents/${id}/jobs`)
}

// 查询单个文档处理任务详情
export function getDocumentJob(id: string) {
  return request<DocumentProcessingJob>(`/document-jobs/${id}`)
}

// 查询文档版本列表
export function listDocumentVersions(id: string) {
  return request<DocumentVersionListItem[]>(`/documents/${id}/versions`)
}

// 查询文档版本详情
export function getDocumentVersion(id: string, versionId: string) {
  return request<DocumentVersionDetail>(`/documents/${id}/versions/${versionId}`)
}

// 保存文档新版本，后端自动重建 chunks、向量和索引
export function createDocumentVersion(id: string, data: CreateDocumentVersionRequest) {
  return request<DocumentProcessingJob>(`/documents/${id}/versions`, { method: 'POST', body: data })
}
