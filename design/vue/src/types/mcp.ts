export interface MCPTemplate { id: string; name: string; description: string; config_schema: ConfigSchema | null }
export interface MCPConnection { id: string; template_id: string | null; display_name: string; is_enabled: boolean; connection?: MCPConfig; config?: Record<string, unknown> }
export interface MCPDraft { display_name: string; template_id?: string; connection?: MCPConfig; config?: Record<string, unknown> }
export type MCPProbeResult = MCPTestResult

export interface MCPConfig {
  transport: 'sse' | 'http'
  url?: string
  headers?: Record<string, string>
  // 旧字段只用于读取兼容；新保存使用独立时限
  timeout?: number
  connect_timeout?: number
  call_timeout?: number
}

// MCP 工具信息（测试连接返回）
export interface MCPToolInfo {
  name: string
  description?: string
}

// 管理员MCP模板（连接仅在编辑详情返回）。
export interface MCPTemplateAdminInfo {
 id:string
 key:string
 name:string
 description:string
 connection?:MCPConfig
 config_schema:ConfigSchema | null
 is_enabled:boolean
 is_system:boolean
 mcp_tool_manifest:MCPToolInfo[] | null
}

// MCP连接探测结果
export interface MCPTestResult {
  success: boolean
  message: string
  error?: string
  response_time_ms: number
  details?: string
  mcp_tool_count?: number
  mcp_tools?: MCPToolInfo[]
}

// ── Config Schema ──

export interface SchemaProperty {
  type: string
  title?: string
  description?: string
  default?: unknown
  minLength?: number
  maxLength?: number
  minimum?: number
  maximum?: number
  enum?: string[]
  secret?: boolean
}

export interface ConfigSchema {
  type: string
  properties: Record<string, SchemaProperty>
  required?: string[]
  [key: string]: unknown
}
