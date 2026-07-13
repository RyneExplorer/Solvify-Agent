<template>
  <aside
    class="h-full bg-slate-50 border-r border-slate-200 flex flex-col shrink-0 transition-all duration-300"
    :class="collapsed ? 'w-[64px]' : 'w-[260px]'"
  >
    <!-- Logo -->
    <div class="h-14 flex items-center border-b border-slate-200 shrink-0" :class="collapsed ? 'px-3 justify-center' : 'px-4 gap-2.5'">
      <!-- Collapsed: show expand icon -->
      <button
        v-show="collapsed"
        @click="collapsed = !collapsed"
        class="p-1.5 hover:bg-slate-200/60 rounded-lg transition-colors"
        title="展开侧边栏"
      >
        <svg class="w-5 h-5 text-slate-500" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <rect x="3" y="3" width="18" height="18" rx="2"/>
          <path d="M9 3v18"/>
          <path d="m14 15 3-3-3-3"/>
        </svg>
      </button>
      <!-- Expanded: show logo + name + collapse button -->
      <AppLogo v-show="!collapsed" size="sm" class="flex-shrink-0" />
      <span v-show="!collapsed" class="text-base font-semibold text-slate-900" style="font-family: 'Space Grotesk', sans-serif; letter-spacing: -0.02em;">Solvify</span>
      <button
        v-show="!collapsed"
        @click="collapsed = !collapsed"
        class="ml-auto p-1.5 hover:bg-slate-200/60 rounded-lg transition-colors"
        title="收缩侧边栏"
      >
        <svg class="w-5 h-5 text-slate-500" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <rect x="3" y="3" width="18" height="18" rx="2"/>
          <path d="M15 3v18"/>
          <path d="m10 15-3-3 3-3"/>
        </svg>
      </button>
    </div>

    <!-- Main nav -->
    <nav class="px-3 py-3 space-y-0.5 shrink-0">
      <template v-for="item in visibleNavItems.filter(i => i.key !== 'search')" :key="item.key">
        <router-link
          :to="item.to"
          class="w-full flex items-center gap-2.5 rounded-[10px] text-sm transition-colors no-underline"
          :class="[
            collapsed ? 'px-3 py-2.5 justify-center' : 'px-3 py-2.5',
            isActive(item.key)
              ? 'text-accent-700 font-semibold bg-accent-50'
              : 'text-slate-600 hover:bg-slate-200/60 font-normal'
          ]"
        >
          <span class="w-5 flex items-center justify-center shrink-0" v-html="item.icon" />
          <span v-show="!collapsed">{{ item.label }}</span>
        </router-link>

        <!-- Search button right after chat -->
        <button
          v-if="item.key === 'chat'"
          @click="searchDialogRef?.open()"
          class="w-full flex items-center gap-2.5 rounded-[10px] text-sm transition-colors text-slate-600 hover:bg-slate-200/60 font-normal cursor-pointer"
          :class="collapsed ? 'px-3 py-2.5 justify-center' : 'px-3 py-2.5'"
        >
          <span class="w-5 flex items-center justify-center shrink-0">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.35-4.35"/></svg>
          </span>
          <span v-show="!collapsed">搜索</span>
        </button>
      </template>
    </nav>

    <!-- Divider -->
    <div v-show="!collapsed" class="px-3 shrink-0"><div class="border-t border-slate-200" /></div>

    <!-- History -->
    <div v-show="!collapsed" class="flex-1 overflow-hidden flex flex-col min-h-0">
      <button
        @click="historyExpanded = !historyExpanded"
        class="px-4 py-2 flex items-center justify-between shrink-0 w-full hover:bg-slate-200/40 transition-colors cursor-pointer"
      >
        <span class="text-xs font-semibold text-slate-400 uppercase tracking-wider">历史对话</span>
        <svg
          class="w-3.5 h-3.5 text-slate-400 transition-transform duration-200"
          :class="{ '-rotate-90': !historyExpanded }"
          fill="none" stroke="currentColor" viewBox="0 0 24 24"
        >
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/>
        </svg>
      </button>
      <div v-show="historyExpanded" class="flex-1 overflow-y-auto px-3 pb-2 space-y-0.5">
        <div v-if="historyList.length === 0" class="px-3 py-8 text-center text-xs text-slate-400">暂无历史对话</div>
        <div
          v-for="item in historyList"
          :key="item.id"
          class="group relative"
        >
          <!-- Rename input -->
          <div v-if="renamingId === item.id" class="flex items-center gap-1 px-1">
            <input
              ref="renameInputRef"
              v-model="renameTitle"
              @keydown.enter="confirmRename(item.id)"
              @keydown.escape="cancelRename"
              class="flex-1 px-2 py-1.5 text-sm rounded border border-accent-500 outline-none bg-white"
              autofocus
            />
            <button @click="confirmRename(item.id)" class="p-1 hover:bg-slate-200 rounded" title="确认">
              <svg class="w-3.5 h-3.5 text-emerald-600" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"/></svg>
            </button>
            <button @click="cancelRename" class="p-1 hover:bg-slate-200 rounded" title="取消">
              <svg class="w-3.5 h-3.5 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/></svg>
            </button>
          </div>

          <!-- Normal item -->
          <router-link
            v-else
            :to="`/chat/${item.id}`"
            class="flex items-center w-full text-left px-3 py-2 rounded-lg text-sm text-slate-600 transition-colors truncate no-underline hover:bg-slate-200/60"
            :class="{ 'bg-accent-50 text-accent-600 font-medium': isHistoryActive(item.id) }"
            :title="item.title"
          >
            <span class="flex-1 truncate">{{ item.title }}</span>
            <div class="relative shrink-0 opacity-0 group-hover:opacity-100 transition-opacity ml-1">
              <button
                @click.prevent.stop="toggleMenu($event, item.id)"
                class="p-1 hover:bg-slate-300/40 rounded transition-colors"
              >
                <svg class="w-3.5 h-3.5 text-slate-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 5v.01M12 12v.01M12 19v.01"/>
                </svg>
              </button>
            </div>
          </router-link>
        </div>
      </div>
    </div>

    <!-- User area -->
    <div
      class="h-14 border-t border-slate-200 flex items-center px-4 shrink-0 gap-2.5 cursor-pointer hover:bg-slate-200/40 transition-colors"
      @click="$router.push('/profile')"
    >
      <div class="w-7 h-7 rounded-full bg-accent-600 text-white flex items-center justify-center text-xs font-medium shrink-0">{{ userInitial }}</div>
      <div v-show="!collapsed" class="min-w-0 flex-1">
        <div class="text-[13px] font-medium text-slate-900 truncate leading-tight">{{ userName }}</div>
        <div class="text-[11px] text-slate-400 truncate leading-tight">{{ userEmail }}</div>
      </div>
      <button
        v-show="!collapsed"
        @click.stop="$emit('logout')"
        class="p-1.5 hover:bg-slate-200/60 rounded-md transition-colors"
        title="退出登录"
      >
        <svg class="w-4 h-4 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1"/></svg>
      </button>
    </div>
  </aside>

  <!-- Context Menu (teleported to body to avoid overflow clipping) -->
  <Teleport to="body">
    <div
      v-if="openMenuId"
      class="fixed w-32 bg-white border border-slate-200 rounded-lg shadow-lg z-[9999] py-1"
      :style="{ top: menuPos.top + 'px', left: menuPos.left + 'px' }"
    >
      <button
        @click.prevent.stop="startRename(openMenuId!)"
        class="w-full text-left px-3 py-1.5 text-xs text-slate-600 hover:bg-slate-50 flex items-center gap-2"
      >
        <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z"/></svg>
        重命名
      </button>
      <button
        @click.prevent.stop="confirmDeleteId = openMenuId; openMenuId = null"
        class="w-full text-left px-3 py-1.5 text-xs text-red-600 hover:bg-red-50 flex items-center gap-2"
      >
        <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/></svg>
        删除
      </button>
    </div>

    <!-- Delete Confirmation Dialog -->
    <div
      v-if="confirmDeleteId"
      class="fixed inset-0 z-[10000] flex items-center justify-center"
    >
      <div class="absolute inset-0 bg-black/30" @click="confirmDeleteId = null" />
      <div class="relative bg-white rounded-xl shadow-xl border border-slate-200 p-5 w-72">
        <h3 class="text-sm font-semibold text-slate-900 mb-2">确认删除</h3>
        <p class="text-xs text-slate-500 mb-5">删除后将无法恢复，确定要删除这个对话吗？</p>
        <div class="flex justify-end gap-2">
          <button
            @click="confirmDeleteId = null"
            class="px-3 py-1.5 text-xs rounded-lg border border-slate-200 text-slate-600 hover:bg-slate-50"
          >取消</button>
          <button
            @click="handleDelete(confirmDeleteId!)"
            class="px-3 py-1.5 text-xs rounded-lg bg-red-600 text-white hover:bg-red-700"
          >删除</button>
        </div>
      </div>
    </div>
  </Teleport>

  <!-- Search Dialog -->
  <SearchDialog ref="searchDialogRef" />
</template>

<script setup lang="ts">
import { computed, ref, nextTick, onMounted, onUnmounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { updateSession, deleteSession } from '@/api/chat'
import AppLogo from '@/components/AppLogo.vue'
import SearchDialog from '@/components/SearchDialog.vue'
import { useAuth } from '@/composables/useAuth'

const historyExpanded = ref(true)
const collapsed = ref(false)
const searchDialogRef = ref<InstanceType<typeof SearchDialog>>()
const { isAdmin } = useAuth()

interface NavItem {
  key: string
  label: string
  to: string
  icon: string
  adminOnly?: boolean
}

const navItems: NavItem[] = [
  {
    key: 'chat',
    label: '新对话',
    to: '/chat',
    icon: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>`,
  },
  {
    key: 'search',
    label: '搜索',
    to: '/search',
    icon: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.35-4.35"/></svg>`,
  },
  {
    key: 'kb',
    label: '知识库',
    to: '/kb',
    icon: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/></svg>`,
  },
  {
    key: 'docs',
    label: '文档',
    to: '/docs',
    icon: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>`,
  },
  {
    key: 'settings',
    label: '配置',
    to: '/settings',
    icon: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9c.26.604.852.997 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/></svg>`,
  },
  {
    key: 'admin',
    label: '管理',
    to: '/admin',
    adminOnly: true,
    icon: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>`,
  },
]

const visibleNavItems = computed(() => navItems.filter(item => !item.adminOnly || isAdmin.value))

interface HistoryItem { id: string; title: string }

const props = withDefaults(
  defineProps<{
    historyList?: HistoryItem[]
    userName?: string
    userEmail?: string
  }>(),
  {
    historyList: () => [],
    userName: 'Admin',
    userEmail: 'admin@solvify.ai',
  },
)

const emit = defineEmits<{
  (e: 'logout'): void
  (e: 'refresh'): void
}>()

const route = useRoute()
const router = useRouter()
const userInitial = computed(() => props.userName.charAt(0).toUpperCase())

// ── Menu ──
const openMenuId = ref<string | null>(null)
const menuPos = ref({ top: 0, left: 0 })
const confirmDeleteId = ref<string | null>(null)

function toggleMenu(event: MouseEvent, id: string) {
  if (openMenuId.value === id) {
    openMenuId.value = null
    return
  }
  const rect = (event.currentTarget as HTMLElement).getBoundingClientRect()
  menuPos.value = {
    top: rect.bottom + 4,
    left: rect.right - 128,
  }
  openMenuId.value = id
}

function closeMenu() {
  openMenuId.value = null
}

// ── Rename ──
const renamingId = ref<string | null>(null)
const renameTitle = ref('')
const renameInputRef = ref<HTMLInputElement[]>([])

function startRename(idOrItem: string | HistoryItem) {
  const item = typeof idOrItem === 'string'
    ? props.historyList.find(h => h.id === idOrItem)
    : idOrItem
  if (!item) return
  closeMenu()
  renamingId.value = item.id
  renameTitle.value = item.title
  nextTick(() => {
    const input = renameInputRef.value?.[0]
    if (input) { input.focus(); input.select() }
  })
}

function cancelRename() {
  renamingId.value = null
  renameTitle.value = ''
}

async function confirmRename(id: string) {
  const title = renameTitle.value.trim()
  if (!title) return cancelRename()
  try {
    await updateSession(id, { title })
    emit('refresh')
  } catch { /* silent */ }
  cancelRename()
}

// ── Delete ──
async function handleDelete(id: string) {
  closeMenu()
  confirmDeleteId.value = null
  try {
    await deleteSession(id)
    // 如果删除的是当前正在查看的会话，跳转到对话首页
    if (route.params.sessionId === id) {
      router.push('/chat')
    }
    emit('refresh')
  } catch { /* silent */ }
}

// ── Click outside to close menu ─
function onDocClick() {
  openMenuId.value = null
}

// ─ Global shortcut Ctrl+K ──
function onGlobalKeydown(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key === 'k') {
    e.preventDefault()
    searchDialogRef.value?.open()
  }
}

onMounted(() => {
  document.addEventListener('click', onDocClick)
  document.addEventListener('keydown', onGlobalKeydown)
})
onUnmounted(() => {
  document.removeEventListener('click', onDocClick)
  document.removeEventListener('keydown', onGlobalKeydown)
})

// ── Nav ──
function isActive(key: string): boolean {
  if (key === 'chat') return route.path === '/chat'
  return route.path === `/${key}`
}

function isHistoryActive(id: string): boolean {
  return route.params.sessionId === id
}
</script>
