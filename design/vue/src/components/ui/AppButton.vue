<template>
  <button
    :class="buttonClasses"
    :disabled="disabled"
    v-bind="$attrs"
  >
    <slot />
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost' | 'icon'
  size?: 'sm' | 'md'
  disabled?: boolean
}>(), {
  variant: 'primary',
  size: 'md',
  disabled: false,
})

const buttonClasses = computed(() => {
  const base = 'inline-flex items-center gap-1.5 rounded-xl transition-all duration-150 font-medium cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed'

  const variants: Record<string, string> = {
    primary: 'bg-slate-900 text-white border-none hover:bg-slate-800',
    secondary: 'bg-white text-slate-900 border border-slate-200 hover:bg-slate-50',
    danger: 'bg-red-600 text-white border-none hover:bg-red-700',
    ghost: 'bg-transparent text-slate-600 border-none hover:bg-slate-100',
    icon: 'bg-transparent text-slate-400 border-none hover:bg-slate-100 p-2 rounded-lg',
  }

  const sizes: Record<string, string> = {
    sm: 'text-xs px-3 py-1.5',
    md: 'text-sm px-5 py-2.5',
  }

  return `${base} ${variants[props.variant]} ${sizes[props.size]}`
})
</script>
