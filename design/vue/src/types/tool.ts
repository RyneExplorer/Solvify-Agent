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
  is_enabled: boolean
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
