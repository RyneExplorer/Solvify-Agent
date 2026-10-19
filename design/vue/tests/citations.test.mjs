import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { transformWithEsbuild } from 'vite'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'

let source = await readFile(new URL('../src/composables/useChat.ts', import.meta.url), 'utf8')
source = source
  .replace(/import \{ useRouter \} from 'vue-router'/, 'const useRouter = () => ({ push() {} })')
  .replace(/import \{ ElMessage \} from 'element-plus'/, 'const ElMessage = { warning() {}, error() {} }')
  .replace(/import \* as (\w+) from '@\/api\/[^']+'/g, (_, name) => `const ${name} = {}`)
  .replace(/import \{ request \} from '@\/api\/client'/, 'const request = () => Promise.resolve({})')
  .replace("from './chatExecutionPlan'", `from '${new URL('../src/composables/chatExecutionPlan.ts', import.meta.url).href}'`)
  .replace("from 'vue'", `from '${import.meta.resolve('vue')}'`)
  .replace("from 'marked'", `from '${import.meta.resolve('marked')}'`)
const { code } = await transformWithEsbuild(source, 'useChat.ts', { loader: 'ts' })
const { useChat } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`)

async function chatForTest(t) {
  const previous = globalThis.document
  globalThis.document = { createElement() { return {
    textContent: '',
    get innerHTML() { return this.textContent.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;') },
  } } }
  t.after(() => { if (previous) globalThis.document = previous; else delete globalThis.document })
  let chat
  const app = createSSRApp({ setup() { chat = useChat(); return () => null } })
  app.provide('refreshHistory', async () => {})
  await renderToString(app)
  return chat
}

test('可信引用属性仅解码一次，保留标题和URL查询参数，兼容历史标签', async t => {
  const chat = await chatForTest(t)
  const web = '<web url="https://example.com?a=1&amp;b=2" title="Go &#34;规范&#34; &amp; 并发" />'
  const html = chat.formatContent('结论<kb doc="select &amp; channel" chunk_id="chunk-1" />' + web)
  assert.match(html, /class="inline-cite-kb" data-chunk-id="chunk-1" data-doc="select &amp; channel"/)
  assert.match(html, /📄select &amp; channel/)
  assert.match(html, /href="https:\/\/example.com\?a=1&amp;b=2"/)
  assert.doesNotMatch(html, /&amp;amp;/)
  assert.deepEqual(chat.extractWebSources(web + web), [{ url: 'https://example.com?a=1&b=2', title: 'Go "规范" & 并发' }])
})

test('行内、围栏、缩进和未闭合代码里的引用只显示为代码', async t => {
  const chat = await chatForTest(t)
  const kb = '<kb doc="代码示例" chunk_id="example" />'
  const web = '<web url="https://example.com" title="示例" />'
  for (const code of ['`' + kb + '`', '```html\n' + kb + '\n' + web + '\n```', '~~~html\n' + web + '\n~~~', '    ' + kb, '```html\n' + web]) {
    const html = chat.formatContent(code)
    assert.match(html, /<code/)
    assert.doesNotMatch(html, /class="inline-cite-/)
    assert.deepEqual(chat.extractWebSources(code), [])
  }
})

test('多个来源引用与相邻代码不会互相吞掉', async t => {
  const chat = await chatForTest(t)
  const html = chat.formatContent('`示例` <web url="https://example.com" title="来源一" />\n\n正文 <web url="https://example.org" title="来源二" />')
  assert.match(html, /<code>示例<\/code>/)
  assert.match(html, />\[1\]<\/a>/)
  assert.match(html, />\[2\]<\/a>/)
  const literal = chat.formatContent('`%%CITE0%%` <web url="https://example.com" title="来源一" />')
  assert.match(literal, /<code>%%CITE0%%<\/code>/)
  assert.equal((literal.match(/class="inline-cite-web"/g) ?? []).length, 1)
})
