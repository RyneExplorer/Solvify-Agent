<script setup lang="ts">
import { computed, provide } from 'vue'
import type { ChatSpan } from '@/types/chat'
import TraceSpanNode from './TraceSpanNode.vue'

const props = defineProps<{
  root: ChatSpan
}>()

provide(
  'trace_root_duration_ms',
  computed(() => props.root?.duration_ms ?? 1),
  true,
)
</script>

<template>
  <TraceSpanNode v-if="root" :span="root" :depth="0" />
</template>
