<template>
  <AppCard class="flex items-center gap-4">
    <div class="w-12 h-12 rounded-xl bg-slate-100 flex items-center justify-center text-xl">
      {{ icon }}
    </div>
    <div>
      <div class="text-[28px] font-bold text-slate-900 leading-tight" style="font-family: 'Space Grotesk', sans-serif;">
        {{ value }}
      </div>
      <div class="text-[13px] text-slate-400 mt-1">{{ label }}</div>
    </div>
  </AppCard>
</template>

<script setup lang="ts">
import AppCard from './AppCard.vue'

defineProps<{
  label: string
  value: string
  icon: string
}>()
</script>
