import test from 'node:test'
import assert from 'node:assert/strict'
import { modelSettingsError, modelDraftError, applyModelCapabilities } from '../src/lib/model-capabilities.mjs'

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
