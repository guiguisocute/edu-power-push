/* live 模式会话状态。access token 在 client.ts 内存中。
   本模块管：当前用户、登录态、启动无提示恢复。
   后端 User 映射为视图层 mock User 形状。
   booting = 试探会话，禁止发个人数据请求。
   anon = 未登录。signed-in = 已登录。mock 不用本模块。 */

import { useSyncExternalStore } from 'react'
import { api, restoreSession, setAccessToken, subscribeAccessToken, type OAuthProvider } from './client'
import { IS_LIVE } from './mode'
import type { User as ApiUser } from './types'
import type { User } from '../lib/mock'
import { formatPlace } from '../lib/mock'
import { maskAccount } from '../config/features'

export type SessionStatus = 'booting' | 'anon' | 'signed-in'

export interface SessionState {
  status: SessionStatus
  user: User | null
}

let state: SessionState = { status: IS_LIVE ? 'booting' : 'anon', user: null }
const subs = new Set<() => void>()

function emit(next: SessionState) {
  state = next
  subs.forEach((f) => f())
}

function subscribe(cb: () => void) {
  subs.add(cb)
  return () => {
    subs.delete(cb)
  }
}

subscribeAccessToken((token) => {
  if (token === null && state.status === 'signed-in') {
    emit({ status: 'anon', user: null })
  }
})

/** 个性化缓存必须按会话主体分区。匿名与不同账号禁止复用同一 key。 */
export function sessionCacheScope(session: SessionState): string {
  if (session.status === 'booting') return 'booting'
  if (session.status === 'signed-in') return `user:${session.user?.email || 'unknown'}`
  return 'anon'
}

/** 后端 User 映射为视图 User。
    place 仅展示。隐私预览必须用 building/floor/room。禁止反解析 place。 */
export function toViewUser(u: ApiUser): User {
  const name = u.nickname || u.email.split('@')[0]
  const m = u.meter
  return {
    name,
    initial: name.slice(0, 1),
    meter: m ? m.meter : null,
    place: m ? formatPlace(m.building, m.floor, m.room) : null,
    phone: maskAccount(u.email),
    email: u.email,
    // 角色跟随会话。降权后下一次取数落到普通用户。
    role: u.role || 'user',
    building: m ? m.building : null,
    floor: m ? m.floor : null,
    room: m ? m.room : null,
    createdAt: u.created_at ?? null,
    lastLoginAt: u.last_login_at ?? null,
    emailVerifiedAt: u.email_verified_at ?? null,
    activeSessions: u.active_sessions ?? null,
    boundAt: m ? (m.bound_at ?? null) : null,
  }
}

function adopt(u: ApiUser): User {
  const view = toViewUser(u)
  emit({ status: 'signed-in', user: view })
  return view
}

/** 启动时无提示恢复。无 refresh cookie 为正常未登录，不报错。
    全进程只执行一次，避免 StrictMode 重复请求。 */
let bootInFlight: Promise<User | null> | null = null

export function bootSession(): Promise<User | null> {
  if (!IS_LIVE) return Promise.resolve(null)
  if (!bootInFlight) {
    bootInFlight = restoreSession().then((tokens) => {
      if (!tokens) {
        emit({ status: 'anon', user: null })
        return null
      }
      return adopt(tokens.user)
    })
  }
  return bootInFlight
}

export async function signIn(email: string, password: string, captchaToken?: string): Promise<User> {
  const tokens = await api.login({ email: email.trim().toLowerCase(), password }, captchaToken)
  setAccessToken(tokens.access_token)
  return adopt(tokens.user)
}

export async function signUp(email: string, password: string, code: string, nickname?: string): Promise<User> {
  const tokens = await api.register({
    email: email.trim().toLowerCase(),
    password,
    code: code.trim(),
    nickname: nickname?.trim() || undefined,
  })
  setAccessToken(tokens.access_token)
  return adopt(tokens.user)
}

/*
第三方登录：整页跳转到提供方。无 Promise 可 await。
回跳后后端已种 refresh cookie。bootSession 恢复会话。
next 决定回跳页。后端仅接受本站相对路径。
*/
export function startOAuthSignIn(provider: OAuthProvider, next?: string): void {
  window.location.assign(api.oauthStartUrl(provider, next))
}

/** 登出。先吊销 refresh family。网络失败也必须清本地会话。 */
export async function signOut(): Promise<void> {
  try {
    await api.logout()
  } catch {
    /* 已过期或断网：本地仍清除 */
  }
  setAccessToken(null)
  emit({ status: 'anon', user: null })
}

/** 注销账号。后端删除后 cookie 已清。本地会话立刻落到未登录。 */
export async function deleteAccount(password: string): Promise<void> {
  await api.deleteAccount(password)
  setAccessToken(null)
  emit({ status: 'anon', user: null })
}

/** 绑表或换绑。成功后用后端 User 刷新会话。 */
export async function bindMeter(meter: string): Promise<User> {
  return adopt(await api.bindMeter(meter))
}

function sessionState(): SessionState {
  return state
}

export function useSession(): SessionState {
  return useSyncExternalStore(subscribe, sessionState, sessionState)
}
