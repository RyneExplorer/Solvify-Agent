<template>
  <div class="flex flex-col h-full bg-white">
    <!-- 顶部栏 -->
    <div class="h-14 flex items-center justify-between px-6 border-b border-gray-100 flex-shrink-0">
      <h2 class="text-sm font-semibold text-gray-800">{{ displayTitle }}</h2>
      <div class="flex items-center gap-1.5">
        <span class="w-1.5 h-1.5 rounded-full" :class="connected ? 'bg-emerald-500' : 'bg-gray-300'" />
        <span class="text-xs text-gray-400">{{ connected ? '就绪' : '加载中...' }}</span>
      </div>
    </div>

    <!-- 消息区域 -->
    <div ref="chatContainer" class="flex-1 overflow-y-auto px-6 py-6">
      <div class="max-w-[800px] mx-auto space-y-6">
        <div v-for="(msg, i) in messages" :key="msg.id || i"
          class="flex"
          :class="msg.role === 'user' ? 'justify-end' : 'justify-start'"
        >
          <div class="max-w-[80%]">
            <!-- 消息气泡 -->
            <div
              :class="[
                'px-4 py-3 rounded-2xl text-sm leading-relaxed whitespace-pre-wrap',
                msg.role === 'user'
                  ? 'bg-gray-100 text-gray-800'
                  : 'bg-white text-gray-800'
              ]"
            >
              <!-- 推理时间线（深度模式 - 流式/历史） -->
              <div v-if="msg.role === 'assistant' && msg.timeline?.length" class="mb-3">
                <button
                  @click="collapsedTimelines.has(i) ? collapsedTimelines.delete(i) : collapsedTimelines.add(i)"
                  class="flex items-center gap-1.5 text-xs text-slate-400 hover:text-slate-600 mb-1 cursor-pointer"
                >
                  <svg
                    class="w-3 h-3 transition-transform"
                    :class="{ '-rotate-90': collapsedTimelines.has(i) }"
                    fill="none" stroke="currentColor" viewBox="0 0 24 24"
                  ><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/></svg>
                  <span>推理步骤 ({{ msg.timeline.length }})</span>
                </button>
                <div v-show="!collapsedTimelines.has(i)">
                  <div v-for="(step, si) in msg.timeline" :key="si" class="flex items-start gap-2 py-1 text-xs">
                    <span v-if="step.status === 'running'" class="w-3 h-3 mt-0.5 border-2 border-gray-200 border-t-emerald-500 rounded-full animate-spin flex-shrink-0"></span>
                    <span v-else class="w-3 h-3 mt-0.5 text-emerald-500 flex-shrink-0">✓</span>
                    <span class="text-gray-500">{{ step.title || step.content }}</span>
                  </div>
                </div>
              </div>

              <!-- 内容 -->
              <div v-if="msg.role === 'assistant'" v-html="formatContent(msg.content, msg.sources)"></div>
              <template v-else>{{ msg.content }}</template>
            </div>

            <!-- AI 消息操作按钮 -->
            <div v-if="msg.role === 'assistant'" class="flex items-center gap-1 mt-2">
              <button class="p-1.5 rounded-md hover:bg-slate-100 text-slate-400" title="复制" @click="copyText(msg.content)">
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"/>
                </svg>
              </button>
              <button class="p-1.5 rounded-md hover:bg-slate-100 text-slate-400" title="重新生成" @click="regenerate">
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"/>
                </svg>
              </button>
            </div>

            <!-- 来源 -->
            <div v-if="msg.role === 'assistant' && (msg.sources?.length || extractWebCount(msg.content))" class="mt-2 flex flex-wrap items-center gap-1.5">
              <template v-if="msg.sources?.filter((s: any) => s?.title).length">
                <span class="text-[11px] text-gray-400">知识库:</span>
                <span
                  v-for="(s, si) in msg.sources.filter((s: any) => s?.title)"
                  :key="si"
                  :data-chunk-ids="getSourceChunkIds(s)"
                  :data-doc="cleanTitle(s.title)"
                  class="text-[11px] px-2 py-0.5 bg-gray-100 border border-gray-200 rounded-full text-gray-500 cursor-help hover:bg-gray-200 transition-colors"
                >{{ cleanTitle(s.title) }}</span>
              </template>
              <template v-if="extractWebSources(msg.content).length">
                <span class="text-[11px] text-gray-400">联网搜索:</span>
                <a v-for="(ws, wi) in extractWebSources(msg.content)" :key="'w'+wi"
                  :href="ws.url" target="_blank" :title="ws.title"
                  class="text-[10px] px-1.5 py-0.5 bg-blue-50 border border-blue-200 rounded-full text-blue-500 no-underline hover:bg-blue-100">🔗{{ wi + 1 }}</a>
              </template>
            </div>

            <!-- 错误 -->
            <div v-if="msg.role === 'error'" class="mt-2 p-3 bg-red-50 border border-red-200 rounded-lg">
              <div class="flex items-start gap-2">
                <svg class="w-4 h-4 text-red-500 mt-0.5 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/>
                </svg>
                <div class="flex-1">
                  <div class="text-sm font-medium text-red-800">{{ msg.content }}</div>
                  <div v-if="msg.detail" class="text-xs text-red-600 mt-1">{{ msg.detail }}</div>
                  <button
                    v-if="msg.retryable"
                    @click="retryMessage(msg)"
                    class="mt-2 px-3 py-1 text-xs bg-red-100 hover:bg-red-200 text-red-700 rounded-md transition-colors"
                  >
                    重试
                  </button>
                </div>
              </div>
            </div>
          </div>
        </div>

        <!-- 加载中 -->
        <div v-if="isLoading" class="flex justify-start">
          <div class="max-w-[80%]">
            <div class="px-4 py-3 rounded-2xl bg-white text-sm">
              <!-- 时间线 -->
              <div v-if="streamTimeline.length" class="mb-3 space-y-1">
                <div v-for="(step, si) in streamTimeline" :key="si" class="flex items-start gap-2 py-0.5 text-xs">
                  <span v-if="step.status === 'running'" class="w-3 h-3 mt-0.5 border-2 border-gray-200 border-t-emerald-500 rounded-full animate-spin flex-shrink-0"></span>
                  <span v-else class="w-3 h-3 mt-0.5 text-emerald-500 flex-shrink-0">✓</span>
                  <span :class="step.status === 'running' ? 'text-gray-500' : 'text-emerald-600'">{{ step.title || step.content }}</span>
                </div>
              </div>
              <!-- 进度 -->
              <div v-if="progressText" class="flex items-center gap-2 mb-2 text-xs text-gray-400">
                <span class="w-3 h-3 border-2 border-gray-200 border-t-emerald-500 rounded-full animate-spin"></span>
                {{ progressText }}
              </div>
              <!-- 等待动画 -->
              <div v-if="!streamContent" class="flex gap-1 py-1">
                <span class="w-1 h-1 rounded-full bg-gray-300 animate-bounce" style="animation-delay:0s"></span>
                <span class="w-1 h-1 rounded-full bg-gray-300 animate-bounce" style="animation-delay:0.15s"></span>
                <span class="w-1 h-1 rounded-full bg-gray-300 animate-bounce" style="animation-delay:0.3s"></span>
              </div>
              <!-- 流式内容 -->
              <div v-if="streamContent" class="leading-relaxed whitespace-pre-wrap" v-html="formatContent(streamContent, streamSources)"></div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 输入区域 -->
    <div class="px-6 py-4 flex-shrink-0">
      <div class="max-w-[800px] mx-auto">
        <div class="bg-white rounded-2xl border border-gray-200 shadow-sm overflow-hidden">
          <!-- 文本框 -->
          <div class="relative">
            <textarea
              v-model="input"
              @keydown="handleKeyDown"
              @input="autoResize"
              placeholder="请输入您想要咨询的问题或需要帮助的内容..."
              rows="3"
              class="w-full px-5 py-4 text-sm text-gray-700 placeholder-gray-300 resize-none outline-none border-none"
            />
          </div>

          <!-- 工具栏 -->
          <div class="flex items-center justify-between px-4 py-3 border-t border-gray-50">
            <!-- 左侧工具 -->
            <div class="flex items-center gap-2">
              <!-- 知识库选择器 -->
              <div class="relative" ref="kbWrapperRef">
                <button @click="showKbDropdown = !showKbDropdown"
                  class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs text-gray-500 hover:bg-gray-50 transition-colors border border-gray-200">
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6.253v13m0-13C10.832 5.477 9.246 5 7.5 5S4.168 5.477 3 6.253v13C4.168 18.477 5.754 18 7.5 18s3.332.477 4.5 1.253m0-13C13.168 5.477 14.754 5 16.5 5c1.747 0 3.332.477 4.5 1.253v13C19.832 18.477 18.247 18 16.5 18c-1.746 0-3.332.477-4.5 1.253"/>
                  </svg>
                  <span class="max-w-[100px] truncate">{{ kbTriggerText }}</span>
                  <svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/>
                  </svg>
                </button>
                <div v-if="showKbDropdown" class="absolute bottom-full left-0 mb-1 w-64 bg-white border border-gray-200 rounded-xl shadow-lg max-h-[200px] overflow-y-auto z-50">
                  <div v-if="!knowledgeBases.length" class="px-4 py-3 text-xs text-gray-400 text-center">加载中...</div>
                  <div v-for="kb in knowledgeBases" :key="kb.id" @click="toggleKB(kb.id)"
                    class="px-3.5 py-2.5 text-[13px] cursor-pointer hover:bg-gray-50 flex items-center gap-2.5"
                    :class="{ 'text-emerald-600': selectedKBs.includes(kb.id) }">
                    <span class="w-4 h-4 border border-gray-200 rounded flex items-center justify-center flex-shrink-0"
                      :class="{ 'bg-emerald-500 border-emerald-500': selectedKBs.includes(kb.id) }">
                      <svg v-if="selectedKBs.includes(kb.id)" class="w-2.5 h-2.5 text-white" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><path d="M20 6L9 17l-5-5"/></svg>
                    </span>
                    {{ kb.name }}
                  </div>
                </div>
              </div>

              <!-- 搜索模式 -->
              <div class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs text-gray-500 hover:bg-gray-50 transition-colors border border-gray-200 cursor-pointer">
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z"/>
                </svg>
                <select v-model="searchMode" class="bg-transparent outline-none text-xs text-gray-500 cursor-pointer pr-4 appearance-none">
                  <option value="quick">快速检索</option>
                  <option value="smart-reasoning">深度模式</option>
                </select>
                <svg class="w-3 h-3 -ml-5 pointer-events-none" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/>
                </svg>
              </div>

              <!-- 模型选择器 -->
              <div class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs text-gray-500 hover:bg-gray-50 transition-colors border border-gray-200 cursor-pointer">
                <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z"/>
                </svg>
                <select v-model="selectedModel" class="bg-transparent outline-none text-xs text-gray-500 cursor-pointer pr-4 appearance-none">
                  <optgroup v-if="systemModels.length" label="系统模型">
                    <option v-for="m in systemModels" :key="m.id" :value="m.id">{{ m.provider }} / {{ m.model_id }}</option>
                  </optgroup>
                  <optgroup v-if="userModels.length" label="我的模型">
                    <option v-for="m in userModels" :key="m.id" :value="m.id">{{ m.display_name || m.model_id }}</option>
                  </optgroup>
                  <option v-if="!systemModels.length && !userModels.length" value="">加载中...</option>
                </select>
                <svg class="w-3 h-3 -ml-5 pointer-events-none" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/>
                </svg>
              </div>
            </div>

            <!-- 右侧发送/停止按钮 -->
            <button
              v-if="isLoading"
              @click="stopGeneration"
              class="w-9 h-9 rounded-full flex items-center justify-center transition-all bg-red-500 hover:bg-red-600 text-white shadow-md"
              title="停止生成"
            >
              <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 24 24">
                <rect x="6" y="6" width="12" height="12" rx="2"/>
              </svg>
            </button>
            <button
              v-else
              @click="sendMessage"
              :disabled="!input.trim()"
              :class="[
                'w-9 h-9 rounded-full flex items-center justify-center transition-all',
                input.trim()
                  ? 'bg-gray-800 hover:bg-gray-700 text-white shadow-md'
                  : 'bg-gray-100 text-gray-300 cursor-not-allowed'
              ]"
            >
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M5 10l7-7m0 0l7 7m-7-7v18"/>
              </svg>
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- 底部声明 -->
    <div class="text-center pb-3">
      <span class="text-xs text-gray-300">内容由 AI 生成，仅供参考</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { ElMessage } from 'element-plus'
import { marked } from 'marked'
import { useMarkdownTooltip } from '@/composables/useMarkdownTooltip'
import { nextTipKey, setTooltip, getSourceChunkIds } from '@/composables/useChat'
import { getToken } from '@/api/client'

// ── Props ──
const props = defineProps<{ title: string }>()

// ── 配置（默认值，可后续做成可配置） ──
const API_URL = 'http://localhost:8080'

// ── UI 状态 ──
const input = ref('')
const selectedModel = ref('')
const selectedKBs = ref<string[]>([])
const searchMode = ref<'quick' | 'smart-reasoning'>('quick')
const showKbDropdown = ref(false)
const kbWrapperRef = ref<HTMLDivElement>()
const chatContainer = ref<HTMLDivElement>()

// ── 数据状态 ──
const systemModels = ref<{ id: string; provider: string; model_id: string }[]>([])
const userModels = ref<{ id: string; display_name?: string; model_id?: string }[]>([])
const knowledgeBases = ref<{ id: string; name: string }[]>([])
const connected = ref(false)

// ── 聊天状态 ──
// 聊天消息结构
interface Message {
  id?: string
  role: 'user' | 'assistant' | 'error'
  content: string
  detail?: string
  retryable?: boolean
  sources?: any[]
  timeline?: any[]
}
const messages = ref<Message[]>([])
const activeSessionId = ref<string | null>(null)
const isLoading = ref(false)
const progressText = ref('')
const streamContent = ref('')
const streamTimeline = ref<any[]>([])
const streamSources = ref<any[]>([])
const collapsedTimelines = ref<Set<number>>(new Set())
let abortController: AbortController | null = null

// ── 计算属性 ──
const displayTitle = computed(() => props.title || '智能问答')
const kbTriggerText = computed(() => {
  if (!selectedKBs.value.length) return '全部知识库'
  return selectedKBs.value.map(id => knowledgeBases.value.find(k => k.id === id)?.name || id).join(', ')
})

// ── API 请求 ──
// 通用 API 请求封装
async function api(path: string, method = 'GET', body: any = null): Promise<any> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' }
  const token = getToken()
  if (token) {
    headers.Authorization = `Bearer ${token}`
  }

  const res = await fetch(API_URL + path, {
    method,
    headers,
    ...(body ? { body: JSON.stringify(body) } : {}),
  })
  if (!res.ok) {
    const text = await res.text().catch(() => '')
    throw new Error(text || `HTTP ${res.status}`)
  }
  return res.json()
}

// ── 初始化 ──
// 初始化模型和知识库
async function init() {
  try {
    const [modelsRes, userModelsRes, kbRes, profileRes] = await Promise.all([
      api('/api/v1/models').catch(() => null),
      api('/api/v1/user/model-configs').catch(() => null),
      api('/api/v1/knowledge-bases').catch(() => null),
      api('/api/v1/user/profile').catch(() => null),
    ])
    if (modelsRes?.code === 0) systemModels.value = modelsRes.data?.models || []
    if (userModelsRes?.code === 0) userModels.value = userModelsRes.data?.models || []
    if (kbRes?.code === 0) knowledgeBases.value = Array.isArray(kbRes.data) ? kbRes.data : (kbRes.data?.list || [])

    // 恢复上次选择的模型，优先使用用户保存的 lastModel，必须在可选模型列表内
    const availableIds = new Set([
      ...systemModels.value.map((m) => m.id),
      ...userModels.value.map((m) => m.id),
    ])
    const lastModel = profileRes?.data?.user?.lastModel
    if (lastModel && availableIds.has(lastModel)) {
      selectedModel.value = lastModel
    } else if (systemModels.value[0]?.id) {
      selectedModel.value = systemModels.value[0].id
    } else if (userModels.value[0]?.id) {
      selectedModel.value = userModels.value[0].id
    }

    connected.value = true
  } catch {
    connected.value = false
  }
}

onMounted(() => {
  init()
  document.addEventListener('click', onDocClick)
})

onUnmounted(() => {
  document.removeEventListener('click', onDocClick)
})

// 文档点击事件处理（关闭知识库下拉）
function onDocClick(e: MouseEvent) {
  if (kbWrapperRef.value && !kbWrapperRef.value.contains(e.target as Node)) {
    showKbDropdown.value = false
  }
}

// ── 知识库 ──
// 切换知识库选中状态
function toggleKB(id: string) {
  const idx = selectedKBs.value.indexOf(id)
  if (idx >= 0) selectedKBs.value.splice(idx, 1)
  else selectedKBs.value.push(id)
}

// ── 发送消息 ──
// 发送消息（SSE 流式）
async function sendMessage() {
  const content = input.value.trim()
  if (!content || isLoading.value) return

  if (!selectedModel.value) {
    ElMessage.warning('请先选择一个模型')
    return
  }

  if (!selectedKBs.value.length) {
    ElMessage.warning('请至少选择一个知识库')
    return
  }

  input.value = ''
  messages.value.push({ id: 'u-' + Date.now(), role: 'user', content })
  isLoading.value = true
  progressText.value = ''
  streamContent.value = ''
  streamTimeline.value = []
  streamSources.value = []
  scrollToBottom()

  let assistantId: string | null = null
  let finalContent = ''
  let finalSources: any[] = []
  let finalTimeline: any[] = []

  // 自动创建会话
  if (!activeSessionId.value) {
    try {
      const res = await api('/api/v1/chat/sessions', 'POST', {
        title: content.substring(0, 30),
        model_id: selectedModel.value,
      })
      if (res.code === 0 && res.data) activeSessionId.value = res.data.id
      else { throw new Error('创建会话失败') }
    } catch (e: any) {
      messages.value.push({ id: 'e-' + Date.now(), role: 'error', content: e.message })
      isLoading.value = false; return
    }
  }

  const modelType = userModels.value.find(m => m.id === selectedModel.value) ? 'user' : 'system'
  abortController = new AbortController()
  const safetyTimer = setTimeout(() => {
    if (isLoading.value) { isLoading.value = false; abortController?.abort(); messages.value.push({ role: 'error', content: '响应超时' }) }
  }, 120000)

  try {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    const token = getToken()
    if (token) {
      headers.Authorization = `Bearer ${token}`
    }

    const res = await fetch(API_URL + `/api/v1/chat/sessions/${activeSessionId.value}/messages`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ content, model_id: selectedModel.value, model_type: modelType, search_mode: searchMode.value, knowledge_base_ids: selectedKBs.value }),
      signal: abortController.signal,
    })
    if (!res.ok) throw new Error(await res.text().catch(() => `HTTP ${res.status}`))

    const reader = res.body!.getReader()
    const decoder = new TextDecoder()
    let buffer = ''

    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''

      for (const line of lines) {
        if (!line.startsWith('data: ')) continue
        try {
          const data = JSON.parse(line.slice(6))

          if (data.type === 'start') {
            assistantId = data.message_id; finalSources = data.sources || []
          } else if (data.type === 'progress') {
            progressText.value = data.content || ''
          } else if (['plan', 'thinking', 'tool_call', 'tool_result', 'warning'].includes(data.type)) {
            // 标记前一步完成
            for (const item of streamTimeline.value) { if (item.status === 'running') item.status = 'success' }
            if (data.type !== 'thinking' || data.status !== 'success') {
              streamTimeline.value.push({ title: data.title || data.content || '', detail: data.detail || '', status: data.status || 'running' })
            }
            progressText.value = ''
            scrollToBottom()
          } else if (data.type === 'sources') {
            if (data.sources?.length) finalSources = streamSources.value = data.sources
          } else if (data.type === 'content' || data.type === 'answer') {
            if (!streamContent.value) { progressText.value = ''; for (const item of streamTimeline.value) { if (item.status === 'running') item.status = 'success' } }
            finalContent += data.content || ''
            streamContent.value = finalContent
            scrollToBottom()
          } else if (data.type === 'error') {
            isLoading.value = false
            messages.value.push({
              role: 'error',
              content: data.title || data.error || '未知错误',
              detail: data.detail,
              retryable: data.retryable,
            })
            return
          } else if (data.type === 'done') {
            for (const item of streamTimeline.value) { if (item.status === 'running') item.status = 'success' }
            if (data.sources?.length) {
              finalSources = data.sources
              streamSources.value = data.sources
            }
            finalTimeline = [...streamTimeline.value]

            messages.value.push({
              id: assistantId || 'a-' + Date.now(),
              role: 'assistant',
              content: finalContent,
              sources: finalSources,
              timeline: finalTimeline.length ? finalTimeline : undefined,
            })
            if (finalTimeline.length) {
              collapsedTimelines.value.add(messages.value.length - 1)
            }

            isLoading.value = false; streamContent.value = ''; streamTimeline.value = []; streamSources.value = []
            progressText.value = ''
            return
          }
        } catch { /* 跳过非法 JSON */ }
      }
    }
  } catch (e: any) {
    if (e instanceof DOMException && e.name === 'AbortError') {
      if (finalContent || streamContent.value || streamTimeline.value.length || streamSources.value?.length) {
        messages.value.push({
          id: assistantId || 'a-' + Date.now(),
          role: 'assistant',
          content: streamContent.value || finalContent,
          sources: finalSources.length ? finalSources : (streamSources.value?.length ? [...streamSources.value] : undefined),
          timeline: streamTimeline.value.length ? [...streamTimeline.value] : undefined,
        })
      }
    } else {
      messages.value.push({ role: 'error', content: e.message })
    }
  } finally {
    clearTimeout(safetyTimer)
    abortController = null
    isLoading.value = false; streamContent.value = ''; streamTimeline.value = []; progressText.value = ''
  }

  scrollToBottom()
}

// ── 辅助方法 ──
// 滚动到底部
function scrollToBottom() {
  nextTick(() => {
    if (chatContainer.value) chatContainer.value.scrollTop = chatContainer.value.scrollHeight
  })
}

// 文本框自适应高度
function autoResize(e: Event) {
  const el = e.target as HTMLTextAreaElement
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 200) + 'px'
}

// 键盘事件处理（回车发送）
function handleKeyDown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); sendMessage() }
}

// 复制文本
function copyText(text: string) {
  navigator.clipboard.writeText(text)
    .then(() => ElMessage.success('已复制'))
    .catch(() => ElMessage.error('复制失败'))
}

// 重新生成
function regenerate() {
  // 删除最后一条 AI 回复并重新发送
  const lastIdx = messages.value.length - 1
  if (lastIdx >= 0 && messages.value[lastIdx].role === 'assistant') {
    messages.value.splice(lastIdx, 1)
  }
  // 查找最后一条用户消息内容
  const lastUser = [...messages.value].reverse().find(m => m.role === 'user')
  if (lastUser) {
    // 删除原用户消息，避免 sendMessage 重复添加
    const userIdx = messages.value.lastIndexOf(lastUser)
    if (userIdx >= 0) {
      messages.value.splice(userIdx, 1)
    }
    input.value = lastUser.content
    sendMessage()
  }
}

// 重试错误消息
function retryMessage(errorMsg: Message) {
  // 找到错误消息的索引
  const errorIdx = messages.value.indexOf(errorMsg)
  if (errorIdx < 0) return

  // 删除错误消息
  messages.value.splice(errorIdx, 1)

  // 找到最后一条用户消息并重试
  const lastUser = [...messages.value].reverse().find(m => m.role === 'user')
  if (lastUser) {
    // 删除原用户消息，避免 sendMessage 重复添加
    const userIdx = messages.value.lastIndexOf(lastUser)
    if (userIdx >= 0) {
      messages.value.splice(userIdx, 1)
    }
    input.value = lastUser.content
    sendMessage()
  }
}

// 中止生成
function stopGeneration() {
  if (abortController) {
    abortController.abort()
  }
}

// ── 内容格式化 ──
// 从内容中提取网页来源
function extractWebSources(content: string): { url: string; title: string }[] {
  const result: { url: string; title: string }[] = []
  const seen = new Set<string>()
  const re = /<web\s+(?:(?:url|title)="[^"]*"\s*)+\/>/g
  let m
  while ((m = re.exec(String(content))) !== null) {
    const urlMatch = m[0].match(/url="([^"]*)"/)
    const titleMatch = m[0].match(/title="([^"]*)"/)
    const url = urlMatch?.[1] ?? ''
    if (url && !seen.has(url)) { seen.add(url); result.push({ url, title: titleMatch?.[1] ?? url }) }
  }
  return result
}

// 获取网页来源数量
function extractWebCount(content: string): number {
  return extractWebSources(content).length
}

useMarkdownTooltip()

// 格式化消息内容
function formatContent(content: string, _sources?: unknown[]): string {
  if (!content) return ''

  // 提取并替换 <kb>/<web> 标签为引用
  const cites: { type: string; doc?: string; chunkId?: string; url?: string; title?: string }[] = []
  let processed = String(content)

  processed = processed.replace(/<kb\s+doc="([^"]*)"\s+chunk_id="([^"]*)"\s*\/>/g, (_, doc, chunkId) => {
    const idx = cites.length
    cites.push({ type: 'kb', doc, chunkId })
    return `%%CITE${idx}%%`
  })
  processed = processed.replace(/<web\s+(?:(?:url|title)="[^"]*"\s*)+\/>/g, (match) => {
    const urlMatch = match.match(/url="([^"]*)"/)
    const titleMatch = match.match(/title="([^"]*)"/)
    const idx = cites.length
    cites.push({ type: 'web', url: urlMatch?.[1] ?? '', title: titleMatch?.[1] ?? '' })
    return `%%CITE${idx}%%`
  })

  let html = marked.parse(processed, { breaks: true, gfm: true }) as string

  const urlToNumMap = new Map<string, number>()
  let webNum = 0
  for (let i = 0; i < cites.length; i++) {
    const c = cites[i]
    let citeHtml
    if (c.type === 'kb') {
      const safeDoc = escapeHtml(cleanTitle(c.doc || ''))
      const chunkId = c.chunkId || ''
      const docTitle = escapeAttr(c.doc || '')
      citeHtml = `<span class="inline-cite-kb" data-chunk-id="${escapeAttr(chunkId)}" data-doc="${docTitle}">📄${safeDoc}</span>`
    } else if (c.type === 'web') {
      const url = c.url || ''
      let num = urlToNumMap.get(url)
      if (num === undefined) {
        webNum++
        num = webNum
        urlToNumMap.set(url, num)
      }
      const title = c.title || '网页链接'
      const tipKey = nextTipKey()
      setTooltip(tipKey, title)
      const safeUrl = escapeAttr(url)
      citeHtml = `<a class="inline-cite-web" href="${safeUrl}" target="_blank" rel="noopener" data-tip-key="${tipKey}">[${num}]</a>`
    }
    html = html.replace(`%%CITE${i}%%`, citeHtml!)
  }

  return html
}

// HTML 转义
function escapeHtml(s: string) { const d = document.createElement('div'); d.textContent = s; return d.innerHTML }
// HTML 属性转义
function escapeAttr(s: string) { return s.replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/\n/g,'&#10;') }
// 数字转圆圈数字
function toCircleNum(n: number) { const c = '①②③④⑤⑥⑦⑧⑨⑩⑪⑫⑬⑭⑯⑰⑱⑲⑳'; return n >= 1 && n <= 20 ? c[n-1] : `[${n}]` }
// 清理 tooltip 文本
function cleanTooltipText(text: string): string {
  if (!text) return ''
  // 仅移除实际的元数据标签，不要全局清除 `title=` / `doc=`
  // 因为正常文章文本中可能合法包含这些子串
  return text
    .replace(/<kb(?:\s[^>]*)?\s*\/?>\s*/gi, '')
    .replace(/<web(?:\s[^>]*)?\s*\/?>\s*/gi, '')
    .replace(/<\/?kb>/gi, '')
    .replace(/<\/?web>/gi, '')
    .replace(/\r\n/g, '\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}

// 清理标题换行
function cleanTitle(text: string): string {
  if (!text) return ''
  return text.replace(/[\r\n]+/g, ' ').trim()
}
</script>

<style>
/* Markdown 内容样式 */
.md-content h1, .md-content h2, .md-content h3 { color: #1e293b; font-weight: 600; margin: 16px 0 8px; }
.md-content h1:first-child, .md-content h2:first-child, .md-content h3:first-child { margin-top: 0; }
.md-content p { margin: 0 0 8px; line-height: 1.7; }
.md-content p:last-child { margin-bottom: 0; }
.md-content ul, .md-content ol { margin: 0 0 8px; padding-left: 20px; }
.md-content li { margin-bottom: 2px; line-height: 1.7; }
.md-content strong { color: #0f172a; font-weight: 600; }
.md-content code { background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 4px; padding: 1px 6px; font-size: 13px; color: #059669; }
.md-content pre { background: #f8fafc; border: 1px solid #e2e8f0; border-radius: 10px; padding: 14px; margin: 0 0 8px; overflow-x: auto; font-size: 13px; }
.md-content pre code { background: none; border: none; padding: 0; color: #0f172a; }
.md-content blockquote { border-left: 3px solid #10b981; padding: 6px 14px; margin: 0 0 8px; background: #ecfdf5; border-radius: 0 8px 8px 0; color: #475569; }
.md-content table { width: 100%; border-collapse: collapse; margin: 0 0 8px; font-size: 13px; }
.md-content th, .md-content td { border: 1px solid #e2e8f0; padding: 6px 10px; text-align: left; }
.md-content th { background: #f8fafc; font-weight: 500; }

/* 行内引用样式 */
.inline-cite-kb {
  display: inline-flex; align-items: center; gap: 3px; padding: 0 5px;
  background: rgba(16,185,129,0.08); border: 1px solid rgba(16,185,129,0.2); border-radius: 4px;
  font-size: 11px; color: #059669; cursor: help;
}
.inline-cite-kb:hover {
  background: rgba(16,185,129,0.15);
}
.inline-cite-web {
  display: inline-flex; align-items: center; justify-content: center;
  min-width: 18px; height: 16px; padding: 0 4px;
  background: rgba(59,130,246,0.08); border: 1px solid rgba(59,130,246,0.2); border-radius: 4px;
  font-size: 11px; font-weight: 500; color: #3b82f6; text-decoration: none;
  cursor: pointer; vertical-align: middle;
}
.inline-cite-web:hover {
  background: rgba(59,130,246,0.15);
}
</style>
