<template>
  <div class="py-8 px-10">
    <!-- Header -->
    <div class="flex justify-between items-center mb-6">
      <div>
        <h1 class="text-[28px] font-bold text-slate-900 m-0" style="font-family: 'Space Grotesk', sans-serif; letter-spacing: -0.02em;">知识库管理</h1>
        <p class="text-sm text-slate-400 mt-2">管理自建知识库，查看第三方平台同步的知识库</p>
      </div>
      <div class="flex gap-2">
        <AppButton variant="secondary">同步知识库</AppButton>
        <AppButton>+ 新建知识库</AppButton>
      </div>
    </div>

    <!-- Filters -->
    <div class="flex gap-2 mb-5">
      <SearchInput v-model="searchQuery" placeholder="搜索知识库..." wrapper-class="w-80" />
      <AppSelect v-model="filterCategory" class="w-40">
        <option>全部分类</option><option>技术</option><option>产品</option><option>客服</option><option>培训</option>
      </AppSelect>
      <AppSelect v-model="filterSource" class="w-40">
        <option>全部来源</option><option>自建</option><option>钉钉同步</option><option>飞书同步</option><option>Notion 同步</option><option>联网搜索</option>
      </AppSelect>
    </div>

    <!-- Self-created KBs -->
    <div class="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-3">自建知识库</div>
    <div class="grid gap-3 mb-8" style="grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));">
      <KBCard v-for="kb in selfKBs" :key="kb.id" :kb="kb" />
    </div>

    <!-- Synced KBs -->
    <div class="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-3">同步知识库</div>
    <div class="grid gap-3 mb-8" style="grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));">
      <KBCard
        v-for="kb in syncedKBs" :key="kb.id"
        :kb="kb"
        :source-label="sourceLabels[kb.source as keyof typeof sourceLabels]"
      />
    </div>

    <!-- Web search KBs -->
    <div class="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-3">联网搜索知识库</div>
    <div class="grid gap-3" style="grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));">
      <KBCard
        v-for="kb in webSearchKBs" :key="kb.id"
        :kb="kb"
        :source-label="sourceLabels[kb.source as keyof typeof sourceLabels]"
      >
        <div class="mt-2.5 px-3 py-2.5 bg-green-50 rounded-lg text-xs text-green-600">
          由深度模式联网搜索结果自动保存，存储计入个人配额
        </div>
      </KBCard>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import AppButton from '../components/ui/AppButton.vue'
import AppSelect from '../components/ui/AppSelect.vue'
import SearchInput from '../components/ui/SearchInput.vue'
import KBCard from '../components/ui/KBCard.vue'

const searchQuery = ref('')
const filterCategory = ref('全部分类')
const filterSource = ref('全部来源')

interface KB {
  id: number
  name: string
  category: string
  docs: number
  size: string
  status: string
  updated: string
  source: string
}

const knowledgeBases: KB[] = [
  { id: 1, name: '技术文档库', category: '技术', docs: 342, size: '2.1 GB', status: 'ready', updated: '2 小时前', source: 'self' },
  { id: 2, name: '产品需求库', category: '产品', docs: 156, size: '890 MB', status: 'ready', updated: '1 天前', source: 'self' },
  { id: 3, name: '客服知识库', category: '客服', docs: 892, size: '1.5 GB', status: 'processing', updated: '30 分钟前', source: 'self' },
  { id: 4, name: '钉钉-技术分享', category: '技术', docs: 234, size: '560 MB', status: 'ready', updated: '1 小时前', source: 'dingtalk' },
  { id: 5, name: '飞书-产品文档', category: '产品', docs: 189, size: '720 MB', status: 'ready', updated: '2 天前', source: 'feishu' },
  { id: 6, name: 'Notion-开发笔记', category: '技术', docs: 67, size: '180 MB', status: 'ready', updated: '3 天前', source: 'notion' },
  { id: 7, name: '联网搜索知识库', category: '综合', docs: 45, size: '12 MB', status: 'ready', updated: '1 小时前', source: 'web_search' },
]

const sourceLabels = {
  dingtalk: { text: '钉钉同步', color: '#2563eb' },
  feishu: { text: '飞书同步', color: '#7c3aed' },
  notion: { text: 'Notion 同步', color: '#000000' },
  web_search: { text: '联网搜索', color: '#16a34a' },
}

const selfKBs = computed(() => knowledgeBases.filter(kb => kb.source === 'self'))
const syncedKBs = computed(() => knowledgeBases.filter(kb => ['dingtalk', 'feishu', 'notion'].includes(kb.source)))
const webSearchKBs = computed(() => knowledgeBases.filter(kb => kb.source === 'web_search'))
</script>
