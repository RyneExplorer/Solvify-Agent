import { request } from './client'
import type {
  LoginRequest,
  LoginResponseData,
  RegisterRequest,
  CaptchaData,
  SendEmailCodeRequest,
  RefreshTokenRequest,
  RefreshTokenResponse,
  UserInfo,
} from '@/types/auth'

/** 登录 */
export function login(data: LoginRequest) {
  return request<LoginResponseData>('/auth/login', { method: 'POST', body: data, isPublic: true })
}

/** 注册 */
export function register(data: RegisterRequest) {
  return request<null>('/auth/register', { method: 'POST', body: data, isPublic: true })
}

/** 获取图形验证码 */
export function getCaptcha() {
  return request<CaptchaData>('/auth/captcha', { isPublic: true })
}

/** 发送邮箱验证码 */
export function sendEmailCode(data: SendEmailCodeRequest) {
  return request<null>('/auth/email/code', { method: 'POST', body: data, isPublic: true })
}

/** 刷新 Token */
export function refreshToken(data: RefreshTokenRequest) {
  return request<RefreshTokenResponse>('/auth/refresh', { method: 'POST', body: data, isPublic: true })
}

/** 登出 */
export function logout() {
  return request<null>('/auth/logout', { method: 'POST' })
}

/** 获取当前用户信息 */
export function getProfile() {
  return request<UserInfo>('/user/profile')
}
