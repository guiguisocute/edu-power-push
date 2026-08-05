/* 数据看板与全校用电视图。对齐原型 isCampus 区块。
   负荷柱状图、四列统计、用电构成、工作日与周末曲线。
   内联样式对应原型。数据算式来自 lib/mock.ts。 */

import { useEffect, useRef } from 'react'
import { useStore } from '../lib/store'
import { useElementWidth, useIsCoarsePointer, useIsMobileViewport } from '../lib/useSize'
import { formatKwhInUnit, makeFmt, themeColors, seg, type ConsumptionDisplayUnit } from '../lib/format'
import { campusSeries, buildings, daily, dorm, rnd, T_LABEL, type CRange, type Building } from '../lib/mock'
import { SegGroup, SegBtn } from '../components/ui'
import { indexFromEvent, useBrush } from '../lib/useBrush'
import { buildingParam, floorParam, useScopeOptions } from '../lib/scopes'
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
  useLiveBillBreakdown,
  useLiveBreakdown,
  useLiveCampusBills,
  useLiveCampusSeries,
  useLiveCampusSummary,
  useLiveBills,
  useLiveMeterSeries,
} from '../api/live'
import type { CampusBreakdownRow } from '../api/types'
import { buildCampusYearBars, hasBreakdownValues } from '../lib/campusYear'
import LiveNote from '../components/LiveNote'
import MonthWindowPicker from '../components/MonthWindowPicker'
import { ChartSkeleton, InlineNote } from '../components/Loading'
import SemesterPicker from '../components/SemesterPicker'
import {
  currentSemesterKey,
  findSemester,
  foldWeeks,
  normalizeSemesters,
  semesterLabel,
  semesterWeeks,
  semesterWindow,
} from '../lib/semesters'
import BasisSwitch from '../components/BasisSwitch'
import {
  addMonths,
  monthIndex,
  monthLabel,
  monthOptions,
  monthShort,
  monthsBetween,
} from '../lib/months'

/** 月账单 YYYY-MM 转轴刻度。本月加 ·本月。 */
function billAxisLabel(ym: string, provisional = false): string {
  const base = monthShort(ym)
  return provisional ? base + '·本月' : base
}

/** 月账单 YYYY-MM 转悬停大字。 */
function billFullLabel(ym: string, provisional = false): string {
  const [y, m] = ym.split('-')
  const base = y + ' 年 ' + Number(m) + ' 月'
  return provisional ? base + '（本月 · 日累计，账单未出）' : base
}

/** 从 bucket ISO/YYYY-MM 抽出年月。避免 UTC 午夜被拨到上个月。 */
function bucketYearMonth(raw: string): { y: string; m: string } | null {
  const hit = raw.match(/(\d{4})-(\d{2})/)
  if (!hit) return null
  return { y: hit[1], m: hit[2] }
}

/* live 下无数据来源的展示块。禁止渲染假数。直接说明原因。 */
function Unavailable({ title, desc }: { title: string; desc: string }) {
  return (
    <div style={{ marginTop: '28px', paddingTop: '22px', borderTop: '1px solid var(--line)' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
        <span style={{ width: '5px', height: '5px', background: 'var(--fg3)', flex: 'none' }} />
        <span style={{ fontSize: '13px', color: 'var(--fg2)' }}>{title}</span>
      </div>
      <div style={{ fontSize: '12.5px', color: 'var(--fg3)', lineHeight: 1.7, marginTop: '12px', textWrap: 'pretty' }}>
        {desc}
      </div>
    </div>
  )
}

/** 走势图画布高度。16 栋叠线时需加高才能分开。 */
const TREND_H = 360

/** 光标横坐标映射到最近格点。折线锚在格点。禁止与柱图 floor 换算共用。 */
function colFromEvent(e: { clientX: number; currentTarget: Element }, count: number): number {
  const r = e.currentTarget.getBoundingClientRect()
  if (count <= 1 || r.width <= 0) return 0
  const x = Math.min(Math.max(e.clientX - r.left, 0), r.width)
  return Math.min(count - 1, Math.max(0, Math.round((x / r.width) * (count - 1))))
}

/* 横向条形排名。楼栋对比与楼层对比共用。
   降序、条长即量、右侧标数。首位品牌红，其余按名次发灰。 */
function RankBars({
  rows,
  dark,
  empty,
}: {
  rows: { name: string; v: number; label: string; hint: string }[]
  dark: boolean
  empty: string
}) {
  if (rows.length === 0) return <Unavailable title="当前范围没有可对比的数据" desc={empty} />
  const max = Math.max(...rows.map((r) => r.v), 0.001)
  return (
    <div style={{ display: 'flex', flexDirection: 'column', marginTop: '20px' }}>
      {rows.map((r, i) => (
        <div
          key={r.name}
          title={r.hint}
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '12px',
            padding: '9px 0',
            borderBottom: '1px solid var(--line2)',
          }}
        >
          <span
            style={{
              width: '22px',
              flex: 'none',
              font: "500 10.5px/1 'JetBrains Mono',monospace",
              color: i === 0 ? 'var(--red)' : 'var(--fg3)',
            }}
          >
            {String(i + 1).padStart(2, '0')}
          </span>
          <span
            style={{
              width: '58px',
              flex: 'none',
              fontSize: '12.5px',
              color: 'var(--fg)',
              overflow: 'hidden',
              textOverflow: 'ellipsis',
              whiteSpace: 'nowrap',
            }}
          >
            {r.name}
          </span>
          <span style={{ flex: 1, minWidth: 0, height: '12px', background: 'var(--line2)' }}>
            <span
              style={{
                display: 'block',
                height: '100%',
                width: Math.max(2, (r.v / max) * 100) + '%',
                background:
                  i === 0 ? 'var(--red)' : `color-mix(in srgb, var(--fg) ${dark ? 38 : 32}%, transparent)`,
                transition: 'width .35s cubic-bezier(.22,.61,.36,1)',
              }}
            />
          </span>
          <span
            style={{
              width: '96px',
              textAlign: 'right',
              font: "500 12px/1 'JetBrains Mono',monospace",
              fontVariantNumeric: 'tabular-nums',
              color: i === 0 ? 'var(--red)' : 'var(--fg2)',
            }}
          >
            {r.label}
          </span>
        </div>
      ))}
    </div>
  )
}

/* 色阶条。本站 token 手写。等价于 visualMap 的映射区间。
   拖手柄改映射区间，不筛数据。区间外格子压成平色。 */
function HeatScale({
  max,
  lo,
  hi,
  onChange,
  onReset,
  fmt,
  hoverV,
  outTone,
}: {
  max: number
  lo: number
  hi: number
  onChange: (lo: number, hi: number) => void
  onReset: () => void
  fmt: (v: number) => string
  hoverV: number | null
  outTone: string
}) {
  const drag = useRef<'lo' | 'hi' | null>(null)
  const MIN_GAP = 0.04
  const fracAt = (e: { clientX: number; currentTarget: Element }) => {
    const r = e.currentTarget.getBoundingClientRect()
    if (r.width <= 0) return 0
    return Math.min(1, Math.max(0, (e.clientX - r.left) / r.width))
  }
  const apply = (f: number) => {
    if (drag.current === 'lo') onChange(Math.min(f, hi - MIN_GAP), hi)
    else if (drag.current === 'hi') onChange(lo, Math.max(f, lo + MIN_GAP))
  }
  const dirty = lo > 0.001 || hi < 0.999
  const hoverF = hoverV == null || max <= 0 ? null : Math.min(1, Math.max(0, hoverV / max))
  return (
    <div
      style={{ display: 'flex', alignItems: 'flex-start', gap: '12px', marginTop: '18px', flexWrap: 'wrap' }}
    >
      <span
        style={{
          width: 'var(--rowlabel)',
          flex: 'none',
          font: "500 9px/1.6 'JetBrains Mono',monospace",
          letterSpacing: '.18em',
          color: 'var(--fg3)',
        }}
      >
        SCALE
      </span>
      {/* 条设下限宽度。窄屏时两个手柄才能拖开。 */}
      <div style={{ flex: '1 1 200px', minWidth: 0 }}>
        <div
          onPointerDown={(e) => {
            const f = fracAt(e)
            drag.current = Math.abs(f - lo) <= Math.abs(f - hi) ? 'lo' : 'hi'
            try {
              e.currentTarget.setPointerCapture(e.pointerId)
            } catch {}
            apply(f)
          }}
          onPointerMove={(e) => drag.current && apply(fracAt(e))}
          onPointerUp={() => (drag.current = null)}
          onPointerCancel={() => (drag.current = null)}
          style={{
            position: 'relative',
            height: '12px',
            cursor: 'ew-resize',
            touchAction: 'none',
            marginTop: '4px',
          }}
        >
          {/* 三段：下限平色、中间渐变、上限实色。横轴即数值轴。 */}
          <div style={{ position: 'absolute', inset: 0, display: 'flex' }}>
            <div style={{ width: lo * 100 + '%', background: outTone }} />
            <div
              style={{
                width: (hi - lo) * 100 + '%',
                background:
                  'linear-gradient(90deg, color-mix(in srgb, var(--red) 4%, transparent), color-mix(in srgb, var(--red) 92%, transparent))',
              }}
            />
            <div style={{ flex: 1, background: 'color-mix(in srgb, var(--red) 92%, transparent)' }} />
          </div>
          {([['lo', lo] as const, ['hi', hi] as const]).map(([k, f]) => (
            <div
              key={k}
              style={{
                position: 'absolute',
                left: f * 100 + '%',
                top: '-4px',
                bottom: '-4px',
                width: '2px',
                background: 'var(--fg)',
                transform: 'translateX(-1px)',
                pointerEvents: 'none',
              }}
            />
          ))}
          {/* 悬浮格子在色阶上的位置。 */}
          {hoverF != null && (
            <div
              style={{
                position: 'absolute',
                left: hoverF * 100 + '%',
                top: '-11px',
                transform: 'translateX(-50%)',
                width: '9px',
                height: '9px',
                borderRadius: '99px',
                background: 'var(--red)',
                boxShadow: '0 0 0 2.5px var(--bg)',
                pointerEvents: 'none',
                transition: 'left .14s ease',
              }}
            />
          )}
        </div>
        <div
          style={{
            position: 'relative',
            height: '13px',
            marginTop: '7px',
            font: "400 10px/1 'JetBrains Mono',monospace",
            color: 'var(--fg3)',
          }}
        >
          <span style={{ position: 'absolute', left: 0 }}>0</span>
          <span style={{ position: 'absolute', right: 0 }}>{fmt(max)}</span>
          {lo > 0.08 && (
            <span style={{ position: 'absolute', left: lo * 100 + '%', transform: 'translateX(-50%)', color: 'var(--fg2)' }}>
              {fmt(max * lo)}
            </span>
          )}
          {hi < 0.92 && (
            <span style={{ position: 'absolute', left: hi * 100 + '%', transform: 'translateX(-50%)', color: 'var(--fg2)' }}>
              {fmt(max * hi)}
            </span>
          )}
        </div>
      </div>
      <button
        type="button"
        className="hv-line-fg"
        onClick={onReset}
        disabled={!dirty}
        style={{
          flex: 'none',
          background: 'none',
          border: '1px solid var(--line)',
          borderRadius: '999px',
          margin: 0,
          padding: '5px 12px',
          font: 'inherit',
          fontSize: '11.5px',
          color: 'var(--fg2)',
          cursor: dirty ? 'pointer' : 'default',
          opacity: dirty ? 1 : 0.35,
        }}
      >
        重置色阶
      </button>
    </div>
  )
}

/* 并排面板标题块。统一为小标签/标题/说明三段。
   说明行固定两行高，避免两块正文错位。 */
function PanelHead({ en, title, note }: { en: string; title: string; note: string }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
      <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
        {en}
      </div>
      <div style={{ fontSize: '26px', fontWeight: 600, letterSpacing: '-.035em' }}>{title}</div>
      <div
        style={{
          fontSize: '12px',
          lineHeight: 1.5,
          color: 'var(--fg3)',
          minHeight: '36px',
          textWrap: 'pretty',
        }}
      >
        {note}
      </div>
    </div>
  )
}

interface ScopeInfo {
  f: number
  unit: 'MWh' | 'kWh'
  name: string
  bldg: Building | null
  kwh?: number
  pop?: number
  rooms?: number
  floor?: number
}

export default function CampusView() {
  const { s, set, persist } = useStore()
  const dark = s.theme === 'dark'
  const { RED, OK } = themeColors(dark)
  const elecRate = parseFloat(s.features?.display?.electricityRate || '') || 0.62
  const { RMB, kpair, mpair, alt } = makeFmt(s.unit, elecRate)
  /* 折线区与热力图格子区实测宽度。两处 x 轴刻度疏密都按它算。 */
  const trendPlotRef = useRef<HTMLDivElement>(null)
  const trendW = useElementWidth(trendPlotRef)
  const hmPlotRef = useRef<HTMLDivElement>(null)
  const hmW = useElementWidth(hmPlotRef)
  const mainPlotRef = useRef<HTMLDivElement>(null)
  const mainPlotW = useElementWidth(mainPlotRef)
  const coarse = useIsCoarsePointer()
  const mobileChart = useIsMobileViewport()
  /* 触屏：单击临时查看。短时再点同一柱钉住/取消。桌面单击钉住。 */
  const barTapRef = useRef<{ i: number; t: number }>({ i: -1, t: 0 })
  /* 触屏走势：区分点开浮窗与横向扫时间。 */
  const trendTouchRef = useRef<{ startX: number; scrubbed: boolean } | null>(null)
  const k = daily()
  const dm = dorm()

  // ---- 范围：全部楼栋 / 选中楼栋 / 选中楼层
  /* live 楼栋名来自 campus/scopes，与 mock DORMS 不同。
     live 用电量一律由 campus/summary 与 campus/series 提供。 */
  const scopeInfo = (): ScopeInfo => {
    const all = buildings()
    const CAMPUS = 213800
    if (IS_LIVE) {
      if (s.bldgScope === 'all') return { f: 1, unit: 'MWh', name: '全部楼栋', bldg: null }
      const name = s.floorScope === 'all' ? s.bldgScope : s.bldgScope + ' ' + s.floorScope
      return { f: 1, unit: 'kWh', name, bldg: null }
    }
    if (s.bldgScope === 'all') return { f: 1, unit: 'MWh', name: '全部楼栋', bldg: null }
    const b = all.find((x) => x.n === s.bldgScope) || all[0]
    if (s.floorScope === 'all')
      return { f: b.kwh / CAMPUS, unit: 'kWh', name: b.n, bldg: b, kwh: b.kwh, pop: b.p, rooms: b.rooms }
    const fi = Number(s.floorScope)
    return {
      f: (b.floors[fi] || 0) / CAMPUS,
      unit: 'kWh',
      name: b.n + ' ' + (fi + 1) + 'F',
      bldg: b,
      floor: fi,
      kwh: b.floors[fi] || 0,
      pop: Math.round(b.p / b.f),
      rooms: Math.max(1, Math.round(b.rooms / b.f)),
    }
  }

  // ---- 全校序列与范围缩放
  const cs0 = campusSeries(s.range)
  const sci = scopeInfo()
  // ---- live 数据。mock 空转。上游无小时数据。live 下日周期不可用。
  const liveBuilding = buildingParam(s.bldgScope)
  const liveFloor = floorParam(s.floorScope)
  const scopeOpts = useScopeOptions(s.bldgScope, '全部楼栋')
  /* 业务范围变更后清理本地遗留选择。 */
  useEffect(() => {
    if (!IS_LIVE || scopeOpts.loading || s.bldgScope === 'all') return
    if (!scopeOpts.buildings.some((o) => o.v === s.bldgScope)) {
      set({ bldgScope: 'all', floorScope: 'all', cHover: null, cPin: null, cZoom: null })
    }
  }, [scopeOpts.loading, scopeOpts.buildings, s.bldgScope, set])
  /** live 是否已缩到某栋。mock 用 sci.bldg 判断。 */
  const scoped = IS_LIVE ? s.bldgScope !== 'all' : false
  const liveDayNA = IS_LIVE && s.range === 'day'
  const thisMonth = currentMonthKey()

  /* ---- 账期窗口。
     年 = [cFrom, cTo]。月 = 选定自然月按日铺。
     主图、构成、楼栋对比、热力图共用此窗口。 */
  const winFrom = s.cFrom
  const winTo = monthIndex(s.cTo) < monthIndex(s.cFrom) ? s.cFrom : s.cTo
  const includesCur = monthIndex(winTo) >= monthIndex(thisMonth)
  const lastComplete = includesCur ? addMonths(thisMonth, -1) : winTo
  const hasComplete = monthIndex(winFrom) <= monthIndex(lastComplete)
  const monthView = IS_LIVE && s.range === 'month'
  const useYearHeat = IS_LIVE && s.range === 'year'

  /* 学期视图：起止由校历决定。主图横轴为第 1 周…第 N 周。
     校历未配时 termSem 为 null，退回近 18 周滚动窗口。 */
  const semesters = normalizeSemesters(s.features?.display?.semesters)
  const termKey = s.cTerm || currentSemesterKey(semesters)
  const termView = IS_LIVE && s.range === 'term'
  const termSem = termView ? findSemester(semesters, termKey) : null
  const termWeeks = termSem ? semesterWeeks(termSem) : []
  const termRange = termSem ? semesterWindow(termSem) : null

  /* 月视图为一个自然月，不是近 30 天。年视图双击钻入时落点必须是该月。 */
  const liveWin = monthView
    ? { ...monthWindow(Number(s.cMonth.slice(0, 4)), Number(s.cMonth.slice(5, 7)) - 1), granularity: 'day' as const }
    : termRange
      ? { ...termRange, granularity: 'week' as const }
      : rangeWindow(s.range)
  /* 主图按天取。liveWin 的 week 为 ISO 周，开学日非周一时会串周。
     构成、楼栋对比、热力图仍用 liveWin 窗口。 */
  const mainWin = termRange ?? liveWin

  /* 年视图：完结月走官方账单。本月账单未出时用日序列补本月柱。 */
  const lcb = useLiveCampusBills(liveBuilding)
  const billMonthsComplete = (lcb.data?.months ?? [])
    .filter((b) => b.month <= lastComplete && b.month >= winFrom)
    .slice()
    .sort((a, b) => a.month.localeCompare(b.month))
  /* 本月日累计。扫描已覆盖天数可加总，不必等账单。 */
  const curWin = currentMonthWindow()
  const lcsCurMonth = useLiveCampusSeries(
    useYearHeat && includesCur ? curWin : null,
    liveBuilding,
    liveFloor,
  )
  const curMonthKwh = seriesSumKwh(lcsCurMonth.data)

  // 当月完全无数时不追加 0 柱。最后读数停在最近有效账单月。
  const yearBarsAll = buildCampusYearBars(
    billMonthsComplete,
    thisMonth,
    includesCur ? curMonthKwh : null,
    seriesMeters(lcsCurMonth.data),
  )

  /* 年视图一律走账单口径。禁止退回与所选账期无关的读数差值曲线。 */
  const useBills = IS_LIVE && s.range === 'year'
  const billMonths = useBills ? yearBarsAll : []
  const emptyWindow = useBills && billMonths.length === 0

  const lcs = useLiveCampusSeries(mainWin, liveBuilding, liveFloor)
  /* 主图首屏：无柱时才铺骨架。换账期时旧图保留，走顶部进度线。 */
  const mainLoading = IS_LIVE && (useBills ? lcb.loading : lcs.loading)
  /* 四列统计用主图同一窗口。年视图除外（账单口径）。 */
  const statWin = useBills || !liveWin ? null : { from: liveWin.from, to: liveWin.to }
  const lsum = useLiveCampusSummary(liveBuilding, liveFloor, statWin)
  const lbdDelta = useLiveBreakdown(useYearHeat ? null : liveWin, liveBuilding)
  /* 账单矩阵只要完结月。本月列用当月日 breakdown 按楼栋加总后拼上。 */
  const lbdBill = useLiveBillBreakdown(
    useYearHeat && hasComplete,
    winFrom,
    lastComplete,
    liveBuilding,
  )
  const lbdCurMonth = useLiveBreakdown(
    useYearHeat && includesCur ? curWin : null,
    liveBuilding,
  )
  const lbd = useYearHeat ? lbdBill : lbdDelta

  const lv = useBills
    ? {
        vals: billMonths.map((b) => b.kwh) as (number | null)[],
        labels: billMonths.map((b) => billFullLabel(b.month, b.provisional)),
        axis: billMonths.map((b) => billAxisLabel(b.month, b.provisional)),
        provisional: billMonths.map((b) => b.provisional),
        meters: billMonths.map((b) => b.meters),
      }
    : termSem && termWeeks.length
      ? (() => {
          const sv = seriesVals(lcs.data)
          const folded = foldWeeks(
            termWeeks,
            sv.vals.map((v, i) => ({ at: new Date(sv.at[i]), value: v, meters: sv.meters[i] })),
          )
          return {
            // 没抄到数的周保持 null。折周禁止把没数变成 0。
            vals: folded.vals.map((v, i) => (folded.has[i] ? v : null)) as (number | null)[],
            labels: termWeeks.map((w) => w.label),
            meters: folded.meters,
            at: termWeeks.map((w) => w.from.getTime()),
            axis: termWeeks.map((w) => w.label) as string[] | null,
            provisional: null as boolean[] | null,
          }
        })()
      : monthView
        ? (() => {
            /* 月视图铺满自然日。稀疏点直接 flex 会变成巨型柱。 */
            const y = Number(s.cMonth.slice(0, 4))
            const m0 = Number(s.cMonth.slice(5, 7)) - 1
            const dense = densifyMonthDays(seriesVals(lcs.data), y, m0)
            return {
              vals: dense.vals.map((v, i) => (dense.has[i] ? v : null)) as (number | null)[],
              labels: dense.labels,
              meters: dense.meters,
              at: dense.labels.map((_, i) => Date.UTC(y, m0, i + 1)),
              axis: dense.labels as string[] | null,
              provisional: null as boolean[] | null,
            }
          })()
        : { ...seriesVals(lcs.data), axis: null as string[] | null, provisional: null as boolean[] | null }
  const liveCS = IS_LIVE
    ? (() => {
        const scale = sci.unit === 'MWh' ? 0.001 : 1
        const n = Math.max(lv.vals.length, 1)
        const vals = lv.vals.length ? lv.vals.map((v) => (v ?? 0) * scale) : [0]
        // kwh 保留未换算原值。户均除表数后仍是 kWh。禁止再走 MWh。
        const kwh = lv.vals.length ? lv.vals.map((v) => v ?? 0) : [0]
        const meters = lv.vals.length ? lv.vals.map((_, i) => lv.meters[i] ?? 0) : [0]
        const has = lv.vals.length ? lv.vals.map((v) => v != null) : [false]
        const provisional = lv.provisional ?? vals.map(() => false)
        const label = (i: number) => lv.labels[i] ?? '—'
        const axisAt = (i: number) => (lv.axis ? lv.axis[i] : null) ?? label(i)
        return {
          n,
          label,
          axisAt,
          unit: sci.unit,
          vals,
          kwh,
          meters,
          has,
          provisional,
          ticks: [axisAt(0), axisAt(Math.floor((n - 1) / 2)), axisAt(n - 1)] as [string, string, string],
        }
      })()
    : null

  /* mock 无入住判定。用范围内房间数当除数。仅演示。 */
  const mockMeters = sci.rooms ?? buildings().reduce((a, b) => a + b.rooms, 0)
  const csFull =
    liveCS ??
    (() => {
      const vals = sci.f === 1 ? cs0.vals : cs0.vals.map((v) => Math.round(v * sci.f * 1000 * 10) / 10)
      return {
        n: cs0.n,
        label: cs0.label,
        axisAt: cs0.label,
        ticks: cs0.ticks,
        unit: sci.unit,
        has: null as boolean[] | null,
        provisional: null as boolean[] | null,
        vals,
        kwh: vals.map((v) => (sci.unit === 'MWh' ? v * 1000 : v)),
        meters: vals.map(() => mockMeters),
      }
    })()
  // 拖拽选区缩放（显示窗口）。
  const zoom = s.cZoom && s.cZoom[1] < csFull.n && s.cZoom[1] > s.cZoom[0] ? s.cZoom : null
  const z0 = zoom ? zoom[0] : 0
  const cs = zoom
    ? (() => {
        const vals = csFull.vals.slice(zoom[0], zoom[1] + 1)
        const label = (i: number) => csFull.label(z0 + i)
        const axisAt = (i: number) => csFull.axisAt(z0 + i)
        const provisional = csFull.provisional
          ? csFull.provisional.slice(zoom[0], zoom[1] + 1)
          : vals.map(() => false)
        const has = csFull.has ? csFull.has.slice(zoom[0], zoom[1] + 1) : null
        const n = vals.length
        return {
          n,
          label,
          axisAt,
          unit: csFull.unit,
          vals,
          kwh: csFull.kwh.slice(zoom[0], zoom[1] + 1),
          meters: csFull.meters.slice(zoom[0], zoom[1] + 1),
          has,
          provisional,
          ticks: [axisAt(0), axisAt(Math.floor((n - 1) / 2)), axisAt(n - 1)] as [string, string, string],
        }
      })()
    : {
        ...csFull,
        provisional: csFull.provisional ?? csFull.vals.map(() => false),
      }
  /* ---- 口径：总量/户均。户均除数只算当期有数房间。空房用量仍在总量。 */
  const PC = s.perCap
  const cVal = (i: number) => {
    if (!PC) return cs.vals[i] ?? 0
    const m = cs.meters[i] ?? 0
    return m > 0 ? (cs.kwh[i] ?? 0) / m : 0
  }
  const cVals = cs.vals.map((_, i) => cVal(i))
  const cpair = PC ? kpair : sci.unit === 'MWh' ? mpair : kpair
  const cUnitTail = PC ? '/户' : ''
  const cstr = (v: number) => {
    const p = cpair(v)
    return p.v + ' ' + p.u + cUnitTail
  }
  /** 户均说明：写出除数。 */
  const pcNote = (meters: number) => (meters > 0 ? ` · ÷ ${meters} 间日均非空房` : ' · 无非空房数据')
  const brush = useBrush(
    cs.n,
    (a, b) => set({ cZoom: [z0 + a, z0 + b], cHover: null, cPin: null }),
    (i, meta) => {
      const touch = meta.pointerType !== 'mouse'
      if (!touch) {
        set((p) => ({ cPin: p.cPin === i ? null : i, cHover: i }))
        return
      }
      const now = Date.now()
      const prev = barTapRef.current
      const dbl = prev.i === i && now - prev.t < 380
      barTapRef.current = { i, t: now }
      if (dbl) {
        set((p) => ({ cPin: p.cPin === i ? null : i, cHover: i }))
      } else {
        // 单击临时查看。换柱时清除钉住。
        set({ cHover: i, cPin: null })
      }
    },
  )
  /* 为柱顶数字留出稳定的呼吸空间，避免最高柱标签撞到网格顶线。 */
  const cmax = Math.max(...cVals, 0.0001) * 1.28
  /* 默认高亮最近有数日。铺满整月后禁止落到月末空槽上。 */
  const lastPresent = (() => {
    if (!cs.has) return cs.n - 1
    for (let i = cs.n - 1; i >= 0; i--) if (cs.has[i]) return i
    return cs.n - 1
  })()
  /* 三态：默认最新有数柱 / 临时查看 hover / 钉住 pin。
     钉住后读数与游标固定。触屏单击临时，双击钉住。 */
  const ci =
    s.cPin != null
      ? Math.min(s.cPin, cs.n - 1)
      : s.cHover != null
        ? Math.min(s.cHover, cs.n - 1)
        : lastPresent
  const cValueLabelStep = mainPlotW > 0 ? Math.max(1, Math.ceil((cs.n * 34) / mainPlotW)) : 1
  const cBars = cVals.map((v, i) => {
    const present = !cs.has || cs.has[i] !== false
    const pinned = s.cPin === i
    const hovered = s.cHover === i
    const isProv = !!cs.provisional[i]
    /* 钉住 = 实心红。临时查看 = 半透明红。默认最新有数柱高亮不描边。 */
    const active =
      s.cPin != null ? pinned : hovered || (s.cHover == null && i === lastPresent)
    let bg: string
    if (!present) bg = 'transparent'
    else if (pinned) bg = 'var(--red)'
    else if (hovered) bg = `color-mix(in srgb, var(--red) ${dark ? 72 : 62}%, transparent)`
    else if (active) bg = 'var(--red)'
    else if (isProv)
      // 本月未出账：红软底加描边，与已出账实柱区分。
      bg = `color-mix(in srgb, var(--red) ${dark ? 28 : 22}%, transparent)`
    else bg = `color-mix(in srgb, var(--fg) ${dark ? 34 : 28}%, transparent)`
    return {
      h: present ? ((v / cmax) * 100).toFixed(1) + '%' : '0%',
      bg,
      pinned,
      peeked: hovered && !pinned,
      provisional: isProv,
      label: cpair(v).v,
      showLabel:
        present &&
        (mobileChart ? s.cHover === i || s.cPin === i : i === ci || i % cValueLabelStep === 0),
      active,
      onEnter: () => {
        if (!coarse && !mobileChart) set({ cHover: i })
      },
    }
  })
  const cSumV = cVals.reduce((a, b) => a + b, 0)
  /* 环比 = 游标格 ÷ 前一格 − 1。标签写明对比哪两格。
     仅取有数且已完结的格子。跳过 provisional 与空日。 */
  const mom = (() => {
    const n = csFull.vals.length
    const usable = (i: number) => {
      if (i < 0 || i >= n) return false
      if (csFull.has && !csFull.has[i]) return false
      if (csFull.provisional && csFull.provisional[i]) return false
      return true
    }
    const valAt = (i: number) => {
      if (!PC) return csFull.vals[i] ?? 0
      const m = csFull.meters[i] ?? 0
      return m > 0 ? (csFull.kwh[i] ?? 0) / m : 0
    }
    let cur = z0 + ci
    while (cur >= 0 && !usable(cur)) cur--
    let prev = cur - 1
    while (prev >= 0 && !usable(prev)) prev--
    if (cur < 0 || prev < 0) return null
    const a = valAt(prev)
    const b = valAt(cur)
    if (!(a > 0)) return null
    return { pct: ((b - a) / a) * 100, from: csFull.axisAt(prev), to: csFull.axisAt(cur) }
  })()
  const cGap = cs.n > 20 ? '2px' : cs.n > 10 ? '4px' : '8px'
  const cCursorX = (cs.n === 1 ? 0 : ((ci + 0.5) / cs.n) * 100).toFixed(2) + '%'
  /* 日视图依赖小时负荷；本校默认关，运维可在 frontend-config 打开 features.charts.day_range */
  const dayRangeOn = !!s.features?.charts?.dayRange
  const rangeDefs: [CRange, string][] = (
    [
      ...(dayRangeOn ? ([['day', '日']] as [CRange, string][]) : []),
      ['month', '月'],
      ['term', '学期'],
      ['year', '年'],
    ] as [CRange, string][]
  )
  useEffect(() => {
    if (!dayRangeOn && s.range === 'day') {
      set({ range: 'month', cHover: null, cPin: null, cZoom: null })
    }
  }, [dayRangeOn, s.range, set])
  const resetChartSel = () => set({ cHover: null, cPin: null, cZoom: null })

  /* 下拉里的月份：库里有多早就能选多早（账单最早月 → 当月），
     一条账单都没有时兜底最近 12 个月，保证下拉不是空的。 */
  const monthOpts = monthOptions((lcb.data?.months ?? []).map((b) => b.month), 12)
  /** 年视图钻取：双击某个月 → 切到该月的日视图（分度值从月变日） */
  const drillToMonth = (i: number) => {
    const bar = billMonths[z0 + i]
    if (!bar) return
    set({ range: 'month', cMonth: bar.month, cHover: null, cPin: null, cZoom: null })
  }

  // ---- 选择器 option：live 来自 GET /api/v1/campus/scopes，mock 来自原型楼栋表
  const bldgOpts = IS_LIVE
    ? scopeOpts.buildings
    : [{ v: 'all', l: '全部楼栋' }].concat(buildings().map((b) => ({ v: b.n, l: b.n + ' · ' + T_LABEL[b.t] })))
  const B = sci.bldg
  const floorOpts = IS_LIVE
    ? scopeOpts.floors
    : B
      ? [{ v: 'all', l: '全部楼层' }].concat(B.floors.map((_, i) => ({ v: String(i), l: i + 1 + 'F' })))
      : []

  /* ---- 四列统计的窗口口径 -------------------------------------------------
     EN 槽位放紧凑标记（12M / 30D），中文说明写完整账期。
     「近 24h」这种与选择器无关的固定窗口已经去掉：四列说的必须是同一段时间。 */
  const winSpanMonths = monthsBetween(winFrom, winTo).length
  const statTag = useBills
    ? winSpanMonths + 'M'
    : monthView
      ? '1M'
      : termSem
        ? termWeeks.length + 'W'
        : s.range === 'term'
          ? '18W'
          : '30D'
  const statWinText = useBills
    ? monthLabel(winFrom) + '–' + monthLabel(winTo)
    : monthView
      ? monthLabel(s.cMonth)
      : termSem
        ? semesterLabel(termSem.key)
        : s.range === 'term'
          ? '近 18 周'
          : '近 30 天'
  /* 年视图的合计取自账单（含「本月」那根日累计柱，与主图一致）；
     户均除数使用当前非空、且对应月份有账单的房间数。 */
  const billSumKwh = billMonths.reduce((a, b) => a + b.kwh, 0)
  const billMetersRef = (() => {
    for (let i = billMonths.length - 1; i >= 0; i--) if (billMonths[i].meters > 0) return billMonths[i].meters
    return 0
  })()
  /* 完结月段。本月账单未出，不进对比。窗口环比与我 vs 户均只用它。 */
  const billComplete = billMonthsComplete.map((b) => ({
    month: b.month,
    kwh: parseFloat(b.total_kwh) || 0,
    meters: b.meters || 0,
  }))
  const billCompleteSum = billComplete.reduce((a, b) => a + b.kwh, 0)
  const billCompleteMeters = billComplete.length ? billComplete[billComplete.length - 1].meters : 0

  /* 窗口跨天数。日均除数。账期长度不一，必须折成日均才能横向比。 */
  const statDays = (() => {
    if (useBills) {
      const from = new Date(Number(winFrom.slice(0, 4)), Number(winFrom.slice(5, 7)) - 1, 1)
      const endKey = includesCur ? thisMonth : winTo
      const end = includesCur
        ? new Date()
        : new Date(Number(endKey.slice(0, 4)), Number(endKey.slice(5, 7)), 1)
      return Math.max(1, Math.round((end.getTime() - from.getTime()) / 86400000))
    }
    if (!liveWin) return 1
    return Math.max(1, Math.round((Date.parse(liveWin.to) - Date.parse(liveWin.from)) / 86400000))
  })()

  /* ---- 我的房间 vs 该范围户均。两侧必须同源。
     年视图本表走 /me/bills，全校走 campus/bills。其它周期走读数差值。 */
  const myMeter = (IS_LIVE && s.user?.meter) || undefined
  const myBills = useLiveBills(useBills ? myMeter : undefined, winFrom, lastComplete)
  const myCons = useLiveMeterSeries(
    useBills ? undefined : myMeter,
    'consumption',
    statWin ? { ...statWin, granularity: 'day' as const } : null,
  )
  const vsRoomLive = (() => {
    const en = 'VS ROOM AVG'
    const scopeName = scoped ? sci.name : '全校'
    if (!s.user) return { en, v: '—', u: '', note: '登录后可比', title: '登录并绑定宿舍电表后，显示你与该范围户均的差距' }
    if (!myMeter) return { en, v: '—', u: '', note: '绑表后可比', title: '绑定宿舍电表后，显示你与该范围户均的差距' }
    if (useBills ? myBills.loading || lcb.loading : myCons.loading || lsum.loading)
      return { en, v: '—', u: '', note: '加载中…', title: '' }
    const rows = useBills
      ? (myBills.data ?? []).filter((b) => b.month >= winFrom && b.month <= lastComplete)
      : null
    const mine = useBills
      ? rows && rows.length
        ? rows.reduce((a, b) => a + (parseFloat(b.usage_kwh) || 0), 0)
        : null
      : seriesSumKwh(myCons.data)
    const avg = useBills
      ? billCompleteMeters > 0 && billComplete.length
        ? billCompleteSum / billCompleteMeters
        : null
      : lsum.data
        ? num(lsum.data.per_room_kwh)
        : null
    const span = useBills
      ? rows && rows.length
        ? monthLabel(rows[0].month) + '–' + monthLabel(rows[rows.length - 1].month)
        : statWinText
      : statWinText
    if (mine == null || avg == null || !(avg > 0))
      return {
        en,
        v: '—',
        u: '',
        note: '暂无可比数据',
        title: useBills
          ? '你的电表在 ' + statWinText + ' 内还没有月账单'
          : '当前周期没有可比读数 · ' +
            availNote(myCons.data?.availability, myCons.data?.quality, myCons.error),
      }
    const pct = ((mine - avg) / avg) * 100
    const mp = kpair(mine)
    const ap = kpair(avg)
    return {
      en,
      v: (pct > 0 ? '+' : '') + pct.toFixed(0),
      u: '%',
      note: '你的电表' + (pct > 0 ? '高于' : '低于') + scopeName + '户均',
      color: pct > 0 ? RED : OK,
      title:
        '你的房间 ' + span + ' 用了 ' + mp.v + ' ' + mp.u + '，' + scopeName + '户均 ' + ap.v + ' ' + ap.u,
    }
  })()

  // ---- campusStats：选中楼栋 vs 全校，两个变体 + vsBlock
  const myK = k[29]
  const myP = kpair(myK)
  const roomAvg = B ? sci.kwh! / sci.rooms! : dm.reduce((a, x) => a + x, 0) / dm.length
  const myVs = ((myK - roomAvg) / roomAvg) * 100
  const vsBlock = {
    en: 'VS ROOM AVG',
    v: s.user ? (myVs > 0 ? '+' : '') + myVs.toFixed(0) : '—',
    u: s.user ? '%' : '',
    note: s.user ? (myVs > 0 ? '高于房均' : '低于房均') : '登录后可比',
    color: s.user ? (myVs > 0 ? RED : OK) : undefined,
    title: s.user
      ? '你的房间昨日 ' + myP.v + ' ' + myP.u + '，较该范围房均 ' + (myVs > 0 ? '+' : '') + myVs.toFixed(0) + '%'
      : '登录并绑定电表后，与该范围房均对比',
  }
  const bY = kpair(sci.kwh || 0)
  const bR = kpair(roomAvg)
  const cY = mpair(213.8)
  const cR = kpair(roomAvg)
  /* 总量与户均块并列。口径开关只决定谁在头一格。 */
  const orderByBasis = <T,>(total: T, per: T): [T, T] => (PC ? [per, total] : [total, per])
  const campusStats = IS_LIVE
    ? (() => {
        /* 年视图合计/户均取自账单。其它周期取 campus/summary 同窗口结果。 */
        const t = useBills ? (billMonths.length ? billSumKwh : null) : lsum.data ? num(lsum.data.total_kwh) : null
        const meters = useBills ? billMetersRef : (lsum.data?.meters ?? lsum.data?.quality?.covered ?? 0)
        const pr = useBills ? (meters > 0 && t != null ? t / meters : null) : lsum.data ? num(lsum.data.per_room_kwh) : null
        const sumNote = useBills
          ? billMonths.length
            ? '账单口径 · ' + billMonths.length + ' 个月' + (includesCur ? '（末月为本月日累计，账单未出）' : '')
            : '所选账期内没有月账单'
          : availNote(lsum.data?.availability, lsum.data?.quality, lsum.error)
        const tPair = t == null ? { v: '—', u: '' } : scoped || t < 1000 ? kpair(t) : mpair(t / 1000)
        const prPair = pr == null ? { v: '—', u: '' } : kpair(pr)
        /* 说明行只留一句。来源与除数放 title。 */
        const totalBlock = {
          en: 'TOTAL · ' + statTag,
          v: tPair.v,
          u: tPair.u,
          note: scoped ? sci.name : '全校',
          title: statWinText + ' 合计 · ' + sumNote,
        }
        const perBlock = {
          en: 'PER METER · ' + statTag,
          v: prPair.v,
          u: prPair.u ? prPair.u + '/户' : '',
          note: meters > 0 ? '÷ ' + meters + ' 间日均非空房' : '无非空房数据',
          title: statWinText + ' 户均' + pcNote(meters),
        }
        /* 第三格用日均。环比常因账期不等长不可比。 */
        const dailyRaw = t == null ? null : (PC ? (meters > 0 ? t / meters : null) : t)
        const daily = dailyRaw == null ? null : dailyRaw / statDays
        const dPair = daily == null ? { v: '—', u: '' } : kpair(daily)
        const dailyBlock = {
          en: 'DAILY AVG · ' + statTag,
          v: dPair.v,
          u: daily == null ? '' : dPair.u + (PC ? '/户·天' : '/天'),
          note: statDays + ' 天摊平',
          title:
            (PC ? '户均日用电' : (scoped ? sci.name : '全校') + '日均用电') +
            ' = ' +
            statWinText +
            ' 合计 ÷ ' +
            statDays +
            ' 天' +
            (daily == null ? '' : ' · ≈ ' + alt(daily) + '/天'),
        }
        return [...orderByBasis(totalBlock, perBlock), dailyBlock, vsRoomLive]
      })()
    : B
      ? [
          ...orderByBasis(
            { en: 'TOTAL · 24H', v: bY.v, u: bY.u, note: sci.name, title: sci.name + ' 昨日用电' },
            {
              en: 'PER METER · 24H',
              v: bR.v,
              u: bR.u + '/户',
              note: '共 ' + sci.rooms! + ' 间',
              title: '昨日户均 · 该范围共 ' + sci.rooms! + ' 间',
            },
          ),
          { en: 'MOM', v: (B.mom > 0 ? '+' : '') + B.mom.toFixed(1), u: '%', note: '较上一周期', title: '与上一周期相比' },
          vsBlock,
        ]
      : [
          ...orderByBasis(
            { en: 'TOTAL · 24H', v: cY.v, u: cY.u, note: '全校昨日', title: '全校昨日用电，较前日 +6.1%' },
            { en: 'PER METER · 24H', v: cR.v, u: cR.u + '/户', note: '全校宿舍', title: '全校宿舍昨日户均，可直接对照自己房间' },
          ),
          { en: 'PEAK HOUR', v: '21–22', u: '时', note: '尖峰时段', title: '全校尖峰时段，错峰用电最省' },
          vsBlock,
        ]

  /* 矩阵按什么拆：全校看楼栋，缩到某栋就看它的楼层。
     热力图、走势、构成、对比四块共用这一个词，措辞不能各写各的。 */
  const mixLabel = (IS_LIVE ? lbd.data?.group_by : B ? 'floor' : 'building') === 'floor' ? '楼层' : '楼栋'

  /* ---- 热力图
     年视图：完结月=账单矩阵，最后一列「本月」=当月日 breakdown 按楼栋加总。
     其它周期：读数差值 breakdown。 */
  const hmMerged = (() => {
    if (!IS_LIVE) {
      const scope = B ? [B] : buildings()
      const days = 14
      const rows: CampusBreakdownRow[] = scope.map((b) => ({
        key: b.n,
        total_kwh: String(b.kwh),
        share: 0,
        meters: b.rooms,
        meter_counts: Array(days).fill(b.rooms),
        values: Array.from({ length: days }, (_, d) =>
          String(((b.kwh / 30) * (0.72 + rnd(d, b.n.length + 3) * 0.6)).toFixed(2)),
        ) as (string | null)[],
      }))
      const buckets = Array.from({ length: 14 }, (_, d) => new Date(2026, 6, 13 + d).toISOString())
      return { rows, buckets, gran: 'day' as const, max: 0 }
    }
    if (!useYearHeat) {
      /* 不再截前 14 行：本校现在 16 栋，将来接别的校区只会更多，
         截断的后果是「有的楼在图上根本不存在」，比图长一点糟得多。 */
      const rows = lbd.data?.rows ?? []
      const buckets = lbd.data?.buckets ?? []
      return {
        rows,
        buckets,
        gran: (lbd.data?.granularity as 'day' | 'week' | 'month') || 'day',
        max: lbd.data ? parseFloat(lbd.data.max_cell_kwh) : 0,
      }
    }
    // 年：窗口内的账单列（+ 窗口含当月时再拼一列当月日累计）
    const billRows = hasComplete ? (lbdBill.data?.rows ?? []) : []
    const billBuckets = hasComplete ? (lbdBill.data?.buckets ?? []) : []
    const curByKey = new Map<string, number>()
    for (const r of lbdCurMonth.data?.rows ?? []) {
      let s = 0
      let any = false
      for (const v of r.values) {
        if (v == null) continue
        const n = parseFloat(v)
        if (!Number.isFinite(n)) continue
        s += n
        any = true
      }
      if (any) curByKey.set(r.key, s)
    }
    const includeCurrentBreakdown = includesCur && hasBreakdownValues(lbdCurMonth.data?.rows ?? [])
    const keys = new Set<string>([...billRows.map((r) => r.key), ...(includeCurrentBreakdown ? curByKey.keys() : [])])
    const curMeterOf = new Map((lbdCurMonth.data?.rows ?? []).map((r) => [r.key, r.meters]))
    const rows: CampusBreakdownRow[] = [...keys].map((key) => {
      const base = billRows.find((r) => r.key === key)
      const vals = (base?.values ?? billBuckets.map(() => null)).slice() as (string | null)[]
      const meterCounts = (base?.meter_counts ?? billBuckets.map(() => base?.meters ?? 0)).slice()
      if (includeCurrentBreakdown) {
        const cur = curByKey.get(key)
        vals.push(cur != null ? String(cur) : null)
        meterCounts.push(curMeterOf.get(key) ?? 0)
      }
      const total =
        vals.reduce((a, v) => a + (v == null ? 0 : parseFloat(v) || 0), 0)
      const knownMeters = meterCounts.filter((n) => n > 0)
      return {
        key,
        total_kwh: String(total),
        share: 0,
        meters: knownMeters.length ? Math.round(knownMeters.reduce((a, n) => a + n, 0) / knownMeters.length) : 0,
        meter_counts: meterCounts,
        values: vals,
      }
    })
    rows.sort((a, b) => parseFloat(b.total_kwh) - parseFloat(a.total_kwh))
    const grand = rows.reduce((a, r) => a + parseFloat(r.total_kwh), 0) || 1
    for (const r of rows) r.share = parseFloat(r.total_kwh) / grand
    // 本月列用月初 ISO，刻度解析成 YYYY-MM
    const thisBucket = new Date(
      Number(thisMonth.slice(0, 4)),
      Number(thisMonth.slice(5, 7)) - 1,
      1,
    ).toISOString()
    const buckets = includeCurrentBreakdown ? [...billBuckets, thisBucket] : [...billBuckets]
    let max = 0
    for (const r of rows) {
      for (const v of r.values) {
        if (v == null) continue
        const n = parseFloat(v)
        if (n > max) max = n
      }
    }
    return { rows, buckets, gran: 'month' as const, max }
  })()
  const hmRows = hmMerged.rows
  const hmBuckets = hmMerged.buckets
  const hmGran = hmMerged.gran
  const metersAt = (row: CampusBreakdownRow, i: number) => row.meter_counts?.[i] ?? row.meters
  /* 户均口径要重算峰值：后端给的 max_cell_kwh 是总量口径的，拿它当分母色阶会全糊 */
  const hmMax = PC
    ? Math.max(
        ...hmRows.flatMap((r) =>
          r.values.map((v, i) => {
            const meters = metersAt(r, i)
            return v == null || meters <= 0 ? 0 : parseFloat(v) / meters
          }),
        ),
        0.001,
      )
    : hmMerged.max ||
      Math.max(...hmRows.flatMap((r) => r.values.map((v) => (v == null ? 0 : parseFloat(v)))), 0.001)
  /* 刻度与 buckets 一一对应。月粒度用 25/07；最后一列若是本月加 ·本月。 */
  const hmTick = (raw: string, idx?: number) => {
    const ym = bucketYearMonth(raw)
    if (hmGran === 'month' && ym) {
      const lab = ym.y.slice(2) + '/' + ym.m
      const isCurCol =
        useYearHeat &&
        idx === hmBuckets.length - 1 &&
        ym.y + '-' + ym.m === thisMonth
      return isCurCol ? lab + '·本月' : lab
    }
    if (hmGran === 'week' && ym) {
      const d = new Date(raw)
      const day = String(d.getUTCDate()).padStart(2, '0')
      return ym.y.slice(2) + '/' + ym.m + '/' + day
    }
    if (ym) return Number(ym.m) + '/' + new Date(raw).getUTCDate()
    return raw
  }
  /* 刻度疏密按实测宽度定。原先「月粒度每列都标」在桌面上没问题，
     手机上 12 列挤在 300px 里，每个标签被自己那格裁成「25/(」，一个都读不出来。 */
  const hmLabelStep = (() => {
    const per = hmGran === 'week' ? 52 : 40
    const room = Math.max(2, Math.floor((hmW || 720) / per))
    return hmBuckets.length <= room ? 1 : Math.ceil(hmBuckets.length / room)
  })()
  const hmShowLabel = (i: number) => i === 0 || i === hmBuckets.length - 1 || i % hmLabelStep === 0
  /* 列宽一律按可视区平分：原先日/周粒度靠 minWidth + 横向滚动，
     那个滚动容器会把格子的浮窗一起裁掉。列数最多 31，平分下来照样看得清。 */
  const hmCellH = hmGran === 'month' ? 26 : 22
  /* ---- 色阶区间（见 HeatScale）。存的是峰值的比例，换口径/换账期不会跳。 */
  const hmLo = Math.max(0, Math.min(s.cHmLo, 0.96))
  const hmHi = Math.min(1, Math.max(s.cHmHi, hmLo + 0.04))
  const hmLoV = hmLo * hmMax
  const hmHiV = hmHi * hmMax
  /** 区间外（偏低那一侧）的平色。要与「无数」的空格区分：那个是透明加边框 */
  const hmOutTone = `color-mix(in srgb, var(--fg) ${dark ? 9 : 6}%, transparent)`
  /** 某一格的数值（已按当前口径折算）；null = 没数或算不出户均 */
  const hmValue = (raw: string | null, meters: number): number | null => {
    if (raw == null) return null
    const kwh = parseFloat(raw)
    if (!Number.isFinite(kwh)) return null
    if (!PC) return kwh
    return meters > 0 ? kwh / meters : null
  }
  /* 空格 = 无账单/无读数。0 度是有数的浅色。二者禁止混用。
     户均按该时间桶日均非空房数折算。区间外格子压成平色，不消失。 */
  const hmCell = (raw: string | null, meters: number) => {
    const v = hmValue(raw, meters)
    /* 空格线用 inset 阴影，禁止用 border。border 会使空格与有数格差 2px，
       导致列与日期轴错位。阴影不参与布局。 */
    if (v == null)
      return {
        bg: 'transparent',
        ring: 'inset 0 0 0 1px var(--line2)',
        title: raw == null ? '无有效读数' : '无非空房，算不出户均',
      }
    if (v < hmLoV) return { bg: hmOutTone, ring: 'none', title: '低于色阶下限' }
    const t = hmHiV > hmLoV ? Math.min(1, (v - hmLoV) / (hmHiV - hmLoV)) : 1
    return {
      bg: `color-mix(in srgb, var(--red) ${(4 + t * 88).toFixed(0)}%, transparent)`,
      ring: 'none',
      title: '',
    }
  }
  /* ---- 格子浮窗。title 属性要等一秒、样式还归浏览器管，
     这块和「用电日历」是同一种读法，就该用同一种浮窗。 */
  const hmHoverCell = s.cHmCell
  const hmHoverV =
    hmHoverCell && hmRows[hmHoverCell[0]]
      ? hmValue(
          hmRows[hmHoverCell[0]].values[hmHoverCell[1]] ?? null,
          metersAt(hmRows[hmHoverCell[0]], hmHoverCell[1]),
        )
      : null
  const hmTip = (() => {
    if (!hmHoverCell) return null
    const [ri, ci] = hmHoverCell
    const row = hmRows[ri]
    if (!row) return null
    const raw = row.values[ci] ?? null
    const cellMeters = metersAt(row, ci)
    const v = hmValue(raw, cellMeters)
    const kwh = raw == null ? null : parseFloat(raw)
    // 该列全部楼栋合计。用于算该格占全校比例。
    let colSum = 0
    for (const r of hmRows) {
      const x = r.values[ci]
      if (x == null) continue
      const n = parseFloat(x)
      if (Number.isFinite(n)) colSum += n
    }
    // 该行自己的均值。用于判断该格对本栋是否偏高。
    const own = row.values.map((x) => (x == null ? null : parseFloat(x))).filter((x): x is number => x != null && Number.isFinite(x))
    const ownAvg = own.length ? own.reduce((a, b) => a + b, 0) / own.length : 0
    const pair = v == null ? null : PC ? kpair(v) : v >= 1000 ? mpair(v / 1000) : kpair(v)
    return {
      head: row.key + ' · ' + hmTick(hmBuckets[ci] ?? '', ci),
      v: pair ? pair.v : '—',
      u: pair ? pair.u + (PC ? '/户' : '') : '',
      alt:
        kwh == null
          ? '这一格没有有效读数'
          : PC
            ? kwh.toFixed(1) + ' kWh ÷ ' + cellMeters + ' 间日均非空房'
            : '≈ ' + alt(kwh) + ' · ' + cellMeters + ' 间日均非空房',
      share: kwh == null || colSum <= 0 ? '' : '占该' + (hmGran === 'month' ? '月' : '期') + '全部' + (mixLabel === '楼层' ? '楼层' : '楼栋') + ' ' + ((kwh / colSum) * 100).toFixed(1) + '%',
      vsOwn:
        kwh == null || ownAvg <= 0
          ? ''
          : '较本' + (mixLabel === '楼层' ? '层' : '栋') + '均值 ' + (kwh >= ownAvg ? '+' : '') + (((kwh - ownAvg) / ownAvg) * 100).toFixed(0) + '%',
      vsOwnColor: kwh != null && kwh >= ownAvg ? RED : OK,
      row: ri,
      col: ci,
    }
  })()
  const hmNote = IS_LIVE
    ? useYearHeat
      ? lbdBill.error || lbdCurMonth.error
        ? String(lbdBill.error || lbdCurMonth.error)
        : lbdBill.loading || lbdCurMonth.loading
          ? '加载中…'
          : ''
      : availNote(lbd.data?.availability, lbd.data?.quality, lbd.error)
    : '示例数据 · 接后端后为真实抄表值'

  /* ---- 用电构成与楼栋对比。与热力图共用行数据。
     年视图用 hmMerged（含本月列），禁止用接口原样行漏掉本月。 */
  const scopeRows: CampusBreakdownRow[] = useYearHeat ? hmMerged.rows : (lbd.data?.rows ?? [])
  const scopeTotal = scopeRows.reduce((a, r) => a + (parseFloat(r.total_kwh) || 0), 0)

  /* 用电构成：按楼栋拆。选中某栋后按该栋楼层拆。
     禁止按建筑类别拆：库存仅有宿舍电表。 */
  const mixRowsRaw: { name: string; share: number; kwh: number; meters: number }[] = IS_LIVE
    ? scopeRows
        .map((r) => ({
          name: r.key,
          kwh: parseFloat(r.total_kwh) || 0,
          share: scopeTotal > 0 ? ((parseFloat(r.total_kwh) || 0) / scopeTotal) * 100 : 0,
          meters: r.meters,
        }))
        .sort((a, b) => b.kwh - a.kwh)
    : (() => {
        const all = buildings()
        const scope = B ? [B] : all
        const total = scope.reduce((a, b) => a + b.kwh, 0) || 1
        return scope
          .map((b) => ({ name: b.n, share: (b.kwh / total) * 100, kwh: b.kwh, meters: b.rooms }))
          .sort((a, b) => b.kwh - a.kwh)
      })()
  /* 占满整行之后不再截断：一张能横着摊开的表，16 行看得完，
     「其他 8 栋」那种合并行反而把想查的楼藏了起来。 */
  const mixRows = mixRowsRaw
  const mixTotal = IS_LIVE ? scopeTotal : cSumV
  const mixTotalPair = mixTotal >= 1000 ? mpair(mixTotal * 0.001) : kpair(mixTotal)
  const mixTotalUnit = mixTotalPair.u as ConsumptionDisplayUnit
  /* 构成的排序与色阶始终按**总量**：占比条画的就是总量的构成，
     户均只换右侧那一列读数（谁户均高看下面「楼栋对比」，那块才按户均重排）。 */
  const mixAvgPer = (() => {
    const m = mixRows.reduce((a, r) => a + r.meters, 0)
    return m > 0 ? mixRows.reduce((a, r) => a + r.kwh, 0) / m : 0
  })()
  const mixSegs = mixRows.map((m, i) => {
    const tone = i === 0 ? RED : `color-mix(in srgb, var(--fg) ${Math.max(12, 48 - i * 4)}%, transparent)`
    const per = m.meters > 0 ? m.kwh / m.meters : null
    return {
      name: m.name,
      rank: i + 1,
      pct: m.share.toFixed(1) + '%',
      w: m.share + '%',
      bg: tone,
      dot: tone,
      // 全宽表里两个口径都列出来，不必再靠切开关来回看
      total: formatKwhInUnit(m.kwh, mixTotalUnit, elecRate).v,
      per: per == null ? '—' : kpair(per).v,
      // 户均较全范围户均的偏离。专答是否费电，不答是否规模大。
      dev: per == null || mixAvgPer <= 0 ? null : ((per - mixAvgPer) / mixAvgPer) * 100,
      meters: m.meters,
    }
  })
  const mixPerUnit = kpair(1).u + '/户'

  /* ---- 楼栋对比 / 楼层对比：两块横向条形排名，照 bills_dashboard 那两张卡片的读法
     （降序、条长即量、右侧标数），视觉仍是本站 token 与红阶，不用 ECharts 的绿蓝渐变。

     和页面顶部的范围选择**解耦**：缩到某栋之后，仍然应该能看见全校楼栋的排名，
     否则「我这栋在全校算高还是低」就没地方回答了。
     两块图各自按需取数；key 与主矩阵重合时命中同一份缓存，不会真的多打接口。 */
  const bldgNames = IS_LIVE
    ? scopeOpts.buildings.filter((o) => o.v !== 'all').map((o) => o.v)
    : buildings().map((b) => b.n)
  /** 楼层面板看哪一栋：用户选过就听用户的，否则跟随页面范围，再否则最费电那栋 */
  const cmpBldg =
    (s.cCmpBldg && bldgNames.includes(s.cCmpBldg) ? s.cCmpBldg : '') ||
    (scoped ? s.bldgScope : '') ||
    (IS_LIVE ? (mixRowsRaw[0]?.name ?? '') : buildings()[0]?.n ?? '')
  /* 主矩阵已经是楼栋维度时不必再取一次；已经是这栋的楼层维度时同理 */
  const needBldgFetch = IS_LIVE && scoped
  const needFloorFetch = IS_LIVE && !!cmpBldg && !(scoped && cmpBldg === s.bldgScope)
  const cmpBldgDelta = useLiveBreakdown(!useYearHeat && needBldgFetch ? liveWin : null, undefined)
  const cmpBldgBill = useLiveBillBreakdown(useYearHeat && hasComplete && needBldgFetch, winFrom, lastComplete, undefined)
  const cmpFloorDelta = useLiveBreakdown(!useYearHeat && needFloorFetch ? liveWin : null, cmpBldg)
  const cmpFloorBill = useLiveBillBreakdown(useYearHeat && hasComplete && needFloorFetch, winFrom, lastComplete, cmpBldg)

  interface CmpRow {
    name: string
    kwh: number
    meters: number
    per: number
  }
  /* 户均口径下按户均重排：这一块问的就是「谁真的费电」，
     不换排序的话，表多的楼永远排在前面，等于还在看总量。 */
  const toCmp = (rows: CampusBreakdownRow[]): CmpRow[] =>
    rows
      .map((r) => {
        const kwh = parseFloat(r.total_kwh) || 0
        return { name: r.key, kwh, meters: r.meters, per: r.meters > 0 ? kwh / r.meters : 0 }
      })
      .sort((a, b) => (PC ? b.per - a.per : b.kwh - a.kwh))
  const bldgCmpRows = IS_LIVE
    ? toCmp(needBldgFetch ? ((useYearHeat ? cmpBldgBill.data?.rows : cmpBldgDelta.data?.rows) ?? []) : scopeRows)
    : buildings()
        .map((b) => ({ name: b.n, kwh: b.kwh, meters: b.rooms, per: b.kwh / b.rooms }))
        .sort((a, b) => (PC ? b.per - a.per : b.kwh - a.kwh))
  const floorCmpRows = IS_LIVE
    ? toCmp(
        needFloorFetch ? ((useYearHeat ? cmpFloorBill.data?.rows : cmpFloorDelta.data?.rows) ?? []) : scopeRows,
      )
    : (() => {
        const b = buildings().find((x) => x.n === cmpBldg) ?? buildings()[0]
        if (!b) return []
        const rooms = Math.max(1, Math.round(b.rooms / b.f))
        return b.floors
          .map((kwh, i) => ({ name: i + 1 + 'F', kwh, meters: rooms, per: kwh / rooms }))
          .sort((x, y) => (PC ? y.per - x.per : y.kwh - x.kwh))
      })()
  const cmpLoading = IS_LIVE
    ? useYearHeat
      ? cmpBldgBill.loading || cmpFloorBill.loading || lbdBill.loading
      : cmpBldgDelta.loading || cmpFloorDelta.loading || lbd.loading
    : false
  /* 年视图补取的完结月账单不含本月。主矩阵含本月。范围缩小时须说明差异。 */
  const cmpNoCurMonth = useYearHeat && includesCur

  /* ---- 走势对比（多序列折线）。与热力图共用楼栋×时间矩阵。
     热力图答哪格烫。折线答谁在涨、谁跟大盘。
     颜色不承担身份。非聚焦序列同色发灰细线。聚焦条变红加粗。 */
  const trendAll = hmRows
    .map((r) => {
      const meterCounts = r.values.map((_, i) => metersAt(r, i))
      const pts = r.values.map((v, i) => {
        if (v == null) return null
        const n = parseFloat(v)
        if (!Number.isFinite(n)) return null
        // 每列用该桶日均非空房数。禁止用窗口平均值覆盖历史。
        return PC ? (meterCounts[i] > 0 ? n / meterCounts[i] : null) : n
      })
      return { key: r.key, meters: r.meters, meterCounts, pts, total: pts.reduce<number>((a, v) => a + (v ?? 0), 0) }
    })
    .filter((r) => r.pts.some((v) => v != null))
    .sort((a, b) => b.total - a.total)
  /* 全部序列都画、都进图例。图例超高时自滚动。 */
  const trendRows = trendAll
  const trendN = hmBuckets.length
  /* 仅一列时间时禁止画走势。 */
  const trendOn = trendRows.length > 0 && trendN >= 2
  const trendMax = Math.max(...trendRows.flatMap((r) => r.pts.map((v) => v ?? 0)), 0.001) * 1.08
  /** 列 → 横坐标百分比（锚在格点上，不是格心） */
  const tx = (i: number) => (trendN <= 1 ? 50 : (i / (trendN - 1)) * 100)
  /** 缺账单或读数处断开。禁止直线跨空月补数。 */
  const trendPath = (pts: (number | null)[]) => {
    let d = ''
    let pen = false
    pts.forEach((v, i) => {
      if (v == null) {
        pen = false
        return
      }
      const x = tx(i).toFixed(3)
      const y = (100 - (v / trendMax) * 100).toFixed(3)
      // 孤立点补一个同点 L，靠 round linecap 画成一个圆点，否则单点的序列什么都不显示
      d += (pen ? 'L' : 'M') + x + ' ' + y + (pen ? ' ' : ' L' + x + ' ' + y + ' ')
      pen = true
    })
    return d.trim()
  }
  /* 钉住列 (已固定数轴) 与 实时预览列 (第二根游走竖线) */
  const trendColPin = s.cTrendColPin != null && s.cTrendColPin >= 0 && s.cTrendColPin < trendN ? s.cTrendColPin : null
  const previewCol = s.cTrendCol != null && s.cTrendCol >= 0 && s.cTrendCol < trendN ? s.cTrendCol : null
  const defaultCol = (() => {
    for (let i = trendN - 1; i >= 0; i--) if (trendRows.some((r) => r.pts[i] != null)) return i
    return Math.max(0, trendN - 1)
  })()
  /* 底部表格固定看 trendColPin，未钉住时看 previewCol */
  const tableCol = trendColPin ?? (previewCol ?? defaultCol)
  /* 当前视角的重点列 (有预览优先显示预览，无预览显示钉住/默认) */
  const trendCol = previewCol ?? tableCol
  const trendColLabel = hmTick(hmBuckets[trendCol] ?? '', trendCol)
  const tableColLabel = hmTick(hmBuckets[tableCol] ?? '', tableCol)
  /* 换范围或账期后旧钉住 key 可不在表里。必须校验。 */
  const trendPinned = s.cTrendPin && trendRows.some((r) => r.key === s.cTrendPin) ? s.cTrendPin : null
  const trendFocus =
    trendRows.find((r) => r.key === (trendPinned ?? s.cTrendKey)) ?? trendRows[0] ?? null
  /* 单位整图只算一次。y 轴与图例共用同一把尺。 */
  const trendUseM = !PC && trendMax >= 1000
  const trendVal = (v: number) => (trendUseM ? mpair(v / 1000) : kpair(v))
  const trendUnit = trendVal(trendMax).u + (PC ? '/户' : '')
  /** 列总账。总量求和。户均按表数加权。禁止直接平均。 */
  const trendColTotal = (i: number) => {
    let sum = 0
    let meters = 0
    let any = false
    for (const r of trendRows) {
      const v = r.pts[i]
      if (v == null) continue
      any = true
      if (PC) {
        const bucketMeters = r.meterCounts[i] ?? 0
        sum += v * bucketMeters
        meters += bucketMeters
      } else sum += v
    }
    if (!any) return null
    return PC ? (meters > 0 ? sum / meters : null) : sum
  }
  /* 横轴按首尾等距取样。旧的「固定步长 + 强插末项」会让最后两项贴在一起；
     手机游走时也不再临时插入标签，避免横轴随手指跳动。 */
  const trendTickWidth = Math.max(
    56,
    ...hmBuckets.map((b, i) => hmTick(b, i).length * 6.2 + 14),
  )
  const trendTickRoom = Math.min(
    trendN,
    Math.max(2, Math.floor((trendW || (mobileChart ? 320 : 720)) / trendTickWidth)),
  )
  const trendTickIndices = (() => {
    const out = new Set<number>()
    if (trendN <= 0) return out
    if (trendN === 1 || trendTickRoom === 1) {
      out.add(0)
      return out
    }
    for (let slot = 0; slot < trendTickRoom; slot++) {
      out.add(Math.round((slot * (trendN - 1)) / (trendTickRoom - 1)))
    }
    return out
  })()
  const trendShowLabel = (i: number) =>
    trendTickIndices.has(i) || (!mobileChart && s.cTrendCol != null && i === trendCol)
  const trendUnitWord = mixLabel === '楼层' ? '层' : '栋'
  const trendFocusV = trendFocus ? trendFocus.pts[trendCol] : null
  /* 右上角读数细节：环比、列内排名、备选换算。 */
  const trendDetails = (() => {
    if (!trendOn || !trendFocus || trendFocusV == null) return null
    const p = trendVal(trendFocusV)
    let prevI = trendCol - 1
    while (prevI >= 0 && trendFocus.pts[prevI] == null) prevI--
    const prevV = prevI >= 0 ? trendFocus.pts[prevI] : null
    const delta = prevV != null && prevV > 0 ? ((trendFocusV - prevV) / prevV) * 100 : null
    const colVals = trendRows.map((r) => r.pts[trendCol]).filter((v): v is number => v != null)
    const rank = colVals.filter((v) => v > trendFocusV).length + 1
    const focusMeters = trendFocus.meterCounts[trendCol] ?? 0
    return {
      head: trendFocus.key + ' · ' + trendColLabel,
      v: p.v,
      u: trendUnit,
      alt:
        (PC ? '总量 ' + kpair(trendFocusV * focusMeters).v + ' ' + kpair(1).u : '≈ ' + alt(trendFocusV)) +
        ' · ' +
        focusMeters +
        ' 间日均非空房',
      delta:
        delta == null
          ? null
          : (delta > 0 ? '+' : '') + delta.toFixed(1) + '%',
      deltaColor: delta == null ? 'var(--fg3)' : delta > 0 ? RED : OK,
      rank: '第 ' + rank + ' / ' + colVals.length + ' ' + trendUnitWord,
    }
  })()

  /* 并排面板窗口说明。文案结构与行数一致，标题与正文对齐。 */
  const winLabel = useYearHeat
    ? `${monthLabel(winFrom)} – ${monthLabel(winTo)}`
    : monthView
      ? monthLabel(s.cMonth)
      : '当前周期'

  return (
    <div data-screen-label="全校用电" style={{ animation: 'rise .28s ease both' }}>
      <section style={{ padding: '52px 0 46px', borderBottom: '1px solid var(--line)' }}>
        <div data-r="hdr" style={{ display: 'flex', alignItems: 'flex-end', gap: '30px', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
            <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
              CAMPUS LOAD · {s.features.display.campusName}
            </div>
            <div style={{ fontSize: '34px', fontWeight: 600, letterSpacing: '-.04em' }}>{sci.name}</div>
          </div>
          <div
            data-r="hdrside"
            style={{
              marginLeft: 'auto',
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'flex-end',
              gap: '12px',
            }}
          >
          <div style={{ display: 'flex', gap: '10px', flexWrap: 'wrap', alignItems: 'center' }}>
            <select
              value={s.bldgScope}
              onChange={(e) => set({ bldgScope: e.target.value, floorScope: 'all', cHover: null, cPin: null, cZoom: null })}
              style={{
                padding: '8px 12px',
                border: '1px solid var(--line)',
                background: 'var(--bg)',
                color: 'var(--fg)',
                font: "400 12.5px/1 'Instrument Sans','Noto Sans SC',sans-serif",
                cursor: 'pointer',
                maxWidth: '190px',
              }}
            >
              {bldgOpts.map((o) => (
                <option key={o.v} value={o.v}>
                  {o.l}
                </option>
              ))}
            </select>
            {(B || (scoped && floorOpts.length > 0)) && (
              <select
                value={s.floorScope}
                onChange={(e) => set({ floorScope: e.target.value, cHover: null, cPin: null, cZoom: null })}
                style={{
                  padding: '8px 12px',
                  border: '1px solid var(--line)',
                  background: 'var(--bg)',
                  color: 'var(--fg)',
                  font: "400 12.5px/1 'Instrument Sans','Noto Sans SC',sans-serif",
                  cursor: 'pointer',
                }}
              >
                {floorOpts.map((o) => (
                  <option key={o.v} value={o.v}>
                    {o.l}
                  </option>
                ))}
              </select>
            )}
            {zoom && (
              <button
                className="hv-line-fg"
                onClick={resetChartSel}
                style={{
                  background: 'none',
                  border: '1px solid var(--line)',
                  borderRadius: '999px',
                  margin: 0,
                  padding: '6px 14px',
                  font: 'inherit',
                  fontSize: '12px',
                  color: 'var(--fg2)',
                  cursor: 'pointer',
                }}
              >
                重置缩放 ✕
              </button>
            )}
            <span className="chart-tap-hint">轻触柱形查看数值</span>
            <SegGroup>
              {rangeDefs.map((r) => (
                <SegBtn
                  key={r[0]}
                  seg={seg(s.range === r[0], r[1], () =>
                    set({ range: r[0], cHover: null, cPin: null, cZoom: null }),
                  )}
                  pad="6px 15px"
                  fs="12.5px"
                />
              ))}
            </SegGroup>
          </div>
          {/* 口径开关管住整页：主图 / 四列统计 / 用电构成 / 楼栋对比 / 热力图 一起切，
              避免出现「这块是总量、那块是户均」的错读。 */}
          <BasisSwitch
            label="口径"
            value={s.perCap ? 'per' : 'total'}
            onChange={(v) => {
              set({ perCap: v === 'per' })
              persist({ perCap: v === 'per' })
            }}
            options={[
              { v: 'total', l: '总量', title: '范围内所有电表的合计' },
              { v: 'per', l: '户均', title: '合计 ÷ 当前非空且当期有数的房间数' },
            ]}
          />
          </div>
        </div>
        {/* 账期窗口：主图标题下方独立一行，避免挤在右上角看不见。
            选完即重绘，下面的用电构成 / 楼栋对比 / 热力图跟着走同一个窗口。 */}
        {termView ? (
          <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginTop: '18px', flexWrap: 'wrap' }}>
            <SemesterPicker
              value={termSem ? termSem.key : termKey}
              options={semesters}
              onChange={(key) => set({ cTerm: key, cHover: null, cPin: null, cZoom: null })}
            />
          </div>
        ) : (
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '12px',
              marginTop: '18px',
              flexWrap: 'wrap',
            }}
          >
            {useYearHeat ? (
              <MonthWindowPicker
                label="RANGE"
                from={winFrom}
                to={winTo}
                options={monthOpts}
                onChange={(a, b) => set({ cFrom: a, cTo: b, cHover: null, cPin: null, cZoom: null })}
              />
            ) : (
              <MonthWindowPicker
                label="MONTH"
                from={s.cMonth}
                options={monthOpts}
                onChange={(a) => set({ cMonth: a, cHover: null, cPin: null, cZoom: null })}
              />
            )}
          </div>
        )}

        <div style={{ display: 'flex', alignItems: 'flex-end', gap: '38px', marginTop: '30px', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '5px' }}>
            <div style={{ font: "400 11px/1 'JetBrains Mono',monospace", color: 'var(--fg3)' }}>
              {cs.label(ci)}
              {s.cPin != null ? ' · 已钉住' : coarse && s.cHover != null ? ' · 临时查看' : ''}
              {PC && <span style={{ color: 'var(--fg3)' }}>{pcNote(cs.meters[ci] ?? 0)}</span>}
            </div>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: '6px', flexWrap: 'wrap' }}>
              <span
                style={{
                  fontSize: '38px',
                  fontWeight: 600,
                  letterSpacing: '-.04em',
                  lineHeight: 1,
                  fontVariantNumeric: 'tabular-nums',
                }}
              >
                {cpair(cVal(ci)).v}
              </span>
              <span style={{ fontSize: '13px', color: 'var(--fg2)' }}>
                {cpair(cVal(ci)).u}
                {cUnitTail}
              </span>

            </div>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '5px' }}>
            <div style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>{PC ? '区间户均合计' : '区间合计'}</div>
            <div style={{ fontSize: '17px', fontWeight: 500, fontVariantNumeric: 'tabular-nums' }}>
              {!PC && !RMB && sci.unit === 'MWh' && cSumV >= 1000
                ? (cSumV / 1000).toFixed(2) + ' GWh'
                : cstr(cSumV)}
            </div>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '5px' }}>
            <div style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>峰值</div>
            <div style={{ fontSize: '17px', fontWeight: 500, fontVariantNumeric: 'tabular-nums' }}>
              {cstr(Math.max(...cVals))}
            </div>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '5px' }}>
            {/* 环比标签写明对比哪两格。 */}
            <div style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>
              环比
              {mom && (
                <span style={{ font: "400 10.5px/1 'JetBrains Mono',monospace" }}>
                  {' · ' + mom.from + '→' + mom.to}
                </span>
              )}
            </div>
            <div
              style={{
                fontSize: '17px',
                fontWeight: 500,
                fontVariantNumeric: 'tabular-nums',
                color: mom == null ? 'var(--fg3)' : mom.pct > 0 ? RED : OK,
              }}
              title={
                mom == null
                  ? '需要相邻两格均有已完结数据。当前游标位置不满足。'
                  : mom.to + ' 相对 ' + mom.from + (PC ? '（户均口径）' : '（总量口径）')
              }
            >
              {mom == null ? '—' : (mom.pct > 0 ? '+' : '') + mom.pct.toFixed(1) + '%'}
            </div>
          </div>
        </div>

        <div ref={mainPlotRef} style={{ display: 'flex', gap: '16px', marginTop: '30px' }}>
          <div
            style={{
              width: '48px',
              flex: 'none',
              display: 'flex',
              flexDirection: 'column',
              justifyContent: 'space-between',
              font: "400 10.5px/1 'JetBrains Mono',monospace",
              color: 'var(--fg3)',
              textAlign: 'right',
              height: '268px',
            }}
          >
            <span>{cpair(cmax).v}</span>
            <span>{cpair(cmax / 2).v}</span>
            <span>0</span>
          </div>
          <div style={{ flex: 1, minWidth: 0 }}>
            {mainLoading && !cs.vals.some((v) => v) ? (
              <ChartSkeleton height={268} bars={Math.min(Math.max(cs.n, 8), 31)} />
            ) : (
            <div
              key={
                s.range +
                '-' +
                s.bldgScope +
                '-' +
                s.floorScope +
                '-' +
                s.unit +
                '-' +
                (useYearHeat ? winFrom + ':' + winTo : s.cMonth) +
                '-' +
                (zoom ? zoom.join(':') : 'all')
              }
              className="chart-anim"
              onMouseLeave={() => {
                if (!coarse && !mobileChart) set({ cHover: null })
              }}
              /* 年视图里双击 = 钻进那个月的日视图；其它周期沿用「双击复位」。
                 年视图要复位缩放有右上角的「重置缩放 ✕」，不缺入口。 */
              onDoubleClick={(e) => (useBills ? drillToMonth(indexFromEvent(e, cs.n)) : resetChartSel())}
              {...brush.props}
              style={{
                width: '100%',
                position: 'relative',
                height: '268px',
                borderBottom: '1px solid var(--line)',
                touchAction: 'pan-y',
                userSelect: 'none',
              }}
            >
              <div style={{ position: 'absolute', left: 0, right: 0, top: 0, borderTop: '1px dashed var(--line2)' }} />
              <div style={{ position: 'absolute', left: 0, right: 0, top: '50%', borderTop: '1px dashed var(--line2)' }} />
              <div style={{ position: 'absolute', inset: 0, display: 'flex', alignItems: 'flex-end', gap: cGap }}>
                {cBars.map((b, i) => (
                  <div
                    key={i}
                    onMouseEnter={b.onEnter}
                    style={{
                      position: 'relative',
                      flex: 1,
                      height: '100%',
                      display: 'flex',
                      alignItems: 'flex-end',
                      cursor: 'pointer',
                      minWidth: 0,
                    }}
                  >
                    {b.showLabel && (
                      <span
                        aria-hidden="true"
                        style={{
                          position: 'absolute',
                          left: '50%',
                          bottom: `calc(${b.h} + 7px)`,
                          transform: 'translateX(-50%)',
                          zIndex: 3,
                          padding: '1px 2px',
                          background: 'color-mix(in srgb, var(--bg) 90%, transparent)',
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
                        transition: 'height .35s cubic-bezier(.22,.61,.36,1), background .18s',
                        height: b.h,
                        background: b.bg,
                        outline: b.pinned
                          ? '2px solid var(--red)'
                          : b.peeked
                            ? '1px dashed var(--red)'
                            : b.provisional
                              ? '1px dashed var(--red)'
                              : 'none',
                        outlineOffset: '1px',
                      }}
                    />
                  </div>
                ))}
              </div>
              <div
                style={{
                  position: 'absolute',
                  top: 0,
                  bottom: 0,
                  width: '1px',
                  background: 'var(--red)',
                  opacity: s.cPin != null ? 0.7 : s.cHover != null ? 0.45 : 0.35,
                  pointerEvents: 'none',
                  transition: 'left .18s ease',
                  left: cCursorX,
                }}
              />
              {brush.sel && (
                <div
                  style={{
                    position: 'absolute',
                    top: 0,
                    bottom: 0,
                    pointerEvents: 'none',
                    background: 'var(--redsoft)',
                    borderLeft: '1px solid var(--red)',
                    borderRight: '1px solid var(--red)',
                    left: (Math.min(brush.sel[0], brush.sel[1]) / cs.n) * 100 + '%',
                    width: ((Math.abs(brush.sel[1] - brush.sel[0]) + 1) / cs.n) * 100 + '%',
                  }}
                />
              )}
            </div>
            )}
            {/* 年账单：逐月刻度；其它周期仍三点刻度 */}
            {useBills && !zoom ? (
              <div
                style={{
                  display: 'flex',
                  width: '100%',
                  marginTop: '11px',
                  gap: cGap,
                  font: "400 10px/1 'JetBrains Mono',monospace",
                  color: 'var(--fg3)',
                }}
              >
                {Array.from({ length: cs.n }, (_, i) => {
                  const pinned = s.cPin === i
                  const active = i === ci
                  return (
                    <span
                      key={i}
                      title={
                        useBills
                          ? coarse
                            ? cs.axisAt(i) + ' · 点按查看，再点同一月钉住'
                            : cs.axisAt(i) + ' · 单击钉住读数，双击进入该月日视图'
                          : undefined
                      }
                      onMouseEnter={() => {
                        if (!coarse) set({ cHover: i })
                      }}
                      onClick={() => {
                        if (coarse) {
                          const now = Date.now()
                          const prev = barTapRef.current
                          const dbl = prev.i === i && now - prev.t < 380
                          barTapRef.current = { i, t: now }
                          if (dbl) set((p) => ({ cPin: p.cPin === i ? null : i, cHover: i }))
                          else set({ cHover: i, cPin: null })
                        } else {
                          set({ cPin: s.cPin === i ? null : i, cHover: i })
                        }
                      }}
                      onDoubleClick={() => useBills && !coarse && drillToMonth(i)}
                      style={{
                        flex: 1,
                        textAlign: 'center',
                        color: active ? 'var(--red)' : 'var(--fg3)',
                        fontWeight: pinned || active ? 600 : 400,
                        whiteSpace: 'nowrap',
                        overflow: 'hidden',
                        textOverflow: 'clip',
                        cursor: 'pointer',
                        transition: 'color .18s',
                      }}
                    >
                      {cs.axisAt(i)}
                    </span>
                  )
                })}
              </div>
            ) : (
              <div
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  marginTop: '11px',
                  font: "400 10.5px/1 'JetBrains Mono',monospace",
                  color: 'var(--fg3)',
                }}
              >
                <span>{cs.ticks[0]}</span>
                <span>{cs.ticks[1]}</span>
                <span>{cs.ticks[2]}</span>
              </div>
            )}
          </div>
        </div>
        {IS_LIVE && (() => {
          // 年视图正常有数据时不堆说明；只在缺数 / 日视图时出状态行
          if (useBills && !emptyWindow && !liveDayNA) return null
          const text = liveDayNA
            ? '上游每日约两个抄表窗口，无法提供小时负荷（upstream_hourly_data_unavailable）'
            : emptyWindow
              ? `账期 ${monthLabel(winFrom)} ~ ${monthLabel(winTo)} 内没有月账单。请更换区间，或等待账单任务补齐。`
              : monthView
                ? `${monthLabel(s.cMonth)} 逐日 · ` +
                  availNote(lcs.data?.availability, lcs.data?.quality, lcs.error)
                : availNote(lcs.data?.availability, lcs.data?.quality, lcs.error)
          return text ? <LiveNote text={text} /> : null
        })()}
      </section>

      <section
        data-r="stats-split"
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(4,1fr)',
          padding: coarse ? '20px 0' : '40px 0 44px',
          borderBottom: '1px solid var(--line)',
          width: '100%',
          boxSizing: 'border-box',
          overflow: 'hidden',
        }}
      >
        {campusStats.map((st, i) => {
          const isLongVal = typeof st.v === 'string' && st.v.length > 5
          return (
            <div
              key={st.en}
              style={{
                display: 'flex',
                flexDirection: 'column',
                gap: coarse ? '6px' : '13px',
                padding: coarse ? '0 4px' : '0 26px',
                minWidth: 0,
                overflow: 'hidden',
                // 第一列不画左线：分隔线只在列与列之间
                borderLeft: `1px solid ${i === 0 ? 'transparent' : 'var(--line)'}`,
              }}
            >
              <div
                style={{
                  font: "500 9px/1 'JetBrains Mono',monospace",
                  letterSpacing: coarse ? '0.02em' : '.18em',
                  color: 'var(--fg3)',
                  whiteSpace: 'nowrap',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                }}
              >
                {st.en}
              </div>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: coarse ? '2px' : '6px', flexWrap: 'wrap' }}>
                <span
                  style={{
                    fontSize: coarse ? (isLongVal ? '12.5px' : '16px') : '30px',
                    fontWeight: 600,
                    letterSpacing: '-.04em',
                    lineHeight: 1,
                    fontVariantNumeric: 'tabular-nums',
                    whiteSpace: 'nowrap',
                    color: (st as { color?: string }).color ?? 'var(--fg)',
                  }}
                >
                  {st.v}
                </span>
                {st.u && (
                  <span style={{ fontSize: coarse ? '9.5px' : '12px', color: 'var(--fg2)', whiteSpace: 'nowrap' }}>
                    {st.u}
                  </span>
                )}
              </div>
              {/* 一行短句就够；完整口径挂 title，鼠标停一下就有 */}
              <div
                title={st.title || undefined}
                style={{
                  fontSize: coarse ? '9.5px' : '12.5px',
                  color: 'var(--fg3)',
                  whiteSpace: 'nowrap',
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  cursor: st.title ? 'help' : undefined,
                }}
              >
                {st.note}
              </div>
            </div>
          )
        })}
      </section>

      {/* 用电构成。占满整行。专管占比、总量、户均与较均值偏离。 */}
      <section style={{ padding: '44px 0 40px', borderBottom: '1px solid var(--line)' }}>
        <div data-r="hdr" style={{ display: 'flex', alignItems: 'flex-end', gap: '20px', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
            <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
              CONSUMPTION MIX · {mixLabel === '楼层' ? 'FLOOR' : 'BUILDING'}
            </div>
            <div style={{ fontSize: '26px', fontWeight: 600, letterSpacing: '-.035em' }}>
              用电构成 · 按{mixLabel}
            </div>
          </div>
          <span data-r="hdrside" style={{ marginLeft: 'auto', fontSize: '12px', color: 'var(--fg3)' }}>
            {winLabel} · {mixRows.length} {mixLabel === '楼层' ? '层' : '栋'} · 合计{' '}
            {mixTotalPair.v} {mixTotalUnit}
          </span>
        </div>
        {mixSegs.length === 0 ? (
          <Unavailable
            title="当前范围没有可拆分的用量"
            desc={
              IS_LIVE
                ? '所选周期内没有有效数据。请延长周期，或等待账单与扫描就绪。'
                : '示例数据不足。'
            }
          />
        ) : (
          <>
            <div style={{ display: 'flex', height: '44px', width: '100%', marginTop: '24px', gap: '1px' }}>
              {mixSegs.map((m) => (
                <div
                  key={m.name}
                  title={m.name + ' · ' + m.pct}
                  style={{ height: '100%', background: m.bg, width: m.w, minWidth: '1px' }}
                />
              ))}
            </div>
            {/* 九列在手机上排不下：横向滚动 + 左侧楼栋身份列 sticky，
                缩小 SHARE 条，避免扫到右侧时不知道是哪一栋。 */}
            <div data-r="scroll" className="mix-scroll">
            <div className="mix-table" style={{ minWidth: '760px' }}>
            {/* 表头：全宽了才排得下这几列，别再让人靠切口径开关来回比 */}
            <div
              className="mix-row"
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '12px',
                padding: '18px 0 9px',
                borderBottom: '1px solid var(--line)',
                font: "500 9px/1 'JetBrains Mono',monospace",
                letterSpacing: '.16em',
                color: 'var(--fg3)',
              }}
            >
              <span className="mix-sticky" style={{ display: 'flex', alignItems: 'center', gap: '12px', flex: 'none' }}>
                <span style={{ width: '22px', flex: 'none' }}>#</span>
                <span style={{ width: '9px', flex: 'none' }} />
                <span style={{ width: '88px', flex: 'none' }}>{mixLabel === '楼层' ? 'FLOOR' : 'BUILDING'}</span>
              </span>
              <span style={{ width: '48px', flex: 'none', textAlign: 'right' }} title="窗口内日均非空房间数">
                ROOMS
              </span>
              <span className="mix-share" style={{ flex: 1, minWidth: 0 }}>
                SHARE
              </span>
              <span style={{ width: '48px', textAlign: 'right' }}>%</span>
              <span style={{ width: '80px', textAlign: 'right' }}>{mixTotalUnit}</span>
              <span style={{ width: '80px', textAlign: 'right' }}>{mixPerUnit}</span>
              <span style={{ width: '64px', textAlign: 'right' }} title="该行户均较本范围户均的偏离">
                VS AVG
              </span>
            </div>
            <div style={{ display: 'flex', flexDirection: 'column' }}>
              {mixSegs.map((m) => (
                <div
                  key={m.name}
                  className="mix-row"
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '12px',
                    padding: '10px 0',
                    borderBottom: '1px solid var(--line2)',
                  }}
                >
                  <span
                    className="mix-sticky"
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: '12px',
                      flex: 'none',
                      background: 'var(--bg)',
                    }}
                  >
                    <span
                      style={{
                        width: '22px',
                        flex: 'none',
                        font: "500 10.5px/1 'JetBrains Mono',monospace",
                        color: m.rank === 1 ? 'var(--red)' : 'var(--fg3)',
                      }}
                    >
                      {String(m.rank).padStart(2, '0')}
                    </span>
                    <span style={{ width: '9px', height: '9px', flex: 'none', background: m.dot }} />
                    <span
                      style={{
                        width: '88px',
                        flex: 'none',
                        fontSize: '13px',
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {m.name}
                    </span>
                  </span>
                  <span
                    style={{
                      width: '48px',
                      flex: 'none',
                      textAlign: 'right',
                      font: "400 11.5px/1 'JetBrains Mono',monospace",
                      color: 'var(--fg3)',
                      fontVariantNumeric: 'tabular-nums',
                    }}
                  >
                    {m.meters}
                  </span>
                  {/* 行内占比条：桌面 flex 吃满；手机 CSS 收窄，把空间留给数值列 */}
                  <span
                    className="mix-share"
                    style={{ flex: 1, minWidth: 0, height: '10px', background: 'var(--line2)' }}
                  >
                    <span style={{ display: 'block', height: '100%', width: m.w, background: m.bg }} />
                  </span>
                  <span
                    style={{
                      width: '48px',
                      textAlign: 'right',
                      font: "500 12.5px/1 'JetBrains Mono',monospace",
                      fontVariantNumeric: 'tabular-nums',
                    }}
                  >
                    {m.pct}
                  </span>
                  <span
                    style={{
                      width: '80px',
                      textAlign: 'right',
                      font: "400 12px/1 'JetBrains Mono',monospace",
                      color: 'var(--fg2)',
                      fontVariantNumeric: 'tabular-nums',
                    }}
                  >
                    {m.total}
                  </span>
                  <span
                    style={{
                      width: '80px',
                      textAlign: 'right',
                      font: "400 12px/1 'JetBrains Mono',monospace",
                      color: 'var(--fg2)',
                      fontVariantNumeric: 'tabular-nums',
                    }}
                  >
                    {m.per}
                  </span>
                  <span
                    style={{
                      width: '64px',
                      textAlign: 'right',
                      font: "500 11.5px/1 'JetBrains Mono',monospace",
                      fontVariantNumeric: 'tabular-nums',
                      color: m.dev == null ? 'var(--fg3)' : m.dev > 0 ? RED : OK,
                    }}
                  >
                    {m.dev == null ? '—' : (m.dev > 0 ? '+' : '') + m.dev.toFixed(0) + '%'}
                  </span>
                </div>
              ))}
            </div>
            </div>
            </div>
            <div style={{ fontSize: '12px', color: 'var(--fg3)', marginTop: '14px', textWrap: 'pretty' }}>
              占比按统计值总量计算
            </div>
          </>
        )}
      </section>

      {/* 楼栋对比与楼层对比。从构成分出的排名视角，两块并排。
          刻意与页面顶部的范围选择解耦：缩到某栋之后，仍然要能看见全校楼栋的排名。 */}
      <section
        data-r="split"
        style={{
          display: 'grid',
          gridTemplateColumns: '1fr 1fr',
          padding: '44px 0 40px',
          borderBottom: '1px solid var(--line)',
        }}
      >
        <div style={{ display: 'flex', flexDirection: 'column', paddingRight: '44px' }}>
          <PanelHead
            en="BUILDING RANK"
            title="楼栋对比"
            note={
              winLabel +
              (PC ? ' · 按户均降序' : ' · 按合计降序') +
              (cmpNoCurMonth && needBldgFetch ? ' · 不含本月' : '')
            }
          />
          {cmpLoading && bldgCmpRows.length === 0 ? (
            <InlineNote style={{ marginTop: '28px' }} />
          ) : (
            <RankBars
              dark={dark}
              empty="账单或扫描数据就绪后可查看。"
              rows={bldgCmpRows.map((r) => {
                const pair = PC
                  ? { v: r.meters > 0 ? kpair(r.per).v : '—', u: kpair(r.per).u + '/户' }
                  : r.kwh >= 1000
                    ? mpair(r.kwh / 1000)
                    : kpair(r.kwh)
                return {
                  name: r.name,
                  v: PC ? r.per : r.kwh,
                  label: pair.v + ' ' + pair.u,
                  hint: r.name + ' · 日均 ' + r.meters + ' 间非空房 · 合计 ' + kpair(r.kwh).v + ' ' + kpair(r.kwh).u,
                }
              })}
            />
          )}
        </div>
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            paddingLeft: '44px',
            borderLeft: '1px solid var(--line)',
          }}
        >
          <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
            <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
              FLOOR RANK
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: '12px', flexWrap: 'wrap' }}>
              <span style={{ fontSize: '26px', fontWeight: 600, letterSpacing: '-.035em' }}>楼层对比</span>
              {/* 这一块自己挑楼栋：全校视角下也得能翻到任意一栋的楼层分布 */}
              <select
                value={cmpBldg}
                onChange={(e) => set({ cCmpBldg: e.target.value })}
                style={{
                  padding: '6px 10px',
                  border: '1px solid var(--line)',
                  background: 'var(--bg)',
                  color: 'var(--fg)',
                  font: "400 12.5px/1 'Instrument Sans','Noto Sans SC',sans-serif",
                  cursor: 'pointer',
                  maxWidth: '160px',
                }}
              >
                {bldgNames.map((n) => (
                  <option key={n} value={n}>
                    {n}
                  </option>
                ))}
              </select>
            </div>
            <div
              style={{
                fontSize: '12px',
                lineHeight: 1.5,
                color: 'var(--fg3)',
                minHeight: '36px',
                textWrap: 'pretty',
              }}
            >
              {winLabel +
                (PC ? ' · 按户均降序' : ' · 按合计降序') +
                (cmpNoCurMonth && needFloorFetch ? ' · 不含本月' : '')}
            </div>
          </div>
          {cmpLoading && floorCmpRows.length === 0 ? (
            <InlineNote style={{ marginTop: '28px' }} />
          ) : (
            <RankBars
              dark={dark}
              empty={cmpBldg ? cmpBldg + ' 在该账期内没有可拆到楼层的数据。' : '请先选择一栋楼。'}
              rows={floorCmpRows.map((r) => {
                const pair = PC
                  ? { v: r.meters > 0 ? kpair(r.per).v : '—', u: kpair(r.per).u + '/户' }
                  : r.kwh >= 1000
                    ? mpair(r.kwh / 1000)
                    : kpair(r.kwh)
                return {
                  name: r.name,
                  v: PC ? r.per : r.kwh,
                  label: pair.v + ' ' + pair.u,
                  hint: cmpBldg + ' ' + r.name + ' · 日均 ' + r.meters + ' 间非空房',
                }
              })}
            />
          )}
        </div>
      </section>

      {/* 走势对比（多序列折线）：热力图的同一份矩阵，换成折线读法 */}
      <section
        data-r="heatwrap"
        style={{
          display: 'flex',
          padding: '44px 0 40px',
          borderBottom: '1px solid var(--line)',
        }}
      >
        <div style={{ flex: 1, minWidth: 0 }}>
          <div data-r="hdr" style={{ display: 'flex', alignItems: 'flex-end', gap: '20px', flexWrap: 'wrap' }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
              <div
                style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}
              >
                {mixLabel === '楼层' ? 'FLOOR' : 'BUILDING'} TRENDS · OVER TIME
              </div>
              <div style={{ fontSize: '26px', fontWeight: 600, letterSpacing: '-.035em' }}>
                走势对比 · 按{mixLabel}
              </div>
            </div>
            {/* 聚焦读数。固定高度：换聚焦、换游标都不许把下面的图顶上顶下 */}
            {trendOn && trendFocus && trendDetails && (
              <div
                data-r="hdrside"
                style={{
                  marginLeft: 'auto',
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: 'flex-end',
                  gap: '6px',
                  minHeight: '40px',
                }}
              >
                <div
                  style={{
                    font: "400 11px/1 'JetBrains Mono',monospace",
                    color: 'var(--fg3)',
                    display: 'flex',
                    alignItems: 'center',
                    gap: '8px',
                    flexWrap: 'wrap',
                    justifyContent: 'flex-end',
                  }}
                >
                  <span>
                    {trendDetails.head}
                    {trendColPin != null || trendPinned != null ? ' · 已定住' : ''}
                  </span>
                  {(trendColPin != null || trendPinned != null) && (
                    <button
                      type="button"
                      className="hv-line-fg"
                      onClick={() => set({ cTrendColPin: null, cTrendPin: null })}
                      style={{
                        background: 'none',
                        border: '1px solid var(--line)',
                        borderRadius: '999px',
                        padding: '3px 10px',
                        font: 'inherit',
                        fontSize: '11px',
                        color: 'var(--fg2)',
                        cursor: 'pointer',
                        lineHeight: 1.2,
                      }}
                    >
                      取消定住
                    </button>
                  )}
                </div>
                <div
                  style={{
                    display: 'flex',
                    alignItems: 'baseline',
                    gap: '8px',
                    flexWrap: 'wrap',
                    justifyContent: 'flex-end',
                  }}
                >
                  <span
                    style={{
                      fontSize: '24px',
                      fontWeight: 600,
                      letterSpacing: '-.035em',
                      lineHeight: 1,
                      color: 'var(--red)',
                      fontVariantNumeric: 'tabular-nums',
                    }}
                  >
                    {trendDetails.v}
                  </span>
                  <span style={{ fontSize: '12px', color: 'var(--fg2)' }}>{trendDetails.u}</span>

                  {trendDetails.delta && (
                    <span style={{ font: "500 12px/1 'JetBrains Mono',monospace", color: trendDetails.deltaColor }}>
                      {trendDetails.delta}
                    </span>
                  )}

                  <span style={{ fontSize: '11.5px', color: 'var(--fg3)', font: "400 11.5px/1 'JetBrains Mono',monospace" }}>
                    {trendDetails.rank}
                  </span>
                </div>
                <div style={{ font: "400 11px/1 'JetBrains Mono',monospace", color: 'var(--fg3)', textAlign: 'right' }}>
                  {trendDetails.alt}
                </div>
              </div>
            )}
          </div>

          {!trendOn ? (
            <Unavailable
              title="当前范围无法绘制走势"
              desc={
                trendN < 2
                  ? '所选窗口只有一格时间，无法连线。请延长账期后再查看。'
                  : IS_LIVE
                    ? '所选周期内没有可拆分的用量。请延长周期，或等待账单与扫描就绪。'
                    : '示例数据不足。'
              }
            />
          ) : (
            <div style={{ display: 'flex', gap: '16px', marginTop: '26px' }}>
              {/* y 轴五档。便于对着线估读数。 */}
              <div
                style={{
                  width: '58px',
                  flex: 'none',
                  display: 'flex',
                  flexDirection: 'column',
                  justifyContent: 'space-between',
                  font: "400 10.5px/1 'JetBrains Mono',monospace",
                  color: 'var(--fg3)',
                  textAlign: 'right',
                  height: TREND_H + 'px',
                }}
              >
                {[1, 0.75, 0.5, 0.25, 0].map((f) => (
                  <span key={f} style={{ fontVariantNumeric: 'tabular-nums' }}>
                    {f === 0 ? '0' : trendVal(trendMax * f).v}
                  </span>
                ))}
              </div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div
                  ref={trendPlotRef}
                  key={
                    s.range +
                    '-' +
                    s.bldgScope +
                    '-' +
                    s.floorScope +
                    '-' +
                    s.unit +
                    '-' +
                    (PC ? 'per' : 'sum') +
                    '-' +
                    (useYearHeat ? winFrom + ':' + winTo : s.cMonth)
                  }
                  className="chart-anim"
                  onClick={(e) => {
                    if (!trendPlotRef.current) return
                    const r = trendPlotRef.current.getBoundingClientRect()
                    if (trendN <= 1 || r.width <= 0) return
                    const x = Math.min(Math.max(e.clientX - r.left, 0), r.width)
                    const i = Math.min(trendN - 1, Math.max(0, Math.round((x / r.width) * (trendN - 1))))
                    set((p) => ({
                      cTrendColPin: p.cTrendColPin === i ? null : i,
                    }))
                  }}
                  onPointerDown={(e) => {
                    if (e.pointerType === 'mouse') return
                    trendTouchRef.current = { startX: e.clientX, scrubbed: false }
                  }}
                  onPointerMove={(e) => {
                    if (e.pointerType === 'mouse') {
                      if (!trendPlotRef.current) return
                      const r = trendPlotRef.current.getBoundingClientRect()
                      if (trendN <= 1 || r.width <= 0) return
                      const x = Math.min(Math.max(e.clientX - r.left, 0), r.width)
                      const i = Math.min(trendN - 1, Math.max(0, Math.round((x / r.width) * (trendN - 1))))
                      set({ cTrendCol: i })
                      return
                    }
                    const st = trendTouchRef.current
                    if (!st) return
                    if (Math.abs(e.clientX - st.startX) > 10) st.scrubbed = true
                    if (st.scrubbed && trendPlotRef.current) {
                      const r = trendPlotRef.current.getBoundingClientRect()
                      if (trendN > 1 && r.width > 0) {
                        const x = Math.min(Math.max(e.clientX - r.left, 0), r.width)
                        const i = Math.min(trendN - 1, Math.max(0, Math.round((x / r.width) * (trendN - 1))))
                        set({ cTrendCol: i })
                      }
                    }
                  }}
                  onPointerUp={(e) => {
                    if (e.pointerType !== 'mouse') {
                      const st = trendTouchRef.current
                      if (st && !st.scrubbed && trendPlotRef.current) {
                        const r = trendPlotRef.current.getBoundingClientRect()
                        if (trendN > 1 && r.width > 0) {
                          const x = Math.min(Math.max(e.clientX - r.left, 0), r.width)
                          const i = Math.min(trendN - 1, Math.max(0, Math.round((x / r.width) * (trendN - 1))))
                          set((p) => ({
                            cTrendCol: i,
                            cTrendColPin: p.cTrendColPin === i ? null : i,
                          }))
                        }
                      }
                    }
                    trendTouchRef.current = null
                  }}
                  onPointerCancel={() => {
                    trendTouchRef.current = null
                  }}
                  onPointerLeave={(e) => {
                    if (e.pointerType === 'mouse') set({ cTrendCol: null })
                  }}
                  style={{
                    position: 'relative',
                    height: TREND_H + 'px',
                    borderBottom: '1px solid var(--line)',
                    // 触屏要横向扫时间，禁用浏览器默认手势；纵向滚页面靠图外区域
                    touchAction: coarse ? 'none' : 'pan-y',
                  }}
                >
                  {[0, 25, 50, 75].map((p) => (
                    <div
                      key={p}
                      style={{
                        position: 'absolute',
                        left: 0,
                        right: 0,
                        top: p + '%',
                        borderTop: '1px dashed var(--line2)',
                      }}
                    />
                  ))}
                  {/* 游标 1：钉住列虚线 (变虚，悬停不动) */}
                  {trendColPin != null && (
                    <div
                      style={{
                        position: 'absolute',
                        top: 0,
                        bottom: 0,
                        left: tx(trendColPin) + '%',
                        borderLeft: '1.5px dashed var(--red)',
                        opacity: 0.85,
                        pointerEvents: 'none',
                        zIndex: 2,
                      }}
                    />
                  )}

                  {/* 游标 2：第二根游走竖线 (实线，继续跟随鼠标/手指浏览) */}
                  {previewCol != null && previewCol !== trendColPin && (
                    <div
                      style={{
                        position: 'absolute',
                        top: 0,
                        bottom: 0,
                        width: '1px',
                        background: 'var(--red)',
                        opacity: 0.55,
                        pointerEvents: 'none',
                        left: tx(previewCol) + '%',
                        zIndex: 3,
                        transition: 'left .1s ease',
                      }}
                    />
                  )}
                  <svg
                    viewBox="0 0 100 100"
                    preserveAspectRatio="none"
                    style={{
                      position: 'absolute',
                      inset: 0,
                      width: '100%',
                      height: '100%',
                      overflow: 'visible',
                      pointerEvents: 'none',
                    }}
                  >
                    {/* 上下文：非聚焦序列用同色发灰细线。表示分布，不是身份。 */}
                    {trendRows.map((r) =>
                      r.key === trendFocus?.key ? null : (
                        <path
                          key={r.key}
                          d={trendPath(r.pts)}
                          fill="none"
                          stroke={`color-mix(in srgb, var(--fg) ${dark ? 26 : 20}%, transparent)`}
                          strokeWidth="1"
                          vectorEffect="non-scaling-stroke"
                          strokeLinejoin="round"
                          strokeLinecap="round"
                        />
                      ),
                    )}
                    {/* 聚焦：先描一圈底色把身下的灰线让开，再上红线 */}
                    {trendFocus && (
                      <>
                        <path
                          d={trendPath(trendFocus.pts)}
                          fill="none"
                          stroke="var(--bg)"
                          strokeWidth="5"
                          vectorEffect="non-scaling-stroke"
                          strokeLinejoin="round"
                          strokeLinecap="round"
                        />
                        <path
                          d={trendPath(trendFocus.pts)}
                          fill="none"
                          stroke="var(--red)"
                          strokeWidth="2"
                          vectorEffect="non-scaling-stroke"
                          strokeLinejoin="round"
                          strokeLinecap="round"
                        />
                      </>
                    )}
                    {/* 命中层：透明粗描边，鼠标不必对准 1px 的线；事件照样冒泡给容器更新游标 */}
                    {trendRows.map((r) => (
                      <path
                        key={'hit-' + r.key}
                        d={trendPath(r.pts)}
                        fill="none"
                        stroke="transparent"
                        strokeWidth="12"
                        vectorEffect="non-scaling-stroke"
                        style={{ pointerEvents: 'stroke', cursor: 'pointer' }}
                        onPointerEnter={(e) => {
                          if (e.pointerType === 'mouse') set({ cTrendKey: r.key })
                        }}
                        onClick={(e) => {
                          e.stopPropagation()
                          const i = colFromEvent(e, trendN)
                          set((p) => ({
                            cTrendKey: r.key,
                            cTrendCol: i,
                            cTrendColPin: p.cTrendColPin === i ? null : i,
                          }))
                        }}
                      >
                        <title>{r.key}</title>
                      </path>
                    ))}
                  </svg>
                  {/* 1. 钉住列节点 (空心红圈) */}
                  {trendColPin != null && trendFocus?.pts[trendColPin] != null && (
                    <div
                      style={{
                        position: 'absolute',
                        left: tx(trendColPin) + '%',
                        top: 100 - (trendFocus.pts[trendColPin]! / trendMax) * 100 + '%',
                        transform: 'translate(-50%,-50%)',
                        pointerEvents: 'none',
                        zIndex: 4,
                      }}
                    >
                      <div
                        style={{
                          width: '9px',
                          height: '9px',
                          borderRadius: '99px',
                          background: 'var(--bg)',
                          border: '2px solid var(--red)',
                          boxShadow: '0 0 0 2px var(--bg)',
                        }}
                      />
                    </div>
                  )}

                  {/* 2. 实时游走浏览节点 (实心红点) */}
                  {previewCol != null && trendFocus?.pts[previewCol] != null && (
                    <div
                      style={{
                        position: 'absolute',
                        left: tx(previewCol) + '%',
                        top: 100 - (trendFocus.pts[previewCol]! / trendMax) * 100 + '%',
                        transform: 'translate(-50%,-50%)',
                        pointerEvents: 'none',
                        zIndex: 5,
                        transition: 'left .1s ease, top .1s ease',
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
                  )}

                </div>
                {/* 刻度锚在格点上，首末两格贴边不出界 */}
                <div
                  style={{
                    position: 'relative',
                    height: '12px',
                    marginTop: '11px',
                    font: "400 10px/1 'JetBrains Mono',monospace",
                    color: 'var(--fg3)',
                  }}
                >
                  {hmBuckets.map((b, i) =>
                    trendShowLabel(i) ? (
                      <span
                        key={i}
                        onPointerEnter={() => set({ cTrendCol: i })}
                        style={{
                          position: 'absolute',
                          left: tx(i) + '%',
                          transform:
                            i === 0 ? 'none' : i === trendN - 1 ? 'translateX(-100%)' : 'translateX(-50%)',
                          whiteSpace: 'nowrap',
                          cursor: 'pointer',
                          color: i === trendCol ? 'var(--red)' : undefined,
                          fontWeight: i === trendCol ? 600 : 400,
                          transition: 'color .18s',
                        }}
                      >
                        {hmTick(b, i)}
                      </span>
                    ) : null,
                  )}
                </div>
              </div>
            </div>
          )}
        </div>

        {/* 图例即读数表：右列是游标那一列的值，随游标走。
            颜色不分身份，所以这张表就是「谁是谁」的唯一出处，不能省。 */}
        {trendOn && (
          <div
            data-r="heatside"
            style={{
              width: '272px',
              flex: 'none',
              marginLeft: '32px',
              paddingLeft: '32px',
              borderLeft: '1px solid var(--line)',
              display: 'flex',
              flexDirection: 'column',
            }}
          >
            <div
              style={{ font: "500 9px/1 'JetBrains Mono',monospace", letterSpacing: '.18em', color: 'var(--fg3)' }}
            >
              {tableColLabel} · {trendUnit}
              {trendColPin != null ? ' · 已固定' : ''}
            </div>
            <div style={{ fontSize: '12px', color: 'var(--fg3)', lineHeight: 1.6, marginTop: '12px', textWrap: 'pretty' }}>
              {'该范围全部 ' +
                trendAll.length +
                ' ' +
                trendUnitWord +
                (coarse
                  ? '都在图上；点击图上任意时间点可钉住，移开滑动可继续预览第二根游走线。'
                  : '都在图上；点击时间点可钉住读数，指针滑动可继续游走浏览。')}
            </div>
            {/* 全量列表自滚动。禁止截断，避免楼栋查不到。 */}
            <div
              style={{
                display: 'flex',
                flexDirection: 'column',
                marginTop: '18px',
                maxHeight: TREND_H + 'px',
                overflowY: 'auto',
                paddingRight: '4px',
              }}
            >
              {trendRows.map((r) => {
                const on = trendFocus?.key === r.key
                const v = r.pts[tableCol]
                return (
                  <div
                    key={r.key}
                    onPointerEnter={(e) => {
                      if (e.pointerType === 'mouse') set({ cTrendKey: r.key })
                    }}
                    onPointerLeave={(e) => {
                      if (e.pointerType === 'mouse') set({ cTrendKey: null })
                    }}
                    onClick={() =>
                      set((p) => ({
                        cTrendPin: p.cTrendPin === r.key ? null : r.key,
                        cTrendKey: r.key,
                      }))
                    }
                    title={r.key + ' · 窗口日均 ' + r.meters + ' 间非空房 · 点击定住该曲线'}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: '10px',
                      padding: '9px 0',
                      borderBottom: '1px solid var(--line2)',
                      cursor: 'pointer',
                    }}
                  >
                    {/* 线型图例用线段，不用方块。 */}
                    <span
                      style={{
                        width: '15px',
                        flex: 'none',
                        height: on ? '2px' : '1px',
                        background: on
                          ? 'var(--red)'
                          : `color-mix(in srgb, var(--fg) ${dark ? 38 : 30}%, transparent)`,
                        transition: 'background .16s',
                      }}
                    />
                    <span
                      style={{
                        fontSize: '12.5px',
                        color: on ? 'var(--fg)' : 'var(--fg2)',
                        fontWeight: on ? 600 : 400,
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {r.key}
                    </span>
                    {trendPinned === r.key && (
                      <span style={{ font: "500 8.5px/1 'JetBrains Mono',monospace", letterSpacing: '.14em', color: 'var(--red)' }}>
                        PIN
                      </span>
                    )}
                    <span
                      style={{
                        marginLeft: 'auto',
                        font: "500 12px/1 'JetBrains Mono',monospace",
                        fontVariantNumeric: 'tabular-nums',
                        color: on ? 'var(--red)' : 'var(--fg2)',
                      }}
                    >
                      {v == null ? '—' : trendVal(v).v + ' ' + trendVal(v).u + (PC ? '/户' : '')}
                    </span>
                  </div>
                )
              })}
            </div>
            {/* 这一列的合计/加权户均，垫在列表底部当个总账 */}
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '10px',
                padding: '10px 0 0',
                marginTop: '4px',
                borderTop: '1px solid var(--line)',
              }}
            >
              <span style={{ fontSize: '12.5px', color: 'var(--fg3)' }}>
                合计
                <span style={{ fontSize: '11px' }}>{PC ? ' · 当期日均非空房数加权' : ' · 该列全部' + trendUnitWord}</span>
              </span>
              <span
                style={{
                  marginLeft: 'auto',
                  font: "500 12px/1 'JetBrains Mono',monospace",
                  fontVariantNumeric: 'tabular-nums',
                  color: 'var(--fg2)',
                }}
              >
                {(() => {
                  const v = trendColTotal(tableCol)
                  return v == null ? '—' : trendVal(v).v + ' ' + trendVal(v).u + (PC ? '/户' : '')
                })()}
              </span>
            </div>
          </div>
        )}
      </section>

      {/* 热力图置底 */}
      <section style={{ padding: '44px 0 40px' }}>
        <div data-r="hdr" style={{ display: 'flex', alignItems: 'flex-end', gap: '20px', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
            <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
              LOAD HEATMAP · {mixLabel === '楼层' ? 'FLOOR' : 'BUILDING'} × TIME
            </div>
            <div style={{ fontSize: '26px', fontWeight: 600, letterSpacing: '-.035em' }}>热力图 · {mixLabel}</div>
          </div>
          <span data-r="hdrside" style={{ marginLeft: 'auto', fontSize: '12px', color: 'var(--fg3)' }}>{hmNote}</span>
        </div>
        {lbd.loading && IS_LIVE ? (
          <InlineNote style={{ marginTop: '28px' }} text="热力图读取中" />
        ) : hmRows.length === 0 || hmBuckets.length === 0 ? (
          <Unavailable
            title="当前范围没有可绘制的格子"
            desc={
              useYearHeat
                ? '所选月份还没有月账单。请等待账单任务完成后再查看。'
                : '所选周期内没有有效抄表差值。请延长周期，或等待下一轮扫描完成。'
            }
          />
        ) : (
          <>
            {/* 不再套横向滚动容器：那个容器会把格子的浮窗一起裁掉，
                而列数最多 31，平分下来每格仍有十几像素，够看。 */}
            <div
              style={{ marginTop: '26px' }}
              onPointerLeave={(e) => {
                // 鼠标离开收起；触屏松手也会 leave，不能清，否则必须按住才看得见
                if (e.pointerType === 'mouse') set({ cHmCell: null })
              }}
            >
              {hmRows.map((row, ri) => (
                <div key={row.key} style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '3px' }}>
                  <span
                    title={row.key + ' · 窗口日均 ' + row.meters + ' 间非空房'}
                    style={{
                      width: 'var(--rowlabel)',
                      flex: 'none',
                      fontSize: '12px',
                      color: hmTip?.row === ri ? 'var(--fg)' : 'var(--fg2)',
                      fontWeight: hmTip?.row === ri ? 600 : 400,
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                      transition: 'color .14s',
                    }}
                  >
                    {row.key}
                  </span>
                  <div style={{ display: 'flex', gap: '2px', flex: 1, minWidth: 0 }}>
                    {row.values.map((v, i) => {
                      const c = hmCell(v, metersAt(row, i))
                      const on = hmTip?.row === ri && hmTip?.col === i
                      return (
                        <div
                          key={i}
                          onPointerEnter={(e) => {
                            if (e.pointerType === 'mouse') set({ cHmCell: [ri, i] })
                          }}
                          onClick={() => {
                            // 触屏：点选保留；再点同一格关闭。桌面仍靠悬停，不走 click。
                            if (!coarse) return
                            set((p) =>
                              p.cHmCell && p.cHmCell[0] === ri && p.cHmCell[1] === i
                                ? { cHmCell: null }
                                : { cHmCell: [ri, i] },
                            )
                          }}
                          style={{
                            position: 'relative',
                            flex: '1 1 0',
                            minWidth: 0,
                            height: hmCellH + 'px',
                            background: c.bg,
                            boxShadow: c.ring,
                            boxSizing: 'border-box',
                            outline: on ? '1.5px solid var(--fg)' : 'none',
                            outlineOffset: '-1px',
                            zIndex: on ? 12 : undefined,
                            // 细格上优先让页面纵滚；点一下仍可触发 click
                            touchAction: 'pan-y',
                            cursor: 'pointer',
                          }}
                        >
                          {/* 浮窗照「用电日历」那一套：细边框 + 本体底色 + 投影。
                              贴顶的行改挂到格子下方，贴边的列把自己推回画布内。 */}
                          {on && hmTip && (
                            <div
                              style={{
                                position: 'absolute',
                                left: 0,
                                [ri <= 1 ? 'top' : 'bottom']: 'calc(100% + 8px)',
                                transform:
                                  i <= (hmBuckets.length - 1) * 0.22
                                    ? 'translateX(-6px)'
                                    : i >= (hmBuckets.length - 1) * 0.78
                                      ? 'translateX(calc(-100% + 20px))'
                                      : 'translateX(-50%)',
                                zIndex: 16,
                                pointerEvents: 'none',
                                border: '1px solid var(--fg)',
                                background: 'var(--bg)',
                                padding: '11px 13px',
                                display: 'flex',
                                flexDirection: 'column',
                                gap: '7px',
                                minWidth: '158px',
                                boxShadow: '0 12px 32px rgba(0,0,0,.18)',
                              }}
                            >
                              <span
                                style={{ font: "400 10px/1 'JetBrains Mono',monospace", color: 'var(--fg3)', whiteSpace: 'nowrap' }}
                              >
                                {hmTip.head}
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
                                  {hmTip.v}
                                </span>
                                <span style={{ fontSize: '11.5px', color: 'var(--fg2)' }}>{hmTip.u}</span>
                              </span>
                              <span
                                style={{ font: "400 10.5px/1 'JetBrains Mono',monospace", color: 'var(--fg3)', whiteSpace: 'nowrap' }}
                              >
                                {hmTip.alt}
                              </span>
                              {hmTip.vsOwn && (
                                <span style={{ fontSize: '11px', fontWeight: 500, whiteSpace: 'nowrap', color: hmTip.vsOwnColor }}>
                                  {hmTip.vsOwn}
                                </span>
                              )}
                              {hmTip.share && (
                                <span style={{ fontSize: '11px', color: 'var(--fg3)', whiteSpace: 'nowrap' }}>
                                  {hmTip.share}
                                </span>
                              )}
                            </div>
                          )}
                        </div>
                      )
                    })}
                  </div>
                </div>
              ))}
              <div style={{ display: 'flex', alignItems: 'flex-start', gap: '10px', marginTop: '8px' }}>
                <span style={{ width: 'var(--rowlabel)', flex: 'none' }} />
                <div
                  ref={hmPlotRef}
                  style={{
                    display: 'flex',
                    gap: '2px',
                    flex: 1,
                    minWidth: 0,
                    font: "400 10px/1.2 'JetBrains Mono',monospace",
                    color: 'var(--fg3)',
                  }}
                >
                  {hmBuckets.map((b, i) => (
                    <span
                      key={i}
                      title={hmTick(b, i)}
                      style={{
                        flex: '1 1 0',
                        minWidth: 0,
                        textAlign: 'center',
                        whiteSpace: 'nowrap',
                        /* 标签允许溢出本格。否则 24px 会裁断 25/07。 */
                        overflow: 'visible',
                        writingMode: hmGran !== 'month' && hmBuckets.length > 16 ? 'vertical-rl' : 'horizontal-tb',
                        transform: hmGran !== 'month' && hmBuckets.length > 16 ? 'rotate(180deg)' : undefined,
                        height: hmGran !== 'month' && hmBuckets.length > 16 ? '52px' : undefined,
                        visibility: hmShowLabel(i) || hmTip?.col === i ? 'visible' : 'hidden',
                        color:
                          hmTip?.col === i
                            ? 'var(--red)'
                            : useYearHeat && i === hmBuckets.length - 1
                              ? 'var(--red)'
                              : undefined,
                        fontWeight:
                          hmTip?.col === i || (useYearHeat && i === hmBuckets.length - 1) ? 600 : undefined,
                      }}
                    >
                      {hmTick(b, i)}
                    </span>
                  ))}
                </div>
              </div>
            </div>
            {/* 色阶：可拖的映射区间 + 悬浮格子的游标点 */}
            <HeatScale
              max={hmMax}
              lo={hmLo}
              hi={hmHi}
              onChange={(a, b) => set({ cHmLo: a, cHmHi: b })}
              onReset={() => set({ cHmLo: 0, cHmHi: 1 })}
              hoverV={hmHoverV}
              outTone={hmOutTone}
              fmt={(v) =>
                PC
                  ? v.toFixed(v < 10 ? 2 : 1)
                  : v >= 1000
                    ? (v / 1000).toFixed(1) + 'k'
                    : v.toFixed(v < 10 ? 1 : 0)
              }
            />

            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '6px',
                marginTop: '14px',
                marginLeft: 'calc(var(--rowlabel) + 12px)',
                flexWrap: 'wrap',
                font: "400 10.5px/1.6 'JetBrains Mono',monospace",
                color: 'var(--fg3)',
              }}
            >
              <span style={{ width: '16px', height: '10px', background: hmOutTone }} />
              低于下限
              <span style={{ width: '16px', height: '10px', border: '1px solid var(--line2)' }} />
              无数据 · 峰值{' '}
              {PC
                ? hmMax.toFixed(2) + ' kWh/户'
                : hmMax >= 1000
                  ? (hmMax / 1000).toFixed(1) + ' MWh'
                  : hmMax.toFixed(1) + ' kWh'}
              {hmBuckets.length > 0 && (
                <span>
                  · {hmRows.length} 行 × {hmBuckets.length} 列 ·{' '}
                  {hmGran === 'month' ? '月' : hmGran === 'week' ? '周' : '日'}
                  {coarse ? ' · 点格子查看，再点关闭' : ''}
                  {useYearHeat ? ' · 账单口径' : ''}
                </span>
              )}
            </div>
          </>
        )}
      </section>
    </div>
  )
}
