<template>
  <select
    :value="modelValue"
    @change="$emit('update:modelValue', ($event.target as HTMLSelectElement).value)"
    class="app-select"
    v-bind="$attrs"
  >
    <slot />
  </select>
</template>

<script setup lang="ts">
defineProps<{
  modelValue: string
}>()

defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()
</script>

<style scoped>
.app-select {
  @apply text-sm rounded-xl border border-slate-200 bg-slate-50 text-slate-900 px-4 py-2.5 pr-10 outline-none transition-colors cursor-pointer appearance-none;
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 12 12'%3E%3Cpath fill='%2394a3b8' d='M6 8.825L1.175 4 2.238 2.938 6 6.7 9.763 2.937 10.825 4z'/%3E%3C/svg%3E");
  background-repeat: no-repeat;
  background-position: right 12px center;
}
.app-select:focus {
  @apply border-slate-900;
}
</style>
