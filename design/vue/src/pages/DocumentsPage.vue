<template>
  <div class="py-8 px-10">
    <!-- Header -->
    <div class="flex justify-between items-center mb-6">
      <div>
        <h1 class="text-[28px] font-bold text-slate-900 m-0" style="font-family: 'Space Grotesk', sans-serif; letter-spacing: -0.02em;">文档管理</h1>
        <p class="text-sm text-slate-400 mt-2">上传、管理和编辑知识库中的文档</p>
      </div>
      <div class="flex gap-2">
        <AppButton variant="secondary">多源导入</AppButton>
        <AppButton>+ 上传文档</AppButton>
      </div>
    </div>

    <!-- Upload dropzone -->
    <div class="border-2 border-dashed border-slate-200 rounded-2xl p-10 text-center mb-6 bg-slate-50">
      <div class="text-[32px] mb-3">📤</div>
      <div class="text-base font-medium text-slate-900">拖拽文件到此处上传</div>
      <div class="text-[13px] text-slate-400 mt-2">支持 PDF/Word/Txt/Markdown/HTML/CSV/Excel/PPT/JSON/图片，单文件最大 100MB</div>
    </div>

    <!-- Documents table -->
    <AppCard class="!p-0 overflow-hidden">
      <table class="w-full text-sm border-collapse">
        <thead>
          <tr class="bg-slate-50 border-b border-slate-200">
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">文件名</th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">类型</th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">大小</th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">状态</th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">上传时间</th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(doc, i) in documents" :key="i" class="border-b border-slate-100 last:border-b-0">
            <td class="px-4 py-3">
              <span class="text-accent-600 cursor-pointer hover:underline">{{ doc.name }}</span>
            </td>
            <td class="px-4 py-3 text-slate-900">{{ doc.type }}</td>
            <td class="px-4 py-3 text-slate-900">{{ doc.size }}</td>
            <td class="px-4 py-3">
              <AppBadge :variant="doc.status === 'ready' ? 'success' : doc.status === 'error' ? 'error' : 'warning'">
                {{ doc.status === 'ready' ? '已就绪' : doc.status === 'error' ? '处理失败' : '处理中' }}
              </AppBadge>
            </td>
            <td class="px-4 py-3 text-slate-900">{{ doc.uploaded }}</td>
            <td class="px-4 py-3">
              <div class="flex gap-1">
                <AppButton variant="ghost" size="sm">编辑</AppButton>
                <AppButton variant="ghost" size="sm" class="!text-red-600 hover:!bg-red-50">删除</AppButton>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </AppCard>
  </div>
</template>

<script setup lang="ts">
import AppCard from '../components/ui/AppCard.vue'
import AppButton from '../components/ui/AppButton.vue'
import AppBadge from '../components/ui/AppBadge.vue'

const documents = [
  { name: '产品需求文档 v3.2.pdf', size: '2.4 MB', status: 'ready', type: 'PDF', uploaded: '2024-01-15' },
  { name: 'API 接口设计.docx', size: '890 KB', status: 'ready', type: 'Word', uploaded: '2024-01-14' },
  { name: '系统架构图.png', size: '1.2 MB', status: 'processing', type: '图片', uploaded: '2024-01-14' },
  { name: '数据库设计文档.md', size: '45 KB', status: 'ready', type: 'Markdown', uploaded: '2024-01-13' },
  { name: '部署指南.html', size: '120 KB', status: 'ready', type: 'HTML', uploaded: '2024-01-12' },
  { name: '测试用例集.xlsx', size: '3.1 MB', status: 'ready', type: 'Excel', uploaded: '2024-01-11' },
  { name: '用户手册.pdf', size: '5.6 MB', status: 'error', type: 'PDF', uploaded: '2024-01-10' },
]
</script>
