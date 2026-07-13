import { request } from './client'
import type { StorageQuota } from '@/types/storage'

// 查询当前用户存储配额
export function getStorageQuota() {
  return request<StorageQuota>('/storage/quota')
}
