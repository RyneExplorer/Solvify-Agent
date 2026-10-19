<template>
  <details class="mt-3 rounded-xl border border-slate-100 bg-slate-50/70" @toggle="onToggle">
    <summary class="cursor-pointer px-4 py-3 text-sm text-slate-600">工具列表<span v-if="loaded" class="ml-2 text-xs text-slate-400">{{ tools.length }} 个</span></summary>
    <div class="px-4 pb-4 space-y-3">
      <div class="flex items-center justify-between gap-3">
        <p class="text-xs text-slate-500">{{ connection.effective_enabled ? '仅启用的工具可供深度模式调用。' : '连接当前不可用，已保存的工具选择会保留。' }}</p>
        <AppButton size="sm" variant="ghost" class="shrink-0 whitespace-nowrap" :disabled="loading || !!pending" @click="load">刷新工具</AppButton>
      </div>
      <p v-if="!connection.template_enabled" class="text-xs text-amber-600">管理员已停用此系统预置，暂时无法读取或启用工具。</p>
      <p v-if="loading" class="text-sm text-slate-400" role="status">正在读取工具…</p>
      <p v-if="error" class="text-sm text-red-600" role="alert">{{ error }}</p>
      <div v-for="tool in tools" :key="tool.name" class="flex items-start justify-between gap-4 border-t border-slate-200/70 pt-3">
        <div class="min-w-0">
          <p class="break-all text-sm font-medium text-slate-800">{{ tool.name }}</p>
          <p v-if="tool.description" class="mt-1 whitespace-pre-wrap break-words text-xs text-slate-500">{{ tool.description }}</p>
          <p class="mt-1 text-xs" :class="tool.is_enabled && connection.effective_enabled ? 'text-emerald-600' : 'text-slate-400'">{{ tool.is_enabled ? (connection.effective_enabled ? '已启用' : '已启用，等待连接可用') : '已停用' }}</p>
        </div>
        <button type="button" role="switch" :aria-label="`${tool.name} 工具`" :aria-checked="tool.is_enabled" :disabled="loading || !!pending || (!connection.template_enabled && !tool.is_enabled)" class="shrink-0 rounded-full px-3 py-1.5 text-xs font-medium disabled:opacity-50" :class="tool.is_enabled ? 'bg-emerald-100 text-emerald-700' : 'bg-slate-200 text-slate-600'" @click="toggleTool(tool)">{{ pending === tool.name ? '保存中…' : tool.is_enabled ? '停用' : '启用' }}</button>
      </div>
      <p v-if="loaded && !loading && !tools.length && !error" class="text-sm text-slate-400">此 MCP 当前没有提供工具。</p>
    </div>
  </details>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import AppButton from './ui/AppButton.vue'
import * as api from '@/api/mcp'
import type { MCPConnection, MCPConnectionTool } from '@/types/mcp'

const props = defineProps<{ connection: MCPConnection }>()
const tools = ref<MCPConnectionTool[]>([])
const loaded = ref(false), loading = ref(false), error = ref(''), pending = ref('')
const expanded = ref(false)

async function load() {
  if (loading.value) return
  loading.value = true; error.value = ''
  const version = props.connection.updated_at
  try {
    const result = await api.listMCPConnectionTools(props.connection.id)
    if (version === props.connection.updated_at) { tools.value = result.data.tools; loaded.value = true }
  } catch (e) {
    error.value = e instanceof Error ? e.message : '读取工具失败，请稍后重试'
  } finally {
    loading.value = false
    if (expanded.value && version !== props.connection.updated_at) load()
  }
}
function onToggle(event: Event) {
  expanded.value = (event.target as HTMLDetailsElement).open
  if (expanded.value && !loaded.value && !loading.value) load()
}
async function toggleTool(tool: MCPConnectionTool) {
  if (pending.value) return
  pending.value = tool.name; error.value = ''
  try {
    const result = await api.updateMCPTool(props.connection.id, tool.name, !tool.is_enabled)
    tool.is_enabled = result.data.is_enabled
    tool.effective_enabled = result.data.is_enabled && props.connection.effective_enabled
  } catch (e) {
    error.value = e instanceof Error ? e.message : '保存工具开关失败'
  } finally { pending.value = '' }
}
watch(() => props.connection.updated_at, () => {
  tools.value = []; loaded.value = false
  if (expanded.value) load()
})
</script>
