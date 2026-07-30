/** Unified API response envelope */
export interface ApiResponse<T> {
  code: number
  message?: string
  data: T
  /** 由 client.ts 从响应头注入，可选 */
  _meta?: {
    trace_id?: string
    request_id?: string
  }
}

/** Paginated list wrapper */
export interface PaginatedData<T> {
  list: T[]
  total: number
  page: number
  page_size: number
}
