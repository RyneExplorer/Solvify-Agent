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
  provider_type: string  // http, mcp, custom
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
  method: string
  url: string
  headers: Record<string, string>
  body_template: Record<string, unknown>
  response_mapping: Record<string, string>
  auth: AuthConfig | null
  // MCP 专用（仅 provider_type=mcp 时有值）
  mcp?: MCPConfig
}

// MCP 服务器配置（裸格式，后端会自动包装为 {provider_config: {mcp: ...}}）
export interface MCPConfig {
  transport: 'stdio' | 'sse' | 'http'
  // stdio
  command?: string
  args?: string[]
  env?: Record<string, string>
  // sse/http
  url?: string
  headers?: Record<string, string>
  // 通用
  timeout?: number
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

// 认证配置
export interface AuthConfig {
  type: string  // bearer, api_key, basic
  token_field: string
  header: string
  prefix: string
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

// ── Tool Template (combined type + providers) ──

export interface ProviderBrief {
  id: string
  provider_key: string
  name: string
  description: string
  provider_type: string
  config_schema: Record<string, unknown> | null
  input_schema: Record<string, unknown> | null
  is_system: boolean
  mcp_tool_manifest?: MCPToolInfo[] | null
}

export interface ToolTemplate {
  id: string
  name: string
  tool_key: string
  description: string
  execution_mode: string
  provider_count: number
  providers: ProviderBrief[]
}

// ── User Tool Config ──

export interface UserToolConfigInfo {
  id: string
  tool_type_id: string
  tool_type_name: string
  tool_type_key: string
  provider_id: string
  provider_name: string
  display_name: string
  config: Record<string, unknown> | null
  is_enabled: boolean
  created_at: string
  updated_at: string
}

export interface CreateUserToolConfigRequest {
  tool_type_id: string
  provider_id: string
  display_name: string
  config: Record<string, unknown>
}

export interface UpdateUserToolConfigRequest {
  provider_id?: string
  display_name?: string
  config?: Record<string, unknown>
  is_enabled?: boolean
}

// ── List Responses ──

export interface ListToolTemplatesResponse {
  templates: ToolTemplate[]
}

export interface ListToolTypesResponse {
  tool_types: ToolTypeInfo[]
}

export interface ListToolProvidersResponse {
  providers: ToolProviderInfo[]
}

export interface ListUserToolConfigsResponse {
  configs: UserToolConfigInfo[]
}
