import { request } from './client'
import type { SearchResult } from '@/types/search'

export function searchAll(params: { q: string; top_k?: number }) {
  const query = new URLSearchParams()
  query.set('q', params.q)
  if (params.top_k) query.set('top_k', String(params.top_k))
  return request<SearchResult>(`/search?${query.toString()}`)
}
