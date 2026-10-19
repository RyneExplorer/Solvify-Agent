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
      <!-- 左侧列 -->
      <div class="col-span-2 space-y-5">
        <!-- AI 模型标签页 -->
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
                  <div class="text-xs text-slate-400 mt-0.5 truncate">{{ m.api_format }} · {{ m.base_url }} · 最大上下文 {{ m.max_context_length }} {{ m.capability_status === 'text_only' ? '· 不支持工具 · 请更换模型' : (m.capability_status === 'pending' || modelSettingsError(m.max_context_length, m.config)) ? '· 能力待确认' : '' }}</div>
                </div>
                <div class="flex items-center gap-1.5 shrink-0">
                  <button @click="openModelEdit(m)" class="text-xs px-2.5 py-1 rounded-md text-slate-600 hover:bg-slate-100 border border-slate-200">编辑</button>
                  <button @click="handleModelDelete(m.id)" class="text-xs px-2.5 py-1 rounded-md text-red-600 hover:bg-red-50 border border-red-200">删除</button>
                </div>
              </div>
            </div>
          </section>
        </template>


        <MCPSettings v-if="activeTab === 'mcp'" />

        <!-- 同步配置标签页 -->
        <template v-if="activeTab === 'sync'">
          <section>
            <div class="flex items-center justify-between mb-3">
              <h2 class="text-sm font-semibold text-slate-900">钉钉账号</h2>
              <AppBadge :variant="dingTalkBinding?.bound ? 'success' : 'neutral'">
                {{ dingTalkBinding?.bound ? '已绑定' : '未绑定' }}
              </AppBadge>
            </div>

            <div v-if="dingTalkBindingLoading" class="bg-white border border-slate-200 rounded-xl px-4 py-8 text-center text-sm text-slate-400">
              正在加载绑定状态...
            </div>

            <div v-else class="bg-white border border-slate-200 rounded-xl p-5">
              <div v-if="dingTalkBindingError" class="mb-4 rounded-lg bg-red-50 px-3 py-2 text-xs text-red-600">
                {{ dingTalkBindingError }}
              </div>

              <div v-if="dingTalkBinding?.bound" class="flex items-center justify-between gap-4">
                <div class="flex items-center gap-3 min-w-0">
                  <img
                    v-if="dingTalkBinding.avatar"
                    :src="dingTalkBinding.avatar"
                    alt=""
                    class="w-11 h-11 rounded-full object-cover shrink-0"
                  />
                  <div v-else class="w-11 h-11 rounded-full bg-blue-50 text-blue-600 flex items-center justify-center text-sm font-semibold shrink-0">
                    钉
                  </div>
                  <div class="min-w-0">
                    <div class="text-sm font-medium text-slate-900 truncate">{{ dingTalkBinding.nickname || '已绑定钉钉账号' }}</div>
                    <div class="text-xs text-slate-400 mt-1 truncate">企业 corpId：{{ dingTalkBinding.corp_id || '-' }}</div>
                  </div>
                </div>
                <AppButton variant="secondary" size="sm" @click="unbindDingTalk">解绑</AppButton>
              </div>

              <div v-else class="flex items-center justify-between gap-4">
                <div>
                  <div class="text-sm font-medium text-slate-900">尚未绑定钉钉账号</div>
                  <div class="text-xs text-slate-400 mt-1">绑定后可在知识库页面同步有权限访问的钉钉知识库</div>
                </div>
                <AppButton size="sm" @click="openDingTalkBinding">绑定</AppButton>
              </div>
            </div>
          </section>
        </template>

      </div>

      <!-- 右侧列：状态汇总卡片 -->
      <div class="col-span-1">
        <div class="sticky top-6 bg-slate-50 border border-slate-200 rounded-xl p-5">
          <h3 class="text-sm font-semibold text-slate-900 mb-4">当前状态</h3>
          <div class="space-y-4">
            <div v-if="activeTab === 'model'">
              <div class="text-xs text-slate-400 mb-1">可用模型</div>
              <div class="text-lg font-semibold text-slate-900">{{ systemModels.filter(m => m.is_enabled).length + userModels.length }}</div>
              <div class="text-xs text-slate-400 mt-0.5">系统 {{ systemModels.filter(m => m.is_enabled).length }} · 自定义 {{ userModels.length }}</div>
            </div>


            <div v-if="activeTab === 'sync'">
              <div class="text-xs text-slate-400 mb-1">钉钉账号</div>
              <div class="text-lg font-semibold text-slate-900">{{ dingTalkBinding?.bound ? '已绑定' : '未绑定' }}</div>
              <div class="text-xs text-slate-400 mt-0.5">{{ dingTalkBinding?.bound ? (dingTalkBinding.nickname || '当前账号') : '等待绑定' }}</div>
            </div>
            <div class="border-t border-slate-200 pt-3">
              <div class="text-xs text-slate-400 leading-relaxed">{{ tabHint }}</div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 弹窗 -->
    <Teleport to="body">
      <div v-if="showModal" class="fixed inset-0 z-50 flex items-center justify-center bg-black/30" @click.self="showModal = false">
        <div class="bg-white rounded-2xl shadow-xl p-5 w-full mx-4" :class="modalMode === 'model' ? 'max-w-[600px]' : 'max-w-md'" style="max-height:90vh;overflow-y:auto;">
          <h3 class="text-lg font-semibold text-slate-900 mb-4" style="font-family: 'Space Grotesk', sans-serif;">{{ modalTitle }}</h3>

          <template v-if="modalMode === 'model'">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-3">
            <div class="mb-3 flex items-center text-xs text-slate-400">使用 OpenAI 兼容接口</div>
            <div class="mb-3"><label class="block text-[13px] font-medium text-slate-600 mb-1.5">Base URL</label><input v-model="mForm.base_url" placeholder="https://api.openai.com/v1" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500" /></div>
            <div class="mb-3"><label class="block text-[13px] font-medium text-slate-600 mb-1.5">Model ID</label><input v-model="mForm.model_id" placeholder="gpt-4" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500" /></div>
            <div class="mb-3"><label class="block text-[13px] font-medium text-slate-600 mb-1.5">API Key</label><el-input v-model="mForm.api_key" type="password" show-password autocomplete="off" placeholder="已保存密钥自动回填" size="large" class="model-api-key-input" style="--model-key-focus-color: #6366f1" /></div>
            </div>
            <ModelCapabilityFields v-model:window="mForm.max_context_length" v-model:config="modelOptions" />

            <!-- 测试结果 -->
            <div v-if="modelTestResult" class="mb-5">
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
          </template>



          <div class="flex flex-wrap gap-2 justify-end mt-4 border-t border-slate-100 pt-4">
            <AppButton variant="secondary" @click="showModal = false">取消</AppButton>
            <AppButton v-if="modalMode === 'model'" variant="outline" :disabled="!mForm.model_id || !mForm.base_url" :loading="modelTesting" @click="doTestModel('capabilities')">获取能力</AppButton>
            <AppButton v-if="modalMode === 'model'" variant="outline" :disabled="!mForm.model_id || !mForm.base_url" :loading="modelTesting" @click="doTestModel()">{{ modelTesting ? '测试中...' : '测试模型' }}</AppButton>
            <AppButton :loading="modelSaving" @click="doSave" :disabled="!mForm.model_id || !!modelSettingsError(mForm.max_context_length, modelOptions) || modelTesting">{{ modalMode === 'model' ? (modelSaving ? '验证中…' : '验证并保存') : '保存' }}</AppButton>
          </div>
        </div>
      </div>
    </Teleport>

    <Teleport to="body">
      <div v-if="dingTalkModalVisible" class="fixed inset-0 z-50 flex items-center justify-center bg-black/30" @click.self="dingTalkModalVisible = false">
        <div class="bg-white rounded-2xl shadow-xl p-6 w-full max-w-sm mx-4">
          <div class="flex items-center justify-between mb-2">
            <h3 class="text-lg font-semibold text-slate-900">绑定钉钉账号</h3>
            <button type="button" class="text-xl leading-none text-slate-400 hover:text-slate-600" @click="dingTalkModalVisible = false">×</button>
          </div>
          <p class="text-xs text-slate-400 mb-4">请使用钉钉扫码完成授权</p>
          <div v-if="dingTalkBindingError" class="mb-4 rounded-lg bg-red-50 px-3 py-2 text-xs text-red-600">
            {{ dingTalkBindingError }}
          </div>
          <div id="settings-dingtalk-login-frame" class="mx-auto w-[300px] h-[300px] border border-slate-100 rounded-xl overflow-hidden bg-slate-50" />
          <div class="mt-4 flex justify-end gap-2">
            <AppButton variant="secondary" @click="dingTalkModalVisible = false">取消</AppButton>
            <AppButton variant="secondary" :disabled="dingTalkQrLoading" @click="renderDingTalkLoginFrame">
              {{ dingTalkQrLoading ? '二维码加载中...' : '刷新二维码' }}
            </AppButton>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { userModelSettingsError as modelSettingsError, userModelConfig, applyModelCapabilities } from '@/lib/model-capabilities.mjs'
import ModelCapabilityFields from '@/components/ModelCapabilityFields.vue'
import { ref, reactive, computed, nextTick, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useModelConfig } from '@/composables/useModelConfig'
import MCPSettings from '@/components/MCPSettings.vue'
import {
  deleteDingTalkBinding,
  exchangeDingTalkAuthCode,
  getDingTalkBinding,
  getDingTalkOAuthConfig,
} from '@/api/dingtalk'
import AppButton from '@/components/ui/AppButton.vue'
import AppBadge from '@/components/ui/AppBadge.vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import type { UserModelConfigInfo, CreateUserModelConfigRequest, ModelTestResult } from '@/types/model'
import { testUserModelConfig, getUserModelConfig } from '@/api/model'
import type { DingTalkBinding } from '@/types/dingtalk'

// ── 标签页 ──
const activeTab = ref('model')
const tabs = [
  { key: 'model', label: 'AI 模型' },
  { key: 'mcp', label: 'MCP' },
  { key: 'sync', label: '同步配置' },
]

// 当前标签页的提示文案
const tabHint = computed(() => {
  if (activeTab.value === 'model') return '系统模型由管理员统一配置；自定义模型仅当前用户可用。请选择支持工具调用的模型，以配合快速检索和联网搜索功能。'
  if (activeTab.value === 'mcp') return '配置个人 MCP 或填写系统预置所需凭据，由深度模式的专业 Agent 使用。'
  if (activeTab.value === 'sync') return '钉钉账号绑定状态与知识库页面保持一致，解绑不会删除已创建的同步知识库。'
  return '配置需要在深度模式下使用的工具。启用后，AI 将根据对话内容自动调用相应工具获取信息。'
})

// ── 组合式函数 ──
const { systemModels, userModels, loadAll: loadModels, createConfig: createModel, updateConfig: updateModel, deleteConfig: deleteModel } = useModelConfig()

const dingTalkBinding = ref<DingTalkBinding | null>(null)
const dingTalkBindingLoading = ref(false)
const dingTalkBindingError = ref('')
const dingTalkModalVisible = ref(false)
const dingTalkQrLoading = ref(false)
const dingTalkBindingSubmitting = ref(false)
const exchangedDingTalkAuthCodes = new Set<string>()

// ── 弹窗 ──
const showModal = ref(false)
const modalMode = ref<'model'>('model')
const editId = ref<string | null>(null)
const cfgText = ref('')

const modelOptions = ref<Record<string, unknown>>({})
const mForm = reactive<CreateUserModelConfigRequest>({ api_format: 'openai', base_url: '', model_id: '', api_key: '', max_context_length: 0 })
const modelTestResult = ref<ModelTestResult | null>(null)
const modelTesting = ref(false)
const modelSaving = ref(false)
watch(() => [mForm.api_format, mForm.model_id, mForm.base_url], () => {
  if (!showModal.value || modalMode.value !== 'model') return
  mForm.max_context_length = 0; modelOptions.value = {}; modelTestResult.value = null
}, { flush: 'sync' })
const modalTitle = computed(() => `${editId.value ? '编辑' : '添加'}模型`)
// ── 操作 ──
// 打开添加模型弹窗
function openModelCreate() { modalMode.value = 'model'; editId.value = null; mForm.api_format = 'openai'; mForm.base_url = ''; mForm.model_id = ''; mForm.api_key = ''; mForm.max_context_length = 0; modelOptions.value = {}; cfgText.value = ''; modelTestResult.value = null; showModal.value = true }
// 打开编辑模型弹窗
async function openModelEdit(m: UserModelConfigInfo) {
  try {
    const res = await getUserModelConfig(m.id)
    m = res.data
  } catch {
    ElMessage.error('读取模型配置失败，请重试')
    return
  }
  modalMode.value = 'model'; editId.value = m.id; mForm.api_format = m.api_format; mForm.base_url = m.base_url; mForm.model_id = m.model_id; mForm.api_key = m.api_key || ''
  mForm.max_context_length = m.max_context_length || 0
  modelOptions.value = { ...m.config }; cfgText.value = ''; modelTestResult.value = null; showModal.value = true
}

watch(() => [mForm.max_context_length, mForm.api_key, modelOptions.value.reasoning_mode], () => { modelTestResult.value = null })

// 测试模型连通性
async function doTestModel(operation: 'test' | 'capabilities' = 'test') {
  try {
    modelTesting.value = true
    modelTestResult.value = null
    const config = userModelConfig(modelOptions.value)
    const identity = JSON.stringify([mForm.api_format, mForm.model_id, mForm.base_url, mForm.api_key, ...(operation === 'test' ? [mForm.max_context_length, modelOptions.value.reasoning_mode] : [])])
    const res = await testUserModelConfig({
      operation,
      config_id: editId.value || undefined,
      provider: mForm.api_format,
      model_id: mForm.model_id,
      base_url: mForm.base_url,
      api_key: mForm.api_key,
      config,
      max_context_length: mForm.max_context_length || undefined,
    })
    if (identity !== JSON.stringify([mForm.api_format, mForm.model_id, mForm.base_url, mForm.api_key, ...(operation === 'test' ? [mForm.max_context_length, modelOptions.value.reasoning_mode] : [])])) return
    modelTestResult.value = res.data
    if (operation === 'capabilities') {
      const next = applyModelCapabilities(mForm.max_context_length, modelOptions.value, res.data)
      mForm.max_context_length = next.window; modelOptions.value = next.config
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
// 删除模型
async function handleModelDelete(id: string) {
  try {
    await ElMessageBox.confirm('确定要删除这个模型配置吗？', '提示', { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
    await deleteModel(id)
    await loadModels()
    ElMessage.success('删除成功')
  } catch (e: any) {
    if (e === 'cancel' || e === 'close') return
    ElMessage.error(e.message || '删除失败')
  }
}

// 保存模型配置
async function doSave() {
 if(modelSaving.value) return
 try {
  const error = modelSettingsError(mForm.max_context_length,modelOptions.value); if(error) throw new Error(error)
  modelSaving.value = true
  const config = userModelConfig(modelOptions.value)
  if(editId.value) await updateModel(editId.value,{...mForm,config,reconfirm_capabilities:true})
  else await createModel({...mForm,config})
  await loadModels(); showModal.value=false; ElMessage.success('保存成功')
 } catch(e) { ElMessage.error(e instanceof Error ? e.message : '保存失败') } finally {modelSaving.value=false}
}

// 加载当前用户的钉钉绑定状态
async function loadDingTalkBinding() {
  dingTalkBindingLoading.value = true
  dingTalkBindingError.value = ''
  try {
    const res = await getDingTalkBinding()
    dingTalkBinding.value = res.data
  } catch (error) {
    dingTalkBindingError.value = error instanceof Error ? error.message : '钉钉绑定状态加载失败'
  } finally {
    dingTalkBindingLoading.value = false
  }
}

// 打开钉钉扫码绑定弹窗
async function openDingTalkBinding() {
  dingTalkModalVisible.value = true
  dingTalkBindingError.value = ''
  await nextTick()
  await renderDingTalkLoginFrame()
}

// 渲染钉钉扫码组件
async function renderDingTalkLoginFrame() {
  dingTalkQrLoading.value = true
  dingTalkBindingError.value = ''
  try {
    await loadDingTalkScript()
    const res = await getDingTalkOAuthConfig()
    await nextTick()
    const container = document.getElementById('settings-dingtalk-login-frame')
    if (container) container.innerHTML = ''
    if (!window.DTFrameLogin) throw new Error('钉钉扫码组件加载失败')
    window.DTFrameLogin(
      { id: 'settings-dingtalk-login-frame', width: 300, height: 300 },
      {
        redirect_uri: res.data.redirect_uri,
        client_id: res.data.client_id,
        scope: res.data.scope,
        response_type: res.data.response_type,
        prompt: res.data.prompt,
        state: res.data.state,
      },
      async result => {
        await bindDingTalk(result.authCode, result.state || res.data.state)
      },
      message => {
        dingTalkBindingError.value = formatDingTalkLoginError(message)
      },
    )
  } catch (error) {
    dingTalkBindingError.value = error instanceof Error ? error.message : '二维码加载失败'
  } finally {
    dingTalkQrLoading.value = false
  }
}

// 兑换授权码并刷新钉钉绑定状态
async function bindDingTalk(authCode: string, state: string) {
  if (!authCode || !state) {
    dingTalkBindingError.value = '钉钉授权参数不能为空'
    return
  }
  const exchangeKey = `${state}:${authCode}`
  if (dingTalkBindingSubmitting.value || dingTalkBinding.value?.bound || exchangedDingTalkAuthCodes.has(exchangeKey)) return
  exchangedDingTalkAuthCodes.add(exchangeKey)
  dingTalkBindingSubmitting.value = true
  try {
    const res = await exchangeDingTalkAuthCode({ auth_code: authCode, state })
    dingTalkBinding.value = res.data
    dingTalkModalVisible.value = false
    ElMessage.success('钉钉账号已绑定')
  } catch (error) {
    dingTalkBindingError.value = error instanceof Error ? error.message : '钉钉绑定失败'
  } finally {
    dingTalkBindingSubmitting.value = false
  }
}

// 解除当前用户的钉钉账号绑定
async function unbindDingTalk() {
  try {
    await ElMessageBox.confirm('确认解绑当前钉钉账号吗？', '解绑钉钉', {
      confirmButtonText: '解绑',
      cancelButtonText: '取消',
      type: 'warning',
    })
    await deleteDingTalkBinding()
    dingTalkBinding.value = { bound: false }
    ElMessage.success('已解绑钉钉账号')
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') {
      ElMessage.error(error instanceof Error ? error.message : '解绑失败')
    }
  }
}

// 加载钉钉扫码脚本
function loadDingTalkScript() {
  if (window.DTFrameLogin) return Promise.resolve()
  const existing = document.querySelector<HTMLScriptElement>('script[data-dingtalk-login]')
  if (existing) {
    return new Promise<void>((resolve, reject) => {
      existing.addEventListener('load', () => resolve(), { once: true })
      existing.addEventListener('error', () => reject(new Error('钉钉扫码脚本加载失败')), { once: true })
    })
  }
  return new Promise<void>((resolve, reject) => {
    const script = document.createElement('script')
    script.src = 'https://g.alicdn.com/dingding/h5-dingtalk-login/0.21.0/ddlogin.js'
    script.async = true
    script.dataset.dingtalkLogin = 'true'
    script.onload = () => resolve()
    script.onerror = () => reject(new Error('钉钉扫码脚本加载失败'))
    document.head.appendChild(script)
  })
}

// 格式化钉钉扫码组件错误
function formatDingTalkLoginError(message: string) {
  if (message.includes('应用不存在')) {
    return '钉钉应用不存在，请检查应用配置和回调地址'
  }
  return message || '钉钉扫码失败'
}

watch(activeTab, value => {
  if (value === 'sync') loadDingTalkBinding()
})

onMounted(() => { loadModels() })

</script>
