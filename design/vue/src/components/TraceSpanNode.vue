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

function statusText(status?: string): string {
  switch (status) {
    case 'ok': return '正常'
    case 'error': return '失败'
    case 'canceled':
    case 'cancelled': return '已取消'
    case 'slow': return '慢请求'
    default: return '未知'
  }
}

const statusColor = computed(() => {
  switch (props.span.status) {
    case 'error': return 'text-red-600 bg-red-50 border-red-200'
    case 'ok': return 'text-emerald-600 bg-emerald-50 border-emerald-200'
    case 'canceled':
    case 'cancelled':
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

function formatAttrEntries(attrs: Record<string, unknown> | undefined): Array<{ k: string; v: string }> {
  if (!attrs) return []
  const entries: Array<{ k: string; v: string }> = []
  for (const [k, v] of Object.entries(attrs)) {
    try {
      entries.push({
        k,
        v: typeof v === 'string' ? v : JSON.stringify(v),
      })
    } catch {
      entries.push({ k, v: String(v) })
    }
  }
  return entries
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
        <div class="flex items-center gap-3 mb-1 flex-wrap">
          <span class="font-mono text-[12px] text-slate-700 truncate">步骤名：{{ span.name }}</span>
          <span
            class="text-[10px] px-1.5 py-0.5 rounded-md border tracking-wide font-medium"
            :class="statusColor"
          >状态：{{ statusText(span.status) }}</span>
          <span class="text-[10px] text-slate-400 border border-slate-200 rounded-md px-1.5 py-0.5">组件：{{ span.component || '-' }}</span>
          <span class="text-[10px] text-slate-400">Span <code class="font-mono bg-slate-100 px-1 rounded">{{ (span.span_id || '').slice(0, 8) }}</code></span>
          <span v-if="span.parent_id" class="text-[10px] text-slate-400">Parent <code class="font-mono bg-slate-100 px-1 rounded">{{ span.parent_id.slice(0, 8) }}</code></span>
        </div>
        <div class="flex items-center gap-3">
          <div class="flex-1 h-2 bg-slate-100 rounded overflow-hidden">
            <div
              class="h-full rounded transition-all"
              :class="
                span.status === 'error' ? 'bg-red-400'
                : span.status === 'slow' || span.status === 'canceled' || span.status === 'cancelled' ? 'bg-amber-400'
                : depth === 0 ? 'bg-slate-800'
                : 'bg-emerald-400'
              "
              :style="{ width: `${barPct}%` }"
            />
          </div>
          <span class="text-xs text-slate-700 font-mono tabular-nums w-28 text-right">耗时(ms)：{{ span.duration_ms ?? 0 }}</span>
        </div>
        <div v-if="span.error" class="mt-2 rounded-md bg-red-50 border border-red-200 text-red-700 text-[12px] px-3 py-2 whitespace-pre-wrap">
          <div class="text-[11px] uppercase tracking-wide text-red-400 mb-1">错误信息</div>
          {{ span.error }}
        </div>
        <div class="mt-2 grid grid-cols-2 gap-3" v-if="(span.attrs && Object.keys(span.attrs).length) || (span.events?.length)">
          <div v-if="span.attrs && Object.keys(span.attrs).length" class="min-w-0">
            <div class="text-[10px] uppercase tracking-wide text-slate-400 mb-1">属性</div>
            <div class="rounded-md border border-slate-200 p-2 space-y-1.5 bg-slate-50">
              <div
                v-for="item in formatAttrEntries(span.attrs)"
                :key="item.k"
                class="flex items-start gap-2 text-[11px]"
              >
                <span class="shrink-0 font-medium text-slate-500 px-1.5 py-0.5 bg-white border border-slate-200 rounded">{{ item.k }}</span>
                <span class="text-slate-700 break-all font-mono">{{ item.v }}</span>
              </div>
            </div>
          </div>
          <div v-if="span.events?.length" class="min-w-0">
            <div class="text-[10px] uppercase tracking-wide text-slate-400 mb-1">事件 ({{ span.events.length }})</div>
            <pre class="text-[10px] font-mono bg-slate-50 border border-slate-200 rounded p-2 text-slate-600 overflow-auto max-h-48 m-0">{{ formatEvents(span.events) }}</pre>
          </div>
        </div>
      </div>
    </div>
    <div v-if="expanded && span.children?.length" class="divide-y divide-slate-100">
      <div class="px-4 py-1 text-[10px] uppercase tracking-wide text-slate-400 bg-slate-50/50" :style="{ paddingLeft: `${16 + (depth + 1) * 20}px` }">子步骤（{{ span.children.length }}）</div>
      <trace-span-node
        v-for="c in span.children"
        :key="c.span_id"
        :span="c"
        :depth="depth + 1"
      />
    </div>
  </div>
</template>
