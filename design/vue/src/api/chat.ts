import { request, streamRequest } from './client'
import type {
  ChatSession,
  CreateSessionRequest,
  UpdateSessionRequest,
  SendMessageRequest,
  ListSessionsResponse,
  ListMessagesResponse,
} from '@/types/chat'

// ── Sessions ──

export function createSession(data: CreateSessionRequest) {
  return request<ChatSession>('/chat/sessions', { method: 'POST', body: data })
}

export function listSessions() {
  return request<ListSessionsResponse>('/chat/sessions')
}

export function getSession(id: string) {
  return request<ChatSession>(`/chat/sessions/${id}`)
}

export function updateSession(id: string, data: UpdateSessionRequest) {
  return request<ChatSession>(`/chat/sessions/${id}`, {
    method: 'PUT',
    body: data,
  })
}

export function deleteSession(id: string) {
  return request<null>(`/chat/sessions/${id}`, { method: 'DELETE' })
}

// ── Messages ──

export function getMessages(sessionId: string) {
  return request<ListMessagesResponse>(`/chat/sessions/${sessionId}/messages`)
}

/** Send message via SSE streaming. Returns a ReadableStream reader. */
export function sendMessage(sessionId: string, data: SendMessageRequest, signal?: AbortSignal) {
  return streamRequest(`/chat/sessions/${sessionId}/messages`, data, signal)
}
