// 表单提示与保存保护；服务端仍是配置校验的权威边界。
export function managedModelConfig(config = {}) {
  const next = { ...config, execution_policy: 'managed_v1' }
  for (const key of ['max_completion_tokens', 'temperature', 'timeout', 'reasoning_effort']) delete next[key]
  next.tool_calling ??= true
  next.output_token_field ||= 'max_tokens'
  next.reasoning_capability ||= 'unknown'
  next.reasoning_mode ||= 'default'
  next.tools_with_reasoning ??= true
  return next
}

export function simplifiedModelSettingsError(window, config = {}) {
  const next = managedModelConfig(config)
  next.max_completion_tokens = Math.min(8192, next.max_output_tokens || 0, Math.floor(window / 4))
  return modelSettingsError(window, next)
}

export function modelSettingsError(window, config = {}) {
  const p = config || {}
  if (!Number.isInteger(window) || window < 1024 || window > 2000000) return '请确认总上下文窗口'
  for (const key of ['max_output_tokens', 'max_completion_tokens']) {
    if (!Number.isInteger(p[key]) || p[key] <= 0) return '请填写模型输入、生成及实际生成额度'
  }
  if (p.max_input_tokens !== undefined && (!Number.isInteger(p.max_input_tokens) || p.max_input_tokens < 0)) return '独立输入上限无效'
  if ((p.execution_policy !== 'managed_v1' && (p.max_input_tokens > window || p.max_output_tokens > window)) || p.max_completion_tokens >= window || p.max_completion_tokens > p.max_output_tokens) return '调用额度不能超过模型能力'
  if (!['max_tokens', 'max_completion_tokens'].includes(p.output_token_field)) return '请选择输出参数字段'
  if (typeof p.tool_calling !== 'boolean') return '请确认工具调用能力'
  if (!p.tool_calling) return '项目模型必须支持工具调用，请更换模型'
  if (!['none', 'optional', 'always', 'unknown'].includes(p.reasoning_capability)) return '请确认模型思考能力'
  if (p.reasoning_capability === 'unknown' && p.reasoning_mode !== 'default') return '思考能力待识别，请沿用服务商默认行为'
  if (!['default', 'off', 'on'].includes(p.reasoning_mode)) return '请选择思考设置'
  if ((p.reasoning_capability === 'none' && p.reasoning_mode === 'on') || (p.reasoning_capability === 'always' && p.reasoning_mode === 'off')) return '思考设置与模型能力不兼容'
  if (p.reasoning_protocol && !['thinking', 'enable_thinking', 'reasoning_effort'].includes(p.reasoning_protocol)) return '思考协议无效'
  if (p.reasoning_capability === 'optional' && p.reasoning_mode !== 'default' && !p.reasoning_protocol) return '切换思考模式需要服务商协议'
  if (p.reasoning_protocol === 'reasoning_effort' && p.reasoning_mode !== 'default') return '思考强度协议使用服务商默认开关'
  if (p.reasoning_capability !== 'none' && p.reasoning_mode !== 'off' && p.tools_with_reasoning !== true) return '当前思考设置不支持工具调用，请关闭思考或更换模型'
  if (p.reasoning_effort && (p.reasoning_protocol !== 'reasoning_effort' || !['low', 'medium', 'high'].includes(p.reasoning_effort) || p.reasoning_mode === 'off' || p.reasoning_capability === 'none')) return '思考强度与当前配置不兼容'
  if (p.temperature !== undefined && (!Number.isFinite(p.temperature) || p.temperature < 0 || p.temperature > 2)) return '温度必须在0到2之间'
  if (p.timeout !== undefined && (!Number.isInteger(p.timeout) || p.timeout <= 0 || p.timeout > 300)) return '超时必须在1到300秒之间'
  return ''
}

// 草稿允许未知字段留空；填写的值仍必须合法。
export function modelDraftError(window, config = {}) {
  const p = config || {}
  if (p.tool_calling === false) return '项目模型必须支持工具调用，请更换模型'
  if (!Number.isInteger(window) || window < 0 || window > 2000000 || (window > 0 && window < 1024)) return '总窗口须留空或为1024到2000000的整数'
  for (const key of ['max_input_tokens', 'max_output_tokens', 'max_completion_tokens']) {
    if (p[key] !== undefined && (!Number.isInteger(p[key]) || p[key] < (key === 'max_completion_tokens' ? 1 : 0))) return '输入/输出额度无效'
  }
  const candidate = { ...p, max_output_tokens: p.max_output_tokens || 1, max_completion_tokens: p.max_completion_tokens ?? 1,
    output_token_field: p.output_token_field || 'max_tokens', tool_calling: p.tool_calling ?? true,
    reasoning_capability: p.reasoning_capability || 'none', reasoning_mode: p.reasoning_mode || 'default', tools_with_reasoning: p.tools_with_reasoning ?? true }
  const effectiveWindow = window || Math.max(1024, p.max_input_tokens || 0, p.max_output_tokens || 0, (p.max_completion_tokens || 0) + 1)
  if (!p.max_output_tokens) candidate.max_output_tokens = Math.max(1, p.max_completion_tokens || 0)
  return modelSettingsError(effectiveWindow, candidate)
}

// 仅显式“获取”回填资料，失败保留现有值；实际生成额度不自动设定。
export function applyModelCapabilities(window, config, result) {
  if (!result?.success) return { window, config }
  const next = { ...config, ...result.suggested_config }
	if (result.suggested_config?.reasoning_capability) {
		const capability = next.reasoning_capability
		if (capability !== 'optional' || !['thinking', 'enable_thinking'].includes(next.reasoning_protocol)) next.reasoning_mode = 'default'
	}
  if (result.detected_max_input_tokens > 0) next.max_input_tokens = result.detected_max_input_tokens
  else delete next.max_input_tokens
  if (result.detected_max_output_tokens > 0) next.max_output_tokens = result.detected_max_output_tokens
  next.capability_origin = { source: result.capability_source, url: result.capability_url || '', checked_at: result.capability_checked_at || '' }
  return { window: result.detected_max_context_length > 0 ? result.detected_max_context_length : window, config: next }
}

// 普通用户只选择窗口和模式，服务端负责取得部署额度与协议。
export function userModelConfig(config = {}) {
  return { reasoning_mode: config.reasoning_mode || 'default' }
}
export function thinkingState(config = {}) {
  const capability = config.reasoning_capability || 'unknown'
  if (capability === 'none') return { enabled: false, disabled: true, label: '该模型不支持思考' }
  if (capability === 'always') return { enabled: true, disabled: true, label: '该模型始终开启思考' }
  if (capability === 'optional' && ['thinking', 'enable_thinking'].includes(config.reasoning_protocol)) {
    return { enabled: config.reasoning_mode === 'on', disabled: false, label: config.reasoning_mode === 'default' ? '沿用服务商默认行为' : config.reasoning_mode === 'on' ? '已开启' : '已关闭' }
  }
  return { enabled: false, disabled: true, label: '思考开关待识别，沿用服务商默认行为' }
}
export function userModelSettingsError(window, config = {}) {
  if (!Number.isInteger(window) || window < 1024 || window > 2000000) return '请填写1024到2000000的上下文窗口'
  if (config.tool_calling === false) return '该模型不支持工具调用，请更换模型'
  const mode = config.reasoning_mode || 'default'
  if (!['default', 'off', 'on'].includes(mode)) return '思考设置无效'
  const state = thinkingState(config)
  if ((config.reasoning_capability === 'none' && mode === 'on') || (config.reasoning_capability === 'always' && mode === 'off')) return '思考设置与模型能力不兼容'
  if ((config.reasoning_capability || 'unknown') === 'unknown' && mode !== 'default') return '思考能力待识别，请沿用服务商默认行为'
  if (config.reasoning_capability === 'optional' && state.disabled && mode !== 'default') return '尚未识别思考切换协议'
  if (mode !== 'off' && config.reasoning_capability !== 'none' && config.tools_with_reasoning === false) return '当前思考模式不支持工具，请关闭思考或更换模型'
  return ''
}
