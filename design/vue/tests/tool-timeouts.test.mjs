import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { parse, compileScript } from '@vue/compiler-sfc'
import { transformWithEsbuild } from 'vite'
import { toolTimeoutValue, toolTimeoutFromConfig } from '../src/lib/tool-timeout.mjs'

// 提取并执行现有管理页的真实表单编解码函数，不复制实现。
const source = await readFile(new URL('../src/pages/AdminPage.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const script = descriptor.scriptSetup.content
const ast = compileScript(descriptor, { id: 'admin-tool-timeout-test' }).scriptSetupAst
const functions = ['createEmptyMCPForm', 'mcpFromJSON', 'buildMCPProviderConfig', 'buildMCPTimeoutConfig', 'providerConfigFromJSON', 'buildProviderConfig', 'parseKVRows']
const selected = ast.filter(node => node.type === 'FunctionDeclaration' && functions.includes(node.id?.name))
const declarations = selected.map(node => script.slice(node.start, node.end)).join('\n')
assert.equal(selected.length, functions.length)
const helper = new URL('../src/lib/tool-timeout.mjs', import.meta.url).href
const { code } = await transformWithEsbuild(`import { toolTimeoutValue, toolTimeoutFromConfig } from '${helper}';\n${declarations}\nexport { ${functions.join(', ')} }`, 'forms.ts', { loader: 'ts' })
const forms = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`)
const httpForm = () => ({ provider_type: 'http', method: 'GET', url: 'https://example.invalid', timeout: '', headers_rows: [], response_mapping_rows: [], body_template_text: '{}' })

test('HTTP现有编辑框保存、回显和清空时限', () => {
  const form = httpForm()
  assert.equal(forms.buildProviderConfig(form).timeout, undefined)
  form.timeout = '60'
  const saved = forms.buildProviderConfig(form)
  assert.equal(saved.timeout, 60)
  assert.equal(forms.providerConfigFromJSON(saved).timeout, 60)
  assert.equal(forms.providerConfigFromJSON(JSON.stringify(saved)).timeout, 60)
  form.timeout = ''
  assert.equal(forms.buildProviderConfig(form).timeout, undefined)
  assert.equal(forms.providerConfigFromJSON({ method: 'GET' }).timeout, '')
})

test('MCP独立超时保存、回显、清空及旧timeout迁移', () => {
  const empty = forms.mcpFromJSON({ mcp: { transport: 'http', url: 'https://example.invalid' } })
  assert.equal(empty.connect_timeout, '')
  assert.equal(empty.call_timeout, '')
  assert.deepEqual(forms.buildMCPTimeoutConfig(empty), {})
  for (const seconds of [1, 30, 60, 300, 86400]) {
    const form = { ...empty, connect_timeout: String(seconds), call_timeout: '120' }
    const saved = forms.buildMCPProviderConfig(form)
    assert.equal(saved.connect_timeout, seconds)
    assert.equal(saved.call_timeout, 120)
    assert.equal(saved.timeout, undefined)
    assert.equal(forms.mcpFromJSON(saved).connect_timeout, seconds)
    assert.equal(forms.mcpFromJSON({ mcp: saved }).call_timeout, 120)
  }
  const legacy = forms.mcpFromJSON({ mcp: { transport: 'http', url: 'https://example.invalid', timeout: 45 } })
  assert.equal(legacy.connect_timeout, 45)
  assert.equal(legacy.call_timeout, 45)
  const migrated = forms.buildMCPProviderConfig(legacy)
  assert.equal(migrated.timeout, undefined)
  assert.equal(migrated.connect_timeout, 45)
  legacy.connect_timeout = ''
  const cleared = forms.buildMCPProviderConfig(legacy)
  assert.equal(cleared.connect_timeout, undefined)
  assert.equal(cleared.call_timeout, 45)
  const mixed = forms.mcpFromJSON({ timeout: 45, connect_timeout: 90 })
  assert.equal(mixed.connect_timeout, 90)
  assert.equal(mixed.call_timeout, 45)
})

test('HTTP和MCP保存/测试共用校验，拒绝非法秒数', () => {
  for (const value of [0, -1, 1.5, 86401, 'abc', Infinity, NaN]) {
    assert.throws(() => toolTimeoutValue(value))
    assert.throws(() => forms.buildProviderConfig({ ...httpForm(), timeout: value }))
    assert.throws(() => forms.buildMCPProviderConfig({ ...forms.createEmptyMCPForm(), transport: 'http', url: 'https://example.invalid', call_timeout: value }))
  }
  assert.equal(toolTimeoutFromConfig(0), '')
})

test('工具时限复用原供应商创建/更新接口，原样保存provider_config', async () => {
  let call
  globalThis.__toolTimeoutRequest = async (path, options) => { call = { path, ...options }; return { data: options.body } }
  let api = await readFile(new URL('../src/api/admin.ts', import.meta.url), 'utf8')
  api = api.replace("import { request } from './client'", 'const request = globalThis.__toolTimeoutRequest')
  const { code } = await transformWithEsbuild(api, 'admin.ts', { loader: 'ts' })
  const { adminCreateToolProvider, adminUpdateToolProvider } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`)
  const pc = forms.buildMCPProviderConfig({ ...forms.createEmptyMCPForm(), transport: 'http', url: 'https://example.invalid', connect_timeout: 90, call_timeout: 120 })
  await adminCreateToolProvider('type', { name: 'test', provider_key: 'test', provider_type: 'mcp', description: '', provider_config: pc })
  assert.equal(call.path, '/admin/tool-types/type/providers')
  assert.equal(call.method, 'POST')
  assert.equal(call.body.provider_config.connect_timeout, 90)
  assert.equal(call.body.provider_config.call_timeout, 120)
  await adminUpdateToolProvider('type', 'provider', { provider_config: pc })
  assert.equal(call.path, '/admin/tool-types/type/providers/provider')
  assert.equal(call.method, 'PUT')
  assert.equal(call.body.provider_config.connect_timeout, 90)
  assert.equal(call.body.provider_config.call_timeout, 120)
  delete globalThis.__toolTimeoutRequest
})

test('系统预置在原编辑框只提交两个超时字段', async () => {
  const node = ast.find(node => node.type === 'FunctionDeclaration' && node.id?.name === 'saveToolProvider')
  let call
  const form = { ...forms.createEmptyMCPForm(), transport: 'http', url: 'https://preset.invalid', connect_timeout: '90', call_timeout: '120' }
  const scope = {
    currentToolType: { value: { id: 'type' } },
    toolProviderForm: { value: { provider_type: 'mcp' } },
    mcpForm: form,
    editingToolProvider: { value: { id: 'preset', is_system: true } },
    toolProviderModalVisible: { value: true },
    adminUpdateToolProvider: async (...args) => { call = args },
    loadToolProviders: async () => {}, loadToolTypes: async () => {},
    ElMessage: { success() {}, error(message) { throw new Error(message) } },
  }
  globalThis.__mcpTimeoutSaveScope = scope
  const { code } = await transformWithEsbuild(`
    const { ${Object.keys(scope).join(', ')} } = globalThis.__mcpTimeoutSaveScope;
    ${declarations}
    ${script.slice(node.start, node.end)}
    import { toolTimeoutValue, toolTimeoutFromConfig } from '${helper}';
    export { saveToolProvider };
  `, 'preset-save.ts', { loader: 'ts' })
  const { saveToolProvider } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`)
  await saveToolProvider()
  assert.deepEqual(call, ['type', 'preset', { provider_config: { mcp: { connect_timeout: 90, call_timeout: 120 } } }])
  assert.equal(scope.toolProviderModalVisible.value, false)
  delete globalThis.__mcpTimeoutSaveScope
})
