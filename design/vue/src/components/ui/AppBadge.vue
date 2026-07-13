<template>
  <span :class="badgeClasses">
    <slot />
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  variant?: 'success' | 'warning' | 'error' | 'neutral' | 'blue'
}>(), {
  variant: 'neutral',
})

const badgeClasses = computed(() => {
  const base = 'inline-block rounded-full px-2.5 py-0.5 text-xs font-medium'

  const variants: Record<string, string> = {
    success: 'bg-green-50 text-green-600',
    warning: 'bg-amber-50 text-amber-600',
    error: 'bg-red-50 text-red-600',
    neutral: 'bg-slate-100 text-slate-600',
    blue: 'bg-blue-50 text-blue-600',
  }

  return `${base} ${variants[props.variant]}`
})
</script>
