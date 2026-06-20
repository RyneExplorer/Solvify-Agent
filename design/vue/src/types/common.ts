/** Unified API response envelope */
export interface ApiResponse<T> {
  code: number
  message?: string
  data: T
}

/** Paginated list wrapper */
export interface PaginatedData<T> {
  list: T[]
  total: number
  page: number
  page_size: number
}
