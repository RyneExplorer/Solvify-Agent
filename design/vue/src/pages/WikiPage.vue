<template>
  <div class="py-8 px-10">
    <!-- Header -->
    <div class="flex justify-between items-center mb-6">
      <div>
        <h1 class="text-[28px] font-bold text-slate-900 m-0" style="font-family: 'Space Grotesk', sans-serif; letter-spacing: -0.02em;">Wiki 知识库</h1>
        <p class="text-sm text-slate-400 mt-2">由 Agent 自动生成的结构化知识页面</p>
      </div>
      <AppButton>生成 Wiki</AppButton>
    </div>

    <!-- Wiki layout: tree nav + content -->
    <div class="grid gap-4" style="grid-template-columns: 240px 1fr;">
      <!-- Sidebar tree -->
      <AppCard class="!p-3 h-fit">
        <div class="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-2.5 px-2.5">页面目录</div>
        <div
          v-for="(item, i) in wikiTree" :key="i"
          @click="activeWikiPage = i"
          :class="[
            'py-1.5 rounded-lg text-sm cursor-pointer transition-colors',
            activeWikiPage === i
              ? 'bg-accent-50 text-accent-600 font-medium'
              : 'text-slate-600 hover:bg-slate-50'
          ]"
          :style="{ paddingLeft: (10 + item.level * 16) + 'px', paddingRight: '10px' }"
        >
          {{ item.title }}
        </div>
      </AppCard>

      <!-- Content area -->
      <AppCard>
        <h2 class="text-xl font-semibold text-slate-900 mb-4" style="font-family: 'Space Grotesk', sans-serif;">系统架构</h2>
        <div class="text-sm text-slate-600 leading-relaxed">
          <p>Solvify-Agent 采用微服务架构，核心组件包括：</p>
          <ul class="pl-5 my-3 space-y-1">
            <li><strong class="text-slate-900">API Gateway</strong> - 统一入口，负责认证、限流、路由</li>
            <li><strong class="text-slate-900">RAG Engine</strong> - 检索增强生成引擎，支持多模式搜索</li>
            <li><strong class="text-slate-900">Agent Orchestrator</strong> - ReAct Agent 编排器，处理复杂推理任务</li>
            <li><strong class="text-slate-900">Document Processor</strong> - 多格式文档解析和向量化</li>
            <li><strong class="text-slate-900">Vector Store</strong> - 向量存储层，支持多种后端引擎</li>
          </ul>
          <p>各服务通过消息队列进行异步通信，保证系统的可扩展性和容错能力。</p>

          <!-- Knowledge graph box -->
          <div class="mt-5 p-4 bg-slate-50 rounded-xl border border-slate-200">
            <div class="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-2">知识图谱</div>
            <div class="text-[13px] text-slate-600 leading-relaxed" style="font-family: 'JetBrains Mono', monospace;">
              系统架构 → API Gateway, RAG Engine, Agent Orchestrator<br/>
              RAG Engine → 向量检索, 全文检索, 混合排序<br/>
              Agent Orchestrator → ReAct 循环, 工具调用, 多步推理
            </div>
          </div>
        </div>
      </AppCard>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import AppCard from '../components/ui/AppCard.vue'
import AppButton from '../components/ui/AppButton.vue'

const activeWikiPage = ref(0)

const wikiTree = [
  { title: '系统架构', level: 0 },
  { title: '整体设计', level: 1 },
  { title: '微服务拆分', level: 1 },
  { title: '数据库设计', level: 0 },
  { title: 'ER 图', level: 1 },
  { title: '索引优化', level: 1 },
  { title: 'API 文档', level: 0 },
  { title: '认证接口', level: 1 },
  { title: '知识库接口', level: 1 },
  { title: '问答接口', level: 1 },
  { title: '部署指南', level: 0 },
  { title: 'Docker 部署', level: 1 },
  { title: 'K8s 部署', level: 1 },
]
</script>
