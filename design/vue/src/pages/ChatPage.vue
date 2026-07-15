<template>
  <div class="flex flex-col h-full bg-white">
    <!-- Header -->
    <div class="h-14 flex items-center justify-between px-6 border-b border-slate-200 shrink-0">
      <h2 class="text-sm font-semibold text-slate-800">{{ session?.title ?? '新对话' }}</h2>
      <div class="flex items-center gap-1.5">
        <span class="w-1.5 h-1.5 rounded-full" :class="connected ? 'bg-emerald-500' : 'bg-slate-300'" />
        <span class="text-xs text-slate-400">{{ connected ? '就绪' : '加载中...' }}</span>
      </div>
    </div>

    <!-- State: NEW CHAT (centered) -->
    <template v-if="!hasMessages && !isLoading">
      <div class="flex-1 flex flex-col items-center justify-center px-6 gap-8">
        <div class="text-center">
          <h1 class="text-4xl font-bold text-slate-800 italic mb-2">开始新的对话</h1>
          <p class="text-sm text-slate-400">向你的知识库提问，获取回答与分析</p>
        </div>
        <div class="w-full max-w-2xl">
          <ChatInputCard
            :input="input"
            :model-value="selectedModel"
            :loading="isLoading"
            :search-mode="searchMode"
            :kb-text="kbTriggerText"
            :knowledge-bases="knowledgeBases"
            :selected-k-bs="selectedKBs"
            :model-options="modelOptions"
            @update:input="input = $event"
            @update:model-value="selectedModel = $event"
            @send="sendMessage"
            @stop="stopGeneration"
            @toggle-kb="toggleKB"
            @toggle-search-mode="searchMode = $event"
          />
        </div>
      </div>
      <div class="text-center pb-3 shrink-0"><span class="text-xs text-slate-300">内容由 AI 生成，仅供参考</span></div>
    </template>

    <!-- State: ACTIVE CHAT (messages + bottom input) -->
    <template v-else>
      <div ref="chatEl" class="flex-1 overflow-y-auto px-6 py-6">
        <div class="max-w-[800px] mx-auto space-y-6">
          <div v-for="(msg, msgIdx) in messages" :key="msg.id" class="flex" :class="msg.role === 'user' ? 'justify-end' : 'justify-start'">
            <div class="max-w-[80%]">
              <!-- Timeline -->
              <div v-if="msg.role === 'assistant' && msg.timeline?.length" class="mb-3">
                <button
                  @click="collapsedTimelines.has(msgIdx) ? collapsedTimelines.delete(msgIdx) : collapsedTimelines.add(msgIdx)"
                  class="flex items-center gap-1.5 text-xs text-slate-400 hover:text-slate-600 mb-1 cursor-pointer"
                >
                  <svg
                    class="w-3 h-3 transition-transform"
                    :class="{ '-rotate-90': collapsedTimelines.has(msgIdx) }"
                    fill="none" stroke="currentColor" viewBox="0 0 24 24"
                  ><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/></svg>
                  <span>推理步骤 ({{ msg.timeline.length }})</span>
                </button>
                <div v-show="!collapsedTimelines.has(msgIdx)" class="space-y-1.5">
                  <div v-for="(step, si) in msg.timeline" :key="si" class="group">
                    <div class="flex items-start gap-2 py-0.5 text-xs">
                      <span class="text-emerald-500 shrink-0">✓</span>
                      <span class="text-emerald-600">{{ step.title }}</span>
                    </div>
                    <div v-if="step.detail" class="ml-5 mt-1 text-xs text-slate-500 bg-slate-50 rounded-lg px-3 py-2 whitespace-pre-wrap">{{ step.detail }}</div>
                  </div>
                </div>
              </div>
              <!-- Bubble -->
              <div :class="['px-4 py-3 rounded-2xl text-sm leading-relaxed', msg.role === 'user' ? 'bg-slate-100 text-slate-800' : msg.role === 'error' ? 'bg-red-50 text-red-700 border border-red-200' : 'bg-white text-slate-800']">
                <div v-if="msg.role === 'assistant'" v-html="formatContent(msg.content, msg.sources)" class="md-content" />
                <template v-else>{{ msg.content }}</template>
              </div>
              <!-- Actions -->
              <div v-if="msg.role === 'assistant' || msg.role === 'error'" class="flex items-center gap-1 mt-2">
                <button class="p-1.5 rounded-md hover:bg-slate-100 text-slate-400" title="复制" @click="copyText(msg.content)">
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"/></svg>
                </button>
                <button v-if="msg.role === 'assistant'" class="p-1.5 rounded-md hover:bg-slate-100 text-slate-400" title="重新生成" @click="regenerate">
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"/></svg>
                </button>
                <button v-if="msg.role === 'error' && msg.retryable" class="p-1.5 rounded-md hover:bg-slate-100 text-slate-400" title="重试" @click="retryLastMessage">
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"/></svg>
                </button>
                <button v-if="msg.role === 'assistant'" class="p-1.5 rounded-md hover:bg-slate-100 text-slate-400" title="保存到知识库" @click="openSaveNoteDialog(msg.content)">
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M19 21H5a2 2 0 01-2-2V5a2 2 0 012-2h11l5 5v11a2 2 0 01-2 2z"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M17 21v-8H7v8M7 3v5h8"/></svg>
                </button>
              </div>
              <!-- Sources -->
              <div v-if="msg.role === 'assistant' && msg.sources?.filter(s => s?.title).length" class="mt-2 flex flex-wrap items-center gap-1.5">
                <span class="text-[11px] text-slate-400">来源:</span>
                <span
                  v-for="(s, si) in msg.sources.filter(s => s?.title)"
                  :key="si"
                  :data-chunk-ids="getSourceChunkIds(s)"
                  :data-doc="cleanTitle(s.title)"
                  class="text-[11px] px-2 py-0.5 bg-slate-100 border border-slate-200 rounded-full text-slate-500 cursor-help hover:bg-slate-200 transition-colors"
                >{{ cleanTitle(s.title) }}</span>
              </div>
              <div v-if="msg.role === 'error' && msg.detail && msg.detail !== msg.content" class="mt-1 text-xs text-red-500">{{ msg.detail }}</div>
            </div>
          </div>

          <!-- Streaming loading -->
          <div v-if="isLoading" class="flex justify-start">
            <div class="max-w-[80%]">
              <div class="px-4 py-3 rounded-2xl bg-white text-sm">
                <div v-if="streamTimeline.length" class="mb-3 space-y-1.5">
                  <div v-for="(step, si) in streamTimeline" :key="si" class="group">
                    <div class="flex items-start gap-2 py-0.5 text-xs">
                      <span v-if="step.status === 'running'" class="w-3 h-3 mt-0.5 border-2 border-slate-200 border-t-emerald-500 rounded-full animate-spin shrink-0" />
                      <span v-else class="w-3 h-3 mt-0.5 text-emerald-500 shrink-0">✓</span>
                      <span :class="step.status === 'running' ? 'text-slate-500' : 'text-emerald-600'">{{ step.title }}</span>
                    </div>
                    <div v-if="step.detail" class="ml-5 mt-1 text-xs text-slate-500 bg-slate-50 rounded-lg px-3 py-2 whitespace-pre-wrap">{{ step.detail }}</div>
                  </div>
                </div>
                <div v-if="progressText" class="flex items-center gap-2 mb-2 text-xs text-slate-400">
                  <span class="w-3 h-3 border-2 border-slate-200 border-t-emerald-500 rounded-full animate-spin" />{{ progressText }}
                </div>
                <div v-if="!streamContent" class="flex gap-1 py-1">
                  <span class="w-1 h-1 rounded-full bg-slate-300 animate-bounce" style="animation-delay:0s" />
                  <span class="w-1 h-1 rounded-full bg-slate-300 animate-bounce" style="animation-delay:0.15s" />
                  <span class="w-1 h-1 rounded-full bg-slate-300 animate-bounce" style="animation-delay:0.3s" />
                </div>
                <div v-if="streamContent" v-html="formatContent(streamContent, streamSources)" class="md-content leading-relaxed" />
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="px-6 py-4 shrink-0">
        <div class="max-w-[800px] mx-auto">
          <ChatInputCard
            :input="input"
            :model-value="selectedModel"
            :loading="isLoading"
            :search-mode="searchMode"
            :kb-text="kbTriggerText"
            :knowledge-bases="knowledgeBases"
            :selected-k-bs="selectedKBs"
            :model-options="modelOptions"
            @update:input="input = $event"
            @update:model-value="selectedModel = $event"
            @send="sendMessage"
            @stop="stopGeneration"
            @toggle-kb="toggleKB"
            @toggle-search-mode="searchMode = $event"
          />
        </div>
      </div>
      <div class="text-center pb-3 shrink-0"><span class="text-xs text-slate-300">内容由 AI 生成，仅供参考</span></div>
    </template>

    <!-- Save Note Dialog -->
    <AppDialog v-model="showNoteDialog" title="保存到知识库" size="md">
      <div class="space-y-4">
        <div>
          <label class="text-xs text-slate-500 mb-1.5 block">笔记标题</label>
          <input
            v-model="noteTitle"
            type="text"
            class="w-full px-3 py-2.5 text-sm rounded-xl border border-slate-200 bg-slate-50 text-slate-900 outline-none focus:border-slate-900"
            placeholder="输入笔记标题"
          />
        </div>
        <div>
          <label class="text-xs text-slate-500 mb-1.5 block">选择知识库</label>
          <AppDropdownSelect
            ref="noteKbDropdownRef"
            :model-label="selectedKbName"
            placeholder="请选择知识库"
          >
            <div class="px-3 py-2 border-b border-slate-100" v-if="knowledgeBases.length > 5">
              <div class="relative">
                <svg class="w-4 h-4 text-slate-400 absolute left-2.5 top-1/2 -translate-y-1/2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"/>
                </svg>
                <input
                  v-model="kbSearch"
                  placeholder="搜索知识库..."
                  class="w-full pl-8 pr-3 py-1.5 text-xs bg-slate-50 rounded-lg outline-none border border-transparent focus:border-slate-200"
                />
              </div>
            </div>
            <div class="max-h-[240px] overflow-y-auto py-1">
              <template v-if="filteredLocalKBs.length">
                <div class="px-4 py-1.5 text-[11px] text-slate-400 font-medium">自建知识库</div>
                <div
                  v-for="kb in filteredLocalKBs"
                  :key="kb.id"
                  @click="selectNoteKb(kb)"
                  class="px-4 py-2 text-sm cursor-pointer hover:bg-slate-50 flex items-center justify-between gap-2"
                  :class="noteTargetKB === kb.id ? 'text-emerald-600 font-medium' : 'text-slate-700'"
                >
                  <span class="truncate">{{ kb.name }}</span>
                  <svg v-if="noteTargetKB === kb.id" class="w-4 h-4 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M5 13l4 4L19 7"/>
                  </svg>
                </div>
              </template>
              <template v-if="filteredSyncKBs.length">
                <div class="px-4 py-1.5 text-[11px] text-slate-400 font-medium border-t border-slate-50 mt-1 pt-2">同步知识库</div>
                <div
                  v-for="kb in filteredSyncKBs"
                  :key="kb.id"
                  @click="selectNoteKb(kb)"
                  class="px-4 py-2 text-sm cursor-pointer hover:bg-slate-50 flex items-center justify-between gap-2"
                  :class="noteTargetKB === kb.id ? 'text-emerald-600 font-medium' : 'text-slate-700'"
                >
                  <span class="truncate">{{ kb.name }}</span>
                  <svg v-if="noteTargetKB === kb.id" class="w-4 h-4 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M5 13l4 4L19 7"/>
                  </svg>
                </div>
              </template>
              <div v-if="!filteredLocalKBs.length && !filteredSyncKBs.length" class="px-4 py-6 text-center text-sm text-slate-400">
                暂无知识库
              </div>
            </div>
          </AppDropdownSelect>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <AppButton variant="secondary" size="sm" @click="showNoteDialog = false">取消</AppButton>
          <AppButton size="sm" :disabled="!noteTargetKB || savingNote" @click="doSaveNote">
            {{ savingNote ? '保存中...' : '保存' }}
          </AppButton>
        </div>
      </template>
    </AppDialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage } from 'element-plus'
import { useChat, getTooltipContent, cleanTitle } from '@/composables/useChat'
import { useMarkdownTooltip } from '@/composables/useMarkdownTooltip'
import { createNote } from '@/api/document'
import ChatInputCard from '@/components/ChatInputCard.vue'
import AppDialog from '@/components/ui/AppDialog.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppDropdownSelect from '@/components/ui/AppDropdownSelect.vue'

const route = useRoute()
const chat = useChat()
const {
  activeSession: session, messages, collapsedTimelines, isLoading, streamContent, streamSources, streamTimeline, progressText,
  modelOptions, knowledgeBases, connected, input, selectedModel, selectedKBs, searchMode, kbTriggerText,
  init, sendMessage, scrollToBottom, toggleKB, formatContent, getSourceChunkIds, copyText, regenerate, retryLastMessage, stopGeneration,
  selectSession, loadSessions, newChat, cleanTooltipText,
} = chat

const chatEl = ref<HTMLDivElement>()
const hasMessages = computed(() => messages.value.length > 0)

useMarkdownTooltip()

// ── 保存笔记到知识库 ──
const showNoteDialog = ref(false)
const noteTitle = ref('')
const noteContent = ref('')
const noteTargetKB = ref('')
const kbSearch = ref('')
const savingNote = ref(false)
const noteKbDropdownRef = ref<InstanceType<typeof AppDropdownSelect>>()

function closeNoteKbDropdown() {
  noteKbDropdownRef.value?.close?.()
}

const localKBs = computed(() => knowledgeBases.value.filter(kb => kb.source_type === 'local'))
const syncKBs = computed(() => knowledgeBases.value.filter(kb => kb.source_type !== 'local'))
const filteredLocalKBs = computed(() => {
  if (!kbSearch.value.trim()) return localKBs.value
  const q = kbSearch.value.toLowerCase()
  return localKBs.value.filter(kb => kb.name.toLowerCase().includes(q))
})
const filteredSyncKBs = computed(() => {
  if (!kbSearch.value.trim()) return syncKBs.value
  const q = kbSearch.value.toLowerCase()
  return syncKBs.value.filter(kb => kb.name.toLowerCase().includes(q))
})
const selectedKbName = computed(() => {
  const found = knowledgeBases.value.find(kb => kb.id === noteTargetKB.value)
  return found?.name || ''
})

function selectNoteKb(kb: { id: string; name: string }) {
  noteTargetKB.value = kb.id
  kbSearch.value = ''
  closeNoteKbDropdown()
}

function openSaveNoteDialog(content: string) {
  noteContent.value = content
  noteTitle.value = (session.value?.title || 'AI回答笔记') + ' - ' + new Date().toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
  noteTargetKB.value = ''
  kbSearch.value = ''
  showNoteDialog.value = true
}

async function doSaveNote() {
  if (!noteTargetKB.value || savingNote.value) return
  savingNote.value = true
  try {
    const cleanedContent = cleanTooltipText(noteContent.value)
    await createNote(noteTargetKB.value, { title: noteTitle.value, content: cleanedContent })
    showNoteDialog.value = false
    ElMessage.success('已加入上传队列')
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : '保存失败')
  } finally {
    savingNote.value = false
  }
}

watch(
  () => [messages.value.length, streamContent.value, streamTimeline.value.length],
  () => scrollToBottom(chatEl.value ?? null),
)

// Load session from URL param
watch(
  () => route.params.sessionId,
  (id) => {
    if (id && typeof id === 'string') {
      selectSession(id)
    } else {
      newChat()
    }
  },
  { immediate: true },
)

onMounted(() => { init(); loadSessions() })
onBeforeUnmount(() => { stopGeneration() })
</script>
