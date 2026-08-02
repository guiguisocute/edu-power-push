/* 管理界面取数层。
   与用户端共用会话（access token + refresh cookie）。
   权限由后端按角色现查。禁止使用 ADMIN_TOKEN。
   ADMIN_TOKEN 仅供 CI 与迁移脚本，禁止进入浏览器。 */

import { api, req } from '../api/client'
import type { CaptchaAction, CaptchaProvider } from '../api/client'
import type { Role } from '../lib/adminAccess'

export type AdminRole = Role

export interface AdminActor {
  user_id: string
  label: string
  role: AdminRole
  via_token: boolean
}

export interface AdminSession {
  actor: AdminActor
  environment: 'development' | 'test' | 'production'
  version: string
  /** false = 服务端未配置 SETTINGS_ENCRYPTION_KEY，凭证存不了。 */
  secrets_ready: boolean
  admin_count: number
}

export interface AdminOverview {
  inventory: { inventory_total: number; excluded_total: number; eligible_total: number }
  latest_run: unknown
  unacknowledged_anomalies: number
  unacknowledged_critical_anomalies: number
  unacknowledged_bill_revisions: number
  version: string
  database: string
  migrations: string
  worker: string
  worker_heartbeat_at?: string | null
  last_aggregate_at?: string | null
  mail_provider: string
  mail_configured: boolean
  scan_schedule: { cron: string; bound_cron: string; qps: number; concurrency: number }
  bill_schedule: { cron: string; qps: number; concurrency: number; mode: string }
  detail_schedule: { cron: string; qps: number; concurrency: number }
}

/* 与 openapi.yaml 的 ScanCounters 逐字段对齐。
   禁止写索引签名。拼错字段名会变成 undefined 并显示 0。
   字段列全，拼错则编译失败。 */
export interface ScanCounters {
  inventory_total: number
  excluded_total: number
  eligible_total: number
  processed_total: number
  valid_total: number
  stale_total: number
  empty_total: number
  error_total: number
  parse_error_total: number
  duplicate_reading_total: number
}

export interface ScanRun {
  id: string
  trigger: string
  status: string
  started_at: string
  finished_at?: string | null
  heartbeat_at?: string | null
  qps: number
  concurrency: number
  bound_only: boolean
  counters: ScanCounters
}

export interface BillCounters {
  inventory_total: number
  excluded_total: number
  eligible_total: number
  processed_total: number
  valid_total: number
  partial_total: number
  no_data_total: number
  empty_total: number
  error_total: number
  month_data_total: number
  month_no_data_total: number
  month_partial_total: number
  month_error_total: number
  canonical_saved_total: number
  changed_total: number
}

export interface BillRun {
  id: string
  parent_run_id?: string | null
  trigger: string
  status: string
  mode: string
  months: string[]
  started_at: string
  finished_at?: string | null
  heartbeat_at?: string | null
  qps: number
  concurrency: number
  limit?: number | null
  counters: BillCounters
  error_message?: string | null
}

export interface DailyDetailCounters {
  inventory_total: number
  excluded_total: number
  eligible_total: number
  processed_total: number
  valid_total: number
  no_data_total: number
  partial_total: number
  empty_total: number
  error_total: number
  days_saved_total: number
  changed_total: number
}

export interface DailyDetailRun {
  id: string
  parent_run_id?: string | null
  trigger: string
  status: string
  months: string[]
  started_at: string
  finished_at?: string | null
  heartbeat_at?: string | null
  qps: number
  concurrency: number
  limit?: number | null
  initialization: boolean
  counters: DailyDetailCounters
  error_message?: string | null
}

export interface AdminUser {
  id: string
  email: string
  nickname: string
  role: AdminRole
  status: 'active' | 'disabled'
  created_at: string
  last_login_at?: string | null
  email_verified_at?: string | null
  active_sessions: number
  meter?: string | null
  place: string
  channels_enabled: number
}

export interface MailSettingsPayload {
  provider: string
  from: string
  admin_to: string
  base_url: string
  reply_to: string
  smtp: { host: string; port: number; user: string; tls_mode: string }
  tencent: { secret_id: string; region: string; from_email: string; template_id: number }
}

export interface MailSettingsView {
  settings: MailSettingsPayload
  secret_set: Record<string, boolean>
  secret_masked: Record<string, string>
  source: 'env' | 'panel'
  effective_provider: string
  missing: string[]
  ready: boolean
  secrets_writable: boolean
  secrets_unreadable: boolean
  providers: string[]
  updated_at?: string | null
  updated_by: string
}

/** 第三方登录凭证。client secret 只出不进。读到打码值；原样回传表示未改。 */
export interface OAuthSettingsPayload {
  base_url: string
  google_client_id: string
  github_client_id: string
}

export interface OAuthSettingsView {
  settings: OAuthSettingsPayload
  source: 'env' | 'panel'
  /** 回调地址。由服务端算出，须贴入提供方后台。 */
  redirect_uris: Record<string, string>
  configured: Record<string, boolean>
  missing: Record<string, string[]>
  secret_set: Record<string, boolean>
  secret_masked: Record<string, string>
  secrets_writable: boolean
  secrets_unreadable: boolean
  /** .env 兜底站点地址。面板留空时使用。 */
  env_base_url: string
  updated_at?: string | null
  updated_by: string
}

export interface CaptchaSettingsPayload {
  provider: CaptchaProvider
  site_key: string
  hostname: string
  actions: CaptchaAction[]
}

export interface CaptchaSettingsView {
  settings: CaptchaSettingsPayload
  source: 'env' | 'panel'
  providers: CaptchaProvider[]
  protected_actions: CaptchaAction[]
  secret_set: Record<string, boolean>
  secret_masked: Record<string, string>
  secrets_writable: boolean
  secrets_unreadable: boolean
  missing: string[]
  ready: boolean
  updated_at?: string | null
  updated_by: string
}

export interface CaptchaProbeResult {
  provider: CaptchaProvider
  available: boolean
  passed: boolean
}

/* 当前采集学校。清单由后端上游提供。
   前端不内置学校名单。换爬虫即换清单。 */
export interface SchoolOption {
  id: string
  name: string
  balance: boolean
  bill: boolean
  detail: boolean
}

export interface SchoolProvider {
  name: string
  display_name: string
  default_base_url: string
  schools: SchoolOption[]
}

export interface SchoolBinding {
  provider: string
  area_id: string
  area_name: string
  base_url: string
}

export interface SchoolSettingsView {
  providers: SchoolProvider[]
  current: SchoolBinding
  source: 'env' | 'panel'
  configured: boolean
  updated_at?: string | null
  updated_by?: string
}

export interface ScannerSettingsView {
  settings: {
    shared: {
      qps: number
      concurrency: number
    }
    balance: {
      cron: string
      bound_cron: string
      qps: number
      concurrency: number
      retry_max: number
      enabled: boolean
    }
    bills: BatchScannerSettings
    daily_details: BatchScannerSettings & {
      retry_cron: string
      bootstrap_from: string
      auto_bootstrap: boolean
    }
  }
  source: 'env' | 'panel'
  limits: {
    shared: ScannerLimits
    balance: ScannerLimits
    bills: ScannerLimits
    daily_details: ScannerLimits
    max_qps?: number
    max_concurrency?: number
  }
  updated_at?: string | null
  updated_by: string
}

export interface ScannerLimits {
  max_qps: number
  max_concurrency: number
}

export interface BatchScannerSettings {
  cron: string
  qps: number
  concurrency: number
  retry_max: number
  month_retry_max: number
  enabled: boolean
}

export interface AuditEntry {
  id: string
  actor_label: string
  action: string
  target: string
  detail: Record<string, unknown>
  request_id: string
  created_at: string
}

/* 所有管理端点带 auth:'session'。使用当前用户 access token。
   后端每次请求现查角色。降权立刻生效。 */
export const adminApi = {
  session: () => req<AdminSession>('GET', '/api/v1/admin/session', { auth: 'session' }),
  overview: () => req<AdminOverview>('GET', '/api/v1/admin/overview', { auth: 'session' }),
  readiness: () => api.healthReady(),

  /* 列表端点一律为 { items, next_cursor }，不是裸数组。 */
  scanRuns: (limit = 10) =>
    req<{ items: ScanRun[]; next_cursor?: string | null }>(
      'GET', `/api/v1/admin/scan-runs?page_size=${limit}`, { auth: 'session' },
    ),
  /* full 为后端护栏。不给范围也不给 full 返回 explicit_scope_required。
     本按钮语义为全量扫描，此处钉死 full。 */
  createScanRun: (body: { qps?: number; concurrency?: number; limit?: number } = {}) =>
    req<ScanRun>('POST', '/api/v1/admin/scan-runs', {
      body: body.limit ? body : { ...body, full: true },
      auth: 'session',
    }),
  retryScanRun: (runID: string) =>
    req<ScanRun>('POST', `/api/v1/admin/scan-runs/${encodeURIComponent(runID)}/retry`, {
      body: { statuses: ['stale', 'empty', 'error', 'parse_error', 'canceled'] }, auth: 'session',
    }),
  /* 取消为异步。接口仅落标记。worker 下次心跳（15 秒内）才停下。
     响应 status 多为 running。列表轮询刷出终态。 */
  cancelScanRun: (runID: string) =>
    req<ScanRun>('POST', `/api/v1/admin/scan-runs/${encodeURIComponent(runID)}/cancel`, { auth: 'session' }),

  billRuns: (limit = 10) =>
    req<{ items: BillRun[]; next_cursor?: string | null }>(
      'GET', `/api/v1/admin/bill-runs?page_size=${limit}`, { auth: 'session' },
    ),
  createBillRun: (body: { qps?: number; concurrency?: number; limit?: number } = {}) =>
    req<BillRun>('POST', '/api/v1/admin/bill-runs', {
      body: body.limit ? body : { ...body, full: true }, auth: 'session',
    }),
  retryBillRun: (runID: string) =>
    req<BillRun>('POST', `/api/v1/admin/bill-runs/${encodeURIComponent(runID)}/retry`, {
      body: { statuses: ['partial', 'empty', 'error', 'canceled'] }, auth: 'session',
    }),
  cancelBillRun: (runID: string) =>
    req<BillRun>('POST', `/api/v1/admin/bill-runs/${encodeURIComponent(runID)}/cancel`, { auth: 'session' }),

  dailyDetailRuns: (limit = 10) =>
    req<{ items: DailyDetailRun[]; next_cursor?: string | null }>(
      'GET', `/api/v1/admin/daily-detail-runs?page_size=${limit}`, { auth: 'session' },
    ),
  createDailyDetailRun: (body: { qps?: number; concurrency?: number; limit?: number; initialization?: boolean } = {}) =>
    req<DailyDetailRun>('POST', '/api/v1/admin/daily-detail-runs', {
      body: body.limit ? body : { ...body, full: true }, auth: 'session',
    }),
  retryDailyDetailRun: (runID: string) =>
    req<DailyDetailRun>('POST', `/api/v1/admin/daily-detail-runs/${encodeURIComponent(runID)}/retry`, {
      body: { statuses: ['partial', 'empty', 'error', 'canceled'] }, auth: 'session',
    }),
  cancelDailyDetailRun: (runID: string) =>
    req<DailyDetailRun>('POST', `/api/v1/admin/daily-detail-runs/${encodeURIComponent(runID)}/cancel`, { auth: 'session' }),

  schoolSettings: () =>
    req<SchoolSettingsView>('GET', '/api/v1/admin/settings/school', { auth: 'session' }),
  saveSchoolSettings: (body: SchoolBinding) =>
    req<SchoolSettingsView>('PUT', '/api/v1/admin/settings/school', { body, auth: 'session' }),

  scannerSettings: () =>
    req<ScannerSettingsView>('GET', '/api/v1/admin/settings/scanner', { auth: 'session' }),
  saveScannerSettings: (body: ScannerSettingsView['settings']) =>
    req<ScannerSettingsView>('PUT', '/api/v1/admin/settings/scanner', { body, auth: 'session' }),

  users: (q: { q?: string; role?: string; status?: string; limit?: number }) => {
    const params = new URLSearchParams()
    if (q.q) params.set('q', q.q)
    if (q.role) params.set('role', q.role)
    if (q.status) params.set('status', q.status)
    params.set('limit', String(q.limit ?? 50))
    return req<{ items: AdminUser[]; total: number }>(
      'GET', '/api/v1/admin/users?' + params.toString(), { auth: 'session' },
    )
  },
  updateUser: (id: string, body: { role?: AdminRole; status?: 'active' | 'disabled' }) =>
    req<AdminUser>('PATCH', `/api/v1/admin/users/${encodeURIComponent(id)}`, { body, auth: 'session' }),
  revokeUserSessions: (id: string) =>
    req<{ revoked: number }>(
      'POST', `/api/v1/admin/users/${encodeURIComponent(id)}/sessions/revoke`, { auth: 'session' },
    ),
  unbindUserMeter: (id: string) =>
    req<void>('DELETE', `/api/v1/admin/users/${encodeURIComponent(id)}/meter`, { auth: 'session' }),
  deleteUser: (id: string) =>
    req<void>('DELETE', `/api/v1/admin/users/${encodeURIComponent(id)}`, { auth: 'session' }),

  mailSettings: () => req<MailSettingsView>('GET', '/api/v1/admin/settings/mail', { auth: 'session' }),
  /* secrets 三态：缺席 = 保留原值。null = 清空。字符串 = 新值。
     打码值原样回传会被后端忽略。输入框可直接显示打码串。 */
  saveMailSettings: (body: { settings: MailSettingsPayload; secrets?: Record<string, string | null> }) =>
    req<MailSettingsView>('PUT', '/api/v1/admin/settings/mail', { body, auth: 'session' }),
  testMail: (to: string) =>
    req<{ ok: boolean; error?: string }>(
      'POST', '/api/v1/admin/settings/mail/test', { body: { to }, auth: 'session' },
    ),

  oauthSettings: () =>
    req<OAuthSettingsView>('GET', '/api/v1/admin/settings/oauth', { auth: 'session' }),
  saveOAuthSettings: (body: { settings: OAuthSettingsPayload; secrets?: Record<string, string | null> }) =>
    req<OAuthSettingsView>('PUT', '/api/v1/admin/settings/oauth', { body, auth: 'session' }),

  captchaSettings: () =>
    req<CaptchaSettingsView>('GET', '/api/v1/admin/settings/captcha', { auth: 'session' }),
  saveCaptchaSettings: (body: { settings: CaptchaSettingsPayload; secrets?: Record<string, string | null> }) =>
    req<CaptchaSettingsView>('PUT', '/api/v1/admin/settings/captcha', { body, auth: 'session' }),
  testCaptcha: (token = '') =>
    req<CaptchaProbeResult>('POST', '/api/v1/admin/settings/captcha/test', {
      body: { token }, auth: 'session', captchaToken: token,
    }),

  frontendConfig: () => api.frontendConfig(),
  saveFrontendConfig: (body: unknown) =>
    req<unknown>('PUT', '/api/v1/admin/frontend-config', { body, auth: 'session' }),

  audit: (limit = 30) =>
    req<{ items: AuditEntry[] }>('GET', `/api/v1/admin/audit?limit=${limit}`, { auth: 'session' }),
}
