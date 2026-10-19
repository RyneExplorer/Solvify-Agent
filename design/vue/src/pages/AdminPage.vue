<template>
  <div class="py-8 px-10">
    <h1 class="text-[28px] font-bold text-slate-900 mb-6" style="font-family: 'Space Grotesk', sans-serif; letter-spacing: -0.02em;">后台管理</h1>

    <!-- 标签页 -->
    <div class="flex border-b border-slate-200 mb-6 overflow-x-auto">
      <button
        v-for="tab in adminTabs" :key="tab.key"
        @click="activeTab = tab.key"
        :class="[
          'px-4 py-2.5 text-sm border-b-2 transition-colors cursor-pointer whitespace-nowrap',
          activeTab === tab.key
            ? 'text-slate-900 font-medium border-slate-900'
            : 'text-slate-400 border-transparent hover:text-slate-600'
        ]"
      >
        {{ tab.label }}
      </button>
    </div>

    <!-- Users tab -->
    <div v-if="activeTab === 'users'">
      <div class="flex justify-between mb-4">
        <SearchInput v-model="userSearch" placeholder="搜索用户名/邮箱..." wrapper-class="w-80" />
        <AppButton @click="openUserModal()">+ 添加用户</AppButton>
      </div>
      <AppCard class="!p-0 overflow-hidden">
        <table class="w-full text-sm border-collapse">
          <thead>
            <tr class="bg-slate-50 border-b border-slate-200">
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">用户</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">邮箱</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">角色</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">状态</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="u in users" :key="u.id" class="border-b border-slate-100 last:border-b-0">
              <td class="px-4 py-3">
                <div class="flex items-center gap-2">
                  <AppAvatar :name="u.username" />
                  <span class="font-medium text-slate-900">{{ u.username }}</span>
                </div>
              </td>
              <td class="px-4 py-3 text-slate-900">{{ u.email }}</td>
              <td class="px-4 py-3"><AppBadge variant="blue">{{ roleText(u.role) }}</AppBadge></td>
              <td class="px-4 py-3"><AppBadge :variant="u.status === 1 ? 'success' : 'neutral'">{{ u.status === 1 ? '活跃' : '停用' }}</AppBadge></td>
              <td class="px-4 py-3">
                <div class="flex items-center gap-1">
                  <AppButton variant="ghost" size="sm" @click="openUserModal(u)">编辑</AppButton>
                  <AppButton variant="ghost" size="sm" @click="openResetPasswordModal(u)">重置密码</AppButton>
                  <AppButton variant="ghost" size="sm" class="text-red-600 hover:text-red-700 hover:bg-red-50" @click="deleteUser(u.id)">删除</AppButton>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </AppCard>
    </div>

    <!-- Sessions tab -->
    <div v-if="activeTab === 'sessions'">
      <div class="flex justify-between mb-4">
        <div class="flex gap-2">
          <SearchInput v-model="sessionSearch" placeholder="搜索标题/用户名..." wrapper-class="w-64" />
          <AppSelect v-model="sessionStatus" :options="sessionStatusOptions" />
        </div>
        <AppButton variant="danger" size="sm" @click="cleanupSessions">清理过期会话</AppButton>
      </div>
      <AppCard class="!p-0 overflow-hidden">
        <table class="w-full text-sm border-collapse">
          <thead>
            <tr class="bg-slate-50 border-b border-slate-200">
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">标题</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">用户</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">模型</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">状态</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">消息数</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">更新时间</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="s in sessions" :key="s.id" class="border-b border-slate-100 last:border-b-0">
              <td class="px-4 py-3 font-medium text-slate-900 max-w-xs truncate" :title="s.title">{{ s.title || '未命名会话' }}</td>
              <td class="px-4 py-3 text-slate-900">{{ s.username }}</td>
              <td class="px-4 py-3 text-slate-500">{{ s.model_id }}</td>
              <td class="px-4 py-3"><AppBadge :variant="s.status === 'active' ? 'success' : 'neutral'">{{ s.status }}</AppBadge></td>
              <td class="px-4 py-3 text-slate-500">{{ s.message_count }}</td>
              <td class="px-4 py-3 text-slate-500">{{ formatDate(s.updated_at) }}</td>
              <td class="px-4 py-3">
                <AppButton variant="ghost" size="sm" class="text-red-600 hover:text-red-700 hover:bg-red-50" @click="deleteSession(s.id)">删除</AppButton>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="!sessions.length" class="px-4 py-8 text-center text-sm text-slate-400">暂无会话</div>
      </AppCard>
      <div v-if="sessionTotal > sessionPageSize" class="flex justify-end mt-4">
        <el-pagination v-model:current-page="sessionPage" :page-size="sessionPageSize" :total="sessionTotal" layout="prev, pager, next" background />
      </div>
    </div>

    <!-- 模型管理 -->
    <div v-if="activeTab === 'models'">
      <div class="flex justify-between mb-4">
        <SearchInput v-model="modelSearch" placeholder="搜索模型..." wrapper-class="w-80" />
        <AppButton @click="openModelModal()">+ 添加模型</AppButton>
      </div>
      <AppCard class="!p-0 overflow-hidden">
        <table class="w-full text-sm border-collapse">
          <thead>
            <tr class="bg-slate-50 border-b border-slate-200">
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">提供商</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">模型 ID</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">Base URL</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">最大上下文</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">状态</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="m in filteredModels" :key="m.id" class="border-b border-slate-100 last:border-b-0">
              <td class="px-4 py-3 font-medium text-slate-900">{{ m.provider }}</td>
              <td class="px-4 py-3 text-slate-900">{{ m.model_id }}</td>
              <td class="px-4 py-3 text-slate-900">{{ m.base_url || '-' }}</td>
              <td class="px-4 py-3 text-slate-900 font-mono tabular-nums">{{ m.max_context_length ?? '-' }} {{ m.capability_status === 'text_only' ? '（当前能力不能用于工具Agent）' : (m.capability_status === 'pending' || modelSettingsError(m.max_context_length, m.config)) ? '（能力待确认）' : '' }}</td>
              <td class="px-4 py-3"><AppBadge :variant="m.is_enabled ? 'success' : 'neutral'">{{ m.is_enabled ? '启用' : '停用' }}</AppBadge></td>
              <td class="px-4 py-3">
                <div class="flex items-center gap-1">
                  <AppButton variant="ghost" size="sm" @click="openModelModal(m)">编辑</AppButton>
                  <AppButton variant="ghost" size="sm" @click="toggleModelEnabled(m)">{{ m.is_enabled ? '停用' : '启用' }}</AppButton>
                  <AppButton variant="ghost" size="sm" class="text-red-600 hover:text-red-700 hover:bg-red-50" @click="deleteModel(m.id)">删除</AppButton>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </AppCard>
    </div>

    <MCPAdminSettings v-if="activeTab === 'mcpServers'" />

    <!-- 模型弹窗 -->
    <div v-if="modelModalVisible" class="fixed inset-0 z-50 flex items-center justify-center">
      <div class="absolute inset-0 bg-black/30" @click="modelModalVisible = false" />
      <div class="relative bg-white rounded-2xl shadow-xl border border-slate-200 p-5 w-[600px] max-w-[calc(100vw-2rem)] max-h-[90vh] flex flex-col">
        <h3 class="text-lg font-semibold text-slate-900 mb-4">{{ editingModel ? '编辑模型' : '添加模型' }}</h3>
        <div class="space-y-3 overflow-y-auto min-h-0 pr-1">
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <div class="flex items-center text-xs text-slate-400">使用 OpenAI 兼容接口</div>
            <div>
              <label class="block text-[13px] font-medium text-slate-600 mb-1.5">模型 ID</label>
              <input v-model="modelForm.model_id" placeholder="例如：gpt-4 / qwen-max" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
            </div>
            <div>
              <label class="block text-[13px] font-medium text-slate-600 mb-1.5">Base URL</label>
              <input v-model="modelForm.base_url" placeholder="https://api.openai.com/v1" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
            </div>
            <div>
              <label class="block text-[13px] font-medium text-slate-600 mb-1.5">API Key</label>
              <el-input v-model="modelForm.api_key" type="password" show-password autocomplete="off" placeholder="已保存密钥自动回填" size="large" class="model-api-key-input" />
            </div>
          </div>
          <ModelCapabilityFields admin-mode v-model:window="modelForm.max_context_length" v-model:config="modelForm.config" />

          <!-- 测试结果 -->
          <div v-if="modelTestResult" class="mt-2">
            <div :class="['p-3 rounded-xl text-sm', modelTestResult.success ? 'bg-green-50 text-green-800' : 'bg-red-50 text-red-800']">
              <div class="flex items-center gap-2 mb-1">
                <span>{{ modelTestResult.success ? '✓' : '✗' }}</span>
                <span class="font-medium">{{ modelTestResult.message }}</span>
              </div>
              <div v-if="modelTestResult.response_time_ms" class="text-xs opacity-70">响应时间: {{ modelTestResult.response_time_ms }}ms</div>
              <div v-if="modelTestResult.detected_max_context_length" class="text-xs mt-1 opacity-70">探测到的最大上下文: {{ modelTestResult.detected_max_context_length }}</div>
              <div v-if="modelTestResult.error" class="text-xs mt-1 opacity-70">错误: {{ modelTestResult.error }}</div>
              <div class="text-xs mt-1">连接 {{ modelTestResult.connection_status || '未验证' }} · 流式 {{ modelTestResult.streaming_status || '未验证' }} · 工具 {{ modelTestResult.tool_calling_status || '未验证' }}</div>
              <div v-if="modelTestResult.detected_max_input_tokens" class="text-xs">服务商最大输入：{{ modelTestResult.detected_max_input_tokens }}</div>
              <div v-if="modelTestResult.detected_max_output_tokens" class="text-xs">服务商最大生成：{{ modelTestResult.detected_max_output_tokens }}</div>
              <div v-if="modelTestResult.details" class="text-xs mt-1 opacity-70">详情: {{ modelTestResult.details }}</div>
            </div>
          </div>
        </div>
        <div class="flex flex-wrap justify-end gap-2 mt-4 shrink-0 border-t border-slate-100 pt-4">
          <AppButton variant="secondary" @click="modelModalVisible = false">取消</AppButton>
          <AppButton variant="outline" :disabled="!modelForm.model_id || !modelForm.base_url" :loading="modelTesting" @click="testModel('capabilities')">获取能力</AppButton>
          <AppButton variant="outline" :disabled="!modelForm.provider || !modelForm.model_id || !modelForm.base_url" :loading="modelTesting" @click="testModel()">{{ modelTesting ? '测试中...' : '测试模型' }}</AppButton>
          <AppButton :disabled="!modelFormValid || modelTesting" :loading="modelSaving" @click="saveModel">{{ modelSaving ? '验证中…' : '验证并保存' }}</AppButton>
        </div>
      </div>
    </div>

    <!-- 用户弹窗 -->
    <div v-if="userModalVisible" class="fixed inset-0 z-50 flex items-center justify-center">
      <div class="absolute inset-0 bg-black/30" @click="userModalVisible = false" />
      <div class="relative bg-white rounded-2xl shadow-xl border border-slate-200 p-6 w-[480px]">
        <h3 class="text-lg font-semibold text-slate-900 mb-4">{{ editingUser ? '编辑用户' : '添加用户' }}</h3>
        <div class="space-y-4">
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">用户名 <span class="text-red-500">*</span></label>
            <input v-model="userForm.username" placeholder="请输入用户名" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">邮箱 <span class="text-red-500">*</span></label>
            <input v-model="userForm.email" placeholder="请输入邮箱" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
          </div>
          <div v-if="!editingUser">
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">密码 <span class="text-red-500">*</span></label>
            <input v-model="userForm.password" type="password" placeholder="至少 6 位" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">角色</label>
            <AppSelect v-model="userForm.role" class="w-full">
              <el-option :value="1" label="普通用户" />
              <el-option :value="2" label="管理员" />
            </AppSelect>
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">状态</label>
            <AppSelect v-model="userForm.status" class="w-full">
              <el-option :value="1" label="正常" />
              <el-option :value="2" label="禁用" />
              <el-option :value="3" label="注销" />
              <el-option :value="4" label="待验证" />
            </AppSelect>
          </div>
        </div>
        <div class="flex justify-end gap-2 mt-6">
          <AppButton variant="secondary" @click="userModalVisible = false">取消</AppButton>
          <AppButton :disabled="!userFormValid" @click="saveUser">保存</AppButton>
        </div>
      </div>
    </div>

    <!-- Reset Password Modal -->
    <div v-if="resetPasswordModalVisible" class="fixed inset-0 z-50 flex items-center justify-center">
      <div class="absolute inset-0 bg-black/30" @click="resetPasswordModalVisible = false" />
      <div class="relative bg-white rounded-2xl shadow-xl border border-slate-200 p-6 w-[400px]">
        <h3 class="text-lg font-semibold text-slate-900 mb-4">重置密码</h3>
        <p class="text-sm text-slate-500 mb-4">正在为 <span class="font-medium text-slate-900">{{ resetPasswordUser?.username }}</span> 设置新密码</p>
        <div>
          <label class="block text-[13px] font-medium text-slate-600 mb-1.5">新密码 <span class="text-red-500">*</span></label>
          <input v-model="resetPasswordForm.password" type="password" placeholder="至少 6 位" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
        </div>
        <div class="flex justify-end gap-2 mt-6">
          <AppButton variant="secondary" @click="resetPasswordModalVisible = false">取消</AppButton>
          <AppButton :disabled="!resetPasswordFormValid" @click="saveResetPassword">保存</AppButton>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { simplifiedModelSettingsError as modelSettingsError, managedModelConfig, applyModelCapabilities } from '@/lib/model-capabilities.mjs'
import ModelCapabilityFields from '@/components/ModelCapabilityFields.vue'
import { toolTimeoutValue, toolTimeoutFromConfig } from '@/lib/tool-timeout.mjs'
import { provide, ref, reactive, computed, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import AppCard from '../components/ui/AppCard.vue'
import AppButton from '../components/ui/AppButton.vue'
import AppBadge from '../components/ui/AppBadge.vue'
import AppAvatar from '../components/ui/AppAvatar.vue'
import AppSelect from '../components/ui/AppSelect.vue'
import MCPAdminSettings from '@/components/MCPAdminSettings.vue'
import SearchInput from '../components/ui/SearchInput.vue'
import type { ModelInfo, ModelTestResult } from '@/types/model'
import type { AdminSession } from '@/types/chat'
import {
  adminListUsers,
  adminCreateUser,
  adminUpdateUser,
  adminDeleteUser,
  adminResetUserPassword,
  adminListModels,
  adminGetModelForEdit,
  adminCreateModel,
  adminUpdateModel,
  adminDeleteModel,
  adminListSessions,
  adminDeleteSession,
  adminCleanupSessions,
} from '@/api/admin'
import type { AdminUser } from '@/types/auth'

const activeTab = ref('users')
const userSearch = ref('')
const sessionSearch = ref('')
const sessionStatus = ref('')
const sessionPage = ref(1)
const sessionPageSize = ref(10)
const sessionTotal = ref(0)
const modelSearch = ref('')

const adminTabs = [
  { key: 'users', label: '用户管理' },
  { key: 'sessions', label: '会话管理' },
  { key: 'models', label: '模型管理' },
  { key: 'mcpServers', label: 'MCP 预置' },
]

const users = ref<AdminUser[]>([])
const sessions = ref<AdminSession[]>([])

const sessionStatusOptions = [
  { value: '', label: '全部状态' },
  { value: 'active', label: '活跃' },
  { value: 'archived', label: '已归档' },
  { value: 'closed', label: '已关闭' },
]

// ── Models ──
const models = ref<ModelInfo[]>([])
const filteredModels = computed(() => {
  if (!modelSearch.value.trim()) return models.value
  const kw = modelSearch.value.toLowerCase()
  return models.value.filter(
    m => m.provider.toLowerCase().includes(kw) || m.model_id.toLowerCase().includes(kw)
  )
})
const modelModalVisible = ref(false)
const editingModel = ref<ModelInfo | null>(null)
const modelForm = ref({ provider: 'openai', model_id: '', base_url: '', api_key: '', max_context_length: 0, config: {} as Record<string, unknown> })
const modelFormValid = computed(() => modelForm.value.provider.trim() && modelForm.value.model_id.trim() && !modelSettingsError(modelForm.value.max_context_length, modelForm.value.config))
const modelTestResult = ref<ModelTestResult | null>(null)
const modelTesting = ref(false)
const modelSaving = ref(false)
watch(() => [modelForm.value.provider, modelForm.value.model_id, modelForm.value.base_url], () => {
  if (!modelModalVisible.value) return
  modelForm.value.max_context_length = 0; modelForm.value.config = {}; modelTestResult.value = null
}, { flush: 'sync' })

async function openModelModal(model?: ModelInfo) {
  if (model) {
    try {
      const res = await adminGetModelForEdit(model.id)
      model = res.data
    } catch {
      ElMessage.error('读取模型配置失败，请重试')
      return
    }
  }
  editingModel.value = model ?? null
  if (model) {
    modelForm.value = { provider: model.provider, model_id: model.model_id, base_url: model.base_url || '', api_key: model.api_key || '', max_context_length: model.max_context_length || 0, config: { ...model.config } }
  } else {
    modelForm.value = { provider: 'openai', model_id: '', base_url: '', api_key: '', max_context_length: 0, config: {} as Record<string, unknown> }
  }
  modelModalVisible.value = true
}

watch(() => [modelForm.value.max_context_length, modelForm.value.api_key, modelForm.value.config.reasoning_mode], () => { modelTestResult.value = null })

async function saveModel() {
  if (modelSaving.value) return
  try {
    const error = modelSettingsError(modelForm.value.max_context_length, modelForm.value.config); if (error) throw new Error(error)
    modelSaving.value = true
    modelForm.value.config = managedModelConfig(modelForm.value.config)
    if (editingModel.value) {
      const payload = { ...modelForm.value, reconfirm_capabilities: !modelSettingsError(modelForm.value.max_context_length, modelForm.value.config) }
      if (!payload.api_key) delete payload.api_key
      await adminUpdateModel(editingModel.value.id, payload)
    } else {
      await adminCreateModel(modelForm.value)
    }
    modelModalVisible.value = false
    await loadModels()
    ElMessage.success(editingModel.value ? '保存成功' : '添加成功')
  } catch (e: any) {
    ElMessage.error(e.message || '保存失败')
  } finally { modelSaving.value = false }
}

async function testModel(operation: 'test' | 'capabilities' = 'test') {
  try {
    modelTesting.value = true
    modelTestResult.value = null
    const identity = JSON.stringify([modelForm.value.provider, modelForm.value.model_id, modelForm.value.base_url, modelForm.value.api_key, ...(operation === 'test' ? [modelForm.value.max_context_length, modelForm.value.config.reasoning_mode] : [])])
    const res = await adminTestModel({
      operation,
      provider: modelForm.value.provider,
      model_id: modelForm.value.model_id,
      base_url: modelForm.value.base_url,
      api_key: modelForm.value.api_key,
      config: managedModelConfig(modelForm.value.config),
      max_context_length: modelForm.value.max_context_length || undefined,
    })
    if (identity !== JSON.stringify([modelForm.value.provider, modelForm.value.model_id, modelForm.value.base_url, modelForm.value.api_key, ...(operation === 'test' ? [modelForm.value.max_context_length, modelForm.value.config.reasoning_mode] : [])])) return
    modelTestResult.value = res.data
    if (operation === 'capabilities') {
      const next = applyModelCapabilities(modelForm.value.max_context_length, modelForm.value.config, res.data)
      modelForm.value.max_context_length = next.window; modelForm.value.config = next.config
    }
  } catch (e: any) {
    modelTestResult.value = {
      success: false,
      message: '测试失败',
      error: e.message || '未知错误',
      response_time_ms: 0,
    }
  } finally {
    modelTesting.value = false
  }
}

async function toggleModelEnabled(model: ModelInfo) {
  try {
    await adminUpdateModel(model.id, { is_enabled: !model.is_enabled })
    await loadModels()
    ElMessage.success(model.is_enabled ? '停用成功' : '启用成功')
  } catch (e: any) {
    ElMessage.error(e.message || '操作失败')
  }
}

async function deleteModel(id: string) {
  try {
    await ElMessageBox.confirm('确定删除这个模型吗？', '提示', { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
    await adminDeleteModel(id)
    await loadModels()
    ElMessage.success('删除成功')
  } catch (e: any) {
    if (e === 'cancel' || e === 'close') return
    ElMessage.error(e.message || '删除失败')
  }
}

async function loadModels() {
  try {
    const res = await adminListModels()
    if (res.code === 0) models.value = res.data.models || []
  } catch (e: any) {
    ElMessage.error(e.message || '加载模型失败')
  }
}

// ── Users ──
const userModalVisible = ref(false)
const editingUser = ref<AdminUser | null>(null)
const userForm = ref({ username: '', email: '', password: '', status: 1, role: 1 })
const userFormValid = computed(() => {
  if (editingUser.value) {
    return userForm.value.username.trim() && userForm.value.email.trim()
  }
  return userForm.value.username.trim() && userForm.value.email.trim() && userForm.value.password.length >= 6
})

function roleText(role: number) {
  const map: Record<number, string> = { 1: '普通用户', 2: '管理员' }
  return map[role] || '未知'
}

function openUserModal(user?: AdminUser) {
  editingUser.value = user ?? null
  if (user) {
    userForm.value = { username: user.username, email: user.email, password: '', status: user.status, role: user.role }
  } else {
    userForm.value = { username: '', email: '', password: '', status: 1, role: 1 }
  }
  userModalVisible.value = true
}

async function saveUser() {
  try {
    if (editingUser.value) {
      const payload: Partial<typeof userForm.value> = { ...userForm.value }
      if (!payload.password) delete payload.password
      await adminUpdateUser(editingUser.value.id, payload)
    } else {
      await adminCreateUser(userForm.value)
    }
    userModalVisible.value = false
    await loadUsers()
    ElMessage.success(editingUser.value ? '保存成功' : '添加成功')
  } catch (e: any) {
    ElMessage.error(e.message || '保存失败')
  }
}

async function deleteUser(id: string) {
  try {
    await ElMessageBox.confirm('确定删除这个用户吗？', '提示', { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
    await adminDeleteUser(id)
    await loadUsers()
    ElMessage.success('删除成功')
  } catch (e: any) {
    if (e === 'cancel' || e === 'close') return
    ElMessage.error(e.message || '删除失败')
  }
}

async function loadUsers() {
  try {
    const res = await adminListUsers({ page: 1, pageSize: 100, username: userSearch.value, email: userSearch.value })
    if (res.code === 0) users.value = res.data.list || []
  } catch (e: any) {
    ElMessage.error(e.message || '加载用户失败')
  }
}

// ── Sessions ──
function formatDate(date: string) {
  if (!date) return '-'
  return new Date(date).toLocaleString('zh-CN', { hour12: false })
}

async function loadSessions() {
  try {
    const res = await adminListSessions({
      page: sessionPage.value,
      pageSize: sessionPageSize.value,
      keyword: sessionSearch.value,
      status: sessionStatus.value,
    })
    if (res.code === 0) {
      sessions.value = res.data.list || []
      sessionTotal.value = res.data.total || 0
    }
  } catch (e: any) {
    ElMessage.error(e.message || '加载会话失败')
  }
}

async function deleteSession(id: string) {
  try {
    await ElMessageBox.confirm('确定删除这个会话吗？', '提示', { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
    await adminDeleteSession(id)
    await loadSessions()
    ElMessage.success('删除成功')
  } catch (e: any) {
    if (e === 'cancel' || e === 'close') return
    ElMessage.error(e.message || '删除失败')
  }
}

async function cleanupSessions() {
  try {
    await ElMessageBox.confirm('确定清理所有过期会话吗？此操作不可恢复。', '提示', { confirmButtonText: '清理', cancelButtonText: '取消', type: 'warning' })
    const res = await adminCleanupSessions()
    if (res.code === 0) {
      await loadSessions()
      ElMessage.success(`已清理 ${res.data.deleted} 个过期会话`)
    }
  } catch (e: any) {
    if (e === 'cancel' || e === 'close') return
    ElMessage.error(e.message || '清理失败')
  }
}

// Reset Password
const resetPasswordModalVisible = ref(false)
const resetPasswordUser = ref<AdminUser | null>(null)
const resetPasswordForm = ref({ password: '' })
const resetPasswordFormValid = computed(() => resetPasswordForm.value.password.length >= 6)

function openResetPasswordModal(user: AdminUser) {
  resetPasswordUser.value = user
  resetPasswordForm.value = { password: '' }
  resetPasswordModalVisible.value = true
}

async function saveResetPassword() {
  if (!resetPasswordUser.value) return
  try {
    await adminResetUserPassword(resetPasswordUser.value.id, resetPasswordForm.value)
    resetPasswordModalVisible.value = false
    ElMessage.success('重置密码成功')
  } catch (e: any) {
    ElMessage.error(e.message || '重置密码失败')
  }
}

watch(activeTab, (tab) => {
  if (tab === 'users') loadUsers()
  if (tab === 'sessions') loadSessions()
  if (tab === 'models') loadModels()
})

watch(userSearch, () => loadUsers())
watch([sessionSearch, sessionStatus, sessionPage], () => loadSessions())

onMounted(() => {
  if (activeTab.value === 'users') loadUsers()
  if (activeTab.value === 'sessions') loadSessions()
  if (activeTab.value === 'models') loadModels()
})
</script>
