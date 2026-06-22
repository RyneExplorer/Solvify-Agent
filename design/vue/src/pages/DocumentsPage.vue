<template>
  <div class="py-8 px-10">
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
      <div class="text-[13px] text-slate-400 mt-2">支持 PDF/Word/Txt/Markdown/HTML/CSV/Excel/PPT/JSON/图片，单文件最大 100MB</div>
      <p v-if="uploadError" class="text-xs text-red-500 mt-3 mb-0">{{ uploadError }}</p>
      <p v-if="lastJob" class="text-xs text-slate-500 mt-3 mb-0">
        已创建处理任务：{{ shortID(lastJob.id) }}，当前状态 {{ jobStatusText(lastJob.status) }}
      </p>
    </div>

    <AppCard class="!p-0 overflow-visible">
      <div v-if="documentsLoading" class="px-4 py-10 text-center text-sm text-slate-500">正在加载文档列表</div>
      <div v-else-if="documentsError" class="px-4 py-10 text-center text-sm text-red-500">{{ documentsError }}</div>
      <div v-else-if="!knowledgeBaseID" class="px-4 py-10 text-center text-sm text-slate-500">请先从知识库页面进入文档管理</div>
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
                <AppButton variant="ghost" size="sm" class="!px-2" @click="toggleActionMenu(doc.id)">...</AppButton>
                <div
                  v-if="activeMenuID === doc.id"
                  class="absolute right-0 top-9 z-50 w-44 rounded-xl border border-slate-100 bg-white p-1.5 shadow-xl"
                >
                  <button class="menu-item" @click="editDocument(doc)">编辑文档</button>
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
                  <button class="menu-item" @click="openJobsFromMenu(doc)">任务历史</button>
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
        <AppButton variant="secondary" size="sm" :disabled="syncItemsLoading" @click="loadSyncItems">
          {{ syncItemsLoading ? '刷新中' : '刷新列表' }}
        </AppButton>
      </div>
      <div v-if="syncItemsLoading" class="px-4 py-10 text-center text-sm text-slate-500">正在加载钉钉文件</div>
      <div v-else-if="syncItemsError" class="px-4 py-10 text-center text-sm text-red-500">{{ syncItemsError }}</div>
      <div v-else-if="syncItems.length === 0" class="px-4 py-10 text-center text-sm text-slate-500">暂无钉钉文件，请先刷新目录</div>
      <table v-else class="w-full text-sm border-collapse">
        <thead>
          <tr class="bg-slate-50 border-b border-slate-200">
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">名称</th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">类型</th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">大小</th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">导入状态</th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">更新时间</th>
            <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="item in syncItems" :key="item.id" class="border-b border-slate-100 last:border-b-0">
            <td class="px-4 py-3">
              <div class="font-medium text-slate-900">{{ item.name }}</div>
              <div v-if="syncItemHint(item)" class="text-xs text-slate-400 mt-1">{{ syncItemHint(item) }}</div>
              <div v-if="item.error_message" class="text-xs text-red-500 mt-1">{{ item.error_message }}</div>
            </td>
            <td class="px-4 py-3 text-slate-900">{{ syncItemTypeText(item) }}</td>
            <td class="px-4 py-3 text-slate-900">{{ formatBytes(item.file_size) }}</td>
            <td class="px-4 py-3">
              <AppBadge :variant="syncImportStatusVariant(item.import_status)">
                {{ syncImportStatusText(item.import_status) }}
              </AppBadge>
            </td>
            <td class="px-4 py-3 text-slate-900">{{ formatDate(item.source_updated_at) }}</td>
            <td class="px-4 py-3">
              <div class="flex flex-wrap gap-2">
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
            <h3 class="text-base font-semibold text-slate-900 m-0">处理任务历史</h3>
            <p class="text-xs text-slate-400 mt-1 mb-0">{{ selectedDocument?.file_name }}</p>
          </div>
          <AppButton variant="ghost" size="sm" @click="closeJobs">关闭</AppButton>
        </div>
        <div class="p-5">
          <div v-if="jobsLoading" class="text-sm text-slate-500">正在加载任务记录</div>
          <div v-else-if="jobsError" class="text-sm text-red-500">{{ jobsError }}</div>
          <div v-else-if="jobs.length === 0" class="text-sm text-slate-500">暂无任务记录</div>
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
        </div>
      </div>
    </div>

    <div v-if="deleteDialogOpen" class="fixed inset-0 bg-slate-900/30 flex items-center justify-center px-4 z-50" @click.self="closeDeleteDialog">
      <div class="bg-white rounded-2xl shadow-xl w-full max-w-md overflow-hidden">
        <div class="px-5 py-4 border-b border-slate-100">
          <h3 class="text-base font-semibold text-slate-900 m-0">删除文档</h3>
          <p class="text-sm text-slate-500 mt-2 mb-0">确认删除「{{ deletingDocument?.file_name || deletingDocument?.title }}」吗？删除后文档会进入软删除状态。</p>
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
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { deleteDocument, listDocumentJobs, listDocuments, processDocument, uploadDocument } from '@/api/document'
import { getKnowledgeBase } from '@/api/knowledge'
import { getStorageQuota } from '@/api/storage'
import { importSyncItem, listSyncItems, listSyncSources } from '@/api/sync'
import type { Document, DocumentProcessingJob } from '@/types/document'
import type { KnowledgeBase } from '@/types/knowledge'
import type { StorageQuota } from '@/types/storage'
import type { SyncItem } from '@/types/sync'
import AppCard from '../components/ui/AppCard.vue'
import AppButton from '../components/ui/AppButton.vue'
import AppBadge from '../components/ui/AppBadge.vue'

const documentStatusFailed = 4

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
const selectedDocument = ref<Document | null>(null)
const activeMenuID = ref('')
const deleteDialogOpen = ref(false)
const deletingDocument = ref<Document | null>(null)
const deleteError = ref('')
const knowledgeBase = ref<KnowledgeBase | null>(null)
const knowledgeBaseLoading = ref(false)
const knowledgeBaseError = ref('')
const syncItems = ref<SyncItem[]>([])
const syncItemsLoading = ref(false)
const syncItemsError = ref('')
const syncSourceID = ref('')
const importingItemID = ref('')

const knowledgeBaseID = computed(() => {
  const value = route.query.knowledge_base_id
  return typeof value === 'string' ? value : ''
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
async function loadDocuments() {
  if (!knowledgeBaseID.value) {
    documents.value = []
    return
  }
  documentsLoading.value = true
  documentsError.value = ''
  try {
    const res = await listDocuments(knowledgeBaseID.value)
    documents.value = res.data
  } catch (error) {
    documentsError.value = error instanceof Error ? error.message : '读取文档列表失败'
  } finally {
    documentsLoading.value = false
  }
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
    if (!source) return
    syncSourceID.value = source.id
    const itemRes = await listSyncItems(source.id)
    syncItems.value = itemRes.data || []
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
    await Promise.all([loadDocuments(), loadStorageQuota()])
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
    await Promise.all([loadDocuments(), loadJobs(doc)])
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
    await importSyncItem(item.id)
    await Promise.all([loadDocuments(), loadStorageQuota(), loadSyncItems()])
  } catch (error) {
    syncItemsError.value = error instanceof Error ? error.message : '导入钉钉文件失败'
    await loadSyncItems()
  } finally {
    importingItemID.value = ''
  }
}

// openImportedDocument 打开已导入文档
function openImportedDocument(documentID: string) {
  router.push({ path: `/docs/${documentID}/edit`, query: { knowledge_base_id: knowledgeBaseID.value } })
}

// editDocument 跳转到文档编辑页面
function editDocument(doc: Document) {
  activeMenuID.value = ''
  router.push({ path: `/docs/${doc.id}/edit`, query: { knowledge_base_id: knowledgeBaseID.value } })
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

// confirmDeleteDocument 确认软删除文档
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
  await loadJobs(doc)
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

// fileTypeText 规整文件类型展示
function fileTypeText(fileType: string) {
  return fileType ? fileType.toUpperCase() : '-'
}

// syncItemTypeText 规整钉钉文件类型展示
function syncItemTypeText(item: SyncItem) {
  if (item.item_type === 'FOLDER') return '目录'
  return (item.extension || item.category || item.item_type || '-').toUpperCase()
}

// syncItemHint 返回钉钉文件辅助说明
function syncItemHint(item: SyncItem) {
  if (isUnsupportedAliDocItem(item)) return '钉钉在线文档暂不支持自动导入'
  if (item.item_type === 'FOLDER') return item.has_children ? '目录' : '目录或快捷方式'
  return item.category || ''
}

// canImportSyncItem 判断钉钉文件是否可导入
function canImportSyncItem(item: SyncItem) {
  if (item.item_type !== 'FILE') return false
  if (item.import_status === 2 || item.import_status === 3) return false
  return !isUnsupportedAliDocItem(item)
}

// isUnsupportedAliDocItem 判断是否为暂不支持自动导入的钉钉在线文档
function isUnsupportedAliDocItem(item: SyncItem) {
  return item.category === 'ALIDOC' && ['adoc', 'axls'].includes(item.extension)
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

watch(knowledgeBaseID, () => {
  uploadError.value = ''
  lastJob.value = null
  activeMenuID.value = ''
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
</style>
