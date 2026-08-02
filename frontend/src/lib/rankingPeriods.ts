export interface RankingDateRange {
  from: string
  to: string
}

const parts = (date: string) => date.split('-').map(Number)

/** 把接口闭区间压成站内图表紧凑日期写法。 */
export function formatRankingRange(range: RankingDateRange): string {
  const [fy, fm, fd] = parts(range.from)
  const [ty, tm, td] = parts(range.to)
  if (!fy || !fm || !fd || !ty || !tm || !td) return '—'
  const start = `${fy}.${String(fm).padStart(2, '0')}.${String(fd).padStart(2, '0')}`
  if (range.from === range.to) return start
  const end = fy === ty
    ? `${String(tm).padStart(2, '0')}.${String(td).padStart(2, '0')}`
    : `${ty}.${String(tm).padStart(2, '0')}.${String(td).padStart(2, '0')}`
  return `${start} — ${end}`
}

export function formatRankingMoment(value: string): string {
  const d = new Date(value)
  if (!Number.isFinite(d.getTime())) return '—'
  const fields = Object.fromEntries(
    new Intl.DateTimeFormat('zh-CN', {
      timeZone: 'Asia/Shanghai', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
    }).formatToParts(d).map((p) => [p.type, p.value]),
  )
  return `${fields.month}.${fields.day} ${fields.hour}:${fields.minute}`
}

/** mock 与 live 共用可配置周期切换。避免演示口径与线上不一致。 */
export function fallbackRankingPeriods(
  period: 'day' | 'week' | 'month',
  anchor = new Date(),
  refreshTime = '09:00',
) {
  const dateOnly = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  const scheduled = (d: Date) => `${dateOnly(d)}T${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}:00+08:00`
  const day = (d: Date, offset: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + offset)
  const shift = (d: Date, offset: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + offset, d.getHours(), d.getMinutes())
  let updatedAt: Date
  let nextUpdateAt: Date
  let from: Date
  let toExclusive: Date
  let previousFrom: Date
  const match = /^(\d{2}):(\d{2})$/.exec(refreshTime)
  const parsedHour = match ? Number(match[1]) : 9
  const parsedMinute = match ? Number(match[2]) : 0
  const validTime = parsedHour >= 0 && parsedHour <= 23 && parsedMinute >= 0 && parsedMinute <= 59
  const refreshHour = validTime ? parsedHour : 9
  const refreshMinute = validTime ? parsedMinute : 0
  if (period === 'week') {
    const today = new Date(anchor.getFullYear(), anchor.getMonth(), anchor.getDate())
    const monday = day(today, -((today.getDay() + 6) % 7))
    updatedAt = new Date(monday.getFullYear(), monday.getMonth(), monday.getDate(), refreshHour, refreshMinute)
    if (anchor < updatedAt) updatedAt = shift(updatedAt, -7)
    nextUpdateAt = shift(updatedAt, 7)
    toExclusive = new Date(updatedAt.getFullYear(), updatedAt.getMonth(), updatedAt.getDate())
    from = day(toExclusive, -7)
    previousFrom = day(from, -7)
  } else if (period === 'month') {
    updatedAt = new Date(anchor.getFullYear(), anchor.getMonth(), 1, refreshHour, refreshMinute)
    if (anchor < updatedAt) updatedAt = new Date(anchor.getFullYear(), anchor.getMonth() - 1, 1, refreshHour, refreshMinute)
    nextUpdateAt = new Date(updatedAt.getFullYear(), updatedAt.getMonth() + 1, 1, refreshHour, refreshMinute)
    toExclusive = new Date(updatedAt.getFullYear(), updatedAt.getMonth(), 1)
    from = new Date(toExclusive.getFullYear(), toExclusive.getMonth() - 1, 1)
    previousFrom = new Date(from.getFullYear(), from.getMonth() - 1, 1)
  } else {
    updatedAt = new Date(anchor.getFullYear(), anchor.getMonth(), anchor.getDate(), refreshHour, refreshMinute)
    if (anchor < updatedAt) updatedAt = shift(updatedAt, -1)
    nextUpdateAt = shift(updatedAt, 1)
    toExclusive = new Date(updatedAt.getFullYear(), updatedAt.getMonth(), updatedAt.getDate())
    from = day(toExclusive, -1)
    previousFrom = day(from, -1)
  }
  return {
    current: { from: dateOnly(from), to: dateOnly(day(toExclusive, -1)) },
    previous: { from: dateOnly(previousFrom), to: dateOnly(day(from, -1)) },
    updatedAt: scheduled(updatedAt),
    nextUpdateAt: scheduled(nextUpdateAt),
  }
}
