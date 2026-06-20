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
              <div v-if="msg.role === 'assistant'" class="flex items-center gap-1 mt-2">
                <button class="p-1.5 rounded-md hover:bg-slate-100 text-slate-400" title="复制" @click="copyText(msg.content)">
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"/></svg>
                </button>
                <button class="p-1.5 rounded-md hover:bg-slate-100 text-slate-400" title="重新生成" @click="regenerate">
                  <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"/></svg>
                </button>
              </div>
              <!-- Sources -->
              <div v-if="msg.role === 'assistant' && msg.sources?.filter(s => s?.title).length" class="mt-2 flex flex-wrap items-center gap-1.5">
                <span class="text-[11px] text-slate-400">来源:</span>
                <span v-for="(s, si) in msg.sources.filter(s => s?.title)" :key="si"
                  :title="getSourceTooltip(s)"
                  class="text-[11px] px-2 py-0.5 bg-slate-100 border border-slate-200 rounded-full text-slate-500 cursor-help hover:bg-slate-200 transition-colors">{{ s.title }}</span>
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
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useChat } from '@/composables/useChat'
import ChatInputCard from '@/components/ChatInputCard.vue'

const route = useRoute()
const chat = useChat()
const {
  activeSession: session, messages, collapsedTimelines, isLoading, streamContent, streamSources, streamTimeline, progressText,
  modelOptions, knowledgeBases, connected, input, selectedModel, selectedKBs, searchMode, kbTriggerText,
  init, sendMessage, scrollToBottom, toggleKB, formatContent, getSourceTooltip, copyText, regenerate, stopGeneration,
  selectSession, loadSessions, newChat,
} = chat

const chatEl = ref<HTMLDivElement>()
const hasMessages = computed(() => messages.value.length > 0)

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
