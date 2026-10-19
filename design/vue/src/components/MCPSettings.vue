<template>
  <div class="space-y-5">
    <div class="flex items-center justify-between gap-3">
      <p class="text-sm text-slate-500">分别管理系统预置与自定义 MCP。启用连接和工具后，可在深度模式中选择使用。</p>
      <AppButton class="shrink-0 whitespace-nowrap" @click="open()">添加 MCP</AppButton>
    </div>
    <p v-if="error" class="text-sm text-red-600" role="alert">{{ error }} <button @click="load">重试</button></p>
    <p v-if="loading" class="text-sm text-slate-400">正在加载连接…</p>
    <section v-for="group in connectionGroups" :key="group.source" class="rounded-2xl border border-slate-200 p-4 sm:p-5" :aria-label="group.title">
      <div class="flex items-center justify-between gap-3"><h3 class="font-semibold text-slate-900">{{ group.title }} <span class="ml-1 text-xs font-normal text-slate-400">{{ group.items.length }}</span></h3><AppButton size="sm" variant="ghost" :disabled="busy" @click="openForSource(group.source)">{{ group.source === 'system' ? '添加系统预置' : '添加自定义 MCP' }}</AppButton></div>
      <p class="mt-1 text-xs text-slate-500">{{ group.source === 'system' ? '服务地址由管理员维护，凭据与启停设置仅对你生效。' : '你添加的远程 HTTP MCP，可管理连接配置与工具。' }}</p>
      <div v-for="c in group.items" :key="c.id" class="mt-4 border-t border-slate-100 pt-4">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div class="min-w-0"><p class="font-medium text-slate-900 break-all">{{ c.display_name }}</p><p class="mt-1 text-xs text-slate-500">{{ c.template_id ? `预置：${c.template_name || '系统 MCP'}` : '自定义 HTTP MCP' }} · {{ !c.template_enabled ? '管理员已停用' : c.is_enabled ? '已启用' : '已停用' }}</p></div>
          <div class="flex gap-2 shrink-0"><AppButton size="sm" variant="ghost" :disabled="busy" @click="open(c)">配置</AppButton><AppButton size="sm" variant="ghost" :disabled="busy || (!c.template_enabled && !c.is_enabled)" @click="toggle(c)">{{ c.is_enabled ? '停用连接' : '启用连接' }}</AppButton><AppButton size="sm" variant="ghost" :disabled="busy" @click="remove(c)">删除</AppButton></div>
        </div>
        <MCPConnectionTools :connection="c" />
      </div>
      <p v-if="!loading && !group.items.length" class="py-6 text-center text-sm text-slate-400">{{ group.source === 'system' ? '尚未添加系统预置，选择预置并填写所需凭据即可使用。' : '尚未添加自定义 MCP。' }}</p>
    </section>
    <Teleport to="body">
      <div v-if="visible" class="fixed inset-0 z-50 flex items-center justify-center bg-black/30" @click.self="close">
        <div class="bg-white rounded-2xl p-6 w-full max-w-lg mx-4 max-h-[90vh] overflow-y-auto space-y-4">
          <h3 class="text-lg font-semibold text-slate-900">{{ editing ? '配置 MCP' : '添加 MCP' }}</h3>
          <div v-if="!editing"><label class="label">连接来源</label><AppSelect v-model="templateId" class="w-full" @change="changeTemplate"><el-option value="" label="自定义 HTTP MCP"/><el-option v-for="t in templates" :key="t.id" :value="t.id" :label="t.name"/></AppSelect></div>
          <p v-if="selectedTemplate" class="text-xs text-slate-500">{{ selectedTemplate.description }}</p>
          <div><label class="label">名称</label><input v-model="name" maxlength="100" class="field"/></div>
          <template v-if="templateId">
            <p v-if="!selectedTemplate" class="text-sm text-amber-600">此预置当前不可用，可关闭后停用或删除连接。</p>
            <div v-for="(field, key) in selectedTemplate?.config_schema?.properties || {}" :key="key">
              <label class="label">{{ field.title || key }}{{ selectedTemplate?.config_schema?.required?.includes(String(key)) ? ' *' : '' }}</label>
              <el-input class="mcp-secret-input" v-if="field.secret" v-model="values[String(key)]" type="password" show-password autocomplete="off"/>
              <AppSelect v-else-if="field.enum" v-model="values[String(key)]" class="w-full"><el-option v-for="v in field.enum" :key="v" :value="v" :label="v"/></AppSelect>
              <input v-else-if="field.type === 'boolean'" v-model="values[String(key)]" type="checkbox"/>
              <input v-else v-model="values[String(key)]" :type="field.type === 'number' || field.type === 'integer' ? 'number' : 'text'" class="field"/>
              <p v-if="field.description" class="text-xs text-slate-400 mt-1">{{ field.description }}</p>
            </div>
            <p v-if="selectedTemplate && !Object.keys(selectedTemplate.config_schema?.properties || {}).length" class="text-xs text-slate-400">此预置无需额外凭据，保存后即可启用。</p>
          </template>
          <template v-else>
            <div><label class="label">Streamable HTTP 地址</label><input v-model="url" placeholder="https://mcp.example.com/mcp" class="field"/><p class="text-xs text-slate-400 mt-1">填写可从服务器访问的公网端点。</p></div>
            <div><label class="label">认证方式</label><AppSelect v-model="auth" class="w-full"><el-option value="none" label="无需认证"/><el-option value="bearer" label="Bearer Token"/><el-option value="header" label="API Key 请求头"/></AppSelect></div>
            <input v-if="auth === 'header'" v-model="headerName" placeholder="请求头名称，例如 X-API-Key" class="field"/>
            <el-input class="mcp-secret-input" v-if="auth !== 'none'" v-model="token" type="password" show-password autocomplete="off" placeholder="按该 MCP 服务的接入说明填写"/>
            <details><summary class="text-xs text-slate-500 cursor-pointer">高级配置</summary><div class="space-y-3 mt-3"><label class="label">额外请求头（JSON 对象）</label><textarea v-model="extraHeaders" rows="3" class="field font-mono"/><div class="grid grid-cols-2 gap-3"><div><label class="label">连接超时（秒）</label><input v-model="connectTimeout" type="number" min="1" max="86400" placeholder="默认30" class="field"/></div><div><label class="label">调用超时（秒）</label><input v-model="callTimeout" type="number" min="1" max="86400" placeholder="默认300" class="field"/></div></div></div></details>
          </template>
          <p v-if="result" :class="result.success ? 'text-emerald-600' : 'text-red-600'" class="text-sm" role="status">{{ result.message }}</p>
          <div v-if="result?.success" class="text-xs text-slate-500">{{ result.mcp_tools?.map(t => t.name).join('、') || '此服务未提供工具' }}</div>
          <div class="flex justify-end gap-2"><AppButton variant="secondary" :disabled="busy" @click="close">取消</AppButton><AppButton variant="outline" :disabled="busy" @click="probe">测试连接</AppButton><AppButton :disabled="busy || !name.trim()" @click="save">保存</AppButton></div>
        </div>
      </div>
    </Teleport>
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import AppButton from './ui/AppButton.vue'
import AppSelect from './ui/AppSelect.vue'
import MCPConnectionTools from './MCPConnectionTools.vue'
import * as api from '@/api/mcp'
import type { MCPConnection, MCPDraft, MCPTemplate, MCPProbeResult } from '@/types/mcp'
import { toolTimeoutValue } from '@/lib/tool-timeout.mjs'
const templates = ref<MCPTemplate[]>([]), connections = ref<MCPConnection[]>([])
const loading = ref(false), busy = ref(false), error = ref(''), visible = ref(false), editing = ref('')
const templateId = ref(''), name = ref(''), url = ref(''), auth = ref('none'), token = ref(''), headerName = ref('X-API-Key'), extraHeaders = ref('{}'), connectTimeout = ref<number | string>(''), callTimeout = ref<number | string>('')
const values = ref<Record<string, any>>({}), result = ref<MCPProbeResult | null>(null)
const selectedTemplate = computed(() => templates.value.find(t => t.id === templateId.value))
const connectionGroups = computed(() => [
  { source: 'system', title: '系统 MCP', items: connections.value.filter(c => c.template_id) },
  { source: 'custom', title: '自定义 MCP', items: connections.value.filter(c => !c.template_id) },
])
async function openForSource(source: string) {
  await open()
  if (source === 'system') {
    const available = templates.value.find(t => !connections.value.some(c => c.template_id === t.id))
    if (!available) { close(); ElMessage.info('暂无可添加的系统预置'); return }
    templateId.value = available.id; changeTemplate()
  }
}
async function load() { loading.value = true; error.value = ''; try { const [a,b] = await Promise.all([api.listMCPTemplates(),api.listMCPConnections()]); templates.value = a.data; connections.value = b.data } catch(e) { error.value = e instanceof Error ? e.message : '加载失败' } finally { loading.value = false } }
function changeTemplate() { values.value = {}; for(const [key,field] of Object.entries(selectedTemplate.value?.config_schema?.properties || {})) if(field.default !== undefined) values.value[key] = field.default; if(selectedTemplate.value) name.value = selectedTemplate.value.name; result.value = null }
async function open(c?: MCPConnection) {
  try {
    const detail = c ? (await api.getMCPConnection(c.id)).data : null
    editing.value = c?.id || ''; templateId.value = detail?.template_id || ''; name.value = detail?.display_name || ''; values.value = { ...detail?.config }; result.value = null
    const cfg = detail?.connection, headers = { ...cfg?.headers }; url.value = cfg?.url || ''; connectTimeout.value = cfg?.connect_timeout ?? cfg?.timeout ?? ''; callTimeout.value = cfg?.call_timeout ?? cfg?.timeout ?? ''; auth.value = 'none'; token.value = ''; headerName.value = 'X-API-Key'
    if(headers.Authorization?.startsWith('Bearer ')) { auth.value = 'bearer'; token.value = headers.Authorization.slice(7); delete headers.Authorization }
    extraHeaders.value = JSON.stringify(headers,null,2); visible.value = true
  } catch(e) { ElMessage.error(e instanceof Error ? e.message : '读取配置失败') }
}
function close() { if(busy.value) return; visible.value = false; token.value = ''; values.value = {}; extraHeaders.value = '{}'; url.value = ''; result.value = null }
function draft(): MCPDraft {
  if(templateId.value) {
    const config: Record<string,unknown> = {}
    for(const [key,field] of Object.entries(selectedTemplate.value?.config_schema?.properties || {})) { const value = values.value[key]; if(value !== undefined && value !== '') config[key] = field.type === 'integer' || field.type === 'number' ? Number(value) : value }
    return { display_name: name.value, template_id: templateId.value, config }
  }
  const headers = JSON.parse(extraHeaders.value)
  if(!headers || Array.isArray(headers) || typeof headers !== 'object' || Object.values(headers).some(v => typeof v !== 'string')) throw new Error('请求头必须为字符串值的 JSON 对象')
  if(auth.value === 'bearer') headers.Authorization = `Bearer ${token.value}`
  if(auth.value === 'header') { if(!headerName.value.trim()) throw new Error('请填写请求头名称'); headers[headerName.value.trim()] = token.value }
  return {display_name:name.value,connection:{transport:'http',url:url.value.trim(),headers,connect_timeout:toolTimeoutValue(connectTimeout.value),call_timeout:toolTimeoutValue(callTimeout.value)},config:{}}
}
async function probe() { busy.value = true; try { result.value = (await api.probeMCPConnection(draft())).data } catch(e) { result.value = {success:false,message:e instanceof Error ? e.message : '连接失败',response_time_ms:0} } finally { busy.value = false } }
async function save() { busy.value = true; try { const body = draft(); if(editing.value) await api.updateMCPConnection(editing.value,body); else await api.createMCPConnection(body); busy.value = false; close(); await load(); ElMessage.success('MCP配置已保存') } catch(e) { ElMessage.error(e instanceof Error ? e.message : '保存失败') } finally { busy.value = false } }
async function toggle(c:MCPConnection) { busy.value = true; try { await api.updateMCPConnection(c.id,{is_enabled:!c.is_enabled}); await load() } catch(e) { ElMessage.error(e instanceof Error ? e.message : '操作失败') } finally { busy.value = false } }
async function remove(c:MCPConnection) { try { await ElMessageBox.confirm(`删除 MCP「${c.display_name}」？`,'删除连接',{type:'warning'}); busy.value = true; await api.deleteMCPConnection(c.id); await load() } catch(e) { if(e !== 'cancel' && e !== 'close') ElMessage.error(e instanceof Error ? e.message : '删除失败') } finally { busy.value = false } }
watch([templateId,name,url,auth,token,headerName,extraHeaders,connectTimeout,callTimeout,values],() => { result.value = null },{deep:true})
onMounted(load)
</script>
<style scoped>
.label { display:block; font-size:13px; font-weight:500; color:#475569; margin-bottom:6px; }
.field { width:100%; border:1px solid #e2e8f0; border-radius:12px; padding:10px 12px; background:#f8fafc; font-size:14px; color:#0f172a; outline:none; }
.field:focus { border-color:#6366f1; }
.mcp-secret-input :deep(.el-input__wrapper) { min-height:42px; border-radius:12px; background:#f8fafc; box-shadow:0 0 0 1px #e2e8f0 inset; padding:0 12px; }
.mcp-secret-input :deep(.el-input__wrapper.is-focus) { box-shadow:0 0 0 1px #6366f1 inset; }
</style>
