import type { ApiResponse } from '@/types/common'

const BASE_URL = '/api/v1'
const TOKEN_KEY = 'solvify_token'
let routerInstance: { push: (path: string) => void } | null = null

export function setRouter(router: { push: (path: string) => void }) {
  routerInstance = router
}

interface RequestOptions {
  method?: string
  body?: unknown
  /** Skip auth header for public endpoints */
  isPublic?: boolean
}

interface FormRequestOptions {
  method?: string
  body: FormData
  /** Skip auth header for public endpoints */
  isPublic?: boolean
}

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string) {
  localStorage.setItem(TOKEN_KEY, token)
}

export function removeToken() {
  localStorage.removeItem(TOKEN_KEY)
}

export function hasToken(): boolean {
  return !!getToken()
}

/** Generic JSON request */
export async function request<T>(
  path: string,
  options: RequestOptions = {},
): Promise<ApiResponse<T>> {
  const { method = 'GET', body, isPublic } = options

  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }

  // Attach JWT token for non-public requests
  if (!isPublic) {
    const token = getToken()
    if (token) {
      headers['Authorization'] = `Bearer ${token}`
    }
  }

  const res = await fetch(`${BASE_URL}${path}`, {
    method,
    headers,
    ...(body ? { body: JSON.stringify(body) } : {}),
  })

  if (!res.ok) {
    if (res.status === 401) {
      removeToken()
      routerInstance?.push('/login')
      throw new Error('登录已过期，请重新登录')
    }
    const text = await res.text().catch(() => '')
    throw new Error(text || `HTTP ${res.status}`)
  }

  const data = await res.json()
  // Server-side token invalid / auth error
  if (data.code === 401 || data.code === 403) {
    removeToken()
    routerInstance?.push('/login')
    throw new Error(data.message || '登录已过期，请重新登录')
  }
  // Business error
  if (data.code !== 0) {
    throw new Error(data.message || '请求失败')
  }
  return data
}

/** 通用 multipart/form-data 请求 */
export async function formRequest<T>(
  path: string,
  options: FormRequestOptions,
): Promise<ApiResponse<T>> {
  const { method = 'POST', body, isPublic } = options

  const headers: Record<string, string> = {}

  // multipart 请求不能手动设置 Content-Type，浏览器会自动生成 boundary
  if (!isPublic) {
    const token = getToken()
    if (token) {
      headers['Authorization'] = `Bearer ${token}`
    }
  }

  const res = await fetch(`${BASE_URL}${path}`, {
    method,
    headers,
    body,
  })

  if (!res.ok) {
    if (res.status === 401) {
      removeToken()
      routerInstance?.push('/login')
      throw new Error('登录已过期，请重新登录')
    }
    const text = await res.text().catch(() => '')
    throw new Error(text || `HTTP ${res.status}`)
  }

  const data = await res.json()
  if (data.code === 401 || data.code === 403) {
    removeToken()
    routerInstance?.push('/login')
    throw new Error(data.message || '登录已过期，请重新登录')
  }
  if (data.code !== 0) {
    throw new Error(data.message || '请求失败')
  }
  return data
}

/** 获取需要鉴权的二进制响应 */
export async function blobRequest(path: string): Promise<Blob> {
  const headers: Record<string, string> = {}
  const token = getToken()
  if (token) headers.Authorization = `Bearer ${token}`

  const res = await fetch(`${BASE_URL}${path}`, { headers })
  if (!res.ok) {
    if (res.status === 401) {
      removeToken()
      routerInstance?.push('/login')
      throw new Error('登录已过期，请重新登录')
    }
    const text = await res.text().catch(() => '')
    throw new Error(text || `HTTP ${res.status}`)
  }
  return res.blob()
}

/** SSE stream request — returns a ReadableStream reader */
export async function streamRequest(
  path: string,
  body: unknown,
  signal?: AbortSignal,
): Promise<ReadableStreamDefaultReader<Uint8Array>> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }

  const token = getToken()
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }

  const res = await fetch(`${BASE_URL}${path}`, {
    method: 'POST',
    headers,
    body: JSON.stringify(body),
    signal,
  })

  if (!res.ok) {
    if (res.status === 401) {
      removeToken()
      routerInstance?.push('/login')
      throw new Error('登录已过期，请重新登录')
    }
    const text = await res.text().catch(() => '')
    throw new Error(text || `HTTP ${res.status}`)
  }

  return res.body!.getReader()
}
