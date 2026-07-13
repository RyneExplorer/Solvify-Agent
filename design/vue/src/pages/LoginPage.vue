<template>
  <div class="min-h-screen bg-slate-50 flex items-center justify-center px-4">
    <div class="w-full max-w-md">
      <!-- Logo -->
      <div class="text-center mb-8">
        <AppLogo size="md" class="mx-auto mb-3" />
        <h1 class="text-2xl font-bold text-slate-900" style="font-family: 'Space Grotesk', sans-serif;">Solvify</h1>
        <p class="text-sm text-slate-400 mt-1">企业知识库 AI 助手</p>
      </div>

      <!-- Card -->
      <div class="bg-white rounded-2xl border border-slate-200 shadow-sm p-6">
        <!-- Tab switch -->
        <div class="flex border-b border-slate-200 mb-5">
          <button @click="switchMode('login')" class="flex-1 pb-2.5 text-sm border-b-2 transition-colors"
            :class="formMode === 'login' ? 'text-slate-900 font-semibold border-accent-600' : 'text-slate-400 border-transparent'">登录</button>
          <button @click="switchMode('register')" class="flex-1 pb-2.5 text-sm border-b-2 transition-colors"
            :class="formMode === 'register' ? 'text-slate-900 font-semibold border-accent-600' : 'text-slate-400 border-transparent'">注册</button>
        </div>

        <!-- Login form -->
        <form v-if="formMode === 'login'" @submit.prevent="handleLogin">
          <div class="mb-3">
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">用户名</label>
            <input v-model="loginForm.username" placeholder="请输入用户名" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500" />
          </div>
          <div class="mb-3">
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">密码</label>
            <input v-model="loginForm.password" type="password" placeholder="请输入密码" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500" />
          </div>
          <div class="mb-3">
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">验证码</label>
            <div class="flex gap-2">
              <input v-model="loginForm.captcha" placeholder="4位验证码" maxlength="4" class="flex-1 rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500" />
              <div @click="loadCaptcha" class="h-10 w-[120px] rounded-xl border border-slate-200 bg-slate-50 flex items-center justify-center shrink-0 cursor-pointer overflow-hidden" title="点击刷新">
                <img v-if="captcha?.captcha" :src="captcha.captcha" class="h-full w-full object-cover" alt="验证码" />
                <span v-else class="text-xs text-slate-400">加载中...</span>
              </div>
            </div>
          </div>

          <div v-if="error" class="mb-3 text-xs text-red-500">{{ error }}</div>

          <button type="submit" :disabled="loading || !loginForm.username || !loginForm.password || !loginForm.captcha"
            class="w-full rounded-xl bg-accent-600 hover:bg-accent-700 disabled:bg-slate-300 text-white text-sm font-medium py-2.5 transition-colors">
            {{ loading ? '登录中...' : '登录' }}
          </button>
        </form>

        <!-- Register form -->
        <form v-if="formMode === 'register'" @submit.prevent="handleRegister">
          <div class="mb-3">
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">用户名</label>
            <input v-model="regForm.username" placeholder="3-50位字母数字" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500" />
          </div>
          <div class="mb-3">
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">邮箱</label>
            <input v-model="regForm.email" type="email" placeholder="仅支持QQ邮箱" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500" />
          </div>
          <div class="mb-3">
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">邮箱验证码</label>
            <div class="flex gap-2">
              <input v-model="regForm.emailCaptcha" placeholder="6位验证码" maxlength="6" class="flex-1 rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500" />
              <button type="button" @click="handleSendCode" :disabled="!regForm.email || sendingCode"
                class="shrink-0 px-4 py-2.5 rounded-xl bg-slate-100 hover:bg-slate-200 disabled:bg-slate-50 text-xs text-slate-600 font-medium transition-colors">
                {{ sendingCode ? '已发送' : '获取验证码' }}
              </button>
            </div>
          </div>
          <div class="mb-3">
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">密码</label>
            <input v-model="regForm.password" type="password" placeholder="至少6位" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500" />
          </div>
          <div class="mb-3">
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">确认密码</label>
            <input v-model="regForm.confirmPassword" type="password" placeholder="再次输入密码" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-accent-500" />
          </div>

          <div v-if="error" class="mb-3 text-xs text-red-500">{{ error }}</div>

          <button type="submit" :disabled="regLoading || !regForm.username || !regForm.email || !regForm.emailCaptcha || !regForm.password"
            class="w-full rounded-xl bg-accent-600 hover:bg-accent-700 disabled:bg-slate-300 text-white text-sm font-medium py-2.5 transition-colors">
            {{ regLoading ? '注册中...' : '注册' }}
          </button>
        </form>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { useAuth } from '@/composables/useAuth'
import AppLogo from '@/components/AppLogo.vue'

const {
  loading, error, captcha, formMode,
  loadCaptcha, login, register, sendEmailCode, switchMode,
} = useAuth()

const loginForm = reactive({ username: '', password: '', captcha: '' })
const regForm = reactive({ username: '', email: '', emailCaptcha: '', password: '', confirmPassword: '' })
const regLoading = ref(false)
const sendingCode = ref(false)

async function handleLogin() {
  await login(loginForm.username, loginForm.password, loginForm.captcha)
}

async function handleRegister() {
  if (regForm.password !== regForm.confirmPassword) {
    error.value = '两次密码不一致'
    return
  }
  regLoading.value = true
  const ok = await register(regForm.username, regForm.password, regForm.confirmPassword, regForm.email, regForm.emailCaptcha)
  regLoading.value = false
  if (ok) {
    regForm.username = ''; regForm.email = ''; regForm.emailCaptcha = ''; regForm.password = ''; regForm.confirmPassword = ''
  }
}

async function handleSendCode() {
  sendingCode.value = true
  await sendEmailCode(regForm.email)
  setTimeout(() => { sendingCode.value = false }, 30000)
}

onMounted(() => { loadCaptcha() })
</script>
