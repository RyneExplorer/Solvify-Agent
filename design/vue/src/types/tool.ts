// ── Tool Type ──

export interface ToolTypeInfo {
  id: string
  name: string
  tool_key: string
  description: string
  execution_mode: string
  input_schema: Record<string, unknown> | null
  is_enabled: boolean
  provider_count: number
}

// ── Tool Provider ──

export interface ToolProviderInfo {
  id: string
  tool_type_id: string
  provider_key: string
  name: string
  description: string
  provider_type: 'mcp'
  config_schema: Record<string, unknown> | null
  input_schema: Record<string, unknown> | null
  provider_config: ProviderConfig | null
  admin_config: Record<string, unknown> | null
  rate_limit: Record<string, unknown> | null
  mcp_tool_manifest?: MCPToolInfo[] | null
  is_enabled: boolean
  is_system: boolean
  display_order: number
}

// 供应商配置
export interface ProviderConfig {
  mcp?: MCPConfig
}

// MCP 服务器配置（裸格式，后端会自动包装为 {provider_config: {mcp: ...}}）
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

// 管理后台 MCP 服务器聚合信息
export interface MCPServerInfo {
  id: string
  tool_type_id: string
  tool_type_name: string
  tool_type_key: string
  provider_key: string
  name: string
  description: string
  is_enabled: boolean
  is_system: boolean
  mcp_tool_manifest: MCPToolInfo[] | null
}

// 工具测试结果（通用，含 MCP 扩展字段）
export interface ToolTestResult {
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
