<template>
  <div class="w-full max-w-2xl mx-auto px-4">
    <div class="bg-white rounded-2xl shadow-lg border border-gray-100 overflow-hidden">
      <!-- 输入框 -->
      <div class="relative">
        <textarea
          v-model="inputText"
          rows="3"
          class="w-full px-5 py-4 text-sm text-gray-700 placeholder-gray-300 resize-none outline-none border-none"
          placeholder="请输入您想要咨询的问题或需要帮助的内容..."
          @keydown.enter.prevent="handleSend"
        ></textarea>
      </div>

      <!-- 工具栏 -->
      <div class="flex items-center justify-between px-4 py-3 border-t border-gray-50">
        <!-- 左侧工具 -->
        <div class="flex items-center gap-2">
          <!-- 全部知识库 -->
          <button class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs text-gray-500 hover:bg-gray-50 transition-colors border border-gray-200">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6.253v13m0-13C10.832 5.477 9.246 5 7.5 5S4.168 5.477 3 6.253v13C4.168 18.477 5.754 18 7.5 18s3.332.477 4.5 1.253m0-13C13.168 5.477 14.754 5 16.5 5c1.747 0 3.332.477 4.5 1.253v13C19.832 18.477 18.247 18 16.5 18c-1.746 0-3.332.477-4.5 1.253"/>
            </svg>
            <span>全部知识库</span>
            <svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/>
            </svg>
          </button>

          <!-- 快速 -->
          <button class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs text-gray-500 hover:bg-gray-50 transition-colors border border-gray-200">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z"/>
            </svg>
            <span>快速</span>
            <svg class="w-3 h-3" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/>
            </svg>
          </button>

          <!-- 模型选择 -->
          <div class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs text-gray-500 hover:bg-gray-50 transition-colors border border-gray-200 cursor-pointer">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z"/>
            </svg>
            <select v-model="selectedModel" class="bg-transparent outline-none text-xs text-gray-500 cursor-pointer">
              <option value="gpt-4">GPT-4</option>
              <option value="claude-3">Claude-3</option>
              <option value="gpt-3.5">GPT-3.5</option>
            </select>
          </div>
        </div>

        <!-- 右侧发送按钮 -->
        <button 
          @click="handleSend"
          :disabled="!inputText.trim()"
          :class="[
            'w-9 h-9 rounded-full flex items-center justify-center transition-all',
            inputText.trim() 
              ? 'bg-primary-400 hover:bg-primary-500 text-white shadow-md' 
              : 'bg-gray-100 text-gray-300 cursor-not-allowed'
          ]"
        >
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M5 10l7-7m0 0l7 7m-7-7v18"/>
          </svg>
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

const inputText = ref('')
const selectedModel = ref('gpt-4')

const handleSend = () => {
  if (!inputText.value.trim()) return
  console.log('发送消息:', inputText.value)
  // 这里可以添加发送逻辑
  inputText.value = ''
}
</script>
