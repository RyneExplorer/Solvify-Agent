import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile, writeFile, unlink } from 'node:fs/promises'
import { parse, compileScript } from '@vue/compiler-sfc'
import { transformWithEsbuild } from 'vite'
import { toolTimeoutValue } from '../src/lib/tool-timeout.mjs'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'

async function componentFunctions(file, names, scope) {
  const source = await readFile(new URL(`../src/components/${file}.vue`, import.meta.url), 'utf8')
  const { descriptor } = parse(source)
  const ast = compileScript(descriptor, { id: file }).scriptSetupAst
  const functions = ast.filter(n => n.type === 'FunctionDeclaration' && names.includes(n.id?.name))
  assert.equal(functions.length, names.length)
  const text = functions.map(n => descriptor.scriptSetup.content.slice(n.start, n.end)).join('\n')
  const { code } = await transformWithEsbuild(text, 'component.ts', { loader: 'ts' })
  return new Function(...Object.keys(scope), `${code}; return {${names.join(',')}}`)(...Object.values(scope))
}
const ref = value => ({ value })
function userScope() {
  return Object.fromEntries(Object.entries({ templateId: '', selectedTemplate: undefined, name: '搜索', values: {}, url: ' https://search.example/mcp ', auth: 'none', token: '', headerName: 'X-API-Key', extraHeaders: '{}', connectTimeout: '', callTimeout: '', editing: '', visible: true, busy: false, result: null }).map(([k, v]) => [k, ref(v)]))
}

test('真实聊天组件只在深度模式展示MCP选择器', async () => {
  const {descriptor} = parse(await readFile(new URL('../src/components/ChatInputCard.vue',import.meta.url),'utf8'))
  const compiled = compileScript(descriptor,{id:'mcp-chat-test',inlineTemplate:true})
  const {code} = await transformWithEsbuild(compiled.content,'ChatInputCard.ts',{loader:'ts'})
  const generated = new URL(`./.mcp-chat-${process.pid}.mjs`,import.meta.url)
  await writeFile(generated,code)
  try {
    const {default:component} = await import(generated.href)
    const props = {input:'',modelValue:'',loading:false,kbText:'',knowledgeBases:[],selectedKBs:[],modelOptions:[],mcpOptions:[{id:'search',name:'测试搜索MCP',description:''}],'selected-mcp-ids':['search'],mcpLoading:false}
    const deep = await renderToString(createSSRApp(component,{...props,searchMode:'smart-reasoning'}))
    const quick = await renderToString(createSSRApp(component,{...props,searchMode:'quick'}))
    assert.ok(deep.includes('测试搜索MCP'))
    assert.ok(!quick.includes('测试搜索MCP'))
    const none = await renderToString(createSSRApp(component,{...props,'selected-mcp-ids':[],searchMode:'smart-reasoning'}))
    assert.ok(none.includes('不使用 MCP'))
  } finally { await unlink(generated) }
})

test('预置只提交用户字段，API Key不进入连接地址或工具参数', async () => {
  const scope = userScope()
  scope.templateId.value = 'template'
  scope.selectedTemplate.value = { config_schema: { properties: { api_key: {type:'string'}, count: {type:'integer'}, enabled: {type:'boolean'} } } }
  scope.values.value = { api_key: 'user-secret', count: '3', enabled: false, ignored: 'no' }
  scope.token.value = 'must-not-leak'
  const { draft } = await componentFunctions('MCPSettings', ['draft'], {...scope, toolTimeoutValue})
  assert.deepEqual(draft(), { display_name:'搜索', template_id:'template', config:{api_key:'user-secret',count:3,enabled:false} })
})

test('个人HTTP MCP按选择构造认证头，非法JSON头不发送', async () => {
  const scope = userScope()
  scope.auth.value = 'bearer'; scope.token.value = 'secret'; scope.callTimeout.value = '30'
  const { draft } = await componentFunctions('MCPSettings', ['draft'], {...scope, toolTimeoutValue})
  const body = draft()
  assert.equal(body.connection.url, 'https://search.example/mcp')
  assert.equal(body.connection.transport, 'http')
  assert.equal(body.connection.headers.Authorization, 'Bearer secret')
  assert.equal(body.connection.call_timeout, 30)
  scope.auth.value = 'header'; scope.headerName.value = 'X-Search-Key'
  assert.deepEqual(draft().connection.headers, {'X-Search-Key':'secret'})
  scope.auth.value = 'none'
  assert.deepEqual(draft().connection.headers, {})
  for (const raw of ['[]','null','"text"','{"X":123}','{']) {
    scope.extraHeaders.value = raw
    assert.throws(draft)
  }
})

test('连接测试发送当前编辑的真实凭据，不调用任何工具执行接口', async () => {
  const scope = userScope(); scope.auth.value = 'bearer'; scope.token.value = 'edited-secret'
  let body
  const api = { probeMCPConnection: async payload => { body = payload; return {data:{success:true,message:'发现工具'}} } }
  const { probe } = await componentFunctions('MCPSettings', ['draft','probe'], {...scope, toolTimeoutValue, api})
  await probe()
  assert.equal(body.connection.headers.Authorization, 'Bearer edited-secret')
  assert.equal(scope.result.value.success, true)
  assert.equal(scope.busy.value, false)
})

test('保存已有MCP更新原连接，成功关闭时清除凭据', async () => {
  const scope = userScope(); scope.editing.value = 'existing'; scope.auth.value = 'bearer'; scope.token.value = 'secret'
  let call, loads = 0
  const api = { updateMCPConnection: async (id, body) => {call={id,body}} }
  const { save } = await componentFunctions('MCPSettings', ['draft','save','close'], {...scope, toolTimeoutValue, api, load:async()=>{loads++}, ElMessage:{success(){},error(e){throw new Error(e)}}})
  await save()
  assert.equal(call.id, 'existing')
  assert.equal(call.body.connection.headers.Authorization, 'Bearer secret')
  assert.equal(scope.visible.value, false)
  assert.equal(scope.token.value, '')
  assert.deepEqual(scope.values.value, {})
  assert.equal(loads, 1)
})

test('编辑MCP预置保留字段类型、选项和约束，拒绝重复字段', async () => {
  const fields = ref([{key:'region',title:'区域',type:'string',secret:false,required:true,definition:{enum:['cn','us'],description:'服务区域'}},{key:'count',title:'数量',type:'integer',secret:false,required:false,definition:{minimum:1}}])
  const { schema } = await componentFunctions('MCPAdminSettings', ['schema'], {fields,schemaBase:ref({additionalProperties:false})})
  const value = schema()
  assert.deepEqual(value.properties.region.enum, ['cn','us'])
  assert.equal(value.properties.count.type, 'integer')
  assert.equal(value.properties.count.minimum, 1)
  assert.equal(value.additionalProperties, false)
  assert.deepEqual(value.required, ['region'])
  fields.value.push({...fields.value[0]})
  assert.throws(schema)
})

test('MCP API使用连接路由，探测和停用独立于工具执行', async () => {
  const calls = []
  globalThis.__mcpRequest = async (path, options) => {calls.push({path,...options});return {data:[]}}
  try {
    let source = await readFile(new URL('../src/api/mcp.ts', import.meta.url), 'utf8')
    source = source.replace("import { request } from './client'", 'const request = globalThis.__mcpRequest')
    const {code} = await transformWithEsbuild(source, 'mcp.ts', {loader:'ts'})
    const api = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`)
    await api.listMCPConnections(); await api.listMCPTemplates(); await api.getMCPConnection('id')
    await api.createMCPConnection({display_name:'搜索'}); await api.updateMCPConnection('id',{is_enabled:false})
    await api.probeMCPConnection({id:'id'}); await api.deleteMCPConnection('id')
    assert.deepEqual(calls.map(c=>c.path), ['/user/mcp-connections','/user/mcp-connections/templates','/user/mcp-connections/id','/user/mcp-connections','/user/mcp-connections/id','/user/mcp-connections/probe','/user/mcp-connections/id'])
    assert.deepEqual(calls[4].body, {is_enabled:false})
    assert.deepEqual(calls[5].body, {id:'id'})
    assert.equal(calls[6].method, 'DELETE')
  } finally { delete globalThis.__mcpRequest }
})
