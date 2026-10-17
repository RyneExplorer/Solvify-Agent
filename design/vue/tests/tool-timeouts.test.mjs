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
const functions = ['createEmptyMCPForm', 'mcpFromJSON', 'buildMCPProviderConfig', 'providerConfigFromJSON', 'buildProviderConfig', 'parseKVRows']
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

test('MCP显式30秒必须保存；旧配置留空不擅自把调用300改成30', () => {
  const empty = forms.mcpFromJSON({ mcp: { transport: 'http', url: 'https://example.invalid' } })
  assert.equal(empty.timeout, '')
  assert.equal(forms.buildMCPProviderConfig(empty).timeout, undefined)
  for (const seconds of [1, 30, 60, 300, 86400]) {
    const form = { ...empty, timeout: String(seconds) }
    const saved = forms.buildMCPProviderConfig(form)
    assert.equal(saved.timeout, seconds)
    assert.equal(forms.mcpFromJSON(saved).timeout, seconds)
    assert.equal(forms.mcpFromJSON({ mcp: saved }).timeout, seconds)
  }
  const form = forms.mcpFromJSON({ mcp: { transport: 'http', url: 'https://example.invalid', timeout: 60 } })
  form.timeout = ''
  assert.equal(forms.buildMCPProviderConfig(form).timeout, undefined)
})

test('HTTP和MCP保存/测试共用校验，拒绝非法秒数', () => {
  for (const value of [0, -1, 1.5, 86401, 'abc', Infinity, NaN]) {
    assert.throws(() => toolTimeoutValue(value))
    assert.throws(() => forms.buildProviderConfig({ ...httpForm(), timeout: value }))
    assert.throws(() => forms.buildMCPProviderConfig({ ...forms.createEmptyMCPForm(), transport: 'http', url: 'https://example.invalid', timeout: value }))
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
  const pc = forms.buildMCPProviderConfig({ ...forms.createEmptyMCPForm(), transport: 'http', url: 'https://example.invalid', timeout: 30 })
  await adminCreateToolProvider('type', { name: 'test', provider_key: 'test', provider_type: 'mcp', description: '', provider_config: pc })
  assert.equal(call.path, '/admin/tool-types/type/providers')
  assert.equal(call.method, 'POST')
  assert.equal(call.body.provider_config.timeout, 30)
  await adminUpdateToolProvider('type', 'provider', { provider_config: pc })
  assert.equal(call.path, '/admin/tool-types/type/providers/provider')
  assert.equal(call.method, 'PUT')
  assert.equal(call.body.provider_config.timeout, 30)
  delete globalThis.__toolTimeoutRequest
})
