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
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Sidebar from './components/Sidebar.vue'
import { listSessions } from './api/chat'
import { removeToken, hasToken } from './api/client'

interface HistoryItem { id: string; title: string }

const route = useRoute()
const router = useRouter()

const userName = ref('Admin')
const userEmail = ref('admin@solvify.ai')
const historyList = ref<HistoryItem[]>([])

const isLoginPage = computed(() => route.name === 'login')

function handleLogout() {
  removeToken()
  router.push('/login')
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

onMounted(() => { loadHistory() })
</script>
