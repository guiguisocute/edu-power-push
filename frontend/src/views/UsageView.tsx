/* 用电分析视图。对齐原型 isUsage 区块。
   顺序：账期选择、用电日历、余额变化、用电分析报告。
   日历置顶。分时热力图归档在 views/archive。
   受 features.charts.hourly_usage 控制。 */

import { useEffect, useState, type KeyboardEvent, type PointerEvent as ReactPointerEvent } from 'react'
import { useStore } from '../lib/store'
import { makeFmt, themeColors } from '../lib/format'
import { nearestBalancePointIndex } from '../lib/balanceChart'
import { dailyLong, monthKeys, rnd, type DayRec } from '../lib/mock'
import { IS_LIVE } from '../api/mode'
import {
  availNote,
  currentMonthWindow,
  monthWindow,
  seriesSumKwh,
  useLiveBills,
  useLiveMeterSeries,
  useLiveCampusSummary,
  useLiveCampusSeries,
  useLiveDayNight,
} from '../api/live'
import LiveNote from '../components/LiveNote'
import { InlineNote } from '../components/Loading'
import UsageExport from '../components/UsageExport'
import { fromMonthIndex, monthIndex, monthOptions } from '../lib/months'
import type { UsageExportRow } from '../lib/exportUsage'
import HourlyUsageSection from './archive/HourlyUsageSection'

interface CalCell {
  d: string
  /** 日期下方用量数字（kWh 或元）。无数据为空。 */
  val: string
  o: string
  /** 今日/峰值标记环颜色。空串表示不画。 */
  ring: string
  fg: string
  hov: boolean
  tx: string
  tipDate: string
  tipVal: string
  tipUnit: string
  tipAlt: string
  tipDelta: string
  tipDeltaColor: string
  on: (() => void) | null
  tap: (() => void) | null
}

/* 账期下拉月份选项。
   取最近 12 个月与账单真实月份的并集。
   前者保证无账单时下拉非空。后者保证历史月可到达。 */
export function liveMonthKeys(bills: { month: string }[]) {
  const seen = new Map<string, { k: string; y: number; m: number }>()
  const add = (y: number, m: number) => {
    const k = y + '-' + m // m 为 0 基，与 store mKey 格式一致
    if (!seen.has(k)) seen.set(k, { k, y, m })
  }
  const now = new Date()
  for (let i = 0; i < 12; i++) {
    // 用构造函数。禁止 setMonth 连续自减，跨年或 31 号会跳月。
    const d = new Date(now.getFullYear(), now.getMonth() - i, 1)
    add(d.getFullYear(), d.getMonth())
  }
  for (const b of bills) {
    const [by, bm] = b.month.split('-').map(Number)
    if (by && bm) add(by, bm - 1)
  }
  return [...seen.values()].sort((a, b) => b.y - a.y || b.m - a.m)
}

export default function UsageView() {
  const { s, set } = useStore()
  const [balFocus, setBalFocus] = useState<number | null>(null)
  const { RED, OK, FG3 } = themeColors(s.theme === 'dark')
  const elecRate = parseFloat(s.features?.display?.electricityRate || '') || 0.62
  const { RMB, U, f1, f2, alt, kpair } = makeFmt(s.unit, elecRate)
  const showHourly = !!s.features?.charts?.hourlyUsage

  const L = dailyLong()
  const liveMeter = (IS_LIVE && s.user?.meter) || undefined
  /* 一次取回全部月账单（不带 from/to）。禁止每切账期打一次接口。
     账期下拉可按真实账单月份列项。 */
  const lBills = useLiveBills(liveMeter)
  const hist = IS_LIVE ? (lBills.data ?? []) : []
  const keys = IS_LIVE ? liveMonthKeys(hist) : monthKeys()
  const mk = keys.find((o) => o.k === s.mKey) || keys[0]
  const y = mk.y
  const m = mk.m
  const now = new Date()
  const isCur = IS_LIVE ? y === now.getFullYear() && m === now.getMonth() : y === 2026 && m === 6
  useEffect(() => setBalFocus(null), [mk.k])

  // live：账期内日用电与余额时序。mock 下 hooks 空转。
  const mWin = monthWindow(y, m)
  const lCons = useLiveMeterSeries(liveMeter, 'consumption', { ...mWin, granularity: 'day' })
  const lBal = useLiveMeterSeries(liveMeter, 'balance', { ...mWin, granularity: 'day' })
  /* 月合计以账单为准。账单为上游权威值。缺月时回落日序列合计。
     扫描开始前的月常有官方月合计、无逐日明细。禁止标为暂无数据。 */
  const monthStr = y + '-' + String(m + 1).padStart(2, '0')
  const bill = hist.find((b) => b.month === monthStr) ?? null
  const billKwh = bill ? parseFloat(bill.usage_kwh) : null
  const billCost = bill ? parseFloat(bill.cost_yuan) : null
  const hasBill = billKwh != null && Number.isFinite(billKwh)

  /* 本月日累计。账单未出时，月合计从 /me/series 日粒度加总。 */
  const curWin = currentMonthWindow()
  const lConsCur = useLiveMeterSeries(liveMeter, 'consumption', {
    ...curWin,
    granularity: 'day',
  })
  const curMonthKwh = seriesSumKwh(lConsCur.data)

  const days: DayRec[] = IS_LIVE
    ? (lCons.data?.points ?? [])
        .filter((p) => p.value != null)
        .map((p) => {
          const d = new Date(p.period_start)
          return { i: 0, y: d.getFullYear(), m: d.getMonth(), d: d.getDate(), wd: d.getDay(), kwh: parseFloat(p.value!) }
        })
        .filter((d) => d.y === y && d.m === m)
    : L.filter((x) => x.y === y && x.m === m)
  const hasDaily = days.length > 0
  /** 有官方月账单但无逐日抄表差值（历史恢复月常态） */
  const billOnly = IS_LIVE && hasBill && !hasDaily
  /** 正在看本月且账单未出。月合计用日累计。 */
  const isCurProvisional = IS_LIVE && isCur && !hasBill
  const monthTotalFromDaily = isCurProvisional
    ? days.reduce((a, x) => a + x.kwh, 0) || curMonthKwh || 0
    : null
  const mmax = Math.max(...days.map((x) => x.kwh)) || 1
  const byDate: Record<number, DayRec> = {}
  days.forEach((x) => {
    byDate[x.d] = x
  })
  const dim = new Date(y, m + 1, 0).getDate()
  const lead = (new Date(y, m, 1).getDay() + 6) % 7
  const dAvg = days.length ? days.reduce((a, x) => a + x.kwh, 0) / days.length : 0
  const WDN = ['日', '一', '二', '三', '四', '五', '六']
  const cells: CalCell[] = []
  for (let i = 0; i < lead; i++)
    cells.push({
      d: '',
      val: '',
      o: '0',
      ring: '',
      fg: 'transparent',
      hov: false,
      tx: 'none',
      tipDate: '',
      tipVal: '',
      tipUnit: '',
      tipAlt: '',
      tipDelta: '',
      tipDeltaColor: FG3,
      on: null,
      tap: null,
    })
  for (let d = 1; d <= dim; d++) {
    const x = byDate[d]
    const isToday = IS_LIVE
      ? y === now.getFullYear() && m === now.getMonth() && d === now.getDate()
      : y === 2026 && m === 6 && d === 25
    const col = (lead + d - 1) % 7
    const p = x ? kpair(x.kwh) : null
    const dv = x ? ((x.kwh - dAvg) / (dAvg || 1)) * 100 : 0
    // 红层 opacity = 0.08 + (kwh/mmax)*0.92。
    const heat = x ? x.kwh / mmax : 0
    cells.push({
      d: String(d),
      // 格内用量：随 s.unit 显示 kWh 或元。一小数位。
      val: x ? f1(x.kwh) : '',
      // 全格统一白字，不描边。底色越浅字越弱。
      o: x ? (0.08 + heat * 0.92).toFixed(3) : '0',
      ring: isToday ? RED : x && x.kwh === mmax ? 'var(--fg)' : '',
      // 日期为次要信息。数值为主。
      fg: x ? 'rgba(255,255,255,.8)' : 'var(--fg3)',
      hov: s.calHover === d,
      on: () => set({ calHover: d }),
      // 触屏无 hover：点开再点关。
      tap: () => set({ calHover: s.calHover === d ? null : d }),
      tx:
        col <= 1
          ? 'translateX(-10px)'
          : col >= 5
            ? 'translateX(calc(-100% + 54px))'
            : 'translateX(calc(-50% + 22px))',
      tipDate:
        m + 1 + ' 月 ' + d + ' 日 · 周' + WDN[new Date(y, m, d).getDay()] + (isToday ? ' · 今日' : ''),
      tipVal: x ? p!.v : '—',
      tipUnit: x ? p!.u : '',
      tipAlt: x ? '≈ ' + alt(x.kwh) : '暂无数据',
      tipDelta: x ? '较本月日均 ' + (dv > 0 ? '+' : '') + dv.toFixed(0) + '%' : '',
      tipDeltaColor: dv > 0 ? RED : OK,
    })
  }
  const sum = days.reduce((a, x) => a + x.kwh, 0)
  const wk = days.filter((x) => x.wd === 0 || x.wd === 6)
  const wdd = days.filter((x) => x.wd !== 0 && x.wd !== 6)
  const peak = days.reduce((a, x) => (x.kwh > a.kwh ? x : a), days[0] || { kwh: 0, d: 0 })
  const wdAvg = Array.from({ length: 7 }, (_, i) => {
    const g = days.filter((x) => (x.wd + 6) % 7 === i)
    return g.length ? g.reduce((a, x) => a + x.kwh, 0) / g.length : 0
  })
  const wdMax = Math.max(...wdAvg) || 1

  // 余额：mock 按月内 8/22 充值反推，月末锚定 43.87。live 用后端余额时序。
  const cost = days.map((x) => x.kwh * elecRate)
  const rec = IS_LIVE ? days.map(() => 0) : days.map((x) => (x.d === 8 ? 100 : x.d === 22 ? 80 : 0))
  const net: number[] = []
  let acc = 0
  days.forEach((_, i) => {
    acc += rec[i] - cost[i]
    net.push(acc)
  })
  const target = isCur ? 43.87 : Math.round((22 + rnd(m * 7 + y, 83) * 46) * 100) / 100
  const off = target - (net[net.length - 1] ?? 0)
  const mockBalances = net.map((v) => Math.max(0, Math.round((v + off) * 100) / 100))
  const balancePoints = IS_LIVE
    ? (lBal.data?.points ?? [])
        .filter((p) => p.value != null && Number.isFinite(parseFloat(p.value!)))
        .map((p) => ({ value: parseFloat(p.value!), date: new Date(p.period_start) }))
        .filter((p) => Number.isFinite(p.date.getTime()))
    : mockBalances.map((value, i) => ({ value, date: new Date(days[i].y, days[i].m, days[i].d) }))
  const bal = balancePoints.map((p) => p.value)
  const bmax = Math.max(Math.ceil(Math.max(...bal, 0) / 40) * 40, 40)
  const bp = bal.map((v, i) => [(bal.length === 1 ? 0 : i / (bal.length - 1)) * 100, 100 - (v / bmax) * 100])
  const balPath = bp.length ? 'M' + bp.map((pt) => pt[0].toFixed(2) + ',' + pt[1].toFixed(2)).join(' L') : ''

  /* ---- 导出用电详情 ----
     范围下拉与账期下拉同源。能翻到的月份才能导出。 */
  const exportMonths = monthOptions(
    IS_LIVE ? hist.map((b) => b.month) : keys.map((o) => `${o.y}-${String(o.m + 1).padStart(2, '0')}`),
  )
  const exportDefaultMonth = `${y}-${String(m + 1).padStart(2, '0')}`
  /* mock 无后端。按余额折线口径造数：月内 8/22 充值，月末锚定回推。
     导出必须与页面数据一致。 */
  const mockExportRows = (fromKey: string, toKey: string): UsageExportRow[] => {
    const rows: UsageExportRow[] = []
    for (let idx = monthIndex(fromKey); idx <= monthIndex(toKey); idx++) {
      const key = fromMonthIndex(idx)
      const yy = Number(key.slice(0, 4))
      const mm = Number(key.slice(5, 7)) - 1
      const monthDays = L.filter((x) => x.y === yy && x.m === mm)
      if (monthDays.length === 0) continue
      const net: number[] = []
      let acc = 0
      for (const day of monthDays) {
        acc += (day.d === 8 ? 100 : day.d === 22 ? 80 : 0) - day.kwh * elecRate
        net.push(acc)
      }
      const monthTarget =
        yy === 2026 && mm === 6 ? 43.87 : Math.round((22 + rnd(mm * 7 + yy, 83) * 46) * 100) / 100
      const offset = monthTarget - (net[net.length - 1] ?? 0)
      monthDays.forEach((day, i) => {
        rows.push({
          date: `${day.y}-${String(day.m + 1).padStart(2, '0')}-${String(day.d).padStart(2, '0')}`,
          kwh: Math.round(day.kwh * 100) / 100,
          cost: Math.round(day.kwh * elecRate * 100) / 100,
          balance: Math.max(0, Math.round((net[i] + offset) * 100) / 100),
        })
      })
    }
    return rows
  }

  const label = y + ' 年 ' + (m + 1) + ' 月'

  const monthLabel = label
  const monthVal = mk.k
  const monthOpts = keys.map((o) => ({ v: o.k, l: o.y + ' 年 ' + (o.m + 1) + ' 月' }))
  const resetCal = () => set({ calHover: null })
  const calPrev = () => {
    const i = keys.findIndex((o) => o.k === mk.k)
    if (i < keys.length - 1) set({ mKey: keys[i + 1].k, heat: null, calHover: null })
  }
  const calNext = () => {
    const i = keys.findIndex((o) => o.k === mk.k)
    if (i > 0) set({ mKey: keys[i - 1].k, heat: null, calHover: null })
  }
  const calThis = () => set({ mKey: keys[0].k, heat: null, calHover: null })
  const calPrevOp = keys.findIndex((o) => o.k === mk.k) < keys.length - 1 ? '1' : '.3'
  const calNextOp = keys.findIndex((o) => o.k === mk.k) > 0 ? '1' : '.3'
  const calThisOp = mk.k === keys[0].k ? '.3' : '1'
  const monthNote = IS_LIVE
    ? lCons.loading && !hasBill && !isCurProvisional
      ? '账期数据加载中…'
      : isCurProvisional
        ? '本月 · 日累计（官方账单未出）· 共 ' + days.length + ' 天'
        : billOnly
          ? '官方月账单 · 无逐日明细（读数扫描未覆盖该月）'
          : hasDaily && hasBill
            ? '共 ' + days.length + ' 天有效数据 · 月合计以账单为准'
            : hasDaily
              ? '共 ' + days.length + ' 天有效数据'
              : hasBill
                ? '官方月账单已出'
                : '该账期暂无数据'
    : isCur
      ? '当月数据截至 7/25，共 ' + days.length + ' 天'
      : '完整月份 · 共 ' + days.length + ' 天'

  /* 日历首屏取数：空格用呼吸态。已有数时禁止呼吸，避免刷新闪烁。 */
  const calLoading = IS_LIVE && lCons.loading && !hasDaily
  const calWeekdays = ['一', '二', '三', '四', '五', '六', '日'].map((l) => ({ l: l }))
  const calCells = cells
  /* 月合计：有账单用账单。本月无账单用日累计。其余写暂无数据。 */
  const monthTotalKwh = hasBill
    ? billKwh!
    : isCurProvisional
      ? (monthTotalFromDaily ?? 0)
      : sum
  const hasMonthTotal = hasBill || isCurProvisional || hasDaily
  const calSum = hasMonthTotal
    ? RMB && billCost != null
      ? billCost.toFixed(1)
      : RMB && isCurProvisional
        ? (monthTotalKwh * elecRate).toFixed(1)
        : f1(monthTotalKwh)
    : '—'
  /* 「≈」格仅做单位换算。kWh 视图给钱，元视图给度。
     有官方电费时优先用账单金额。 */
  const calAlt = !hasMonthTotal
    ? '暂无数据'
    : RMB
      ? monthTotalKwh.toFixed(2) + ' kWh'
      : billCost != null
        ? billCost.toFixed(2) + ' 元'
        : alt(monthTotalKwh)
  const na = '暂无数据'
  const calAvg = hasDaily ? f2(sum / days.length) : na
  const calPeak = hasDaily ? f2(peak.kwh) : na
  const calPeakDay = hasDaily ? m + 1 + '/' + peak.d : ''
  const calWeekend = wk.length ? f2(wk.reduce((a, x) => a + x.kwh, 0) / wk.length) : na
  const calWeekday = wdd.length ? f2(wdd.reduce((a, x) => a + x.kwh, 0) / wdd.length) : na
  /* 四行统计占比条。以最高单日为满格。
     比例按原始 kWh 算。切单位禁止改条长。
     无数据时 0 宽空槽，保持行高。 */
  const calPeakBase = peak.kwh || 1
  const calRows: { lab: string; val: string; w: string; sub?: string; red?: boolean }[] = [
    { lab: '日均', val: calAvg, raw: hasDaily ? sum / days.length : 0 },
    { lab: '最高单日', val: calPeak, raw: hasDaily ? peak.kwh : 0, sub: calPeakDay, red: true },
    { lab: '工作日日均', val: calWeekday, raw: wdd.length ? wdd.reduce((a, x) => a + x.kwh, 0) / wdd.length : 0 },
    { lab: '周末日均', val: calWeekend, raw: wk.length ? wk.reduce((a, x) => a + x.kwh, 0) / wk.length : 0 },
  ].map(({ raw, ...r }) => ({
    ...r,
    w: r.val === na ? '0%' : Math.max(0, Math.min(100, (raw / calPeakBase) * 100)).toFixed(1) + '%',
  }))
  /* 七行恒定存在。无数据时空条加破折号。禁止塌成一行暂无数据。 */
  const calWdBars = ['一', '二', '三', '四', '五', '六', '日'].map((l, i) => ({
    l: l,
    v: hasDaily ? f2(wdAvg[i]) : '—',
    w: hasDaily ? ((wdAvg[i] / wdMax) * 100).toFixed(1) + '%' : '0%',
    bg: wdAvg[i] === wdMax ? RED : 'color-mix(in srgb, var(--fg) 26%, transparent)',
  }))
  const calLegend = [0.12, 0.32, 0.55, 0.78, 1].map((o) => ({ o: String(o) }))

  const balArea = balPath + ' L100,100 L0,100 Z'
  const balAxisTop = String(bmax)
  const balAxisMid = String(bmax / 2)
  const balMarkers = days
    .map((x, i) => ({ x: x, i: i }))
    .filter((o) => rec[o.i] > 0)
    .map((o) => ({
      x: ((days.length === 1 ? 0 : o.i / (days.length - 1)) * 100).toFixed(2) + '%',
      y: ((bal[o.i] / bmax) * 100).toFixed(2) + '%',
      label: '+' + rec[o.i],
      title: m + 1 + '/' + o.x.d + ' 充值 ' + rec[o.i] + ' 元',
    }))
  const balTipIndex = balFocus == null || !balancePoints.length ? null : Math.min(balFocus, balancePoints.length - 1)
  const balTip = balTipIndex == null
    ? null
    : (() => {
        const point = balancePoints[balTipIndex]
        const [x, yPos] = bp[balTipIndex]
        const previous = balTipIndex > 0 ? balancePoints[balTipIndex - 1].value : null
        const delta = previous == null ? null : point.value - previous
        return {
          x,
          y: yPos,
          tx: x < 16 ? '-6px' : x > 84 ? 'calc(-100% + 6px)' : '-50%',
          date: `${point.date.getMonth() + 1} 月 ${point.date.getDate()} 日 · 周${WDN[point.date.getDay()]}`,
          value: point.value.toFixed(2),
          delta: delta == null ? '该账期首条余额记录' : `较上一条 ${delta >= 0 ? '+' : ''}${delta.toFixed(2)} 元`,
        }
      })()
  const focusBalancePointer = (e: ReactPointerEvent<HTMLDivElement>) => {
    const rect = e.currentTarget.getBoundingClientRect()
    setBalFocus(nearestBalancePointIndex(e.clientX, rect.left, rect.width, balancePoints.length))
  }
  const moveBalanceKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (!balancePoints.length) return
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      e.preventDefault()
      const base = balTipIndex ?? balancePoints.length - 1
      setBalFocus(Math.max(0, Math.min(balancePoints.length - 1, base + (e.key === 'ArrowLeft' ? -1 : 1))))
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      setBalFocus(balTipIndex == null ? balancePoints.length - 1 : null)
    } else if (e.key === 'Escape') {
      setBalFocus(null)
    }
  }
  const mTickA = m + 1 + '/1'
  const mTickB = m + 1 + '/' + Math.round(dim / 2)
  const mTickC = days.length ? m + 1 + '/' + days[days.length - 1].d : m + 1 + '/' + dim

  /* 用电分析报告：对比本表与同层/同栋/全校。
     结论仅来自数据差额。禁止写电器用法指引。
     结论必须指回具体数字。 */
  const myB = (IS_LIVE && s.user?.building) || ''
  const myF = (IS_LIVE && s.user?.floor) || ''
  const anOn = IS_LIVE && !!liveMeter && !!myB
  const anWin = { from: mWin.from, to: mWin.to }
  const anFloorSum = useLiveCampusSummary(myB, myF, anOn && myF ? anWin : null)
  const anBldgSum = useLiveCampusSummary(myB, undefined, anOn ? anWin : null)
  const anAllSum = useLiveCampusSummary(undefined, undefined, anOn ? anWin : null)
  /* 同栋逐日序列。用于同栋工作日/周末节奏与峰值日户均。 */
  const anBldgSeries = useLiveCampusSeries(anOn ? { ...anWin, granularity: 'day' as const } : null, myB)

  /* 日/夜用电。仅用本表抄表区间。无楼栋也可。
     availability 非 ready 时整块不渲染。禁止凑数。 */
  const lDayNight = useLiveDayNight(liveMeter, IS_LIVE && liveMeter ? anWin : null)
  const dn = lDayNight.data?.availability === 'ready' ? lDayNight.data : null
  const dnRows = dn
    ? (() => {
        const rows = [
          { k: '白天', win: dn.day_window, s: dn.day, me: false },
          { k: '夜间', win: dn.night_window, s: dn.night, me: false },
        ]
        // 每小时功率高者标红。时长不等，比强度用功率。
        const peakRate = Math.max(...rows.map((r) => parseFloat(r.s.kwh_per_hour) || 0))
        return rows.map((r) => {
          const rate = parseFloat(r.s.kwh_per_hour) || 0
          return {
            ...r,
            me: rate >= peakRate && peakRate > 0,
            total: f2(parseFloat(r.s.kwh) || 0),
            rate: f2(rate),
            w: peakRate > 0 ? ((rate / peakRate) * 100).toFixed(1) + '%' : '0%',
          }
        })
      })()
    : []
  /* 结论：两侧每小时功率差。相差不足 10% 视为持平。 */
  const dnGap = dn
    ? (() => {
        const d = parseFloat(dn.day.kwh_per_hour) || 0
        const n = parseFloat(dn.night.kwh_per_hour) || 0
        if (!(d > 0) || !(n > 0)) return null
        const pct = ((n - d) / d) * 100
        if (Math.abs(pct) < 10) return '白天与夜间强度接近'
        return `夜间每小时比白天${pct > 0 ? '高' : '低'} ${Math.abs(pct).toFixed(0)}%`
      })()
    : null

  const anReport = (() => {
    if (!IS_LIVE) return null
    if (!s.user) return { state: 'anon' as const }
    if (!liveMeter) return { state: 'unbound' as const }
    if (!myB) return { state: 'noplace' as const }
    if (!hasDaily) return { state: 'nodaily' as const }
    if (anFloorSum.loading || anBldgSum.loading || anAllSum.loading) return { state: 'loading' as const }

    const myDays = days.length
    const myTotal = sum
    const myDaily = myTotal / myDays
    /* 对比一律折成每户每天。禁止用不同天数的合计直接比。 */
    const bldgPts = (anBldgSeries.data?.points ?? []).filter((p) => p.value != null)
    const scopeDays = bldgPts.length || myDays
    const perDay = (v: number | null) => (v == null || scopeDays <= 0 ? null : v / scopeDays)
    const floorAvg = perDay(anFloorSum.data ? parseFloat(anFloorSum.data.per_room_kwh ?? '') : null)
    const bldgAvg = perDay(anBldgSum.data ? parseFloat(anBldgSum.data.per_room_kwh ?? '') : null)
    const allAvg = perDay(anAllSum.data ? parseFloat(anAllSum.data.per_room_kwh ?? '') : null)
    const ok = (v: number | null): v is number => v != null && Number.isFinite(v) && v > 0

    // ---- 我的节奏
    const myWk = wk.length ? wk.reduce((a, x) => a + x.kwh, 0) / wk.length : null
    const myWd = wdd.length ? wdd.reduce((a, x) => a + x.kwh, 0) / wdd.length : null
    const myWkGap = ok(myWd) && myWk != null ? ((myWk - myWd) / myWd) * 100 : null
    // ---- 同栋节奏。比例无量纲，楼栋总量与个人用量可比。
    const bd = bldgPts.map((p) => ({ wd: new Date(p.period_start).getDay(), v: parseFloat(p.value!) }))
    const bWkArr = bd.filter((x) => x.wd === 0 || x.wd === 6)
    const bWdArr = bd.filter((x) => x.wd !== 0 && x.wd !== 6)
    const bWk = bWkArr.length ? bWkArr.reduce((a, x) => a + x.v, 0) / bWkArr.length : null
    const bWd = bWdArr.length ? bWdArr.reduce((a, x) => a + x.v, 0) / bWdArr.length : null
    const bWkGap = ok(bWd) && bWk != null ? ((bWk - bWd) / bWd) * 100 : null

    const sorted = days.slice().sort((a, b) => a.d - b.d)
    const last7 = sorted.slice(-7)
    const prev7 = sorted.slice(-14, -7)
    const l7 = last7.length ? last7.reduce((a, x) => a + x.kwh, 0) / last7.length : null
    const p7 = prev7.length ? prev7.reduce((a, x) => a + x.kwh, 0) / prev7.length : null
    const trendGap = ok(p7) && l7 != null ? ((l7 - p7) / p7) * 100 : null

    const low = days.reduce((a, x) => (x.kwh < a.kwh ? x : a), days[0])

    /* ---- 结论：一条一句，只报数与差额。 */
    type Tip = { tone: 'warn' | 'ok' | 'info'; text: string }
    const tips: Tip[] = []
    const ref: { name: string; v: number }[] = []
    if (ok(floorAvg)) ref.push({ name: '同层', v: floorAvg })
    if (ok(bldgAvg)) ref.push({ name: '同栋', v: bldgAvg })
    if (ok(allAvg)) ref.push({ name: '全校', v: allAvg })

    const base = ok(floorAvg) ? { name: '同层', v: floorAvg } : ref[0]
    if (base) {
      const gap = ((myDaily - base.v) / base.v) * 100
      if (gap > 10) {
        const cut = myDaily - base.v
        tips.push({
          tone: 'warn',
          text: `比${base.name}户均高 ${gap.toFixed(0)}% · 日均 ${f2(myDaily)} / ${f2(base.v)} ${U} · 追平需每天少 ${f2(cut)}`,
        })
      } else if (gap < -10) {
        tips.push({
          tone: 'ok',
          text: `比${base.name}户均低 ${Math.abs(gap).toFixed(0)}% · 日均 ${f2(myDaily)} / ${f2(base.v)} ${U}`,
        })
      } else {
        tips.push({
          tone: 'info',
          text: `与${base.name}户均持平 · ${gap > 0 ? '+' : ''}${gap.toFixed(0)}%`,
        })
      }
    }
    if (ref.length >= 2) {
      const above = ref.filter((r) => myDaily > r.v)
      if (above.length === ref.length)
        tips.push({ tone: 'warn', text: `${ref.map((r) => r.name).join('、')}均高于户均` })
      else if (above.length === 0)
        tips.push({ tone: 'ok', text: `${ref.map((r) => r.name).join('、')}均低于户均` })
      else
        tips.push({
          tone: 'info',
          text: `高于${above.map((r) => r.name).join('、')}户均，低于${ref.filter((r) => myDaily <= r.v).map((r) => r.name).join('、')}户均`,
        })
    }
    if (myWkGap != null && bWkGap != null && Math.abs(myWkGap - bWkGap) > 15) {
      tips.push({
        tone: myWkGap > bWkGap ? 'info' : 'ok',
        text:
          `周末比工作日${myWkGap > 0 ? '高' : '低'} ${Math.abs(myWkGap).toFixed(0)}%，` +
          `同栋${bWkGap > 0 ? '高' : '低'} ${Math.abs(bWkGap).toFixed(0)}%`,
      })
    }
    if (peak && dAvg > 0 && peak.kwh > dAvg * 1.8) {
      const sameDay = bldgPts.find((p) => new Date(p.period_start).getDate() === peak.d)
      const bMeters = anBldgSum.data?.meters ?? 0
      const bSame = sameDay && bMeters > 0 ? parseFloat(sameDay.value!) / bMeters : null
      tips.push({
        tone: 'info',
        text:
          `${m + 1}/${peak.d} 最高 ${f2(peak.kwh)} ${U} · 日均的 ${(peak.kwh / dAvg).toFixed(1)} 倍` +
          (bSame != null ? ` · 同栋当天户均 ${f2(bSame)}` : ''),
      })
    }
    if (trendGap != null && Math.abs(trendGap) > 15) {
      tips.push({
        tone: trendGap > 0 ? 'warn' : 'ok',
        text: `近 7 天日均 ${f2(l7!)} ${U} · 较前 7 天${trendGap > 0 ? '涨' : '降'} ${Math.abs(trendGap).toFixed(0)}%`,
      })
    }
    /* 余额可撑天数 = 当前余额 ÷ 当前日均电费。全部来自实测。 */
    const curBal = bal.length ? bal[bal.length - 1] : null
    if (curBal != null && myDaily > 0) {
      const daysLeft = Math.floor(curBal / (myDaily * elecRate))
      tips.push({
        tone: daysLeft <= 7 ? 'warn' : 'info',
        text: `余额 ${curBal.toFixed(2)} 元 · 按当月日均约够 ${daysLeft} 天`,
      })
    }
    if (myDays < 5)
      tips.push({ tone: 'info', text: `仅 ${myDays} 天有抄表数据 · 样本偏少` })

    const bars = [
      { name: '我', v: myDaily, me: true },
      ...(ok(floorAvg) ? [{ name: '同层户均', v: floorAvg, me: false }] : []),
      ...(ok(bldgAvg) ? [{ name: '同栋户均', v: bldgAvg, me: false }] : []),
      ...(ok(allAvg) ? [{ name: '全校户均', v: allAvg, me: false }] : []),
    ]
    return {
      state: 'ready' as const,
      myDaily,
      myDays,
      scopeDays,
      bars,
      peak,
      low,
      myWk,
      myWd,
      myWkGap,
      bWkGap,
      l7,
      p7,
      trendGap,
      tips,
      place: [myB, myF].filter(Boolean).join(' · '),
      quality: anBldgSum.data?.quality,
      err: anFloorSum.error || anBldgSum.error || anAllSum.error,
    }
  })()

  const unitLabel = U
  return (
    <div data-screen-label="用电分析" style={{ animation: 'rise .28s ease both' }}>
      <section
        data-r="hdr"
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: '18px',
          flexWrap: 'wrap',
          padding: '34px 0 26px',
          borderBottom: '1px solid var(--line)',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'baseline', gap: '14px' }}>
          <span style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
            STATEMENT PERIOD
          </span>
          {monthNote === '账期数据加载中…' ? (
            <InlineNote text="账期数据读取中" />
          ) : (
            <span style={{ fontSize: '12.5px', color: 'var(--fg3)' }}>{monthNote}</span>
          )}
        </div>
        <div data-r="hdrside" style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: '12px' }}>
          <span style={{ fontSize: '12.5px', color: 'var(--fg2)' }}>账期</span>
          <select
            value={monthVal}
            onChange={(e) => set({ mKey: e.target.value, heat: null, calHover: null })}
            style={{
              padding: '9px 14px',
              border: '1px solid var(--fg)',
              background: 'var(--bg)',
              color: 'var(--fg)',
              font: "600 13px/1 'Instrument Sans','Noto Sans SC',sans-serif",
              cursor: 'pointer',
            }}
          >
            {monthOpts.map((o) => (
              <option key={o.v} value={o.v}>
                {o.l}
              </option>
            ))}
          </select>
        </div>
      </section>

      <section style={{ padding: '44px 0 40px', borderBottom: '1px solid var(--line)' }}>
        <div data-r="hdr" style={{ display: 'flex', alignItems: 'flex-end', gap: '38px', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
            <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
              USAGE CALENDAR · {monthLabel}
            </div>
            <div style={{ fontSize: '34px', fontWeight: 600, letterSpacing: '-.04em' }}>用电日历</div>
          </div>
          {/* 下载放在日历标题旁。日历即逐日用电本身。 */}
          <div data-r="hdrside" style={{ marginLeft: 'auto', marginBottom: '4px' }}>
            <UsageExport
              meter={s.user?.meter || ''}
              place={s.user?.place || ''}
              rate={elecRate}
              months={exportMonths}
              defaultMonth={exportDefaultMonth}
              mockRows={mockExportRows}
            />
          </div>
        </div>
        {/* 有账单无日序列：格子空，月合计仍是官方数字。必须说明。 */}
        {billOnly && (
          <div
            style={{
              marginTop: '18px',
              padding: '14px 16px',
              border: '1px solid var(--line)',
              background: 'var(--sub)',
              fontSize: '12.5px',
              color: 'var(--fg2)',
              lineHeight: 1.65,
              textWrap: 'pretty',
            }}
          >
            本月有官方账单 · Month Total{' '}
            <span style={{ font: "500 12.5px/1 'JetBrains Mono',monospace", color: 'var(--fg)' }}>
              {RMB && billCost != null ? billCost.toFixed(2) + ' 元' : f1(billKwh!) + ' ' + U}
            </span>
            {billCost != null && !RMB ? '（电费 ' + billCost.toFixed(2) + ' 元）' : ''}
            。逐日用电、峰值、分时等暂无数据。
          </div>
        )}

        <div data-r="calwrap" style={{ display: 'flex', alignItems: 'stretch', marginTop: '30px' }}>
          <div style={{ flex: 'none' }}>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(7,44px)', gap: '4px' }}>
              {calWeekdays.map((w, i) => (
                <div
                  key={i}
                  style={{
                    font: "500 9.5px/1 'JetBrains Mono',monospace",
                    color: 'var(--fg3)',
                    textAlign: 'center',
                    paddingBottom: '6px',
                  }}
                >
                  {w.l}
                </div>
              ))}
            </div>
            <div
              key={mk.k}
              className="chart-anim"
              onMouseLeave={resetCal}
              style={{ display: 'grid', gridTemplateColumns: 'repeat(7,44px)', gap: '4px' }}
            >
              {calCells.map((c, i) => (
                <div
                  key={i}
                  onMouseEnter={c.on ?? undefined}
                  onClick={c.tap ?? undefined}
                  className={calLoading && c.d ? 'skel' : undefined}
                  style={{
                    position: 'relative',
                    width: '44px',
                    height: '44px',
                    background: 'var(--line2)',
                    animationDelay: calLoading ? (i % 7) * 0.07 + 's' : undefined,
                    cursor: c.tap ? 'pointer' : 'default',
                  }}
                >
                  <div style={{ position: 'absolute', inset: 0, background: 'var(--red)', opacity: c.o }} />
                  {/* 标记环画在红层之上。内嵌底色发丝分隔环与填充。 */}
                  {c.ring ? (
                    <div
                      style={{
                        position: 'absolute',
                        inset: 0,
                        border: '2px solid ' + c.ring,
                        boxShadow: 'inset 0 0 0 1px var(--bg)',
                        pointerEvents: 'none',
                      }}
                    />
                  ) : null}
                  <div
                    style={{
                      position: 'absolute',
                      left: '5px',
                      top: '4px',
                      font: "500 9px/1 'JetBrains Mono',monospace",
                      color: c.fg,
                    }}
                  >
                    {c.d}
                  </div>
                  {c.val ? (
                    /* 数值为主：字号压过日期，独占下半部，右对齐成列。 */
                    <div
                      style={{
                        position: 'absolute',
                        left: '4px',
                        right: '5px',
                        bottom: '5px',
                        font: "600 12px/1 'JetBrains Mono',monospace",
                        color: '#fff',
                        textAlign: 'right',
                        letterSpacing: '-.03em',
                        fontVariantNumeric: 'tabular-nums',
                        whiteSpace: 'nowrap',
                        overflow: 'hidden',
                      }}
                    >
                      {c.val}
                    </div>
                  ) : null}
                  {c.hov && (
                    <div
                      style={{
                        position: 'absolute',
                        left: 0,
                        bottom: 'calc(100% + 9px)',
                        transform: c.tx,
                        zIndex: 14,
                        pointerEvents: 'none',
                        border: '1px solid var(--fg)',
                        background: 'var(--bg)',
                        padding: '11px 13px',
                        display: 'flex',
                        flexDirection: 'column',
                        gap: '7px',
                        minWidth: '138px',
                        boxShadow: '0 12px 32px rgba(0,0,0,.18)',
                      }}
                    >
                      <span
                        style={{
                          font: "400 10px/1 'JetBrains Mono',monospace",
                          color: 'var(--fg3)',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        {c.tipDate}
                      </span>
                      <span style={{ display: 'flex', alignItems: 'baseline', gap: '5px' }}>
                        <span
                          style={{
                            fontSize: '21px',
                            fontWeight: 600,
                            letterSpacing: '-.035em',
                            lineHeight: 1,
                            fontVariantNumeric: 'tabular-nums',
                          }}
                        >
                          {c.tipVal}
                        </span>
                        <span style={{ fontSize: '11.5px', color: 'var(--fg2)' }}>{c.tipUnit}</span>
                      </span>
                      <span
                        style={{
                          font: "400 10.5px/1 'JetBrains Mono',monospace",
                          color: 'var(--fg3)',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        {c.tipAlt}
                      </span>
                      <span
                        style={{ fontSize: '11px', fontWeight: 500, whiteSpace: 'nowrap', color: c.tipDeltaColor }}
                      >
                        {c.tipDelta}
                      </span>
                    </div>
                  )}
                </div>
              ))}
            </div>
            {/* 图例上方补口径说明。单位跟随 s.unit，不塞进每格。 */}
            <div
              style={{
                display: 'flex',
                alignItems: 'baseline',
                gap: '7px',
                marginTop: '15px',
              }}
            >
              <span
                style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}
              >
                {RMB ? 'CNY / DAY' : 'KWH / DAY'}
              </span>
              <span style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>
                格内数字为当日{RMB ? '电费（元）' : '用电（kWh）'}
              </span>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginTop: '13px' }}>
              <span style={{ fontSize: '11px', color: 'var(--fg3)' }}>低</span>
              {calLegend.map((g, i) => (
                <span key={i} style={{ width: '11px', height: '11px', background: 'var(--red)', opacity: g.o }} />
              ))}
              <span style={{ fontSize: '11px', color: 'var(--fg3)' }}>高</span>
              <span
                style={{
                  marginLeft: 'auto',
                  display: 'flex',
                  alignItems: 'center',
                  gap: '6px',
                  fontSize: '11px',
                  color: 'var(--fg3)',
                }}
              >
                <span style={{ width: '11px', height: '11px', border: '2px solid var(--red)' }} />
                今日
              </span>
              <span style={{ display: 'flex', alignItems: 'center', gap: '6px', fontSize: '11px', color: 'var(--fg3)' }}>
                <span style={{ width: '11px', height: '11px', border: '2px solid var(--fg)' }} />
                峰值
              </span>
            </div>
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '6px',
                marginTop: '14px',
                paddingTop: '14px',
                borderTop: '1px solid var(--line2)',
              }}
            >
              <button
                onClick={calPrev}
                className="hv-line-fg"
                style={{
                  background: 'none',
                  border: '1px solid var(--line)',
                  margin: 0,
                  padding: '8px 12px',
                  font: 'inherit',
                  fontSize: '12px',
                  cursor: 'pointer',
                  color: 'var(--fg2)',
                  opacity: calPrevOp,
                }}
              >
                ← 上一月
              </button>
              <button
                onClick={calNext}
                className="hv-line-fg"
                style={{
                  background: 'none',
                  border: '1px solid var(--line)',
                  margin: 0,
                  padding: '8px 12px',
                  font: 'inherit',
                  fontSize: '12px',
                  cursor: 'pointer',
                  color: 'var(--fg2)',
                  opacity: calNextOp,
                }}
              >
                下一月 →
              </button>
              <button
                onClick={calThis}
                className="hv-fg"
                style={{
                  marginLeft: 'auto',
                  background: 'none',
                  border: 0,
                  padding: '8px 0',
                  font: 'inherit',
                  fontSize: '12px',
                  fontWeight: 500,
                  cursor: 'pointer',
                  color: 'var(--red)',
                  opacity: calThisOp,
                }}
              >
                回到本月
              </button>
            </div>
          </div>

          <div
            data-r="calside"
            style={{
              flex: 1,
              minWidth: '280px',
              paddingLeft: '44px',
              marginLeft: '44px',
              borderLeft: '1px solid var(--line)',
              display: 'flex',
              flexDirection: 'column',
            }}
          >
            <div style={{ display: 'flex', flexDirection: 'column', gap: '5px' }}>
              <span
                style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}
              >
                MONTH TOTAL{hasBill ? ' · BILL' : isCurProvisional ? ' · 本月' : ''}
              </span>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: '7px', marginTop: '6px' }}>
                <span
                  style={{
                    fontSize: '38px',
                    fontWeight: 600,
                    letterSpacing: '-.04em',
                    lineHeight: 1,
                    fontVariantNumeric: 'tabular-nums',
                  }}
                >
                  {calSum}
                </span>
                <span style={{ fontSize: '13px', color: 'var(--fg2)' }}>{unitLabel || (RMB ? '元' : 'kWh')}</span>
                <span style={{ fontSize: '12.5px', color: 'var(--fg3)', marginLeft: '6px' }}>≈ {calAlt}</span>
              </div>
              {/* live 下说明行位置先占住，避免账单到达后布局跳动。
                  mock 无异步，不占位。 */}
              {(IS_LIVE || hasBill || isCurProvisional) && (
                <span
                  style={{
                    fontSize: '11.5px',
                    color: 'var(--fg3)',
                    marginTop: '6px',
                    minHeight: IS_LIVE ? '16px' : undefined,
                  }}
                >
                  {hasBill || isCurProvisional
                    ? isCurProvisional
                      ? '本月日累计 · 账单未出'
                      : billOnly
                        ? '官方账单 · 月合计'
                        : '官方账单口径（优先于日序列加总）'
                    : ''}
                </span>
              )}
            </div>
            {/* 标签、占比条、数值三段定宽。标签列定宽使条左对齐。 */}
            <div style={{ display: 'flex', flexDirection: 'column', marginTop: '22px' }}>
              {calRows.map((r, i, arr) => {
                const empty = r.val === na
                return (
                  <div
                    key={r.lab}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: '12px',
                      padding: '11px 0',
                      borderTop: '1px solid var(--line2)',
                      borderBottom: i === arr.length - 1 ? '1px solid var(--line2)' : undefined,
                    }}
                  >
                    <span
                      style={{
                        width: '96px',
                        flex: 'none',
                        display: 'flex',
                        alignItems: 'baseline',
                        gap: '8px',
                      }}
                    >
                      <span style={{ fontSize: '12.5px', color: 'var(--fg2)' }}>{r.lab}</span>
                      {r.sub ? <span style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>{r.sub}</span> : null}
                    </span>
                    <span
                      className={calLoading ? 'skel' : undefined}
                      style={{
                        flex: 1,
                        minWidth: 0,
                        height: '11px',
                        background: 'var(--line2)',
                        animationDelay: calLoading ? i * 0.07 + 's' : undefined,
                      }}
                    >
                      <span
                        style={{
                          display: 'block',
                          height: '100%',
                          width: r.w,
                          background: r.red ? RED : 'color-mix(in srgb, var(--fg) 26%, transparent)',
                        }}
                      />
                    </span>
                    <span
                      style={{
                        width: '84px',
                        flex: 'none',
                        textAlign: 'right',
                        font: empty
                          ? "400 12.5px/1 'Instrument Sans','Noto Sans SC',sans-serif"
                          : "500 13px/1 'JetBrains Mono',monospace",
                        fontVariantNumeric: 'tabular-nums',
                        color: empty ? 'var(--fg3)' : r.red ? 'var(--red)' : undefined,
                      }}
                    >
                      {empty ? '—' : r.val + (unitLabel ? ' ' + unitLabel : '')}
                    </span>
                  </div>
                )
              })}
            </div>
            <div
              style={{
                font: "500 9.5px/1 'JetBrains Mono',monospace",
                letterSpacing: '.2em',
                color: 'var(--fg3)',
                marginTop: '26px',
              }}
            >
              BY WEEKDAY
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', marginTop: '8px' }}>
              {calWdBars.map((w, i) => (
                <div key={i} style={{ display: 'flex', alignItems: 'center', gap: '12px', padding: '7px 0' }}>
                  <span style={{ width: '16px', flex: 'none', fontSize: '12px', color: 'var(--fg2)' }}>{w.l}</span>
                  <span
                    className={calLoading ? 'skel' : undefined}
                    style={{
                      flex: 1,
                      minWidth: 0,
                      height: '11px',
                      background: 'var(--line2)',
                      animationDelay: calLoading ? i * 0.07 + 's' : undefined,
                    }}
                  >
                    <span style={{ display: 'block', height: '100%', background: w.bg, width: w.w }} />
                  </span>
                  {/* 84px 与上方统计数值列同宽，两组条右端对齐。 */}
                  <span
                    style={{
                      width: '84px',
                      flex: 'none',
                      textAlign: 'right',
                      font: "500 11.5px/1 'JetBrains Mono',monospace",
                      fontVariantNumeric: 'tabular-nums',
                      color: hasDaily ? undefined : 'var(--fg3)',
                    }}
                  >
                    {w.v}
                  </span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </section>

      <section style={{ padding: '44px 0 46px', borderBottom: '1px solid var(--line)' }}>
        <div style={{ display: 'flex', alignItems: 'flex-end', gap: '38px', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
            <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
              BALANCE HISTORY · {monthLabel}
            </div>
            <div style={{ fontSize: '34px', fontWeight: 600, letterSpacing: '-.04em' }}>余额变化</div>
          </div>
          <div data-r="hdrside" style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: '22px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '7px' }}>
              <span style={{ width: '15px', height: '2px', background: 'var(--fg)' }} />
              <span style={{ fontSize: '11.5px', color: 'var(--fg2)' }}>余额</span>
            </div>
            {!IS_LIVE && (
              <div style={{ display: 'flex', alignItems: 'center', gap: '7px' }}>
                <span style={{ width: '9px', height: '9px', borderRadius: '99px', background: 'var(--red)' }} />
                <span style={{ fontSize: '11.5px', color: 'var(--fg2)' }}>充值记录</span>
              </div>
            )}
          </div>
        </div>
        <div style={{ display: 'flex', gap: '16px', marginTop: '36px' }}>
          <div
            style={{
              width: '40px',
              flex: 'none',
              display: 'flex',
              flexDirection: 'column',
              justifyContent: 'space-between',
              font: "400 10.5px/1 'JetBrains Mono',monospace",
              color: 'var(--fg3)',
              textAlign: 'right',
              height: '250px',
            }}
          >
            <span>{balAxisTop}</span>
            <span>{balAxisMid}</span>
            <span>0</span>
          </div>
          <div
            key={mk.k + '-' + s.unit}
            className="chart-anim"
            style={{ flex: 1, position: 'relative', height: '250px', borderBottom: '1px solid var(--line)' }}
          >
            <div style={{ position: 'absolute', left: 0, right: 0, top: 0, borderTop: '1px dashed var(--line2)' }} />
            <div style={{ position: 'absolute', left: 0, right: 0, top: '50%', borderTop: '1px dashed var(--line2)' }} />
            <svg
              viewBox="0 0 100 100"
              preserveAspectRatio="none"
              style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', overflow: 'visible', pointerEvents: 'none' }}
            >
              <path d={balArea} fill="var(--fg)" opacity=".06" />
              <path
                d={balPath}
                fill="none"
                stroke="var(--fg)"
                strokeWidth="2"
                vectorEffect="non-scaling-stroke"
                strokeLinejoin="round"
                strokeLinecap="round"
              />
            </svg>
            {balMarkers.map((mm, i) => (
              <div
                key={i}
                title={mm.title}
                style={{ position: 'absolute', transform: 'translate(-50%,50%)', left: mm.x, bottom: mm.y, pointerEvents: 'none' }}
              >
                <div
                  style={{
                    width: '10px',
                    height: '10px',
                    borderRadius: '99px',
                    background: 'var(--red)',
                    boxShadow: '0 0 0 3px var(--bg)',
                  }}
                />
                <div
                  style={{
                    position: 'absolute',
                    left: '50%',
                    transform: 'translateX(-50%)',
                    bottom: '17px',
                    whiteSpace: 'nowrap',
                    font: "500 10.5px/1 'JetBrains Mono',monospace",
                    color: 'var(--red)',
                  }}
                >
                  {mm.label}
                </div>
              </div>
            ))}
            <div
              data-balance-chart
              role="img"
              aria-label="余额变化折线图；悬停、触摸或使用左右方向键查看每条记录"
              tabIndex={balancePoints.length ? 0 : -1}
              onPointerDown={focusBalancePointer}
              onPointerMove={focusBalancePointer}
              onPointerLeave={(e) => {
                if (e.pointerType === 'mouse') setBalFocus(null)
              }}
              onBlur={() => setBalFocus(null)}
              onKeyDown={moveBalanceKey}
              style={{ position: 'absolute', inset: 0, zIndex: 10, cursor: 'crosshair', touchAction: 'pan-y', outline: 'none' }}
            >
              {balTip && (
                <>
                  <div
                    style={{
                      position: 'absolute',
                      left: balTip.x + '%',
                      top: 0,
                      bottom: 0,
                      borderLeft: '1px dashed var(--line)',
                      pointerEvents: 'none',
                    }}
                  />
                  <div
                    style={{
                      position: 'absolute',
                      left: balTip.x + '%',
                      top: balTip.y + '%',
                      transform: 'translate(-50%,-50%)',
                      pointerEvents: 'none',
                    }}
                  >
                    <div
                      style={{
                        width: '9px',
                        height: '9px',
                        borderRadius: '99px',
                        background: 'var(--red)',
                        boxShadow: '0 0 0 3px var(--bg)',
                      }}
                    />
                  </div>
                  <div
                    data-balance-tooltip
                    role="status"
                    aria-live="polite"
                    style={{
                      position: 'absolute',
                      left: balTip.x + '%',
                      top: balTip.y + '%',
                      transform: `translate(${balTip.tx},${balTip.y < 45 ? '18px' : 'calc(-100% - 18px)'})`,
                      pointerEvents: 'none',
                      border: '1px solid var(--fg)',
                      background: 'var(--bg)',
                      padding: '11px 13px',
                      display: 'flex',
                      flexDirection: 'column',
                      gap: '7px',
                      minWidth: '150px',
                      boxShadow: '0 12px 32px rgba(0,0,0,.18)',
                    }}
                  >
                    <span style={{ font: "400 10px/1 'JetBrains Mono',monospace", color: 'var(--fg3)', whiteSpace: 'nowrap' }}>
                      {balTip.date} · 余额
                    </span>
                    <span style={{ display: 'flex', alignItems: 'baseline', gap: '5px' }}>
                      <span
                        style={{
                          fontSize: '21px',
                          fontWeight: 600,
                          letterSpacing: '-.035em',
                          lineHeight: 1,
                          fontVariantNumeric: 'tabular-nums',
                        }}
                      >
                        {balTip.value}
                      </span>
                      <span style={{ fontSize: '11.5px', color: 'var(--fg2)' }}>元</span>
                    </span>
                    <span style={{ fontSize: '11px', color: 'var(--fg3)', whiteSpace: 'nowrap' }}>{balTip.delta}</span>
                  </div>
                </>
              )}
            </div>
          </div>
        </div>
        <div
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            marginLeft: '56px',
            marginTop: '11px',
            font: "400 10.5px/1 'JetBrains Mono',monospace",
            color: 'var(--fg3)',
          }}
        >
          <span>{mTickA}</span>
          <span>{mTickB}</span>
          <span>{mTickC}</span>
        </div>
        {IS_LIVE && <LiveNote text={availNote(lBal.data?.availability, lBal.data?.quality, lBal.error)} />}
      </section>

      {/* ---- 用电分析报告。对比同层/同栋/全校。结论仅来自数据差额。 */}
      {IS_LIVE && anReport && (
        <section style={{ padding: '44px 0 46px', borderBottom: '1px solid var(--line)' }}>
          <div data-r="hdr" style={{ display: 'flex', alignItems: 'flex-end', gap: '20px', flexWrap: 'wrap' }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
              <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
                CONSUMPTION REPORT · {label}
              </div>
              <div style={{ fontSize: '34px', fontWeight: 600, letterSpacing: '-.04em' }}>用电分析</div>
            </div>
            {anReport.state === 'ready' && (
              <span data-r="hdrside" style={{ marginLeft: 'auto', fontSize: '12px', color: 'var(--fg3)' }}>
                {anReport.place} · 我 {anReport.myDays} 天 / 同栋 {anReport.scopeDays} 天有抄表
              </span>
            )}
          </div>

          {anReport.state !== 'ready' ? (
            <div
              style={{
                marginTop: '26px',
                paddingTop: '22px',
                borderTop: '1px solid var(--line)',
                fontSize: '13px',
                color: 'var(--fg2)',
                lineHeight: 1.8,
                textWrap: 'pretty',
              }}
            >
              {anReport.state === 'anon' && '登录后可与同层、同栋、全校户均对比。'}
              {anReport.state === 'unbound' && '绑定电表后可与同层、同栋、全校户均对比。'}
              {anReport.state === 'noplace' && '电表未关联楼栋或楼层。无法做同层同栋对比。请联系管理员。'}
              {anReport.state === 'nodaily' && '该账期只有官方月账单。无逐日数据。'}
              {anReport.state === 'loading' && '正在读取同层、同栋、全校对照数据…'}
            </div>
          ) : (
            <>
              <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', marginTop: '30px' }}>
                {/* 左：本表节奏 */}
                <div style={{ paddingRight: '44px', display: 'flex', flexDirection: 'column' }}>
                  <div
                    style={{
                      font: "500 9px/1 'JetBrains Mono',monospace",
                      letterSpacing: '.18em',
                      color: 'var(--fg3)',
                      marginBottom: '18px',
                    }}
                  >
                    RHYTHM · 本月节奏
                  </div>
                  {[
                    { k: '日均', v: f2(anReport.myDaily) + ' ' + U, sub: anReport.myDays + ' 天平均' },
                    {
                      k: '最高的一天',
                      v: f2(anReport.peak.kwh) + ' ' + U,
                      sub: m + 1 + ' 月 ' + anReport.peak.d + ' 日 · 日均的 ' + (anReport.peak.kwh / (dAvg || 1)).toFixed(1) + ' 倍',
                    },
                    {
                      k: '最低的一天',
                      v: f2(anReport.low.kwh) + ' ' + U,
                      sub: m + 1 + ' 月 ' + anReport.low.d + ' 日',
                    },
                    {
                      k: '工作日 / 周末',
                      v:
                        (anReport.myWd == null ? '—' : f2(anReport.myWd)) +
                        ' / ' +
                        (anReport.myWk == null ? '—' : f2(anReport.myWk)) +
                        ' ' +
                        U,
                      sub:
                        anReport.myWkGap == null
                          ? '样本不足'
                          : '周末' +
                            (anReport.myWkGap > 0 ? '高 ' : '低 ') +
                            Math.abs(anReport.myWkGap).toFixed(0) +
                            '%' +
                            (anReport.bWkGap == null
                              ? ''
                              : ' · 同栋' + (anReport.bWkGap > 0 ? '高 ' : '低 ') + Math.abs(anReport.bWkGap).toFixed(0) + '%'),
                    },
                    {
                      k: '近 7 天 / 前 7 天',
                      v:
                        (anReport.l7 == null ? '—' : f2(anReport.l7)) +
                        ' / ' +
                        (anReport.p7 == null ? '—' : f2(anReport.p7)) +
                        ' ' +
                        U,
                      sub:
                        anReport.trendGap == null
                          ? '样本不足'
                          : (anReport.trendGap > 0 ? '涨 ' : '降 ') + Math.abs(anReport.trendGap).toFixed(0) + '%',
                    },
                  ].map((r) => (
                    <div
                      key={r.k}
                      style={{
                        display: 'flex',
                        alignItems: 'baseline',
                        gap: '12px',
                        padding: '11px 0',
                        borderBottom: '1px solid var(--line2)',
                      }}
                    >
                      <span style={{ fontSize: '13px', color: 'var(--fg2)', width: '104px', flex: 'none' }}>{r.k}</span>
                      <span
                        style={{
                          font: "600 14px/1 'JetBrains Mono',monospace",
                          fontVariantNumeric: 'tabular-nums',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        {r.v}
                      </span>
                      <span
                        style={{
                          marginLeft: 'auto',
                          fontSize: '11.5px',
                          color: 'var(--fg3)',
                          textAlign: 'right',
                          textWrap: 'pretty',
                        }}
                      >
                        {r.sub}
                      </span>
                    </div>
                  ))}
                </div>

                {/* 右：对比。同一把尺（每户每天）。本表红，其余灰。 */}
                <div
                  style={{
                    paddingLeft: '44px',
                    borderLeft: '1px solid var(--line)',
                    display: 'flex',
                    flexDirection: 'column',
                  }}
                >
                  <div
                    style={{
                      font: "500 9px/1 'JetBrains Mono',monospace",
                      letterSpacing: '.18em',
                      color: 'var(--fg3)',
                      marginBottom: '18px',
                    }}
                  >
                    BENCHMARK · 每户每天 {U}
                  </div>
                  {(() => {
                    const mx = Math.max(...anReport.bars.map((b) => b.v), 0.001)
                    return anReport.bars.map((b) => (
                      <div key={b.name} style={{ padding: '10px 0' }}>
                        <div style={{ display: 'flex', alignItems: 'baseline', gap: '10px', marginBottom: '7px' }}>
                          <span style={{ fontSize: '13px', fontWeight: b.me ? 600 : 400, color: b.me ? 'var(--fg)' : 'var(--fg2)' }}>
                            {b.name}
                          </span>
                          <span
                            style={{
                              marginLeft: 'auto',
                              font: "500 13px/1 'JetBrains Mono',monospace",
                              fontVariantNumeric: 'tabular-nums',
                              color: b.me ? 'var(--red)' : 'var(--fg2)',
                            }}
                          >
                            {f2(b.v)}
                          </span>
                        </div>
                        <div style={{ height: '12px', background: 'var(--line2)' }}>
                          <div
                            style={{
                              height: '100%',
                              width: Math.max(2, (b.v / mx) * 100) + '%',
                              background: b.me
                                ? 'var(--red)'
                                : `color-mix(in srgb, var(--fg) ${s.theme === 'dark' ? 34 : 28}%, transparent)`,
                              transition: 'width .35s cubic-bezier(.22,.61,.36,1)',
                            }}
                          />
                        </div>
                      </div>
                    ))
                  })()}
                  <div style={{ fontSize: '11.5px', color: 'var(--fg3)', marginTop: '12px', lineHeight: 1.7, textWrap: 'pretty' }}>
                    户均 = 范围合计 ÷ 当期有数的表数 ÷ 天数；空置房间也在分母里。
                  </div>
                </div>
              </div>

              {/* 结论。每条必须指回具体数字。 */}
              <div style={{ marginTop: '34px', paddingTop: '26px', borderTop: '1px solid var(--line)' }}>
                <div
                  style={{
                    font: "500 9px/1 'JetBrains Mono',monospace",
                    letterSpacing: '.18em',
                    color: 'var(--fg3)',
                    marginBottom: '16px',
                  }}
                >
                  FINDINGS · 结论
                </div>
                {anReport.tips.length === 0 ? (
                  <div style={{ fontSize: '13px', color: 'var(--fg3)' }}>该账期无明显差异。</div>
                ) : (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
                    {anReport.tips.map((t, i) => (
                      <div key={i} style={{ display: 'flex', alignItems: 'flex-start', gap: '12px' }}>
                        <span
                          style={{
                            width: '5px',
                            height: '5px',
                            flex: 'none',
                            marginTop: '8px',
                            background: t.tone === 'warn' ? RED : t.tone === 'ok' ? OK : 'var(--fg3)',
                          }}
                        />
                        <span style={{ fontSize: '13.5px', lineHeight: 1.75, color: 'var(--fg)', textWrap: 'pretty' }}>
                          {t.text}
                        </span>
                      </div>
                    ))}
                  </div>
                )}
                <div style={{ fontSize: '11.5px', color: 'var(--fg3)', marginTop: '18px', lineHeight: 1.7, textWrap: 'pretty' }}>
                  均由你的抄表数与同层 / 同栋 / 全校聚合值算出。
                  {anReport.err ? ' · 对照数据取数失败：' + anReport.err : ''}
                </div>
              </div>
            </>
          )}
        </section>
      )}

      {/* ---- 日/夜。上游一天两次读数。最细拆到两次抄表之间。
           拆不出时 dn 为 null，整块不出现。 */}
      {dn && (
        <section style={{ padding: '44px 0 46px', borderBottom: '1px solid var(--line)' }}>
          <div data-r="hdr" style={{ display: 'flex', alignItems: 'flex-end', gap: '20px', flexWrap: 'wrap' }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
              <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
                DAY / NIGHT · {dn.day_window} · {dn.night_window}
              </div>
              <div style={{ fontSize: '34px', fontWeight: 600, letterSpacing: '-.04em' }}>日间与夜间</div>
            </div>
            {dnGap && (
              <span data-r="hdrside" style={{ marginLeft: 'auto', fontSize: '12.5px', color: 'var(--fg2)' }}>
                {dnGap}
              </span>
            )}
          </div>

          <div style={{ display: 'flex', flexDirection: 'column', marginTop: '28px' }}>
            {dnRows.map((r) => (
              <div key={r.k} style={{ padding: '13px 0', borderTop: '1px solid var(--line2)' }}>
                <div style={{ display: 'flex', alignItems: 'baseline', gap: '10px', marginBottom: '9px', flexWrap: 'wrap' }}>
                  <span style={{ fontSize: '13px', fontWeight: r.me ? 600 : 400 }}>{r.k}</span>
                  <span style={{ font: "400 10.5px/1 'JetBrains Mono',monospace", color: 'var(--fg3)' }}>{r.win}</span>
                  <span
                    style={{
                      marginLeft: 'auto',
                      font: "500 13px/1 'JetBrains Mono',monospace",
                      fontVariantNumeric: 'tabular-nums',
                      color: r.me ? 'var(--red)' : 'var(--fg2)',
                    }}
                  >
                    {r.rate} {U}/h
                  </span>
                  <span
                    style={{
                      width: '92px',
                      textAlign: 'right',
                      font: "400 11.5px/1 'JetBrains Mono',monospace",
                      fontVariantNumeric: 'tabular-nums',
                      color: 'var(--fg3)',
                    }}
                  >
                    共 {r.total} {U}
                  </span>
                </div>
                <div style={{ height: '12px', background: 'var(--line2)' }}>
                  <div
                    style={{
                      height: '100%',
                      width: r.w,
                      background: r.me
                        ? 'var(--red)'
                        : `color-mix(in srgb, var(--fg) ${s.theme === 'dark' ? 34 : 28}%, transparent)`,
                      transition: 'width .35s cubic-bezier(.22,.61,.36,1)',
                    }}
                  />
                </div>
              </div>
            ))}
          </div>
          {/* 口径写在图边。禁止误读为小时数据。 */}
          <div style={{ fontSize: '11.5px', color: 'var(--fg3)', marginTop: '16px', lineHeight: 1.7, textWrap: 'pretty' }}>
            条形比的是每小时功率 —— 两侧时长不等（白天 {dn.day.hours} 小时 / 夜间 {dn.night.hours} 小时），直接比总量会偏向更长的一侧。
            数据来自两次抄表之间的差值，整段归给占时长多的一侧，不按比例劈分；边界吻合度{' '}
            {Math.round(Math.min(dn.day.purity, dn.night.purity) * 100)}%
            {dn.skipped_intervals > 0 ? ` · 跨边界丢弃 ${dn.skipped_intervals} 段` : ''}。
          </div>
        </section>
      )}

      {showHourly && <HourlyUsageSection days={days} m={m} monthLabel={label} />}

    </div>
  )
}
