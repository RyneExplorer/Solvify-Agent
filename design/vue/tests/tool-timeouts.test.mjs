import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { parse, compileScript } from '@vue/compiler-sfc'
import { transformWithEsbuild } from 'vite'
import { toolTimeoutValue, toolTimeoutFromConfig } from '../src/lib/tool-timeout.mjs'

// 提取并执行现有管理页的真实表单编解码函数，不复制实现。
const source = await readFile(new URL('../src/components/MCPAdminSettings.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const script = descriptor.scriptSetup.content
const ast = compileScript(descriptor, { id: 'admin-tool-timeout-test' }).scriptSetupAst
const functions = ['createEmptyMCPForm', 'mcpFromJSON', 'buildMCPConnection', 'buildMCPTimeoutConfig']
const selected = ast.filter(node => node.type === 'FunctionDeclaration' && functions.includes(node.id?.name))
const declarations = selected.map(node => script.slice(node.start, node.end)).join('\n')
assert.equal(selected.length, functions.length)
const helper = new URL('../src/lib/tool-timeout.mjs', import.meta.url).href
const { code } = await transformWithEsbuild(`import { toolTimeoutValue, toolTimeoutFromConfig } from '${helper}';\n${declarations}\nexport { ${functions.join(', ')} }`, 'forms.ts', { loader: 'ts' })
const forms = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`)
test('MCP独立超时保存、回显、清空及旧timeout迁移', () => {
  const empty = forms.mcpFromJSON({ transport: 'http', url: 'https://example.invalid' })
  assert.equal(empty.connect_timeout, '')
  assert.equal(empty.call_timeout, '')
  assert.deepEqual(forms.buildMCPTimeoutConfig(empty), {connect_timeout:undefined,call_timeout:undefined})
  for (const seconds of [1, 30, 60, 300, 86400]) {
    const form = { ...empty, connect_timeout: String(seconds), call_timeout: '120' }
    const saved = forms.buildMCPConnection(form)
    assert.equal(saved.connect_timeout, seconds)
    assert.equal(saved.call_timeout, 120)
    assert.equal(saved.timeout, undefined)
    assert.equal(forms.mcpFromJSON(saved).connect_timeout, seconds)
    assert.equal(forms.mcpFromJSON(saved).call_timeout, 120)
  }
  const legacy = forms.mcpFromJSON({ transport: 'http', url: 'https://example.invalid', timeout: 45 })
  assert.equal(legacy.connect_timeout, 45)
  assert.equal(legacy.call_timeout, 45)
  const migrated = forms.buildMCPConnection(legacy)
  assert.equal(migrated.timeout, undefined)
  assert.equal(migrated.connect_timeout, 45)
  legacy.connect_timeout = ''
  const cleared = forms.buildMCPConnection(legacy)
  assert.equal(cleared.connect_timeout, undefined)
  assert.equal(cleared.call_timeout, 45)
  const mixed = forms.mcpFromJSON({ timeout: 45, connect_timeout: 90 })
  assert.equal(mixed.connect_timeout, 90)
  assert.equal(mixed.call_timeout, 45)
})

test('MCP保存/测试共用校验，拒绝非法秒数', () => {
  for (const value of [0, -1, 1.5, 86401, 'abc', Infinity, NaN]) {
    assert.throws(() => toolTimeoutValue(value))
    assert.throws(() => forms.buildMCPConnection({ ...forms.createEmptyMCPForm(), transport: 'http', url: 'https://example.invalid', call_timeout: value }))
  }
  assert.equal(toolTimeoutFromConfig(0), '')
})

test('MCP时限通过模板接口保存，不依赖工具类型或供应商', async () => {
  let call
  globalThis.__toolTimeoutRequest = async (path, options) => { call = { path, ...options }; return { data: options.body } }
  let api = await readFile(new URL('../src/api/admin.ts', import.meta.url), 'utf8')
  api = api.replace("import { request } from './client'", 'const request = globalThis.__toolTimeoutRequest')
  const { code } = await transformWithEsbuild(api, 'admin.ts', { loader: 'ts' })
  const { adminCreateMCPTemplate, adminUpdateMCPTemplate } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`)
  const pc = forms.buildMCPConnection({ ...forms.createEmptyMCPForm(), transport: 'http', url: 'https://example.invalid', connect_timeout: 90, call_timeout: 120 })
  await adminCreateMCPTemplate({ name: 'test', key: 'test', description: '', connection: pc })
  assert.equal(call.path, '/admin/mcp-templates')
  assert.equal(call.method, 'POST')
  assert.equal(call.body.connection.connect_timeout, 90)
  assert.equal(call.body.connection.call_timeout, 120)
  await adminUpdateMCPTemplate('template', { connection: pc })
  assert.equal(call.path, '/admin/mcp-templates/template')
  assert.equal(call.method, 'PUT')
  assert.equal(call.body.connection.connect_timeout, 90)
  assert.equal(call.body.connection.call_timeout, 120)
  delete globalThis.__toolTimeoutRequest
})
