<template>
  <div class="flex h-screen w-screen bg-white">
    <Sidebar
      :history-list="historyList"
      :active-item="activeItem"
      :active-page="activePage"
      :user-name="userName"
      :user-email="userEmail"
      @select-item="handleSelectItem"
      @new-chat="handleNewChat"
      @navigate="handleNavigate"
    />

    <!-- Page content area -->
    <div class="flex-1 flex flex-col overflow-auto">
      <DashboardPage v-if="activePage === 'dashboard'" :user-name="userName" />
      <QAPage v-else-if="activePage === 'qa' && activeItem" :title="activeItem" />
      <ChatHome v-else-if="activePage === 'qa'" />
      <KnowledgeBasePage v-else-if="activePage === 'kb'" />
      <DocumentsPage v-else-if="activePage === 'docs'" />
      <WikiPage v-else-if="activePage === 'wiki'" />
      <SettingsPage v-else-if="activePage === 'settings'" />
      <AdminPage v-else-if="activePage === 'admin'" />
      <ChatHome v-else />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import Sidebar from './components/Sidebar.vue'
import ChatHome from './components/ChatHome.vue'
import QAPage from './pages/QAPage.vue'
import DashboardPage from './pages/DashboardPage.vue'
import KnowledgeBasePage from './pages/KnowledgeBasePage.vue'
import DocumentsPage from './pages/DocumentsPage.vue'
import WikiPage from './pages/WikiPage.vue'
import SettingsPage from './pages/SettingsPage.vue'
import AdminPage from './pages/AdminPage.vue'

const activeItem = ref('')
const activePage = ref('new-chat')

const userName = ref('Admin')
const userEmail = ref('admin@solvify.ai')

const historyList = ref([
  'Go语言简介',
  'Go语言基础概念解析',
  'Go语言简介与特点',
  '今日天气查询',
  '用户问题意图分析',
  '孙家旺个人简介',
  '智答云集项目需求概述',
  '微信客服会话',
  '智能问答平台',
  '知识库存储位置查询',
  '常用英语词汇列表',
  '欢迎咨询',
  'Go语言简介与特性解析',
  '孙家旺个人介绍',
  '微信客服会话',
])

const handleSelectItem = (item: string) => {
  activeItem.value = item
  activePage.value = 'qa'
}

const handleNewChat = () => {
  activeItem.value = ''
  activePage.value = 'new-chat'
}

const handleNavigate = (page: string) => {
  activePage.value = page
  activeItem.value = ''
}
</script>
