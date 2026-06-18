<template>
  <aside class="w-[260px] h-full bg-slate-50 border-r border-slate-200 flex flex-col shrink-0">
    <!-- Logo 区域 -->
    <div class="h-14 flex items-center px-5 border-b border-slate-200 shrink-0 gap-2.5">
      <div class="w-7 h-7 rounded-lg bg-accent-600 text-white flex items-center justify-center text-sm font-bold flex-shrink-0"
           style="font-family: 'Space Grotesk', sans-serif;">
        S
      </div>
      <span class="text-base font-semibold text-slate-900"
            style="font-family: 'Space Grotesk', sans-serif; letter-spacing: -0.02em;">
        Solvify
      </span>
      <button class="ml-auto p-1.5 hover:bg-slate-200/60 rounded-lg transition-colors">
        <svg class="w-4 h-4 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16"/>
        </svg>
      </button>
    </div>

    <!-- 主导航 -->
    <nav class="px-3 py-3 space-y-0.5 shrink-0">
      <button
        v-for="item in navItems"
        :key="item.key"
        @click="$emit('navigate', item.key)"
        :class="[
          'w-full flex items-center gap-2.5 px-3 py-2.5 rounded-[10px] text-sm transition-colors',
          activePage === item.key
            ? 'bg-accent-600 text-white font-medium'
            : 'text-slate-600 hover:bg-slate-200/60 font-normal'
        ]"
      >
        <span class="w-5 flex items-center justify-center shrink-0" v-html="item.icon"></span>
        <span>{{ item.label }}</span>
      </button>
    </nav>

    <!-- 分隔线 -->
    <div class="px-3 shrink-0">
      <div class="border-t border-slate-200"></div>
    </div>

    <!-- 历史对话 -->
    <div class="flex-1 overflow-hidden flex flex-col min-h-0">
      <div class="px-4 py-2 flex items-center justify-between shrink-0">
        <span class="text-xs font-semibold text-slate-400 uppercase tracking-wider">历史对话</span>
        <button class="p-1 hover:bg-slate-200/60 rounded transition-colors">
          <svg class="w-3.5 h-3.5 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/>
          </svg>
        </button>
      </div>

      <div class="flex-1 overflow-y-auto px-3 pb-2 space-y-0.5">
        <button
          v-for="(item, index) in historyList"
          :key="index"
          @click="$emit('selectItem', item)"
          :class="[
            'w-full text-left px-3 py-2 rounded-lg text-sm transition-colors truncate',
            activeItem === item
              ? 'bg-accent-50 text-accent-600 font-medium'
              : 'text-slate-600 hover:bg-slate-200/60'
          ]"
          :title="item"
        >
          {{ item }}
        </button>

        <div v-if="historyList.length === 0" class="px-3 py-8 text-center text-xs text-slate-400">
          暂无历史对话
        </div>
      </div>
    </div>

    <!-- 底部用户区 -->
    <div class="h-14 border-t border-slate-200 flex items-center px-4 shrink-0 gap-2.5">
      <div class="w-7 h-7 rounded-full bg-accent-600 text-white flex items-center justify-center text-xs font-medium shrink-0">
        {{ userInitial }}
      </div>
      <div class="min-w-0 flex-1">
        <div class="text-[13px] font-medium text-slate-900 truncate leading-tight">{{ userName }}</div>
        <div class="text-[11px] text-slate-400 truncate leading-tight">{{ userEmail }}</div>
      </div>
      <div class="flex items-center gap-0.5">
        <button class="p-1.5 hover:bg-slate-200/60 rounded-md transition-colors" title="语言切换">
          <svg class="w-4 h-4 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 01-9 9m9-9a9 9 0 00-9-9m9 9H3m9 9a9 9 0 01-9-9m9 9c1.657 0 3-4.03 3-9s-1.343-9-3-9m0 18c-1.657 0-3-4.03-3-9s1.343-9 3-9m-9 9a9 9 0 019-9"/>
          </svg>
        </button>
        <button class="p-1.5 hover:bg-slate-200/60 rounded-md transition-colors" title="设置">
          <svg class="w-4 h-4 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"/>
            <circle cx="12" cy="12" r="3"/>
          </svg>
        </button>
      </div>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue'

// ── Navigation items (matching Solvify prototype) ──
const navItems = [
  {
    key: 'dashboard',
    label: '概览',
    icon: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/>
    </svg>`,
  },
  {
    key: 'qa',
    label: '新对话',
    icon: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <path d="M12 4v16m8-8H4"/>
    </svg>`,
  },
  {
    key: 'kb',
    label: '知识库',
    icon: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/>
    </svg>`,
  },
  {
    key: 'docs',
    label: '文档',
    icon: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/>
    </svg>`,
  },
  {
    key: 'wiki',
    label: 'Wiki',
    icon: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="12" cy="12" r="10"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/><path d="M2 12h20"/>
    </svg>`,
  },
  {
    key: 'settings',
    label: '配置',
    icon: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9c.26.604.852.997 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>
    </svg>`,
  },
  {
    key: 'admin',
    label: '管理',
    icon: `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
      <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/>
    </svg>`,
  },
]

// ── Props ──
interface Props {
  historyList: string[]
  activeItem: string
  activePage?: string
  userName?: string
  userEmail?: string
}

const props = withDefaults(defineProps<Props>(), {
  activePage: 'new-chat',
  userName: 'Admin',
  userEmail: 'admin@solvify.ai',
})

const userInitial = computed(() => props.userName.charAt(0).toUpperCase())

// ── Emits ──
defineEmits<{
  (e: 'selectItem', item: string): void
  (e: 'newChat'): void
  (e: 'navigate', page: string): void
}>()
</script>
