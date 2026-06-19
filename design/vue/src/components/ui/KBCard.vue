<template>
  <AppCard class="cursor-pointer">
    <div class="flex justify-between items-start mb-3.5">
      <div>
        <h3 class="text-base font-semibold text-slate-900 m-0">{{ kb.name }}</h3>
        <div class="flex items-center gap-2 mt-1">
          <span class="text-xs text-slate-400">{{ kb.category }}</span>
          <span v-if="sourceLabel"
            class="text-[11px] font-medium inline-block rounded-full px-2 py-0.5"
            :style="{ color: sourceLabel.color, backgroundColor: sourceLabel.color + '15' }"
          >{{ sourceLabel.text }}</span>
        </div>
      </div>
      <AppBadge :variant="kb.status === 'ready' ? 'success' : 'warning'">
        {{ statusText }}
      </AppBadge>
    </div>

    <div class="flex gap-6 text-[13px] text-slate-400 mb-3.5">
      <span>{{ kb.docs }} 篇文档</span>
      <span>{{ kb.size }}</span>
    </div>

    <div class="flex justify-between items-center">
      <span class="text-xs text-slate-400">{{ timeLabel }} {{ kb.updated }}</span>
      <div class="flex gap-1">
        <AppButton v-if="kb.source === 'self'" variant="ghost" size="sm">编辑</AppButton>
        <AppButton v-if="kb.source === 'self'" variant="ghost" size="sm">文档</AppButton>
        <AppButton v-if="kb.source !== 'self'" variant="ghost" size="sm">查看</AppButton>
        <AppButton v-if="kb.source !== 'self' && kb.source !== 'web_search'" variant="ghost" size="sm">立即同步</AppButton>
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

interface KB {
  id: number
  name: string
  category: string
  docs: number
  size: string
  status: string
  updated: string
  source: string
}

interface SourceLabel {
  text: string
  color: string
}

const props = defineProps<{
  kb: KB
  sourceLabel?: SourceLabel | null
}>()

const statusText = computed(() => {
  if (props.kb.status === 'ready') return props.kb.source === 'self' ? '已就绪' : '已就绪'
  return props.kb.source === 'self' ? '处理中' : '同步中'
})

const timeLabel = computed(() => props.kb.source === 'self' ? '更新于' : '最后同步')
</script>
