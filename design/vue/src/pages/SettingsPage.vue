<template>
  <div class="py-8 px-10 max-w-4xl mx-auto h-full overflow-y-auto">
    <h1 class="text-2xl font-bold text-slate-900 mb-8" style="font-family: 'Space Grotesk', sans-serif; letter-spacing: -0.02em;">系统配置</h1>

    <div class="flex border-b border-slate-200 mb-8">
      <button
        v-for="tab in tabs"
        :key="tab.key"
        @click="activeTab = tab.key"
        class="px-4 py-3 text-sm border-b-2 transition-colors cursor-pointer"
        :class="activeTab === tab.key
          ? 'text-slate-900 font-medium border-slate-900'
          : 'text-slate-400 border-transparent hover:text-slate-600'"
      >{{ tab.label }}</button>
    </div>

    <div class="grid grid-cols-3 gap-6">
      <!-- Left column -->
      <div class="col-span-2 space-y-5">
        <!-- AI Model tab -->
        <template v-if="activeTab === 'model'">
          <section>
            <div class="flex items-center justify-between mb-3">
              <h2 class="text-sm font-semibold text-slate-900">系统模型</h2>
              <span class="text-xs text-slate-400">{{ systemModels.length }} 个</span>
            </div>
            <div class="bg-white border border-slate-200 rounded-xl overflow-hidden">
              <div v-if="!systemModels.length" class="px-4 py-8 text-center text-sm text-slate-400">暂无系统模型</div>
              <div
                v-for="m in systemModels"
                :key="m.id"
                class="flex items-center justify-between px-4 py-3 border-b border-slate-100 last:border-0"
              >
                <div>
                  <div class="text-sm font-medium text-slate-900">{{ m.provider }}</div>
                  <div class="text-xs text-slate-400 mt-0.5">{{ m.model_id }}</div>
                </div>
                <AppBadge :variant="m.is_enabled ? 'success' : 'neutral'">{{ m.is_enabled ? '可用' : '停用' }}</AppBadge>
              </div>
            </div>
          </section>

          <section>
            <div class="flex items-center justify-between mb-3">
              <h2 class="text-sm font-semibold text-slate-900">我的模型</h2>
              <AppButton size="sm" @click="openModelCreate">添加模型</AppButton>
            </div>
            <div class="bg-white border border-slate-200 rounded-xl overflow-hidden">
              <div v-if="!userModels.length" class="px-4 py-8 text-center text-sm text-slate-400">暂无自定义模型</div>
              <div
                v-for="m in userModels"
                :key="m.id"
                class="flex items-center justify-between px-4 py-3 border-b border-slate-100 last:border-0"
              >
                <div class="min-w-0 flex-1 mr-3">
                  <div class="text-sm font-medium text-slate-900 truncate">{{ m.display_name || m.model_id }}</div>
                  <div class="text-xs text-slate-400 mt-0.5 truncate">{{ m.api_format }} · {{ m.base_url }}</div>
                </div>
                <div class="flex items-center gap-1.5 shrink-0">
                  <button @click="openModelEdit(m)" class="text-xs px-2.5 py-1 rounded-md text-slate-600 hover:bg-slate-100 border border-slate-200">编辑</button>
                  <button @click="handleModelDelete(m.id)" class="text-xs px-2.5 py-1 rounded-md text-red-600 hover:bg-red-50 border border-red-200">删除</button>
                </div>
              </div>
            </div>
          </section>
        </template>

        <!-- Search Tool tab -->
        <template v-if="activeTab === 'search'">
          <section>
            <div class="flex items-center justify-between mb-3">
              <h2 class="text-sm font-semibold text-slate-900">可用工具</h2>
              <span class="text-xs text-slate-400">{{ toolTemplates.length }} 个</span>
            </div>
            <div class="bg-white border border-slate-200 rounded-xl overflow-hidden">
              <div v-if="!toolTemplates.length" class="px-4 py-8 text-center text-sm text-slate-400">暂无可用的工具模板</div>
              <div
                v-for="t in toolTemplates"
                :key="t.id"
                class="px-4 py-3 border-b border-slate-100 last:border-0"
              >
                <div class="flex items-center justify-between">
                  <div>
                    <div class="text-sm font-medium text-slate-900">{{ t.name }}</div>
                    <div class="text-xs text-slate-400 mt-0.5">{{ t.description || t.tool_key }}</div>
                  </div>
                  <span class="text-[11px] text-slate-500 bg-slate-100 px-2 py-0.5 rounded-full">{{ t.execution_mode === 'sync' ? '同步' : '异步' }}</span>
                </div>
              </div>
            </div>
          </section>

          <section>
            <div class="flex items-center justify-between mb-3">
              <h2 class="text-sm font-semibold text-slate-900">我的工具</h2>
              <AppButton size="sm" @click="openToolCreate">添加工具</AppButton>
            </div>
            <div class="bg-white border border-slate-200 rounded-xl overflow-hidden">
              <div v-if="!userToolConfigs.length" class="px-4 py-8 text-center text-sm text-slate-400">暂无配置的工具</div>
              <div
                v-for="c in userToolConfigs"
                :key="c.id"
                class="flex items-center justify-between px-4 py-3 border-b border-slate-100 last:border-0"
              >
                <div class="min-w-0 flex-1 mr-3">
                  <div class="text-sm font-medium text-slate-900">{{ c.display_name || c.tool_type_name }}</div>
                  <div class="text-xs text-slate-400 mt-0.5">{{ c.tool_type_name }} · {{ c.provider_name }} · {{ c.is_enabled ? '启用' : '停用' }}</div>
                </div>
                <div class="flex items-center gap-1.5 shrink-0">
                  <button
                    @click="handleToolEnable(c)"
                    class="text-xs px-2.5 py-1 rounded-md border"
                    :class="c.is_enabled ? 'text-emerald-600 bg-emerald-50 border-emerald-200' : 'text-slate-600 hover:bg-slate-100 border-slate-200'"
                  >{{ c.is_enabled ? '当前使用' : '设为使用' }}</button>
                  <button @click="openToolEdit(c)" class="text-xs px-2.5 py-1 rounded-md text-slate-600 hover:bg-slate-100 border border-slate-200">编辑</button>
                  <button @click="handleToolDelete(c.id)" class="text-xs px-2.5 py-1 rounded-md text-red-600 hover:bg-red-50 border border-red-200">删除</button>
                </div>
              </div>
            </div>
          </section>
        </template>

        <!-- Status tab -->
        <template v-if="activeTab === 'status'">
          <section>
            <div class="flex items-center justify-between mb-3">
              <h2 class="text-sm font-semibold text-slate-900">基础设施状态</h2>
              <span class="text-xs text-slate-400">管理员配置</span>
            </div>
            <div class="bg-white border border-slate-200 rounded-xl overflow-hidden">
              <div
                v-for="item in infra"
                :key="item.name"
                class="flex items-center justify-between px-4 py-3 border-b border-slate-100 last:border-0"
              >
                <div>
                  <div class="text-sm font-medium text-slate-900">{{ item.name }}</div>
                  <div class="text-xs text-slate-400 mt-0.5">{{ item.detail }}</div>
                </div>
                <AppBadge :variant="item.variant">{{ item.status }}</AppBadge>
              </div>
            </div>
          </section>

          <section>
            <div class="flex items-center justify-between mb-3">
              <h2 class="text-sm font-semibold text-slate-900">第三方平台集成</h2>
              <span class="text-xs text-slate-400">{{ intg.filter(i => i.status === '已连接').length }}/{{ intg.length }} 已连接</span>
            </div>
            <div class="bg-white border border-slate-200 rounded-xl overflow-hidden">
              <div
                v-for="item in intg"
                :key="item.name"
                class="flex items-center justify-between px-4 py-3 border-b border-slate-100 last:border-0"
              >
                <div>
                  <div class="text-sm font-medium text-slate-900">{{ item.name }}</div>
                  <div class="text-xs text-slate-400 mt-0.5">{{ item.desc }}</div>
                </div>
                <div class="flex items-center gap-3">
                  <span class="text-xs text-slate-400">已同步 {{ item.synced }}</span>
                  <AppBadge :variant="item.status === '已连接' ? 'success' : 'neutral'">{{ item.status }}</AppBadge>
                </div>
              </div>
            </div>
          </section>
        </template>
      </div>

      <!-- Right column: summary card -->
      <div class="col-span-1">
        <div class="sticky top-6 bg-slate-50 border border-slate-200 rounded-xl p-5">
          <h3 class="text-sm font-semibold text-slate-900 mb-4">当前状态</h3>
          <div class="space-y-4">
            <div v-if="activeTab === 'model'">
              <div class="text-xs text-slate-400 mb-1">可用模型</div>
              <div class="text-lg font-semibold text-slate-900">{{ systemModels.filter(m => m.is_enabled).length + userModels.length }}</div>
              <div class="text-xs text-slate-400 mt-0.5">系统 {{ systemModels.filter(m => m.is_enabled).length }} · 自定义 {{ userModels.length }}</div>
            </div>
            <div v-if="activeTab === 'search'">
              <div class="text-xs text-slate-400 mb-1">已启用工具</div>
              <div class="text-lg font-semibold text-slate-900">{{ userToolConfigs.filter(c => c.is_enabled).length }}</div>
              <div class="text-xs text-slate-400 mt-0.5">共 {{ userToolConfigs.length }} 个配置</div>
            </div>
            <div v-if="activeTab === 'status'">
              <div class="text-xs text-slate-400 mb-1">运行中服务</div>
              <div class="text-lg font-semibold text-slate-900">{{ infra.filter(i => i.status === '运行中').length }}</div>
              <div class="text-xs text-slate-400 mt-0.5">共 {{ infra.length }} 个基础设施</div>
            </div>
            <div class="border-t border-slate-200 pt-3">
              <div class="text-xs text-slate-400 leading-relaxed">{{ tabHint }}</div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Modal -->
    <Teleport to="body">
      <div v-if="showModal" class="fixed inset-0 z-50 flex items-center justify-center bg-black/30" @click.self="showModal = false">
        <div class="bg-white rounded-2xl shadow-xl p-6 w-full max-w-md mx-4" style="max-height:90vh;overflow-y:auto;">
          <h3 class="text-lg font-semibold text-slate-900 mb-4" style="font-family: 'Space Grotesk', sans-serif;">{{ modalTitle }}</h3>

          <template v-if="modalMode === 'model'">
            <div class="mb-3"><label class="block text-[13px] font-medium text-slate-600 mb-1.5">API 格式</label><AppSelect v-model="mForm.api_format" class="w-full"><el-option value="openai" label="OpenAI 兼容" /><el-option value="anthropic" label="Anthropic" /><el-option value="custom" label="自定义" /></AppSelect></div>
            <div class="mb-3"><label class="block text-[13px] font-medium text-slate-600 mb-1.5">Base URL</label><input v-model="mForm.base_url" placeholder="https://api.openai.com/v1" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500" /></div>
            <div class="mb-3"><label class="block text-[13px] font-medium text-slate-600 mb-1.5">Model ID</label><input v-model="mForm.model_id" placeholder="gpt-4" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500" /></div>
            <div class="mb-3"><label class="block text-[13px] font-medium text-slate-600 mb-1.5">API Key</label><input v-model="mForm.api_key" type="password" placeholder="sk-..." class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500" /></div>
            <div class="mb-5"><label class="block text-[13px] font-medium text-slate-600 mb-1.5">配置 (JSON 可选)</label><textarea v-model="cfgText" rows="3" placeholder='{"temperature": 0.7}' class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500 resize-none" /></div>
          </template>

          <template v-if="modalMode === 'tool'">
            <div class="mb-3"><label class="block text-[13px] font-medium text-slate-600 mb-1.5">显示名称</label><input v-model="tForm.display_name" placeholder="我的搜索工具" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500" /></div>
            <div class="mb-3"><label class="block text-[13px] font-medium text-slate-600 mb-1.5">工具类型 <span class="text-red-500">*</span></label><AppSelect v-model="selToolType" placeholder="选择工具类型" class="w-full" @change="onToolTypeChange"><el-option v-for="t in toolTemplates" :key="t.id" :value="t.id" :label="t.name" /></AppSelect></div>
            <div v-if="selectedExistingProviderConfig" class="mb-3 rounded-xl border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-700">
              该供应商已配置为「{{ selectedExistingProviderConfig.display_name || selectedExistingProviderConfig.provider_name }}」，请编辑现有配置。
            </div>
            <div class="mb-3" v-if="selProviders.length"><label class="block text-[13px] font-medium text-slate-600 mb-1.5">提供商 <span class="text-red-500">*</span></label><AppSelect v-model="tForm.provider_id" placeholder="选择提供商" class="w-full" @change="onProviderChange"><el-option v-for="p in selProviders" :key="p.id" :value="p.id" :label="p.name" /></AppSelect></div>

            <!-- 动态配置表单 -->
            <div v-if="selectedProviderSchema" class="mb-5 border border-slate-200 rounded-xl p-4 bg-slate-50">
              <h4 class="text-sm font-medium text-slate-900 mb-3">工具配置</h4>
              <div v-for="(field, key) in selectedProviderSchema.properties" :key="key" class="mb-3">
                <label class="block text-[13px] font-medium text-slate-600 mb-1.5">
                  {{ field.title || key }}
                  <span v-if="selectedProviderSchema.required?.includes(String(key))" class="text-red-500">*</span>
                </label>
                <p v-if="field.description" class="text-xs text-slate-400 mb-1">{{ field.description }}</p>

                <!-- String 输入 -->
                <input
                  v-if="field.type === 'string' && !field.enum"
                  :type="field.secret ? 'password' : 'text'"
                  :value="toolConfigValues[String(key)] ?? (field.default as string | undefined) ?? ''"
                  :placeholder="(field.default as string | undefined) ?? ''"
                  class="w-full rounded-xl border border-slate-200 bg-white text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500"
                  @input="toolConfigValues[String(key)] = ($event.target as HTMLInputElement).value"
                />

                <!-- Enum 选择 -->
                <select
                  v-else-if="field.type === 'string' && field.enum"
                  :value="toolConfigValues[String(key)] ?? field.default"
                  class="w-full rounded-xl border border-slate-200 bg-white text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500"
                  @change="toolConfigValues[String(key)] = ($event.target as HTMLSelectElement).value"
                >
                  <option v-for="opt in field.enum" :key="opt" :value="opt">{{ opt }}</option>
                </select>

                <!-- Number 输入 -->
                <input
                  v-else-if="field.type === 'integer' || field.type === 'number'"
                  type="number"
                  :value="toolConfigValues[String(key)] ?? field.default"
                  :min="field.minimum"
                  :max="field.maximum"
                  :step="field.type === 'integer' ? 1 : 0.1"
                  class="w-full rounded-xl border border-slate-200 bg-white text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500"
                  @input="toolConfigValues[String(key)] = Number(($event.target as HTMLInputElement).value)"
                />
              </div>
            </div>

            <!-- 无 schema 时显示 JSON 输入 -->
            <div v-else class="mb-5">
              <label class="block text-[13px] font-medium text-slate-600 mb-1.5">配置 (JSON 可选)</label>
              <textarea v-model="cfgText" rows="4" placeholder='{"api_key": "..."}' class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500 resize-none" />
            </div>
          </template>

          <div class="flex gap-2 justify-end">
            <AppButton variant="secondary" @click="showModal = false">取消</AppButton>
            <AppButton @click="doSave" :disabled="modalMode === 'model' ? !mForm.model_id : !tForm.provider_id">保存</AppButton>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { useModelConfig } from '@/composables/useModelConfig'
import { useToolConfig } from '@/composables/useToolConfig'
import AppButton from '@/components/ui/AppButton.vue'
import AppBadge from '@/components/ui/AppBadge.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import type { UserModelConfigInfo, CreateUserModelConfigRequest } from '@/types/model'
import type { UserToolConfigInfo, CreateUserToolConfigRequest, ConfigSchema } from '@/types/tool'

// ── Tabs ──
const activeTab = ref('model')
const tabs = [
  { key: 'model', label: 'AI 模型' },
  { key: 'search', label: '工具配置' },
  { key: 'status', label: '系统状态' },
]

const tabHint = computed(() => {
  if (activeTab.value === 'model') return '系统模型由管理员统一配置；自定义模型仅当前用户可用。'
  if (activeTab.value === 'search') return '在深度模式下，系统会调用已启用的工具进行联网或知识库检索。'
  return '基础设施状态由管理员维护，普通用户不可修改。'
})

// ── Composables ──
const { systemModels, userModels, loadAll: loadModels, createConfig: createModel, updateConfig: updateModel, deleteConfig: deleteModel } = useModelConfig()
const { toolTemplates, userToolConfigs, loadAll: loadTools, createConfig: createTool, updateConfig: updateTool, deleteConfig: deleteTool } = useToolConfig()

// ── Modal ──
const showModal = ref(false)
const modalMode = ref<'model' | 'tool'>('model')
const editId = ref<string | null>(null)
const cfgText = ref('')
const selToolType = ref('')

const mForm = reactive<CreateUserModelConfigRequest>({ api_format: 'openai', base_url: '', model_id: '', api_key: '' })
const tForm = reactive<CreateUserToolConfigRequest>({ tool_type_id: '', provider_id: '', display_name: '', config: {} })

const modalTitle = computed(() => `${editId.value ? '编辑' : '添加'}${modalMode.value === 'model' ? '模型' : '工具'}`)
const selProviders = computed(() => {
  const t = toolTemplates.value.find(t => t.id === selToolType.value)
  return t?.providers ?? []
})

// 动态配置表单相关
const toolConfigValues = ref<Record<string, unknown>>({})
const selectedProviderSchema = computed<ConfigSchema | null>(() => {
  if (!tForm.provider_id) return null
  const t = toolTemplates.value.find(t => t.id === selToolType.value)
  const p = t?.providers.find(p => p.id === tForm.provider_id)
  const schema = p?.config_schema as ConfigSchema | null | undefined
  if (!schema || schema.type !== 'object' || !schema.properties) return null
  return schema
})

function onToolTypeChange() {
  tForm.tool_type_id = selToolType.value
  tForm.provider_id = ''
  toolConfigValues.value = {}
}

function onProviderChange() {
  toolConfigValues.value = {}
}

const selectedExistingProviderConfig = computed(() => {
  if (editId.value || !tForm.provider_id) return null
  return userToolConfigs.value.find(c => c.provider_id === tForm.provider_id) ?? null
})

// ── Static data ──
const infra = [
  { name: '向量数据库', detail: 'PostgreSQL (pgvector)', status: '运行中', variant: 'success' as const },
  { name: 'RAG 引擎', detail: 'Eino Framework', status: '运行中', variant: 'success' as const },
  { name: '默认 AI 模型', detail: 'GPT-4 (系统级)', status: '系统配置', variant: 'neutral' as const },
]
const intg = [
  { name: '钉钉', desc: '通过 Webhook 同步知识库', synced: '234 篇', status: '已连接' },
  { name: '飞书', desc: '从飞书文档同步知识库', synced: '189 篇', status: '已连接' },
  { name: 'Notion', desc: '从 Notion 页面同步知识库', synced: '-', status: '未配置' },
]

// ── Actions ──
function openModelCreate() { modalMode.value = 'model'; editId.value = null; mForm.api_format = 'openai'; mForm.base_url = ''; mForm.model_id = ''; mForm.api_key = ''; cfgText.value = ''; showModal.value = true }
function openModelEdit(m: UserModelConfigInfo) {
  modalMode.value = 'model'; editId.value = m.id; mForm.api_format = m.api_format; mForm.base_url = m.base_url; mForm.model_id = m.model_id; mForm.api_key = m.api_key || ''
  cfgText.value = m.config ? JSON.stringify(m.config, null, 2) : ''; showModal.value = true
}
async function handleModelDelete(id: string) { if (confirm('确定要删除这个模型配置吗？')) { await deleteModel(id); loadModels() } }

function openToolCreate() {
  modalMode.value = 'tool'; editId.value = null
  tForm.tool_type_id = ''; tForm.provider_id = ''; tForm.display_name = ''; tForm.config = {}
  cfgText.value = ''; selToolType.value = ''; toolConfigValues.value = {}
  showModal.value = true
}
function openToolEdit(c: UserToolConfigInfo) {
  modalMode.value = 'tool'; editId.value = c.id
  tForm.tool_type_id = c.tool_type_id; tForm.provider_id = c.provider_id; tForm.display_name = c.display_name || ''
  selToolType.value = c.tool_type_id
  // 加载已有的配置值到动态表单
  toolConfigValues.value = c.config ? { ...c.config } : {}
  cfgText.value = c.config ? JSON.stringify(c.config, null, 2) : ''
  showModal.value = true
}
async function handleToolEnable(c: UserToolConfigInfo) {
  if (c.is_enabled) return
  try {
    await updateTool(c.id, { is_enabled: true })
    await loadTools()
    ElMessage.success(`已切换为 ${c.provider_name}`)
  } catch (e: unknown) {
    ElMessage.error(e instanceof Error ? e.message : '启用失败')
  }
}
async function handleToolDelete(id: string) { if (confirm('确定要删除这个工具配置吗？')) { await deleteTool(id); loadTools() } }

async function doSave() {
  try {
    if (modalMode.value === 'model') {
      const config = cfgText.value ? JSON.parse(cfgText.value) : {}
      if (editId.value) await updateModel(editId.value, { ...mForm, config })
      else await createModel({ ...mForm, config })
      loadModels()
    } else {
      // 使用动态表单的值或 JSON 输入
      let config: Record<string, unknown> = {}
      if (selectedProviderSchema.value) {
        config = { ...toolConfigValues.value }
      } else if (cfgText.value) {
        config = JSON.parse(cfgText.value)
      }
      if (selectedExistingProviderConfig.value) {
        throw new Error('该供应商已配置，请编辑现有配置')
      }
      const toolTypeId = tForm.tool_type_id || selToolType.value
      if (!toolTypeId) throw new Error('请选择工具类型')
      if (!tForm.provider_id) throw new Error('请选择提供商')
      if (editId.value) await updateTool(editId.value, { provider_id: tForm.provider_id, display_name: tForm.display_name, config })
      else await createTool({ tool_type_id: toolTypeId, provider_id: tForm.provider_id, display_name: tForm.display_name, config })
      loadTools()
    }
    showModal.value = false
    ElMessage.success('保存成功')
  } catch (e: unknown) {
    ElMessage.error(e instanceof Error ? e.message : '保存失败')
  }
}

onMounted(() => { loadModels(); loadTools() })
</script>
