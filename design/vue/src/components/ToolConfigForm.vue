<template>
  <div class="tool-config-form">
    <h3 class="text-lg font-medium mb-4">{{ title }}</h3>

    <form @submit.prevent="handleSubmit">
      <div v-for="(field, key) in schemaProperties" :key="key" class="mb-4">
        <label class="block text-sm font-medium text-gray-700 mb-1">
          {{ field.title || key }}
          <span v-if="isRequired(key)" class="text-red-500">*</span>
        </label>

        <p v-if="field.description" class="text-xs text-gray-500 mb-1">
          {{ field.description }}
        </p>

        <!-- String 类型 -->
        <input
          v-if="field.type === 'string' && !field.enum"
          :type="field.secret ? 'password' : 'text'"
          :value="formData[key]"
          :placeholder="(field.default as string | undefined) || ''"
          :required="isRequired(key)"
          :minlength="field.minLength"
          :maxlength="field.maxLength"
          class="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
          @input="updateField(key, ($event.target as HTMLInputElement).value)"
        />

        <!-- Enum (Select) 类型 -->
        <select
          v-else-if="field.type === 'string' && field.enum"
          :value="formData[key] || field.default"
          :required="isRequired(key)"
          class="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
          @change="updateField(key, ($event.target as HTMLSelectElement).value)"
        >
          <option v-for="option in field.enum" :key="option" :value="option">
            {{ option }}
          </option>
        </select>

        <!-- Integer / Number 类型 -->
        <input
          v-else-if="field.type === 'integer' || field.type === 'number'"
          type="number"
          :value="formData[key] ?? field.default"
          :required="isRequired(key)"
          :min="field.minimum"
          :max="field.maximum"
          :step="field.type === 'integer' ? 1 : 0.1"
          class="w-full px-3 py-2 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
          @input="updateField(key, Number(($event.target as HTMLInputElement).value))"
        />

        <!-- Boolean 类型 -->
        <div v-else-if="field.type === 'boolean'" class="flex items-center">
          <input
            type="checkbox"
            :checked="(formData[key] as boolean | undefined) ?? (field.default as boolean | undefined) ?? false"
            :required="isRequired(key)"
            class="h-4 w-4 text-blue-600 focus:ring-blue-500 border-gray-300 rounded"
            @change="updateField(key, ($event.target as HTMLInputElement).checked)"
          />
          <span class="ml-2 text-sm text-gray-600">{{ field.description || '启用' }}</span>
        </div>

        <!-- 错误提示 -->
        <p v-if="errors[key]" class="text-xs text-red-500 mt-1">
          {{ errors[key] }}
        </p>
      </div>

      <div class="flex justify-end gap-2 mt-6">
        <button
          type="button"
          class="px-4 py-2 text-gray-700 bg-gray-100 rounded-md hover:bg-gray-200"
          @click="$emit('cancel')"
        >
          取消
        </button>
        <button
          type="submit"
          :disabled="!isValid"
          class="px-4 py-2 text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {{ submitText }}
        </button>
      </div>
    </form>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'

interface SchemaProperty {
  type: string
  title?: string
  description?: string
  default?: unknown
  minLength?: number
  maxLength?: number
  minimum?: number
  maximum?: number
  enum?: string[]
  secret?: boolean
}

interface JsonSchema {
  type: string
  properties: Record<string, SchemaProperty>
  required?: string[]
}

const props = defineProps<{
  schema: JsonSchema
  initialValues?: Record<string, unknown>
  title?: string
  submitText?: string
}>()

const emit = defineEmits<{
  submit: [data: Record<string, unknown>]
  cancel: []
}>()

const formData = ref<Record<string, unknown>>({})
const errors = ref<Record<string, string>>({})

// 初始化表单数据
onMounted(() => {
  const initial: Record<string, unknown> = {}
  const properties = props.schema.properties || {}

  for (const [key, field] of Object.entries(properties)) {
    if (props.initialValues && props.initialValues[key] !== undefined) {
      initial[key] = props.initialValues[key]
    } else if (field.default !== undefined) {
      initial[key] = field.default
    } else {
      // 根据类型设置默认值
      switch (field.type) {
        case 'string':
          initial[key] = ''
          break
        case 'integer':
        case 'number':
          initial[key] = field.minimum || 0
          break
        case 'boolean':
          initial[key] = false
          break
        default:
          initial[key] = null
      }
    }
  }

  formData.value = initial
})

// 监听 initialValues 变化
watch(
  () => props.initialValues,
  (newValues) => {
    if (newValues) {
      formData.value = { ...formData.value, ...newValues }
    }
  },
  { deep: true }
)

const schemaProperties = computed(() => {
  return props.schema.properties || {}
})

const requiredFields = computed(() => {
  return new Set(props.schema.required || [])
})

function isRequired(key: string): boolean {
  return requiredFields.value.has(key)
}

function updateField(key: string, value: unknown) {
  formData.value[key] = value
  // 清除该字段的错误
  delete errors.value[key]
}

// 验证表单
const isValid = computed(() => {
  const properties = props.schema.properties || {}

  for (const key of requiredFields.value) {
    const value = formData.value[key]
    const field = properties[key]

    if (value === undefined || value === null || value === '') {
      return false
    }

    // 字符串长度验证
    if (field.type === 'string' && field.minLength && typeof value === 'string') {
      if (value.length < field.minLength) return false
    }

    // 数字范围验证
    if ((field.type === 'integer' || field.type === 'number') && typeof value === 'number') {
      if (field.minimum !== undefined && value < field.minimum) return false
      if (field.maximum !== undefined && value > field.maximum) return false
    }
  }

  return true
})

function validate(): boolean {
  errors.value = {}
  const properties = props.schema.properties || {}
  let valid = true

  for (const key of requiredFields.value) {
    const value = formData.value[key]
    const field = properties[key]

    if (value === undefined || value === null || value === '') {
      errors.value[key] = `${field.title || key} 是必填项`
      valid = false
    }
  }

  for (const [key, field] of Object.entries(properties)) {
    const value = formData.value[key]

    if (value === undefined || value === null || value === '') continue

    // 字符串长度验证
    if (field.type === 'string' && typeof value === 'string') {
      if (field.minLength && value.length < field.minLength) {
        errors.value[key] = `最少需要 ${field.minLength} 个字符`
        valid = false
      }
      if (field.maxLength && value.length > field.maxLength) {
        errors.value[key] = `最多只能 ${field.maxLength} 个字符`
        valid = false
      }
    }

    // 数字范围验证
    if ((field.type === 'integer' || field.type === 'number') && typeof value === 'number') {
      if (field.minimum !== undefined && value < field.minimum) {
        errors.value[key] = `不能小于 ${field.minimum}`
        valid = false
      }
      if (field.maximum !== undefined && value > field.maximum) {
        errors.value[key] = `不能大于 ${field.maximum}`
        valid = false
      }
    }
  }

  return valid
}

function handleSubmit() {
  if (validate()) {
    emit('submit', { ...formData.value })
  }
}
</script>

<style scoped>
.tool-config-form {
  max-width: 480px;
}
</style>
