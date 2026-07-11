<template>
  <div class="relative" ref="wrapperRef">
    <button
      type="button"
      class="w-full px-3 py-2.5 text-sm rounded-xl border border-slate-200 bg-slate-50 text-slate-900 flex items-center justify-between transition-all hover:border-slate-300 focus:outline-none"
      :class="{ 'border-slate-900': open }"
      @click="toggle"
    >
      <span class="truncate" :class="modelLabel ? 'text-slate-900' : 'text-slate-400'">
        {{ modelLabel || placeholder }}
      </span>
      <svg class="w-3.5 h-3.5 text-slate-400 shrink-0 transition-transform" :class="{ 'rotate-180': open }" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/>
      </svg>
    </button>

    <Teleport to="body">
      <Transition name="dropdown-fade">
        <div
          v-if="open"
          ref="panelRef"
          class="fixed bg-white border border-slate-200 rounded-xl shadow-xl z-[100] overflow-hidden"
          :style="panelStyle"
        >
          <slot />
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, nextTick, watch } from 'vue'

const props = withDefaults(defineProps<{
  modelLabel?: string
  placeholder?: string
  modelValue?: boolean
}>(), {
  modelLabel: '',
  placeholder: '请选择',
  modelValue: undefined,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
}>()

const wrapperRef = ref<HTMLDivElement>()
const panelRef = ref<HTMLDivElement>()
const open = ref(false)
const panelStyle = ref({ top: '0px', left: '0px', width: '0px' })

function updatePanelPosition() {
  if (!wrapperRef.value) return
  const rect = wrapperRef.value.getBoundingClientRect()
  panelStyle.value = {
    top: rect.bottom + 6 + 'px',
    left: rect.left + 'px',
    width: rect.width + 'px',
  }
}

function toggle() {
  open.value = !open.value
  if (open.value) {
    updatePanelPosition()
  }
  emit('update:modelValue', open.value)
}

function close() {
  open.value = false
  emit('update:modelValue', false)
}

function handleClickOutside(e: MouseEvent) {
  if (!open.value) return
  const target = e.target as Node
  if (wrapperRef.value?.contains(target)) return
  if (panelRef.value?.contains(target)) return
  close()
}

function handleKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && open.value) {
    close()
  }
}

watch(() => props.modelValue, (val) => {
  if (val !== undefined && val !== open.value) {
    open.value = val
    if (val) {
      nextTick(updatePanelPosition)
    }
  }
})

onMounted(() => {
  if (props.modelValue !== undefined) {
    open.value = props.modelValue
  }
  document.addEventListener('mousedown', handleClickOutside)
  document.addEventListener('keydown', handleKeydown)
  window.addEventListener('resize', updatePanelPosition)
  window.addEventListener('scroll', updatePanelPosition, true)
})

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', handleClickOutside)
  document.removeEventListener('keydown', handleKeydown)
  window.removeEventListener('resize', updatePanelPosition)
  window.removeEventListener('scroll', updatePanelPosition, true)
})

defineExpose({ open, close, toggle })
</script>

<style scoped>
.dropdown-fade-enter-active,
.dropdown-fade-leave-active {
  transition: opacity 0.15s ease, transform 0.15s ease;
}
.dropdown-fade-enter-from,
.dropdown-fade-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>
