/* API 客户端。面向 backend /api/v1（契约见 openapi.yaml）。
   access JWT 仅存内存，禁止写入 localStorage。
   refresh JWT 为 HttpOnly cookie；前端必须与后端同源。
   收到 401 时 POST /auth/refresh 并重试一次。再失败则清空会话。
   VITE_API_TOKEN 仅本机调试。产品构建禁止注入。 */

import type {
  CampusBills,
  CampusBreakdown,
  CampusScopes,
  CampusSummary,
  ChannelList,
  ChannelTestResult,
  Health,
  InventoryTree,
  LeaderboardPreference,
  MeterOverview,
  MeterPreview,
  MeterRefresh,
  MonthlyBill,
  NotificationChannel,
  NotificationSettings,
  PushLogPage,
  Ranking,
  Readiness,
  TimeSeries,
  DayNightSplit,
  TokenResponse,
  UnavailableCapability,
  User,
} from './types'

const BASE: string = import.meta.env.VITE_API_BASE || ''
const ADMIN_TOKEN: string = import.meta.env.VITE_API_TOKEN || ''

export type OAuthProvider = 'google' | 'github'
export type CaptchaProvider = 'disabled' | 'turnstile'
export type CaptchaAction = 'auth.login' | 'auth.register_code' | 'auth.password_reset_code' | 'meter.preview'

export interface CaptchaPublicConfig {
  provider: CaptchaProvider
  site_key: string
  actions: CaptchaAction[]
}

export class ApiError extends Error {
  code: string
  requestId: string
  status: number
  /** 429 的 Retry-After（秒）。前端据此对齐本地冷却。 */
  retryAfter?: number
  constructor(status: number, code: string, message: string, requestId: string, retryAfter?: number) {
    super(message)
    this.status = status
    this.code = code
    this.requestId = requestId
    this.retryAfter = retryAfter
  }
}

/* ---- access token（仅内存） ---- */

let accessToken: string | null = null
const accessTokenSubs = new Set<(token: string | null) => void>()

export function setAccessToken(token: string | null): void {
  accessToken = token
  accessTokenSubs.forEach((listener) => listener(token))
}

/** session.ts 订阅令牌失效。避免令牌与界面登录态分叉。 */
export function subscribeAccessToken(listener: (token: string | null) => void): () => void {
  accessTokenSubs.add(listener)
  return () => accessTokenSubs.delete(listener)
}

/* 全进程仅允许一次 refresh 在飞。refresh token 每次使用后轮换。
   启动恢复与 401 重试必须共用同一 promise。
   StrictMode 双跑 effect 时避免浪费一次轮换。 */
let refreshInFlight: Promise<TokenResponse | null> | null = null

/** 用 refresh cookie 换新令牌。无 cookie 或已过期返回 null。 */
export function rotateSession(): Promise<TokenResponse | null> {
  if (!refreshInFlight) {
    refreshInFlight = (async () => {
      try {
        const res = await fetch(`${BASE}/api/v1/auth/refresh`, {
          method: 'POST',
          credentials: 'same-origin',
        })
        if (!res.ok) {
          setAccessToken(null)
          return null
        }
        const body = (await res.json()) as TokenResponse
        setAccessToken(body.access_token)
        return body
      } catch {
        return null
      } finally {
        refreshInFlight = null
      }
    })()
  }
  return refreshInFlight
}

/** 启动时无提示恢复会话。与 401 重试共用同一次往返。 */
export const restoreSession = rotateSession

/* ---- 请求 ---- */

type Method = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'
type Params = Record<string, string | number | boolean | undefined>

interface Options {
  params?: Params
  body?: unknown
  /** session = 仅用户令牌。auto = 用户令牌优先，回退 ADMIN_TOKEN。none = 不带凭证。 */
  auth?: 'session' | 'auto' | 'none'
  /** 人机验证令牌。仅放请求头，禁止混入业务 JSON。 */
  captchaToken?: string
}

function query(params?: Params): string {
  if (!params) return ''
  const pairs = Object.entries(params).filter(([, v]) => v !== undefined && v !== '')
  if (!pairs.length) return ''
  return '?' + pairs.map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`).join('&')
}

async function toError(res: Response): Promise<ApiError> {
  let code = 'http_' + res.status
  let message = res.statusText
  let requestId = res.headers.get('X-Request-ID') || ''
  try {
    const body = await res.json()
    if (body?.error) {
      code = body.error.code
      message = body.error.message
      requestId = body.error.request_id || requestId
    }
  } catch {
    /* 非 JSON 错误体 */
  }
  const ra = Number(res.headers.get('Retry-After'))
  return new ApiError(res.status, code, message, requestId, Number.isFinite(ra) && ra > 0 ? ra : undefined)
}

async function send(method: Method, path: string, opts: Options, retry: boolean): Promise<Response> {
  const headers: Record<string, string> = {}
  const mode = opts.auth || 'auto'
  if (mode !== 'none') {
    if (accessToken) headers.Authorization = `Bearer ${accessToken}`
    else if (mode === 'auto' && ADMIN_TOKEN) headers.Authorization = `Bearer ${ADMIN_TOKEN}`
  }
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json'
  if (opts.captchaToken) headers['X-Captcha-Token'] = opts.captchaToken
  const res = await fetch(`${BASE}${path}${query(opts.params)}`, {
    method,
    headers,
    credentials: 'same-origin',
    body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
  })
  // 401 且带用户令牌：access token 过期，轮换后重试一次。
  if (res.status === 401 && retry && accessToken) {
    if (await rotateSession()) return send(method, path, opts, false)
  }
  return res
}

/* ---- 在途请求计数 ----
   顶部进度线依赖此计数。计数集中在此层，避免视图各自上报闪烁。
   /auth/refresh 不计数。会话自愈不触发加载线。 */
let inFlight = 0
const inFlightSubs = new Set<() => void>()

export function subscribeInFlight(cb: () => void): () => void {
  inFlightSubs.add(cb)
  return () => {
    inFlightSubs.delete(cb)
  }
}

export function inFlightCount(): number {
  return inFlight
}

function trackInFlight(delta: number) {
  inFlight = Math.max(0, inFlight + delta)
  inFlightSubs.forEach((f) => f())
}

/* 导出给管理面板（admin/api.ts）。直接复用请求原语。 */
export async function req<T>(method: Method, path: string, opts: Options = {}): Promise<T> {
  trackInFlight(1)
  try {
    const res = await send(method, path, opts, true)
    if (!res.ok) throw await toError(res)
    if (res.status === 204) return undefined as T
    return (await res.json()) as T
  } finally {
    trackInFlight(-1)
  }
}

const get = <T,>(path: string, params?: Params, auth?: Options['auth']) => req<T>('GET', path, { params, auth })

export const api = {
  healthLive: () => get<Health>('/health/live', undefined, 'none'),
  healthReady: () => get<Readiness>('/health/ready', undefined, 'none'),

  /** 运维面板下发的前端配置。公开端点。失败时调用方回退默认值。 */
  frontendConfig: () => get<unknown>('/api/v1/frontend-config', undefined, 'none'),

  captchaConfig: () => get<CaptchaPublicConfig>('/api/v1/captcha/config', undefined, 'none'),

  /* ---- 认证（不带令牌；refresh 走 cookie） ---- */

  /** 注册前向邮箱发验证码。后端强制要求邮箱归属验证。 */
  registerCode: (email: string, captchaToken?: string) =>
    req<void>('POST', '/api/v1/auth/register/code', { body: { email }, auth: 'none', captchaToken }),
  register: (body: { email: string; password: string; code: string; nickname?: string }) =>
    req<TokenResponse>('POST', '/api/v1/auth/register', { body, auth: 'none' }),
  login: (body: { email: string; password: string }, captchaToken?: string) =>
    req<TokenResponse>('POST', '/api/v1/auth/login', { body, auth: 'none', captchaToken }),
  logout: () => req<void>('POST', '/api/v1/auth/logout', { auth: 'none' }),

  /* 第三方登录必须整页跳转。授权码流程不能用 fetch。
     返回 start URL，由调用方 location.assign。
     回跳后后端已种 refresh cookie。SPA 用 /auth/refresh 换 access token。 */
  oauthStartUrl: (provider: OAuthProvider, next?: string) =>
    `${BASE}/api/v1/auth/oauth/${provider}/start${query({ next })}`,

  /** 找回密码发码。恒返回 204。不枚举账号。 */
  passwordForgot: (email: string, captchaToken?: string) =>
    req<void>('POST', '/api/v1/auth/password/forgot', { body: { email }, auth: 'none', captchaToken }),
  /** 用邮箱验证码重置密码。成功后吊销全部 refresh family。 */
  passwordReset: (body: { email: string; code: string; password: string }) =>
    req<void>('POST', '/api/v1/auth/password/reset', { body, auth: 'none' }),

  /* ---- 当前用户（access JWT） ---- */

  me: () => get<User>('/api/v1/me', undefined, 'session'),
  /** 更新昵称 */
  updateProfile: (body: { nickname: string }) =>
    req<User>('PUT', '/api/v1/me/profile', { body, auth: 'session' }),
  /** 更新密码。成功后全部会话吊销。前端应退出到登录。 */
  changePassword: (body: { current_password: string; new_password: string }) =>
    req<void>('PUT', '/api/v1/me/password', { body, auth: 'session' }),
  /** 登出全部设备（含本机）。不需要改密码。 */
  revokeSessions: () =>
    req<{ revoked: number }>('POST', '/api/v1/me/sessions/revoke', { auth: 'session' }),
  /** 换绑邮箱。向新邮箱发码。 */
  emailChangeCode: (email: string) =>
    req<void>('POST', '/api/v1/me/email/code', { body: { email }, auth: 'session' }),
  changeEmail: (body: { email: string; code: string; password: string }) =>
    req<User>('PUT', '/api/v1/me/email', { body, auth: 'session' }),
  /** 注销账号。不可逆。需要当前密码。成功后服务端已清 refresh cookie。 */
  deleteAccount: (password: string) =>
    req<void>('DELETE', '/api/v1/me', { body: { password }, auth: 'session' }),
  /** 绑表前查询宿舍位置。仅返回位置与可绑定状态。禁止含余额。 */
  meterPreview: (meter: string, captchaToken?: string) =>
    req<MeterPreview>('GET', '/api/v1/me/meter/preview', {
      params: { meter }, auth: 'session', captchaToken,
    }),
  bindMeter: (meter: string) => req<User>('PUT', '/api/v1/me/meter', { body: { meter }, auth: 'session' }),

  /** 榜单参与与脱敏。隐私开关必须服务端生效。 */
  leaderboardPreference: () => get<LeaderboardPreference>('/api/v1/me/leaderboard', undefined, 'session'),
  saveLeaderboardPreference: (body: Partial<LeaderboardPreference>) =>
    req<LeaderboardPreference>('PUT', '/api/v1/me/leaderboard', { body, auth: 'session' }),

  /** 推送渠道、规则与记录 */
  channels: () => get<ChannelList>('/api/v1/me/channels', undefined, 'session'),
  saveChannels: (body: { channels: { channel: string; enabled: boolean; config: Record<string, unknown> }[] }) =>
    req<ChannelList>('PUT', '/api/v1/me/channels', { body, auth: 'session' }),
  saveChannel: (channel: string, body: { enabled: boolean; config: Record<string, unknown> }) =>
    req<NotificationChannel>('PUT', `/api/v1/me/channels/${encodeURIComponent(channel)}`, {
      body,
      auth: 'session',
    }),
  /** 附加推送收件邮箱必须先通过邮箱归属验证。 */
  mailRecipientCode: (email: string) =>
    req<void>('POST', '/api/v1/me/channels/mail/recipient/code', { body: { email }, auth: 'session' }),
  verifyMailRecipient: (email: string, code: string) =>
    req<{ email: string }>('POST', '/api/v1/me/channels/mail/recipient/verify', {
      body: { email, code },
      auth: 'session',
    }),
  testChannel: (channel: string) =>
    req<ChannelTestResult>('POST', `/api/v1/me/channels/${encodeURIComponent(channel)}/test`, {
      auth: 'session',
    }),
  notificationSettings: () => get<NotificationSettings>('/api/v1/me/notification-settings', undefined, 'session'),
  saveNotificationSettings: (body: NotificationSettings) =>
    req<NotificationSettings>('PUT', '/api/v1/me/notification-settings', { body, auth: 'session' }),
  /** cursor 原样来自上一页 next_cursor。服务端视为不透明串。禁止前端自算。 */
  pushLogs: (q?: { limit?: number; cursor?: string }) => get<PushLogPage>('/api/v1/me/push-logs', q, 'session'),

  meOverview: () => get<MeterOverview>('/api/v1/me/overview', undefined, 'session'),
  meSeries: (q: {
    from: string
    to: string
    granularity: 'day' | 'week' | 'month'
    metric: 'balance' | 'consumption'
  }) => get<TimeSeries>('/api/v1/me/series', q, 'session'),
  meBills: (q?: { from_month?: string; to_month?: string }) =>
    get<MonthlyBill[]>('/api/v1/me/bills', q, 'session'),
  /** 日/夜用电拆分。拆不出时用 availability 说明。禁止返回假数。 */
  meDayNight: (q: { from: string; to: string }) =>
    get<DayNightSplit>('/api/v1/me/day-night', q, 'session'),

  /** 单表即时刷新。后端向上游拉实时读数。服务端按表 30 秒冷却。 */
  meRefresh: () => req<MeterRefresh>('POST', '/api/v1/me/refresh', { auth: 'session' }),

  /* ---- 校园聚合（匿名可读；带 JWT 时 rankings 额外下发 self） ---- */

  campusScopes: () => get<CampusScopes>('/api/v1/campus/scopes'),

  campusSummary: (q: { from: string; to: string; building?: string; floor?: string }) =>
    get<CampusSummary>('/api/v1/campus/summary', q),

  campusSeries: (q: {
    from: string
    to: string
    granularity: 'day' | 'week' | 'month'
    building?: string
    floor?: string
  }) => get<TimeSeries>('/api/v1/campus/series', q),

  /* 楼栋（或该栋楼层）× 时间用电矩阵。
     用电构成用行合计。热力图用格子。
     source=monthly_bill 时走月账单矩阵，使用 from_month/to_month。 */
  campusBreakdown: (q: {
    from?: string
    to?: string
    granularity?: 'day' | 'week' | 'month'
    building?: string
    source?: 'monthly_bill'
    from_month?: string
    to_month?: string
  }) => get<CampusBreakdown>('/api/v1/campus/breakdown', q),

  /* 全校月度用量。上游官方账单为权威值。
     campusSeries 走读数差值。本接口走月账单。冲突时以账单为准。 */
  campusBills: (q?: { from_month?: string; to_month?: string; building?: string }) =>
    get<CampusBills>('/api/v1/campus/bills', q),

  campusRankings: (q: {
    period: 'day' | 'week' | 'month'
    mode: 'usage' | 'saving' | 'surge' | 'drop'
    building?: string
    floor?: string
    limit?: number
  }) => get<Ranking>('/api/v1/campus/rankings', q),

  hourlyHeatmap: () => get<UnavailableCapability>('/api/v1/campus/hourly-heatmap'),

  /* ---- 运维端（仅本机调试注入 ADMIN_TOKEN 时可用） ---- */

  meterOverview: (meter: string) => get<MeterOverview>(`/api/v1/meters/${encodeURIComponent(meter)}/overview`),
  inventoryTree: (includeExcluded = false) =>
    get<InventoryTree>('/api/v1/inventory/tree', { include_excluded: includeExcluded }),
}
