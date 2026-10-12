<template>
  <section v-if="plan.tasks?.length" class="mb-3 rounded-xl border border-slate-200 bg-slate-50/70 px-3 py-2.5" aria-label="深度任务计划" aria-live="polite">
    <div class="mb-2 flex items-center justify-between gap-3 text-xs text-slate-500">
      <span class="font-medium text-slate-600">深度任务 · {{ completed }}/{{ plan.tasks.length }}</span>
      <span>{{ strategyLabel }} · {{ statusLabel(plan.status) }}</span>
    </div>
    <div v-if="plan.version || plan.phase || plan.round" class="mb-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-slate-500">
      <span v-if="plan.phase">{{ phaseLabel }}</span>
      <span v-if="plan.version">计划版本 {{ plan.version }}</span>
      <span v-if="plan.round">第 {{ plan.round }} 轮</span>
    </div>
    <p v-if="plan.replan_reason" class="mb-2 whitespace-pre-wrap break-words text-[11px] text-slate-500">调整原因：{{ plan.replan_reason }}</p>
    <ol class="space-y-2">
      <li v-for="task in plan.tasks" :key="task.id" class="flex items-start gap-2 text-xs">
        <span class="mt-0.5 h-3 w-3 shrink-0 rounded-full" :class="statusClass(task.status)" aria-hidden="true" />
        <div class="min-w-0 flex-1">
          <div class="flex items-start justify-between gap-2">
            <span class="break-words text-slate-700">
              {{ task.title }}
              <span v-if="task.added_version && task.added_version > 1" class="ml-1 text-[10px] text-sky-600">版本 {{ task.added_version }} 新增</span>
            </span>
            <span class="shrink-0 text-[11px] text-slate-500">{{ statusLabel(task.status) }}</span>
          </div>
          <div class="mt-0.5 text-[11px] text-slate-400">{{ agentLabel(task.agent) }}</div>
          <div v-if="task.depends_on?.length" class="mt-0.5 text-[11px] text-slate-400">依赖：{{ task.depends_on.map(dependencyTitle).join('、') }}</div>
          <details v-if="task.summary" class="mt-1 text-[11px] text-slate-500">
            <summary class="cursor-pointer">{{ task.status === 'error' ? '查看失败原因' : '查看任务结果' }}</summary>
            <p class="mt-1 whitespace-pre-wrap break-words">{{ task.summary }}</p>
          </details>
        </div>
      </li>
    </ol>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ExecutionPlan } from '@/types/chat'

const props = defineProps<{ plan: ExecutionPlan }>()
const completed = computed(() => props.plan.tasks.filter(task => task.status === 'success').length)
const strategyLabel = computed(() => ({ direct: '直接执行', sequential: '顺序执行', parallel: '并行执行' } as Record<string, string>)[props.plan.strategy] ?? '任务执行')
const phaseLabel = computed(() => ({ planning: '规划任务', executing: '执行任务', replanning: '评估与重规划', waiting: '等待审批', completed: '已完成', failed: '执行失败', canceled: '已取消' } as Record<string, string>)[props.plan.phase ?? ''] ?? '任务处理中')

function statusLabel(status: string) {
  return ({ pending: '等待依赖', running: '执行中', success: '已完成', error: '失败', waiting: '等待审批', canceled: '已取消' } as Record<string, string>)[status] ?? '待执行'
}
function agentLabel(agent: string) {
  return ({ knowledge_research: '知识库研究', web_research: '外部工具研究', document_operation: '文档操作', synthesis_review: '汇总与校验' } as Record<string, string>)[agent] ?? '任务处理'
}
function dependencyTitle(id: string) {
  return props.plan.tasks.find(task => task.id === id)?.title ?? id
}
function statusClass(status: string) {
  return ({ success: 'bg-emerald-500', running: 'bg-sky-500 animate-pulse', error: 'bg-red-500', waiting: 'bg-amber-500', canceled: 'bg-slate-400' } as Record<string, string>)[status] ?? 'border border-slate-300 bg-white'
}
</script>
