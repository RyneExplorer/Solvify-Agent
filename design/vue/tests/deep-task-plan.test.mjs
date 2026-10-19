import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile, writeFile, unlink } from 'node:fs/promises'
import { parse, compileScript } from '@vue/compiler-sfc'
import { transformWithEsbuild } from 'vite'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'
import { applyExecutionPlanEvent, finishExecutionPlan } from '../src/composables/chatExecutionPlan.ts'
import { streamRequest } from '../src/api/client.ts'

test('错误标题和解释置于同一个提示框，安全显示并去除重复解释', async () => {
  const filename = new URL('../src/components/ChatFailure.vue', import.meta.url)
  const { descriptor } = parse(await readFile(filename, 'utf8'), { filename: filename.pathname })
  const compiled = compileScript(descriptor, { id: 'chat-failure-test', inlineTemplate: true })
  const { code } = await transformWithEsbuild(compiled.content, 'ChatFailure.ts', { loader: 'ts' })
  const generated = new URL(`./.chat-failure-${process.pid}.mjs`, import.meta.url)
  await writeFile(generated, code)
  try {
    const { default: component } = await import(generated.href)
    const html = await renderToString(createSSRApp(component, { title: '执行未完成', detail: '<script>说明</script>' }))
    assert.equal((html.match(/role="alert"/g) ?? []).length, 1)
    assert.ok(html.includes('执行未完成') && html.includes('&lt;script&gt;说明&lt;/script&gt;'))
    const repeated = await renderToString(createSSRApp(component, { title: '执行未完成', detail: '执行未完成' }))
    assert.equal((repeated.match(/执行未完成/g) ?? []).length, 1)
  } finally {
    await unlink(generated)
  }
})

test('SSE 建立前的业务错误保持后端提示，避免误判为空流', async () => {
  const originalFetch = globalThis.fetch
  const originalStorage = globalThis.localStorage
  globalThis.localStorage = { getItem: () => null }
  globalThis.fetch = async () => new Response(JSON.stringify({ code: 404, message: '会话不存在' }), {
    status: 200, headers: { 'Content-Type': 'application/json' },
  })
  try {
    await assert.rejects(streamRequest('/chat/sessions/missing/messages', {}), /会话不存在/)
  } finally {
    globalThis.fetch = originalFetch
    globalThis.localStorage = originalStorage
  }
})

test('失败与取消收尾保留已完成任务并结束等待任务，不修改历史快照', () => {
  const plan = structuredClone(initialPlan)
  plan.tasks[0].status = 'success'
  plan.tasks[1].status = 'waiting'
  for (const status of ['error', 'canceled']) {
    const finished = finishExecutionPlan(plan, status)
    assert.equal(finished.status, status)
    assert.equal(finished.phase, status === 'error' ? 'failed' : 'canceled')
    assert.equal(finished.tasks[0].status, 'success')
    assert.equal(finished.tasks[1].status, 'canceled')
    assert.equal(finished.tasks[2].status, 'canceled')
    assert.equal(plan.tasks[1].status, 'waiting')
  }
})

const initialPlan = {
  id: 'p', strategy: 'parallel', status: 'running', tasks: [
    { id: 'a', agent: 'knowledge_research', title: '研究 A', status: 'pending' },
    { id: 'b', agent: 'web_research', title: '研究 B', status: 'pending' },
    { id: 's', agent: 'synthesis_review', title: '汇总', status: 'pending', depends_on: ['a', 'b'] },
  ],
}

test('SSE 任务增量保留同时运行的任务、依赖和历史快照', () => {
  let plan = applyExecutionPlanEvent(null, { type: 'plan', plan: initialPlan })
  const snapshot = plan
  for (const id of ['a', 'b']) {
    plan = applyExecutionPlanEvent(plan, { type: 'task_updated', plan_id: 'p', task: { ...plan.tasks.find(task => task.id === id), status: 'running' } })
  }
  assert.equal(plan.tasks.filter(task => task.status === 'running').length, 2)
  assert.equal(snapshot.tasks[0].status, 'pending')
  assert.deepEqual(plan.tasks[2].depends_on, ['a', 'b'])
  assert.equal(applyExecutionPlanEvent(plan, { type: 'task_updated', plan_id: 'other', task: { id: 'x' } }), plan)
  plan = applyExecutionPlanEvent(plan, { type: 'task_result', plan_id: 'p', task: { ...plan.tasks[0], status: 'success', summary: '研究结果' } })
  assert.equal(plan.tasks[1].status, 'running')
  assert.equal(plan.tasks[0].summary, '研究结果')
})

test('实时和历史任务卡渲染状态、中文角色、依赖与安全文本', async () => {
  const filename = new URL('../src/components/DeepTaskPlan.vue', import.meta.url)
  const { descriptor } = parse(await readFile(filename, 'utf8'), { filename: filename.pathname })
  const compiled = compileScript(descriptor, { id: 'deep-task-plan-test', inlineTemplate: true })
  const { code } = await transformWithEsbuild(compiled.content, 'DeepTaskPlan.ts', { loader: 'ts' })
  const generated = new URL(`./.deep-task-plan-${process.pid}.mjs`, import.meta.url)
  await writeFile(generated, code)
  try {
    const { default: component } = await import(generated.href)
    const plan = structuredClone(initialPlan)
    plan.status = 'waiting'
    plan.version = 2
    plan.phase = 'replanning'
    plan.round = 3
    plan.replan_reason = '补充来源验证'
    plan.tasks[2].added_version = 2
    plan.tasks[0].status = 'success'
    plan.tasks[1].status = 'waiting'
    plan.tasks[1].summary = '<script>alert(1)</script>'
    const html = await renderToString(createSSRApp(component, { plan }))
    for (const text of ['任务计划', '1 / 3 项完成', '并行执行', '已完成', '等待审批', '依赖：研究 A', '依赖：研究 B', '步骤 1', '步骤 2', '外部工具研究', '计划版本 2', '执行批次 3', '评估与重规划', '调整原因：补充来源验证', '版本 2 新增']) assert.ok(html.includes(text), text)
    assert.match(html, /role="progressbar"[^>]*aria-valuenow="1"[^>]*aria-valuemax="3"/)
    assert.ok(!html.includes('<details open'), '结果与计划详情默认折叠')
    assert.ok(!html.includes('<script>alert(1)</script>'))
    assert.ok(html.includes('&lt;script&gt;'))
    plan.status = 'error'
    plan.tasks[1].status = 'error'
    const failed = await renderToString(createSSRApp(component, { plan }))
    assert.ok(failed.includes('查看失败原因'))
    const legacy = await renderToString(createSSRApp(component, { plan: initialPlan }))
    assert.ok(!legacy.includes('计划版本'))
  } finally {
    await unlink(generated)
  }
})

test('重规划保留终态记录和新任务标记，拒绝过期快照及任务增量', () => {
  const initial = { ...structuredClone(initialPlan), version: 1, phase: 'executing', round: 1 }
  initial.tasks[0] = { ...initial.tasks[0], status: 'success', summary: '可信结果' }
  initial.tasks[1] = { ...initial.tasks[1], status: 'error', summary: '外部研究失败' }
  let plan = applyExecutionPlanEvent(null, { type: 'plan', plan: initial })
  const next = {
    ...structuredClone(initial), version: 2, phase: 'replanning', round: 2, replan_reason: '替代研究',
    tasks: [initial.tasks[0], initial.tasks[1], { ...initial.tasks[2], status: 'canceled' },
      { id: 'retry', agent: 'web_research', title: '替代研究', status: 'pending' }],
  }
  plan = applyExecutionPlanEvent(plan, { type: 'plan', plan: next })
  assert.equal(plan.version, 2)
  assert.equal(plan.tasks[0].added_version, 1)
  assert.equal(plan.tasks[0].summary, '可信结果')
  assert.equal(plan.tasks[1].status, 'error')
  assert.equal(plan.tasks[2].status, 'canceled')
  assert.equal(plan.tasks[3].added_version, 2)
  assert.equal(plan.replan_reason, '替代研究')
  assert.equal(applyExecutionPlanEvent(plan, { type: 'plan', plan: initial }), plan)
  assert.equal(applyExecutionPlanEvent(plan, { type: 'task_updated', plan_id: 'p', plan_version: 1, task: { ...initial.tasks[0], status: 'running' } }), plan)
  assert.equal(applyExecutionPlanEvent(plan, { type: 'task_updated', plan_id: 'p', task: { ...initial.tasks[0], status: 'running' } }), plan)
  plan = applyExecutionPlanEvent(plan, { type: 'task_updated', plan_id: 'p', plan_version: 2, phase: 'executing', round: 3, task: { ...next.tasks[3], status: 'running' } })
  assert.equal(plan.phase, 'executing')
  assert.equal(plan.round, 3)
  assert.equal(plan.tasks[3].status, 'running')
})
