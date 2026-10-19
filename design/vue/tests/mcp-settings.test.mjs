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
    await api.listMCPConnectionTools('id'); await api.updateMCPTool('id','web_fetch_exa',false)
    assert.equal(calls[7].path,'/user/mcp-connections/id/tools')
    assert.equal(calls[8].path,'/user/mcp-connections/id/tools')
    assert.equal(calls[8].method,'PUT')
    assert.deepEqual(calls[8].body,{name:'web_fetch_exa',is_enabled:false})
  } finally { delete globalThis.__mcpRequest }
})

test('工具开关只提交所属连接和工具状态，失败时保留原状态并允许重试', async () => {
  const tool = {name:'search',is_enabled:true,effective_enabled:true}
  const calls=[]
  const scope={pending:ref(''),error:ref(''),props:{connection:{id:'mine',effective_enabled:true}},api:{updateMCPTool:async(...args)=>{calls.push(args);return {data:{is_enabled:false}}}}}
  const {toggleTool}=await componentFunctions('MCPConnectionTools',['toggleTool'],scope)
  await toggleTool(tool)
  assert.deepEqual(calls,[['mine','search',false]])
  assert.equal(tool.is_enabled,false); assert.equal(tool.effective_enabled,false)
  assert.equal(scope.pending.value,'')
  scope.api.updateMCPTool=async()=>{throw Error('服务暂不可用')}
  await toggleTool(tool)
  assert.equal(tool.is_enabled,false); assert.equal(scope.error.value,'服务暂不可用'); assert.equal(scope.pending.value,'')
})

test('工具清单按需加载，停用连接下仍保留工具偏好，丢弃旧配置返回的清单', async () => {
  const scope={props:{connection:{id:'mine',updated_at:'v1'}},loading:ref(false),loaded:ref(false),tools:ref([]),error:ref(''),expanded:ref(false),api:{listMCPConnectionTools:async()=>({data:{tools:[{name:'fetch',is_enabled:true,effective_enabled:false}]}})}}
  const {load,onToggle}=await componentFunctions('MCPConnectionTools',['load','onToggle'],scope)
  onToggle({target:{open:false}})
  assert.equal(scope.loaded.value,false)
  await load()
  assert.equal(scope.tools.value[0].is_enabled,true)
  assert.equal(scope.tools.value[0].effective_enabled,false)
  scope.api.listMCPConnectionTools=async()=>{scope.props.connection.updated_at='v2';return {data:{tools:[{name:'stale'}]}}}
  await load()
  assert.equal(scope.tools.value[0].name,'fetch')
})

test('系统分区添加时只选择尚未绑定的预置，自定义入口不自动选系统', async () => {
  let opens=0,changes=0
  const scope={open:async()=>{opens++},templates:ref([{id:'used'},{id:'free'}]),connections:ref([{template_id:'used'}]),templateId:ref(''),changeTemplate(){changes++},close(){},ElMessage:{info(){}}}
  const {openForSource}=await componentFunctions('MCPSettings',['openForSource'],scope)
  await openForSource('system')
  assert.equal(scope.templateId.value,'free');assert.equal(changes,1)
  await openForSource('custom')
  assert.equal(changes,1);assert.equal(opens,2)
})

test('迁移后的MCP模板保留稳定标识仍可编辑保存，不重新选择工具类型', async () => {
  let saved
  const scope = {
    busy:ref(false),system:ref(false),editing:ref('legacy-template'),name:ref('旧搜索'),key:ref('legacy/old-template-id'),description:ref('保留绑定'),
    connectionConfig:()=>({transport:'http',url:'https://example.invalid/mcp'}),schema:()=>({type:'object',properties:{}}),
    api:{adminUpdateMCPTemplate:async(id,body)=>{saved={id,body}}},close(){},load:async()=>{},ElMessage:{success(){},error(message){throw Error(message)}},
  }
  const {save}=await componentFunctions('MCPAdminSettings',['save'],scope)
  await save()
  assert.equal(saved.id,'legacy-template')
  assert.equal(saved.body.key,'legacy/old-template-id')
  assert.equal(saved.body.connection.transport,'http')
  assert.equal(scope.busy.value,false)
})
