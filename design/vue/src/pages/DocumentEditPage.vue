<template>
  <div class="min-h-screen bg-slate-50">
    <div class="bg-white border-b border-slate-200 px-8 py-5">
      <div class="flex items-center justify-between gap-4">
        <div class="flex items-start gap-4">
          <button class="back-button" type="button" @click="cancelEdit">←</button>
          <div>
            <h1 class="text-xl font-semibold text-slate-900 m-0">编辑文档</h1>
            <p class="text-sm text-slate-500 mt-1 mb-0">左侧编辑 Markdown，右侧实时预览效果</p>
          </div>
        </div>
        <div class="flex shrink-0 items-center gap-3">
          <AppButton variant="secondary" :disabled="saving" @click="cancelEdit">取消</AppButton>
          <AppButton :disabled="saving || !content.trim()" @click="saveVersion">
            {{ saving ? '保存中' : '保存修改' }}
          </AppButton>
        </div>
      </div>
    </div>

    <div v-if="loading" class="px-8 py-12 text-center text-sm text-slate-500">正在加载文档版本</div>
    <div v-else-if="loadError" class="px-8 py-12 text-center text-sm text-red-500">{{ loadError }}</div>
    <div v-else class="grid h-[calc(100vh-92px)] grid-cols-1 overflow-hidden xl:grid-cols-2">
      <section class="flex min-h-0 flex-col bg-white border-r border-slate-200">
        <div class="h-12 px-5 border-b border-slate-200 flex items-center justify-between text-sm text-slate-500">
          <span>Markdown 编辑器</span>
          <span>{{ content.length }} 字符</span>
        </div>
        <div class="flex min-h-0 flex-1 flex-col gap-2 p-3">
          <div>
            <label class="block text-xs font-medium text-slate-700 mb-0.5">文档标题</label>
            <input
              v-model="documentTitle"
              readonly
              class="w-full rounded-lg border border-slate-200 bg-slate-50 px-3 py-1.5 text-sm text-slate-700 outline-none"
            >
            <p class="text-[11px] text-slate-400 mt-0.5 mb-0">当前后端仅保存正文版本，标题来自文档记录</p>
          </div>

          <div>
            <label class="block text-xs font-medium text-slate-700 mb-0.5">变更说明</label>
            <input
              v-model="changeSummary"
              maxlength="120"
              placeholder="例如：修正文档正文、补充说明"
              class="w-full rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-sm text-slate-900 outline-none focus:border-slate-900"
            >
          </div>

          <div class="flex min-h-0 flex-1 flex-col">
            <label class="block text-sm font-medium text-slate-700 mb-1">文档内容</label>
            <textarea
              v-model="content"
              class="min-h-0 flex-1 w-full resize-none rounded-xl border border-slate-200 bg-white px-4 py-4 text-sm leading-7 text-slate-800 outline-none focus:border-slate-900"
              placeholder="请输入 Markdown 内容"
            />
          </div>

          <div v-if="saveError" class="rounded-xl bg-red-50 px-4 py-3 text-sm text-red-600">{{ saveError }}</div>
        </div>
      </section>

      <section class="flex min-h-0 flex-col bg-white">
        <div class="h-12 px-5 border-b border-slate-200 flex items-center text-sm text-slate-500">实时预览</div>
        <div class="min-h-0 flex-1 overflow-y-auto p-8">
          <article v-if="content.trim()" class="markdown-preview" v-html="previewHtml" />
          <div v-else class="text-sm text-slate-400">文档内容为空，右侧暂无预览</div>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { marked } from 'marked'
import { createDocumentVersion, getDocument, getDocumentVersion, listDocumentVersions } from '@/api/document'
import type { Document, DocumentVersionDetail } from '@/types/document'
import AppButton from '../components/ui/AppButton.vue'

const route = useRoute()
const router = useRouter()
const loading = ref(false)
const saving = ref(false)
const loadError = ref('')
const saveError = ref('')
const documentInfo = ref<Document | null>(null)
const latestVersion = ref<DocumentVersionDetail | null>(null)
const documentTitle = ref('')
const content = ref('')
const changeSummary = ref('')

const documentID = computed(() => {
  const value = route.params.id
  return typeof value === 'string' ? value : ''
})

const knowledgeBaseID = computed(() => {
  const value = route.query.knowledge_base_id
  return typeof value === 'string' ? value : ''
})

const previewHtml = computed(() => {
  return marked.parse(normalizePreviewContent(content.value), { breaks: true, gfm: true }) as string
})

// normalizeMarkdownContent 兼容处理被压平成一行的 Markdown 内容
function normalizeMarkdownContent(value: string) {
  return value
    .replace(/\r\n?/g, '\n')
    .replace(/[ \t]+$/gm, '')
    .replace(/\s*(```[\w-]*)\s*/g, '\n$1\n')
    .replace(/\s*```\s*/g, '\n```\n')
    .replace(/([^\n])\s+(#{1,6}\s+)/g, '$1\n\n$2')
    .replace(/([^\n])\s+(\d+\.\s+)/g, '$1\n$2')
    .replace(/([^\n])\s+([-*+]\s+)/g, '$1\n$2')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}

// normalizePreviewContent 格式化仅用于预览的特殊 Markdown 内容
function normalizePreviewContent(value: string) {
  const lines = value.split('\n')
  const normalizedLines: string[] = []
  let inCodeBlock = false

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index]
    const nextLine = lines[index + 1] || ''
    if (line.trim() === 'text' && isFlattenedTreeLine(nextLine)) {
      continue
    }
    if (line.trim().startsWith('```')) {
      normalizedLines.push(line)
      inCodeBlock = !inCodeBlock
      continue
    }
    if (isFlattenedTreeLine(line) && !inCodeBlock) {
      normalizedLines.push('```text')
      normalizedLines.push(formatFlattenedTreeLine(line))
      normalizedLines.push('```')
      continue
    }
    normalizedLines.push(formatFlattenedTreeLine(line))
  }

  return normalizedLines.join('\n')
}

// isFlattenedTreeLine 判断是否是被压平成单行的目录树
function isFlattenedTreeLine(value: string) {
  return /[├└]──/.test(value) && /\s\|\s/.test(value)
}

// formatFlattenedTreeLine 将压平目录树恢复为多行等宽树状文本
function formatFlattenedTreeLine(value: string) {
  if (!isFlattenedTreeLine(value)) return value

  const firstBranchIndex = value.search(/[├└]──/)
  if (firstBranchIndex <= 0) return value

  const rootName = value.slice(0, firstBranchIndex).trim()
  const branchText = value.slice(firstBranchIndex)
  const branchRegex = /((?:\|\s*)*)([├└]──)\s+([^├└|]+)/g
  const branches: Array<{ depth: number, connector: string, name: string }> = []
  let match: RegExpExecArray | null

  while ((match = branchRegex.exec(branchText)) !== null) {
    branches.push({
      depth: (match[1].match(/\|/g) || []).length,
      connector: match[2],
      name: match[3].trim(),
    })
  }

  if (!rootName || branches.length === 0) return value

  const treeLines = [`${rootName}/`]
  branches.forEach((branch, index) => {
    const isDirectory = (branches[index + 1]?.depth || 0) > branch.depth
    const itemName = isDirectory && !branch.name.endsWith('/') ? `${branch.name}/` : branch.name
    const prefix = branch.depth > 0 ? `${'│   '.repeat(branch.depth)}` : ''
    treeLines.push(`${prefix}${branch.connector} ${itemName}`)
  })

  return treeLines.join('\n')
}

// loadEditorData 加载文档和最新版本内容
async function loadEditorData() {
  if (!documentID.value) {
    loadError.value = '文档 ID 不能为空'
    return
  }
  loading.value = true
  loadError.value = ''
  try {
    const [documentRes, versionsRes] = await Promise.all([
      getDocument(documentID.value),
      listDocumentVersions(documentID.value),
    ])
    documentInfo.value = documentRes.data
    documentTitle.value = documentRes.data.title || documentRes.data.file_name

    const latest = [...versionsRes.data].sort((a, b) => b.version_no - a.version_no)[0]
    if (!latest) {
      loadError.value = '当前文档暂无可编辑版本'
      return
    }
    const versionRes = await getDocumentVersion(documentID.value, latest.id)
    latestVersion.value = versionRes.data
    content.value = normalizeMarkdownContent(versionRes.data.content)
    changeSummary.value = ''
  } catch (error) {
    loadError.value = error instanceof Error ? error.message : '加载文档版本失败'
  } finally {
    loading.value = false
  }
}

// saveVersion 保存新版本并返回文档列表
async function saveVersion() {
  if (!documentID.value || !content.value.trim()) return
  saving.value = true
  saveError.value = ''
  try {
    await createDocumentVersion(documentID.value, {
      content: content.value.trim(),
      change_summary: changeSummary.value.trim(),
    })
    backToDocuments()
  } catch (error) {
    saveError.value = error instanceof Error ? error.message : '保存文档版本失败'
  } finally {
    saving.value = false
  }
}

// cancelEdit 取消编辑并返回文档列表
function cancelEdit() {
  backToDocuments()
}

// backToDocuments 返回当前知识库文档列表
function backToDocuments() {
  router.push({ path: '/docs', query: knowledgeBaseID.value ? { knowledge_base_id: knowledgeBaseID.value } : {} })
}

onMounted(() => {
  loadEditorData()
})
</script>

<style scoped>
.back-button {
  display: inline-flex;
  width: 2.75rem;
  height: 2.75rem;
  align-items: center;
  justify-content: center;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: #475569;
  cursor: pointer;
  font-size: 1.5rem;
  line-height: 1;
}

.back-button:hover {
  background: #f1f5f9;
  color: #0f172a;
}

.markdown-preview {
  color: #334155;
  font-size: 0.9375rem;
  line-height: 1.75;
  word-break: break-word;
}

.markdown-preview :deep(h1),
.markdown-preview :deep(h2),
.markdown-preview :deep(h3),
.markdown-preview :deep(h4),
.markdown-preview :deep(h5),
.markdown-preview :deep(h6) {
  margin: 1.25rem 0 0.625rem;
  color: #0f172a;
  font-weight: 700;
  line-height: 1.45;
}

.markdown-preview :deep(h1) {
  font-size: 1.5rem;
}

.markdown-preview :deep(h2) {
  border-bottom: 1px solid #e2e8f0;
  padding-bottom: 0.25rem;
  font-size: 1.25rem;
}

.markdown-preview :deep(h3) {
  font-size: 1.0625rem;
}

.markdown-preview :deep(h4),
.markdown-preview :deep(h5),
.markdown-preview :deep(h6) {
  font-size: 0.9375rem;
}

.markdown-preview :deep(p) {
  margin: 0 0 0.875rem;
}

.markdown-preview :deep(ol),
.markdown-preview :deep(ul) {
  margin: 0 0 0.875rem 1.25rem;
  padding-left: 1rem;
}

.markdown-preview :deep(ol) {
  list-style: decimal;
}

.markdown-preview :deep(ul) {
  list-style: disc;
}

.markdown-preview :deep(li) {
  margin: 0.25rem 0;
}

.markdown-preview :deep(strong) {
  color: #0f172a;
}

.markdown-preview :deep(code) {
  border-radius: 0.375rem;
  background: #f1f5f9;
  padding: 0.125rem 0.375rem;
  color: #334155;
}

.markdown-preview :deep(pre) {
  margin: 0.875rem 0;
  overflow-x: auto;
  border-radius: 0.75rem;
  border: 1px solid #e2e8f0;
  background: #f8fafc;
  padding: 0.875rem 1rem;
}

.markdown-preview :deep(pre code) {
  display: block;
  min-width: max-content;
  background: transparent;
  padding: 0;
  color: #334155;
  font-size: 0.875rem;
  line-height: 1.75;
}

.markdown-preview :deep(blockquote) {
  margin: 0.875rem 0;
  border-left: 3px solid #cbd5e1;
  background: #f8fafc;
  padding: 0.625rem 0.875rem;
  color: #475569;
}

.markdown-preview :deep(table) {
  width: 100%;
  margin: 0.875rem 0;
  border-collapse: collapse;
  font-size: 0.875rem;
}

.markdown-preview :deep(th),
.markdown-preview :deep(td) {
  border: 1px solid #e2e8f0;
  padding: 0.5rem 0.625rem;
  text-align: left;
}

.markdown-preview :deep(th) {
  background: #f8fafc;
  color: #0f172a;
  font-weight: 600;
}

.markdown-preview :deep(hr) {
  margin: 1.25rem 0;
  border: 0;
  border-top: 1px solid #e2e8f0;
}
</style>
