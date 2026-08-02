/* 数据看板年视图的当月未结算保护。
   null 表示尚无数据，不是 0。禁止塞 0 柱。
   仅当拿到真实当月累计后才追加临时列。
   否则最后一列停在最近有账单的月份。 */

export interface CompleteBillMonth {
  month: string
  total_kwh: string
  meters: number
}

export interface CampusYearBar {
  month: string
  kwh: number
  provisional: boolean
  meters: number
}

export function buildCampusYearBars(
  complete: readonly CompleteBillMonth[],
  currentMonth: string,
  currentKwh: number | null,
  currentMeters: number,
): CampusYearBar[] {
  const bars = complete.map((bill) => ({
    month: bill.month,
    kwh: parseFloat(bill.total_kwh) || 0,
    provisional: false,
    meters: bill.meters || 0,
  }))
  if (currentKwh == null || !Number.isFinite(currentKwh)) return bars
  bars.push({ month: currentMonth, kwh: currentKwh, provisional: true, meters: currentMeters })
  return bars
}

export function hasBreakdownValues(rows: readonly { values: readonly (string | null)[] }[]): boolean {
  return rows.some((row) => row.values.some((raw) => raw != null && Number.isFinite(parseFloat(raw))))
}
