<template>
  <el-select
    :model-value="modelValue"
    @update:model-value="$emit('update:modelValue', $event)"
    :placeholder="placeholder"
    class="app-select-el"
    v-bind="$attrs"
  >
    <slot />
  </el-select>
</template>

<script setup lang="ts">
defineProps<{
  modelValue: string | number
  placeholder?: string
}>()

defineEmits<{
  (e: 'update:modelValue', value: string | number): void
}>()
</script>

<style>
.app-select-el {
  width: 100%;
}
.app-select-el .el-select__wrapper {
  border-radius: 0.75rem !important;
  box-shadow: 0 0 0 1px #e2e8f0 inset !important;
  background-color: #f8fafc !important;
  min-height: 41px;
}
.app-select-el .el-select__placeholder,
.app-select-el .el-select__selected-item {
  color: #0f172a !important;
  font-size: 0.875rem !important;
}
.app-select-el .el-select__wrapper.is-focused {
  box-shadow: 0 0 0 1px #0f172a inset !important;
}
.app-select-el .el-select-dropdown__item {
  color: #334155 !important;
  font-size: 0.875rem !important;
  border-radius: 0.5rem;
  margin: 0 6px;
  padding: 0 10px;
}
.app-select-el .el-select-dropdown__item.selected,
.app-select-el .el-select-dropdown__item.hover:not(.selected) {
  color: #0f172a !important;
  background-color: #f1f5f9 !important;
}
.app-select-el .el-select-dropdown__list {
  padding: 6px 0 !important;
}
</style>
