<script setup lang="ts">
import type { ChatSpan } from '@/types/chat'
import { computed, ref, inject } from 'vue'

const props = defineProps<{
  span: ChatSpan
  depth?: number
}>()

const depth = computed(() => props.depth ?? 0)
const expanded = ref(depth.value <= 1)
const rootDurationMs = inject<number>('trace_root_duration_ms')

const statusColor = computed(() => {
  switch (props.span.status) {
    case 'error': return 'text-red-600 bg-red-50 border-red-200'
    case 'ok': return 'text-emerald-600 bg-emerald-50 border-emerald-200'
    case 'slow': return 'text-amber-600 bg-amber-50 border-amber-200'
    default: return 'text-slate-500 bg-slate-50 border-slate-200'
  }
})

const barPct = computed(() => {
  const dur = props.span.duration_ms
  const root = rootDurationMs ?? props.span.duration_ms
  if (!dur || !root) return 0
  return Math.max(1, Math.min(100, (dur / root) * 100))
})

function toggle() {
  expanded.value = !expanded.value
}

function formatAttrs(attrs: Record<string, unknown> | undefined): string {
  if (!attrs || !Object.keys(attrs).length) return ''
  try {
    return JSON.stringify(attrs, null, 2)
  } catch {
    return String(attrs)
  }
}

function formatEvents(events: ChatSpan['events']): string {
  if (!events?.length) return ''
  try {
    return JSON.stringify(
      events.map(e => ({ time: e.time, name: e.name, attrs: e.attrs })),
      null,
      2,
    )
  } catch {
    return String(events)
  }
}
</script>

<template>
  <div class="select-text">
    <div
      class="flex items-start gap-3 px-4 py-3 hover:bg-slate-50/60"
      :style="{ paddingLeft: `${16 + depth * 20}px` }"
    >
      <button
        v-if="span.children?.length"
        type="button"
        class="mt-1 w-5 h-5 shrink-0 rounded hover:bg-slate-200 text-slate-500 text-xs flex items-center justify-center"
        @click="toggle"
      >
        <span v-if="expanded">−</span>
        <span v-else>+</span>
      </button>
      <span v-else class="mt-1 w-5 h-5 shrink-0" />
      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-3 mb-1">
          <span class="font-mono text-[12px] text-slate-700 truncate">{{ span.name }}</span>
          <span
            class="text-[10px] px-1.5 py-0.5 rounded-md border uppercase tracking-wide font-medium"
            :class="statusColor"
          >{{ span.status || '-' }}</span>
          <span class="text-[10px] text-slate-400 border border-slate-200 rounded-md px-1.5 py-0.5">{{ span.component || '-' }}</span>
          <span class="text-[10px] text-slate-400">Span <code class="font-mono bg-slate-100 px-1 rounded">{{ (span.span_id || '').slice(0, 8) }}</code></span>
          <span v-if="span.parent_id" class="text-[10px] text-slate-400">Parent <code class="font-mono bg-slate-100 px-1 rounded">{{ span.parent_id.slice(0, 8) }}</code></span>
        </div>
        <div class="flex items-center gap-3">
          <div class="flex-1 h-2 bg-slate-100 rounded overflow-hidden">
            <div
              class="h-full rounded transition-all"
              :class="
                span.status === 'error' ? 'bg-red-400'
                : span.status === 'slow' ? 'bg-amber-400'
                : depth === 0 ? 'bg-slate-800'
                : 'bg-emerald-400'
              "
              :style="{ width: `${barPct}%` }"
            />
          </div>
          <span class="text-xs text-slate-700 font-mono tabular-nums w-24 text-right">{{ span.duration_ms ?? 0 }} ms</span>
        </div>
        <div v-if="span.error" class="mt-2 rounded-md bg-red-50 border border-red-200 text-red-700 text-[12px] px-3 py-2 whitespace-pre-wrap">{{ span.error }}</div>
        <div class="mt-2 grid grid-cols-2 gap-3" v-if="(span.attrs && Object.keys(span.attrs).length) || (span.events?.length)">
          <div v-if="span.attrs && Object.keys(span.attrs).length" class="min-w-0">
            <div class="text-[10px] uppercase tracking-wide text-slate-400 mb-1">Attrs</div>
            <pre class="text-[10px] font-mono bg-slate-50 border border-slate-200 rounded p-2 text-slate-600 overflow-auto max-h-48 m-0">{{ formatAttrs(span.attrs) }}</pre>
          </div>
          <div v-if="span.events?.length" class="min-w-0">
            <div class="text-[10px] uppercase tracking-wide text-slate-400 mb-1">Events ({{ span.events.length }})</div>
            <pre class="text-[10px] font-mono bg-slate-50 border border-slate-200 rounded p-2 text-slate-600 overflow-auto max-h-48 m-0">{{ formatEvents(span.events) }}</pre>
          </div>
        </div>
      </div>
    </div>
    <div v-if="expanded && span.children?.length" class="divide-y divide-slate-100">
      <trace-span-node
        v-for="c in span.children"
        :key="c.span_id"
        :span="c"
        :depth="depth + 1"
      />
    </div>
  </div>
</template>
