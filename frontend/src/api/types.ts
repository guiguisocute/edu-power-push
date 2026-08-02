/* API 类型。与 openapi.yaml 对齐。
   金额与电量为 Decimal 字符串。时间为 RFC 3339 字符串。 */

import type { Role } from '../lib/adminAccess'

export type Decimal = string

export type Availability = 'ready' | 'partial' | 'insufficient_history' | 'unavailable'

export interface Quality {
  eligible: number
  covered: number
  coverage_ratio: number
  stale: number
  anomalies: number
}

export interface ApiErrorBody {
  error: { code: string; message: string; request_id: string }
}

export interface Health {
  status: 'ok'
  time: string
}

export interface Readiness {
  status: 'ready' | 'degraded'
  database: 'ready' | 'unavailable'
  migrations: string
  worker: 'ready' | 'stale' | 'unavailable'
}

/* ---- 产品用户会话 ---- */

export interface UserMeter {
  meter: string
  campus: string
  building: string
  floor: string
  room: string
  bound_at?: string | null
}

export interface User {
  id: string
  email: string
  nickname: string
  status: string
  /* 账号角色。前端仅用于侧栏是否显示管理选项卡。
     每个管理端点自行查库。改此字段骗不出权限。
     缺字段时按 user。 */
  role?: Role
  created_at: string
  last_login_at?: string | null
  /** 邮箱验证时间。注册与换绑邮箱流程会写入。 */
  email_verified_at?: string | null
  /** 未吊销且未过期的 refresh family 数。即仍登录的设备数。 */
  active_sessions?: number
  /** 注册仅创建账号。未绑表时为 null。 */
  meter: UserMeter | null
}

export interface TokenResponse {
  access_token: string
  token_type: 'Bearer'
  /** 秒 */
  expires_in: number
  user: User
}

/** 绑表前核对宿舍位置。禁止含余额或读数。 */
export interface MeterPreview {
  meter: string
  campus: string
  building: string
  floor: string
  room: string
  claimable: boolean
  reason?: 'already_bound' | 'excluded' | 'unsupported_building' | null
}

/** 筛选下拉的楼栋与楼层来源。 */
export interface CampusScopes {
  updated_at: string
  campuses: { name: string; buildings: { name: string; floors: string[]; room_count: number }[] }[]
}

export interface LatestReading {
  reading_time: string
  observed_at: string
  prepaid_yuan: Decimal
  subsidy_yuan?: Decimal
  total_yuan: Decimal
  total_kwh: Decimal
  meter_status?: string
  freshness: 'fresh' | 'stale'
}

export interface MeterOverview {
  meter: string
  location: { building: string; floor: string; room: string }
  latest: LatestReading | null
  recent_7d_kwh?: Decimal | null
  availability: Availability
  quality: Quality
}

/** 当前账号绑定电表的单表即时刷新。 */
export interface MeterRefresh {
  meter: string
  /** refreshed=上游有新读数。cached=与库中相同。upstream_unavailable=上游不可用。 */
  status: 'refreshed' | 'cached' | 'upstream_unavailable'
  latest: LatestReading | null
  refreshed_at: string
  next_allowed_at?: string | null
  availability: Availability
  quality?: Quality
}

export interface SeriesPoint {
  period_start: string
  period_end?: string
  value?: Decimal | null
  /** 该时间桶内逐日判定后的日均非空房数 */
  meters?: number
  availability: Availability
  quality?: Quality
}

export interface TimeSeries {
  metric: string
  unit: 'yuan' | 'kWh'
  granularity: 'day' | 'week' | 'month'
  /** 各桶日均非空房数最大值。桶无数据时的兜底除数。 */
  scope_meters?: number
  availability: Availability
  quality: Quality
  points: SeriesPoint[]
}

/* 日/夜用电拆分。数据来自扫描区间，非官方日用量。
   上游一天仅两次读数，最细拆到两次抄表之间。
   日夜时长不等。比强度看 kwh_per_hour，比总量看 kwh。 */
export interface DayNightSlice {
  kwh: Decimal
  hours: number
  kwh_per_hour: Decimal
  intervals: number
  days: number
  /** 计入时长中落在本侧窗口的比例。1 = 抄表时间与 06:00/18:00 对齐。 */
  purity: number
}

export interface DayNightSplit {
  meter: string
  from: string
  to: string
  timezone: string
  day_window: string
  night_window: string
  day: DayNightSlice
  night: DayNightSlice
  /** 横跨边界、无法归属而被丢弃的区间数 */
  skipped_intervals: number
  availability: Availability
}

export interface MonthlyBill {
  month: string
  start_kwh: Decimal
  end_kwh: Decimal
  usage_kwh: Decimal
  cost_yuan: Decimal
}

export interface CampusSummary {
  scope: Record<string, string>
  from: string
  to: string
  total_kwh: Decimal | null
  per_room_kwh: Decimal | null
  /** per_room_kwh 的除数：查询窗口内逐日判定后的日均非空房数 */
  meters?: number
  availability: Availability
  quality: Quality
}

/* 全校月度用量。数据源为上游官方月账单。
   日序列优先官方明细；未结算日才回退扫描估算。
   月账单保留为自然月对账与修订数据源。 */
export interface CampusBillMonth {
  month: string // YYYY-MM
  total_kwh: Decimal
  cost_yuan: Decimal
  /** 该月日均非空房数。月度总量仍含空房用量。 */
  meters: number
  per_room_kwh: Decimal
}

export interface CampusBillBuilding {
  building: string
  total_kwh: Decimal
  meters: number
  share: number
}

export interface CampusBills {
  from_month: string
  to_month: string
  building?: string
  months: CampusBillMonth[]
  /** 区间内最后一个月的楼栋拆分 */
  buildings?: CampusBillBuilding[]
  source: 'monthly_bill'
}

/* 榜单行。脱敏在服务端。
   name = 名称列。label = 位置列。
   明文 building/floor/room 仅 is_self 时下发。 */
export interface RankingEntry {
  rank: number
  name: string
  label: string
  is_self: boolean
  building?: string | null
  floor?: string | null
  room?: string | null
  value_kwh: Decimal
  change_ratio?: number | null
}

/* 我的名次。后端全量计算，与 items 的 Top N 截断无关。
   明文位置仅回给本人。in_list 表示该名次已出现在 items。 */
export interface RankingSelf {
  rank: number
  total: number
  percentile: number
  name: string
  label: string
  building: string
  floor: string
  room: string
  value_kwh: Decimal
  change_ratio?: number | null
  in_list: boolean
  /** 上下各一名（含自己）。邻居仍按各自偏好脱敏。 */
  neighbors: RankingEntry[]
}

/** 榜单参与与脱敏。隐私开关服务端生效。 */
export interface LeaderboardPreference {
  opted_in: boolean
  show_building: boolean
  show_floor: boolean
  show_room: boolean
  show_nickname: boolean
  mask_building: boolean
  mask_floor: boolean
  mask_room: boolean
  updated_at: string
}

/** 低额预警与定时摘要规则 */
export interface NotificationSettings {
  low_balance_alert: boolean
  threshold_yuan: Decimal
  scheduled_digest: boolean
  period: 'daily' | 'twice' | 'every3' | 'weekly'
  push_time: string
  timezone?: string
  templates?: {
    low_balance?: NotificationMessageTemplate
    digest?: NotificationMessageTemplate
  }
  updated_at?: string
}

export interface NotificationMessageTemplate {
  title: string
  body: string
}

export interface ChannelTestResult {
  status: 'ok' | 'failed'
  at: string
  message?: string | null
  latency_ms?: number | null
}

export interface NotificationChannel {
  channel: string
  enabled: boolean
  config: Record<string, unknown>
  secret_set: Record<string, boolean>
  last_result?: ChannelTestResult | null
  updated_at?: string
}

export interface ChannelList {
  channels: NotificationChannel[]
}

export interface PushLog {
  id: string
  sent_at: string
  channel: string
  kind: 'low_balance' | 'digest' | 'test' | 'system'
	status: 'accepted' | 'delivered' | 'failed' | 'skipped'
  summary: string
  error?: string | null
}

export interface PushLogPage {
  items: PushLog[]
  next_cursor?: string | null
}

/* 楼栋（或某栋楼层）× 时间用电矩阵。
   无 building 按楼栋分；有 building 按楼层分。
   values 与 buckets 对齐。null 表示无有效读数，不是 0。 */
export interface CampusBreakdownRow {
  key: string
  total_kwh: Decimal
  share: number
  /** 整个窗口日均非空房数。用于行合计户均。 */
  meters: number
  /** 与 values 一一对应：各时间桶内的日均非空房数 */
  meter_counts: number[]
  values: (Decimal | null)[]
}

export interface CampusBreakdown {
  group_by: 'building' | 'floor'
  granularity: 'day' | 'week' | 'month'
  buckets: string[]
  total_kwh: Decimal
  max_cell_kwh: Decimal
  rows: CampusBreakdownRow[]
  availability: Availability
  quality: Quality
}

export interface Ranking {
  period: 'day' | 'week' | 'month'
  mode: 'usage' | 'saving' | 'surge' | 'drop'
  /** 本期与环比期的自然日闭区间。由服务端返回。 */
  current_period: { from: string; to: string }
  previous_period: { from: string; to: string }
  /** 半静态榜单本次切换及下次切换时间 */
  updated_at: string
  next_update_at: string
  availability: Availability
  quality: Quality
  excluded_count: number
  items: RankingEntry[]
  /** 仅已登录且绑表时下发。未绑表或无有效用量时为 null。 */
  self?: RankingSelf | null
}

export interface UnavailableCapability {
  availability: 'unavailable'
  reason_code: string
  message: string
  quality: Quality
}

export interface InventoryTree {
  updated_at: string
  inventory_total: number
  excluded_total: number
  campuses: { name: string; buildings: Record<string, unknown>[] }[]
}
