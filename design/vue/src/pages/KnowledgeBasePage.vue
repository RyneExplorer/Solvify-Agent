<template>
  <div class="py-8 px-10">
    <div class="mb-6">
      <h1 class="text-[28px] font-bold text-slate-900 m-0" style="font-family: 'Space Grotesk', sans-serif;">知识库管理</h1>
      <p class="text-sm text-slate-400 mt-2">管理自建知识库，接入钉钉知识库同步</p>
    </div>

    <div class="grid grid-cols-4 gap-3 mb-5">
      <AppCard>
        <div class="text-xs text-slate-400 mb-1">知识库总数</div>
        <div class="text-2xl font-semibold text-slate-900">{{ knowledgeBases.length }}</div>
      </AppCard>
      <AppCard>
        <div class="text-xs text-slate-400 mb-1">自建知识库</div>
        <div class="text-2xl font-semibold text-slate-900">{{ localCount }}</div>
      </AppCard>
      <AppCard>
        <div class="text-xs text-slate-400 mb-1">文档总数</div>
        <div class="text-2xl font-semibold text-slate-900">{{ totalDocuments }}</div>
      </AppCard>
      <AppCard>
        <div class="text-xs text-slate-400 mb-1">存储占用</div>
        <div class="text-2xl font-semibold text-slate-900">{{ totalStorageText }}</div>
      </AppCard>
    </div>

    <div class="flex items-center justify-between gap-4 mb-5">
      <SearchInput v-model="searchQuery" placeholder="搜索知识库..." wrapper-class="w-full max-w-md" />
      <div class="flex gap-2">
        <AppButton variant="secondary" @click="syncDialogVisible = true">同步钉钉知识库</AppButton>
        <AppButton @click="openCreate">+ 新建知识库</AppButton>
      </div>
    </div>

    <el-alert
      v-if="errorMessage"
      class="mb-5"
      type="error"
      :title="errorMessage"
      :closable="false"
      show-icon
    />

    <AppCard v-if="loading" class="text-center text-sm text-slate-400 py-12">
      正在加载知识库...
    </AppCard>

    <AppCard v-else-if="filteredKnowledgeBases.length === 0" class="text-center py-12">
      <div class="text-base font-medium text-slate-900 mb-2">暂无知识库</div>
      <div class="text-sm text-slate-400 mb-4">创建一个知识库后，就可以上传文档并用于问答检索</div>
      <AppButton @click="openCreate">新建知识库</AppButton>
    </AppCard>

    <template v-else>
      <section v-if="selfKBs.length" class="mb-8">
        <div class="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-3">自建知识库</div>
        <div class="grid gap-3" style="grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));">
          <KBCard
            v-for="kb in selfKBs"
            :key="kb.id"
            :kb="kb"
            @edit="openEdit"
            @documents="openDocuments"
            @delete="confirmDelete"
          />
        </div>
      </section>

      <section class="mb-8">
        <div class="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-3">钉钉同步知识库</div>
        <AppCard v-if="dingtalkKBs.length === 0" class="text-center py-10">
          <div class="text-sm font-medium text-slate-700 mb-1">暂无钉钉同步知识库</div>
          <div class="text-xs text-slate-400 mb-4">绑定钉钉账号后，可以同步你有权限访问的钉钉知识库</div>
          <AppButton variant="secondary" size="sm" @click="syncDialogVisible = true">同步钉钉知识库</AppButton>
        </AppCard>
        <div v-else class="grid gap-3" style="grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));">
          <KBCard
            v-for="kb in dingtalkKBs"
            :key="kb.id"
            :kb="kb"
            :source-label="sourceLabels.dingtalk"
            @view="openDocuments"
            @sync="syncDialogVisible = true"
            @delete="confirmDelete"
          />
        </div>
      </section>

      <section v-if="webSearchKBs.length">
        <div class="text-xs font-semibold text-slate-400 uppercase tracking-wider mb-3">联网搜索知识库</div>
        <div class="grid gap-3" style="grid-template-columns: repeat(auto-fill, minmax(340px, 1fr));">
          <KBCard
            v-for="kb in webSearchKBs"
            :key="kb.id"
            :kb="kb"
            :source-label="sourceLabels.web_search"
            @view="openDocuments"
          >
            <div class="mt-2.5 px-3 py-2.5 bg-green-50 rounded-lg text-xs text-green-600">
              由联网搜索结果自动保存，存储计入个人配额
            </div>
          </KBCard>
        </div>
      </section>
    </template>

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="520px">
      <div class="space-y-4">
        <label class="block">
          <span class="block text-sm font-medium text-slate-700 mb-1.5">名称</span>
          <input
            v-model="form.name"
            class="w-full px-3 py-2.5 text-sm rounded-xl border border-slate-200 bg-slate-50 text-slate-900 outline-none focus:border-slate-900"
            placeholder="例如：产品知识库"
          />
        </label>
        <label class="block">
          <span class="block text-sm font-medium text-slate-700 mb-1.5">分类</span>
          <input
            v-model="form.category"
            class="w-full px-3 py-2.5 text-sm rounded-xl border border-slate-200 bg-slate-50 text-slate-900 outline-none focus:border-slate-900"
            placeholder="例如：产品、技术、客服"
          />
        </label>
        <label class="block">
          <span class="block text-sm font-medium text-slate-700 mb-1.5">描述</span>
          <textarea
            v-model="form.description"
            rows="4"
            class="w-full px-3 py-2.5 text-sm rounded-xl border border-slate-200 bg-slate-50 text-slate-900 outline-none focus:border-slate-900 resize-none"
            placeholder="简单说明这个知识库的用途"
          />
        </label>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <AppButton variant="secondary" :disabled="saving" @click="dialogVisible = false">取消</AppButton>
          <AppButton :disabled="saving" @click="submitForm">{{ saving ? '保存中...' : '保存' }}</AppButton>
        </div>
      </template>
    </el-dialog>

    <DingTalkSyncDialog v-model="syncDialogVisible" @synced="loadKnowledgeBases" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import DingTalkSyncDialog from '@/components/DingTalkSyncDialog.vue'
import AppButton from '@/components/ui/AppButton.vue'
import AppCard from '@/components/ui/AppCard.vue'
import SearchInput from '@/components/ui/SearchInput.vue'
import KBCard from '@/components/ui/KBCard.vue'
import {
  createKnowledgeBase,
  deleteKnowledgeBase,
  listKnowledgeBases,
  updateKnowledgeBase,
} from '@/api/knowledge'
import { deleteSyncSource, listSyncSources } from '@/api/sync'
import type { KnowledgeBase } from '@/types/knowledge'

const router = useRouter()
const loading = ref(false)
const saving = ref(false)
const errorMessage = ref('')
const searchQuery = ref('')
const dialogVisible = ref(false)
const syncDialogVisible = ref(false)
const dialogMode = ref<'create' | 'edit'>('create')
const editingID = ref('')
const knowledgeBases = ref<KnowledgeBase[]>([])

const form = reactive({
  name: '',
  category: '',
  description: '',
})

const sourceLabels = {
  dingtalk: { text: '钉钉同步', color: '#2563eb' },
  web_search: { text: '联网搜索', color: '#16a34a' },
}

const dialogTitle = computed(() => dialogMode.value === 'create' ? '新建知识库' : '编辑知识库')

const localCount = computed(() => knowledgeBases.value.filter(item => item.source_type === 'local').length)

const totalDocuments = computed(() => knowledgeBases.value.reduce((sum, item) => sum + item.document_count, 0))

const totalStorageText = computed(() => formatBytes(knowledgeBases.value.reduce((sum, item) => sum + item.storage_bytes, 0)))

const filteredKnowledgeBases = computed(() => {
  const keyword = searchQuery.value.trim().toLowerCase()
  return knowledgeBases.value.filter(item => {
    return !keyword || [item.name, item.category, item.description]
      .some(value => value.toLowerCase().includes(keyword))
  })
})

const selfKBs = computed(() => filteredKnowledgeBases.value.filter(item => item.source_type === 'local'))

const dingtalkKBs = computed(() => filteredKnowledgeBases.value.filter(item => item.source_type === 'sync' && sourceKey(item) === 'dingtalk'))

const webSearchKBs = computed(() => filteredKnowledgeBases.value.filter(item => sourceKey(item) === 'web_search'))

onMounted(loadKnowledgeBases)

// 加载知识库列表
async function loadKnowledgeBases() {
  loading.value = true
  errorMessage.value = ''
  try {
    const res = await listKnowledgeBases()
    knowledgeBases.value = res.data || []
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '知识库加载失败'
  } finally {
    loading.value = false
  }
}

// 打开创建弹窗
function openCreate() {
  dialogMode.value = 'create'
  editingID.value = ''
  form.name = ''
  form.category = ''
  form.description = ''
  dialogVisible.value = true
}

// 打开编辑弹窗
function openEdit(kb: KnowledgeBase) {
  dialogMode.value = 'edit'
  editingID.value = kb.id
  form.name = kb.name
  form.category = kb.category
  form.description = kb.description
  dialogVisible.value = true
}

// 保存知识库
async function submitForm() {
  const name = form.name.trim()
  if (!name) {
    ElMessage.warning('请输入知识库名称')
    return
  }

  saving.value = true
  const payload = {
    name,
    category: form.category.trim(),
    description: form.description.trim(),
  }
  try {
    if (dialogMode.value === 'edit') {
      await updateKnowledgeBase(editingID.value, payload)
      ElMessage.success('知识库已更新')
    } else {
      await createKnowledgeBase(payload)
      ElMessage.success('知识库已创建')
    }
    dialogVisible.value = false
    await loadKnowledgeBases()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : '保存失败')
  } finally {
    saving.value = false
  }
}

// 删除知识库
async function confirmDelete(kb: KnowledgeBase) {
  try {
    await ElMessageBox.confirm(`确认删除「${kb.name}」吗？`, '删除知识库', {
      confirmButtonText: '删除',
      cancelButtonText: '取消',
      type: 'warning',
    })
    if (kb.source_type === 'sync' && sourceKey(kb) === 'dingtalk') {
      await deleteDingTalkSource(kb.id)
    }
    await deleteKnowledgeBase(kb.id)
    ElMessage.success('知识库已删除')
    await loadKnowledgeBases()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') {
      ElMessage.error(err instanceof Error ? err.message : '删除失败')
    }
  }
}

// 删除钉钉同步源
async function deleteDingTalkSource(knowledgeBaseID: string) {
  const res = await listSyncSources()
  const source = (res.data || []).find(item => item.knowledge_base_id === knowledgeBaseID && item.platform === 'dingtalk')
  if (source) {
    await deleteSyncSource(source.id)
  }
}

// 打开文档页面
function openDocuments(kb: KnowledgeBase) {
  router.push({ path: '/docs', query: { knowledge_base_id: kb.id } })
}

// 获取来源标识
function sourceKey(kb: KnowledgeBase) {
  if (kb.source_platform) return kb.source_platform
  if (kb.source_type === 'local') return 'local'
  return kb.source_type || 'sync'
}

// 格式化存储大小
function formatBytes(bytes: number) {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index += 1
  }
  return `${value.toFixed(value >= 10 || index === 0 ? 0 : 1)} ${units[index]}`
}
</script>
