/* 概览视图。对齐原型 isOverview 区块。
   hero 余额、用电量条形图、最近推送。
   内联样式对应原型。style-hover 用 .hv-* 类。 */

import { useEffect, useRef, useState } from 'react'
import { useStore } from '../lib/store'
import { makeFmt, themeColors, seg } from '../lib/format'
import { daily, personalSeries, type PRange } from '../lib/mock'
import { CH_DEFS, channelPresentation } from '../lib/channels'
import { SegGroup, SegBtn } from '../components/ui'
import { IS_LIVE } from '../api/mode'
import {
  availNote,
  currentMonthKey,
  currentMonthWindow,
  densifyMonthDays,
  monthWindow,
  num,
  rangeWindow,
  seriesMeters,
  seriesSumKwh,
  seriesVals,
  useLiveBills,
  useLiveCampusBills,
  useLiveCampusSeries,
  useLiveMeter,
  useLiveMeterSeries,
  useLivePushLogs,
  useRefreshState,
} from '../api/live'
import LiveNote from '../components/LiveNote'
import { ChartSkeleton, Skeleton } from '../components/Loading'
import RefreshButton from '../components/RefreshButton'
import InlineBindMeter from '../components/InlineBindMeter'
import MonthWindowPicker from '../components/MonthWindowPicker'
import SemesterPicker from '../components/SemesterPicker'
import { monthIndex, monthLabel, monthOptions } from '../lib/months'
import {
  currentSemesterKey,
  findSemester,
  semesterLabel,
  foldWeeks,
  normalizeSemesters,
  semesterWeeks,
  semesterWindow,
} from '../lib/semesters'
import PushHistory, { type PushHistoryNotice } from '../components/PushHistory'
import type { PushLog } from '../api/types'
import { api } from '../api/client'
import { useElementWidth, useIsMobileViewport } from '../lib/useSize'

const PUSH_PAGE_SIZE = 20

export default function OverviewView() {
  const { s, set, startBind } = useStore()
  const dark = s.theme === 'dark'
  const { RED, OK, FG, FG3 } = themeColors(dark)
  const elecRate = parseFloat(s.features?.display?.electricityRate || '') || 0.62
  const fmt = makeFmt(s.unit, elecRate)
  const { RMB, U, cv, nf, f2, alt } = fmt
  const usagePlotRef = useRef<HTMLElement>(null)
  const usagePlotW = useElementWidth(usagePlotRef)
  const mobileChart = useIsMobileViewport()
  const k = daily()
  /* 已登录未绑表：概览不遮罩。数值显示 —。绑表入口在 hero 余额旁。 */
  const unbound = !!s.user && !s.user.meter
  /* 旧状态若仍为 week，落到月。 */
  const pRange: PRange = s.pRange === 'month' || s.pRange === 'term' || s.pRange === 'year' ? s.pRange : 'month'

  // ---- live 数据。mock 下 hooks 空转。
  const meterNo = (IS_LIVE && s.user?.meter) || undefined
  const myBuilding = (IS_LIVE && s.user?.building?.trim()) || undefined
  const lm = useLiveMeter(meterNo)
  /* 年视图走官方月账单。其余周期走读数时序。 */
  const useBillYear = IS_LIVE && pRange === 'year' && !!meterNo
  /* ---- 账期窗口。与数据看板同一套控件与语义。
     年 = [pFrom, pTo] 月区间。月 = 选定自然月按日铺。
     这两档以前是「近 30 天 / 近 365 天」的滚动窗口，想看 3 月只能等它滚过去。
     学期保持滚动 18 周或校历周：按周聚合，落到自然月上没有意义。 */
  const pFrom = s.pFrom
  const pTo = monthIndex(s.pTo) < monthIndex(s.pFrom) ? s.pFrom : s.pTo
  const pIncludesCur = monthIndex(pTo) >= monthIndex(currentMonthKey())
  const monthView = IS_LIVE && pRange === 'month'
  /* 学期：起止由校历决定。横轴为第 1 周…第 N 周。
     取数按天，本地折成校历周。禁止用 ISO 周，以免开学日非周一时串周。
     校历未配时 termSem 为 null，退回近 18 周滚动窗口。 */
  const semesters = normalizeSemesters(s.features?.display?.semesters)
  const termKey = s.pTerm || currentSemesterKey(semesters)
  const termSem = IS_LIVE && pRange === 'term' ? findSemester(semesters, termKey) : null
  const termWeeks = termSem ? semesterWeeks(termSem) : []
  const seriesWin = useBillYear
    ? null
    : monthView
      ? { ...monthWindow(Number(s.pMonth.slice(0, 4)), Number(s.pMonth.slice(5, 7)) - 1), granularity: 'day' as const }
      : termSem
        ? semesterWindow(termSem)
        : rangeWindow(pRange)
  const ls = useLiveMeterSeries(meterNo, 'consumption', seriesWin)
  /* 账单全量取回。年视图柱与月份下拉可选范围都依赖它。 */
  const lBills = useLiveBills(meterNo)
  const curWin = currentMonthWindow()
  const lsCur = useLiveMeterSeries(useBillYear ? meterNo : undefined, 'consumption', curWin)
  /* 同楼平均：同楼栋聚合 ÷ 有数表数。campus 端点匿名可读。 */
  const lcsBldg = useLiveCampusSeries(seriesWin, myBuilding)
  const lcbBldg = useLiveCampusBills(useBillYear && myBuilding ? myBuilding : undefined)
  const lsCurBldg = useLiveCampusSeries(useBillYear && myBuilding ? curWin : null, myBuilding)
  const latest = lm.data?.latest ?? null
  const rf = useRefreshState()
  const lp = useLivePushLogs(!unbound, 5)
  /* 「加载更多」的更早记录仅存本地。首屏 5 条由 useLivePushLogs 托管。
     hook 刷新时必须作废旧页，避免与新首页重复。 */
  const [morePushLogs, setMorePushLogs] = useState<PushLog[]>([])
  const [pushCursor, setPushCursor] = useState<string | null>(null)
  const [pushLoadingMore, setPushLoadingMore] = useState(false)
  useEffect(() => {
    setMorePushLogs([])
    setPushCursor(null)
  }, [lp.data])

  // ---- 余额与状态
  const liveTotal = IS_LIVE ? (latest ? num(latest.total_yuan) : null) : null
  const hasBal = !unbound && (!IS_LIVE || liveTotal != null)
  const total = IS_LIVE ? (liveTotal ?? 0) : 43.87
  const prepaid = IS_LIVE ? (num(latest?.prepaid_yuan) ?? 0) : 43.87
  const subsidy = IS_LIVE ? (num(latest?.subsidy_yuan) ?? 0) : 0
  const totalKwhCum = unbound ? null : IS_LIVE ? num(latest?.total_kwh) : 3356.48
  const recent7 = IS_LIVE ? num(lm.data?.recent_7d_kwh) : null
  const avg7 = IS_LIVE ? (recent7 != null ? recent7 / 7 : 0) : k.slice(23).reduce((a, b) => a + b, 0) / 7
  const avgCost = avg7 * elecRate
  const canEstimate = hasBal && avgCost > 0
  const daysLeft = canEstimate ? Math.floor(total / avgCost) : 0
  const runout = new Date(IS_LIVE ? Date.now() : new Date(2026, 6, 25).getTime())
  runout.setDate(runout.getDate() + daysLeft)
  const low = total <= s.threshold
  const noReading = IS_LIVE && !latest
  const staleReading = IS_LIVE && latest?.freshness === 'stale'
  const statusText = unbound ? '未绑定电表' : noReading ? '暂无数据' : staleReading ? '读数陈旧' : low ? '低于阈值' : '余额正常'
  const statusFg = unbound || noReading ? FG3 : staleReading || low ? RED : OK
  const statusBg =
    unbound || noReading
    ? 'color-mix(in srgb, var(--fg) 6%, transparent)'
    : staleReading || low
      ? 'var(--redsoft)'
      : 'color-mix(in srgb, var(--fg) 6%, transparent)'

  const heroStats = [
    { en: 'PREPAID', label: '预付费余额', v: hasBal ? prepaid.toFixed(2) : '—', u: '元', color: FG },
    { en: 'SUBSIDY', label: '补助电余额', v: hasBal ? subsidy.toFixed(2) : '—', u: '元', color: FG3 },
    {
      en: RMB ? 'TOTAL COST' : 'TOTAL KWH',
      label: '累计用电',
      v: totalKwhCum != null ? nf(cv(totalKwhCum), 1) : '—',
      u: U,
      color: FG,
    },
  ]

  // ---- 周期条形图。live 与用电分析同源。同楼平均 = 本楼栋户均。
  const lsv = seriesVals(ls.data)
  const lsvBldg = seriesVals(lcsBldg.data)
  const thisMonth = currentMonthKey()
  /** 时序桶转户均：总 kWh ÷ 有数表数。 */
  const perRoomFromSeries = (sv: ReturnType<typeof seriesVals>): number[] =>
    sv.vals.map((v, i) => {
      if (v == null) return 0
      const m = sv.meters[i] || 0
      return m > 0 ? v / m : 0
    })
  const liveBillYearBase =
    useBillYear
      ? (() => {
          // 账单仅保留窗口内月份。窗口由用户 RANGE 选定。
          const bills = (lBills.data ?? [])
            .filter((b) => b.month >= pFrom && b.month <= pTo)
            .slice()
            .sort((a, b) => a.month.localeCompare(b.month))
          const bldgByMonth = new Map(
            (lcbBldg.data?.months ?? []).map((b) => [b.month, parseFloat(b.per_room_kwh) || 0]),
          )
          const rows = bills.map((b) => ({
            month: b.month,
            kwh: parseFloat(b.usage_kwh) || 0,
            dorm: bldgByMonth.get(b.month) ?? 0,
            has: true as boolean,
          }))
          const hasCur = rows.some((r) => r.month === thisMonth)
          // 窗口不含当月时不补本月柱。
          if (!hasCur && pIncludesCur) {
            const kwh = seriesSumKwh(lsCur.data) ?? 0
            const meters = seriesMeters(lsCurBldg.data)
            const tot = seriesSumKwh(lsCurBldg.data)
            const dorm = meters > 0 && tot != null ? tot / meters : 0
            rows.push({ month: thisMonth, kwh, dorm, has: seriesSumKwh(lsCur.data) != null })
          }
          const n = Math.max(rows.length, 1)
          const vals = rows.length ? rows.map((r) => r.kwh) : [0]
          const dorm = rows.length ? rows.map((r) => r.dorm) : [0]
          const has = rows.length ? rows.map((r) => r.has) : [false]
          const label = (i: number) => {
            const m = rows[i]?.month
            if (!m) return '—'
            const [yy, mm] = m.split('-')
            return yy.slice(2) + '/' + mm
          }
          return {
            n,
            unit: 'kWh',
            vals,
            has,
            dorm,
            label,
            ticks: [label(0), label(Math.floor((n - 1) / 2)), label(n - 1)] as [string, string, string],
            note: bills.length
              ? '官方月账单' + (hasCur ? '' : ' · 本月日累计')
              : availNote(lsCur.data?.availability, lsCur.data?.quality, lsCur.error || lBills.error),
          }
        })()
      : null
  /* 学期档：日序列折成校历周。本表与楼栋序列共用周边界。 */
  const liveTermBase =
    IS_LIVE && termSem && termWeeks.length
      ? (() => {
          /* 楼栋序列先摊成户均再折周。先加总再除会抹平表数逐日变化。 */
          const toPoints = (sv: ReturnType<typeof seriesVals>, perRoom: number[] | null) =>
            sv.vals.map((v, i) => ({
              at: new Date(sv.at[i]),
              value: v == null ? null : perRoom ? (perRoom[i] ?? 0) : v,
              meters: sv.meters[i],
            }))
          const mine = foldWeeks(termWeeks, toPoints(lsv, null))
          const bldg = foldWeeks(termWeeks, toPoints(lsvBldg, perRoomFromSeries(lsvBldg)))
          const label = (i: number) => termWeeks[i]?.label ?? '—'
          const n = termWeeks.length
          return {
            n,
            unit: 'kWh',
            vals: mine.vals,
            has: mine.has,
            dorm: bldg.vals,
            label,
            ticks: [label(0), label(Math.floor((n - 1) / 2)), label(n - 1)] as [string, string, string],
            note: availNote(ls.data?.availability, ls.data?.quality, ls.error),
          }
        })()
      : null
  const liveSeriesBase =
    IS_LIVE && !useBillYear && !liveTermBase
      ? (() => {
          /* 月视图：按自然日铺满整月。稀疏 API 点直接 flex 会变成巨型柱。 */
          if (monthView) {
            const y = Number(s.pMonth.slice(0, 4))
            const m0 = Number(s.pMonth.slice(5, 7)) - 1
            const mine = densifyMonthDays(lsv, y, m0)
            const bldg = densifyMonthDays(lsvBldg, y, m0)
            const dorm = bldg.vals.map((v, i) => {
              if (!bldg.has[i]) return 0
              const meters = bldg.meters[i] || 0
              return meters > 0 ? v / meters : 0
            })
            const label = (i: number) => mine.labels[i] ?? '—'
            const n = mine.n
            return {
              n,
              unit: 'kWh',
              vals: mine.vals,
              has: mine.has,
              dorm,
              label,
              ticks: [label(0), label(Math.floor((n - 1) / 2)), label(n - 1)] as [string, string, string],
              note: availNote(ls.data?.availability, ls.data?.quality, ls.error),
            }
          }
          const n = Math.max(lsv.vals.length, 1)
          const vals = lsv.vals.length ? lsv.vals.map((v) => v ?? 0) : [0]
          const has = lsv.vals.length ? lsv.vals.map((v) => v != null) : [false]
          const label = (i: number) => lsv.labels[i] ?? '—'
          // 同窗口、同粒度的楼栋序列按标签对齐户均。
          const dormByLabel = new Map<string, number>()
          const bldgPer = perRoomFromSeries(lsvBldg)
          lsvBldg.labels.forEach((lb, i) => dormByLabel.set(lb, bldgPer[i] || 0))
          const dorm = vals.map((_, i) => dormByLabel.get(lsv.labels[i] ?? '') ?? 0)
          return {
            n,
            unit: 'kWh',
            vals,
            has,
            dorm,
            label,
            ticks: [label(0), label(Math.floor((n - 1) / 2)), label(n - 1)] as [string, string, string],
            note: availNote(ls.data?.availability, ls.data?.quality, ls.error),
          }
        })()
      : null
  const liveBase = liveBillYearBase ?? liveTermBase ?? liveSeriesBase
  const emptyBase = {
    n: 30,
    unit: 'kWh',
    vals: new Array(30).fill(0) as number[],
    has: new Array(30).fill(false) as boolean[],
    dorm: new Array(30).fill(0) as number[],
    label: () => '—',
    ticks: ['—', '—', '—'] as [string, string, string],
    note: '',
  }
  const mockBase = { ...personalSeries(pRange), has: null as boolean[] | null, note: '' }
  const ps = unbound ? emptyBase : (liveBase ?? mockBase)
  /* 柱高按当前单位换算。切换 kWh/元 时图表与读数一起变。 */
  const displayVals = ps.vals.map((v) => cv(v))
  const displayDorm = ps.dorm.map((v) => cv(v))
  const showDorm = !unbound && displayDorm.some((v) => v > 0)
  /* 顶部预留数值标签空间。实测宽度不足时按间隔收敛，但当前点始终显示。 */
  const kmax = Math.max(...displayVals, ...(showDorm ? displayDorm : [0]), 0.0001) * 1.28
  /* 默认高亮最近有数日。铺满整月后禁止落到月末空槽。 */
  const lastPresent = (() => {
    if (!ps.has) return Math.max(ps.n - 1, 0)
    for (let i = ps.n - 1; i >= 0; i--) if (ps.has[i]) return i
    return Math.max(ps.n - 1, 0)
  })()
  const rawHi = Math.min(Math.max(0, s.hoverDay), Math.max(ps.n - 1, 0))
  const hi = s.hoverDay >= 99 ? lastPresent : rawHi
  const hasHi = !ps.has || ps.has[hi] !== false
  const valueLabelStep = usagePlotW > 0 ? Math.max(1, Math.ceil((ps.n * 31) / usagePlotW)) : 1
  const hasPickedBar = s.hoverDay >= 0 && s.hoverDay < 99
  const showValueLabel = (i: number) =>
    mobileChart ? hasPickedBar && i === hi : i === hi || i % valueLabelStep === 0
  const bars30 = displayVals.map((v, i) => {
    const present = !ps.has || ps.has[i] !== false
    return {
      // 无数日高度 0 留空。禁止把缺数画成满高矩形。
      h: present ? ((v / kmax) * 100).toFixed(1) + '%' : '0%',
      bg: !present
        ? 'transparent'
        : i === hi
          ? RED
          : `color-mix(in srgb, var(--fg) ${dark ? 22 : 18}%, transparent)`,
      label: nf(v),
      showLabel: present && showValueLabel(i),
      active: i === hi,
      onEnter: () => {
        if (!mobileChart) set({ hoverDay: i })
      },
      onPick: () => set({ hoverDay: i }),
    }
  })
  /* 同楼线只连有数点。缺数不落 0，避免整段被拉到底。 */
  const dormPoly = displayDorm
    .map((v, i) => {
      if (!(v > 0)) return null
      return (((i + 0.5) / ps.n) * 100).toFixed(2) + ',' + (100 - (v / kmax) * 100).toFixed(2)
    })
    .filter((p): p is string => p != null)
    .join(' ')
  const chartHeight = 224
  const dormPoints = displayDorm.map((v, i) => {
    const y = 100 - (v / kmax) * 100
    const lineY = (y / 100) * chartHeight
    const barY = chartHeight - (displayVals[i] / kmax) * chartHeight
    /* 柱值固定在柱顶。两类标签进入同一垂直空间时，把折线值翻到点下方。 */
    const barLabelCenter = barY - 11.5
    const aboveLabelCenter = lineY - 11.5
    const belowLabelCenter = lineY + 11.5
    const aboveClearance = Math.abs(aboveLabelCenter - barLabelCenter)
    const belowClearance = Math.abs(belowLabelCenter - barLabelCenter)
    const labelBelow =
      lineY < 22 ||
      (lineY < chartHeight - 22 && bars30[i].showLabel && aboveClearance < 18 && belowClearance > aboveClearance)

    return {
      value: v,
      present: v > 0,
      x: ((i + 0.5) / ps.n) * 100,
      y,
      showLabel: v > 0 && showValueLabel(i),
      labelBelow,
      active: i === hi,
    }
  })
  const hv = ps.vals[hi]
  const ha = ps.dorm[hi]
  const hd = ha > 0 ? ((hv - ha) / ha) * 100 : 0
  const pRangeDefs: [PRange, string][] = [
    ['month', '月'],
    ['term', '学期'],
    ['year', '年'],
  ]
  /* 账期窗口：年=区间，月=单月，学期=校历学期。
     mock 柱为固定序列。禁止挂无效选择器。 */
  const termView = IS_LIVE && pRange === 'term'
  const showWinPicker = useBillYear || monthView
  const pMonthOpts = monthOptions((lBills.data ?? []).map((b) => b.month), 12)
  const pRangeEn = useBillYear
    ? monthLabel(pFrom) + '–' + monthLabel(pTo)
    : monthView
      ? monthLabel(s.pMonth)
      : termSem
        ? semesterLabel(termSem.key) + ' · ' + termWeeks.length + ' WEEKS'
        : { month: '30 DAYS', term: '18 WEEKS', year: '12 MONTHS' }[pRange]
  const periodAltLabel =
    pRange === 'year'
      ? RMB
        ? '当月用电'
        : '当月电费'
      : pRange === 'term'
        ? RMB
          ? '当周用电'
          : '当周电费'
        : RMB
          ? '当日用电'
          : '当日电费'
  const chartNote = IS_LIVE && !unbound ? liveBase?.note || '' : ''
  /* 首屏 = 尚无可显示的数。刷新已有数据时禁止走骨架。 */
  const balLoading = IS_LIVE && !unbound && lm.loading && liveTotal == null
  const chartLoading =
    IS_LIVE && !unbound && (ls.loading || lBills.loading) && !(ps.has ?? []).some(Boolean)

  // ---- 最近推送
  const on = unbound
    ? []
    : CH_DEFS.filter((d) => channelPresentation(d, s.features.channels, s.features.channelComingSoon).ready && s.ch[d.id] && s.ch[d.id].on)
  const emptyPushNotice = (): PushHistoryNotice =>
    on.length === 0
      ? {
          id: 'empty-channel',
          channel: unbound ? '未绑定电表' : '暂无渠道',
          summary: unbound
            ? '绑定宿舍电表后可开通推送渠道，然后接收余额预警与定时摘要'
            : '尚未启用推送渠道，请前往推送设置开启',
          state: unbound ? '待绑定' : '未开启',
          tone: 'muted',
        }
      : {
          id: 'empty-history',
          channel: '暂无记录',
          summary: '渠道已启用，首次投递成功后将在此显示',
          state: '等待推送',
          tone: 'muted',
        }
  /* mock 也生成真正的 PushLog。列表、详情、复制 JSON 与 live 同路径。 */
  const mockPushLogs = (): PushLog[] => {
    if (on.length === 0) return []
    const week = k.slice(23).reduce((a, b) => a + b, 0)
    const bodies: [PushLog['kind'], string][] = [
      ['low_balance', '低额度预警 · 余额 43.87 元已低于 ' + s.threshold + ' 元阈值'],
      ['digest', '定时摘要 · 余额 43.87 元，昨日用电 ' + k[29].toFixed(2) + ' kWh'],
      ['digest', '定时摘要 · 余额 ' + (43.87 + k[29] * elecRate).toFixed(2) + ' 元，日均 ' + (week / 7).toFixed(2) + ' kWh'],
      ['system', '数据同步完成 · 总用电量 3356.48 kWh'],
      ['digest', '定时摘要 · 余额 ' + (43.87 + (k[29] + k[28]) * elecRate).toFixed(2) + ' 元，近 7 日 ' + week.toFixed(1) + ' kWh'],
      ['test', '渠道测试 · 这是一条来自推送设置页的测试消息'],
      ['low_balance', '低额度预警 · 余额 12.40 元已低于 ' + s.threshold + ' 元阈值'],
      ['digest', '定时摘要 · 余额 55.20 元，昨日用电 ' + k[24].toFixed(2) + ' kWh'],
    ]
    // 从最近一次推送往回，每条间隔 12 小时。演示数据可翻更早记录。
    const base = Date.parse('2026-07-25T17:42:00+08:00')
    return bodies.map(([kind, summary], i) => {
      const failed = i === 2 && on.length > 1
      return {
        id: `mock-push-${i}`,
        sent_at: new Date(base - i * 12 * 3600 * 1000).toISOString(),
        channel: on[i % on.length].id,
        kind,
        status: failed ? 'failed' : 'delivered',
        summary,
        error: failed ? 'dial tcp 203.0.113.10:443: i/o timeout（连接上游超时，已重试 2 次）' : null,
      }
    })
  }
  const livePushLogs = [...(lp.data?.items || []), ...morePushLogs]
  const pushLogs: PushLog[] = !IS_LIVE ? mockPushLogs() : unbound ? [] : livePushLogs
  const pushNotice: PushHistoryNotice | null =
    IS_LIVE && !unbound && lp.loading && !lp.data
      ? { id: 'loading', channel: '正在加载', summary: '正在读取真实推送记录', state: '加载中', tone: 'muted' }
      : IS_LIVE && !unbound && lp.error
        ? { id: 'error', channel: '读取失败', summary: '无法读取推送记录，请稍后刷新重试', state: '加载失败', tone: 'error' }
        : pushLogs.length === 0
          ? emptyPushNotice()
          : null
  /* 首屏游标来自 hook。之后来自上一次加载更多。
     追加期间新记录会重出。按 id 去重后再拼。 */
  const nextPushCursor = pushCursor ?? lp.data?.next_cursor ?? null
  const loadMorePushLogs = async () => {
    if (!nextPushCursor || pushLoadingMore) return
    setPushLoadingMore(true)
    try {
      const page = await api.pushLogs({ limit: PUSH_PAGE_SIZE, cursor: nextPushCursor })
      setMorePushLogs((current) => {
        const seen = new Set([...(lp.data?.items || []), ...current].map((item) => item.id))
        return [...current, ...page.items.filter((item) => !seen.has(item.id))]
      })
      setPushCursor(page.next_cursor ?? null)
    } catch {
      /* 失败不弹 Toast。概览只读。游标不动，再点即重试。 */
    } finally {
      setPushLoadingMore(false)
    }
  }

  return (
    <div data-screen-label="概览" style={{ animation: 'rise .28s ease both' }}>
      <section
        data-r="hero"
        style={{
          display: 'grid',
          gridTemplateColumns: '1.5fr 1fr',
          gap: 0,
          padding: '52px 0 46px',
          borderBottom: '1px solid var(--line)',
        }}
      >
        <div style={{ display: 'flex', flexDirection: 'column', paddingRight: '40px' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '12px', flexWrap: 'wrap', rowGap: '10px' }}>
            <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
              TOTAL BALANCE
            </div>
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '6px',
                padding: '4px 10px',
                borderRadius: '999px',
                background: statusBg,
              }}
            >
              <span style={{ width: '5px', height: '5px', borderRadius: '99px', background: statusFg }} />
              <span style={{ fontSize: '11px', fontWeight: 500, color: statusFg }}>{statusText}</span>
            </div>
          </div>
          {s.bindOpen ? (
            <InlineBindMeter />
          ) : (
            <>
              {/* 刷新贴着余额。RefreshButton 用 min-content，避免窄屏整块掉行。 */}
              <div
                style={{
                  display: 'flex',
                  alignItems: 'baseline',
                  gap: '12px',
                  rowGap: '14px',
                  flexWrap: 'wrap',
                  marginTop: '26px',
                }}
              >
              {balLoading ? (
                /* 占位块按大字号尺寸。落数时本行不跳。 */
                <Skeleton w="clamp(200px,34vw,420px)" h="clamp(48px,8.6vw,107px)" style={{ alignSelf: 'center' }} />
              ) : (
                <div
                  style={{
                    fontSize: 'clamp(56px,10vw,124px)',
                    fontWeight: 600,
                    letterSpacing: '-.055em',
                    lineHeight: 0.86,
                    fontVariantNumeric: 'tabular-nums',
                  }}
                >
                  {hasBal ? total.toFixed(2) : '—'}
                </div>
              )}
              <div style={{ fontSize: '22px', fontWeight: 400, color: 'var(--fg2)' }}>元</div>
              {!unbound && (
                <RefreshButton
                  meter={meterNo}
                  loading={lm.loading || ls.loading}
                  style={{ marginLeft: 'auto', alignSelf: 'center' }}
                />
              )}
              {unbound && (
                <button
                  className="hv-op82"
                  onClick={startBind}
                  style={{
                    alignSelf: 'center',
                    margin: '0 0 0 6px',
                    padding: '13px 26px',
                    border: '1px solid var(--fg)',
                    borderRadius: '999px',
                    font: 'inherit',
                    fontSize: '13px',
                    fontWeight: 600,
                    cursor: 'pointer',
                    background: 'var(--fg)',
                    color: 'var(--bg)',
                    whiteSpace: 'nowrap',
                  }}
                >
                  绑定电表
                </button>
              )}
            </div>
            <div
              style={{ fontSize: '13px', color: 'var(--fg2)', marginTop: '26px', maxWidth: '420px', textWrap: 'pretty' }}
            >
              {unbound ? (
                <>
                  绑定宿舍电表后，将显示余额、
                  <span style={{ color: 'var(--fg)', fontWeight: 500 }}>可支撑天数</span>
                  与每日用电。电表号可在学校官方平台查询，一块表最多绑定 4 个账号。
                </>
              ) : canEstimate ? (
                <>
                  按近 7 天日均 <span style={{ color: 'var(--fg)', fontWeight: 500 }}>{avgCost.toFixed(2)} 元</span>{' '}
                  估算，余额约可支撑 <span style={{ color: 'var(--fg)', fontWeight: 500 }}>{daysLeft} 天</span>，到{' '}
                  {runout.getMonth() + 1} 月 {runout.getDate()} 日 用尽。
                </>
              ) : (
                <>
                  历史数据不足，暂无法估算可支撑天数。
                  {lm.error ? ' （' + lm.error + '）' : ''}
                </>
              )}
            </div>
            </>
          )}
        </div>
        <div
          data-r="heroside"
          style={{
            borderLeft: '1px solid var(--line)',
            paddingLeft: '40px',
            display: 'flex',
            flexDirection: 'column',
            justifyContent: 'center',
            gap: 0,
          }}
        >
          {heroStats.map((st) => (
            <div
              key={st.en}
              style={{
                display: 'flex',
                alignItems: 'baseline',
                gap: '14px',
                padding: '14px 0',
                borderBottom: '1px solid var(--line2)',
              }}
            >
              <div style={{ display: 'flex', flexDirection: 'column', gap: '4px' }}>
                <div
                  style={{ font: "500 9px/1 'JetBrains Mono',monospace", letterSpacing: '.18em', color: 'var(--fg3)' }}
                >
                  {st.en}
                </div>
                <div style={{ fontSize: '12.5px', color: 'var(--fg2)' }}>{st.label}</div>
              </div>
              <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'baseline', gap: '5px' }}>
                <span
                  style={{
                    fontSize: '24px',
                    fontWeight: 600,
                    letterSpacing: '-.035em',
                    fontVariantNumeric: 'tabular-nums',
                    color: st.color,
                  }}
                >
                  {st.v}
                </span>
                <span style={{ fontSize: '12px', color: 'var(--fg3)' }}>{st.u}</span>
              </div>
            </div>
          ))}
        </div>
      </section>

      <section ref={usagePlotRef} style={{ padding: '44px 0 40px', borderBottom: '1px solid var(--line)' }}>
        <div data-r="hdr" style={{ display: 'flex', alignItems: 'flex-end', gap: '38px', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
            <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
              USAGE · {pRangeEn}
            </div>
            <div style={{ fontSize: '26px', fontWeight: 600, letterSpacing: '-.035em' }}>用电量</div>
          </div>
          <div
            data-r="hdrside"
            style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: '16px', flexWrap: 'wrap' }}
          >
            <div style={{ display: 'flex', alignItems: 'center', gap: '7px' }}>
              <span style={{ width: '10px', height: '10px', background: 'var(--fg)', opacity: 0.18 }} />
              <span style={{ fontSize: '11.5px', color: 'var(--fg2)' }}>本表</span>
            </div>
            {showDorm && (
              <div style={{ display: 'flex', alignItems: 'center', gap: '7px' }}>
                <span style={{ width: '15px', height: 0, borderTop: '1.5px dashed var(--red)' }} />
                <span style={{ fontSize: '11.5px', color: 'var(--fg2)' }}>同楼平均</span>
              </div>
            )}
            <span className="chart-tap-hint">轻触柱形查看数值</span>
            <SegGroup>
              {pRangeDefs.map((r) => (
                <SegBtn
                  key={r[0]}
                  seg={seg(pRange === r[0], r[1], () => set({ pRange: r[0], hoverDay: 999, pZoom: null }))}
                  pad="6px 14px"
                  fs="12.5px"
                />
              ))}
            </SegGroup>
          </div>
        </div>
        {/* 账期窗口独立一行。与数据看板一致。窄屏避免控件被甩走。 */}
        {termView && (
          <SemesterPicker
            style={{ marginTop: '18px' }}
            value={termSem ? termSem.key : termKey}
            options={semesters}
            onChange={(key) => set({ pTerm: key, hoverDay: 999, pZoom: null })}
          />
        )}
        {showWinPicker && (
          <MonthWindowPicker
            style={{ marginTop: '18px' }}
            label={useBillYear ? 'RANGE' : 'MONTH'}
            from={useBillYear ? pFrom : s.pMonth}
            to={useBillYear ? pTo : null}
            options={pMonthOpts}
            onChange={(a, b) =>
              set(useBillYear ? { pFrom: a, pTo: b, hoverDay: 999, pZoom: null } : { pMonth: a, hoverDay: 999, pZoom: null })
            }
          />
        )}

        <div style={{ display: 'flex', alignItems: 'flex-end', gap: '38px', marginTop: '30px', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '5px' }}>
            <div style={{ font: "400 11px/1 'JetBrains Mono',monospace", color: 'var(--fg3)' }}>{ps.label(hi)}</div>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: '6px' }}>
              <span
                style={{
                  fontSize: '38px',
                  fontWeight: 600,
                  letterSpacing: '-.04em',
                  lineHeight: 1,
                  fontVariantNumeric: 'tabular-nums',
                }}
              >
                {hasHi ? f2(hv) : '—'}
              </span>
              <span style={{ fontSize: '13px', color: 'var(--fg2)' }}>{U}</span>
            </div>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '5px' }}>
            <div style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>{periodAltLabel}</div>
            <div style={{ fontSize: '17px', fontWeight: 500, fontVariantNumeric: 'tabular-nums' }}>
              {hasHi ? alt(hv) : '—'}
            </div>
          </div>
          {showDorm && (
            <>
              <div style={{ display: 'flex', flexDirection: 'column', gap: '5px' }}>
                <div style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>同楼平均</div>
                <div
                  style={{ fontSize: '17px', fontWeight: 500, color: 'var(--fg2)', fontVariantNumeric: 'tabular-nums' }}
                >
                  {ha > 0 ? f2(ha) + ' ' + U : '—'}
                </div>
              </div>
              <div style={{ display: 'flex', flexDirection: 'column', gap: '5px' }}>
                <div style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>对比同楼</div>
                <div style={{ fontSize: '17px', fontWeight: 500, color: ha > 0 ? (hd > 0 ? RED : OK) : FG3 }}>
                  {ha > 0 ? (hd > 0 ? '+' : '') + hd.toFixed(1) + '%' : '—'}
                </div>
              </div>
            </>
          )}
        </div>

        {chartLoading ? (
          <div style={{ marginTop: '30px' }}>
            <ChartSkeleton height={224} bars={Math.min(Math.max(ps.n, 8), 31)} />
          </div>
        ) : (
        <div
          key={pRange + '-' + s.unit + '-' + rf.epoch}
          className="chart-anim"
          onMouseLeave={() => {
            if (!mobileChart) set({ hoverDay: 99 })
          }}
          style={{
            position: 'relative',
            height: '224px',
            marginTop: '30px',
            borderBottom: '1px solid var(--line)',
          }}
        >
          <div style={{ position: 'absolute', left: 0, right: 0, top: 0, borderTop: '1px dashed var(--line2)' }} />
          <div style={{ position: 'absolute', left: 0, right: 0, top: '50%', borderTop: '1px dashed var(--line2)' }} />
          <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'flex-end', gap: '3px' }}>
            {bars30.map((b, i) => (
              <div
                key={i}
                onMouseEnter={b.onEnter}
                onClick={b.onPick}
                style={{
                  position: 'relative',
                  flex: 1,
                  height: '100%',
                  display: 'flex',
                  alignItems: 'flex-end',
                  cursor: 'crosshair',
                  minWidth: 0,
                }}
              >
                {b.showLabel && (
                  <span
                    aria-hidden="true"
                    style={{
                      position: 'absolute',
                      left: '50%',
                      bottom: `calc(${b.h} + 6px)`,
                      transform: 'translateX(-50%)',
                      zIndex: 3,
                      padding: '1px 2px',
                      background: 'color-mix(in srgb, var(--bg) 88%, transparent)',
                      color: b.active ? 'var(--red)' : 'var(--fg3)',
                      font: "500 9px/1 'JetBrains Mono',monospace",
                      fontVariantNumeric: 'tabular-nums',
                      whiteSpace: 'nowrap',
                      pointerEvents: 'none',
                    }}
                  >
                    {b.label}
                  </span>
                )}
                <div
                  style={{
                    width: '100%',
                    transition: 'height .35s cubic-bezier(.22,.61,.36,1), background .1s',
                    height: b.h,
                    background: b.bg,
                  }}
                />
              </div>
            ))}
          </div>
          {showDorm && (
            <svg
              viewBox="0 0 100 100"
              preserveAspectRatio="none"
              style={{ position: 'absolute', inset: 0, width: '100%', height: '100%', pointerEvents: 'none', overflow: 'visible' }}
            >
              <polyline
                points={dormPoly}
                fill="none"
                stroke="var(--red)"
                strokeWidth="1.5"
                strokeDasharray="5 4"
                vectorEffect="non-scaling-stroke"
                strokeLinejoin="round"
              />
            </svg>
          )}
          {showDorm && (
            <div aria-hidden="true" style={{ position: 'absolute', inset: 0, pointerEvents: 'none', zIndex: 4 }}>
              {dormPoints.map((point, i) =>
                point.present ? (
                  <span
                    key={i}
                    style={{
                      position: 'absolute',
                      left: point.x + '%',
                      top: point.y + '%',
                      width: '6px',
                      height: '6px',
                      transform: 'translate(-50%,-50%)',
                      border: '1.5px solid var(--red)',
                      borderRadius: '50%',
                      background: point.active ? 'var(--red)' : 'var(--bg)',
                      boxSizing: 'border-box',
                    }}
                  >
                    {point.showLabel && (
                      <span
                        style={{
                          position: 'absolute',
                          left: '50%',
                          ...(point.labelBelow ? { top: '8px' } : { bottom: '8px' }),
                          transform: 'translateX(-50%)',
                          padding: '2px 3px',
                          background: 'color-mix(in srgb, var(--bg) 94%, transparent)',
                          color: 'var(--red)',
                          font: "500 9px/1 'JetBrains Mono',monospace",
                          fontVariantNumeric: 'tabular-nums',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        {nf(point.value)}
                      </span>
                    )}
                  </span>
                ) : null,
              )}
            </div>
          )}
        </div>
        )}
        <div
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            marginTop: '11px',
            font: "400 10.5px/1 'JetBrains Mono',monospace",
            color: 'var(--fg3)',
          }}
        >
          <span>{ps.ticks[0]}</span>
          <span>{ps.ticks[1]}</span>
          <span>{ps.ticks[2]}</span>
        </div>
        {IS_LIVE && chartNote ? <LiveNote text={chartNote} /> : null}
      </section>

      <section style={{ padding: '44px 0 0' }}>
        <div style={{ display: 'flex', alignItems: 'flex-end', gap: '16px' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
            <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
              RECENT PUSHES
            </div>
            <div style={{ fontSize: '26px', fontWeight: 600, letterSpacing: '-.035em' }}>最近推送</div>
          </div>
          <button
            className="hv-fg"
            onClick={() => set({ view: 'config' })}
            style={{
              background: 'none',
              border: 0,
              margin: '0 0 4px auto',
              padding: 0,
              font: 'inherit',
              cursor: 'pointer',
              fontSize: '13px',
              color: 'var(--fg2)',
              display: 'flex',
              alignItems: 'center',
              gap: '6px',
            }}
          >
            推送设置 <span>→</span>
          </button>
        </div>
        <PushHistory
          logs={pushLogs}
          notice={pushNotice}
          colors={{ ok: OK, error: RED, muted: FG3 }}
          hasMore={IS_LIVE && !!nextPushCursor}
          loadingMore={pushLoadingMore}
          onLoadMore={loadMorePushLogs}
        />
      </section>
    </div>
  )
}
