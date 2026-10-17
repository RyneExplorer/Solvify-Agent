import { request } from './client'
import type { MCPConnection, MCPDraft, MCPTemplate, MCPProbeResult } from '@/types/mcp'
const base = '/user/mcp-connections'
export const listMCPTemplates = () => request<MCPTemplate[]>(`${base}/templates`)
export const listMCPConnections = () => request<MCPConnection[]>(base)
export const getMCPConnection = (id: string) => request<MCPConnection>(`${base}/${id}`)
export const createMCPConnection = (body: MCPDraft) => request<MCPConnection>(base, { method: 'POST', body })
export const updateMCPConnection = (id: string, body: Partial<MCPDraft> & { is_enabled?: boolean }) => request<MCPConnection>(`${base}/${id}`, { method: 'PUT', body })
export const deleteMCPConnection = (id: string) => request<null>(`${base}/${id}`, { method: 'DELETE' })
export const probeMCPConnection = (body: MCPDraft | { id: string }) => request<MCPProbeResult>(`${base}/probe`, { method: 'POST', body })
