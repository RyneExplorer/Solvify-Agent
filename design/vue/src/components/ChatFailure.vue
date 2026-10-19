<template>
  <div role="alert" class="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-sm leading-relaxed text-red-700">
    <p class="font-medium">{{ title }}</p>
    <p v-if="detail && detail !== title" class="mt-1 whitespace-pre-wrap break-words text-xs text-red-600">{{ detail }}</p>
  </div>
</template>

<script setup lang="ts">
defineProps<{ title: string; detail?: string }>()
</script>
