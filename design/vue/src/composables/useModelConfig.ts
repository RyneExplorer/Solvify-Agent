import { ref } from 'vue'
import * as modelApi from '@/api/model'
import type { ModelInfo, UserModelConfigInfo } from '@/types/model'
import type { CreateUserModelConfigRequest, UpdateUserModelConfigRequest } from '@/types/model'

export function useModelConfig() {
  const systemModels = ref<ModelInfo[]>([])
  const userModels = ref<UserModelConfigInfo[]>([])
  const loading = ref(false)
  const error = ref('')

  async function loadAll() {
    loading.value = true
    error.value = ''
    try {
      const [sysRes, userRes] = await Promise.all([
        modelApi.listModels(),
        modelApi.listUserModelConfigs(),
      ])
      if (sysRes.code === 0) systemModels.value = sysRes.data.models ?? []
      if (userRes.code === 0) userModels.value = userRes.data.models ?? []
    } catch (e: unknown) {
      error.value = e instanceof Error ? e.message : '加载失败'
    } finally {
      loading.value = false
    }
  }

  async function createConfig(data: CreateUserModelConfigRequest) {
    const res = await modelApi.createUserModelConfig(data)
    if (res.code === 0 && res.data) {
      userModels.value.unshift(res.data)
      return res
    }
    throw new Error(res.message || '创建模型配置失败')
  }

  async function updateConfig(
    id: string,
    data: UpdateUserModelConfigRequest,
  ) {
    const res = await modelApi.updateUserModelConfig(id, data)
    if (res.code === 0 && res.data) {
      const idx = userModels.value.findIndex((m) => m.id === id)
      if (idx >= 0) userModels.value[idx] = res.data
      return res
    }
    throw new Error(res.message || '更新模型配置失败')
  }

  async function deleteConfig(id: string) {
    const res = await modelApi.deleteUserModelConfig(id)
    if (res.code === 0) {
      userModels.value = userModels.value.filter((m) => m.id !== id)
      return res
    }
    throw new Error(res.message || '删除模型配置失败')
  }

  return {
    systemModels,
    userModels,
    loading,
    error,
    loadAll,
    createConfig,
    updateConfig,
    deleteConfig,
  }
}
