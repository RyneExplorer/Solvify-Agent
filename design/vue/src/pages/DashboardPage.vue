<template>
  <div class="py-8 px-10">
    <!-- Header -->
    <div class="mb-8">
      <h1 class="text-[28px] font-bold text-slate-900 m-0" style="font-family: 'Space Grotesk', sans-serif; letter-spacing: -0.02em;">
        欢迎回来，{{ userName }}
      </h1>
      <p class="text-sm text-slate-400 mt-2">这是你的知识管理概览</p>
    </div>

    <!-- Stat Cards -->
    <div class="grid grid-cols-4 gap-3 mb-8">
      <StatCard label="知识库" value="12" icon="📚" />
      <StatCard label="文档总数" value="1,847" icon="📄" />
      <StatCard label="本月问答" value="3,291" icon="💬" />
      <StatCard label="存储使用" value="4.2 GB" icon="💾" />
    </div>

    <!-- Two-column layout -->
    <div class="grid grid-cols-[2fr_1fr] gap-4">
      <!-- Recent Activity -->
      <AppCard>
        <h3 class="text-base font-semibold text-slate-900 mb-4" style="font-family: 'Space Grotesk', sans-serif;">最近活动</h3>
        <div v-for="(item, i) in activities" :key="i"
          class="flex items-center justify-between py-3"
          :class="{ 'border-b border-slate-100': i < activities.length - 1 }"
        >
          <div class="flex items-center gap-3">
            <div class="w-1.5 h-1.5 rounded-full bg-accent-600 shrink-0" />
            <div>
              <div class="text-sm text-slate-900">{{ item.detail }}</div>
              <div class="text-xs text-slate-400 mt-0.5">{{ item.action }} &middot; {{ item.kb }}</div>
            </div>
          </div>
          <span class="text-xs text-slate-400 shrink-0">{{ item.time }}</span>
        </div>
      </AppCard>

      <!-- Right column -->
      <div class="flex flex-col gap-3">
        <!-- Quick Actions -->
        <AppCard>
          <h3 class="text-base font-semibold text-slate-900 mb-4" style="font-family: 'Space Grotesk', sans-serif;">快速操作</h3>
          <div class="flex flex-col gap-2">
            <AppButton variant="secondary" class="w-full justify-center">+ 新建知识库</AppButton>
            <AppButton variant="secondary" class="w-full justify-center">上传文档</AppButton>
            <AppButton variant="secondary" class="w-full justify-center">开始问答</AppButton>
          </div>
        </AppCard>

        <!-- System Status -->
        <AppCard>
          <h3 class="text-base font-semibold text-slate-900 mb-4" style="font-family: 'Space Grotesk', sans-serif;">系统状态</h3>
          <div class="flex flex-col gap-3 text-sm">
            <div class="flex justify-between items-center">
              <span class="text-slate-400">RAG 引擎</span>
              <AppBadge variant="success">运行中</AppBadge>
            </div>
            <div class="flex justify-between items-center">
              <span class="text-slate-400">向量数据库</span>
              <AppBadge variant="success">已连接</AppBadge>
            </div>
            <div class="flex justify-between items-center">
              <span class="text-slate-400">AI 模型</span>
              <AppBadge variant="blue">GPT-4</AppBadge>
            </div>
            <div class="flex justify-between items-center">
              <span class="text-slate-400">本月配额</span>
              <AppBadge variant="warning">68/100</AppBadge>
            </div>
          </div>
        </AppCard>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import AppCard from '../components/ui/AppCard.vue'
import AppButton from '../components/ui/AppButton.vue'
import AppBadge from '../components/ui/AppBadge.vue'
import StatCard from '../components/ui/StatCard.vue'

withDefaults(defineProps<{
  userName?: string
}>(), {
  userName: 'Admin',
})

const activities = [
  { detail: '产品需求文档 v3.2.pdf', action: '上传文档', kb: '产品文档库', time: '10 分钟前' },
  { detail: '如何配置向量数据库？', action: '问答会话', kb: '技术支持库', time: '1 小时前' },
  { detail: '新增 23 篇技术文档', action: '知识库更新', kb: '技术文档库', time: '3 小时前' },
]
</script>
