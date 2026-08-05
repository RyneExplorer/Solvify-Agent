<script setup lang="ts">
const props = withDefaults(
  defineProps<{
    title: string
    value: string | number | undefined | null
    hint?: string
    accent?: 'blue' | 'emerald' | 'amber' | 'rose' | 'slate'
  }>(),
  { accent: 'slate' },
)

const accentMap = {
  blue: 'from-sky-50 to-white border-sky-100',
  emerald: 'from-emerald-50 to-white border-emerald-100',
  amber: 'from-amber-50 to-white border-amber-100',
  rose: 'from-rose-50 to-white border-rose-100',
  slate: 'from-slate-50 to-white border-slate-200',
} satisfies Record<string, string>

const valueAccent = {
  blue: 'text-sky-700',
  emerald: 'text-emerald-700',
  amber: 'text-amber-700',
  rose: 'text-rose-700',
  slate: 'text-slate-800',
} satisfies Record<string, string>
</script>

<template>
  <div :class="['rounded-2xl border p-4 bg-gradient-to-b shadow-sm', accentMap[accent]]">
    <div class="text-[11px] font-medium uppercase tracking-wide text-slate-400">{{ title }}</div>
    <div :class="['mt-2 text-2xl font-semibold tabular-nums', valueAccent[accent]]">{{ value ?? '-' }}</div>
    <div v-if="hint" class="mt-1 text-[11px] text-slate-400 leading-snug">{{ hint }}</div>
  </div>
</template>
