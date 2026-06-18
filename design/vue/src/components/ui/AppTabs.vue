<template>
  <div class="flex border-b border-slate-200">
    <button
      v-for="tab in tabs"
      :key="tab.key"
      @click="$emit('update:modelValue', tab.key)"
      :class="[
        'px-4 py-2.5 text-sm border-b-2 transition-colors cursor-pointer',
        modelValue === tab.key
          ? 'text-slate-900 font-medium border-slate-900'
          : 'text-slate-400 border-transparent hover:text-slate-600'
      ]"
    >
      {{ tab.label }}
    </button>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  tabs: { key: string; label: string }[]
  modelValue: string
}>()

defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()
</script>
