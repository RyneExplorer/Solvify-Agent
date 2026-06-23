import { createRouter, createWebHistory } from 'vue-router'
import { hasToken, removeToken } from '@/api/client'
import { getProfile } from '@/api/auth'
import { isAdmin, currentUser } from '@/composables/useAuth'

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
      path: '/search',
      name: 'search',
      component: () => import('@/pages/SearchPage.vue'),
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
      path: '/settings',
      name: 'settings',
      component: () => import('@/pages/SettingsPage.vue'),
      meta: { auth: true },
    },
    {
      path: '/profile',
      name: 'profile',
      component: () => import('@/pages/ProfilePage.vue'),
      meta: { auth: true },
    },
    {
      path: '/admin',
      name: 'admin',
      component: () => import('@/pages/AdminPage.vue'),
      meta: { auth: true, admin: true },
    },
  ],
})

// Auth guard
router.beforeEach(async (to, _from, next) => {
  const authenticated = hasToken()

  if (to.meta.auth && !authenticated) {
    next({ name: 'login', query: { redirect: to.fullPath } })
    return
  }
  if (to.meta.guest && authenticated) {
    next({ name: 'chat' })
    return
  }
  if (to.meta.admin) {
    // 如果用户信息未加载但持有 token，先拉取 profile 再判断权限
    if (authenticated && currentUser.value === null) {
      try {
        const res = await getProfile()
        if (res.code === 0 && res.data) {
          currentUser.value = res.data
        }
      } catch {
        removeToken()
        next({ name: 'login', query: { redirect: to.fullPath } })
        return
      }
    }
    if (!isAdmin.value) {
      next({ name: 'chat' })
      return
    }
  }
  next()
})

export default router
