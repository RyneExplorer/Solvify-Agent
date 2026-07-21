<template>
  <el-dialog
    :model-value="modelValue"
    :class="{ 'dingtalk-sync-dialog--qr': !binding?.bound }"
    align-center
    append-to-body
    lock-scroll
    title="同步钉钉知识库"
    width="680px"
    @update:model-value="$emit('update:modelValue', $event)"
    @open="handleOpen"
  >
    <div class="space-y-5">
      <el-alert
        v-if="errorMessage"
        type="error"
        :title="errorMessage"
        :closable="false"
        show-icon
      />

      <section>
        <div class="flex items-center justify-between mb-3">
          <div>
            <div class="text-sm font-semibold text-slate-900">钉钉账号</div>
            <div class="text-xs text-slate-400 mt-1">用于获取你可访问的钉钉知识库</div>
          </div>
          <AppButton v-if="binding?.bound" variant="secondary" size="sm" @click="unbindDingTalk">
            解绑
          </AppButton>
        </div>

        <AppCard v-if="bindingLoading" class="text-sm text-slate-400 text-center py-8">
          正在检查钉钉绑定状态...
        </AppCard>

        <AppCard v-else-if="binding?.bound" class="flex items-center gap-3">
          <img
            v-if="binding.avatar"
            :src="binding.avatar"
            alt=""
            class="w-10 h-10 rounded-full object-cover"
          />
          <div v-else class="w-10 h-10 rounded-full bg-blue-50 text-blue-600 flex items-center justify-center text-sm font-semibold">
            钉
          </div>
          <div class="min-w-0">
            <div class="text-sm font-medium text-slate-900 truncate">{{ binding.nickname || '已绑定钉钉账号' }}</div>
            <div class="text-xs text-slate-400 mt-0.5 truncate">corpId: {{ binding.corp_id || '-' }}</div>
          </div>
        </AppCard>

        <AppCard v-else class="text-center">
          <div class="text-sm font-medium text-slate-900 mb-2">请先绑定钉钉账号</div>
          <div id="dingtalk-login-frame" class="mx-auto w-[300px] h-[300px] border border-slate-100 rounded-xl overflow-hidden bg-slate-50" />
          <div class="mt-4 flex justify-center">
            <AppButton variant="secondary" :disabled="qrLoading" @click="renderLoginFrame">
              {{ qrLoading ? '二维码加载中...' : '刷新二维码' }}
            </AppButton>
          </div>
        </AppCard>
      </section>

      <section v-if="binding?.bound">
        <div class="flex items-center justify-between mb-3">
          <div>
            <div class="text-sm font-semibold text-slate-900">选择钉钉知识库</div>
          </div>
          <AppButton variant="secondary" size="sm" :disabled="workspaceLoading" @click="loadWorkspaces">
            刷新
          </AppButton>
        </div>

        <AppCard v-if="workspaceLoading" class="text-sm text-slate-400 text-center py-8">
          正在加载钉钉知识库...
        </AppCard>

        <AppCard v-else-if="workspaces.length === 0" class="text-sm text-slate-400 text-center py-8">
          暂无可同步的钉钉知识库
        </AppCard>

        <div v-else class="grid gap-3 max-h-[300px] overflow-y-auto pr-1">
          <button
            v-for="workspace in workspaces"
            :key="workspace.workspace_id"
            type="button"
            class="text-left bg-white border rounded-xl p-4 transition-colors"
            :class="selectedWorkspaceID === workspace.workspace_id
              ? 'border-slate-900 ring-1 ring-slate-900'
              : 'border-slate-200 hover:border-slate-300'"
            @click="selectWorkspace(workspace)"
          >
            <div class="flex items-center gap-3">
              <div class="relative w-10 h-10 rounded-xl overflow-hidden bg-blue-50 text-blue-600 flex items-center justify-center text-sm font-semibold shrink-0">
                <span>{{ workspace.name.slice(0, 1) || '钉' }}</span>
                <img
                  v-if="workspaceIconVisible(workspace)"
                  :src="workspace.icon_url"
                  alt=""
                  class="absolute inset-0 w-full h-full object-cover"
                  @error="markWorkspaceIconFailed(workspace.workspace_id)"
                />
              </div>
              <div class="min-w-0">
                <div class="text-sm font-medium text-slate-900 truncate">{{ workspace.name }}</div>
                <div class="text-xs text-slate-400 mt-1">{{ workspace.type || '钉钉知识库' }}</div>
              </div>
            </div>
          </button>
        </div>
      </section>

      <section v-if="selectedWorkspace">
        <div class="text-sm font-semibold text-slate-900 mb-3">同步配置</div>
        <div class="space-y-3">
          <label class="block">
            <span class="block text-sm font-medium text-slate-700 mb-1.5">本地知识库名称</span>
            <input
              v-model="syncName"
              class="w-full px-3 py-2.5 text-sm rounded-xl border border-slate-200 bg-slate-50 text-slate-900 outline-none focus:border-slate-900"
              placeholder="例如：钉钉-产品文档"
            />
          </label>
          <label class="block">
            <span class="block text-sm font-medium text-slate-700 mb-1.5">分类</span>
            <input
              v-model="syncCategory"
              class="w-full px-3 py-2.5 text-sm rounded-xl border border-slate-200 bg-slate-50 text-slate-900 outline-none focus:border-slate-900"
              placeholder="例如：钉钉同步"
            />
          </label>
        </div>
      </section>
    </div>

    <template #footer>
      <div class="flex justify-end gap-2">
        <AppButton variant="secondary" :disabled="submitting" @click="$emit('update:modelValue', false)">取消</AppButton>
        <AppButton :disabled="!canSubmit || submitting" @click="submitSync">
          {{ submitting ? '刷新中...' : '接入并刷新目录' }}
        </AppButton>
      </div>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  deleteDingTalkBinding,
  exchangeDingTalkAuthCode,
  getDingTalkBinding,
  getDingTalkOAuthConfig,
  listDingTalkWorkspaces,
} from '@/api/dingtalk'
import { createKnowledgeBase, listKnowledgeBases } from '@/api/knowledge'
import { createSyncJob, createSyncSource, listSyncSources } from '@/api/sync'
import AppButton from '@/components/ui/AppButton.vue'
import AppCard from '@/components/ui/AppCard.vue'
import type { DingTalkBinding, DingTalkWorkspace } from '@/types/dingtalk'

declare global {
  interface Window {
    DTFrameLogin?: (
      frameParams: { id: string; width?: number; height?: number },
      loginParams: {
        redirect_uri: string
        client_id: string
        scope: string
        response_type: string
        prompt: string
        state?: string
      },
      successCbk: (result: { redirectUrl: string; authCode: string; state?: string }) => void,
      errorCbk?: (errorMsg: string) => void,
    ) => void
  }
}

defineProps<{
  modelValue: boolean
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'synced'): void
}>()

const binding = ref<DingTalkBinding | null>(null)
const workspaces = ref<DingTalkWorkspace[]>([])
const selectedWorkspaceID = ref('')
const syncName = ref('')
const syncCategory = ref('钉钉同步')
const failedWorkspaceIconIDs = ref(new Set<string>())
const bindingLoading = ref(false)
const workspaceLoading = ref(false)
const qrLoading = ref(false)
const bindingSubmitting = ref(false)
const submitting = ref(false)
const errorMessage = ref('')
const exchangedAuthCodes = new Set<string>()

const selectedWorkspace = computed(() => workspaces.value.find(item => item.workspace_id === selectedWorkspaceID.value) || null)

const canSubmit = computed(() => Boolean(binding.value?.bound && selectedWorkspace.value && syncName.value.trim()))

// 打开弹窗后检查绑定状态
async function handleOpen() {
  resetState()
  await loadBinding()
  if (binding.value?.bound) {
    await loadWorkspaces()
  } else {
    await nextTick()
    await renderLoginFrame()
  }
}

// 重置弹窗状态
function resetState() {
  errorMessage.value = ''
  workspaces.value = []
  selectedWorkspaceID.value = ''
  syncName.value = ''
  syncCategory.value = '钉钉同步'
  failedWorkspaceIconIDs.value = new Set()
}

// 加载钉钉绑定状态
async function loadBinding() {
  bindingLoading.value = true
  errorMessage.value = ''
  try {
    const res = await getDingTalkBinding()
    binding.value = res.data
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '钉钉绑定状态加载失败'
  } finally {
    bindingLoading.value = false
  }
}

// 渲染钉钉扫码二维码
async function renderLoginFrame() {
  qrLoading.value = true
  errorMessage.value = ''
  try {
    await loadDingTalkScript()
    const res = await getDingTalkOAuthConfig()
    await nextTick()
    const container = document.getElementById('dingtalk-login-frame')
    if (container) {
      container.innerHTML = ''
    }
    if (!window.DTFrameLogin) {
      throw new Error('钉钉扫码组件加载失败')
    }
    window.DTFrameLogin(
      { id: 'dingtalk-login-frame', width: 300, height: 300 },
      {
        redirect_uri: res.data.redirect_uri,
        client_id: res.data.client_id,
        scope: res.data.scope,
        response_type: res.data.response_type,
        prompt: res.data.prompt,
        state: res.data.state,
      },
      async result => {
        await exchangeAuthCode(result.authCode, result.state || res.data.state)
      },
      message => {
        errorMessage.value = formatDingTalkLoginError(message)
      },
    )
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '二维码加载失败'
  } finally {
    qrLoading.value = false
  }
}

// 兑换授权码并刷新绑定状态
async function exchangeAuthCode(authCode: string, state: string) {
  if (!authCode || !state) {
    errorMessage.value = '钉钉授权参数不能为空'
    return
  }
  const exchangeKey = `${state}:${authCode}`
  if (bindingSubmitting.value || binding.value?.bound || exchangedAuthCodes.has(exchangeKey)) return
  exchangedAuthCodes.add(exchangeKey)
  bindingSubmitting.value = true
  try {
    const res = await exchangeDingTalkAuthCode({ auth_code: authCode, state })
    binding.value = res.data
    ElMessage.success('钉钉账号已绑定')
    await loadWorkspaces()
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '钉钉绑定失败'
  } finally {
    bindingSubmitting.value = false
  }
}

// 解绑钉钉账号
async function unbindDingTalk() {
  try {
    await ElMessageBox.confirm('确认解绑当前钉钉账号吗？', '解绑钉钉', {
      confirmButtonText: '解绑',
      cancelButtonText: '取消',
      type: 'warning',
    })
    await deleteDingTalkBinding()
    binding.value = { bound: false }
    workspaces.value = []
    selectedWorkspaceID.value = ''
    await nextTick()
    await renderLoginFrame()
    ElMessage.success('已解绑钉钉账号')
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') {
      ElMessage.error(err instanceof Error ? err.message : '解绑失败')
    }
  }
}

// 加载钉钉知识库
async function loadWorkspaces() {
  workspaceLoading.value = true
  errorMessage.value = ''
  try {
    const res = await listDingTalkWorkspaces()
    workspaces.value = res.data?.workspaces || []
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '钉钉知识库加载失败'
  } finally {
    workspaceLoading.value = false
  }
}

// 选择钉钉知识库
function selectWorkspace(workspace: DingTalkWorkspace) {
  selectedWorkspaceID.value = workspace.workspace_id
  syncName.value = `钉钉-${workspace.name}`
}

// 判断知识库图标是否可展示
function workspaceIconVisible(workspace: DingTalkWorkspace) {
  return Boolean(workspace.icon_url && !failedWorkspaceIconIDs.value.has(workspace.workspace_id))
}

// 标记加载失败的知识库图标
function markWorkspaceIconFailed(workspaceID: string) {
  const next = new Set(failedWorkspaceIconIDs.value)
  next.add(workspaceID)
  failedWorkspaceIconIDs.value = next
}

// 创建同步知识库并触发任务
async function submitSync() {
  const workspace = selectedWorkspace.value
  if (!workspace) return
  submitting.value = true
  errorMessage.value = ''
  try {
    const sources = await listSyncSources()
    const existingSource = (sources.data || []).find(item => {
      return item.platform === 'dingtalk' && item.source_config.workspace_id === workspace.workspace_id
    })
    if (existingSource) {
      await createSyncJob(existingSource.id)
      ElMessage.success('目录刷新任务已创建')
      emit('synced')
      emit('update:modelValue', false)
      return
    }

    const name = syncName.value.trim()
    const bases = await listKnowledgeBases()
    const duplicateBase = (bases.data || []).find(item => item.name === name)
    if (duplicateBase) {
      errorMessage.value = '本地知识库名称已存在，请修改名称后重试'
      return
    }

    const kb = await createKnowledgeBase({
      name,
      category: syncCategory.value.trim(),
      description: `从钉钉知识库「${workspace.name}」同步`,
    })
    const source = await createSyncSource({
      knowledge_base_id: kb.data.id,
      name: workspace.name,
      platform: 'dingtalk',
      source_config: {
        workspace_id: workspace.workspace_id,
        root_node_id: workspace.root_node_id,
        sync_mode: 'full',
      },
    })
    await createSyncJob(source.data.id)
    ElMessage.success('目录刷新任务已创建')
    emit('synced')
    emit('update:modelValue', false)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '创建同步任务失败'
  } finally {
    submitting.value = false
  }
}

// 加载钉钉扫码 SDK
function loadDingTalkScript() {
  if (window.DTFrameLogin) {
    return Promise.resolve()
  }
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
    return '钉钉应用不存在，请检查 AppKey 是否为当前企业内部应用的 Client ID、应用是否已发布、登录与分享/安全设置中是否配置了当前 redirect_uri'
  }
  return message || '钉钉扫码失败'
}

</script>

<style>
.dingtalk-sync-dialog--qr {
  margin: 0;
  max-height: calc(100vh - 32px);
  overflow: hidden;
}

.dingtalk-sync-dialog--qr .el-dialog__body {
  overflow: hidden;
}
</style>
