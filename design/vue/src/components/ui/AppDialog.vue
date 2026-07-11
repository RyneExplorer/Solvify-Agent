<template>
  <Teleport to="body">
    <Transition name="app-dialog">
      <div v-if="modelValue" class="fixed inset-0 z-50 flex items-center justify-center px-4" @click.self="handleClose">
        <div class="absolute inset-0 bg-black/30 backdrop-blur-sm" />
        <div
          class="relative bg-white rounded-2xl shadow-2xl border border-slate-200 flex flex-col overflow-hidden"
          :class="dialogSizeClass"
        >
          <div v-if="$slots.header || title" class="flex items-center justify-between px-5 py-4 border-b border-slate-100">
            <slot name="header">
              <h3 class="text-base font-semibold text-slate-800">{{ title }}</h3>
            </slot>
            <button
              v-if="showClose"
              class="p-1.5 rounded-lg hover:bg-slate-100 text-slate-400 transition-colors"
              @click="handleClose"
            >
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12"/>
              </svg>
            </button>
          </div>
          <div class="flex-1 overflow-y-auto px-5 py-4">
            <slot />
          </div>
          <div v-if="$slots.footer" class="px-5 py-3 border-t border-slate-100 bg-slate-50/50">
            <slot name="footer" />
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, watch } from 'vue'

const props = withDefaults(defineProps<{
  modelValue: boolean
  title?: string
  size?: 'sm' | 'md' | 'lg'
  showClose?: boolean
  closeOnOverlay?: boolean
}>(), {
  title: '',
  size: 'md',
  showClose: true,
  closeOnOverlay: true,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'close'): void
}>()

const dialogSizeClass = computed(() => {
  const sizes: Record<string, string> = {
    sm: 'w-full max-w-sm',
    md: 'w-full max-w-md',
    lg: 'w-full max-w-2xl',
  }
  return sizes[props.size] || sizes.md
})

function handleClose() {
  if (!props.closeOnOverlay) return
  emit('update:modelValue', false)
  emit('close')
}

watch(() => props.modelValue, (val) => {
  if (val) {
    document.body.style.overflow = 'hidden'
  } else {
    document.body.style.overflow = ''
  }
})
</script>

<style scoped>
.app-dialog-enter-active,
.app-dialog-leave-active {
  transition: opacity 0.2s ease;
}
.app-dialog-enter-active .relative,
.app-dialog-leave-active .relative {
  transition: transform 0.2s ease, opacity 0.2s ease;
}
.app-dialog-enter-from,
.app-dialog-leave-to {
  opacity: 0;
}
.app-dialog-enter-from .relative,
.app-dialog-leave-to .relative {
  opacity: 0;
  transform: translateY(8px) scale(0.98);
}
</style>
