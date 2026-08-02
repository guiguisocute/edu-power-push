/* 单位与数值格式化。移植自原型 renderVals。 */

import { RATE } from './mock'

export interface Pair {
  v: string
  u: string
}

export interface Fmt {
  RMB: boolean
  U: string
  rate: number
  cv: (v: number) => number
  nf: (x: number, d?: number) => string
  f1: (v: number) => string
  f2: (v: number) => string
  alt: (v: number) => string
  kpair: (v: number) => Pair
  mpair: (v: number) => Pair
}

export type ConsumptionDisplayUnit = 'kWh' | 'MWh' | '元' | '万元'

const safeRate = (rate: number) => (Number.isFinite(rate) && rate > 0 ? rate : RATE)
const formatNumber = (x: number, d?: number) =>
  x >= 1000 ? Math.round(x).toLocaleString() : x >= 100 ? x.toFixed(d == null ? 0 : d) : x.toFixed(2)

/**
 * 用同一表头单位格式化每一行。
 * 避免合计选万元后，小值行被 mpair 降回元而错位。
 */
export function formatKwhInUnit(kwh: number, unit: ConsumptionDisplayUnit, rate: number = RATE): Pair {
  const r = safeRate(rate)
  switch (unit) {
    case 'MWh':
      return { v: formatNumber(kwh / 1000, 1), u: unit }
    case '元':
      return { v: formatNumber(kwh * r), u: unit }
    case '万元':
      return { v: formatNumber((kwh * r) / 10000, 1), u: unit }
    default:
      return { v: formatNumber(kwh), u: unit }
  }
}

/** rate = 元/kWh。默认原型 RATE。live 应传入 features.display.electricityRate。 */
export function makeFmt(unit: 'kwh' | 'rmb', rate: number = RATE): Fmt {
  const r = safeRate(rate)
  const RMB = unit === 'rmb'
  const U = RMB ? '元' : 'kWh'
  const cv = (v: number) => (RMB ? v * r : v)
  const nf = formatNumber
  const f1 = (v: number) => cv(v).toFixed(1)
  const f2 = (v: number) => cv(v).toFixed(2)
  const alt = (v: number) => (RMB ? v.toFixed(2) + ' kWh' : (v * r).toFixed(2) + ' 元')
  const kpair = (v: number): Pair => (RMB ? { v: nf(v * r), u: '元' } : { v: nf(v), u: 'kWh' })
  const mpair = (v: number): Pair => {
    if (!RMB) return { v: nf(v, 1), u: 'MWh' }
    const y = v * 1000 * r
    return y >= 100000 ? { v: nf(y / 10000, 1), u: '万元' } : { v: nf(y), u: '元' }
  }
  return { RMB, U, rate: r, cv, nf, f1, f2, alt, kpair, mpair }
}

/* 主题强调色（原型 RED / OK） */
export function themeColors(dark: boolean) {
  return {
    RED: dark ? '#FF3B4E' : '#D6001C',
    OK: dark ? '#3ECF9A' : '#0F7A56',
    FG: 'var(--fg)',
    FG2: 'var(--fg2)',
    FG3: 'var(--fg3)',
  }
}

/* 分段按钮激活态样式三元组（原型 seg） */
export interface SegProps {
  label: string
  go: () => void
  w: string
  bg: string
  fg: string
}

export function seg(active: boolean, label: string, fn: () => void): SegProps {
  return {
    label,
    go: fn,
    w: active ? '600' : '400',
    bg: active ? 'var(--fg)' : 'transparent',
    fg: active ? 'var(--bg)' : 'var(--fg2)',
  }
}
