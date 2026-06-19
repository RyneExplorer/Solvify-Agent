<template>
  <main class="flex-1 flex flex-col items-center justify-center relative">
    <!-- 标题区域 -->
    <div class="text-center mb-10">
      <h1 class="text-4xl font-bold text-gray-800 italic mb-2">开始新的对话</h1>
      <p class="text-sm text-gray-400">向你的知识库提问，获取回答与分析</p>
    </div>

    <!-- 输入框组件 -->
    <ChatInput />

    <!-- 底部声明 -->
    <p class="absolute bottom-8 text-xs text-gray-300">内容由 AI 生成，仅供参考</p>
  </main>
</template>

<script setup lang="ts">
import ChatInput from './ChatInput.vue'
</script>
