<template>
  <section class="capability-panel">
    <div class="flex items-center justify-between gap-3 mb-3">
      <h4 class="text-sm font-semibold text-slate-800">模型能力</h4>
      <span class="text-[11px] text-slate-400">单位：token</span>
    </div>
    <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
      <label class="field-label">总上下文窗口
        <input :value="window || ''" type="number" min="1024" max="2000000" placeholder="待确认" class="model-input" @input="setWindow(numberValue($event))" />
      </label>
      <label class="field-label">最大输出
        <input :value="config.max_output_tokens || ''" type="number" min="1" placeholder="服务商上限" class="model-input" @input="set('max_output_tokens', numberValue($event))" />
      </label>
      <label class="field-label">本次输出额度
        <input :value="config.max_completion_tokens || ''" type="number" min="1" placeholder="按任务需要" class="model-input" @input="set('max_completion_tokens', numberValue($event))" />
      </label>
    </div>
    <div class="grid grid-cols-1 sm:grid-cols-3 gap-3 mt-3">
      <label class="field-label">工具调用 <span class="text-emerald-600 text-[10px]">必需</span>
        <AppSelect :model-value="booleanValue(config.tool_calling)" class="capability-select" popper-class="model-capability-menu" @change="changeToolCalling(String($event))">
          <el-option value="" label="待确认" />
          <el-option value="true" label="已确认支持" />
          <el-option v-if="config.tool_calling === false" value="false" disabled label="不支持 · 请更换模型" />
        </AppSelect>
      </label>
      <label class="field-label">输出参数
        <AppSelect :model-value="String(config.output_token_field || '')" placeholder="选择输出参数" class="capability-select" popper-class="model-capability-menu" @change="set('output_token_field', String($event))">
          <el-option value="max_tokens" label="max_tokens" /><el-option value="max_completion_tokens" label="max_completion_tokens" />
        </AppSelect>
      </label>
      <label class="field-label">思考能力
        <AppSelect :model-value="String(config.reasoning_capability || '')" placeholder="待确认" class="capability-select" popper-class="model-capability-menu" @change="changeReasoning(String($event))">
          <el-option value="none" label="无思考模式" /><el-option value="optional" label="可切换" /><el-option value="always" label="始终思考" />
        </AppSelect>
      </label>
      <label v-if="config.reasoning_capability === 'optional'" class="field-label">思考设置
        <AppSelect :model-value="String(config.reasoning_mode || 'default')" class="capability-select" popper-class="model-capability-menu" @change="set('reasoning_mode', String($event))">
          <el-option value="default" label="服务商默认" /><el-option value="off" label="关闭" /><el-option value="on" label="开启" />
        </AppSelect>
      </label>
      <label v-if="config.reasoning_capability && config.reasoning_capability !== 'none'" class="field-label">思考时支持工具
        <AppSelect :model-value="booleanValue(config.tools_with_reasoning)" placeholder="待确认" class="capability-select" popper-class="model-capability-menu" @change="set('tools_with_reasoning', String($event) === 'true')">
          <el-option value="true" label="支持" /><el-option value="false" label="不支持 · 需关闭思考" />
        </AppSelect>
      </label>
    </div>
    <p v-if="admissionError" role="alert" class="text-xs text-red-600 mt-3">{{ admissionError }}</p>
    <div class="flex items-center gap-2 text-[11px] text-slate-400 mt-3">
      <span>{{ originLabel(origin?.source) }}</span>
      <span v-if="origin?.checked_at">{{ origin.checked_at.slice(0, 10) }}</span>
    </div>
    <details class="mt-3 border-t border-slate-200 pt-3">
      <summary class="text-xs text-slate-500 cursor-pointer select-none">高级设置与能力来源</summary>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-3 mt-3">
        <label class="field-label">独立输入上限（可选）
          <input :value="config.max_input_tokens || ''" type="number" min="1" placeholder="无额外限制时留空" class="model-input" @input="set('max_input_tokens', numberValue($event))" />
        </label>
        <label v-if="config.reasoning_capability && config.reasoning_capability !== 'none'" class="field-label">思考协议
          <AppSelect :model-value="String(config.reasoning_protocol || '')" class="capability-select" popper-class="model-capability-menu" @change="changeProtocol(String($event))">
            <el-option value="" label="服务商默认" /><el-option value="thinking" label="thinking.type" /><el-option value="enable_thinking" label="enable_thinking" /><el-option value="reasoning_effort" label="reasoning_effort" />
          </AppSelect>
        </label>
        <label v-if="config.reasoning_protocol === 'reasoning_effort'" class="field-label">思考强度
          <AppSelect :model-value="String(config.reasoning_effort || '')" class="capability-select" popper-class="model-capability-menu" @change="set('reasoning_effort', String($event))">
            <el-option value="" label="服务商默认" /><el-option value="low" label="low" /><el-option value="medium" label="medium" /><el-option value="high" label="high" />
          </AppSelect>
        </label>
        <label class="field-label">温度（可选）
          <input :value="config.temperature ?? ''" type="number" min="0" max="2" step="0.1" placeholder="服务商默认" class="model-input" @input="setOptionalNumber('temperature', $event)" />
        </label>
        <label class="field-label">超时 / 秒（可选）
          <input :value="config.timeout ?? ''" type="number" min="1" max="300" placeholder="沿用请求时限" class="model-input" @input="setOptionalNumber('timeout', $event)" />
        </label>
      </div>
      <p v-if="origin?.url" class="text-xs text-slate-400 break-all mt-3">资料：{{ origin.url }}</p>
      <p class="text-[11px] text-slate-400 mt-3 leading-relaxed">独立输入上限留空时共享总窗口。补齐能力后，保存时自动验证工具调用；验证通过才会保存。</p>
    </details>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import AppSelect from '@/components/ui/AppSelect.vue'
import { modelDraftError } from '@/lib/model-capabilities.mjs'
const props = defineProps<{ window: number; config: Record<string, unknown> }>()
const emit = defineEmits<{ 'update:window': [number]; 'update:config': [Record<string, unknown>] }>()
function originLabel(source?: string) { return ({ manual: '手动确认', provider_metadata: '当前服务商元数据', official_catalog: '已核实官方资料' } as Record<string, string>)[source || ''] || '手动填写 / 待确认' }
const admissionError = computed(() => modelDraftError(props.window, props.config))
const origin = computed(() => props.config.capability_origin as { source?: string; url?: string; checked_at?: string } | undefined)
const limits = [
  { key: 'max_output_tokens', label: '模型最大生成额度（token）' },
  { key: 'max_completion_tokens', label: '本配置实际生成额度（token）' },
  { key: 'max_input_tokens', label: '独立输入上限（可选，仅服务商额外限制时填写）' },
]
function changeToolCalling(value: string) {
  const next: Record<string, unknown> = { ...props.config, capability_origin: { source: 'manual' } }
  if (value === '') delete next.tool_calling
  else next.tool_calling = value === 'true'
  emit('update:config', next)
}
function textValue(event: Event) { return (event.target as HTMLInputElement).value }
function numberValue(event: Event) { return Number(textValue(event)) }
function booleanValue(value: unknown) { return typeof value === 'boolean' ? String(value) : '' }
function setWindow(value: number) { emit('update:window', value); emit('update:config', { ...props.config, capability_origin: { source: 'manual' } }) }
function set(key: string, value: unknown) { const next: Record<string, unknown> = { ...props.config, [key]: value, capability_origin: { source: 'manual' } }; if (limits.some(field => field.key === key) && value === 0) delete next[key]; emit('update:config', next) }
function setOptionalNumber(key: string, event: Event) {
  const next: Record<string, unknown> = { ...props.config, capability_origin: { source: 'manual' } }
  if (textValue(event) === '') delete next[key]
  else next[key] = numberValue(event)
  emit('update:config', next)
}
function changeReasoning(value: string) {
  const next: Record<string, unknown> = { ...props.config, capability_origin: { source: 'manual' }, reasoning_capability: value, reasoning_mode: 'default' }
  delete next.reasoning_protocol; delete next.reasoning_effort; delete next.tools_with_reasoning
  emit('update:config', next)
}
function changeProtocol(value: string) {
  const next: Record<string, unknown> = { ...props.config, capability_origin: { source: 'manual' }, reasoning_protocol: value }
  delete next.reasoning_effort
  if (value === 'reasoning_effort') next.reasoning_mode = 'default'
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
