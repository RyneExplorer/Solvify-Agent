import { ref } from 'vue'
import * as toolApi from '@/api/tool'
import type {
  ToolTemplate,
  UserToolConfigInfo,
  CreateUserToolConfigRequest,
  UpdateUserToolConfigRequest,
  ConfigSchema,
} from '@/types/tool'

function parseJSONField(value: unknown): unknown {
  if (typeof value === 'string') {
    try {
      return JSON.parse(value)
    } catch {
      return value
    }
  }
  return value
}

function normalizeProviderSchema(schema: unknown): ConfigSchema | null {
  const parsed = parseJSONField(schema) as Record<string, unknown> | null
  if (!parsed || parsed.type !== 'object' || !parsed.properties) return null
  return parsed as unknown as ConfigSchema
}

function normalizeTemplates(templates: ToolTemplate[]): ToolTemplate[] {
  return templates.map((t) => ({
    ...t,
    providers: t.providers.map((p) => ({
      ...p,
      config_schema: normalizeProviderSchema(p.config_schema),
    })),
  }))
}

export function useToolConfig() {
  const toolTemplates = ref<ToolTemplate[]>([])
  const userToolConfigs = ref<UserToolConfigInfo[]>([])
  const loading = ref(false)
  const error = ref('')

  async function loadAll() {
    loading.value = true
    error.value = ''
    try {
      const [tmplRes, configRes] = await Promise.all([
        toolApi.listToolTemplates(),
        toolApi.listUserToolConfigs(),
      ])
      if (tmplRes.code === 0) {
        const list = Array.isArray(tmplRes.data) ? tmplRes.data : []
        toolTemplates.value = normalizeTemplates(list)
      }
      if (configRes.code === 0)
        userToolConfigs.value = configRes.data?.configs ?? []
    } catch (e: unknown) {
      error.value = e instanceof Error ? e.message : '加载失败'
    } finally {
      loading.value = false
    }
  }

  async function createConfig(data: CreateUserToolConfigRequest) {
    const res = await toolApi.createUserToolConfig(data)
    if (res.code === 0 && res.data) {
      userToolConfigs.value.unshift(res.data)
      return res
    }
    throw new Error(res.message || '创建工具配置失败')
  }

  async function updateConfig(
    id: string,
    data: UpdateUserToolConfigRequest,
  ) {
    const res = await toolApi.updateUserToolConfig(id, data)
    if (res.code === 0 && res.data) {
      const idx = userToolConfigs.value.findIndex((c) => c.id === id)
      if (idx >= 0) userToolConfigs.value[idx] = res.data
      return res
    }
    throw new Error(res.message || '更新工具配置失败')
  }

  async function deleteConfig(id: string) {
    const res = await toolApi.deleteUserToolConfig(id)
    if (res.code === 0) {
      userToolConfigs.value = userToolConfigs.value.filter((c) => c.id !== id)
      return res
    }
    throw new Error(res.message || '删除工具配置失败')
  }

  return {
    toolTemplates,
    userToolConfigs,
    loading,
    error,
    loadAll,
    createConfig,
    updateConfig,
    deleteConfig,
  }
}
