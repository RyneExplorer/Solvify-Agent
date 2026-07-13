<template>
  <el-select
    :model-value="modelValue"
    @update:model-value="$emit('update:modelValue', $event)"
    @change="$emit('change', $event)"
    :placeholder="placeholder"
    popper-class="app-select-dropdown"
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
  (e: 'change', value: string | number): void
}>()
</script>

<style>
.app-select-el {
  width: 100%;
  --el-select-border-color-hover: #cbd5e1;
  --el-select-input-focus-border-color: #0f172a;
  --el-font-size-base: 14px;
}

/* trigger */
.app-select-el .el-select__wrapper {
  border-radius: 12px !important;
  background-color: #f8fafc !important;
  border: 1px solid #e2e8f0 !important;
  box-shadow: none !important;
  min-height: 42px;
  padding: 0 12px;
  transition: border-color 0.15s ease;
}
.app-select-el .el-select__wrapper:hover {
  border-color: #cbd5e1 !important;
}
.app-select-el .el-select__wrapper.is-focused {
  border-color: #0f172a !important;
}
.app-select-el .el-select__wrapper.is-hovering {
  border-color: #cbd5e1 !important;
}

.app-select-el .el-select__placeholder,
.app-select-el .el-select__selected-item {
  color: #94a3b8 !important;
  font-size: 14px !important;
}
.app-select-el .el-select__selected-item {
  color: #0f172a !important;
}

.app-select-el .el-select__caret {
  color: #94a3b8 !important;
  font-size: 12px;
}

/* dropdown */
.app-select-dropdown.el-select-dropdown {
  border: 1px solid #e2e8f0 !important;
  border-radius: 12px !important;
  box-shadow: 0 20px 25px -5px rgb(0 0 0 / 0.08), 0 8px 10px -6px rgb(0 0 0 / 0.05) !important;
  padding: 4px 0;
  margin-top: 6px;
}

.app-select-dropdown .el-select-dropdown__list {
  padding: 4px 0 !important;
}

.app-select-dropdown .el-select-dropdown__item {
  color: #334155 !important;
  font-size: 14px !important;
  height: 36px !important;
  line-height: 36px !important;
  padding: 0 16px !important;
  margin: 0 !important;
  border-radius: 0 !important;
  position: relative;
}
.app-select-dropdown .el-select-dropdown__item:hover,
.app-select-dropdown .el-select-dropdown__item.hover {
  background-color: #f8fafc !important;
  color: #0f172a !important;
}
.app-select-dropdown .el-select-dropdown__item.selected {
  color: #059669 !important;
  background-color: #ecfdf5 !important;
  font-weight: 500 !important;
}
.app-select-dropdown .el-select-dropdown__item.selected::after {
  content: '';
  position: absolute;
  right: 16px;
  top: 50%;
  width: 14px;
  height: 14px;
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' fill='none' viewBox='0 0 24 24' stroke='%23059669' stroke-width='2.5'%3E%3Cpath stroke-linecap='round' stroke-linejoin='round' d='M5 13l4 4L19 7'/%3E%3C/svg%3E");
  background-size: contain;
  background-repeat: no-repeat;
  transform: translateY(-50%);
}

/* popper arrow */
.app-select-dropdown .el-popper__arrow {
  display: none;
}
</style>
