<template>
  <div class="border border-slate-200 rounded-[10px] overflow-hidden">
    <button @click="open = !open"
      class="flex items-center gap-1.5 px-3.5 py-2.5 bg-slate-50/50 cursor-pointer text-xs text-slate-400 select-none transition-colors hover:bg-slate-100 hover:text-slate-500 w-full text-left"
      :class="{ 'open': open }">
      <svg class="flex-shrink-0 transition-transform" :class="{ 'rotate-90': open }" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><path d="M9 18l6-6-6-6"/></svg>
      推理过程 ({{ steps.length }} 步)
    </button>
    <div v-show="open" class="px-3.5 pb-3.5">
      <Timeline :items="timelineItems" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import Timeline from './Timeline.vue'

interface ReasoningStep {
  type: string
  content: string
  detail?: string
}

const props = defineProps<{ steps: ReasoningStep[] }>()
const open = ref(false)

const timelineItems = computed(() =>
  props.steps.map(s => ({
    title: s.content,
    detail: s.detail,
    status: 'success' as const,
  }))
)
</script>
