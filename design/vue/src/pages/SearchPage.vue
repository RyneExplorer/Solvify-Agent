<template>
  <div class="h-full flex flex-col p-6 overflow-auto">
    <div class="max-w-4xl mx-auto w-full">
      <h1 class="text-2xl font-bold text-slate-900 mb-6" style="font-family: 'Space Grotesk', sans-serif;">搜索</h1>

      <div class="flex gap-2 mb-8">
        <div class="relative flex-1">
          <svg class="absolute left-3.5 top-1/2 -translate-y-1/2 w-4 h-4 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <circle cx="11" cy="11" r="8" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            <path d="m21 21-4.35-4.35" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
          <input
            v-model="query"
            placeholder="搜索历史对话或知识库文档..."
            class="w-full pl-10 pr-4 py-3 text-sm rounded-xl border border-slate-200 bg-white text-slate-900 outline-none transition-colors focus:border-slate-900 placeholder:text-slate-400"
            @keydown.enter="doSearch"
          />
        </div>
        <AppButton @click="doSearch">搜索</AppButton>
      </div>

      <div v-if="loading" class="text-center text-sm text-slate-400 py-12">搜索中...</div>

      <template v-else-if="searched">
        <!-- Chat Messages -->
        <div class="mb-8">
          <h2 class="text-sm font-semibold text-slate-900 uppercase tracking-wider mb-3">历史对话</h2>
          <div v-if="result.chat_messages.length" class="space-y-3">
            <AppCard
              v-for="msg in result.chat_messages"
              :key="msg.id"
              class="cursor-pointer hover:border-slate-300 transition-colors"
              @click="goToChat(msg.session_id)"
            >
              <div class="flex items-center gap-2 mb-2">
                <AppBadge :variant="msg.role === 'user' ? 'primary' : 'success'">{{ msg.role === 'user' ? '我' : 'AI' }}</AppBadge>
                <span class="text-xs text-slate-400">{{ msg.session_title || '未命名会话' }}</span>
                <span class="text-xs text-slate-400 ml-auto">{{ formatDate(msg.created_at) }}</span>
              </div>
              <p class="text-sm text-slate-700 line-clamp-3">{{ msg.content }}</p>
            </AppCard>
          </div>
          <div v-else class="text-sm text-slate-400">未找到相关对话</div>
        </div>

        <!-- Documents -->
        <div>
          <h2 class="text-sm font-semibold text-slate-900 uppercase tracking-wider mb-3">知识库文档</h2>
          <div v-if="result.documents.length" class="space-y-3">
            <AppCard
              v-for="doc in result.documents"
              :key="doc.id"
              class="cursor-pointer hover:border-slate-300 transition-colors"
              @click="goToDoc(doc.knowledge_base_id, doc.document_id)"
            >
              <div class="flex items-center gap-2 mb-2">
                <span class="text-sm font-medium text-slate-900">{{ doc.title || '未命名文档' }}</span>
                <span class="text-xs text-slate-400 ml-auto">相似度 {{ (doc.score * 100).toFixed(0) }}%</span>
              </div>
              <p class="text-sm text-slate-700 line-clamp-3">{{ doc.content }}</p>
            </AppCard>
          </div>
          <div v-else class="text-sm text-slate-400">未找到相关文档</div>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import AppCard from '../components/ui/AppCard.vue'
import AppButton from '../components/ui/AppButton.vue'
import AppBadge from '../components/ui/AppBadge.vue'
import { searchAll } from '@/api/search'
import type { SearchResult } from '@/types/search'
import { ElMessage } from 'element-plus'

const router = useRouter()
const query = ref('')
const loading = ref(false)
const searched = ref(false)
const result = ref<SearchResult>({ chat_messages: [], documents: [] })

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
  router.push(`/chat/${sessionId}`)
}

function goToDoc(kbId: string, docId: string) {
  router.push(`/kb/${kbId}/docs/${docId}`)
}

function formatDate(date: string) {
  if (!date) return '-'
  return new Date(date).toLocaleString('zh-CN', { hour12: false })
}
</script>
