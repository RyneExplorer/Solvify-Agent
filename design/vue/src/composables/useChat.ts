import { ref, computed, nextTick, inject } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { marked } from 'marked'
import * as chatApi from '@/api/chat'
import * as modelApi from '@/api/model'
import * as authApi from '@/api/auth'
import { request } from '@/api/client'
import type { ChatSession, FeedbackRequest } from '@/types/chat'
import type { StreamEvent } from '@/types/chat'

// ── UI 展示用的本地类型 ──

// 推理时间线步骤
interface TimelineStep {
  title: string
  detail?: string
  status: string
}

// 聊天消息展示结构
interface DisplayMessage {
  id: string
  role: 'user' | 'assistant' | 'error'
  content: string
  detail?: string
  retryable?: boolean
  sources?: StreamEvent['sources']
  timeline?: TimelineStep[]
  trace_id?: string
  feedback_rating?: 1 | -1
  feedback_reasons?: string[]
  feedback_comment?: string
}

// 模型选项
interface ModelOption {
  id: string
  name: string
  modelType: 'system' | 'user'
}

// 知识库选项
interface KnowledgeBaseOption {
  id: string
  name: string
  document_count?: number
}

// ── Tooltip 内容存储（避免 HTML 属性转义问题） ──

const tooltipStore = new Map<string, string>()

// 获取指定 key 的 tooltip 内容
export function getTooltipContent(key: string): string {
  return tooltipStore.get(key) ?? ''
}

let _tooltipSeq = 0
// 生成下一个 tooltip key
export function nextTipKey(): string {
  return `tip_${_tooltipSeq++}`
}

// 设置 tooltip 内容
export function setTooltip(key: string, value: string): void {
  tooltipStore.set(key, value)
}

// 清理标题中的换行
export function cleanTitle(text: string): string {
  if (!text) return ''
  return text.replace(/[\r\n]+/g, ' ').trim()
}

// 聊天组合式函数
export function useChat() {
  const router = useRouter()
  const refreshHistory = inject<() => Promise<void>>('refreshHistory')

  // ── 会话状态 ──
  const sessions = ref<ChatSession[]>([])
  const activeSessionId = ref<string | null>(null)

  // ── 消息状态 ──
  const messages = ref<DisplayMessage[]>([])
  const collapsedTimelines = ref<Set<number>>(new Set())
  const isLoading = ref(false)
  const streamContent = ref('')
  const streamSources = ref<DisplayMessage['sources']>([])
  const streamTimeline = ref<TimelineStep[]>([])
  const progressText = ref('')

  // ── 选择器 ──
  const modelOptions = ref<ModelOption[]>([])
  const knowledgeBases = ref<KnowledgeBaseOption[]>([])
  const connected = ref(false)

  // ── 输入状态 ──
  const input = ref('')
  const selectedModel = ref('')
  const selectedKBs = ref<string[]>([])
  const searchMode = ref<'quick' | 'smart-reasoning'>('quick')

  // ── 中断控制 ──
  let abortController: AbortController | null = null

  // ── 计算属性 ──
  const activeSession = computed(() =>
    sessions.value.find((s) => s.id === activeSessionId.value),
  )

  const kbTriggerText = computed(() => {
    if (!selectedKBs.value.length) return '全部知识库'
    return selectedKBs.value
      .map((id) => knowledgeBases.value.find((k) => k.id === id)?.name ?? id)
      .join(', ')
  })

  // ── 初始化 ──
  async function init() {
    try {
      const [modelsRes, userModelsRes, kbRes, profileRes] = await Promise.all([
        modelApi.listModels().catch(() => null),
        modelApi.listUserModelConfigs().catch(() => null),
        request<{ data: unknown }>('/knowledge-bases').catch(() => null),
        authApi.getProfile().catch(() => null),
      ])

      const opts: ModelOption[] = []
      if (modelsRes?.code === 0) {
        for (const m of modelsRes.data.models ?? []) {
          opts.push({ id: m.id, name: `${m.provider} / ${m.model_id}`, modelType: 'system' })
        }
      }
      if (userModelsRes?.code === 0) {
        for (const m of userModelsRes.data.models ?? []) {
          opts.push({
            id: m.id,
            name: m.display_name || m.model_id,
            modelType: 'user',
          })
        }
      }
      modelOptions.value = opts

      // 使用用户上次选择的模型（lastModel 嵌套在 user 对象里）
      if (profileRes?.code === 0 && profileRes.data?.user?.lastModel) {
        selectedModel.value = profileRes.data.user.lastModel
      } else if (opts.length > 0) {
        selectedModel.value = opts[0].id
      }

      if (kbRes?.code === 0) {
        const data = kbRes.data as { data?: unknown }
        const raw = data.data ?? kbRes.data
        if (Array.isArray(raw)) {
          knowledgeBases.value = raw as KnowledgeBaseOption[]
        } else if (raw && typeof raw === 'object' && 'list' in raw) {
          knowledgeBases.value = (raw as { list: KnowledgeBaseOption[] }).list
        }
        selectedKBs.value = knowledgeBases.value.map(k => k.id)
      }
      connected.value = true
    } catch {
      connected.value = false
    }
  }

  // ── 会话操作 ──
  // 加载会话列表
  async function loadSessions() {
    try {
      const res = await chatApi.listSessions()
      if (res.code === 0) sessions.value = res.data.sessions ?? []
    } catch { /* silent */ }
  }

  // 创建新会话
  async function createSession(title: string): Promise<string | null> {
    try {
      const res = await chatApi.createSession({
        title: title.substring(0, 30),
        model_id: selectedModel.value,
      })
      if (res.code === 0 && res.data) {
        sessions.value.unshift(res.data)
        refreshHistory?.()
        return res.data.id
      }
    } catch { /* fall through */ }
    return null
  }

  // 加载指定会话的消息列表
  async function loadMessages(sessionId: string) {
    try {
      const res = await chatApi.getMessages(sessionId)
      if (res.code === 0) {
        const serverMessages = (res.data.messages ?? []).map((m) => ({
          id: m.id,
          role: m.role as 'user' | 'assistant',
          content: m.content,
          sources: m.sources,
          timeline: m.reasoning_steps?.map((s) => ({
            title: s.content ?? s.type,
            detail: s.detail,
            status: s.status ?? 'success',
          })),
        }))

        // 保留未持久化的临时用户消息（ID 以 u- 开头）
        // 避免服务器返回空数组时覆盖刚发送的消息
        // 同时排除已在服务器上存在的消息（按内容去重）
        const serverUserContents = new Set(
          serverMessages.filter((m) => m.role === 'user').map((m) => m.content),
        )
        const localUserMsgs = messages.value.filter(
          (m) =>
            m.role === 'user' &&
            m.id.startsWith('u-') &&
            !serverUserContents.has(m.content),
        )

        messages.value = [...localUserMsgs, ...serverMessages]
        collapsedTimelines.value = new Set(
          messages.value
            .map((m, i) => (m.timeline?.length ? i : -1))
            .filter((i) => i >= 0),
        )
      }
    } catch {
      // 出错时保留已有消息，不清空
      messages.value = messages.value
    }
  }

  // 选中指定会话并加载消息
  function selectSession(sessionId: string) {
    activeSessionId.value = sessionId
    loadMessages(sessionId)
  }

  // ── 发送消息（SSE 流式） ──
  async function sendMessage() {
    const content = input.value.trim()
    if (!content || isLoading.value) return

    if (!selectedModel.value) {
      ElMessage.warning('请先选择一个模型')
      return
    }

    // 延迟清空输入框，避免跳转后问题丢失
    // 如果是新会话，等会话创建成功后再清空
    const isNewSession = !activeSessionId.value
    if (!isNewSession) {
      // 已有会话，立即清空
      input.value = ''
    }

    messages.value.push({ id: 'u-' + Date.now(), role: 'user', content })
    isLoading.value = true
    progressText.value = ''
    streamContent.value = ''
    streamTimeline.value = []

    // 自动创建会话
    if (isNewSession) {
      const id = await createSession(content)
      if (!id) {
        messages.value.push({
          id: 'e-' + Date.now(),
          role: 'error',
          content: '创建会话失败',
          retryable: true,
        })
        isLoading.value = false
        return
      }
      // 会话创建成功，清空输入框
      input.value = ''
      activeSessionId.value = id
      router.push(`/chat/${id}`)
    }

    const modelOpt = modelOptions.value.find(
      (m) => m.id === selectedModel.value,
    )

    abortController = new AbortController()

    let assistantId = ''
    let finalContent = ''
    let finalSources: StreamEvent['sources'] = []

    const safetyTimer = setTimeout(() => {
      if (isLoading.value) {
        abortController?.abort()
        messages.value.push({
          id: 'e-' + Date.now(),
          role: 'error',
          content: '响应超时',
          retryable: true,
        })
      }
    }, 120000)

    try {
      const reader = await chatApi.sendMessage(activeSessionId.value, {
      content,
      model_id: selectedModel.value,
      model_type: modelOpt?.modelType ?? 'system',
      search_mode: searchMode.value,
      knowledge_base_ids: selectedKBs.value.length ? selectedKBs.value : knowledgeBases.value.map(k => k.id),
    }, abortController.signal)

    const traceId = reader._meta?.trace_id

    const decoder = new TextDecoder()
    let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() ?? ''

        for (const line of lines) {
          if (!line.startsWith('data: ')) continue

          try {
            const evt: StreamEvent = JSON.parse(line.slice(6))

            switch (evt.type) {
              case 'start':
                if (evt.message_id) assistantId = evt.message_id
                if (evt.sources) finalSources = evt.sources
                break

              case 'progress':
                progressText.value = evt.content ?? ''
                break

              case 'plan':
              case 'tool_call':
              case 'tool_result':
              case 'warning':
                // 添加新步骤前，将所有运行中的步骤标记为成功
                streamTimeline.value.forEach(
                  (s) => s.status === 'running' && (s.status = 'success'),
                )
                streamTimeline.value.push({
                  title: evt.title ?? evt.content ?? '',
                  detail: evt.detail,
                  status: evt.status ?? 'running',
                })
                progressText.value = ''
                break

              case 'thinking': {
                const thinkTitle = evt.title ?? evt.content ?? ''
                if (!thinkTitle) break

                // 查找是否已有同名的 running 状态事件
                const existingIdx = streamTimeline.value.findIndex(
                  (s) => s.title === thinkTitle && s.status === 'running',
                )

                if (existingIdx >= 0) {
                  // 更新已存在的 running 事件为 success
                  streamTimeline.value[existingIdx].status = evt.status ?? 'success'
                  if (evt.detail) {
                    streamTimeline.value[existingIdx].detail = evt.detail
                  }
                } else {
                  // 没有找到同名 running 事件，直接添加
                  streamTimeline.value.forEach(
                    (s) => s.status === 'running' && (s.status = 'success'),
                  )
                  streamTimeline.value.push({
                    title: thinkTitle,
                    detail: evt.detail,
                    status: evt.status ?? 'running',
                  })
                }
                progressText.value = ''
                break
              }

              case 'sources':
                if (evt.sources?.length) finalSources = evt.sources
                break

              case 'content':
              case 'answer':
                progressText.value = ''
                streamTimeline.value.forEach(
                  (s) => s.status === 'running' && (s.status = 'success'),
                )
                finalContent += evt.content ?? ''
                streamContent.value = finalContent
                break

              case 'error':
                isLoading.value = false
                messages.value.push({
                  id: 'e-' + Date.now(),
                  role: 'error',
                  content: evt.title || '未知错误',
                  detail: evt.detail,
                  retryable: evt.retryable !== false,
                })
                return

              case 'done':
                streamTimeline.value.forEach(
                  (s) => s.status === 'running' && (s.status = 'success'),
                )
                if (evt.sources?.length) {
                  finalSources = evt.sources
                  streamSources.value = evt.sources
                }
                messages.value.push({
                  id: assistantId || 'a-' + Date.now(),
                  role: 'assistant',
                  content: finalContent,
                  sources: finalSources,
                  timeline:
                    streamTimeline.value.length > 0
                      ? [...streamTimeline.value]
                      : undefined,
                  trace_id: traceId,
                })
                if (streamTimeline.value.length > 0) {
                  collapsedTimelines.value.add(messages.value.length - 1)
                }
                isLoading.value = false
                streamContent.value = ''
                streamSources.value = []
                streamTimeline.value = []
                progressText.value = ''
                return
            }
          } catch { /* 跳过非法 JSON */ }
        }
      }
    } catch (e: unknown) {
      const isAbort = e instanceof DOMException && e.name === 'AbortError'
      if (isAbort) {
        // 用户中断——保存已有的部分内容 / 推理步骤 / 来源
        if (finalContent || streamContent.value || streamTimeline.value.length || streamSources.value?.length) {
          messages.value.push({
            id: assistantId || 'a-' + Date.now(),
            role: 'assistant',
            content: streamContent.value || finalContent,
            sources: finalSources.length ? finalSources : (streamSources.value?.length ? [...streamSources.value] : undefined),
            timeline: streamTimeline.value.length ? [...streamTimeline.value] : undefined,
            trace_id: traceId,
          })
        }
      } else {
        const msg = e instanceof Error ? e.message : '请求失败'
        messages.value.push({
          id: 'e-' + Date.now(),
          role: 'error',
          content: msg,
          retryable: true,
        })
      }
    } finally {
      clearTimeout(safetyTimer)
      abortController = null
      isLoading.value = false
      streamContent.value = ''
      streamSources.value = []
      streamTimeline.value = []
      progressText.value = ''
    }
  }

  // ── 辅助方法 ──
  // 滚动到底部
  function scrollToBottom(el: HTMLElement | null) {
    nextTick(() => {
      if (el) el.scrollTop = el.scrollHeight
    })
  }

  // 切换知识库选中状态
  function toggleKB(id: string) {
    const idx = selectedKBs.value.indexOf(id)
    if (idx >= 0) selectedKBs.value.splice(idx, 1)
    else selectedKBs.value.push(id)
  }

  // 复制文本到剪贴板
  function copyText(text: string) {
    navigator.clipboard.writeText(text)
      .then(() => ElMessage.success('已复制'))
      .catch(() => ElMessage.error('复制失败'))
  }

  // 重新生成最后一条回复
  function regenerate() {
    const lastIdx = messages.value.length - 1
    if (lastIdx >= 0 && messages.value[lastIdx].role === 'assistant') {
      messages.value.splice(lastIdx, 1)
    }
    const lastUser = [...messages.value]
      .reverse()
      .find((m) => m.role === 'user')
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

  // 重试最后一条错误消息
  function retryLastMessage() {
    // 找到最后一条错误消息
    const lastErrorIdx = [...messages.value]
      .reverse()
      .findIndex((m) => m.role === 'error' && m.retryable)

    if (lastErrorIdx >= 0) {
      // 删除错误消息
      const actualIdx = messages.value.length - 1 - lastErrorIdx
      messages.value.splice(actualIdx, 1)

      // 找到最后一条用户消息并重试
      const lastUser = [...messages.value]
        .reverse()
        .find((m) => m.role === 'user')
      if (lastUser) {
        // 删除原用户消息，避免 sendMessage 重复添加
        const userIdx = messages.value.lastIndexOf(lastUser)
        if (userIdx >= 0) {
          messages.value.splice(userIdx, 1)
        }
        input.value = lastUser.content
        sendMessage()
      }
    } else {
      // 没有可重试的错误，执行普通的重新生成
      regenerate()
    }
  }

  // 中止当前生成
  function stopGeneration() {
    if (abortController) {
      abortController.abort()
    }
  }

  // ── 反馈 ──
  // 提交消息反馈
  async function submitFeedback(
    messageId: string,
    req: FeedbackRequest,
  ): Promise<void> {
    const idx = messages.value.findIndex((m) => m.id === messageId)
    if (idx < 0) return
    const target = messages.value[idx]
    try {
      await chatApi.submitFeedback(messageId, req)
      const next = [...messages.value]
      next[idx] = {
        ...target,
        feedback_rating: req.rating,
        feedback_reasons: req.reasons ?? [],
        feedback_comment: req.comment,
      }
      messages.value = next
      ElMessage.success(req.rating === 1 ? '感谢您的反馈' : '感谢反馈，我们会持续优化')
    } catch (e: unknown) {
      ElMessage.error(e instanceof Error ? e.message : '提交反馈失败')
    }
  }

  // ── 内容格式化 ──
  // 格式化消息内容，处理引用标签
  function formatContent(content: string, _sources?: unknown[]): string {
    if (!content) return ''

    const cites: {
      type: string
      doc?: string
      chunkId?: string
      url?: string
      title?: string
    }[] = []

    let processed = content
    processed = processed.replace(
      /<kb\s+[^>]*?doc="([^"]*)"[^>]*?chunk_id="([^"]*)"[^>]*?\/?>/gi,
      (_, doc: string, chunkId: string) => {
        const idx = cites.length
        cites.push({ type: 'kb', doc, chunkId })
        return `%%CITE${idx}%%`
      },
    )
    processed = processed.replace(
      /<web\s+[^>]*?url="([^"]*)"[^>]*?title="([^"]*)"[^>]*?\/?>/gi,
      (_, url: string, title: string) => {
        const idx = cites.length
        cites.push({ type: 'web', url, title })
        return `%%CITE${idx}%%`
      },
    )

    let html = marked.parse(processed, {
      breaks: true,
      gfm: true,
    }) as string

    const urlToNumMap = new Map<string, number>()
    let webNum = 0
    for (let i = 0; i < cites.length; i++) {
      const c = cites[i]
      let citeHtml: string
      if (c.type === 'kb') {
        const docName = escapeHtml(c.doc ?? '知识库文档')
        const chunkId = c.chunkId ?? ''
        const docTitle = escapeAttr(c.doc ?? '')
        citeHtml = `<span class="inline-cite-kb" data-chunk-id="${escapeAttr(chunkId)}" data-doc="${docTitle}">📄${docName}</span>`
      } else {
        const url = c.url ?? ''
        let num = urlToNumMap.get(url)
        if (num === undefined) {
          webNum++
          num = webNum
          urlToNumMap.set(url, num)
        }
        const title = c.title ?? '网页链接'
        const tipKey = nextTipKey()
        tooltipStore.set(tipKey, title)
        const safeUrl = escapeAttr(url)
        citeHtml = `<a class="inline-cite-web" href="${safeUrl}" target="_blank" rel="noopener" data-tip-key="${tipKey}">[${num}]</a>`
      }
      html = html.replace(`%%CITE${i}%%`, citeHtml)
    }

    return html
  }

  // 清理 tooltip 文本中的元数据标签
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

  // 获取来源文档 tooltip 所需的逗号分隔 chunk ID
  function getSourceChunkIds(source: any): string {
    if (!source) return ''
    const chunks = source.chunks
    if (!Array.isArray(chunks) || chunks.length === 0) return ''
    return chunks
      .map((chunk: any) => chunk.id)
      .filter(Boolean)
      .join(',')
  }

  // 从内容中提取网页来源（用于展示联网搜索链接）
  function extractWebSources(content: string): { url: string; title: string }[] {
    const result: { url: string; title: string }[] = []
    const seen = new Set<string>()
    const re = /<web\s+url="([^"]*)"\s+title="([^"]*)"\s*\/>/g
    let m
    while ((m = re.exec(String(content))) !== null) {
      if (!seen.has(m[1])) {
        seen.add(m[1])
        result.push({ url: m[1], title: m[2] })
      }
    }
    return result
  }

  // 重置聊天状态，开始新会话
  function newChat() {
    activeSessionId.value = null
    messages.value = []
    collapsedTimelines.value = new Set()
    isLoading.value = false
    streamContent.value = ''
    streamTimeline.value = []
    progressText.value = ''
    input.value = ''
  }

  return {
    sessions,
    activeSessionId,
    activeSession,
    messages,
    collapsedTimelines,
    isLoading,
    streamContent,
    streamSources,
    streamTimeline,
    progressText,
    modelOptions,
    knowledgeBases,
    connected,
    input,
    selectedModel,
    selectedKBs,
    searchMode,
    kbTriggerText,
    init,
    loadSessions,
    selectSession,
    sendMessage,
    scrollToBottom,
    toggleKB,
    formatContent,
    getSourceChunkIds,
    extractWebSources,
    copyText,
    regenerate,
    retryLastMessage,
    stopGeneration,
    submitFeedback,
    newChat,
    cleanTooltipText,
  }
}

// ── 工具函数 ──

// HTML 转义
function escapeHtml(s: string): string {
  const d = document.createElement('div')
  d.textContent = s
  return d.innerHTML
}

// HTML 属性转义
function escapeAttr(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&quot;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/\n/g, '&#10;')
}

// 数字转圆圈数字符号
function toCircleNum(n: number): string {
  const c = '①②③④⑤⑥⑦⑧⑨⑩⑪⑫⑬⑭⑯⑰⑱⑲⑳'
  return n >= 1 && n <= 20 ? c[n - 1] : `[${n}]`
}
