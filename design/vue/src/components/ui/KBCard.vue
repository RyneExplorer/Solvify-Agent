<template>
  <AppCard>
    <div class="flex justify-between items-start gap-3 mb-3.5">
      <div class="min-w-0">
        <h3 class="text-base font-semibold text-slate-900 m-0 truncate">{{ kb.name }}</h3>
        <div class="flex items-center gap-2 mt-1">
          <span class="text-xs text-slate-400">{{ kb.category || '未分类' }}</span>
          <span
            v-if="sourceLabel"
            class="text-[11px] font-medium inline-block rounded-full px-2 py-0.5"
            :style="{ color: sourceLabel.color, backgroundColor: sourceLabel.color + '15' }"
          >
            {{ sourceLabel.text }}
          </span>
        </div>
      </div>
      <AppBadge :variant="statusVariant">
        {{ statusText }}
      </AppBadge>
    </div>

    <p v-if="kb.description" class="text-[13px] text-slate-500 leading-5 mb-3 line-clamp-2">
      {{ kb.description }}
    </p>
    <p v-else class="text-[13px] text-slate-400 leading-5 mb-3">暂无描述</p>

    <div class="flex gap-6 text-[13px] text-slate-400 mb-3.5">
      <span>{{ kb.document_count }} 篇文档</span>
      <span>{{ sizeText }}</span>
    </div>

    <div class="flex justify-between items-center">
      <span class="text-xs text-slate-400">{{ timeLabel }} {{ updatedText }}</span>
      <div class="flex gap-1">
        <AppButton v-if="isLocal" variant="ghost" size="sm" @click="$emit('edit', kb)">编辑</AppButton>
        <AppButton v-if="isLocal" variant="ghost" size="sm" @click="$emit('documents', kb)">文档</AppButton>
        <AppButton v-if="isLocal" variant="ghost" size="sm" class="!text-red-600 hover:!bg-red-50" @click="$emit('delete', kb)">删除</AppButton>
        <AppButton v-if="!isLocal" variant="ghost" size="sm" @click="$emit('view', kb)">查看</AppButton>
        <AppButton v-if="isDingTalk" variant="ghost" size="sm" @click="$emit('sync', kb)">刷新目录</AppButton>
        <AppButton v-if="isDingTalk" variant="ghost" size="sm" class="!text-red-600 hover:!bg-red-50" @click="$emit('delete', kb)">删除</AppButton>
      </div>
    </div>

    <slot />
  </AppCard>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import AppCard from './AppCard.vue'
import AppBadge from './AppBadge.vue'
import AppButton from './AppButton.vue'
import type { KnowledgeBase } from '@/types/knowledge'

interface SourceLabel {
  text: string
  color: string
}

const props = defineProps<{
  kb: KnowledgeBase
  sourceLabel?: SourceLabel | null
}>()

defineEmits<{
  (e: 'edit', kb: KnowledgeBase): void
  (e: 'documents', kb: KnowledgeBase): void
  (e: 'delete', kb: KnowledgeBase): void
  (e: 'view', kb: KnowledgeBase): void
  (e: 'sync', kb: KnowledgeBase): void
}>()

const isLocal = computed(() => props.kb.source_type === 'local')

const isDingTalk = computed(() => props.kb.source_type === 'sync' && props.kb.source_platform === 'dingtalk')

const statusText = computed(() => {
  if (props.kb.status === 1) return '已就绪'
  if (props.kb.status === 2) return '已删除'
  return '处理中'
})

const statusVariant = computed(() => {
  if (props.kb.status === 1) return 'success'
  if (props.kb.status === 2) return 'error'
  return 'warning'
})

const sizeText = computed(() => formatBytes(props.kb.storage_bytes))

const timeLabel = computed(() => isLocal.value ? '更新于' : '最后同步')

const updatedText = computed(() => formatTime(props.kb.updated_at))

// 格式化存储大小
function formatBytes(bytes: number) {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index += 1
  }
  return `${value.toFixed(value >= 10 || index === 0 ? 0 : 1)} ${units[index]}`
}

// 格式化更新时间
function formatTime(value: string) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })
}
</script>
