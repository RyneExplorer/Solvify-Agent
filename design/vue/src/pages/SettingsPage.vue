<template>
  <div class="py-8 px-10">
    <h1 class="text-[28px] font-bold text-slate-900 mb-6" style="font-family: 'Space Grotesk', sans-serif; letter-spacing: -0.02em;">系统配置</h1>

    <!-- Tabs -->
    <AppTabs v-model="activeTab" :tabs="tabs" class="mb-6" />

    <!-- AI Model tab -->
    <div v-if="activeTab === 'model'">
      <!-- Free models -->
      <AppCard class="mb-3">
        <h3 class="text-base font-semibold text-slate-900 mb-4" style="font-family: 'Space Grotesk', sans-serif;">免费模型</h3>
        <div class="grid grid-cols-2 gap-3">
          <div v-for="m in freeModels" :key="m.name"
            class="flex justify-between items-center p-3.5 border border-slate-200 rounded-xl"
          >
            <div>
              <div class="text-sm font-medium text-slate-900">{{ m.name }}</div>
              <div class="text-xs text-slate-400 mt-0.5">每月 100 次免费调用</div>
            </div>
            <AppBadge variant="success">可用</AppBadge>
          </div>
        </div>
      </AppCard>

      <!-- Custom model config -->
      <AppCard>
        <h3 class="text-base font-semibold text-slate-900 mb-4" style="font-family: 'Space Grotesk', sans-serif;">自定义模型</h3>
        <div class="mb-4">
          <label class="block text-[13px] font-medium text-slate-600 mb-1.5">API Key</label>
          <input type="password" placeholder="sk-..." class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-3 text-slate-900 outline-none transition-colors focus:border-slate-900" />
        </div>
        <div class="mb-5">
          <label class="block text-[13px] font-medium text-slate-600 mb-1.5">模型名称</label>
          <input placeholder="gpt-4 / claude-3-opus / ..." class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-3 text-slate-900 outline-none transition-colors focus:border-slate-900" />
        </div>
        <AppButton>保存配置</AppButton>
      </AppCard>
    </div>

    <!-- Search Tools tab -->
    <div v-if="activeTab === 'search'">
      <AppCard class="mb-3">
        <h3 class="text-base font-semibold text-slate-900 mb-2" style="font-family: 'Space Grotesk', sans-serif;">搜索工具配置</h3>
        <p class="text-[13px] text-slate-400 mb-4">
          配置搜索工具 API Key 后可使用深度模式，深度模式会在知识库无结果时自动联网搜索。
        </p>
        <div v-for="(tool, i) in searchTools" :key="tool.name"
          class="flex justify-between items-center p-3.5 border border-slate-200 rounded-xl"
          :class="{ 'mb-2': i < searchTools.length - 1 }"
        >
          <div>
            <div class="text-sm font-medium text-slate-900">{{ tool.name }}</div>
            <div class="text-xs text-slate-400 mt-0.5">{{ tool.desc }}</div>
          </div>
          <div class="flex items-center gap-2">
            <AppBadge :variant="tool.configured ? 'success' : 'neutral'">{{ tool.configured ? '已配置' : '未配置' }}</AppBadge>
            <AppButton variant="secondary" size="sm">{{ tool.configured ? '修改' : '配置' }}</AppButton>
          </div>
        </div>
      </AppCard>

      <!-- Add search tool -->
      <AppCard>
        <h3 class="text-base font-semibold text-slate-900 mb-2" style="font-family: 'Space Grotesk', sans-serif;">添加搜索工具</h3>
        <div class="mb-4">
          <label class="block text-[13px] font-medium text-slate-600 mb-1.5">搜索引擎</label>
          <AppSelect v-model="newSearchEngine" class="w-full">
            <option>Bing</option><option>Tavily</option><option>百度</option><option>Google</option>
          </AppSelect>
        </div>
        <div class="mb-5">
          <label class="block text-[13px] font-medium text-slate-600 mb-1.5">API Key</label>
          <input type="password" placeholder="请输入 API Key" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-3 text-slate-900 outline-none transition-colors focus:border-slate-900" />
        </div>
        <AppButton>验证并保存</AppButton>
      </AppCard>
    </div>

    <!-- System Status tab -->
    <div v-if="activeTab === 'status'">
      <AppCard class="mb-3">
        <h3 class="text-base font-semibold text-slate-900 mb-4" style="font-family: 'Space Grotesk', sans-serif;">基础设施状态</h3>
        <p class="text-[13px] text-slate-400 mb-4">以下配置由管理员设置，普通用户不可修改。</p>
        <div class="flex flex-col gap-4">
          <div v-for="item in infraStatuses" :key="item.name"
            class="flex justify-between items-center p-3.5 bg-slate-50 rounded-xl"
          >
            <div>
              <div class="text-sm font-medium text-slate-900">{{ item.name }}</div>
              <div class="text-xs text-slate-400 mt-0.5">{{ item.detail }}</div>
            </div>
            <AppBadge :variant="item.variant">{{ item.status }}</AppBadge>
          </div>
        </div>
      </AppCard>

      <!-- Third-party integration -->
      <AppCard>
        <h3 class="text-base font-semibold text-slate-900 mb-4" style="font-family: 'Space Grotesk', sans-serif;">第三方平台集成</h3>
        <p class="text-[13px] text-slate-400 mb-4">集成配置由管理员管理，同步知识库可在知识库页面查看。</p>
        <div v-for="(item, i) in integrations" :key="item.name"
          class="flex justify-between items-center p-3.5 border border-slate-200 rounded-xl"
          :class="{ 'mb-2': i < integrations.length - 1 }"
        >
          <div>
            <div class="text-sm font-medium text-slate-900">{{ item.name }}</div>
            <div class="text-xs text-slate-400 mt-0.5">{{ item.desc }}</div>
          </div>
          <div class="flex items-center gap-3">
            <span class="text-xs text-slate-400">已同步 {{ item.synced }}</span>
            <AppBadge :variant="item.status === '已连接' ? 'success' : 'neutral'">{{ item.status }}</AppBadge>
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
import AppBadge from '../components/ui/AppBadge.vue'
import AppSelect from '../components/ui/AppSelect.vue'
import AppTabs from '../components/ui/AppTabs.vue'

const activeTab = ref('model')
const newSearchEngine = ref('Bing')

const tabs = [
  { key: 'model', label: 'AI 模型' },
  { key: 'search', label: '搜索工具' },
  { key: 'status', label: '系统状态' },
]

const freeModels = [
  { name: 'GPT-3.5 Turbo' },
  { name: 'Claude Haiku' },
  { name: '通义千问-Turbo' },
  { name: 'GLM-4-Flash' },
]

const searchTools = [
  { name: 'Bing', desc: '微软搜索 · Azure 订阅获取', configured: true },
  { name: 'Tavily', desc: 'AI 搜索 · tavily.com 注册', configured: false },
  { name: '百度', desc: '中文搜索 · 百度开放平台', configured: false },
  { name: 'Google', desc: '谷歌搜索 · Google Cloud 订阅', configured: false },
]

const infraStatuses = [
  { name: '向量数据库', detail: 'PostgreSQL (pgvector)', status: '运行中', variant: 'success' as const },
  { name: 'RAG 引擎', detail: 'Eino Framework', status: '运行中', variant: 'success' as const },
  { name: '默认 AI 模型', detail: 'GPT-4 (系统级)', status: '系统配置', variant: 'blue' as const },
]

const integrations = [
  { name: '钉钉', desc: '通过 Webhook 同步知识库', status: '已连接', synced: '234 篇' },
  { name: '飞书', desc: '从飞书文档同步知识库', status: '已连接', synced: '189 篇' },
  { name: 'Notion', desc: '从 Notion 页面同步知识库', status: '未配置', synced: '-' },
]
</script>
