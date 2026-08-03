/* live 模式取数 hooks。视图消费的薄适配层。
   60s 去重缓存。Decimal 字符串转 number。
   availability 与 quality 原样透传。 */

import { useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { ApiError, api } from './client'
import { IS_LIVE } from './mode'
import { sessionCacheScope, useSession } from './session'
import type {
  Availability,
  CampusBills,
  CampusBreakdown,
  CampusScopes,
  CampusSummary,
  DayNightSplit,
  MeterOverview,
  MonthlyBill,
  PushLogPage,
  Quality,
  Ranking,
  TimeSeries,
  UnavailableCapability,
} from './types'

export interface LiveState<T> {
  data: T | null
  loading: boolean
  error: string | null
}

const cache = new Map<string, { t: number; p: Promise<unknown> }>()
const TTL = 60_000

function cached<T>(key: string, fn: () => Promise<T>, ttl = TTL): Promise<T> {
  const hit = cache.get(key)
  const now = Date.now()
  if (hit && now - hit.t < ttl) return hit.p as Promise<T>
  const p = fn().catch((e) => {
    cache.delete(key)
    throw e
  })
  cache.set(key, { t: now, p })
  return p
}

function useLiveFetch<T>(enabled: boolean, key: string, fn: () => Promise<T>, ttl = TTL): LiveState<T> {
  const [st, setSt] = useState<LiveState<T>>({ data: null, loading: enabled, error: null })
  const keyRef = useRef(key)
  // 手动刷新（epoch 递增）时重跑本 effect。缓存已在 refreshLive 内清空。
  const epoch = useSyncExternalStore(subscribeRefresh, () => refreshEpoch, () => refreshEpoch)
  useEffect(() => {
    if (!enabled) return
    let alive = true
    // 同 key 重取保留旧数据，避免闪空。换 key 才清空。
    const sameKey = keyRef.current === key
    keyRef.current = key
    setSt((p) => ({ data: sameKey ? p.data : null, loading: true, error: null }))
    cached(key, fn, ttl).then(
      (d) => alive && setSt({ data: d, loading: false, error: null }),
      (e) => alive && setSt({ data: null, loading: false, error: e?.message || String(e) }),
    )
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, key, epoch])
  return st
}

/* ---- 手动刷新协调 ----
   live：POST /me/refresh 后清缓存，hooks 重取。
   404/405/501 降级为纯重取，兼容旧后端。
   mock：仅清缓存并打时间戳。冷却 30s。本地冷却仅为体验层。 */
export const REFRESH_CD_MS = 30_000

let refreshEpoch = 0
let refreshVersion = 0
let refreshedAt = Date.now()
let cooldownUntil = 0
let refreshPending = false
let refreshError: string | null = null
const refreshSubs = new Set<() => void>()

function subscribeRefresh(cb: () => void) {
  refreshSubs.add(cb)
  return () => {
    refreshSubs.delete(cb)
  }
}
function emitRefresh() {
  refreshVersion++
  refreshSubs.forEach((f) => f())
}

/** 触发一次全局重新取数。冷却中直接返回。 */
export async function refreshLive(meter?: string): Promise<void> {
  if (cooldownUntil > Date.now()) return
  cooldownUntil = Date.now() + REFRESH_CD_MS
  refreshError = null
  refreshPending = true
  emitRefresh()

  if (IS_LIVE && meter) {
    try {
      const r = await api.meRefresh()
      if (r.status === 'upstream_unavailable') refreshError = '上游不可用，显示的是最后一次成功读数'
      if (r.next_allowed_at) {
        const t = Date.parse(r.next_allowed_at)
        if (Number.isFinite(t)) cooldownUntil = t
      }
    } catch (e) {
      const err = e as ApiError
      if (err?.status === 429) {
        // 服务端限流：本地冷却对齐 Retry-After。不算错误。
        if (err.retryAfter) cooldownUntil = Date.now() + err.retryAfter * 1000
      } else if (err?.status !== 404 && err?.status !== 405 && err?.status !== 501) {
        // 404/405/501：旧环境缺端点，无提示降级为纯重取。
        refreshError = err?.message || String(e)
      }
    }
  }

  cache.clear()
  refreshEpoch++
  refreshedAt = Date.now()
  refreshPending = false
  emitRefresh()
}

export interface RefreshState {
  /** 上次取数时间（毫秒时间戳） */
  at: number
  /** 冷却截止时间（毫秒时间戳）。已过期即可再次刷新。 */
  until: number
  /** 刷新轮次。可用作动效重放 key。 */
  epoch: number
  /** 刷新请求在途（live 模式 POST 往返） */
  pending: boolean
  error: string | null
}

export function useRefreshState(): RefreshState {
  useSyncExternalStore(subscribeRefresh, () => refreshVersion, () => refreshVersion)
  return { at: refreshedAt, until: cooldownUntil, epoch: refreshEpoch, pending: refreshPending, error: refreshError }
}

export const num = (d: string | null | undefined): number | null => (d == null ? null : parseFloat(d))

const iso = (d: Date) => d.toISOString()
const daysAgo = (n: number) => {
  const d = new Date()
  d.setDate(d.getDate() - n)
  return d
}

/** 视图周期映射为契约窗口与粒度。上游无小时数据：day 周期不可取数。 */
export function rangeWindow(range: 'week' | 'month' | 'term' | 'year' | 'day'): {
  from: string
  to: string
  granularity: 'day' | 'week' | 'month'
} | null {
  const to = iso(new Date())
  if (range === 'day') return null
  if (range === 'week') return { from: iso(daysAgo(7)), to, granularity: 'day' }
  if (range === 'month') return { from: iso(daysAgo(30)), to, granularity: 'day' }
  if (range === 'term') return { from: iso(daysAgo(18 * 7)), to, granularity: 'week' }
  return { from: iso(daysAgo(365)), to, granularity: 'month' }
}

export function monthWindow(y: number, m0: number): { from: string; to: string } {
  return { from: iso(new Date(y, m0, 1)), to: iso(new Date(y, m0 + 1, 1)) }
}

/** 个人数据（/me/*）前置条件：会话已恢复且已登录。否则只会收到 401。 */
function useSignedIn(): boolean {
  return useSession().status === 'signed-in'
}

/** 校园聚合匿名可读。仅等待启动无提示 refresh 结束。
    避免登录态恢复时匿名与登录各取一遍。 */
function useSessionSettled(): boolean {
  return useSession().status !== 'booting'
}

/* 个人数据一律走 /me/*。/meters/{meter}/* 为运维端点。
   meter 参数仅作缓存键与已绑表判断，不进 URL。 */

export function useLiveMeter(meter: string | undefined): LiveState<MeterOverview> {
  const ready = useSignedIn()
  return useLiveFetch(IS_LIVE && ready && !!meter, 'me:overview:' + meter, () => api.meOverview())
}

/** 概览页最近推送。日志不走 60 秒缓存，保证测试渠道后立即可见。 */
export function useLivePushLogs(enabled = true, limit = 5): LiveState<PushLogPage> {
  const ready = useSignedIn()
  return useLiveFetch(IS_LIVE && ready && enabled, `me:push-logs:${limit}`, () => api.pushLogs({ limit }), 0)
}

export function useLiveMeterSeries(
  meter: string | undefined,
  metric: 'balance' | 'consumption',
  win: { from: string; to: string; granularity: 'day' | 'week' | 'month' } | null,
): LiveState<TimeSeries> {
  const ready = useSignedIn()
  const key = win ? `me:series:${meter}:${metric}:${win.granularity}:${win.from.slice(0, 10)}:${win.to.slice(0, 10)}` : 'off'
  return useLiveFetch(IS_LIVE && ready && !!meter && !!win, key, () => api.meSeries({ ...win!, metric }))
}

/* 日/夜用电。拆不出时 availability 为 unavailable 或 insufficient_history。
   视图据此整块隐藏。禁止画凑数对比。 */
export function useLiveDayNight(
  meter: string | undefined,
  win: { from: string; to: string } | null,
): LiveState<DayNightSplit> {
  const ready = useSignedIn()
  const key = win ? `me:daynight:${meter}:${win.from.slice(0, 10)}:${win.to.slice(0, 10)}` : 'off'
  return useLiveFetch(IS_LIVE && ready && !!meter && !!win, key, () => api.meDayNight(win!))
}

/** 月度账单。月总量与电费以账单为准。缺月时回落日序列合计。 */
export function useLiveBills(
  meter: string | undefined,
  fromMonth?: string,
  toMonth?: string,
): LiveState<MonthlyBill[]> {
  const ready = useSignedIn()
  const key = `me:bills:${meter}:${fromMonth || ''}:${toMonth || ''}`
  return useLiveFetch(IS_LIVE && ready && !!meter, key, () =>
    api.meBills({ from_month: fromMonth, to_month: toMonth }),
  )
}

/** 筛选下拉的楼栋与楼层来源。 */
export function useLiveScopes(): LiveState<CampusScopes> {
  const ready = useSessionSettled()
  return useLiveFetch(IS_LIVE && ready, 'campus:scopes', () => api.campusScopes())
}

export function useLiveCampusSeries(
  win: { from: string; to: string; granularity: 'day' | 'week' | 'month' } | null,
  building?: string,
  floor?: string,
): LiveState<TimeSeries> {
  const ready = useSessionSettled()
  const key = win
    ? `cseries:${building || 'all'}:${floor || 'all'}:${win.granularity}:${win.from.slice(0, 10)}:${win.to.slice(0, 10)}`
    : 'off'
  return useLiveFetch(IS_LIVE && ready && !!win, key, () => api.campusSeries({ ...win!, building, floor }))
}

/* 全校合计。win 省略 = 近 24h。传窗口 = 跟视图账期。
   传 null = 关闭取数。年视图走账单口径，禁止再问 deltas。 */
export function useLiveCampusSummary(
  building?: string,
  floor?: string,
  win?: { from: string; to: string } | null,
): LiveState<CampusSummary> {
  const ready = useSessionSettled()
  const w = win === undefined ? { from: iso(daysAgo(1)), to: iso(new Date()) } : win
  const key = w
    ? `csummary:${building || 'all'}:${floor || 'all'}:${w.from.slice(0, 10)}:${w.to.slice(0, 10)}`
    : 'off'
  return useLiveFetch(IS_LIVE && ready && !!w, key, () => api.campusSummary({ ...w!, building, floor }))
}

/* enabled=false 供只需要 self（我的名次）的页面用：没绑表就不必发这一次请求。
   缓存键与榜单页一致，两处同参数时命中同一份数据，不会真的多打接口。 */
export function useLiveRankings(
  period: 'day' | 'week' | 'month',
  mode: 'usage' | 'saving' | 'surge' | 'drop',
  building?: string,
  floor?: string,
  enabled = true,
): LiveState<Ranking> {
  const session = useSession()
  const ready = session.status !== 'booting'
  const key = `rank:${sessionCacheScope(session)}:${period}:${mode}:${building || 'all'}:${floor || 'all'}`
  return useLiveFetch(IS_LIVE && ready && enabled, key, () =>
    api.campusRankings({ period, mode, building, floor, limit: 50 }),
  )
}

/* 楼栋 × 时间用电矩阵。用电构成与热力图共用一次请求。
   分两个端点取会导致口径漂移。 */
export function useLiveBreakdown(
  win: { from: string; to: string; granularity: 'day' | 'week' | 'month' } | null,
  building?: string,
): LiveState<CampusBreakdown> {
  const ready = useSessionSettled()
  const key = win
    ? `breakdown:${building || 'all'}:${win.granularity}:${win.from.slice(0, 10)}:${win.to.slice(0, 10)}`
    : 'off'
  return useLiveFetch(IS_LIVE && ready && !!win, key, () =>
    api.campusBreakdown({ ...win!, building }),
  )
}

/** 年视图热力图。月账单楼栋×月矩阵。横轴铺满 from_month..to_month。 */
export function useLiveBillBreakdown(
  enabled: boolean,
  fromMonth: string,
  toMonth: string,
  building?: string,
): LiveState<CampusBreakdown> {
  const ready = useSessionSettled()
  const key = `billbd:${building || 'all'}:${fromMonth}:${toMonth}`
  return useLiveFetch(IS_LIVE && ready && enabled && !!fromMonth && !!toMonth, key, () =>
    api.campusBreakdown({
      source: 'monthly_bill',
      from_month: fromMonth,
      to_month: toMonth,
      building,
    }),
  )
}

/* 全校月度用量。官方月账单对账数据。
   日序列优先官方明细；未结算日才回退扫描估算。
   月账单独立保留，用于自然月闭合校验与上游修订追踪。 */
export function useLiveCampusBills(building?: string): LiveState<CampusBills> {
  const ready = useSessionSettled()
  return useLiveFetch(IS_LIVE && ready, `campus:bills:${building || 'all'}`, () =>
    api.campusBills(building ? { building } : undefined),
  )
}

export function useLiveHeatmap(): LiveState<UnavailableCapability> {
  const ready = useSessionSettled()
  return useLiveFetch(IS_LIVE && ready, 'heatmap', () => api.hourlyHeatmap())
}

/** Asia/Shanghai 日历日 YYYY-MM-DD。与导出 bucketDate 同口径，避免 UTC 午夜拨日。 */
export function seriesDayKey(periodStartMs: number): string {
  if (!Number.isFinite(periodStartMs)) return ''
  return new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(new Date(periodStartMs))
}

/** 时序转数值数组。null 保留为 null，禁止伪 0。
    meters 为每桶户均除数。at 为桶起点毫秒戳，折周按真实日期归属。 */
export function seriesVals(ts: TimeSeries | null): {
  vals: (number | null)[]
  labels: string[]
  meters: number[]
  at: number[]
} {
  if (!ts) return { vals: [], labels: [], meters: [], at: [] }
  return {
    at: ts.points.map((p) => Date.parse(p.period_start)),
    vals: ts.points.map((p) => (p.value == null ? null : parseFloat(p.value))),
    // 桶表数优先；否则窗口整体表数；再否则 0（不显示户均）。
    meters: ts.points.map((p) => p.meters ?? ts.scope_meters ?? 0),
    labels: ts.points.map((p) => {
      const key = seriesDayKey(Date.parse(p.period_start))
      if (!key) return '—'
      const [yy, mm, dd] = key.split('-')
      // 月粒度带年份，避免跨年月份混淆。
      return ts.granularity === 'month' ? yy + '/' + mm : Number(mm) + '/' + Number(dd)
    }),
  }
}

/**
 * 把稀疏日序列铺满自然月。
 * API 只返回有数的天；若直接 flex 柱，3 天会撑成 3 根巨型矩形。
 * 缺数日 value=0、has=false，柱高为 0 留空；真 0 用电 has=true。
 */
export function densifyMonthDays(
  sparse: { vals: (number | null)[]; meters: number[]; at: number[] },
  year: number,
  month0: number,
): {
  vals: number[]
  has: boolean[]
  meters: number[]
  labels: string[]
  n: number
} {
  const dim = new Date(year, month0 + 1, 0).getDate()
  const byDay = new Map<string, { v: number | null; m: number }>()
  for (let i = 0; i < sparse.at.length; i++) {
    const key = seriesDayKey(sparse.at[i])
    if (!key) continue
    byDay.set(key, { v: sparse.vals[i] ?? null, m: sparse.meters[i] ?? 0 })
  }
  const vals: number[] = []
  const has: boolean[] = []
  const meters: number[] = []
  const labels: string[] = []
  const m1 = month0 + 1
  for (let d = 1; d <= dim; d++) {
    const key = year + '-' + String(m1).padStart(2, '0') + '-' + String(d).padStart(2, '0')
    const hit = byDay.get(key)
    const raw = hit?.v ?? null
    const present = raw != null && Number.isFinite(raw)
    vals.push(present ? (raw as number) : 0)
    has.push(present)
    meters.push(hit?.m ?? 0)
    labels.push(m1 + '/' + d)
  }
  return { vals, has, meters, labels, n: dim }
}

/** 累加时序有值点。全无值返回 null，区别于真 0。 */
export function seriesSumKwh(ts: TimeSeries | null | undefined): number | null {
  if (!ts?.points?.length) return null
  let s = 0
  let any = false
  for (const p of ts.points) {
    if (p.value == null) continue
    const v = parseFloat(p.value)
    if (!Number.isFinite(v)) continue
    s += v
    any = true
  }
  return any ? s : null
}

/** 时序窗口内有数的表数。日序列拼合计时，户均除数用它。 */
export function seriesMeters(ts: TimeSeries | null | undefined): number {
  if (!ts) return 0
  if (ts.scope_meters) return ts.scope_meters
  return Math.max(0, ...ts.points.map((p) => p.meters ?? 0))
}

/** 当月自然月窗口 [月初, 现在]。账单未出时用日序列补本月柱。 */
export function currentMonthWindow(): { from: string; to: string; granularity: 'day' } {
  const now = new Date()
  return {
    from: iso(new Date(now.getFullYear(), now.getMonth(), 1)),
    to: iso(now),
    granularity: 'day',
  }
}

/** 当前自然月 YYYY-MM */
export function currentMonthKey(): string {
  const d = new Date()
  return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0')
}

/** 数据状态说明行文案 */
export function availNote(availability: Availability | undefined, quality: Quality | undefined, error: string | null): string {
  if (error) return '后端连接失败 · ' + error
  if (!availability) return '加载中…'
  const q = quality ? ` · 覆盖 ${quality.covered}/${quality.eligible}（陈旧 ${quality.stale}）` : ''
  const label: Record<Availability, string> = {
    ready: '数据就绪',
    partial: '数据部分覆盖',
    insufficient_history: '历史数据不足',
    unavailable: '上游不可用',
  }
  return label[availability] + q
}
