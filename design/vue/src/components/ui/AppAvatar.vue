<template>
  <div
    class="w-8 h-8 rounded-full bg-slate-900 text-white flex items-center justify-center text-sm font-medium shrink-0"
    v-bind="$attrs"
  >
    {{ initial }}
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ name: string }>()
const initial = computed(() => props.name?.charAt(0).toUpperCase() || '?')
</script>
