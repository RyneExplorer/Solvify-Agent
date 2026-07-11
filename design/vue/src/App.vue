<template>
  <!-- Login/Register: fullscreen, no sidebar -->
  <div v-if="isLoginPage" class="h-screen w-screen">
    <router-view />
  </div>

  <!-- App: sidebar + content -->
  <div v-else class="flex h-screen w-screen bg-white">
    <Sidebar :user-name="userName" :user-email="userEmail" :history-list="historyList" @logout="handleLogout" @refresh="loadHistory" />
    <div class="flex-1 flex flex-col overflow-auto">
      <router-view />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Sidebar from './components/Sidebar.vue'
import { listSessions } from './api/chat'
import { removeToken, hasToken } from './api/client'
import { useAuth } from './composables/useAuth'
import { ElMessageBox } from 'element-plus'

interface HistoryItem { id: string; title: string }

const route = useRoute()
const router = useRouter()
const { currentUser, initAuth } = useAuth()

const historyList = ref<HistoryItem[]>([])

const isLoginPage = computed(() => route.name === 'login')
const userName = computed(() => currentUser.value?.username || 'User')
const userEmail = computed(() => currentUser.value?.email || '')

async function handleLogout() {
  try {
    await ElMessageBox.confirm('确定要退出登录吗？', '提示', { confirmButtonText: '退出', cancelButtonText: '取消', type: 'warning' })
    removeToken()
    currentUser.value = null
    router.push('/login')
  } catch (e: any) {
    if (e === 'cancel' || e === 'close') return
  }
}

async function loadHistory() {
  if (!hasToken()) return
  try {
    const res = await listSessions()
    if (res.code === 0) {
      historyList.value = (res.data.sessions ?? []).map(s => ({
        id: s.id,
        title: s.title || '未命名对话',
      }))
    }
  } catch { /* silent */ }
}

// 登录成功后路由从 login 页跳出时，重新加载历史对话
watch(isLoginPage, async (newVal, oldVal) => {
  if (oldVal && !newVal && hasToken()) {
    await loadHistory()
  }
})

onMounted(async () => {
  await initAuth()
  await loadHistory()
})
</script>
