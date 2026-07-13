<template>
  <div v-if="kbSources.length || webSources.length" class="mt-3.5 flex items-center gap-2 flex-wrap">
    <!-- KB sources -->
    <template v-if="kbSources.length">
      <span class="text-[11px] text-slate-400 tracking-wider flex-shrink-0">知识库</span>
      <span v-for="(s, i) in kbSources" :key="i"
        class="relative inline-flex items-center gap-1 px-2.5 py-0.5 bg-slate-100 border border-slate-200 rounded-md text-xs text-slate-500 cursor-default transition-colors hover:border-slate-300 hover:text-slate-700">
        {{ s.title }}
      </span>
    </template>

    <!-- Web sources -->
    <template v-if="webSources.length">
      <span class="text-[11px] text-slate-400 tracking-wider flex-shrink-0" :class="{ 'ml-3': kbSources.length }">联网搜索</span>
      <a v-for="(ws, i) in webSources" :key="i"
        :href="ws.url" target="_blank" :title="ws.title"
        class="inline-flex items-center gap-1 px-2.5 py-0.5 bg-blue-50 border border-blue-200 rounded-md text-xs text-blue-500 no-underline transition-colors hover:bg-blue-100 hover:border-blue-400 hover:text-blue-600">
        {{ toCircleNum(i + 1) }} {{ ws.title }}
      </a>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

interface SourceInfo { title?: string }
interface WebSource { url: string; title: string }

const props = defineProps<{
  sources: SourceInfo[]
  webSources: WebSource[]
}>()

const kbSources = computed(() => props.sources.filter(s => s?.title))

function toCircleNum(n: number): string {
  const circles = '①②③④⑤⑥⑦⑧⑨⑩⑪⑫⑬⑭⑮⑯⑰⑱⑲⑳'
  return n >= 1 && n <= 20 ? circles[n - 1] : `[${n}]`
}
</script>
