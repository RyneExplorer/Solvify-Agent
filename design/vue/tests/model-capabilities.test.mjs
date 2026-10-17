import test from 'node:test'
import assert from 'node:assert/strict'
import { modelSettingsError, modelDraftError, applyModelCapabilities, managedModelConfig, simplifiedModelSettingsError, thinkingState, userModelConfig, userModelSettingsError } from '../src/lib/model-capabilities.mjs'

test('普通用户只提交思考选择，能力字段不成为声明', () => {
  assert.deepEqual(userModelConfig({ max_output_tokens: 99999, tool_calling: true, reasoning_protocol: 'thinking', reasoning_mode: 'off' }), { reasoning_mode: 'off' })
  assert.equal(userModelSettingsError(8192, {}), '')
  assert.ok(userModelSettingsError(0, {}))
  assert.ok(userModelSettingsError(8192, { reasoning_mode: 'on' }))
  assert.ok(userModelSettingsError(8192, { tool_calling: false }))
})
test('思考开关区分无能力、固定开启、可切换和未知', () => {
  assert.deepEqual(thinkingState({ reasoning_capability: 'none' }), { enabled: false, disabled: true, label: '该模型不支持思考' })
  assert.equal(thinkingState({ reasoning_capability: 'always' }).enabled, true)
  assert.equal(thinkingState({ reasoning_capability: 'always' }).disabled, true)
  assert.equal(thinkingState({}).disabled, true)
  assert.equal(thinkingState({ reasoning_capability: 'optional' }).disabled, true)
  const config = { reasoning_capability: 'optional', reasoning_protocol: 'thinking', reasoning_mode: 'on' }
  assert.equal(thinkingState(config).disabled, false)
  assert.equal(thinkingState(config).enabled, true)
  assert.equal(userModelSettingsError(8192, config), '')
  assert.ok(userModelSettingsError(8192, { ...config, tools_with_reasoning: false }))
  assert.equal(userModelSettingsError(8192, { ...config, tools_with_reasoning: false, reasoning_mode: 'off' }), '')
})

test('新获取的能力不可切换时不残留旧思考开关', () => {
  for (const capability of ['none', 'always', 'unknown']) {
    const next = applyModelCapabilities(8192, { reasoning_mode: 'on', reasoning_capability: 'optional', reasoning_protocol: 'thinking' }, { success: true, suggested_config: { reasoning_capability: capability } })
    assert.equal(next.config.reasoning_mode, 'default')
    assert.equal(thinkingState(next.config).disabled, true)
  }
})

test('简化表单仅需额度；系统执行参数不接受手填覆盖', () => {
  const config = { max_output_tokens: 4096, timeout: 1, temperature: 2, max_completion_tokens: 8000 }
  assert.equal(simplifiedModelSettingsError(8192, config), '')
  const managed = managedModelConfig(config)
  assert.equal(managed.execution_policy, 'managed_v1')
  assert.equal(managed.timeout, undefined)
  assert.equal(managed.temperature, undefined)
  assert.equal(managed.max_completion_tokens, undefined)
  assert.equal(config.timeout, 1)
  assert.ok(simplifiedModelSettingsError(0, config))
  assert.ok(simplifiedModelSettingsError(8192, {}))
  assert.ok(simplifiedModelSettingsError(8192, { ...config, tool_calling: false }))
})

const settings = () => ({ max_input_tokens: 4096, max_output_tokens: 1024, max_completion_tokens: 256, output_token_field: 'max_tokens', tool_calling: true, reasoning_capability: 'none', reasoning_mode: 'default' })

test('未知或旧配置须确认，不能自动填入窗口或生成额度', () => {
  assert.ok(modelSettingsError(0, settings()))
  assert.ok(modelSettingsError(8192, {}))
  for (const key of Object.keys(settings()).filter(key => key !== 'max_input_tokens')) {
    const p = settings(); delete p[key]
    assert.ok(modelSettingsError(8192, p), key)
  }
  assert.equal(modelSettingsError(8192, settings()), '')
})
test('输入输出能力限制及整数验证', () => {
  assert.ok(modelSettingsError(8192, { ...settings(), max_input_tokens: 9000 }))
  assert.ok(modelSettingsError(8192, { ...settings(), max_completion_tokens: 2048 }))
  assert.ok(modelSettingsError(8192, { ...settings(), max_output_tokens: 1.5 }))
  assert.ok(modelSettingsError(8192, { ...settings(), tool_calling: false }))
})

test('配置入口禁止不支持工具及冲突的思考组合', () => {
  assert.match(modelDraftError(0, { tool_calling: false }), /必须支持工具/)
  assert.match(modelDraftError(8192, { ...settings(), reasoning_capability: 'optional', reasoning_mode: 'default', tools_with_reasoning: false }), /关闭思考/)
  assert.equal(modelDraftError(8192, { ...settings(), reasoning_capability: 'optional', reasoning_mode: 'off', reasoning_protocol: 'thinking', tools_with_reasoning: false }), '')
})
test('思考开关与协议保持一致', () => {
  assert.ok(modelSettingsError(8192, { ...settings(), reasoning_capability: 'always', reasoning_mode: 'off' }))
  assert.ok(modelSettingsError(8192, { ...settings(), reasoning_capability: 'optional', reasoning_mode: 'on' }))
  assert.equal(modelSettingsError(8192, { ...settings(), reasoning_capability: 'optional', reasoning_mode: 'off', reasoning_protocol: 'thinking' }), '')
})

test('草稿可保存，运行必须确认；独立输入限制可选', () => {
  assert.equal(modelDraftError(0, {}), '')
  assert.ok(modelSettingsError(0, {}))
  const p = settings(); delete p.max_input_tokens
  assert.equal(modelSettingsError(8192, p), '')
  assert.ok(modelDraftError(-1, {}))
  assert.ok(modelDraftError(100, {}))
  assert.ok(modelDraftError(0, { max_output_tokens: 'large' }))
  assert.ok(modelDraftError(0, { timeout: 0 }))
})

test('能力获取失败保留手填值，成功不猜实际输出且不改原对象', () => {
  const p = settings()
  assert.deepEqual(applyModelCapabilities(8192, p, { success: false }), { window: 8192, config: p })
  const next = applyModelCapabilities(8192, p, { success: true, detected_max_context_length: 32768, detected_max_output_tokens: 4096, capability_source: 'provider_metadata' })
  assert.equal(next.window, 32768)
  assert.equal(next.config.max_completion_tokens, 256)
  assert.equal(next.config.max_output_tokens, 4096)
  assert.equal(p.max_output_tokens, 1024)
  const empty = applyModelCapabilities(0, {}, { success: true, detected_max_context_length: 32768, capability_source: 'provider_metadata' })
  assert.equal(empty.config.max_completion_tokens, undefined)
})


test('保存需确认当前思考模式的工具兼容能力', () => {
  const config = { ...settings(), reasoning_capability: 'optional', reasoning_mode: 'default' }
  assert.ok(modelSettingsError(8192, config))
  assert.equal(modelSettingsError(8192, { ...config, tools_with_reasoning: true }), '')
  assert.ok(modelSettingsError(8192, { ...config, tools_with_reasoning: false }))
})
