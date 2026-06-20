<template>
  <img
    src="/solvify.png"
    alt="Solvify"
    :class="sizeClass"
    class="object-contain rounded-lg"
  />
</template>

<script setup lang="ts">
const props = defineProps<{
  size?: 'sm' | 'md' | 'lg'
}>()

const sizeClass = {
  sm: 'w-7 h-7',
  md: 'w-12 h-12',
  lg: 'w-16 h-16',
}[props.size ?? 'md']
</script>
