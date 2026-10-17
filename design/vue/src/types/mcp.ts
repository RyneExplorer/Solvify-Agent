import type { ConfigSchema, MCPConfig, ToolTestResult } from './tool'
export interface MCPTemplate { id: string; name: string; description: string; config_schema: ConfigSchema | null }
export interface MCPConnection { id: string; template_id: string | null; display_name: string; is_enabled: boolean; connection?: MCPConfig; config?: Record<string, unknown> }
export interface MCPDraft { display_name: string; template_id?: string; connection?: MCPConfig; config?: Record<string, unknown> }
export type MCPProbeResult = ToolTestResult
