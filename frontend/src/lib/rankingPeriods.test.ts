import { fallbackRankingPeriods, formatRankingMoment, formatRankingRange } from './rankingPeriods'

export function runRankingPeriodTests() {
  if (formatRankingRange({ from: '2026-07-29', to: '2026-07-29' }) !== '2026.07.29') {
    throw new Error('single-day ranking range is not compact')
  }
  if (formatRankingRange({ from: '2026-07-23', to: '2026-07-29' }) !== '2026.07.23 — 07.29') {
    throw new Error('same-year ranking range is incorrect')
  }
  const week = fallbackRankingPeriods('week', new Date(2026, 6, 29, 16))
  if (week.current.from !== '2026-07-20' || week.current.to !== '2026-07-26' || week.previous.to !== '2026-07-19') {
    throw new Error('weekly fallback is not the last completed Monday-Sunday period')
  }
  const month = fallbackRankingPeriods('month', new Date(2026, 6, 29, 16))
  if (month.current.from !== '2026-06-01' || month.current.to !== '2026-06-30') {
    throw new Error('monthly fallback is not the last completed calendar month')
  }
  if (!formatRankingMoment(week.nextUpdateAt).endsWith('09:00')) {
    throw new Error('ranking refresh time is not formatted at 09:00')
  }
  const custom = fallbackRankingPeriods('day', new Date(2026, 6, 29, 3, 29), '03:30')
  if (custom.current.to !== '2026-07-27' || !formatRankingMoment(custom.nextUpdateAt).endsWith('03:30')) {
    throw new Error('configured ranking refresh time is not applied')
  }
}
