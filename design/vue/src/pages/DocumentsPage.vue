<template>
  <div class="py-8 px-10" @click="activeMenuID = ''">
    <div class="flex flex-wrap justify-between items-start gap-4 mb-6">
      <div>
        <h1 class="text-[28px] font-bold text-slate-900 m-0" style="font-family: 'Space Grotesk', sans-serif;">文档管理</h1>
        <p class="text-sm text-slate-400 mt-2">上传、管理和查看知识库中的文档处理状态</p>
      </div>
      <div class="flex gap-2">
        <AppButton variant="secondary" disabled>多源导入</AppButton>
        <AppButton :disabled="!knowledgeBaseID || uploading" @click="openFilePicker">
          {{ uploading ? '上传中' : '+ 上传文档' }}
        </AppButton>
      </div>
    </div>

    <div class="grid grid-cols-1 lg:grid-cols-[1.2fr_0.8fr] gap-4 mb-6">
      <AppCard>
        <div class="flex items-start justify-between gap-4">
          <div>
            <p class="text-xs font-medium text-slate-400 m-0">当前知识库</p>
            <h2 class="text-lg font-semibold text-slate-900 mt-2 mb-1">{{ currentKnowledgeBaseText }}</h2>
            <p class="text-sm text-slate-500 m-0">{{ knowledgeBaseHint }}</p>
          </div>
          <AppBadge variant="blue">{{ documents.length }} 篇文档</AppBadge>
        </div>
      </AppCard>

      <AppCard>
        <div class="flex items-start justify-between gap-4">
          <div>
            <p class="text-xs font-medium text-slate-400 m-0">我的存储空间</p>
            <h2 class="text-lg font-semibold text-slate-900 mt-2 mb-1">{{ quotaUsageText }}</h2>
            <p class="text-sm text-slate-500 m-0">{{ quotaRemainText }}</p>
          </div>
          <AppBadge :variant="quotaBadgeVariant">{{ quotaBadgeText }}</AppBadge>
        </div>
        <div class="h-2 bg-slate-100 rounded-full overflow-hidden mt-4">
          <div
            class="h-full rounded-full transition-all duration-300"
            :class="quotaProgressClass"
            :style="{ width: `${quotaPercent}%` }"
          />
        </div>
        <p v-if="quotaError" class="text-xs text-red-500 mt-3 mb-0">{{ quotaError }}</p>
      </AppCard>
    </div>

    <input
      ref="fileInputRef"
      type="file"
      class="hidden"
      @change="handleFileChange"
    >

    <div
      class="border-2 border-dashed border-slate-200 rounded-2xl p-10 text-center mb-6 bg-slate-50 transition-colors"
      :class="knowledgeBaseID ? 'cursor-pointer hover:bg-slate-100' : 'opacity-60 cursor-not-allowed'"
      @click="openFilePicker"
      @dragover.prevent
      @drop.prevent="handleFileDrop"
    >
      <div class="text-base font-medium text-slate-900">{{ uploadPanelTitle }}</div>
      <div class="text-[13px] text-slate-400 mt-2">支持 TXT/Markdown/HTML/CSV/JSON/DOCX/PDF/PPTX 文件类型，单文件最大 100MB</div>
      <p v-if="uploadError" class="text-xs text-red-500 mt-3 mb-0">{{ uploadError }}</p>
      <p v-if="lastJob" class="text-xs text-slate-500 mt-3 mb-0">
        已创建处理任务：{{ shortID(lastJob.id) }}，当前状态 {{ jobStatusText(lastJob.status) }}
      </p>
    </div>

    <AppCard class="!p-0 overflow-visible">
      <div v-if="documentsLoading" class="px-4 py-10 text-center text-sm text-slate-500">正在加载文档列表</div>
      <div v-else-if="documentsError" class="px-4 py-10 text-center text-sm text-red-500">{{ documentsError }}</div>
      <div v-else-if="!knowledgeBaseID" class="px-4 py-10 text-center text-sm text-slate-500">
        <div class="flex flex-col items-center gap-3">
          <div>请先从知识库页面进入文档管理</div>
          <AppButton variant="secondary" size="sm" @click="goToKnowledgeBases">去选择知识库</AppButton>
        </div>
      </div>
      <div v-else-if="documents.length === 0" class="px-4 py-10 text-center text-sm text-slate-500">当前知识库还没有已导入文档</div>
      <table v-else class="w-full text-sm border-collapse">
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
          <tr v-for="doc in documents" :key="doc.id" class="border-b border-slate-100 last:border-b-0">
            <td class="px-4 py-3">
              <div class="text-accent-600 font-medium">{{ doc.file_name || doc.title }}</div>
              <div v-if="doc.error_message" class="text-xs text-red-500 mt-1">{{ doc.error_message }}</div>
            </td>
            <td class="px-4 py-3 text-slate-900">{{ fileTypeText(doc.file_type) }}</td>
            <td class="px-4 py-3 text-slate-900">{{ formatBytes(doc.file_size) }}</td>
            <td class="px-4 py-3">
              <AppBadge :variant="documentStatusVariant(doc.status)">
                {{ documentStatusText(doc.status) }}
              </AppBadge>
            </td>
            <td class="px-4 py-3 text-slate-900">{{ formatDate(doc.created_at) }}</td>
            <td class="px-4 py-3">
              <div class="relative inline-block">
                <AppButton variant="ghost" size="sm" class="!px-2" @click.stop="toggleActionMenu(doc.id)">...</AppButton>
                <div
                  v-if="activeMenuID === doc.id"
                  class="absolute right-0 top-9 z-50 w-44 rounded-xl border border-slate-100 bg-white p-1.5 shadow-xl"
                  @click.stop
                >
                  <button class="menu-item" @click="editDocument(doc)">编辑文档</button>
                  <button v-if="!doc.external_url" class="menu-item" @click="openPreviewFromMenu(doc)">查看原文</button>
                  <a
                    v-if="doc.external_url"
                    class="menu-item"
                    :href="doc.external_url"
                    target="_blank"
                    rel="noopener"
                    @click="activeMenuID = ''"
                  >
                    查看原文
                  </a>
                  <button class="menu-item" @click="openJobsFromMenu(doc)">文档历史</button>
                  <button
                    v-if="doc.status === documentStatusFailed"
                    class="menu-item"
                    :disabled="retryingID === doc.id"
                    @click="retryProcessFromMenu(doc)"
                  >
                    {{ retryingID === doc.id ? '重试中' : '重试处理' }}
                  </button>
                  <div class="my-1 border-t border-slate-100" />
                  <button class="menu-item menu-item-danger" @click="openDeleteDialog(doc)">删除</button>
                </div>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </AppCard>

    <AppCard v-if="syncSourceID || syncItems.length || syncItemsLoading" class="!p-0 overflow-visible mt-6">
      <div class="px-4 py-3 border-b border-slate-100 flex items-center justify-between gap-3">
        <div>
          <div class="text-sm font-semibold text-slate-900">钉钉文件</div>
          <div class="text-xs text-slate-400 mt-1">未导入的文件只支持查看原文，不参与本地检索</div>
        </div>
        <div class="flex items-center gap-3">
          <span v-if="selectedSyncFileCount" class="text-xs text-slate-500">已选择 {{ selectedSyncFileCount }} 个文件</span>
          <AppButton variant="secondary" size="sm" :disabled="syncItemsLoading" @click="loadSyncItems">
            {{ syncItemsLoading ? '刷新中' : '刷新列表' }}
          </AppButton>
        </div>
      </div>
      <div v-if="syncItemsLoading" class="px-4 py-10 text-center text-sm text-slate-500">正在加载钉钉文件</div>
      <div v-else-if="syncItemsError" class="px-4 py-10 text-center text-sm text-red-500">{{ syncItemsError }}</div>
      <div v-else-if="syncItems.length === 0" class="px-4 py-10 text-center text-sm text-slate-500">暂无钉钉文件，请先刷新目录</div>
      <table v-else class="w-full table-fixed text-sm border-collapse">
        <colgroup>
          <col class="w-[36%]" />
          <col class="w-[9%]" />
          <col class="w-[10%]" />
          <col class="w-[10%]" />
          <col class="w-[18%]" />
          <col class="w-[17%]" />
        </colgroup>
        <thead>
          <tr class="bg-slate-50 border-b border-slate-200">
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">
              <div class="flex items-center gap-3">
                <el-checkbox
                  :model-value="allSyncItemsChecked"
                  :indeterminate="syncItemsSelectionIndeterminate"
                  aria-label="选择全部钉钉文件"
                  @change="toggleAllSyncItems"
                />
                <span>名称</span>
              </div>
            </th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">类型</th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">大小</th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">导入状态</th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">更新时间</th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="{ item, depth } in visibleSyncItems" :key="item.id" class="border-b border-slate-100 last:border-b-0">
            <td class="px-4 py-3">
              <div class="flex items-start gap-2" :style="{ paddingLeft: `${depth * 24}px` }">
                <el-checkbox
                  class="mt-0.5"
                  :model-value="selectedSyncItemIDs.has(item.external_id)"
                  :indeterminate="isSyncItemIndeterminate(item)"
                  :aria-label="`选择 ${item.name}`"
                  @change="checked => toggleSyncItemSelection(item, checked)"
                />
                <button
                  v-if="item.children.length"
                  type="button"
                  class="sync-tree-toggle"
                  :title="expandedSyncItemIDs.has(item.external_id) ? '收起目录' : '展开目录'"
                  @click="toggleSyncItemExpanded(item)"
                >
                  <span :class="expandedSyncItemIDs.has(item.external_id) ? 'rotate-90' : ''">›</span>
                </button>
                <span v-else class="w-5 shrink-0" />
                <el-icon class="mt-0.5 h-5 w-5 shrink-0 text-lg" :class="syncItemIcon(item).color" aria-hidden="true">
                  <component :is="syncItemIcon(item).component" />
                </el-icon>
                <div class="min-w-0">
                  <div class="font-medium text-slate-900 break-words">{{ item.name }}</div>
                  <div v-if="syncItemHint(item)" class="text-xs text-slate-400 mt-1">{{ syncItemHint(item) }}</div>
                  <div v-if="item.error_message" class="text-xs text-red-500 mt-1">{{ item.error_message }}</div>
                </div>
              </div>
            </td>
            <td class="px-4 py-3 text-slate-900 whitespace-nowrap">{{ syncItemTypeText(item) }}</td>
            <td class="px-4 py-3 text-slate-900 whitespace-nowrap">{{ item.item_type === 'FOLDER' ? '-' : formatBytes(item.file_size) }}</td>
            <td class="px-4 py-3 whitespace-nowrap">
              <AppBadge :variant="syncImportStatusVariant(item.import_status)">
                {{ syncImportStatusText(item.import_status) }}
              </AppBadge>
            </td>
            <td class="px-4 py-3 text-slate-900 whitespace-nowrap">{{ formatDate(item.source_updated_at) }}</td>
            <td class="px-4 py-3">
              <div class="flex flex-nowrap items-center gap-2 whitespace-nowrap">
                <a
                  v-if="item.external_url"
                  class="text-sm text-accent-600 hover:text-accent-700"
                  :href="item.external_url"
                  target="_blank"
                  rel="noopener"
                >
                  查看原文
                </a>
                <button
                  v-if="canImportSyncItem(item)"
                  class="text-sm text-accent-600 hover:text-accent-700 disabled:text-slate-300"
                  :disabled="importingItemID === item.id"
                  @click="importDingTalkItem(item)"
                >
                  {{ importingItemID === item.id ? '导入中' : '导入本地' }}
                </button>
                <button
                  v-if="item.local_document_id"
                  class="text-sm text-slate-600 hover:text-slate-900"
                  @click="openImportedDocument(item.local_document_id)"
                >
                  查看本地文档
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </AppCard>

    <div v-if="jobsPanelOpen" class="fixed inset-0 bg-slate-900/30 flex items-center justify-center px-4 z-50" @click.self="closeJobs">
      <div class="bg-white rounded-2xl shadow-xl w-full max-w-2xl overflow-hidden">
        <div class="px-5 py-4 border-b border-slate-100 flex items-center justify-between">
          <div>
            <h3 class="text-base font-semibold text-slate-900 m-0">文档历史</h3>
            <p class="text-xs text-slate-400 mt-1 mb-0">{{ selectedDocument?.file_name }}</p>
          </div>
          <AppButton variant="ghost" size="sm" @click="closeJobs">关闭</AppButton>
        </div>
        <div class="max-h-[70vh] overflow-y-auto p-5">
          <section>
            <div class="mb-3 flex items-center justify-between gap-3">
              <h4 class="m-0 text-sm font-semibold text-slate-900">版本历史</h4>
              <span class="text-xs text-slate-400">{{ versions.length }} 个版本</span>
            </div>
            <div v-if="versionsLoading" class="text-sm text-slate-500">正在加载版本记录</div>
            <div v-else-if="versionsError" class="text-sm text-red-500">{{ versionsError }}</div>
            <div v-else-if="versions.length === 0" class="rounded-xl border border-slate-100 px-4 py-3 text-sm text-slate-500">暂无版本记录</div>
            <div v-else class="space-y-3">
              <div v-for="version in versions" :key="version.id" class="rounded-xl border border-slate-100 px-4 py-3">
                <div class="flex items-start justify-between gap-3">
                  <div>
                    <div class="text-sm font-medium text-slate-900">v{{ version.version_no }}</div>
                    <div class="mt-1 text-sm text-slate-600">{{ versionChangeSummary(version.change_summary) }}</div>
                  </div>
                  <span class="shrink-0 text-xs text-slate-400">{{ formatDate(version.created_at) }}</span>
                </div>
              </div>
            </div>
          </section>

          <section class="mt-5 border-t border-slate-100 pt-5">
            <div class="mb-3 flex items-center justify-between gap-3">
              <h4 class="m-0 text-sm font-semibold text-slate-900">处理任务</h4>
              <span class="text-xs text-slate-400">{{ jobs.length }} 条记录</span>
            </div>
            <div v-if="jobsLoading" class="text-sm text-slate-500">正在加载任务记录</div>
            <div v-else-if="jobsError" class="text-sm text-red-500">{{ jobsError }}</div>
            <div v-else-if="jobs.length === 0" class="rounded-xl border border-slate-100 px-4 py-3 text-sm text-slate-500">暂无任务记录</div>
            <div v-else class="space-y-3">
              <div v-for="job in jobs" :key="job.id" class="border border-slate-100 rounded-xl px-4 py-3">
                <div class="flex items-center justify-between gap-3">
                  <div class="text-sm font-medium text-slate-900">{{ job.job_type }}</div>
                  <AppBadge :variant="jobStatusVariant(job.status)">{{ jobStatusText(job.status) }}</AppBadge>
                </div>
                <div class="text-xs text-slate-400 mt-2">
                  创建：{{ formatDate(job.created_at) }}
                  <span v-if="job.finished_at"> · 完成：{{ formatDate(job.finished_at) }}</span>
                </div>
                <div v-if="job.error_message" class="text-xs text-red-500 mt-2">{{ job.error_message }}</div>
              </div>
            </div>
          </section>
        </div>
      </div>
    </div>

    <div v-if="previewDialogOpen" class="fixed inset-0 bg-slate-900/40 flex items-center justify-center px-4 z-50" @click.self="closePreview">
      <div class="bg-white rounded-2xl shadow-xl w-full max-w-5xl overflow-hidden">
        <div class="px-5 py-4 border-b border-slate-100 flex items-center justify-between gap-4">
          <div class="min-w-0">
            <h3 class="text-base font-semibold text-slate-900 m-0">查看原文</h3>
            <p class="text-xs text-slate-400 mt-1 mb-0 truncate">{{ previewDocument?.file_name }}</p>
          </div>
          <div class="flex items-center gap-2">
            <a v-if="previewURL" :href="previewURL" :download="previewDocument?.file_name" class="text-sm text-accent-600 hover:text-accent-700">下载原文件</a>
            <AppButton variant="ghost" size="sm" @click="closePreview">关闭</AppButton>
          </div>
        </div>
        <div class="h-[75vh] bg-slate-50 p-4">
          <div v-if="previewLoading" class="h-full flex items-center justify-center text-sm text-slate-500">正在加载原文件</div>
          <div v-else-if="previewError" class="h-full flex items-center justify-center text-sm text-red-500">{{ previewError }}</div>
          <img v-else-if="previewKind === 'image'" :src="previewURL" :alt="previewDocument?.file_name" class="h-full w-full object-contain" />
          <iframe v-else-if="previewKind === 'pdf'" :src="previewURL" :title="previewDocument?.file_name" class="h-full w-full border-0 rounded-xl bg-white" />
          <div v-else-if="previewKind === 'docx'" class="h-full overflow-auto rounded-xl bg-white">
            <div ref="docxPreviewContainer"></div>
          </div>
          <article v-else-if="previewKind === 'markdown'" class="md-content preview-text-panel" v-html="previewHTML" />
          <pre v-else-if="previewKind === 'text'" class="preview-text-panel preview-source"><code>{{ previewText }}</code></pre>
          <pre v-else-if="previewKind === 'json'" class="preview-text-panel preview-source preview-json"><code v-html="previewHTML" /></pre>
          <div v-else-if="previewKind === 'csv'" class="preview-text-panel overflow-auto">
            <table class="preview-csv-table">
              <thead v-if="previewTable.length">
                <tr>
                  <th v-for="(cell, index) in previewTable[0]" :key="`header-${index}`">{{ cell }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(row, rowIndex) in previewTable.slice(1)" :key="rowIndex">
                  <td v-for="(cell, cellIndex) in row" :key="cellIndex">{{ cell }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-else class="h-full flex flex-col items-center justify-center text-center">
            <div class="text-sm font-medium text-slate-900">浏览器暂不支持直接预览该格式</div>
            <div class="text-xs text-slate-500 mt-2">可下载原文件后使用本地应用打开</div>
          </div>
        </div>
      </div>
    </div>

    <div v-if="deleteDialogOpen" class="fixed inset-0 bg-slate-900/30 flex items-center justify-center px-4 z-50" @click.self="closeDeleteDialog">
      <div class="bg-white rounded-2xl shadow-xl w-full max-w-sm overflow-hidden">
        <div class="px-5 py-4 border-b border-slate-100">
          <h3 class="text-base font-semibold text-slate-900 m-0">删除文档</h3>
          <p class="text-sm text-slate-500 mt-2 mb-0">确认删除「{{ deletingDocument?.file_name || deletingDocument?.title }}」吗？</p>
        </div>
        <div v-if="deleteError" class="mx-5 mt-4 rounded-xl bg-red-50 px-3 py-2 text-sm text-red-600">{{ deleteError }}</div>
        <div class="px-5 py-4 flex justify-end gap-2">
          <AppButton variant="secondary" :disabled="!!deletingID" @click="closeDeleteDialog">取消</AppButton>
          <AppButton variant="danger" :disabled="!!deletingID" @click="confirmDeleteDocument">
            {{ deletingID ? '删除中' : '删除' }}
          </AppButton>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Document as DocumentIcon, Files, Folder, Grid, Link, Picture } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { marked } from 'marked'
import Papa from 'papaparse'
import DOMPurify from 'dompurify'
import hljs from 'highlight.js/lib/core'
import jsonLanguage from 'highlight.js/lib/languages/json'
import { deleteDocument, getDocumentJob, getDocumentPreview, listDocumentJobs, listDocumentVersions, listDocuments, processDocument, uploadDocument } from '@/api/document'
import { getKnowledgeBase } from '@/api/knowledge'
import { getStorageQuota } from '@/api/storage'
import { importSyncItem, listSyncItems, listSyncSources } from '@/api/sync'
import type { Document, DocumentProcessingJob, DocumentVersionListItem } from '@/types/document'
import type { KnowledgeBase } from '@/types/knowledge'
import type { StorageQuota } from '@/types/storage'
import type { SyncItem } from '@/types/sync'
import AppCard from '../components/ui/AppCard.vue'
import AppButton from '../components/ui/AppButton.vue'
import AppBadge from '../components/ui/AppBadge.vue'

interface SyncItemTreeNode extends SyncItem {
  children: SyncItemTreeNode[]
}

interface VisibleSyncItem {
  item: SyncItemTreeNode
  depth: number
}

const documentStatusUploaded = 1
const documentStatusProcessing = 2
const documentStatusFailed = 4
const documentJobStatusPending = 1
const documentJobStatusRunning = 2
const documentPollingIntervalMs = 2000

const route = useRoute()
const router = useRouter()
const quota = ref<StorageQuota | null>(null)
const quotaLoading = ref(false)
const quotaError = ref('')
const documents = ref<Document[]>([])
const documentsLoading = ref(false)
const documentsError = ref('')
const uploading = ref(false)
const uploadError = ref('')
const lastJob = ref<DocumentProcessingJob | null>(null)
const deletingID = ref('')
const retryingID = ref('')
const fileInputRef = ref<HTMLInputElement | null>(null)
const jobsPanelOpen = ref(false)
const jobsLoading = ref(false)
const jobsError = ref('')
const jobs = ref<DocumentProcessingJob[]>([])
const versionsLoading = ref(false)
const versionsError = ref('')
const versions = ref<DocumentVersionListItem[]>([])
const selectedDocument = ref<Document | null>(null)
const activeMenuID = ref('')
const deleteDialogOpen = ref(false)
const deletingDocument = ref<Document | null>(null)
const deleteError = ref('')
const previewDialogOpen = ref(false)
const previewLoading = ref(false)
const previewError = ref('')
const previewDocument = ref<Document | null>(null)
const previewURL = ref('')
const previewHTML = ref('')
const previewText = ref('')
const previewTable = ref<string[][]>([])
const docxPreviewContainer = ref<HTMLElement | null>(null)
const knowledgeBase = ref<KnowledgeBase | null>(null)
const knowledgeBaseLoading = ref(false)
const knowledgeBaseError = ref('')
const syncItems = ref<SyncItem[]>([])
const syncItemsLoading = ref(false)
const syncItemsError = ref('')
const syncSourceID = ref('')
const importingItemID = ref('')
const selectedSyncItemIDs = ref(new Set<string>())
const expandedSyncItemIDs = ref(new Set<string>())
const syncTreeSourceID = ref('')
let documentPollingTimer: ReturnType<typeof window.setTimeout> | null = null

const knowledgeBaseID = computed(() => {
  const value = route.query.knowledge_base_id
  return typeof value === 'string' ? value : ''
})

hljs.registerLanguage('json', jsonLanguage)

const previewKind = computed<'image' | 'pdf' | 'docx' | 'markdown' | 'text' | 'json' | 'csv' | 'unsupported'>(() => {
  const type = previewDocument.value?.file_type.toLowerCase() || ''
  if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'].includes(type)) return 'image'
  if (type === 'pdf') return 'pdf'
  if (type === 'docx') return 'docx'
  if (['md', 'markdown'].includes(type)) return 'markdown'
  if (['txt', 'html'].includes(type)) return 'text'
  if (type === 'json') return 'json'
  if (type === 'csv') return 'csv'
  return 'unsupported'
})

const syncItemTree = computed<SyncItemTreeNode[]>(() => {
  const nodeMap = new Map<string, SyncItemTreeNode>()
  for (const item of syncItems.value) {
    nodeMap.set(item.external_id, { ...item, children: [] })
  }

  const roots: SyncItemTreeNode[] = []
  for (const node of nodeMap.values()) {
    const parent = nodeMap.get(node.parent_external_id)
    if (parent && parent !== node) {
      parent.children.push(node)
    } else {
      roots.push(node)
    }
  }

  const branches = [roots]
  while (branches.length) {
    const branch = branches.pop()
    if (!branch) continue
    branch.sort((left, right) => {
      if (left.item_type !== right.item_type) return left.item_type === 'FOLDER' ? -1 : 1
      return left.name.localeCompare(right.name, 'zh-CN')
    })
    for (const node of branch) {
      if (node.children.length) branches.push(node.children)
    }
  }
  return roots
})

const visibleSyncItems = computed<VisibleSyncItem[]>(() => {
  const visible: VisibleSyncItem[] = []
  const stack = syncItemTree.value.slice().reverse().map(item => ({ item, depth: 0 }))
  while (stack.length) {
    const current = stack.pop()
    if (!current) continue
    visible.push(current)
    if (!expandedSyncItemIDs.value.has(current.item.external_id)) continue
    for (let index = current.item.children.length - 1; index >= 0; index -= 1) {
      stack.push({ item: current.item.children[index], depth: current.depth + 1 })
    }
  }
  return visible
})

const selectedSyncFileCount = computed(() => {
  return syncItems.value.filter(item => item.item_type === 'FILE' && selectedSyncItemIDs.value.has(item.external_id)).length
})

const allSyncItemsChecked = computed(() => {
  return syncItems.value.length > 0 && syncItems.value.every(item => selectedSyncItemIDs.value.has(item.external_id))
})

const syncItemsSelectionIndeterminate = computed(() => {
  return selectedSyncItemIDs.value.size > 0 && !allSyncItemsChecked.value
})

const currentKnowledgeBaseText = computed(() => {
  if (knowledgeBaseLoading.value) return '正在加载知识库'
  if (knowledgeBase.value?.name) return knowledgeBase.value.name
  if (knowledgeBaseID.value) return '知识库详情不可用'
  return '未选择知识库'
})

const knowledgeBaseHint = computed(() => {
  if (knowledgeBaseError.value) return knowledgeBaseError.value
  if (knowledgeBase.value?.description) return knowledgeBase.value.description
  if (knowledgeBaseID.value) return '正在管理当前知识库下的文档'
  return '请从知识库页面点击文档进入'
})

const uploadPanelTitle = computed(() => {
  if (!knowledgeBaseID.value) return '请选择知识库后上传'
  if (uploading.value) return '文件正在上传'
  return '点击或拖拽文件到此处上传'
})

const quotaPercent = computed(() => {
  if (!quota.value || quota.value.max_storage_bytes <= 0) return 0
  return Math.min(100, Math.round((quota.value.used_storage_bytes / quota.value.max_storage_bytes) * 100))
})

const quotaUsageText = computed(() => {
  if (quotaLoading.value) return '正在读取配额'
  if (!quota.value) return '-- / --'
  return `${formatBytes(quota.value.used_storage_bytes)} / ${formatBytes(quota.value.max_storage_bytes)}`
})

const quotaRemainText = computed(() => {
  if (!quota.value) return '进入页面后自动查询当前用户配额'
  return `剩余 ${formatBytes(quota.value.remaining_storage_bytes)}`
})

const quotaBadgeText = computed(() => {
  if (quotaLoading.value) return '加载中'
  return `${quotaPercent.value}%`
})

const quotaBadgeVariant = computed(() => {
  if (quotaPercent.value >= 90) return 'error'
  if (quotaPercent.value >= 70) return 'warning'
  return 'success'
})

const quotaProgressClass = computed(() => {
  if (quotaPercent.value >= 90) return 'bg-red-500'
  if (quotaPercent.value >= 70) return 'bg-amber-500'
  return 'bg-accent-500'
})

// loadStorageQuota 自动读取当前用户存储配额
async function loadStorageQuota() {
  quotaLoading.value = true
  quotaError.value = ''
  try {
    const res = await getStorageQuota()
    quota.value = res.data
  } catch (error) {
    quotaError.value = error instanceof Error ? error.message : '读取存储配额失败'
  } finally {
    quotaLoading.value = false
  }
}

// loadKnowledgeBaseDetail 读取当前知识库真实名称
async function loadKnowledgeBaseDetail() {
  knowledgeBase.value = null
  knowledgeBaseError.value = ''
  if (!knowledgeBaseID.value) return
  knowledgeBaseLoading.value = true
  try {
    const res = await getKnowledgeBase(knowledgeBaseID.value)
    knowledgeBase.value = res.data
  } catch (error) {
    knowledgeBaseError.value = error instanceof Error ? error.message : '读取知识库详情失败'
  } finally {
    knowledgeBaseLoading.value = false
  }
}

// loadDocuments 读取当前知识库文档列表
async function loadDocuments(options: { silent?: boolean; skipPollingSync?: boolean } = {}) {
  if (!knowledgeBaseID.value) {
    documents.value = []
    stopDocumentPolling()
    return
  }
  if (!options.silent) {
    documentsLoading.value = true
    documentsError.value = ''
  }
  try {
    const res = await listDocuments(knowledgeBaseID.value)
    documents.value = res.data
  } catch (error) {
    if (!options.silent) {
      documentsError.value = error instanceof Error ? error.message : '读取文档列表失败'
    }
  } finally {
    if (!options.silent) {
      documentsLoading.value = false
    }
    if (!options.skipPollingSync) {
      syncDocumentPolling()
    }
  }
}

// syncDocumentPolling 根据文档处理状态启动或停止轮询
function syncDocumentPolling() {
  if (!knowledgeBaseID.value || !documents.value.some(item => isDocumentProcessing(item.status))) {
    stopDocumentPolling()
    return
  }
  startDocumentPolling()
}

// startDocumentPolling 定时刷新处理中状态
function startDocumentPolling() {
  if (documentPollingTimer) return
  documentPollingTimer = window.setTimeout(async () => {
    documentPollingTimer = null
    await loadDocuments({ silent: true, skipPollingSync: true })
    await refreshLastJob()
    if (jobsPanelOpen.value && selectedDocument.value) {
      const latest = documents.value.find(item => item.id === selectedDocument.value?.id)
      if (latest) selectedDocument.value = latest
      await loadHistory(selectedDocument.value)
    }
    syncDocumentPolling()
  }, documentPollingIntervalMs)
}

// stopDocumentPolling 停止文档状态轮询
function stopDocumentPolling() {
  if (!documentPollingTimer) return
  window.clearTimeout(documentPollingTimer)
  documentPollingTimer = null
}

// refreshLastJob 刷新上传提示中的最近任务状态
async function refreshLastJob() {
  if (!lastJob.value || !isJobProcessing(lastJob.value.status)) return
  try {
    const res = await getDocumentJob(lastJob.value.id)
    lastJob.value = res.data
  } catch {
    // 轮询失败不打断列表状态刷新
  }
}

// toggleSyncItemExpanded 切换钉钉目录展开状态
function toggleSyncItemExpanded(item: SyncItemTreeNode) {
  const next = new Set(expandedSyncItemIDs.value)
  if (next.has(item.external_id)) {
    next.delete(item.external_id)
  } else {
    next.add(item.external_id)
  }
  expandedSyncItemIDs.value = next
}

// toggleSyncItemSelection 切换节点及其全部子节点的勾选状态
function toggleSyncItemSelection(item: SyncItemTreeNode, checked: boolean | string | number) {
  const next = new Set(selectedSyncItemIDs.value)
  const descendants = [item]
  const shouldSelect = Boolean(checked)
  while (descendants.length) {
    const current = descendants.pop()
    if (!current) continue
    if (shouldSelect) {
      next.add(current.external_id)
    } else {
      next.delete(current.external_id)
    }
    descendants.push(...current.children)
  }

  const postOrder: SyncItemTreeNode[] = []
  const pending = [...syncItemTree.value]
  while (pending.length) {
    const current = pending.pop()
    if (!current) continue
    postOrder.push(current)
    pending.push(...current.children)
  }
  for (let index = postOrder.length - 1; index >= 0; index -= 1) {
    const current = postOrder[index]
    if (!current.children.length) continue
    if (current.children.every(child => next.has(child.external_id))) {
      next.add(current.external_id)
    } else {
      next.delete(current.external_id)
    }
  }
  selectedSyncItemIDs.value = next
}

// toggleAllSyncItems 切换全部钉钉节点的勾选状态
function toggleAllSyncItems(checked: boolean | string | number) {
  selectedSyncItemIDs.value = Boolean(checked)
    ? new Set(syncItems.value.map(item => item.external_id))
    : new Set()
}

// isSyncItemIndeterminate 判断目录是否处于部分勾选状态
function isSyncItemIndeterminate(item: SyncItemTreeNode) {
  if (!item.children.length || selectedSyncItemIDs.value.has(item.external_id)) return false
  const descendants = [...item.children]
  while (descendants.length) {
    const current = descendants.pop()
    if (!current) continue
    if (selectedSyncItemIDs.value.has(current.external_id)) return true
    descendants.push(...current.children)
  }
  return false
}

// loadSyncItems 读取当前知识库绑定的钉钉文件目录
async function loadSyncItems() {
  syncItems.value = []
  syncSourceID.value = ''
  syncItemsError.value = ''
  if (!knowledgeBaseID.value) return
  syncItemsLoading.value = true
  try {
    const sourceRes = await listSyncSources()
    const source = (sourceRes.data || []).find(item => {
      return item.knowledge_base_id === knowledgeBaseID.value && item.platform === 'dingtalk'
    })
    if (!source) {
      selectedSyncItemIDs.value = new Set()
      expandedSyncItemIDs.value = new Set()
      syncTreeSourceID.value = ''
      return
    }
    syncSourceID.value = source.id
    const itemRes = await listSyncItems(source.id)
    syncItems.value = itemRes.data || []
    const validIDs = new Set(syncItems.value.map(item => item.external_id))
    if (syncTreeSourceID.value !== source.id) {
      selectedSyncItemIDs.value = new Set()
      expandedSyncItemIDs.value = new Set(
        syncItemTree.value
          .filter(item => item.item_type === 'FOLDER' && item.children.length)
          .map(item => item.external_id),
      )
      syncTreeSourceID.value = source.id
    } else {
      selectedSyncItemIDs.value = new Set([...selectedSyncItemIDs.value].filter(id => validIDs.has(id)))
      expandedSyncItemIDs.value = new Set([...expandedSyncItemIDs.value].filter(id => validIDs.has(id)))
    }
  } catch (error) {
    syncItemsError.value = error instanceof Error ? error.message : '读取钉钉文件失败'
  } finally {
    syncItemsLoading.value = false
  }
}

// openFilePicker 打开文件选择器
function openFilePicker() {
  if (!knowledgeBaseID.value || uploading.value) return
  fileInputRef.value?.click()
}

// handleFileChange 处理文件选择上传
function handleFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (file) {
    uploadSelectedFile(file)
  }
  input.value = ''
}

// handleFileDrop 处理拖拽上传
function handleFileDrop(event: DragEvent) {
  if (!knowledgeBaseID.value || uploading.value) return
  const file = event.dataTransfer?.files?.[0]
  if (file) {
    uploadSelectedFile(file)
  }
}

// uploadSelectedFile 上传文件并刷新文档和配额
async function uploadSelectedFile(file: File) {
  if (!knowledgeBaseID.value) return
  uploading.value = true
  uploadError.value = ''
  lastJob.value = null
  try {
    const res = await uploadDocument(knowledgeBaseID.value, file)
    lastJob.value = res.data.job
    await Promise.all([loadDocuments(), loadStorageQuota(), loadSyncItems()])
  } catch (error) {
    const message = error instanceof Error ? error.message : '上传文档失败'
    uploadError.value = message.includes('存储配额') || message.includes('配额') ? '存储配额不足，请清理后再上传' : message
  } finally {
    uploading.value = false
  }
}

// retryProcess 手动重试失败文档处理
async function retryProcess(doc: Document) {
  retryingID.value = doc.id
  try {
    lastJob.value = await processDocument(doc.id).then(res => res.data)
    await Promise.all([loadDocuments(), loadHistory(doc)])
  } catch (error) {
    uploadError.value = error instanceof Error ? error.message : '重试处理失败'
  } finally {
    retryingID.value = ''
  }
}

// importDingTalkItem 导入钉钉文件到本地文档
async function importDingTalkItem(item: SyncItem) {
  importingItemID.value = item.id
  syncItemsError.value = ''
  try {
    const res = await importSyncItem(item.id)
    await Promise.all([loadDocuments(), loadStorageQuota(), loadSyncItems()])
    if (res.data.status !== documentStatusReady) {
      const message = res.data.error_message || '钉钉文件解析失败'
      syncItemsError.value = message
      ElMessage.error(`导入失败：${message}`)
      return
    }
    ElMessage.success('导入成功')
  } catch (error) {
    const message = error instanceof Error ? error.message : '导入钉钉文件失败'
    syncItemsError.value = message
    ElMessage.error(`导入失败：${message}`)
    await loadSyncItems()
  } finally {
    importingItemID.value = ''
  }
}

// openImportedDocument 打开已导入文档
function openImportedDocument(documentID: string) {
  router.push({ path: `/docs/${documentID}/edit`, query: { knowledge_base_id: knowledgeBaseID.value } })
}

// goToKnowledgeBases 跳转到知识库管理页面
function goToKnowledgeBases() {
  router.push({ path: '/kb' })
}

// editDocument 跳转到文档编辑页面
function editDocument(doc: Document) {
  activeMenuID.value = ''
  router.push({ path: `/docs/${doc.id}/edit`, query: { knowledge_base_id: knowledgeBaseID.value } })
}

// openPreviewFromMenu 从操作菜单打开原始文件预览
function openPreviewFromMenu(doc: Document) {
  activeMenuID.value = ''
  openPreview(doc)
}

// openPreview 加载并展示原始文件
async function openPreview(doc: Document) {
  closePreviewURL()
  clearPreviewContent()
  previewDocument.value = doc
  previewDialogOpen.value = true
  previewLoading.value = true
  previewError.value = ''
  try {
    const blob = await getDocumentPreview(doc.id)
    previewURL.value = URL.createObjectURL(blob)
    if (previewKind.value === 'docx') {
      previewLoading.value = false
      await nextTick()
      if (!docxPreviewContainer.value) throw new Error('DOCX 预览容器初始化失败')
      const { renderAsync } = await import('docx-preview')
      await renderAsync(blob, docxPreviewContainer.value, undefined, {
        className: 'docx-preview',
        inWrapper: true,
        breakPages: true,
      })
    } else if (['markdown', 'text', 'json', 'csv'].includes(previewKind.value)) {
      await renderTextPreview(blob, previewKind.value)
    }
    if (previewKind.value !== 'docx') previewLoading.value = false
  } catch (error) {
    previewError.value = error instanceof Error ? error.message : '读取原始文件失败'
    previewLoading.value = false
  }
}

// closePreview 关闭原始文件预览
function closePreview() {
  previewDialogOpen.value = false
  previewDocument.value = null
  previewError.value = ''
  clearPreviewContent()
  closePreviewURL()
}

// clearPreviewContent 清理文件预览内容
function clearPreviewContent() {
  if (docxPreviewContainer.value) docxPreviewContainer.value.innerHTML = ''
  previewHTML.value = ''
  previewText.value = ''
  previewTable.value = []
}

// renderTextPreview 按文本文件类型生成预览内容
async function renderTextPreview(blob: Blob, kind: 'markdown' | 'text' | 'json' | 'csv') {
  const content = await blob.text()
  if (kind === 'markdown') {
    const html = marked.parse(content, { breaks: true, gfm: true }) as string
    previewHTML.value = DOMPurify.sanitize(html)
    return
  }
  if (kind === 'text') {
    previewText.value = content
    return
  }
  if (kind === 'json') {
    const formatted = JSON.stringify(JSON.parse(content), null, 2)
    previewHTML.value = hljs.highlight(formatted, { language: 'json' }).value
    return
  }
  const result = Papa.parse<string[]>(content, { skipEmptyLines: 'greedy' })
  if (result.errors.length) throw new Error(`CSV 解析失败：${result.errors[0].message}`)
  previewTable.value = result.data
}

// closePreviewURL 释放预览文件地址
function closePreviewURL() {
  if (!previewURL.value) return
  URL.revokeObjectURL(previewURL.value)
  previewURL.value = ''
}

// toggleActionMenu 切换文档操作菜单
function toggleActionMenu(documentID: string) {
  activeMenuID.value = activeMenuID.value === documentID ? '' : documentID
}

// openJobsFromMenu 从操作菜单打开任务历史
function openJobsFromMenu(doc: Document) {
  activeMenuID.value = ''
  openJobs(doc)
}

// retryProcessFromMenu 从操作菜单手动重试处理
function retryProcessFromMenu(doc: Document) {
  activeMenuID.value = ''
  retryProcess(doc)
}

// openDeleteDialog 打开删除确认弹框
function openDeleteDialog(doc: Document) {
  activeMenuID.value = ''
  deletingDocument.value = doc
  deleteError.value = ''
  deleteDialogOpen.value = true
}

// closeDeleteDialog 关闭删除确认弹框
function closeDeleteDialog() {
  if (deletingID.value) return
  deleteDialogOpen.value = false
  deletingDocument.value = null
  deleteError.value = ''
}

// confirmDeleteDocument 确认删除文档
async function confirmDeleteDocument() {
  const doc = deletingDocument.value
  if (!doc) return
  deletingID.value = doc.id
  try {
    await deleteDocument(doc.id)
    deleteDialogOpen.value = false
    deletingDocument.value = null
    await Promise.all([loadDocuments(), loadStorageQuota()])
  } catch (error) {
    deleteError.value = error instanceof Error ? error.message : '删除文档失败'
  } finally {
    deletingID.value = ''
  }
}

// openJobs 打开文档处理任务历史
async function openJobs(doc: Document) {
  selectedDocument.value = doc
  jobsPanelOpen.value = true
  await loadHistory(doc)
}

// loadHistory 读取文档版本历史和处理任务历史
async function loadHistory(doc: Document) {
  await Promise.all([loadVersions(doc), loadJobs(doc)])
}

// loadVersions 读取文档版本历史
async function loadVersions(doc: Document) {
  versionsLoading.value = true
  versionsError.value = ''
  try {
    const res = await listDocumentVersions(doc.id)
    versions.value = res.data
  } catch (error) {
    versionsError.value = error instanceof Error ? error.message : '读取版本记录失败'
  } finally {
    versionsLoading.value = false
  }
}

// versionChangeSummary 规整版本变更说明展示
function versionChangeSummary(value: string) {
  return value.trim() || '未填写变更说明'
}

// loadJobs 读取文档处理任务历史
async function loadJobs(doc: Document) {
  jobsLoading.value = true
  jobsError.value = ''
  try {
    const res = await listDocumentJobs(doc.id)
    jobs.value = res.data
  } catch (error) {
    jobsError.value = error instanceof Error ? error.message : '读取任务记录失败'
  } finally {
    jobsLoading.value = false
  }
}

// closeJobs 关闭任务历史弹窗
function closeJobs() {
  jobsPanelOpen.value = false
  selectedDocument.value = null
  jobs.value = []
  versions.value = []
  jobsError.value = ''
  versionsError.value = ''
}

// documentStatusText 转换文档状态文案
function documentStatusText(status: number) {
  const statusMap: Record<number, string> = {
    1: '已上传',
    2: '处理中',
    3: '已就绪',
    4: '处理失败',
    5: '已删除',
  }
  return statusMap[status] || '未知状态'
}

// documentStatusVariant 转换文档状态标签样式
function documentStatusVariant(status: number): 'success' | 'warning' | 'error' | 'neutral' | 'blue' {
  if (status === 3) return 'success'
  if (status === 4) return 'error'
  if (status === 1 || status === 2) return 'warning'
  return 'neutral'
}

// isDocumentProcessing 判断文档是否仍在处理链路中
function isDocumentProcessing(status: number) {
  return status === documentStatusUploaded || status === documentStatusProcessing
}

// jobStatusText 转换任务状态文案
function jobStatusText(status: number) {
  const statusMap: Record<number, string> = {
    1: '待处理',
    2: '运行中',
    3: '成功',
    4: '失败',
  }
  return statusMap[status] || '未知状态'
}

// jobStatusVariant 转换任务状态标签样式
function jobStatusVariant(status: number): 'success' | 'warning' | 'error' | 'neutral' | 'blue' {
  if (status === 3) return 'success'
  if (status === 4) return 'error'
  if (status === 1 || status === 2) return 'warning'
  return 'neutral'
}

// isJobProcessing 判断处理任务是否仍未结束
function isJobProcessing(status: number) {
  return status === documentJobStatusPending || status === documentJobStatusRunning
}

// fileTypeText 规整文件类型展示
function fileTypeText(fileType: string) {
  return fileType ? fileType.toUpperCase() : '-'
}

// syncItemTypeText 规整钉钉文件类型展示
function syncItemTypeText(item: SyncItem) {
  if (item.item_type === 'FOLDER') return '目录'
  return (item.extension || item.category || item.item_type || '-').toUpperCase()
}

// syncItemIcon 根据钉钉节点类型返回本地图标
function syncItemIcon(item: SyncItem) {
  const extension = item.extension.toLowerCase()
  if (item.item_type === 'FOLDER') return { component: Folder, color: 'text-blue-500' }
  if (['axls', 'xls', 'xlsx', 'csv'].includes(extension)) return { component: Grid, color: 'text-emerald-500' }
  if (extension === 'dlink') return { component: Link, color: 'text-violet-500' }
  if (['png', 'jpg', 'jpeg', 'gif', 'webp'].includes(extension)) return { component: Picture, color: 'text-cyan-500' }
  if (extension === 'pdf') return { component: DocumentIcon, color: 'text-red-500' }
  if (['adoc', 'doc', 'docx', 'pptx', 'txt', 'md', 'markdown', 'html'].includes(extension)) {
    return { component: DocumentIcon, color: 'text-blue-500' }
  }
  return { component: Files, color: 'text-slate-400' }
}

// syncItemHint 返回钉钉文件辅助说明
function syncItemHint(item: SyncItem) {
  if (isAliSheetItem(item)) return '钉钉在线表格暂不支持自动导入'
  if (isAliDocItem(item)) return '钉钉在线文档，可导入本地知识库'
  if (item.item_type === 'FOLDER') return item.has_children ? '目录' : '目录或快捷方式'
  return item.category || ''
}

// canImportSyncItem 判断钉钉文件是否可导入
function canImportSyncItem(item: SyncItem) {
  if (item.item_type !== 'FILE') return false
  if (item.import_status === 2 || item.import_status === 3) return false
  return !isAliSheetItem(item)
}

// isAliDocItem 判断是否为钉钉在线文档
function isAliDocItem(item: SyncItem) {
  return item.category.toUpperCase() === 'ALIDOC' && item.extension.toLowerCase() === 'adoc'
}

// isAliSheetItem 判断是否为暂不支持导入的钉钉在线表格
function isAliSheetItem(item: SyncItem) {
  return item.category.toUpperCase() === 'ALIDOC' && item.extension.toLowerCase() === 'axls'
}

// syncImportStatusText 转换钉钉文件导入状态
function syncImportStatusText(status: number) {
  const statusMap: Record<number, string> = {
    1: '未导入',
    2: '导入中',
    3: '已导入',
    4: '导入失败',
  }
  return statusMap[status] || '未知状态'
}

// syncImportStatusVariant 转换钉钉文件导入状态标签样式
function syncImportStatusVariant(status: number): 'success' | 'warning' | 'error' | 'neutral' | 'blue' {
  if (status === 3) return 'success'
  if (status === 4) return 'error'
  if (status === 2) return 'warning'
  return 'neutral'
}

// shortID 缩短 ID 展示
function shortID(id: string) {
  return id ? id.slice(0, 8) : '-'
}

// formatBytes 格式化文件容量
function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let unitIndex = 0
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024
    unitIndex += 1
  }
  const digits = value >= 10 || unitIndex === 0 ? 0 : 1
  return `${value.toFixed(digits)} ${units[unitIndex]}`
}

// formatDate 格式化时间
function formatDate(value: string | null) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString('zh-CN', { hour12: false })
}

onMounted(() => {
  loadStorageQuota()
  loadKnowledgeBaseDetail()
  loadDocuments()
  loadSyncItems()
})

onUnmounted(() => {
  stopDocumentPolling()
  closePreviewURL()
})

watch(knowledgeBaseID, () => {
  stopDocumentPolling()
  uploadError.value = ''
  lastJob.value = null
  activeMenuID.value = ''
  selectedSyncItemIDs.value = new Set()
  expandedSyncItemIDs.value = new Set()
  syncTreeSourceID.value = ''
  loadKnowledgeBaseDetail()
  loadDocuments()
  loadSyncItems()
})
</script>

<style scoped>
.menu-item {
  display: block;
  width: 100%;
  border: 0;
  border-radius: 0.625rem;
  background: transparent;
  padding: 0.5rem 0.75rem;
  text-align: left;
  font-size: 0.875rem;
  color: #475569;
  cursor: pointer;
}

.menu-item:hover {
  background: #f8fafc;
  color: #0f172a;
}

.menu-item-danger {
  color: #ef4444;
}

.menu-item-danger:hover {
  background: #fef2f2;
  color: #dc2626;
}

.menu-item:disabled {
  cursor: not-allowed;
  opacity: 0.45;
}

.preview-text-panel {
  height: 100%;
  overflow: auto;
  border-radius: 0.75rem;
  background: #fff;
  padding: 1.5rem;
  color: #334155;
}

.preview-source {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.875rem;
  line-height: 1.7;
}

.preview-json :deep(.hljs-attr) {
  color: #0369a1;
}

.preview-json :deep(.hljs-string) {
  color: #047857;
}

.preview-json :deep(.hljs-number),
.preview-json :deep(.hljs-literal) {
  color: #7c3aed;
}

.preview-csv-table {
  min-width: 100%;
  border-collapse: collapse;
  font-size: 0.875rem;
}

.preview-csv-table th,
.preview-csv-table td {
  border: 1px solid #e2e8f0;
  padding: 0.625rem 0.75rem;
  text-align: left;
  white-space: pre-wrap;
}

.preview-csv-table th {
  position: sticky;
  top: 0;
  background: #f8fafc;
  color: #0f172a;
  font-weight: 600;
}

.sync-tree-toggle {
  display: inline-flex;
  width: 1.25rem;
  height: 1.25rem;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 0.25rem;
  background: transparent;
  color: #64748b;
  cursor: pointer;
}

.sync-tree-toggle:hover {
  background: #f1f5f9;
  color: #0f172a;
}

.sync-tree-toggle span {
  display: inline-block;
  font-size: 1.25rem;
  line-height: 1;
  transition: transform 150ms ease;
}
</style>
