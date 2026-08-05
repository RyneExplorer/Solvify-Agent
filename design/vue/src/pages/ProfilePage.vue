<template>
  <div class="py-6 px-4 md:px-6 max-w-xl mx-auto">
    <!-- Header avatar -->
    <div class="flex flex-col items-center mb-6">
      <div class="relative">
        <div class="w-20 h-20 rounded-full bg-accent-600 text-white flex items-center justify-center text-2xl font-medium ring-4 ring-white shadow-lg">
          {{ userInitial }}
        </div>
        <div class="absolute bottom-0 right-0 w-6 h-6 rounded-full bg-emerald-500 border-2 border-white flex items-center justify-center">
          <svg class="w-3.5 h-3.5 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2.5" d="M5 13l4 4L19 7"/></svg>
        </div>
      </div>
      <div class="mt-4 text-center">
        <div class="text-lg font-semibold text-slate-900">{{ profile?.username }}</div>
        <div class="text-xs text-slate-400 mt-0.5">{{ profile?.email }}</div>
      </div>
    </div>

    <!-- Personal info card -->
    <section class="bg-white border border-slate-200 rounded-2xl shadow-sm overflow-hidden mb-6">
      <div class="px-5 py-4 border-b border-slate-100 flex items-center gap-3">
        <div class="w-9 h-9 rounded-xl bg-emerald-50 text-emerald-600 flex items-center justify-center">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"/></svg>
        </div>
        <div>
          <h2 class="text-sm font-semibold text-slate-900">个人资料</h2>
          <p class="text-xs text-slate-400">查看您的基本信息</p>
        </div>
      </div>

      <div class="divide-y divide-slate-100">
        <!-- Username -->
        <div class="px-5 py-4 flex items-center justify-between">
          <div class="flex items-center gap-3 text-sm text-slate-500">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"/></svg>
            <span>昵称</span>
          </div>
          <div class="text-sm text-slate-900">{{ profile?.username }}</div>
        </div>

        <!-- Email -->
        <div class="px-5 py-4">
          <div class="flex items-center justify-between mb-2">
            <div class="flex items-center gap-3 text-sm text-slate-500">
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M3 8l7.89 5.26a2 2 0 002.22 0L21 8M5 19h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z"/></svg>
              <span>邮箱</span>
            </div>
          </div>
          <input
            v-model="email"
            type="email"
            class="w-full px-3 py-2 text-sm rounded-lg border border-slate-200 bg-slate-50 outline-none focus:border-accent-500 transition-colors"
            placeholder="请输入邮箱"
          />
          <div class="mt-2 flex justify-end">
            <AppButton size="sm" @click="handleSaveProfile" :loading="savingProfile">保存修改</AppButton>
          </div>
        </div>
      </div>
    </section>

    <!-- Account details card -->
    <section class="bg-white border border-slate-200 rounded-2xl shadow-sm overflow-hidden mb-6">
      <div class="px-5 py-4 border-b border-slate-100 flex items-center gap-3">
        <div class="w-9 h-9 rounded-xl bg-blue-50 text-blue-600 flex items-center justify-center">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"/></svg>
        </div>
        <div>
          <h2 class="text-sm font-semibold text-slate-900">账号详情</h2>
          <p class="text-xs text-slate-400">查看您的账号信息</p>
        </div>
      </div>

      <div class="divide-y divide-slate-100">
        <!-- User ID -->
        <div class="px-5 py-4 flex items-center justify-between">
          <div class="flex items-center gap-3 text-sm text-slate-500">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M7 20l4-16m2 16l4-16M6 9h14M4 15h14"/></svg>
            <span>用户 ID</span>
          </div>
          <div class="flex items-center gap-2">
            <code class="px-2 py-1 text-xs bg-slate-100 text-slate-700 rounded-md">{{ profile?.id || '-' }}</code>
            <button
              v-if="profile?.id"
              @click="copyUserId"
              class="p-1 hover:bg-slate-100 rounded-md transition-colors"
              title="复制"
            >
              <svg class="w-3.5 h-3.5 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z"/></svg>
            </button>
          </div>
        </div>

        <!-- Role -->
        <div class="px-5 py-4 flex items-center justify-between">
          <div class="flex items-center gap-3 text-sm text-slate-500">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z"/></svg>
            <span>角色</span>
          </div>
          <div class="text-sm text-slate-900">{{ roleText }}</div>
        </div>

        <!-- Created -->
        <div class="px-5 py-4 flex items-center justify-between">
          <div class="flex items-center gap-3 text-sm text-slate-500">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M8 7V3m8 4V3m-9 8h10M5 21h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z"/></svg>
            <span>注册时间</span>
          </div>
          <div class="text-sm text-slate-900">{{ profile?.created_at ? formatDate(profile.created_at) : '-' }}</div>
        </div>
      </div>
    </section>

    <!-- Change Password card -->
    <section class="bg-white border border-slate-200 rounded-2xl shadow-sm overflow-hidden">
      <div class="px-5 py-4 border-b border-slate-100 flex items-center gap-3">
        <div class="w-9 h-9 rounded-xl bg-amber-50 text-amber-600 flex items-center justify-center">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z"/></svg>
        </div>
        <div>
          <h2 class="text-sm font-semibold text-slate-900">安全</h2>
          <p class="text-xs text-slate-400">修改您的登录密码</p>
        </div>
      </div>

      <div class="px-5 py-5 space-y-4">
        <div>
          <label class="block text-xs font-medium text-slate-500 mb-1.5">当前密码</label>
          <input
            v-model="oldPassword"
            type="password"
            class="w-full px-3 py-2 text-sm rounded-lg border border-slate-200 bg-slate-50 outline-none focus:border-accent-500 transition-colors"
            placeholder="请输入当前密码"
          />
        </div>
        <div>
          <label class="block text-xs font-medium text-slate-500 mb-1.5">新密码</label>
          <input
            v-model="newPassword"
            type="password"
            class="w-full px-3 py-2 text-sm rounded-lg border border-slate-200 bg-slate-50 outline-none focus:border-accent-500 transition-colors"
            placeholder="请输入新密码（至少6位）"
          />
        </div>
        <div>
          <label class="block text-xs font-medium text-slate-500 mb-1.5">确认新密码</label>
          <input
            v-model="confirmPassword"
            type="password"
            class="w-full px-3 py-2 text-sm rounded-lg border border-slate-200 bg-slate-50 outline-none focus:border-accent-500 transition-colors"
            placeholder="请再次输入新密码"
          />
        </div>
        <div class="pt-1">
          <AppButton @click="handleChangePassword" :loading="changingPassword">修改密码</AppButton>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { getProfile, updateProfile, changePassword } from '@/api/auth'
import { currentUser } from '@/composables/useAuth'
import type { UserInfo } from '@/types/auth'
import AppButton from '@/components/ui/AppButton.vue'

const router = useRouter()

const profile = ref<UserInfo | null>(null)
const email = ref('')
const oldPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const savingProfile = ref(false)
const changingPassword = ref(false)

const userInitial = computed(() => {
  const name = profile.value?.username || ''
  return name ? name.charAt(0).toUpperCase() : '?'
})

const roleText = computed(() => {
  const role = profile.value?.role
  return role === 2 ? '管理员' : '普通用户'
})

function formatDate(dateStr: string) {
  return new Date(dateStr).toLocaleDateString('zh-CN')
}

async function loadProfile() {
  try {
    const res = await getProfile()
    // 后端 /user/profile 返回 { user: UserInfo, preference: {...} }，只取 user 这层扁平结构
    const userInfo = res.data?.user ?? res.data
    if (res.code === 0 && userInfo) {
      profile.value = userInfo
      email.value = userInfo.email || ''
      currentUser.value = userInfo
    }
  } catch (e: any) {
    ElMessage.error(e.message || '加载个人资料失败')
  }
}

async function handleSaveProfile() {
  if (!email.value.trim()) {
    ElMessage.warning('请输入邮箱')
    return
  }
  savingProfile.value = true
  try {
    const res = await updateProfile({ email: email.value.trim() })
    if (res.code === 0) {
      ElMessage.success('保存成功')
      await loadProfile()
    }
  } catch (e: any) {
    ElMessage.error(e.message || '保存失败')
  } finally {
    savingProfile.value = false
  }
}

async function handleChangePassword() {
  if (!oldPassword.value) {
    ElMessage.warning('请输入当前密码')
    return
  }
  if (!newPassword.value || newPassword.value.length < 6) {
    ElMessage.warning('新密码至少6位')
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    ElMessage.warning('两次输入的密码不一致')
    return
  }
  changingPassword.value = true
  try {
    const res = await changePassword({
      old_password: oldPassword.value,
      new_password: newPassword.value,
    })
    if (res.code === 0) {
      ElMessage.success('密码修改成功，请重新登录')
      oldPassword.value = ''
      newPassword.value = ''
      confirmPassword.value = ''
      localStorage.removeItem('solvify_token')
      router.push('/login')
    }
  } catch (e: any) {
    ElMessage.error(e.message || '修改密码失败')
  } finally {
    changingPassword.value = false
  }
}

function copyUserId() {
  if (!profile.value?.id) return
  navigator.clipboard.writeText(profile.value.id).then(() => {
    ElMessage.success('用户 ID 已复制')
  }).catch(() => {
    ElMessage.error('复制失败')
  })
}

onMounted(loadProfile)
</script>
