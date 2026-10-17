<template>
  <div class="space-y-5">
    <div class="flex justify-between items-center"><p class="text-sm text-slate-500">预置可由第三方提供的 MCP 服务，用户填写配置后使用。</p><AppButton @click="open()">添加 MCP 预置</AppButton></div>
    <p v-if="error" class="text-red-600 text-sm" role="alert">{{ error }}</p>
    <div v-for="s in servers" :key="s.id" class="flex justify-between items-center border-b border-slate-100 py-4 gap-3">
      <div><p class="font-medium text-slate-900">{{ s.name }}</p><p class="text-xs text-slate-400 mt-1">{{ s.description }} · {{ s.is_system ? '部署预置' : '后台预置' }} · {{ s.is_enabled ? '已启用' : '已停用' }}</p></div>
      <div class="flex gap-2"><AppButton variant="ghost" size="sm" @click="open(s)">编辑</AppButton><AppButton variant="ghost" size="sm" :disabled="busy" @click="toggle(s)">{{ s.is_enabled ? '停用' : '启用' }}</AppButton><AppButton v-if="!s.is_system" variant="ghost" size="sm" :disabled="busy" @click="remove(s)">删除</AppButton></div>
    </div>
    <p v-if="!servers.length" class="text-center py-8 text-sm text-slate-400">暂无 MCP 预置</p>
    <Teleport to="body"><div v-if="visible" class="fixed inset-0 z-50 flex items-center justify-center bg-black/30" @click.self="close"><div class="bg-white rounded-2xl p-6 w-full max-w-xl mx-4 max-h-[90vh] overflow-y-auto space-y-4">
      <h3 class="text-lg font-semibold">{{ editing ? '编辑' : '添加' }} MCP 预置</h3>
      <fieldset :disabled="system" class="space-y-4">
        <div class="grid grid-cols-2 gap-3"><div><label class="label">名称</label><input v-model="name" class="field"/></div><div><label class="label">稳定标识</label><input v-model="key" :disabled="!!editing" class="field" placeholder="例如 web-search"/></div></div>
        <div><label class="label">说明</label><input v-model="description" class="field" placeholder="服务用途与凭据获取方式"/></div>
        <div><label class="label">传输方式</label><AppSelect v-model="form.transport" class="w-full"><el-option value="http" label="Streamable HTTP"/><el-option value="sse" label="SSE（已有服务）"/></AppSelect></div>
        <div><label class="label">MCP 地址</label><input v-model="form.url" class="field" placeholder="https://mcp.example.com/mcp"/></div>
        <div><label class="label">请求头（JSON 对象）</label><textarea v-model="headersText" rows="3" class="field font-mono" placeholder='{"Authorization":"Bearer {{api_key}}"}'/><p class="text-xs text-slate-400 mt-1" v-text="'用户字段通过 {{字段名}} 绑定请求头，地址保持固定。'"/></div>
        <div><div class="flex justify-between items-center"><label class="label">用户需要填写的配置</label><AppButton variant="ghost" size="sm" @click="fields.push({key:'',title:'',type:'string',secret:true,required:true,definition:{}})">添加字段</AppButton></div><div v-for="(f,i) in fields" :key="i" class="grid grid-cols-2 gap-2 border-b border-slate-100 py-3"><input v-model="f.key" class="field" placeholder="字段名，例如 api_key"/><input v-model="f.title" class="field" placeholder="显示名称，例如 API Key"/><AppSelect v-model="f.type" class="w-full"><el-option value="string" label="文本"/><el-option value="number" label="数字"/><el-option value="integer" label="整数"/><el-option value="boolean" label="开关"/></AppSelect><label class="text-xs text-slate-500"><input v-model="f.secret" type="checkbox"/> 密码字段</label><div class="flex justify-between"><label class="text-xs text-slate-500"><input v-model="f.required" type="checkbox"/> 必填</label><button class="text-xs text-red-600" @click="fields.splice(i,1)">删除</button></div></div></div>
      </fieldset>
      <div class="grid grid-cols-2 gap-3"><div><label class="label">连接初始化超时（秒）</label><input v-model="form.connect_timeout" type="number" min="1" max="86400" placeholder="默认30" class="field"/></div><div><label class="label">单次调用超时（秒）</label><input v-model="form.call_timeout" type="number" min="1" max="86400" placeholder="默认300" class="field"/></div></div>
      <p v-if="system" class="text-xs text-amber-600">部署预置的连接和字段由配置文件管理；此处保存的超时在重启后继续生效。</p>
      <p v-if="result" :class="result.success ? 'text-emerald-600' : 'text-red-600'" class="text-sm">{{ result.message }}</p>
      <div class="flex justify-end gap-2"><AppButton variant="secondary" :disabled="busy" @click="close">取消</AppButton><AppButton variant="outline" :disabled="busy" @click="probe">测试连接</AppButton><AppButton :disabled="busy" @click="save">保存</AppButton></div>
    </div></div></Teleport>
  </div>
</template>
<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import AppButton from './ui/AppButton.vue'
import AppSelect from './ui/AppSelect.vue'
import * as api from '@/api/admin'
import type { MCPServerInfo, ToolTestResult } from '@/types/tool'
import { toolTimeoutValue,toolTimeoutFromConfig } from '@/lib/tool-timeout.mjs'
interface MCPFormState {transport:'http'|'sse';url:string;headers:Record<string,string>;connect_timeout:number|string;call_timeout:number|string}
function createEmptyMCPForm():MCPFormState {return {transport:'http',url:'',headers:{},connect_timeout:'',call_timeout:''}}
function mcpFromJSON(raw:unknown):MCPFormState {const obj = typeof raw === 'string' ? JSON.parse(raw) : raw;const cfg = obj?.mcp || obj || {};return {...createEmptyMCPForm(),...cfg,connect_timeout:toolTimeoutFromConfig(cfg.connect_timeout ?? cfg.timeout),call_timeout:toolTimeoutFromConfig(cfg.call_timeout ?? cfg.timeout)}}
function buildMCPTimeoutConfig(f:MCPFormState) {return {connect_timeout:toolTimeoutValue(f.connect_timeout),call_timeout:toolTimeoutValue(f.call_timeout)}}
function buildMCPProviderConfig(f:MCPFormState) {if(!f.url.trim()) throw new Error('请填写 MCP 地址');return {transport:f.transport,url:f.url.trim(),headers:f.headers,...buildMCPTimeoutConfig(f)}}
const servers = ref<MCPServerInfo[]>([]), visible = ref(false), busy = ref(false), error = ref(''), editing = ref(''), typeId = ref(''), system = ref(false)
const name=ref(''),key=ref(''),description=ref(''),headersText=ref('{}'),form=ref(createEmptyMCPForm()),fields=ref<{key:string;title:string;type:string;secret:boolean;required:boolean;definition:Record<string,unknown>}[]>([]),result=ref<ToolTestResult|null>(null)
const schemaBase = ref<Record<string,unknown>>({})
async function load() {try {servers.value = (await api.adminListMCPServers()).data.servers;error.value=''} catch(e) {error.value=e instanceof Error?e.message:'加载失败'}}
async function open(s?:MCPServerInfo) {try {
  editing.value=s?.id || '';system.value=!!s?.is_system;typeId.value=s?.tool_type_id || '';name.value=s?.name || '';key.value=s?.provider_key || '';description.value=s?.description || '';fields.value=[];schemaBase.value={};form.value=createEmptyMCPForm();result.value=null
  if(s) {const p=(await api.adminListToolProviders(s.tool_type_id)).data.providers.find(p=>p.id===s.id);if(!p) throw new Error('预置不存在');form.value=mcpFromJSON(p.provider_config);const schema=p.config_schema as any;schemaBase.value={...schema};fields.value=Object.entries(schema?.properties || {}).map(([k,v]:[string,any])=>({key:k,title:v.title || k,type:v.type || 'string',secret:!!v.secret,required:!!schema.required?.includes(k),definition:{...v}}))}
  headersText.value=JSON.stringify(form.value.headers,null,2);visible.value=true
} catch(e) {ElMessage.error(e instanceof Error?e.message:'读取失败')}}
function close() {if(busy.value)return;visible.value=false;headersText.value='{}';form.value=createEmptyMCPForm();fields.value=[];schemaBase.value={};result.value=null}
function providerConfig() {const headers=JSON.parse(headersText.value);if(!headers || typeof headers !== 'object' || Array.isArray(headers) || Object.values(headers).some(v=>typeof v!=='string')) throw new Error('请求头必须为字符串值的JSON对象');return buildMCPProviderConfig({...form.value,headers})}
function schema() {const properties:Record<string,unknown>={},required:string[]=[];for(const f of fields.value) {if(!/^[a-zA-Z_][a-zA-Z0-9_]*$/.test(f.key) || properties[f.key]) throw new Error('配置字段名无效或重复');properties[f.key]={...f.definition,type:f.type,title:f.title || f.key,secret:f.secret};if(f.required)required.push(f.key)};return {...schemaBase.value,type:'object',properties,required}}
async function save() {busy.value=true;try {
 if(system.value) await api.adminUpdateToolProvider(typeId.value,editing.value,{provider_config:{mcp:buildMCPTimeoutConfig(form.value)}})
 else {
  if(!name.value.trim() || !/^[a-zA-Z0-9_-]+$/.test(key.value)) throw new Error('请填写名称和稳定标识（字母、数字、下划线或连字符）')
  if(!typeId.value) {const types=(await api.adminListToolTypes()).data.tool_types;typeId.value=types.find(t=>t.tool_key==='mcp')?.id || '';if(!typeId.value)throw new Error('MCP配置未初始化，请联系管理员')}
  const payload={name:name.value,provider_key:key.value,description:description.value,provider_type:'mcp',provider_config:providerConfig(),config_schema:schema()}
  if(editing.value)await api.adminUpdateToolProvider(typeId.value,editing.value,payload);else await api.adminCreateToolProvider(typeId.value,payload)
 }
 busy.value=false;close();await load();ElMessage.success('MCP预置已保存')
} catch(e) {ElMessage.error(e instanceof Error?e.message:'保存失败')} finally {busy.value=false}}
async function probe() {busy.value=true;try {const config=providerConfig();if(Object.values(config.headers).some(v=>v.includes('{{'))) throw new Error('此预置需要用户填写凭据，请保存后在个人 MCP 配置中测试');result.value=(await api.adminProbeTool({provider_type:'mcp',provider_config:config})).data} catch(e) {result.value={success:false,message:e instanceof Error?e.message:'连接失败',response_time_ms:0}} finally {busy.value=false}}
async function toggle(s:MCPServerInfo) {busy.value=true;try {await api.adminUpdateToolProvider(s.tool_type_id,s.id,{is_enabled:!s.is_enabled});await load()}catch(e){ElMessage.error(e instanceof Error?e.message:'操作失败')}finally{busy.value=false}}
async function remove(s:MCPServerInfo) {try {await ElMessageBox.confirm(`删除预置「${s.name}」及用户绑定？`,'删除预置',{type:'warning'});await api.adminDeleteMCPServer(s.id);await load()} catch(e) {if(e!=='cancel' && e!=='close')ElMessage.error(e instanceof Error?e.message:'删除失败')}}
onMounted(load)
</script>
<style scoped>
.label{display:block;font-size:13px;color:#475569;font-weight:500;margin-bottom:6px}.field{width:100%;border:1px solid #e2e8f0;border-radius:12px;padding:10px 12px;background:#f8fafc;font-size:14px;color:#0f172a;outline:none}.field:focus{border-color:#6366f1}
</style>
