<template>
  <div v-if="visible" class="fixed inset-0 z-[10000] flex items-start justify-center pt-[15vh]" @click.self="close">
    <div class="absolute inset-0 bg-black/30 backdrop-blur-sm" />
    <div class="relative bg-white rounded-2xl shadow-2xl border border-slate-200 w-[640px] max-h-[500px] flex flex-col overflow-hidden">
      <!-- Search Input -->
      <div class="flex items-center gap-3 px-5 py-4 border-b border-slate-100">
        <svg class="w-5 h-5 text-slate-400 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <circle cx="11" cy="11" r="8" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
          <path d="m21 21-4.35-4.35" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
        <input
          ref="searchInputRef"
          v-model="query"
          placeholder="输入关键词进行历史问题搜索和知识库检索"
          class="flex-1 text-sm text-slate-900 outline-none bg-transparent placeholder:text-slate-400"
          @keydown.enter="doSearch"
          @keydown.escape="close"
        />
        <button
          @click="close"
          class="px-1.5 py-0.5 text-[11px] font-medium text-slate-500 bg-slate-100 rounded border border-slate-200 hover:bg-slate-200 transition-colors"
        >关闭</button>
      </div>

      <!-- Tabs -->
      <div class="flex items-center gap-1 px-5 pt-3 border-b border-slate-100">
        <button
          @click="activeTab = 'chat'"
          class="px-4 py-2 text-sm rounded-lg transition-colors cursor-pointer"
          :class="activeTab === 'chat'
            ? 'bg-accent-50 text-accent-600 font-medium'
            : 'text-slate-500 hover:bg-slate-50'"
        >
          对话历史
          <span
            class="ml-1.5 text-xs px-1.5 py-0.5 rounded-full"
            :class="activeTab === 'chat' ? 'bg-accent-100 text-accent-600' : 'bg-slate-100 text-slate-400'"
          >{{ result.chat_messages.length }}</span>
        </button>
        <button
          @click="activeTab = 'doc'"
          class="px-4 py-2 text-sm rounded-lg transition-colors cursor-pointer"
          :class="activeTab === 'doc'
            ? 'bg-accent-50 text-accent-600 font-medium'
            : 'text-slate-500 hover:bg-slate-50'"
        >
          知识库
          <span
            class="ml-1.5 text-xs px-1.5 py-0.5 rounded-full"
            :class="activeTab === 'doc' ? 'bg-accent-100 text-accent-600' : 'bg-slate-100 text-slate-400'"
          >{{ result.documents.length }}</span>
        </button>
      </div>

      <!-- Results -->
      <div class="flex-1 overflow-y-auto p-4">
        <div v-if="loading" class="text-center text-sm text-slate-400 py-8">搜索中...</div>

        <template v-else-if="searched">
          <!-- Chat Messages Tab -->
          <template v-if="activeTab === 'chat'">
            <div v-if="result.chat_messages.length" class="space-y-1.5">
              <button
                v-for="msg in result.chat_messages"
                :key="msg.id"
                @click="goToChat(msg.session_id)"
                class="w-full text-left p-3 rounded-lg hover:bg-slate-50 transition-colors"
              >
                <div class="flex items-center gap-2 mb-1">
                  <span class="text-[11px] font-medium text-accent-600 bg-accent-50 px-1.5 py-0.5 rounded">{{ msg.role === 'user' ? '我' : 'AI' }}</span>
                  <span class="text-xs text-slate-400">{{ msg.session_title || '未命名会话' }}</span>
                </div>
                <p class="text-sm text-slate-700 line-clamp-2">{{ msg.content }}</p>
              </button>
            </div>
            <div v-else class="text-center text-sm text-slate-400 py-8">
              未找到相关对话
            </div>
          </template>

          <!-- Documents Tab -->
          <template v-if="activeTab === 'doc'">
            <div v-if="result.documents.length" class="space-y-1.5">
              <button
                v-for="doc in result.documents"
                :key="doc.id"
                @click="goToDoc(doc.knowledge_base_id, doc.document_id)"
                class="w-full text-left p-3 rounded-lg hover:bg-slate-50 transition-colors"
              >
                <div class="flex items-center gap-2 mb-1">
                  <span class="text-sm font-medium text-slate-900">{{ doc.title || '未命名文档' }}</span>
                </div>
                <p class="text-sm text-slate-700 line-clamp-2">{{ doc.content }}</p>
              </button>
            </div>
            <div v-else class="text-center text-sm text-slate-400 py-8">
              未找到相关文档
            </div>
          </template>
        </template>

        <div v-else class="text-center text-sm text-slate-400 py-8">
          输入关键词开始搜索
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { searchAll } from '@/api/search'
import type { SearchResult } from '@/types/search'
import { ElMessage } from 'element-plus'

const router = useRouter()
const visible = ref(false)
const query = ref('')
const loading = ref(false)
const searched = ref(false)
const activeTab = ref<'chat' | 'doc'>('chat')
const result = ref<SearchResult>({ chat_messages: [], documents: [] })
const searchInputRef = ref<HTMLInputElement>()

function open() {
  visible.value = true
  query.value = ''
  searched.value = false
  activeTab.value = 'chat'
  result.value = { chat_messages: [], documents: [] }
  nextTick(() => {
    searchInputRef.value?.focus()
  })
}

function close() {
  visible.value = false
}

async function doSearch() {
  const q = query.value.trim()
  if (!q) return
  loading.value = true
  searched.value = false
  try {
    const res = await searchAll({ q, top_k: 10 })
    if (res.code === 0) {
      result.value = res.data
      searched.value = true
    }
  } catch (e: any) {
    ElMessage.error(e.message || '搜索失败')
  } finally {
    loading.value = false
  }
}

function goToChat(sessionId: string) {
  close()
  router.push(`/chat/${sessionId}`)
}

function goToDoc(kbId: string, docId: string) {
  close()
  router.push(`/kb/${kbId}/docs/${docId}`)
}

watch(visible, (val) => {
  if (val) {
    document.addEventListener('keydown', onKeydown)
  } else {
    document.removeEventListener('keydown', onKeydown)
  }
})

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    close()
  }
}

defineExpose({ open, close })
</script>
