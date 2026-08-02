/* 月份键（YYYY-MM）工具。账期区间选择器与各视图共用。
   使用年×12+月整数算术。禁止 Date.setMonth 连续自减。
   后者在跨年或 31 号会跳月。 */

export type MonthKey = string // 'YYYY-MM'

const pad = (n: number) => String(n).padStart(2, '0')

/** YYYY-MM 转为自 0 年起的月序号。非法输入返回 NaN。 */
export function monthIndex(key: MonthKey): number {
  const hit = /^(\d{4})-(\d{2})$/.exec(key)
  if (!hit) return NaN
  return Number(hit[1]) * 12 + (Number(hit[2]) - 1)
}

export function fromMonthIndex(idx: number): MonthKey {
  return Math.floor(idx / 12) + '-' + pad((idx % 12) + 1)
}

export function currentMonth(now = new Date()): MonthKey {
  return now.getFullYear() + '-' + pad(now.getMonth() + 1)
}

export function addMonths(key: MonthKey, delta: number): MonthKey {
  const i = monthIndex(key)
  return Number.isFinite(i) ? fromMonthIndex(i + delta) : key
}

/** 含端点，升序。from 晚于 to 时返回空数组。 */
export function monthsBetween(from: MonthKey, to: MonthKey): MonthKey[] {
  const a = monthIndex(from)
  const b = monthIndex(to)
  if (!Number.isFinite(a) || !Number.isFinite(b) || a > b) return []
  const out: MonthKey[] = []
  for (let i = a; i <= b; i++) out.push(fromMonthIndex(i))
  return out
}

/** 含端点的月数。非法区间返回 0。 */
export function monthSpan(from: MonthKey, to: MonthKey): number {
  const a = monthIndex(from)
  const b = monthIndex(to)
  if (!Number.isFinite(a) || !Number.isFinite(b) || a > b) return 0
  return b - a + 1
}

/** 2026-07 → 2026/07 */
export function monthLabel(key: MonthKey): string {
  return key.replace('-', '/')
}

/** 2026-07 → 26/07。图表刻度用。 */
export function monthShort(key: MonthKey): string {
  return key.length >= 7 ? key.slice(2).replace('-', '/') : key
}

/*
下拉月份选项。
取最早有数据月到当月的连续区间，再兜底至少 minCount 个月。
必须连续：缺账单月仍可作端点。兜底保证空库时下拉非空。
*/
export function monthOptions(known: MonthKey[], minCount = 12, now = new Date()): MonthKey[] {
  const cur = currentMonth(now)
  const curIdx = monthIndex(cur)
  let earliest = curIdx - (minCount - 1)
  for (const k of known) {
    const i = monthIndex(k)
    if (Number.isFinite(i) && i < earliest) earliest = i
  }
  const out: MonthKey[] = []
  for (let i = earliest; i <= curIdx; i++) out.push(fromMonthIndex(i))
  return out
}

/** 把区间夹回合法范围。顺序颠倒时以刚改动的一端为准。 */
export function clampRange(
  from: MonthKey,
  to: MonthKey,
  changed: 'from' | 'to',
): [MonthKey, MonthKey] {
  if (monthIndex(from) <= monthIndex(to)) return [from, to]
  return changed === 'from' ? [from, from] : [to, to]
}
