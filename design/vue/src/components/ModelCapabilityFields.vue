<template>
  <section class="capability-panel">
    <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
      <label class="field-label">上下文窗口（token）
        <input :value="window || ''" type="number" min="1024" max="2000000" placeholder="获取能力或按部署资料填写" class="model-input" @input="setWindow(numberValue($event))" />
      </label>
      <div class="field-label">
        <div class="flex items-center justify-between"><span>思考模式</span><el-switch :model-value="thinking.enabled" :disabled="thinking.disabled" aria-label="思考模式" @change="set('reasoning_mode', $event ? 'on' : 'off')" /></div>
        <p class="text-[11px] text-slate-500 mt-2 font-normal">{{ thinking.label }}</p>
        <button v-if="!thinking.disabled && config.reasoning_mode && config.reasoning_mode !== 'default'" type="button" class="text-[11px] text-indigo-600 mt-1" @click="set('reasoning_mode', 'default')">恢复服务商默认</button>
      </div>
    </div>
    <p class="text-[11px] text-slate-500 mt-3">保存前验证当前思考设置的工具调用，不支持工具的模型不能使用。</p>
    <p v-if="config.tool_calling === false" role="alert" class="text-xs text-red-600 mt-2">该模型不支持工具调用，请更换模型。</p>
    <span class="block text-[11px] text-slate-400 mt-2">{{ originLabel(origin?.source) }}</span>
    <details v-if="adminMode" class="mt-3 border-t border-slate-200 pt-3">
      <summary class="text-xs text-slate-500 cursor-pointer select-none">管理员部署资料（获取不到时补充）</summary>
      <p class="text-[11px] text-slate-400 mt-2">仅适用于当前地址和模型，用户配置复用资料；保存仍需实测工具协议。</p>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3 mt-3">
        <label class="field-label">模型最大输出
          <input :value="config.max_output_tokens || ''" type="number" min="1" placeholder="按服务商资料填写" class="model-input" @input="set('max_output_tokens', numberValue($event))" />
        </label>
        <label class="field-label">独立输入上限（可选）
          <input :value="config.max_input_tokens || ''" type="number" min="1" placeholder="无额外限制时留空" class="model-input" @input="set('max_input_tokens', numberValue($event))" />
        </label>
        <label class="field-label">思考能力
          <AppSelect :model-value="String(config.reasoning_capability || 'unknown')" class="capability-select" popper-class="model-capability-menu" @change="changeCapability(String($event))">
            <el-option value="unknown" label="未知" /><el-option value="none" label="不支持" /><el-option value="optional" label="可以切换" /><el-option value="always" label="始终开启" />
          </AppSelect>
        </label>
        <label v-if="config.reasoning_capability === 'optional'" class="field-label">思考开关协议
          <AppSelect :model-value="String(config.reasoning_protocol || '')" class="capability-select" popper-class="model-capability-menu" @change="set('reasoning_protocol', String($event))">
            <el-option value="" label="尚未识别" /><el-option value="thinking" label="thinking.type" /><el-option value="enable_thinking" label="enable_thinking" />
          </AppSelect>
        </label>
        <label class="field-label">输出参数协议
          <AppSelect :model-value="String(config.output_token_field || 'max_tokens')" class="capability-select" popper-class="model-capability-menu" @change="set('output_token_field', String($event))">
            <el-option value="max_tokens" label="max_tokens" /><el-option value="max_completion_tokens" label="max_completion_tokens" />
          </AppSelect>
        </label>
      </div>
      <p v-if="origin?.url" class="text-xs text-slate-400 break-all mt-3">资料：{{ origin.url }}</p>
    </details>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import { thinkingState } from '@/lib/model-capabilities.mjs'
const props = defineProps<{ window: number; config: Record<string, unknown>; adminMode?: boolean }>()
const emit = defineEmits<{ 'update:window': [number]; 'update:config': [Record<string, unknown>] }>()
const origin = computed(() => props.config.capability_origin as { source?: string; url?: string } | undefined)
const thinking = computed(() => thinkingState(props.config))
function originLabel(source?: string) { return ({ manual: '手动确认的部署资料', provider_metadata: '来自当前服务商', official_catalog: '来自已核实官方资料' } as Record<string, string>)[source || ''] || '待获取或由管理员补充能力资料' }
function numberValue(event: Event) { return Number((event.target as HTMLInputElement).value) }
function setWindow(value: number) { emit('update:window', value) }
function set(key: string, value: unknown) {
  const next: Record<string, unknown> = { ...props.config, [key]: value }
  if (key !== 'reasoning_mode') next.capability_origin = { source: 'manual' }
  if (value === 0 || value === '') delete next[key]
  emit('update:config', next)
}
function changeCapability(value: string) {
  const next: Record<string, unknown> = { ...props.config, reasoning_capability: value, reasoning_mode: 'default', capability_origin: { source: 'manual' } }
  delete next.reasoning_protocol
  emit('update:config', next)
}
</script>

<style scoped>
.capability-panel { padding: 16px; border: 1px solid #e2e8f0; border-radius: 14px; background: #f8fafc; }
.field-label { display: block; font-size: 12px; color: #475569; font-weight: 500; }
.model-input { display: block; width: 100%; height: 40px; margin-top: 6px; padding: 8px 12px; border: 1px solid #e2e8f0; border-radius: 10px; background: #fff; font-size: 13px; color: #0f172a; outline: none; transition: border-color .15s; }
.model-input:focus { border-color: #94a3b8; }
.capability-select { display: block; margin-top: 6px; }
.capability-select :deep(.el-select__wrapper) {
  min-height: 40px;
  padding: 0 10px 0 12px;
  border-radius: 10px !important;
  background-color: #fff !important;
  border-color: #e2e8f0 !important;
  box-shadow: none !important;
}
.capability-select :deep(.el-select__wrapper:hover),
.capability-select :deep(.el-select__wrapper.is-focused),
.capability-select :deep(.el-select__wrapper.is-hovering) { border-color: #94a3b8 !important; }
.capability-select :deep(.el-select__placeholder),
.capability-select :deep(.el-select__selected-item) { font-size: 13px !important; font-weight: 400; }
.capability-select :deep(.el-select__caret) { color: #64748b !important; font-size: 12px; }
.capability-select :deep(.el-select__suffix) { width: 22px; height: 22px; justify-content: center; border-radius: 6px; background: #f1f5f9; }
</style>

<style>
/* 下拉菜单通过Teleport挂载，样式限定于模型能力区。 */
.model-capability-menu.el-select__popper {
  min-width: 220px;
  border: 1px solid #e2e8f0;
  border-radius: 12px;
  background: #fff;
  box-shadow: 0 8px 24px rgb(15 23 42 / .08);
  overflow: hidden;
}
.model-capability-menu .el-popper__arrow { display: none; }
.model-capability-menu .el-select-dropdown__list { padding: 6px; }
.model-capability-menu .el-select-dropdown__item {
  height: 36px;
  line-height: 36px;
  margin: 2px 0;
  padding: 0 36px 0 10px;
  border-radius: 7px;
  color: #475569;
  font-size: 13px;
  font-weight: 400;
}
.model-capability-menu .el-select-dropdown__item.is-hovering,
.model-capability-menu .el-select-dropdown__item:hover { background: #f8fafc; color: #0f172a; }
.model-capability-menu .el-select-dropdown__item.is-selected { background: #eef2ff; color: #4338ca; font-weight: 500; }
.model-capability-menu .el-select-dropdown__item.is-selected::after {
  content: '';
  position: absolute;
  right: 13px;
  top: 12px;
  width: 6px;
  height: 10px;
  border-right: 1.5px solid currentColor;
  border-bottom: 1.5px solid currentColor;
  transform: rotate(45deg);
}
.model-capability-menu .el-select-dropdown__item.is-disabled { color: #94a3b8; background: transparent; cursor: not-allowed; }
</style>
