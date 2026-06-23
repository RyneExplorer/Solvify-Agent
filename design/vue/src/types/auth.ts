// ── Login ──

export interface LoginRequest {
  username: string
  password: string
  captcha_id: string
  captcha: string
}

export interface LoginResponseData {
  token: string
  user: UserInfo
}

export interface UserInfo {
  id: string
  username: string
  email: string
  avatar: string
  status: number
  role: number
  lastModel?: string
  created_at: string
  updated_at: string
}

// ── Admin Users ──

export interface UpdateProfileRequest {
  avatar?: string
  email?: string
}

export interface ChangePasswordRequest {
  old_password: string
  new_password: string
}

export interface AdminUser {
  id: string
  username: string
  email: string
  avatar: string
  role: number
  status: number
  created_at: string
  updated_at: string
}

// ── Register ──

export interface RegisterRequest {
  username: string
  password: string
  confirm_password: string
  email: string
  captcha: string
}

// ── Captcha ──

export interface CaptchaData {
  captcha_id: string
  captcha: string // base64 image
}

// ── Email Code ──

export interface SendEmailCodeRequest {
  email: string
}

// ── Token ──

export interface RefreshTokenRequest {
  token: string
}

export interface RefreshTokenResponse {
  token: string
}
