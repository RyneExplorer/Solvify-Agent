<template>
  <details v-if="plan.tasks?.length" class="task-plan mb-3" aria-label="深度任务计划">
    <summary class="plan-header">
      <div class="plan-heading">
        <svg class="detail-chevron" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="m9 5 7 7-7 7"/></svg>
        <span class="plan-title">任务计划</span>
        <span class="plan-count">{{ completed }} / {{ plan.tasks.length }} 项完成</span>
      </div>
      <span class="state-badge" :class="`state-${plan.status}`" role="status">{{ statusLabel(plan.status) }}</span>
    </summary>
    <div class="plan-body">
      <p class="plan-subtitle">{{ strategyLabel }}</p>
      <div class="plan-progress" role="progressbar" aria-label="任务完成进度" :aria-valuenow="completed" :aria-valuemax="plan.tasks.length" aria-valuemin="0"><span :style="{ width: `${completed / plan.tasks.length * 100}%` }" /></div>
      <ol class="task-list">
        <li v-for="(task, index) in plan.tasks" :key="task.id" class="task-row" :class="{ 'task-active': task.status === 'running' }">
          <span class="task-node" :class="`node-${task.status}`" aria-hidden="true">
            <svg v-if="task.status === 'success'" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="m5 12 4 4L19 6"/></svg>
            <span v-else-if="task.status === 'running'" class="task-spinner" />
            <span v-else-if="task.status === 'error'">!</span>
            <span v-else-if="task.status === 'canceled'">−</span>
            <span v-else>{{ index + 1 }}</span>
          </span>
          <div class="task-body">
            <div class="task-heading"><span class="task-title">{{ task.title }}</span><span class="task-status" :class="`text-state-${task.status}`">{{ statusLabel(task.status) }}</span></div>
            <div class="task-meta">
              <span>{{ agentLabel(task.agent) }}</span>
              <span v-if="task.added_version && task.added_version > 1" class="task-added">版本 {{ task.added_version }} 新增</span>
              <template v-if="task.depends_on?.length"><span class="meta-divider" aria-hidden="true" /><span>依赖</span><span v-for="id in task.depends_on" :key="id" class="dependency-tag" :title="dependencyTitle(id)" :aria-label="`依赖：${dependencyTitle(id)}`">{{ dependencyLabel(id) }}</span></template>
            </div>
            <details v-if="task.summary" class="task-result">
              <summary><svg class="detail-chevron" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="m9 5 7 7-7 7"/></svg>{{ task.status === 'error' ? '查看失败原因' : task.status === 'success' ? '查看任务结果' : '查看任务记录' }}</summary>
              <p class="result-content">{{ task.summary }}</p>
            </details>
          </div>
        </li>
      </ol>
      <details v-if="plan.version || plan.phase || plan.round || plan.replan_reason" class="plan-details">
        <summary><svg class="detail-chevron" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="m9 5 7 7-7 7"/></svg>计划详情<span v-if="plan.version" class="plan-version">v{{ plan.version }}</span><span v-if="plan.replan_reason" class="ml-auto">已调整计划</span></summary>
        <div class="plan-details-content">
          <div class="flex flex-wrap gap-x-4 gap-y-1"><span v-if="plan.phase">{{ phaseLabel }}</span><span v-if="plan.version">计划版本 {{ plan.version }}</span><span v-if="plan.round">执行批次 {{ plan.round }}</span></div>
          <p v-if="plan.replan_reason" class="mt-2 whitespace-pre-wrap break-words">调整原因：{{ plan.replan_reason }}</p>
        </div>
      </details>
    </div>
  </details>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { ExecutionPlan } from '@/types/chat'

const props = defineProps<{ plan: ExecutionPlan }>()
const completed = computed(() => props.plan.tasks.filter(task => task.status === 'success').length)
const strategyLabel = computed(() => ({ direct: '直接执行', sequential: '顺序执行', parallel: '并行执行' } as Record<string, string>)[props.plan.strategy] ?? '任务执行')
const phaseLabel = computed(() => ({ planning: '规划任务', executing: '执行任务', replanning: '评估与重规划', waiting: '等待审批', completed: '已完成', failed: '执行失败', canceled: '已取消' } as Record<string, string>)[props.plan.phase ?? ''] ?? '任务处理中')

function statusLabel(status: string) {
  return ({ pending: '待执行', running: '执行中', success: '已完成', error: '执行失败', waiting: '等待审批', canceled: '已取消' } as Record<string, string>)[status] ?? '待执行'
}
function agentLabel(agent: string) {
  return ({ knowledge_research: '知识库研究', web_research: '外部工具研究', document_operation: '文档操作', synthesis_review: '汇总与校验' } as Record<string, string>)[agent] ?? '任务处理'
}
function dependencyTitle(id: string) {
  return props.plan.tasks.find(task => task.id === id)?.title ?? id
}
function dependencyLabel(id: string) {
  const index = props.plan.tasks.findIndex(task => task.id === id)
  return index < 0 ? id : `步骤 ${index + 1}`
}
</script>

<style scoped>
.task-plan { overflow: hidden; border: 1px solid #e2e8f0; border-radius: 10px; background: #fafbfc; }
.plan-header { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 12px 14px; }
.plan-header:hover { background: #f1f5f9; }
.plan-header:focus-visible { outline-offset: -2px; }
.plan-heading { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; min-width: 0; }
.task-plan[open] > .plan-header .detail-chevron { transform: rotate(90deg); }
.plan-title { font-size: 12px; font-weight: 500; line-height: 1.5; color: #475569; }
.plan-count { font-size: 11px; color: #64748b; }
.plan-body { border-top: 1px solid #e2e8f0; background: #fff; }
.plan-subtitle { padding: 12px 20px 10px; font-size: 11px; color: #64748b; }
.state-badge { flex-shrink: 0; color: #64748b; font-size: 11px; line-height: 1.5; }
.state-success { color: #059669; }
.state-running { color: #2563eb; }
.state-error { color: #dc2626; }
.state-waiting { color: #b45309; }
.plan-progress { height: 3px; margin: 0 20px; overflow: hidden; border-radius: 4px; background: #f1f5f9; }
.plan-progress > span { display: block; height: 100%; border-radius: inherit; background: #10b981; transition: width .3s ease; }
.task-list { padding: 10px 12px; }
.task-row { position: relative; display: flex; align-items: flex-start; gap: 12px; padding: 12px 8px; border-radius: 9px; }
.task-row:not(:last-child)::after { position: absolute; top: 36px; bottom: -10px; left: 19px; width: 1px; background: #e2e8f0; content: ''; }
.task-active { background: #f8fafc; }
.task-node { position: relative; z-index: 1; display: grid; place-items: center; width: 24px; height: 24px; flex-shrink: 0; border: 1px solid #e2e8f0; border-radius: 50%; background: #f8fafc; color: #64748b; font-size: 10px; font-weight: 600; }
.node-success { border-color: #d1fae5; background: #ecfdf5; color: #059669; }
.node-running { border-color: #bfdbfe; background: #eff6ff; color: #2563eb; }
.node-error { border-color: #fecaca; background: #fef2f2; color: #dc2626; }
.node-waiting { border-color: #fde68a; background: #fffbeb; color: #b45309; }
.node-canceled { color: #94a3b8; }
.task-spinner { width: 11px; height: 11px; border: 1.5px solid #bfdbfe; border-top-color: #2563eb; border-radius: 50%; animation: plan-spin 1s linear infinite; }
.task-body { flex: 1; min-width: 0; padding-top: 2px; }
.task-heading { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; }
.task-title { min-width: 0; color: #334155; font-size: 13px; font-weight: 500; line-height: 1.6; overflow-wrap: anywhere; }
.task-status { flex-shrink: 0; color: #64748b; font-size: 11px; }
.text-state-success { color: #059669; }
.text-state-running { color: #2563eb; }
.text-state-error { color: #dc2626; }
.text-state-waiting { color: #b45309; }
.task-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; margin-top: 5px; color: #64748b; font-size: 11px; }
.task-added { color: #0284c7; }
.meta-divider { width: 1px; height: 10px; margin: 0 2px; background: #e2e8f0; }
.dependency-tag { max-width: 100%; overflow-wrap: anywhere; border: 1px solid #e2e8f0; border-radius: 4px; padding: 0 5px; color: #64748b; line-height: 1.6; }
.task-result { margin-top: 8px; color: #64748b; font-size: 11px; }
summary { display: flex; align-items: center; gap: 5px; list-style: none; cursor: pointer; user-select: none; }
summary::-webkit-details-marker { display: none; }
summary:hover { color: #334155; }
summary:focus-visible { outline: 2px solid #10b981; outline-offset: 4px; border-radius: 3px; }
.detail-chevron { flex-shrink: 0; transition: transform .15s; }
details[open] > summary > .detail-chevron { transform: rotate(90deg); }
.result-content { max-height: 260px; overflow: auto; margin-top: 9px; border: 1px solid #e2e8f0; border-radius: 8px; background: #f8fafc; padding: 12px; white-space: pre-wrap; overflow-wrap: anywhere; line-height: 1.8; }
.plan-details { border-top: 1px solid #f1f5f9; background: #fafbfc; color: #64748b; font-size: 11px; }
.plan-details > summary { padding: 11px 20px; }
.plan-version { margin-left: 3px; color: #94a3b8; }
.plan-details-content { padding: 0 20px 14px; line-height: 1.8; }
@keyframes plan-spin { to { transform: rotate(360deg); } }
@media (max-width: 480px) {
  .plan-header { padding: 11px 10px; }
  .plan-progress { margin: 0 12px; }
  .task-list { padding: 6px; }
  .task-row { gap: 9px; padding: 11px 6px; }
  .task-row:not(:last-child)::after { left: 17px; }
  .task-heading { flex-wrap: wrap; gap: 2px 8px; }
  .plan-details > summary { padding: 10px 12px; }
  .plan-details-content { padding: 0 12px 12px; }
}
@media (prefers-reduced-motion: reduce) {
  .task-spinner { animation: none; }
  .plan-progress > span, .detail-chevron { transition: none; }
}
</style>
