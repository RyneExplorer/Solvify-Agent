<template>
  <div class="bg-white rounded-2xl border border-slate-200 shadow-sm overflow-hidden">
    <textarea
      :value="input"
      @input="onInput"
      @keydown="onKeydown"
      placeholder="请输入您想要咨询的问题..."
      rows="2"
      class="w-full px-5 py-3 text-sm text-slate-700 placeholder-slate-300 resize-none border-none outline-none"
    />
    <div class="flex items-center justify-between px-4 py-2 border-t border-slate-100">
      <div class="flex items-center gap-2">
        <!-- KB Dropdown -->
        <div class="relative" ref="kbDropdownRef">
          <button
            @click="toggleKbDropdown"
            class="tool-trigger"
          >
            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6.253v13m0-13C10.832 5.477 9.246 5 7.5 5S4.168 5.477 3 6.253v13C4.168 18.477 5.754 18 7.5 18s3.332.477 4.5 1.253m0-13C13.168 5.477 14.754 5 16.5 5c1.747 0 3.332.477 4.5 1.253v13C19.832 18.477 18.247 18 16.5 18c-1.746 0-3.332.477-4.5 1.253"/></svg>
            <span>{{ kbTriggerText }}</span>
            <svg class="w-3 h-3 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/></svg>
          </button>

          <Teleport to="body">
            <Transition name="kb-fade">
              <div
                v-if="showKbDropdown"
                ref="kbPanelRef"
                class="fixed bg-white border border-slate-200 rounded-xl shadow-xl z-[100] overflow-hidden"
                :style="{ top: kbPanelPos.top + 'px', left: kbPanelPos.left + 'px', width: '288px' }"
              >
                <div class="px-4 py-3 border-b border-slate-100">
                  <div class="text-sm font-semibold text-slate-900">知识库范围</div>
                  <div class="text-xs text-slate-400 mt-0.5">已包含全部知识库，取消勾选可排除不需要的</div>
                </div>

                <div class="px-3 py-2 border-b border-slate-100">
                  <div class="relative">
                    <svg class="w-4 h-4 text-slate-400 absolute left-2.5 top-1/2 -translate-y-1/2" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"/></svg>
                    <input
                      v-model="kbSearch"
                      placeholder="搜索知识库..."
                      class="w-full pl-8 pr-3 py-1.5 text-xs bg-slate-50 rounded-lg outline-none border border-transparent focus:border-slate-200"
                    />
                  </div>
                </div>

                <div class="max-h-[240px] overflow-y-auto py-1">
                  <div
                    @click="toggleAllKb"
                    class="px-4 py-2 flex items-center justify-between cursor-pointer hover:bg-slate-50"
                  >
                    <div class="flex items-center gap-2.5">
                      <span
                        class="w-4 h-4 rounded flex items-center justify-center shrink-0 transition-colors"
                        :class="isAllSelected ? 'bg-emerald-500' : 'border border-slate-300'"
                      >
                        <svg v-if="isAllSelected" class="w-2.5 h-2.5 text-white" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><path d="M20 6L9 17l-5-5"/></svg>
                      </span>
                      <span :class="isAllSelected ? 'text-emerald-600 font-medium' : 'text-slate-700'">全部知识库</span>
                    </div>
                    <button
                      v-if="!isAllSelected"
                      @click.stop="selectAllKb"
                      class="text-xs text-slate-400 hover:text-slate-600"
                    >清空</button>
                  </div>

                  <div
                    v-for="kb in filteredKBs"
                    :key="kb.id"
                    @click="$emit('toggleKb', kb.id)"
                    class="px-4 py-2 flex items-center justify-between cursor-pointer hover:bg-slate-50"
                  >
                    <div class="flex items-center gap-2.5 min-w-0 flex-1">
                      <span
                        class="w-4 h-4 rounded flex items-center justify-center shrink-0 transition-colors"
                        :class="selectedKBs.includes(kb.id) ? 'bg-emerald-500' : 'border border-slate-300'"
                      >
                        <svg v-if="selectedKBs.includes(kb.id)" class="w-2.5 h-2.5 text-white" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><path d="M20 6L9 17l-5-5"/></svg>
                      </span>
                      <span
                        class="text-sm truncate"
                        :class="selectedKBs.includes(kb.id) ? 'text-emerald-600 font-medium' : 'text-slate-700'"
                      >{{ kb.name }}</span>
                    </div>
                    <span class="text-xs text-slate-400 shrink-0 ml-2">{{ kb.document_count ?? 0 }}篇</span>
                  </div>
                </div>
              </div>
            </Transition>
          </Teleport>
        </div>

        <!-- Search mode -->
        <div class="relative" ref="searchDropdownRef">
          <button @click="toggleSearchDropdown" class="tool-trigger">
            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z"/></svg>
            <span>{{ searchModeLabel }}</span>
            <svg class="w-3 h-3 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/></svg>
          </button>

          <Teleport to="body">
            <Transition name="kb-fade">
              <div
                v-if="showSearchDropdown"
                ref="searchPanelRef"
                class="fixed bg-white border border-slate-200 rounded-xl shadow-xl z-[100] overflow-hidden py-1"
                :style="{ top: searchPanelPos.top + 'px', left: searchPanelPos.left + 'px', width: '160px' }"
              >
                <div
                  v-for="opt in searchOptions"
                  :key="opt.value"
                  @click="selectSearchMode(opt.value)"
                  class="px-4 py-2 text-sm cursor-pointer hover:bg-slate-50 flex items-center justify-between"
                  :class="searchMode === opt.value ? 'text-emerald-600 font-medium' : 'text-slate-700'"
                >
                  <span>{{ opt.label }}</span>
                  <svg v-if="searchMode === opt.value" class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M5 13l4 4L19 7"/></svg>
                </div>
              </div>
            </Transition>
          </Teleport>
        </div>

        <!-- Model -->
        <div class="relative" ref="modelDropdownRef">
          <button @click="toggleModelDropdown" class="tool-trigger">
            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z"/></svg>
            <span>{{ modelLabel }}</span>
            <svg class="w-3 h-3 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/></svg>
          </button>

          <Teleport to="body">
            <Transition name="kb-fade">
              <div
                v-if="showModelDropdown"
                ref="modelPanelRef"
                class="fixed bg-white border border-slate-200 rounded-xl shadow-xl z-[100] overflow-hidden py-1 max-h-[260px] overflow-y-auto"
                :style="{ top: modelPanelPos.top + 'px', left: modelPanelPos.left + 'px', width: '192px' }"
              >
                <template v-if="sysOpts.length">
                  <div class="px-3 py-1.5 text-[11px] text-slate-400 font-medium">系统模型</div>
                  <div
                    v-for="m in sysOpts"
                    :key="m.id"
                    @click="selectModel(m.id)"
                    class="px-4 py-2 text-sm cursor-pointer hover:bg-slate-50 flex items-center justify-between"
                    :class="modelValue === m.id ? 'text-emerald-600 font-medium' : 'text-slate-700'"
                  >
                    <span class="truncate">{{ m.name }}</span>
                    <svg v-if="modelValue === m.id" class="w-4 h-4 shrink-0 ml-2" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M5 13l4 4L19 7"/></svg>
                  </div>
                </template>
                <template v-if="userOpts.length">
                  <div class="px-3 py-1.5 text-[11px] text-slate-400 font-medium">我的模型</div>
                  <div
                    v-for="m in userOpts"
                    :key="m.id"
                    @click="selectModel(m.id)"
                    class="px-4 py-2 text-sm cursor-pointer hover:bg-slate-50 flex items-center justify-between"
                    :class="modelValue === m.id ? 'text-emerald-600 font-medium' : 'text-slate-700'"
                  >
                    <span class="truncate">{{ m.name }}</span>
                    <svg v-if="modelValue === m.id" class="w-4 h-4 shrink-0 ml-2" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M5 13l4 4L19 7"/></svg>
                  </div>
                </template>
              </div>
            </Transition>
          </Teleport>
        </div>

        <!-- MCP 服务器加载中骨架 -->
        <div v-if="mcpLoading && !mcpOptions.length" class="flex items-center px-2.5" title="MCP 服务器加载中...">
          <div class="h-6 w-20 bg-slate-100 rounded-md animate-pulse" />
        </div>
        <!-- MCP 服务器（仅当有可用 MCP 时显示） -->
        <div v-else-if="mcpOptions.length" class="relative" ref="mcpDropdownRef">
          <button @click="toggleMCPDropdown" class="tool-trigger" :class="{ 'opacity-60': loading }" :disabled="loading">
            <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01"/></svg>
            <span :title="mcpTooltip">{{ mcpLabel }}</span>
            <svg class="w-3 h-3 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/></svg>
          </button>

          <Teleport to="body">
            <Transition name="kb-fade">
              <div
                v-if="showMCPDropdown"
                ref="mcpPanelRef"
                class="fixed bg-white border border-slate-200 rounded-xl shadow-xl z-[100] overflow-hidden"
                :style="{ top: mcpPanelPos.top + 'px', left: mcpPanelPos.left + 'px', width: '288px' }"
              >
                <div class="px-4 py-3 border-b border-slate-100">
                  <div class="text-sm font-semibold text-slate-900">MCP 服务器</div>
                  <div class="text-xs text-slate-400 mt-0.5">勾选要在本次对话启用的服务器；全部不勾选=使用全部</div>
                </div>
                <!-- 搜索框：≥ 7 台时显示 -->
                <div v-if="showMCPSearch" class="px-3 py-2 border-b border-slate-100">
                  <div class="flex items-center gap-1.5 px-2 py-1.5 bg-slate-50 rounded-lg border border-slate-200 focus-within:border-emerald-400 focus-within:bg-white transition-colors">
                    <svg class="w-3.5 h-3.5 text-slate-400 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"/></svg>
                    <input
                      v-model="mcpSearch"
                      type="text"
                      placeholder="搜索 MCP 名称或描述..."
                      class="flex-1 bg-transparent outline-none text-xs text-slate-700 placeholder:text-slate-400"
                    />
                    <button v-if="mcpSearch" @click="mcpSearch = ''" class="text-slate-400 hover:text-slate-600 shrink-0">
                      <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/></svg>
                    </button>
                  </div>
                </div>
                <div class="px-3 py-2 border-b border-slate-100 flex items-center gap-2">
                  <button
                    @click="selectAllMCP"
                    :class="[
                      'text-xs px-2 py-1 rounded-md border border-slate-200 transition-colors',
                      localMCPIds.length === mcpOpts.length && mcpOpts.length > 0 ? 'bg-emerald-50 text-emerald-600' : 'text-slate-500 hover:bg-slate-50'
                    ]"
                  >全选</button>
                  <button
                    @click="clearMCP"
                    :class="[
                      'text-xs px-2 py-1 rounded-md border border-slate-200 transition-colors',
                      localMCPIds.length === 0 ? 'bg-emerald-50 text-emerald-600' : 'text-slate-500 hover:bg-slate-50'
                    ]"
                  >清空(=全部)</button>
                </div>
                <div class="max-h-[260px] overflow-y-auto py-1">
                  <div v-if="filteredMCPOpts.length === 0" class="px-4 py-6 text-center text-xs text-slate-400">
                    {{ mcpSearch ? '没有匹配的 MCP 服务器' : '暂无可用 MCP 服务器' }}
                  </div>
                  <div
                    v-for="o in filteredMCPOpts"
                    :key="o.id"
                    @click="toggleMCPOne(o.id)"
                    class="px-4 py-2 cursor-pointer hover:bg-slate-50"
                  >
                    <div class="flex items-start gap-2.5">
                      <span
                        class="w-4 h-4 mt-0.5 rounded flex items-center justify-center shrink-0 transition-colors"
                        :class="localMCPIds.includes(o.id) ? 'bg-emerald-500' : 'border border-slate-300'"
                      >
                        <svg v-if="localMCPIds.includes(o.id)" class="w-2.5 h-2.5 text-white" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3"><path d="M20 6L9 17l-5-5"/></svg>
                      </span>
                      <div class="flex-1 min-w-0">
                        <div :class="['text-sm truncate', localMCPIds.includes(o.id) ? 'text-emerald-600 font-medium' : 'text-slate-700']">{{ o.name }}</div>
                        <div v-if="o.description" class="text-xs text-slate-400 mt-0.5 truncate">{{ o.description }}</div>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            </Transition>
          </Teleport>
        </div>
      </div>

      <button v-if="loading" @click="$emit('stop')"
        class="w-9 h-9 rounded-full flex items-center justify-center transition-all bg-red-500 hover:bg-red-600 text-white shadow-md" title="停止生成">
        <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 24 24"><rect x="6" y="6" width="12" height="12" rx="2"/></svg>
      </button>
      <template v-else>
        <button @click="$emit('send')" :disabled="!input.trim()"
          class="w-9 h-9 rounded-full flex items-center justify-center transition-all"
          :class="input.trim() ? 'bg-slate-800 hover:bg-slate-700 text-white shadow-md' : 'bg-slate-100 text-slate-300 cursor-not-allowed'">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M5 10l7-7m0 0l7 7m-7-7v18"/></svg>
        </button>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick, watch } from 'vue'

interface KbItem { id: string; name: string; document_count?: number }
interface ModelItem { id: string; name: string; modelType: 'system' | 'user' }
interface MCPOption { id: string; name: string; description: string }

const props = defineProps<{
  input: string
  modelValue: string
  loading: boolean
  searchMode: 'quick' | 'smart-reasoning'
  kbText: string
  knowledgeBases: KbItem[]
  selectedKBs: string[]
  modelOptions: ModelItem[]
  mcpOptions?: MCPOption[]
  selectedMCPIds?: string[]
  mcpLoading?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:input', v: string): void
  (e: 'update:modelValue', v: string): void
  (e: 'send'): void
  (e: 'stop'): void
  (e: 'toggleKb', id: string): void
  (e: 'toggleSearchMode', v: 'quick' | 'smart-reasoning'): void
  (e: 'update:selectedMcpIds', v: string[]): void
}>()

const kbDropdownRef = ref<HTMLDivElement>()
const searchDropdownRef = ref<HTMLDivElement>()
const modelDropdownRef = ref<HTMLDivElement>()
const mcpDropdownRef = ref<HTMLDivElement>()
const kbPanelRef = ref<HTMLDivElement>()
const searchPanelRef = ref<HTMLDivElement>()
const modelPanelRef = ref<HTMLDivElement>()
const mcpPanelRef = ref<HTMLDivElement>()

const showKbDropdown = ref(false)
const showSearchDropdown = ref(false)
const showModelDropdown = ref(false)
const showMCPDropdown = ref(false)
const kbSearch = ref('')
const mcpSearch = ref('')

const kbPanelPos = ref({ top: 0, left: 0 })
const searchPanelPos = ref({ top: 0, left: 0 })
const modelPanelPos = ref({ top: 0, left: 0 })
const mcpPanelPos = ref({ top: 0, left: 0 })

// 本地副本：父组件 selectedMCPIds 变化时同步（含初始化）；本地操作后再 emit
const localMCPIds = ref<string[]>([])
watch(() => props.selectedMCPIds, (v) => { localMCPIds.value = [...(v ?? [])] }, { immediate: true, deep: false })
// 关闭下拉时清空搜索关键字
watch(showMCPDropdown, (open) => { if (!open) mcpSearch.value = '' })
// 当用户未启用任何 MCP 时，mcpOptions 为空，不显示

const sysOpts = computed(() => props.modelOptions.filter(m => m.modelType === 'system'))
const userOpts = computed(() => props.modelOptions.filter(m => m.modelType === 'user'))
const mcpOpts = computed(() => props.mcpOptions ?? [])
const showMCPSearch = computed(() => mcpOpts.value.length >= 7)
const filteredMCPOpts = computed(() => {
  const kw = mcpSearch.value.trim().toLowerCase()
  if (!kw) return mcpOpts.value
  return mcpOpts.value.filter(o =>
    o.name.toLowerCase().includes(kw) ||
    (o.description ?? '').toLowerCase().includes(kw),
  )
})
// 发送按钮旁边 / trigger 的 tooltip：列出当前生效的 MCP 名称
const mcpTooltip = computed(() => {
  if (!mcpOpts.value.length) return ''
  const total = mcpOpts.value.length
  if (!localMCPIds.value.length) return `使用全部 ${total} 台 MCP：\n${mcpOpts.value.map(o => '· ' + o.name).join('\n')}`
  const names = mcpOpts.value.filter(o => localMCPIds.value.includes(o.id)).map(o => o.name)
  return `已选择 ${names.length}/${total} 台 MCP：\n${names.map(n => '· ' + n).join('\n')}`
})

const mcpLabel = computed(() => {
  if (!mcpOpts.value.length) return 'MCP 服务器'
  if (!localMCPIds.value.length) return '全部 MCP'
  const names = mcpOpts.value.filter(o => localMCPIds.value.includes(o.id)).map(o => o.name)
  if (names.length <= 2) return names.join('、') || '选择 MCP'
  return `${names[0]} +${names.length - 1}`
})

const searchOptions: { value: 'quick' | 'smart-reasoning'; label: string }[] = [
  { value: 'quick', label: '快速检索' },
  { value: 'smart-reasoning', label: '深度模式' },
]

const searchModeLabel = computed(() =>
  searchOptions.find(o => o.value === props.searchMode)?.label ?? '快速检索'
)

const modelLabel = computed(() =>
  props.modelOptions.find(m => m.id === props.modelValue)?.name ?? '选择模型'
)

const filteredKBs = computed(() => {
  if (!kbSearch.value.trim()) return props.knowledgeBases
  return props.knowledgeBases.filter(k => k.name.toLowerCase().includes(kbSearch.value.toLowerCase()))
})

const isAllSelected = computed(() =>
  props.knowledgeBases.length > 0 && props.knowledgeBases.every(k => props.selectedKBs.includes(k.id))
)

const kbTriggerText = computed(() => {
  if (isAllSelected.value) return '全部知识库'
  if (!props.selectedKBs.length) return '全部知识库'
  return props.selectedKBs
    .map(id => props.knowledgeBases.find(k => k.id === id)?.name ?? id)
    .join(', ')
})

function calcPos(trigger: HTMLElement, panelHeight: number, panelWidth: number) {
  const rect = trigger.getBoundingClientRect()
  return {
    top: rect.top - panelHeight - 8,
    left: Math.min(rect.left, window.innerWidth - panelWidth - 8),
  }
}

function estimatePanelHeight(itemCount: number, itemHeight: number, headerHeight: number) {
  return headerHeight + itemCount * itemHeight
}

async function toggleKbDropdown() {
  showKbDropdown.value = !showKbDropdown.value
  if (showKbDropdown.value) {
    await nextTick()
    if (kbDropdownRef.value) {
      const count = 2 + filteredKBs.value.length
      const height = estimatePanelHeight(count, 36, 90)
      kbPanelPos.value = calcPos(kbDropdownRef.value, Math.min(height, 330), 288)
    }
  }
}

async function toggleSearchDropdown() {
  showSearchDropdown.value = !showSearchDropdown.value
  if (showSearchDropdown.value) {
    await nextTick()
    if (searchDropdownRef.value) {
      const height = estimatePanelHeight(searchOptions.length, 36, 8)
      searchPanelPos.value = calcPos(searchDropdownRef.value, height, 160)
    }
  }
}

async function toggleModelDropdown() {
  showModelDropdown.value = !showModelDropdown.value
  if (showModelDropdown.value) {
    await nextTick()
    if (modelDropdownRef.value) {
      const count = sysOpts.value.length + userOpts.value.length + (sysOpts.value.length ? 1 : 0) + (userOpts.value.length ? 1 : 0)
      const height = estimatePanelHeight(count, 36, 8)
      modelPanelPos.value = calcPos(modelDropdownRef.value, Math.min(height, 268), 192)
    }
  }
}

function toggleAllKb() {
  if (isAllSelected.value) {
    for (const id of props.knowledgeBases.map(k => k.id)) {
      if (props.selectedKBs.includes(id)) emit('toggleKb', id)
    }
  } else {
    for (const id of props.knowledgeBases.map(k => k.id)) {
      if (!props.selectedKBs.includes(id)) emit('toggleKb', id)
    }
  }
}

function selectAllKb() {
  for (const id of props.knowledgeBases.map(k => k.id)) {
    if (!props.selectedKBs.includes(id)) emit('toggleKb', id)
  }
}

function selectSearchMode(val: 'quick' | 'smart-reasoning') {
  emit('toggleSearchMode', val)
  showSearchDropdown.value = false
}

function selectModel(val: string) {
  emit('update:modelValue', val)
  showModelDropdown.value = false
}

async function toggleMCPDropdown() {
  showMCPDropdown.value = !showMCPDropdown.value
  if (showMCPDropdown.value) {
    await nextTick()
    if (mcpDropdownRef.value) {
      const count = mcpOpts.value.length + 4
      const height = estimatePanelHeight(count, 36, 120)
      mcpPanelPos.value = calcPos(mcpDropdownRef.value, Math.min(height, 400), 288)
    }
  }
}

function toggleMCPOne(id: string) {
  const idx = localMCPIds.value.indexOf(id)
  if (idx >= 0) localMCPIds.value.splice(idx, 1)
  else localMCPIds.value.push(id)
  emit('update:selectedMcpIds', [...localMCPIds.value])
}

function selectAllMCP() {
  localMCPIds.value = [...mcpOpts.value.map(o => o.id)]
  emit('update:selectedMcpIds', [...localMCPIds.value])
  showMCPDropdown.value = false
}

function clearMCP() {
  localMCPIds.value = []
  emit('update:selectedMcpIds', [])
  showMCPDropdown.value = false
}

function onDocClick(e: MouseEvent) {
  const target = e.target as Node
  const insideKb = kbDropdownRef.value?.contains(target) || kbPanelRef.value?.contains(target)
  const insideSearch = searchDropdownRef.value?.contains(target) || searchPanelRef.value?.contains(target)
  const insideModel = modelDropdownRef.value?.contains(target) || modelPanelRef.value?.contains(target)
  const insideMCP = mcpDropdownRef.value?.contains(target) || mcpPanelRef.value?.contains(target)

  if (!insideKb) {
    showKbDropdown.value = false
    kbSearch.value = ''
  }
  if (!insideSearch) showSearchDropdown.value = false
  if (!insideModel) showModelDropdown.value = false
  if (!insideMCP) showMCPDropdown.value = false
}

onMounted(() => document.addEventListener('click', onDocClick))
onUnmounted(() => document.removeEventListener('click', onDocClick))

function onInput(e: Event) {
  const el = e.target as HTMLTextAreaElement
  el.style.height = 'auto'
  el.style.height = Math.min(el.scrollHeight, 200) + 'px'
  emit('update:input', el.value)
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    emit('send')
  }
}
</script>

<style scoped>
.tool-trigger {
  @apply flex items-center gap-1.5 text-xs text-slate-700 hover:bg-slate-50 rounded-lg px-3 py-1.5 transition-colors;
}

.kb-fade-enter-active,
.kb-fade-leave-active {
  transition: opacity 0.15s ease, transform 0.15s ease;
}
.kb-fade-enter-from,
.kb-fade-leave-to {
  opacity: 0;
  transform: translateY(4px);
}
</style>
