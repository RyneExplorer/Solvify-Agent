<template>
  <div class="flex flex-col gap-1">
    <div v-for="(item, i) in items" :key="i"
      class="flex items-start gap-2.5 px-3 py-2 rounded-[10px] text-[13px] animate-[fadeUp_.3s_ease]"
      :class="{
        'bg-emerald-50/50': item.status === 'running',
        'bg-amber-50/50': item.status === 'warning',
        'bg-red-50/50': item.status === 'error',
      }">
      <div class="w-5 h-5 flex-shrink-0 flex items-center justify-center mt-px">
        <div v-if="item.status === 'running'" class="w-3.5 h-3.5 border-2 border-slate-200 border-t-emerald-500 rounded-full animate-spin" />
        <svg v-else-if="item.status === 'success'" class="text-emerald-500" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"><path d="M20 6L9 17l-5-5"/></svg>
        <svg v-else-if="item.status === 'warning'" class="text-amber-500" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>
        <svg v-else-if="item.status === 'error'" class="text-red-500" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>
      </div>
      <div class="flex-1 min-w-0">
        <div class="leading-relaxed" :class="{
          'text-slate-500': item.status === 'running',
          'text-emerald-600': item.status === 'success',
          'text-amber-600': item.status === 'warning',
          'text-red-600': item.status === 'error',
        }">{{ item.title || item.content }}</div>
        <div v-if="item.detail" class="text-xs mt-0.5 leading-relaxed" :class="{
          'text-slate-400': item.status !== 'warning' && item.status !== 'error',
          'text-amber-400/80': item.status === 'warning',
          'text-red-400/80': item.status === 'error',
        }">{{ item.detail }}</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
interface TimelineItem {
  title?: string
  content?: string
  detail?: string
  status: 'running' | 'success' | 'warning' | 'error'
}
defineProps<{ items: TimelineItem[] }>()
</script>
