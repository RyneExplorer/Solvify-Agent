import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizeExecutionTrace } from '../src/composables/chatExecutionTrace.ts'

const call = (id, task = 't1', extra = {}) => ({ type: 'tool_call', title: '调用工具 knowledge_search', status: 'running', detail: '{"query":"Go"}', tool_call_id: id, task_id: task, plan_id: 'p', plan_version: 1, ...extra })
const result = (id, task = 't1', extra = {}) => ({ type: 'tool_result', title: '工具执行结果', status: 'success', tool_call_id: id, task_id: task, plan_id: 'p', plan_version: 1, ...extra })

test('并行同名调用按ID匹配乱序结果，成功结果不再单独展示', () => {
  const input = [call('a'), call('b'), result('b')]
  const snapshot = structuredClone(input)
  const live = normalizeExecutionTrace(input, true)
  assert.deepEqual(live.map(row => row.status), ['running', 'success'])
  assert.equal(live.length, 2)
  assert.ok(live.every(row => row.type === 'tool_call'))
  assert.deepEqual(input, snapshot)
  assert.deepEqual(normalizeExecutionTrace([...input, result('a')], true).map(row => row.status), ['success', 'success'])
})

test('同ID跨任务和计划版本隔离，不能更新别的调用', () => {
  const steps = [call('same', 't1'), call('same', 't2'), call('same', 't1', { plan_version: 2 }), result('same', 't2'), result('same', 't1', { plan_version: 2, status: 'error', detail: '限流' })]
  const rows = normalizeExecutionTrace(steps, true)
  assert.deepEqual(rows.map(row => row.status), ['running', 'success', 'error'])
  assert.equal(rows[2].resultDetail, '限流')
  assert.equal(rows[2].detail, '{"query":"Go"}')
  assert.equal(normalizeExecutionTrace([call('a'), result('b')], true)[0].status, 'running')
})

test('失败后再次调用成功保留每次状态，不把结果文本或其他事件当成功依据', () => {
  const rows = normalizeExecutionTrace([call('a'), result('a', 't1', { status: 'error', detail: '<script>限流</script>' }), call('b'), { title: '下一阶段', status: 'running', type: 'thinking' }, result('b', 't1', { detail: '完整工具输出不再展示' })], true)
  assert.deepEqual(rows.map(row => row.status), ['error', 'success', 'running'])
  assert.equal(rows[0].resultDetail, '<script>限流</script>')
  assert.equal(rows[1].resultDetail, undefined)
})

test('旧记录只匹配唯一候选，不按并行结果返回顺序猜调用归属', () => {
  const legacyCall = { title: '调用工具 grep_chunks', type: 'tool_call', status: 'running' }
  const legacyResult = { title: '工具执行结果', type: 'tool_result', status: 'error', detail: '查询失败' }
  assert.equal(normalizeExecutionTrace([legacyCall, legacyResult], false)[0].status, 'error')
  const ambiguous = normalizeExecutionTrace([legacyCall, { ...legacyCall }, legacyResult], false)
  assert.deepEqual(ambiguous.map(row => row.status), ['ended', 'ended'])
  const named = normalizeExecutionTrace([legacyCall, { ...legacyCall, title: '调用工具 knowledge_search' }, { ...legacyResult, tool_name: 'grep_chunks' }], true)
  assert.deepEqual(named.map(row => row.status), ['error', 'running'])
  assert.equal(normalizeExecutionTrace([{ ...legacyCall, type: undefined }, { ...legacyResult, type: undefined }], false).length, 1)
})

test('结束后缺少结果的旧记录停止转圈，审批和已知失败不改成成功', () => {
  const rows = normalizeExecutionTrace([call('a'), { title: '执行当前任务批次', status: 'running' }, { title: '待审批', status: 'waiting' }, { title: '失败', status: 'error' }], false)
  assert.deepEqual(rows.map(row => row.status), ['ended', 'ended', 'waiting', 'error'])
})

test('顺序重试复用调用ID时按事件边界匹配，重复结果不能提前完成未来调用', () => {
  const rows = normalizeExecutionTrace([call('same'), result('same', 't1', { status: 'error', detail: '无匹配内容' }), call('same'), result('same')], false)
  assert.deepEqual(rows.map(row => row.status), ['error', 'success'])
  const duplicate = normalizeExecutionTrace([call('same'), result('same'), result('same'), call('same')], true)
  assert.deepEqual(duplicate.map(row => row.status), ['success', 'running'])
})
