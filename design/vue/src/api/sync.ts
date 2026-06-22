import { request } from './client'
import type {
  CreateSyncSourceRequest,
  SyncJob,
  SyncItem,
  SyncSource,
  UpdateSyncSourceRequest,
} from '@/types/sync'
import type { Document } from '@/types/document'

// 创建同步源
export function createSyncSource(data: CreateSyncSourceRequest) {
  return request<SyncSource>('/sync-sources', { method: 'POST', body: data })
}

// 查询同步源列表
export function listSyncSources() {
  return request<SyncSource[]>('/sync-sources')
}

// 查询同步源详情
export function getSyncSource(id: string) {
  return request<SyncSource>(`/sync-sources/${id}`)
}

// 更新同步源
export function updateSyncSource(id: string, data: UpdateSyncSourceRequest) {
  return request<SyncSource>(`/sync-sources/${id}`, { method: 'PUT', body: data })
}

// 删除同步源
export function deleteSyncSource(id: string) {
  return request<{ deleted: boolean }>(`/sync-sources/${id}`, { method: 'DELETE' })
}

// 创建同步任务
export function createSyncJob(sourceID: string) {
  return request<SyncJob>(`/sync-sources/${sourceID}/jobs`, { method: 'POST' })
}

// 查询同步源任务列表
export function listSyncJobs(sourceID: string) {
  return request<SyncJob[]>(`/sync-sources/${sourceID}/jobs`)
}

// 查询同步任务详情
export function getSyncJob(id: string) {
  return request<SyncJob>(`/sync-jobs/${id}`)
}

// 查询同步源文件目录
export function listSyncItems(sourceID: string) {
  return request<SyncItem[]>(`/sync-sources/${sourceID}/items`)
}

// 导入同步文件到本地文档
export function importSyncItem(itemID: string) {
  return request<Document>(`/sync-items/${itemID}/import`, { method: 'POST' })
}
