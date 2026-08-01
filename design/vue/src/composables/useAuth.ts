import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import * as authApi from '@/api/auth'
import { setToken, removeToken, hasToken } from '@/api/client'
import type { UserInfo, CaptchaData } from '@/types/auth'

// ── Global auth state (singleton) ──
export const currentUser = ref<UserInfo | null>(null)
const isAuthenticated = computed(() => hasToken() && currentUser.value !== null)
export const isAdmin = computed(() => currentUser.value?.role === 2)

export function useAuth() {
  const router = useRouter()
  const loading = ref(false)
  const error = ref('')
  const captcha = ref<CaptchaData | null>(null)
  const formMode = ref<'login' | 'register'>('login')

  // ── Load captcha ──
  async function loadCaptcha() {
    try {
      const res = await authApi.getCaptcha()
      if (res.code === 0 && res.data) {
        // 后端返回的 base64 不含 data URI 前缀，需要补上
        captcha.value = {
          ...res.data,
          captcha: res.data.captcha.startsWith('data:')
            ? res.data.captcha
            : `data:image/png;base64,${res.data.captcha}`,
        }
      }
    } catch {
      error.value = '获取验证码失败'
    }
  }

  // ── Login ──
  async function login(username: string, password: string, captchaInput: string) {
    if (!captcha.value) {
      error.value = '请先获取验证码'
      return false
    }

    loading.value = true
    error.value = ''

    try {
      const res = await authApi.login({
        username,
        password,
        captcha_id: captcha.value.captcha_id,
        captcha: captchaInput,
      })

      if (res.code === 0 && res.data) {
        setToken(res.data.token)
        currentUser.value = res.data.user
        router.push('/chat')
        return true
      } else {
        error.value = res.message || '登录失败'
        await loadCaptcha() // refresh captcha on failure
        return false
      }
    } catch (e: unknown) {
      error.value = e instanceof Error ? e.message : '登录失败'
      await loadCaptcha()
      return false
    } finally {
      loading.value = false
    }
  }

  // ── Register ──
  async function register(
    username: string,
    password: string,
    confirmPassword: string,
    email: string,
    emailCaptcha: string,
  ) {
    loading.value = true
    error.value = ''

    try {
      const res = await authApi.register({
        username,
        password,
        confirm_password: confirmPassword,
        email,
        captcha: emailCaptcha,
      })

      if (res.code === 0) {
        // Registration successful — switch to login
        formMode.value = 'login'
        error.value = ''
        await loadCaptcha()
        return true
      } else {
        error.value = res.message || '注册失败'
        return false
      }
    } catch (e: unknown) {
      error.value = e instanceof Error ? e.message : '注册失败'
      return false
    } finally {
      loading.value = false
    }
  }

  // ── Send email code ──
  async function sendEmailCode(email: string) {
    try {
      const res = await authApi.sendEmailCode({ email })
      if (res.code === 0) {
        return true
      } else {
        error.value = res.message || '发送失败'
        return false
      }
    } catch (e: unknown) {
      error.value = e instanceof Error ? e.message : '发送验证码失败'
      return false
    }
  }

  // ── Logout ──
  async function logout() {
    try {
      await authApi.logout()
    } catch {
      // ignore logout API errors
    }
    removeToken()
    currentUser.value = null
    router.push('/login')
  }

  // ── Init — check if already logged in ──
  async function initAuth() {
    if (!hasToken()) return
    try {
      const res = await authApi.getProfile()
      // 后端 /user/profile 返回 { user: UserInfo, preference: {...} }，只取 user 这层扁平结构
      const userInfo = res.data?.user ?? res.data
      if (res.code === 0 && userInfo) {
        currentUser.value = userInfo
      }
    } catch {
      removeToken()
      currentUser.value = null
    }
  }

  function switchMode(mode: 'login' | 'register') {
    formMode.value = mode
    error.value = ''
    if (mode === 'login') {
      loadCaptcha()
    }
  }

  return {
    // state
    currentUser,
    isAuthenticated,
    isAdmin,
    loading,
    error,
    captcha,
    formMode,
    // methods
    loadCaptcha,
    login,
    register,
    sendEmailCode,
    logout,
    initAuth,
    switchMode,
  }
}
