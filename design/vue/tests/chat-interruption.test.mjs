import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { transformWithEsbuild } from 'vite'
import { createSSRApp } from 'vue'
import { renderToString } from '@vue/server-renderer'

// 执行真实 composable，替换网络与路由边界，不把状态处理逻辑复制到测试。
let source = await readFile(new URL('../src/composables/useChat.ts', import.meta.url), 'utf8')
source = source
  .replace(/import \{ useRouter \} from 'vue-router'/, 'const useRouter = () => ({ push() {} })')
  .replace(/import \{ ElMessage \} from 'element-plus'/, 'const ElMessage = { warning() {}, error() {} }')
  .replace(/import \* as (\w+) from '@\/api\/[^']+'/g, (_, name) => `const ${name} = globalThis.__timeoutChatAPI`)
  .replace(/import \{ request \} from '@\/api\/client'/, 'const request = () => Promise.resolve({})')
  .replace("from './chatExecutionPlan'", `from '${new URL('../src/composables/chatExecutionPlan.ts', import.meta.url).href}'`)
  .replace("from 'vue'", `from '${import.meta.resolve('vue')}'`)
  .replace("from 'marked'", `from '${import.meta.resolve('marked')}'`)
const { code } = await transformWithEsbuild(source, 'useChat.ts', { loader: 'ts' })

for (const scenario of ['backend_error', 'disconnect', 'cancel', 'success', 'waiting_success']) {
  test(`聊天流 ${scenario} 保留正确正文和终态`, async (t) => {
    const originalNow = Date.now
    const originalInterval = globalThis.setInterval
    const originalClearInterval = globalThis.clearInterval
    let now = originalNow()
    let waitingTick
    let waitingCleared = false
    if (scenario === 'waiting_success') {
      Date.now = () => now
      globalThis.setInterval = callback => { waitingTick = callback; return 123 }
      globalThis.clearInterval = id => { if (id === 123) waitingCleared = true }
    }
    t.after(() => {
      Date.now = originalNow
      globalThis.setInterval = originalInterval
      globalThis.clearInterval = originalClearInterval
      delete globalThis.__timeoutChatAPI
    })
    const encoder = new TextEncoder()
    let reads = 0
    let canceled = false
    let released = false
    globalThis.__timeoutChatAPI = {
      async sendMessage(_session, _request, signal) {
        return {
          async read() {
            reads++
            if (reads === 1) return { done: false, value: encoder.encode('data: {"type":"start","message_id":"a1"}\n\ndata: {"type":"answer","content":"部分答案"}\n\n') }
            if (scenario === 'disconnect') throw new Error('连接已断开')
            if (scenario === 'cancel') throw new DOMException('停止生成', 'AbortError')
            if (scenario === 'waiting_success') {
              now += 16000
              waitingTick()
              assert.equal(chat.progressText.value, '仍在处理，请稍候…')
              now += 30000
              waitingTick()
              assert.equal(chat.progressText.value, '等待时间较长，你可以继续等待或停止生成')
              assert.equal(chat.isLoading.value, true, '等待提示不得结束执行')
            }
            const event = scenario.endsWith('success')
              ? { type: 'done', content: '完整答案', done: true }
              : { type: 'error', title: '模型响应超时', detail: '请稍后重试', done: true }
            return { done: false, value: encoder.encode(`data: ${JSON.stringify(event)}\n\n`) }
          },
          async cancel() { canceled = true },
          releaseLock() { released = true },
        }
      },
    }
    const { useChat } = await import(`data:text/javascript;base64,${Buffer.from(code + '\n// ' + scenario).toString('base64')}`)
    let chat
    const app = createSSRApp({ setup() { chat = useChat(); return () => null } })
    app.provide('refreshHistory', async () => {})
    await renderToString(app)
    chat.activeSessionId.value = 'session'
    chat.selectedModel.value = 'model'
    chat.input.value = '测试'
    await chat.sendMessage()
    const assistants = chat.messages.value.filter(message => message.role === 'assistant')
    assert.equal(assistants.length, 1)
    assert.equal(assistants[0].id, 'a1')
    if (scenario.endsWith('success')) {
      assert.equal(assistants[0].content, '完整答案')
    } else {
      assert.match(assistants[0].content, /部分答案/)
      assert.match(assistants[0].content, /回答已中断，内容不完整/)
    }
    if (scenario === 'backend_error' || scenario === 'disconnect') {
      assert.equal(chat.messages.value.filter(message => message.role === 'error').length, 1)
    }
    assert.equal(chat.isLoading.value, false)
    assert.equal(chat.streamContent.value, '')
    assert.ok(canceled && released, '退出时必须释放读取端')
    if (scenario === 'waiting_success') assert.ok(waitingCleared, '完成后必须清理等待提示计时器')
  })
}
