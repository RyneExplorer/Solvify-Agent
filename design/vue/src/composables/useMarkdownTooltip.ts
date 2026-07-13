import { onMounted, onUnmounted } from 'vue'
import { marked } from 'marked'
import { getTooltipContent } from '@/composables/useChat'
import { getChunk } from '@/api/document'

// Cache for chunk content to avoid repeated requests
const chunkCache = new Map<string, {
  content: string
  documentTitle: string
  knowledgeBaseName: string
}>()


// Minimal normalization: ensure markdown block markers are on their own lines.
// This fixes agent-returned content that contains `##` or `-` markers but no newlines.
function normalizeMarkdown(text: string): string {
  if (!text) return ''
  return text
    // Ensure headings start on a new line
    .replace(/([^\n])(\s*#{1,6}\s)/g, '$1\n\n$2')
    // Ensure list items start on a new line
    .replace(/([^\n])(\s+-\s+)/g, '$1\n$2')
    .trim()
}

function buildTooltipMarkdown(
  content: string,
  documentTitle: string,
  knowledgeBaseName: string,
): string {
  const parts: string[] = ['## 引用内容']
  if (documentTitle) {
    parts.push(`**文章：${documentTitle}**`)
  }
  if (knowledgeBaseName) {
    parts.push(`*来自知识库：${knowledgeBaseName}*`)
  }
  if (content) {
    parts.push(normalizeMarkdown(content))
  }
  return parts.join('\n\n')
}

export function useMarkdownTooltip() {
  let tooltipEl: HTMLDivElement | null = null
  let currentTarget: HTMLElement | null = null
  let hideTimeout: number | null = null

  function cancelHide() {
    if (hideTimeout) {
      clearTimeout(hideTimeout)
      hideTimeout = null
    }
  }

  function scheduleHide() {
    cancelHide()
    hideTimeout = window.setTimeout(() => {
      hideTooltip()
    }, 150)
  }

  async function showTooltip(target: HTMLElement, md: string) {
    hideTooltip()
    cancelHide()
    const result = marked.parse(md, { breaks: true, gfm: true })
    let html = result instanceof Promise ? await result : String(result)
    // If mouse already left, don't show
    if (!target.matches(':hover')) return
    tooltipEl = document.createElement('div')
    tooltipEl.className = 'md-tooltip'
    tooltipEl.innerHTML = `<div class="md-tooltip-inner">${html}</div>`
    tooltipEl.addEventListener('mouseenter', onTooltipEnter)
    tooltipEl.addEventListener('mouseleave', onTooltipLeave)
    document.body.appendChild(tooltipEl)
    currentTarget = target
    positionTooltip(target, tooltipEl)
  }

  function hideTooltip() {
    cancelHide()
    if (tooltipEl) {
      tooltipEl.removeEventListener('mouseenter', onTooltipEnter)
      tooltipEl.removeEventListener('mouseleave', onTooltipLeave)
      tooltipEl.remove()
      tooltipEl = null
      currentTarget = null
    }
  }

  function onTooltipEnter() {
    cancelHide()
  }

  function onTooltipLeave() {
    scheduleHide()
  }

  function positionTooltip(target: HTMLElement, el: HTMLDivElement) {
    const rect = target.getBoundingClientRect()
    const maxWidth = Math.min(500, window.innerWidth - 32)
    el.style.maxWidth = `${maxWidth}px`
    const elRect = el.getBoundingClientRect()
    let left = rect.left + rect.width / 2 - elRect.width / 2
    let top = rect.top - elRect.height - 8
    if (left < 8) left = 8
    if (left + elRect.width > window.innerWidth - 8) left = window.innerWidth - elRect.width - 8
    if (top < 8) top = rect.bottom + 8
    el.style.left = `${left + window.scrollX}px`
    el.style.top = `${top + window.scrollY}px`
  }

  async function onMouseOver(e: MouseEvent) {
    const target = (e.target as HTMLElement).closest('[data-tip-key], [data-chunk-id], [data-chunk-ids]') as HTMLElement
    if (!target) return

    const chunkId = target.getAttribute('data-chunk-id')
    if (chunkId) {
      let cached = chunkCache.get(chunkId)
      if (!cached) {
        try {
          const res = await getChunk(chunkId)
          if (res.code === 0 && res.data) {
            cached = {
              content: res.data.content || '',
              documentTitle: res.data.document_title || '',
              knowledgeBaseName: res.data.knowledge_base_name || '',
            }
            chunkCache.set(chunkId, cached)
          }
        } catch {
          // ignore: fallback to data-doc below
        }
      }
      if (cached) {
        const md = buildTooltipMarkdown(cached.content, cached.documentTitle, cached.knowledgeBaseName)
        if (md) showTooltip(target, md)
        return
      }
      // Fallback: show document title hint if API fails
      const docTitle = target.getAttribute('data-doc')
      if (docTitle) {
        showTooltip(target, `**文章：${docTitle}**`)
      }
      return
    }

    const chunkIdsAttr = target.getAttribute('data-chunk-ids')
    if (chunkIdsAttr) {
      const ids = chunkIdsAttr.split(',').map(s => s.trim()).filter(Boolean)
      const results = await Promise.all(
        ids.map(async (id) => {
          let cached = chunkCache.get(id)
          if (!cached) {
            try {
              const res = await getChunk(id)
              if (res.code === 0 && res.data) {
                cached = {
                  content: res.data.content || '',
                  documentTitle: res.data.document_title || '',
                  knowledgeBaseName: res.data.knowledge_base_name || '',
                }
                chunkCache.set(id, cached)
              }
            } catch {
              // ignore
            }
          }
          return cached
        }),
      )
      const validResults = results.filter(Boolean) as Array<{
        content: string
        documentTitle: string
        knowledgeBaseName: string
      }>
      if (validResults.length > 0) {
        const content = validResults.map(r => r.content).join('\n\n---\n\n')
        const md = buildTooltipMarkdown(content, validResults[0].documentTitle, validResults[0].knowledgeBaseName)
        if (md) showTooltip(target, md)
        return
      }
      // Fallback: show document title hint if API fails
      const docTitle = target.getAttribute('data-doc')
      if (docTitle) {
        showTooltip(target, `**文章：${docTitle}**`)
      }
      return
    }

    const key = target.getAttribute('data-tip-key')
    if (key) {
      const md = getTooltipContent(key)
      if (md) showTooltip(target, md)
    }
  }

  function onMouseOut(e: MouseEvent) {
    const target = (e.target as HTMLElement).closest('[data-tip-key], [data-chunk-id], [data-chunk-ids]') as HTMLElement
    if (!target || target !== currentTarget) return
    const related = e.relatedTarget as HTMLElement
    // If moving to the tooltip itself, keep it visible
    if (tooltipEl && related && (related === tooltipEl || tooltipEl.contains(related))) return
    scheduleHide()
  }

  onMounted(() => {
    document.addEventListener('mouseover', onMouseOver)
    document.addEventListener('mouseout', onMouseOut)
  })

  onUnmounted(() => {
    document.removeEventListener('mouseover', onMouseOver)
    document.removeEventListener('mouseout', onMouseOut)
    hideTooltip()
  })
}
