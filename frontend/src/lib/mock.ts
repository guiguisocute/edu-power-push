/* 确定性 mock 数据引擎。逐算式移植自原型 bundle-src.dc.html。
   基准今日为 2026-07-25。序列由 rnd(i,s) 确定性噪声生成。
   保证与原型渲染结果一致。
   live 模式由 api/client.ts 真实数据源替换。 */

import type { Role } from './adminAccess'

export const RATE = 0.62

export interface User {
  name: string
  initial: string
  /** 未绑表时为 null。注册后先有账号，再到概览绑表。 */
  meter: string | null
  /** 人类可读位置。由 building/floor/room 拼出。禁止反解析。 */
  place: string | null
  /** 脱敏后的登录账号展示（邮箱或手机） */
  phone: string
  /** 完整邮箱。账号设置用。mock 可省略。 */
  email?: string | null
  /** 账号角色。live 由 /me 提供。operator/admin 时侧栏显示系统管理。 */
  role?: Role
  /** 结构化位置。live 必填。供隐私预览。禁止从 place 硬切。 */
  building?: string | null
  floor?: string | null
  room?: string | null
  /* 账号状态事实字段。live 由 /me 提供。mock 可省略。 */
  createdAt?: string | null
  lastLoginAt?: string | null
  emailVerifiedAt?: string | null
  activeSessions?: number | null
  boundAt?: string | null
}

/** 拼展示地址：楼栋 · 楼层 · 房间。各字段原样。禁止强加「室」。 */
export function formatPlace(building?: string | null, floor?: string | null, room?: string | null): string {
  return [building, floor, room].map((s) => (s || '').trim()).filter(Boolean).join(' · ')
}

/** 已登录且已绑表。个人数据视图的前置条件。 */
export function isBound(u: User | null): u is User & { meter: string; place: string } {
  return !!u && !!u.meter && !!u.place
}

export const DEFAULT_USER: User = {
  name: '林亦',
  initial: '林',
  meter: '31240718',
  place: '河东 12 栋 · 4 楼 · 402',
  phone: '138****6021',
  email: 'linyi@example.com',
  building: '河东 12 栋',
  floor: '4 楼',
  room: '402',
}

export const NAV_ICON: Record<string, [string, string]> = {
  overview: ['M13 3 6 14h4.6L9.6 21 17 10h-4.6L13 3z', ''],
  usage: ['M4 4v16h16', 'M7.5 15.2 11 10.4l2.9 2.7 3.6-5.3'],
  config: [
    'M6.6 10.4a5.4 5.4 0 0 1 10.8 0c0 3.5 1.2 4.9 1.7 5.4H4.9c.5-.5 1.7-1.9 1.7-5.4z',
    'M10.1 19a2 2 0 0 0 3.8 0',
  ],
  account: [
    'M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8z',
    'M4.5 21a7.5 7.5 0 0 1 15 0M19 8.5l1 1m-1-1 1-1',
  ],
  campus: ['M4 20V10m5 10V4m5 16v-6m5 6V7', ''],
  board: ['M4 7h13M4 12h9M4 17h5', ''],
  /* 管理侧图标与用户侧同线条语言：1.6 描边、两条 path、无填充。 */
  'sys-console': ['M4.5 14.5a7.5 7.5 0 0 1 15 0', 'M12 14.5 15.6 9.6'],
  'sys-scanner': ['M20.5 12a8.5 8.5 0 1 1-2.9-6.4', 'M20.5 4v5h-5'],
  'sys-users': [
    'M9.5 11.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7z',
    'M3 19.5a6.5 6.5 0 0 1 13 0M16.5 5.4a3.5 3.5 0 0 1 0 6.7M18 14.4a5.5 5.5 0 0 1 3 5.1',
  ],
  'sys-channels': ['M3.5 6.5h17v11h-17z', 'm3.5 7.5 8.5 5.5 8.5-5.5'],
  'sys-captcha': ['M12 3.5 19 6.3v4.9c0 4.3-2.9 7.2-7 9.3-4.1-2.1-7-5-7-9.3V6.3l7-2.8z', 'm9.2 11.8 2 2 3.6-3.8'],
  'sys-display': ['M3.5 5.5h17v10h-17z', 'M9 19.5h6M12 15.5v4'],
}

export const DORMS = [
  '河东 9 栋',
  '河东 12 栋',
  '河东 15 栋',
  '河西 4 栋',
  '河西 7 栋',
  '河西 11 栋',
  '惟义苑 2 栋',
  '惟义苑 6 栋',
  '示例 3 栋',
]

export interface BldgDef {
  n: string
  t: 'dorm' | 'teach' | 'lab' | 'pub'
  f: number
  p: number
}

export const BLDG_DEFS: BldgDef[] = [
  { n: '河东 9 栋', t: 'dorm', f: 6, p: 520 },
  { n: '河东 12 栋', t: 'dorm', f: 6, p: 544 },
  { n: '河东 15 栋', t: 'dorm', f: 7, p: 602 },
  { n: '河西 4 栋', t: 'dorm', f: 6, p: 488 },
  { n: '河西 7 栋', t: 'dorm', f: 6, p: 510 },
  { n: '惟义苑 2 栋', t: 'dorm', f: 5, p: 410 },
  { n: '惟义楼', t: 'teach', f: 5, p: 1800 },
  { n: '外语楼', t: 'teach', f: 5, p: 1400 },
  { n: '音乐楼', t: 'teach', f: 4, p: 620 },
  { n: '化学实验楼', t: 'lab', f: 6, p: 760 },
  { n: '物理实验楼', t: 'lab', f: 5, p: 680 },
  { n: '图书馆北馆', t: 'pub', f: 7, p: 2400 },
  { n: '体育中心', t: 'pub', f: 3, p: 900 },
  { n: '第二食堂', t: 'pub', f: 3, p: 3200 },
]

export const T_LABEL: Record<string, string> = {
  dorm: '宿舍',
  teach: '教学',
  lab: '实验',
  pub: '公共',
}

// ---- 确定性噪声
export function rnd(i: number, s: number): number {
  const x = Math.sin(i * 127.1 + s * 311.7) * 43758.5453
  return x - Math.floor(x)
}

// ---- 近 30 天个人与同楼日用电
let _d: number[] | null = null
export function daily(): number[] {
  if (_d) return _d
  _d = Array.from({ length: 30 }, (_, i) =>
    Math.round((8.4 + 3.2 * Math.sin(i / 4.1) + 4.8 * rnd(i, 3)) * 100) / 100,
  )
  return _d
}

let _dm: number[] | null = null
export function dorm(): number[] {
  if (_dm) return _dm
  _dm = Array.from({ length: 30 }, (_, i) =>
    Math.round((7.9 + 1.5 * Math.sin(i / 5.3) + 0.9 * rnd(i, 11)) * 100) / 100,
  )
  return _dm
}

// ---- 日期工具（基准 2026-07-25）
export function dateStr(back: number): string {
  const d = new Date(2026, 6, 25)
  d.setDate(d.getDate() - back)
  return d.getMonth() + 1 + '/' + d.getDate()
}

export function mdStr(back: number): string {
  const d = new Date(2026, 6, 25)
  d.setDate(d.getDate() - back)
  const p = (n: number) => String(n).padStart(2, '0')
  return p(d.getMonth() + 1) + '.' + p(d.getDate())
}

// ---- 概览：周期序列（月/学期/年）
export type PRange = 'month' | 'term' | 'year'

export interface PersonalSeries {
  n: number
  unit: string
  vals: number[]
  dorm: number[]
  label: (i: number) => string
  ticks: [string, string, string]
}

export function personalSeries(r: PRange): PersonalSeries {
  const k = daily()
  const dm = dorm()
  if (r === 'month')
    return {
      n: 30,
      unit: 'kWh',
      vals: k,
      dorm: dm,
      label: (i) => dateStr(29 - i),
      ticks: [dateStr(29), dateStr(15), dateStr(0)],
    }
  if (r === 'term') {
    const v = Array.from({ length: 18 }, (_, i) =>
      Math.round((52 + 16 * Math.sin(i / 2.6) + 26 * rnd(i, 61)) * 10) / 10,
    )
    return {
      n: 18,
      unit: 'kWh',
      vals: v,
      dorm: v.map((_x, i) => Math.round((54 + 7 * Math.sin(i / 3.1) + 5 * rnd(i, 67)) * 10) / 10),
      label: (i) => '第 ' + (i + 1) + ' 周',
      ticks: ['第 1 周', '第 9 周', '第 18 周'],
    }
  }
  const v = Array.from({ length: 12 }, (_, i) =>
    Math.round((188 + 96 * Math.sin((i - 6) / 1.9) + 70 * rnd(i, 71)) * 10) / 10,
  )
  return {
    n: 12,
    unit: 'kWh',
    vals: v,
    dorm: v.map((_x, i) => Math.round((214 + 62 * Math.sin((i - 6) / 2.1) + 18 * rnd(i, 73)) * 10) / 10),
    label: (i) => i + 1 + ' 月',
    ticks: ['1 月', '6 月', '12 月'],
  }
}

// ---- 数据看板：全校序列（日/月/学期/年）
export type CRange = 'day' | 'month' | 'term' | 'year'

export interface CampusSeries {
  n: number
  unit: string
  vals: number[]
  label: (i: number) => string
  ticks: [string, string, string]
}

export function campusSeries(r: CRange): CampusSeries {
  if (r === 'day')
    return {
      n: 24,
      unit: 'MWh',
      vals: Array.from(
        { length: 24 },
        (_, i) =>
          Math.round(
            (4.1 + 3.9 * Math.sin((i - 6) / 3.6) + 2.6 * rnd(i, 21) + (i >= 19 && i <= 23 ? 2.4 : 0)) * 100,
          ) / 100,
      ),
      label: (i) => String(i).padStart(2, '0') + ':00',
      ticks: ['00:00', '12:00', '23:00'],
    }
  if (r === 'month')
    return {
      n: 30,
      unit: 'MWh',
      vals: Array.from(
        { length: 30 },
        (_, i) => Math.round((172 + 34 * Math.sin(i / 3.4) + 38 * rnd(i, 41)) * 10) / 10,
      ),
      label: (i) => dateStr(29 - i),
      ticks: [dateStr(29), dateStr(15), dateStr(0)],
    }
  if (r === 'term')
    return {
      n: 18,
      unit: 'MWh',
      vals: Array.from(
        { length: 18 },
        (_, i) => Math.round((1020 + 220 * Math.sin(i / 2.8) + 300 * rnd(i, 53)) * 10) / 10,
      ),
      label: (i) => '第 ' + (i + 1) + ' 周',
      ticks: ['第 1 周', '第 9 周', '第 18 周'],
    }
  return {
    n: 12,
    unit: 'MWh',
    vals: Array.from(
      { length: 12 },
      (_, i) => Math.round((5680 + 1740 * Math.sin((i - 6) / 1.9) + 880 * rnd(i, 59)) * 10) / 10,
    ),
    label: (i) => i + 1 + ' 月',
    ticks: ['1 月', '6 月', '12 月'],
  }
}

// ---- 用电分析：365 天长序列与月份键
export interface DayRec {
  i: number
  y: number
  m: number
  d: number
  wd: number
  kwh: number
}

let _dl: DayRec[] | null = null
export function dailyLong(): DayRec[] {
  if (_dl) return _dl
  const k = daily()
  const end = new Date(2026, 6, 25)
  const a: DayRec[] = []
  for (let i = 364; i >= 0; i--) {
    const d = new Date(end)
    d.setDate(d.getDate() - i)
    const idx = 364 - i
    const mo = d.getMonth()
    const winter = mo === 11 || mo === 0 || mo === 1 ? 2.8 : mo === 6 || mo === 7 ? 1.9 : 0
    const kwh =
      i < 30
        ? k[29 - i]
        : Math.round(
            (7.2 +
              2.9 * Math.sin(idx / 4.1) +
              4.4 * rnd(idx, 3) +
              (d.getDay() === 0 || d.getDay() === 6 ? 1.5 : 0) +
              winter) *
              100,
          ) / 100
    a.push({ i: idx, y: d.getFullYear(), m: mo, d: d.getDate(), wd: d.getDay(), kwh })
  }
  _dl = a
  return _dl
}

export interface MonthKey {
  k: string
  y: number
  m: number
}

let _mk: MonthKey[] | null = null
export function monthKeys(): MonthKey[] {
  if (_mk) return _mk
  const seen: string[] = []
  const out: MonthKey[] = []
  dailyLong().forEach((x) => {
    const k = x.y + '-' + x.m
    if (seen.indexOf(k) < 0) {
      seen.push(k)
      out.push({ k, y: x.y, m: x.m })
    }
  })
  _mk = out.filter((o) => dailyLong().filter((x) => x.y === o.y && x.m === o.m).length >= 20).reverse()
  return _mk
}

// ---- 用电分析：某月每日 24 小时分布
export function monthHours(days: DayRec[]): number[][] {
  const base = [
    0.34, 0.22, 0.14, 0.1, 0.08, 0.08, 0.12, 0.26, 0.3, 0.24, 0.2, 0.28, 0.46, 0.52, 0.5, 0.44, 0.38,
    0.42, 0.58, 0.74, 0.86, 0.92, 0.78, 0.56,
  ]
  return days.map((x) => {
    const w = base.map((v, h) => v * (0.72 + 0.55 * rnd(x.i * 24 + h, 7)))
    const s = w.reduce((a, y) => a + y, 0)
    return w.map((v) => (v / s) * x.kwh)
  })
}

// ---- 数据看板：楼栋列表
export interface Building extends BldgDef {
  kwh: number
  mom: number
  per: number
  rooms: number
  floors: number[]
}

let _bl: Building[] | null = null
export function buildings(): Building[] {
  if (_bl) return _bl
  const base: Record<string, number> = { dorm: 3.6, teach: 2.4, lab: 3.6, pub: 2.9 }
  _bl = BLDG_DEFS.map((b, i) => {
    const kwh = Math.round(base[b.t] * b.f * (38 + rnd(i, 61) * 46) * 10) / 10
    const raw = Array.from({ length: b.f }, (_, f) => 0.7 + rnd(i * 13 + f, 71) * 0.9)
    const sum = raw.reduce((a, x) => a + x, 0)
    return {
      ...b,
      kwh,
      mom: Math.round((rnd(i, 67) * 38 - 16) * 10) / 10,
      per: kwh / b.p,
      rooms: Math.max(1, Math.round(b.p / (b.t === 'dorm' ? 4 : 12))),
      floors: raw.map((w) => Math.round(((kwh * w) / sum) * 10) / 10),
    }
  })
  return _bl
}

// ---- 排行榜
export type BoardKind = 'top' | 'save' | 'surge' | 'drop'
export type BPeriod = 'day' | 'week' | 'month'

export interface BoardFilter {
  board: BoardKind
  bPeriod: BPeriod
  bBldg: string
  bFloor: string
}

export function boardSeed(S: BoardFilter): number {
  const b = S.board
  return (
    (b === 'top' ? 5 : b === 'save' ? 17 : b === 'surge' ? 29 : 37) +
    (S.bPeriod === 'day' ? 101 : S.bPeriod === 'week' ? 53 : 0) +
    (S.bBldg === 'all' ? 0 : (DORMS.indexOf(S.bBldg) + 1) * 23) +
    (S.bFloor === 'all' ? 0 : (Number(S.bFloor) + 1) * 7)
  )
}

export function boardTotal(S: BoardFilter): number {
  if (S.bBldg === 'all') return 2864
  return S.bFloor === 'all' ? 216 : 36
}

export function boardVal(i: number, kind: BoardKind, S: BoardFilter): { kwh: number; delta: number } {
  const seed = boardSeed(S)
  const d = S.bPeriod === 'day' ? 30 : S.bPeriod === 'week' ? 4.3 : 1
  let kwh: number, delta: number
  if (kind === 'top') {
    kwh = (186 - i * 1.9 - rnd(i, seed) * 1.2) / d
    delta = rnd(i, seed + 1) * 46 - 14
  } else if (kind === 'save') {
    kwh = (6.2 + i * 0.62 + rnd(i, seed) * 0.4) / d
    delta = rnd(i, seed + 1) * 20 - 16
  } else if (kind === 'drop') {
    kwh = (64 + rnd(i, seed) * 82) / d
    delta = -(78 - i * 0.9 - rnd(i, seed + 1) * 0.6)
  } else {
    kwh = (74 + rnd(i, seed) * 90) / d
    delta = 196 - i * 2.6 - rnd(i, seed + 1) * 1.6
  }
  return { kwh: Math.max(0.1, kwh), delta: Math.round(delta * 10) / 10 }
}

export interface BoardRowData {
  rank: string
  meter: string
  place: string
  kwh: number
  delta: number
}

export function boardRow(i: number, S: BoardFilter): BoardRowData {
  const seed = boardSeed(S)
  const v = boardVal(i, S.board, S)
  const bn = S.bBldg === 'all' ? DORMS[Math.floor(rnd(i, seed + 3) * DORMS.length)] : S.bBldg
  const fl = S.bFloor === 'all' ? 1 + Math.floor(rnd(i, seed + 7) * 6) : Number(S.bFloor) + 1
  const roll = rnd(i, seed + 5)
  // mock：约 1/3 未注册，约 1/3 匿名，其余假昵称。
  const meter =
    roll < 0.33 ? '未注册用户' : roll < 0.66 ? '匿名用户' : '同学' + String((i % 90) + 10)
  return {
    rank: String(i + 1).padStart(2, '0'),
    meter,
    // 与后端默认一致：完整栋号、完整楼层、房间打码。
    place: bn + ' · ' + fl + ' 楼 · **',
    kwh: v.kwh,
    delta: v.delta,
  }
}

export function boardData(S: BoardFilter): BoardRowData[] {
  return Array.from({ length: Math.min(50, boardTotal(S)) }, (_, i) => boardRow(i, S))
}

export interface MyBoard {
  out: boolean
  reason?: 'building' | 'floor'
  rank?: number
  total?: number
  kwh?: number
  delta?: number
  pct?: number
}

export function myBoard(S: BoardFilter, user: User | null): MyBoard | null {
  const U = user
  if (!isBound(U)) return null
  const myFloor = Number((U.place.match(/(\d)\d\d\s*$/) || [])[1] || 4)
  if (S.bBldg !== 'all' && U.place.indexOf(S.bBldg) !== 0) return { out: true, reason: 'building' }
  if (S.bFloor !== 'all' && Number(S.bFloor) + 1 !== myFloor) return { out: true, reason: 'floor' }
  const total = boardTotal(S)
  const seed = boardSeed(S)
  const h = Number(U.meter.slice(-3)) % 89
  const rank = 1 + Math.floor(rnd(h, seed + 41) * (total - 1))
  const b = S.board
  const N = Math.min(50, total)
  const topv = boardVal(Math.min(N - 1, rank - 1), b, S)
  const x = rank <= N ? 0 : (rank - N) / Math.max(1, total - N)
  const decay = b === 'top' ? 0.22 : b === 'save' ? 3.4 : 0.62
  const kwh = rank <= N ? topv.kwh : boardVal(N - 1, b, S).kwh * Math.pow(decay, x)
  let delta: number
  if (b === 'surge' || b === 'drop') {
    const dN = boardVal(N - 1, b, S).delta
    delta = Math.round((dN - (dN - (b === 'surge' ? -18 : 12)) * Math.pow(x, 0.6)) * 10) / 10
  } else delta = Math.round((rnd(h, seed + 43) * 34 - 15) * 10) / 10
  return { out: false, rank, total, kwh, delta, pct: Math.round((1 - rank / total) * 100) }
}
