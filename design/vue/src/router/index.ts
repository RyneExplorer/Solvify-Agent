import { createRouter, createWebHistory } from 'vue-router'
import { hasToken } from '@/api/client'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/chat' },
    {
      path: '/login',
      name: 'login',
      component: () => import('@/pages/LoginPage.vue'),
      meta: { guest: true },
    },
    {
      path: '/chat',
      name: 'chat',
      component: () => import('@/pages/ChatPage.vue'),
      meta: { auth: true },
    },
    {
      path: '/chat/:sessionId',
      name: 'chat-session',
      component: () => import('@/pages/ChatPage.vue'),
      meta: { auth: true },
    },
    {
      path: '/dashboard',
      name: 'dashboard',
      component: () => import('@/pages/DashboardPage.vue'),
      meta: { auth: true },
    },
    {
      path: '/kb',
      name: 'kb',
      component: () => import('@/pages/KnowledgeBasePage.vue'),
      meta: { auth: true },
    },
    {
      path: '/docs',
      name: 'docs',
      component: () => import('@/pages/DocumentsPage.vue'),
      meta: { auth: true },
    },
    {
      path: '/wiki',
      name: 'wiki',
      component: () => import('@/pages/WikiPage.vue'),
      meta: { auth: true },
    },
    {
      path: '/settings',
      name: 'settings',
      component: () => import('@/pages/SettingsPage.vue'),
      meta: { auth: true },
    },
    {
      path: '/admin',
      name: 'admin',
      component: () => import('@/pages/AdminPage.vue'),
      meta: { auth: true },
    },
  ],
})

// Auth guard
router.beforeEach((to, _from, next) => {
  const authenticated = hasToken()

  if (to.meta.auth && !authenticated) {
    next({ name: 'login', query: { redirect: to.fullPath } })
  } else if (to.meta.guest && authenticated) {
    next({ name: 'chat' })
  } else {
    next()
  }
})

export default router
