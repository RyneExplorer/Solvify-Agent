<template>
  <section v-if="normalizedSteps.length" class="execution-trace mb-3" aria-label="执行过程">
    <details class="trace-disclosure">
      <summary class="trace-toggle">
        <svg class="trace-chevron" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="m9 5 7 7-7 7"/></svg>
        <span class="trace-label">执行过程</span>
        <span class="trace-count">{{ normalizedSteps.length }} 条记录</span>
        <span class="trace-state" :class="`trace-state-${overallStatus}`"><span v-if="overallStatus === 'running'" class="trace-spinner" />{{ overallLabel }}</span>
      </summary>
      <ol class="trace-list">
        <li v-for="(step, index) in entries" :key="index" class="trace-row" :class="`trace-row-${step.status}`">
          <span class="trace-node" aria-hidden="true">
            <span v-if="step.status === 'running'" class="trace-spinner" />
            <svg v-else-if="step.status === 'success'" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="m5 12 4 4L19 6"/></svg>
            <span v-else-if="step.status === 'error' || step.status === 'warning'">!</span>
            <span v-else>−</span>
          </span>
          <div class="trace-body">
            <details v-if="step.detail || step.resultDetail" class="trace-entry">
              <summary class="entry-toggle"><span class="entry-title">{{ step.title }}</span><span class="entry-status">{{ statusLabel(step.status) }}</span><svg class="trace-chevron" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="m9 5 7 7-7 7"/></svg></summary>
              <div class="entry-detail">
                <p v-if="step.resultDetail" class="entry-failure">{{ step.resultDetail }}</p>
                <template v-if="step.planTasks">
                  <p class="detail-label">{{ strategyLabel(step.data?.strategy) }} · {{ step.planTasks.length }} 项任务</p>
                  <ol class="trace-plan-tasks"><li v-for="(task, ti) in step.planTasks" :key="ti"><span class="trace-task-index">{{ ti + 1 }}</span><div class="min-w-0"><p class="trace-task-title">{{ task.title }}</p><p v-if="task.description" class="trace-task-description">{{ task.description }}</p></div></li></ol>
                  <details class="raw-detail"><summary>查看原始记录</summary><pre>{{ formatValue(step.data) }}</pre></details>
                </template>
                <dl v-else-if="step.fields" class="trace-fields"><div v-for="([key, value]) in step.fields" :key="key"><dt>{{ key }}</dt><dd>{{ formatValue(value) }}</dd></div></dl>
                <pre v-else>{{ step.text }}</pre>
              </div>
            </details>
            <div v-else class="entry-toggle"><span class="entry-title">{{ step.title }}</span><span class="entry-status">{{ statusLabel(step.status) }}</span></div>
          </div>
        </li>
      </ol>
    </details>
    <p v-if="activeStep" class="trace-current" role="status">{{ activeStep.title }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { normalizeExecutionTrace } from '../composables/chatExecutionTrace'
import type { ExecutionTraceStep } from '../composables/chatExecutionTrace'

interface PlanTaskDetail { title: string; description?: string }
const props = withDefaults(defineProps<{ steps: ExecutionTraceStep[]; active?: boolean; status?: string }>(), { active: false })
const normalizedSteps = computed(() => normalizeExecutionTrace(props.steps, props.active))
const activeStep = computed(() => props.active ? [...normalizedSteps.value].reverse().find(step => step.status === 'running') : undefined)
const overallStatus = computed(() => props.status === 'waiting' ? 'warning' : props.active ? 'running' : props.status === 'error' ? 'error' : props.status === 'success' ? 'success' : props.status === 'canceled' ? 'canceled' : 'idle')
const overallLabel = computed(() => ({ running: '进行中', error: '执行未完成', warning: '等待审批', success: '已完成', canceled: '已取消', idle: '查看记录' })[overallStatus.value])

// 这里只解析展示数据，不从模型提供的计划中推断可信执行状态。
const entries = computed(() => normalizedSteps.value.map(step => {
  const text = step.detail?.trim() ?? ''
  let data: unknown
  try { data = JSON.parse(text.replace(/^```(?:json)?\s*\n([\s\S]*?)\n```$/i, '$1')) } catch { /* 未完成或非JSON内容保留原文。 */ }
  const object = data !== null && typeof data === 'object' && !Array.isArray(data) ? data as Record<string, unknown> : null
  const planTasks = object && Array.isArray(object.tasks) && object.tasks.length > 0 && object.tasks.every(isPlanTask) ? object.tasks as PlanTaskDetail[] : null
  return { ...step, text: data !== undefined ? formatValue(data) : text, data: object, planTasks, fields: object && !planTasks && Object.keys(object).length ? Object.entries(object) : null }
}))

function isPlanTask(value: unknown): value is PlanTaskDetail {
  if (!value || typeof value !== 'object') return false
  const task = value as Record<string, unknown>
  return typeof task.title === 'string' && (task.description === undefined || typeof task.description === 'string')
}
function formatValue(value: unknown): string { return typeof value === 'string' ? value : JSON.stringify(value, null, 2) ?? '' }
function strategyLabel(strategy: unknown): string { return typeof strategy === 'string' ? ({ parallel: '并行执行', sequential: '顺序执行', direct: '直接执行' } as Record<string, string>)[strategy] ?? '任务计划' : '任务计划' }
function statusLabel(status: string): string { return ({ running: '执行中', success: '已完成', warning: '需关注', error: '失败', canceled: '已取消', waiting: '等待审批', ended: '已结束', unknown: '状态未确认' } as Record<string, string>)[status] ?? '待执行' }
</script>

<style scoped>
.execution-trace { min-width: 0; border: 1px solid #e2e8f0; border-radius: 10px; background: #fafbfc; color: #64748b; font-size: 12px; }
summary { list-style: none; cursor: pointer; user-select: none; }
summary::-webkit-details-marker { display: none; }
summary:focus-visible { outline: 2px solid #10b981; outline-offset: -2px; border-radius: 5px; }
.trace-toggle { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; padding: 12px 14px; border-radius: inherit; transition: background .15s; }
.trace-toggle:hover { background: #f1f5f9; }
.trace-label { color: #475569; font-weight: 500; }
.trace-count { color: #64748b; font-size: 11px; }
.trace-state { display: inline-flex; align-items: center; gap: 6px; margin-left: auto; color: #64748b; font-size: 11px; }
.trace-state-running { color: #2563eb; }
.trace-state-error { color: #dc2626; }
.trace-state-warning { color: #b45309; }
.trace-state-success { color: #059669; }
.trace-chevron { flex-shrink: 0; color: #94a3b8; transition: transform .15s; }
details[open] > summary > .trace-chevron { transform: rotate(90deg); }
.trace-current { padding: 0 14px 10px 34px; color: #64748b; font-size: 11px; overflow-wrap: anywhere; }
.trace-disclosure[open] + .trace-current { display: none; }
.trace-list { max-height: 420px; overflow: auto; overscroll-behavior: contain; border-top: 1px solid #e2e8f0; padding: 8px 14px 12px; }
.trace-row { position: relative; display: flex; align-items: flex-start; gap: 10px; }
.trace-row:not(:last-child)::after { position: absolute; top: 24px; bottom: -8px; left: 8px; width: 1px; background: #e2e8f0; content: ''; }
.trace-node { position: relative; z-index: 1; display: grid; place-items: center; flex-shrink: 0; width: 18px; height: 18px; margin-top: 9px; border-radius: 50%; background: #f1f5f9; color: #94a3b8; font-size: 10px; }
.trace-row-success .trace-node { background: #ecfdf5; color: #059669; }
.trace-row-running .trace-node { background: #eff6ff; color: #2563eb; }
.trace-row-error .trace-node { background: #fef2f2; color: #dc2626; }
.trace-row-warning .trace-node, .trace-row-waiting .trace-node { background: #fffbeb; color: #b45309; }
.trace-body { flex: 1; min-width: 0; }
.entry-toggle { display: flex; align-items: center; gap: 8px; min-height: 36px; padding: 7px 0; }
.entry-title { flex: 1; min-width: 0; color: #475569; line-height: 1.7; overflow-wrap: anywhere; }
summary.entry-toggle:hover .entry-title { color: #0f172a; }
.entry-status { flex-shrink: 0; color: #64748b; font-size: 11px; }
.trace-row-error .entry-status { color: #dc2626; }
.trace-row-warning .entry-status, .trace-row-waiting .entry-status { color: #b45309; }
.trace-row-running .entry-status { color: #2563eb; }
.entry-detail { max-height: 280px; overflow: auto; margin: 0 0 10px; padding: 12px; border: 1px solid #e2e8f0; border-radius: 8px; background: #fff; line-height: 1.8; overflow-wrap: anywhere; }
.entry-failure { margin-bottom: 8px; padding-bottom: 8px; border-bottom: 1px solid #fee2e2; color: #dc2626; white-space: pre-wrap; }
pre, dd { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; font: 11px/1.8 ui-monospace, SFMono-Regular, Consolas, monospace; color: #475569; }
.trace-fields > div { display: grid; grid-template-columns: minmax(0, 100px) minmax(0, 1fr); gap: 12px; padding: 5px 0; }
.trace-fields dt { color: #64748b; }
.detail-label { color: #64748b; font-size: 10px; }
.trace-plan-tasks > li { display: flex; align-items: flex-start; gap: 8px; padding-top: 12px; }
.trace-task-index { display: grid; place-items: center; flex-shrink: 0; width: 18px; height: 18px; border-radius: 5px; background: #f1f5f9; font-size: 10px; }
.trace-task-title { color: #334155; font-weight: 500; }
.trace-task-description { margin-top: 5px; color: #64748b; }
.raw-detail { margin-top: 12px; border-top: 1px solid #f1f5f9; padding-top: 9px; font-size: 10px; }
.raw-detail summary { color: #64748b; }
.raw-detail pre { margin-top: 8px; }
.trace-spinner { width: 10px; height: 10px; flex-shrink: 0; border: 1.5px solid #bfdbfe; border-top-color: #2563eb; border-radius: 50%; animation: trace-spin 1s linear infinite; }
@keyframes trace-spin { to { transform: rotate(360deg); } }
@media (max-width: 480px) {
  .trace-toggle { gap: 6px; padding: 11px 10px; }
  .trace-list { padding: 6px 10px 10px; }
  .trace-fields > div { grid-template-columns: minmax(0, 1fr); gap: 2px; }
}
@media (prefers-reduced-motion: reduce) {
  .trace-spinner { animation: none; }
  .trace-chevron, .trace-toggle { transition: none; }
}
</style>
