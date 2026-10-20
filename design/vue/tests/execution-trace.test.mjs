import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { parse, compileScript } from '@vue/compiler-sfc'
import { transformWithEsbuild } from 'vite'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'

async function loadComponent(name) {
  const filename = new URL(`../src/components/${name}.vue`, import.meta.url)
  const { descriptor } = parse(await readFile(filename, 'utf8'), { filename: filename.pathname })
  const compiled = compileScript(descriptor, { id: `${name}-test`, inlineTemplate: true })
  const source = compiled.content.replace(/from ['"]vue['"]/g, `from '${import.meta.resolve('vue')}'`).replace("from '../composables/chatExecutionTrace'", `from '${new URL('../src/composables/chatExecutionTrace.ts', import.meta.url).href}'`)
  const { code } = await transformWithEsbuild(source, `${name}.ts`, { loader: 'ts' })
  return (await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`)).default
}
const Trace = await loadComponent('ExecutionTrace')
const Plan = await loadComponent('DeepTaskPlan')
const render = (component, props) => renderToString(createSSRApp(component, props))

test('执行过程默认收起，失败、取消、审批和未知状态不伪装为成功', async () => {
  const steps = ['success', 'error', 'warning', 'canceled', 'waiting', 'unknown'].map(status => ({ title: `工具 ${status}`, status, detail: '记录内容' }))
  const html = await render(Trace, { steps })
  for (const text of ['执行过程', '6 条记录', '查看记录', '已完成', '失败', '需关注', '已取消', '等待审批', '状态未确认']) assert.ok(html.includes(text), text)
  assert.doesNotMatch(html, /<details[^>]*\bopen(?:\s|>|=)/)
  assert.equal((await render(Trace, { steps: [] })).replace(/<!--[\s\S]*?-->/g, ''), '')
})

test('实时执行在收起时保留当前步骤，不把历史running解释成实时执行', async () => {
  const steps = [{ title: '旧步骤', status: 'success' }, { title: '正在检索知识库', status: 'running' }]
  const live = await render(Trace, { steps, active: true })
  assert.ok(live.includes('进行中'))
  assert.match(live, /class="trace-current"[^>]*role="status"[^>]*>正在检索知识库/)
  const history = await render(Trace, { steps })
  assert.ok(history.includes('查看记录'))
  assert.doesNotMatch(history, /class="trace-current"/)
})

test('计划JSON显示任务标题和描述，原始记录继续可查且内容安全转义', async () => {
  const detail = JSON.stringify({ strategy: 'parallel', tasks: [{ title: '检索知识库', description: '<script>说明</script>' }, { title: '联网查询' }] })
  const html = await render(Trace, { steps: [{ title: '生成任务计划', status: 'success', detail }] })
  for (const text of ['并行执行 · 2 项任务', '检索知识库', '联网查询', '查看原始记录', '&lt;script&gt;说明&lt;/script&gt;']) assert.ok(html.includes(text), text)
  assert.ok(!html.includes('<script>说明</script>'))
  assert.equal((html.match(/class="trace-task-title"/g) ?? []).length, 2)
})

test('工具参数、嵌套JSON、代码围栏、数组和不完整JSON均可阅读', async () => {
  const details = [
    '{"query":"Go 并发","limit":10,"filters":{"enabled":true}}',
    '```json\n{"query":"围栏参数"}\n```',
    '[1, {"name":"数组值"}]',
    '{"tasks":[null, {"title":1}]}',
    '{"query":"尚未完成',
    '<img src=x onerror=alert(1)>',
    'null', 'true', '{}',
  ]
  const html = await render(Trace, { steps: details.map(detail => ({ title: '调用工具', status: 'success', detail })) })
  for (const text of ['<dt>query</dt>', 'Go 并发', '<dt>limit</dt>', '>10<', 'enabled', '围栏参数', '数组值', '尚未完成', '&lt;img', '>null<', '>true<', '>{}<']) assert.ok(html.includes(text), text)
  assert.ok(!html.includes('<img src=x'))
  assert.ok(!html.includes('trace-plan-tasks'), '非法任务结构按普通JSON展示')
})

test('任务计划进度只统计成功项，保留失败、取消与未知依赖的真实记录', async () => {
  const tasks = ['success', 'error', 'canceled', 'waiting', 'running', 'pending'].map((status, i) => ({ id: `t${i}`, title: `任务${i}`, status, agent: 'knowledge_research', summary: '保留的结果', depends_on: i === 5 ? ['t0', 'legacy-id'] : [] }))
  const html = await render(Plan, { plan: { id: 'p', strategy: 'parallel', status: 'error', tasks } })
  assert.ok(html.includes('1 / 6 项完成'))
  for (const text of ['执行失败', '已取消', '等待审批', '执行中', '待执行', '查看失败原因', '查看任务记录', 'legacy-id', '依赖：任务0']) assert.ok(html.includes(text), text)
  assert.match(html, /aria-valuenow="1"[^>]*aria-valuemax="6"/)
  assert.equal((await render(Plan, { plan: { tasks: [] } })).replace(/<!--[\s\S]*?-->/g, ''), '')
})

test('工具结果合并回调用，整体成功不被中间失败覆盖，历史不转圈', async () => {
  const steps = [
    { type: 'tool_call', tool_call_id: 'a', title: '调用工具 grep_chunks', detail: '{"keyword":"Go"}', status: 'running' },
    { type: 'tool_result', tool_call_id: 'a', title: '工具执行结果', detail: '<script>查询失败</script>', status: 'error' },
    { type: 'tool_call', tool_call_id: 'b', title: '调用工具 grep_chunks', status: 'running' },
    { type: 'tool_result', tool_call_id: 'b', title: '工具执行结果', detail: '完整成功结果', status: 'success' },
    { type: 'thinking', title: '执行当前任务批次', status: 'running' },
  ]
  const html = await render(Trace, { steps, status: 'success' })
  assert.ok(html.includes('3 条记录'))
  assert.ok(!html.includes('工具执行结果'))
  assert.ok(!html.includes('完整成功结果'))
  assert.ok(!html.includes('存在失败'))
  assert.match(html.replace(/<!--[\s\S]*?-->/g, ''), /trace-state-success[^>]*>已完成/)
  assert.ok(html.includes('&lt;script&gt;查询失败&lt;/script&gt;'))
  assert.ok(html.includes('已结束'))
  assert.ok(!html.includes('class="trace-spinner"'))
  const failed = await render(Trace, { steps, status: 'error' })
  assert.ok(failed.includes('执行未完成'))
  const waiting = await render(Trace, { steps, status: 'waiting', active: false })
  assert.ok(waiting.includes('等待审批'))
})
