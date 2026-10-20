export interface ExecutionTraceStep {
  title: string
  detail?: string
  status: string
  type?: string
  tool_call_id?: string
  tool_name?: string
  task_id?: string
  plan_id?: string
  plan_version?: number
  resultDetail?: string
}

/** 调用与结果合为一条展示记录；不修改服务器事件或历史快照。 */
export function normalizeExecutionTrace(steps: ExecutionTraceStep[], active: boolean): ExecutionTraceStep[] {
  const rows: ExecutionTraceStep[] = []
  const calls: { row: ExecutionTraceStep; index: number; matched: boolean }[] = []
  const results: { step: ExecutionTraceStep; index: number }[] = []
  steps.forEach((step, index) => {
    const type = step.type || (step.title.startsWith('调用工具 ') ? 'tool_call' : step.title === '工具执行结果' ? 'tool_result' : '')
    if (type === 'tool_result') {
      results.push({ step, index })
      return
    }
    const row = { ...step, type }
    rows.push(row)
    if (type === 'tool_call') calls.push({ row, index, matched: false })
  })

  for (const { step: result, index } of results) {
    const candidates = calls.filter(call => {
      if (call.matched || call.index > index) return false
      for (const key of ['task_id', 'plan_id', 'plan_version'] as const) {
        if (result[key] !== undefined && call.row[key] !== undefined && result[key] !== call.row[key]) return false
      }
      if (result.tool_call_id) return result.tool_call_id === call.row.tool_call_id
      // 旧记录缺少ID时仅接受唯一候选，不能用返回顺序猜并行调用的归属。
      if (call.row.tool_call_id) return false
      const name = call.row.tool_name || call.row.title.replace(/^调用工具\s+/, '')
      return !result.tool_name || result.tool_name === name
    })
    if (candidates.length !== 1) continue
    const call = candidates[0]
    call.matched = true
    call.row.status = result.status || 'unknown'
    // 成功输出不再展示；失败/警告解释归入原调用，保留原始调用参数。
    if (result.status === 'error' || result.status === 'warning') call.row.resultDetail = result.detail
  }

  if (!active) {
    for (const row of rows) {
      // 结束后缺失配对结果不能继续转圈，也不能据整次成功推断单次成功。
      if (row.status === 'running') row.status = 'ended'
    }
  }
  return rows
}
