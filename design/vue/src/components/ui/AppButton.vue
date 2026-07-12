<template>
  <button
    :class="buttonClasses"
    :disabled="disabled || loading"
    v-bind="$attrs"
  >
    <svg v-if="loading" class="animate-spin h-4 w-4" viewBox="0 0 24 24" fill="none">
      <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
      <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
    </svg>
    <slot />
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost' | 'icon' | 'outline'
  size?: 'sm' | 'md'
  disabled?: boolean
  loading?: boolean
}>(), {
  variant: 'primary',
  size: 'md',
  disabled: false,
  loading: false,
})

const buttonClasses = computed(() => {
  const base = 'inline-flex items-center gap-1.5 rounded-xl transition-all duration-150 font-medium cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed'

  const variants: Record<string, string> = {
    primary: 'bg-slate-900 text-white border-none hover:bg-slate-800',
    secondary: 'bg-white text-slate-900 border border-slate-200 hover:bg-slate-50',
    danger: 'bg-red-600 text-white border-none hover:bg-red-700',
    ghost: 'bg-transparent text-slate-600 border-none hover:bg-slate-100',
    icon: 'bg-transparent text-slate-400 border-none hover:bg-slate-100 p-2 rounded-lg',
    outline: 'bg-transparent text-slate-700 border border-slate-300 hover:bg-slate-50',
  }

  const sizes: Record<string, string> = {
    sm: 'text-xs px-3 py-1.5',
    md: 'text-sm px-5 py-2.5',
  }

  return `${base} ${variants[props.variant]} ${sizes[props.size]}`
})
</script>
