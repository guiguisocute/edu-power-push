/* 学期（校历）。起止由运维面板 display.semesters 下发。
   纯函数：切周、取标签、日序列折周。
   禁止用后端 ISO 周：开学日不一定是周一，会混入邻周数据。
   一律按天取，再按开学日 + 7×k 切周。 */

export interface Semester {
  /** 开学年月 YYYY-MM。本记录主键。 */
  key: string
  /** 第 1 周第一天 YYYY-MM-DD */
  start: string
  /** 最后一天（含）YYYY-MM-DD */
  end: string
}

export interface SemesterWeek {
  /** 1 基 */
  index: number
  label: string
  /** [from, to) 本地时间 */
  from: Date
  to: Date
}

const DAY_MS = 86400000

/** YYYY-MM-DD 转本地零点。用 Date(y,m,d)。
    Date.parse 把纯日期当 UTC，东八区会前移一天。 */
export function parseDay(day: string): Date | null {
  const hit = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day.trim())
  if (!hit) return null
  const d = new Date(Number(hit[1]), Number(hit[2]) - 1, Number(hit[3]))
  return Number.isFinite(d.getTime()) ? d : null
}

/** 2026-03 → 2026-03学期 */
export function semesterLabel(key: string): string {
  return key + '学期'
}

export function weekLabel(index: number): string {
  return '第 ' + index + ' 周'
}

/** 配置里挑出合法的几条并按时间排序；非法记录直接丢掉，不猜 */
export function normalizeSemesters(list: Semester[] | undefined | null): Semester[] {
  if (!Array.isArray(list)) return []
  return list
    .filter((x) => x && /^\d{4}-\d{2}$/.test(x.key) && parseDay(x.start) && parseDay(x.end))
    .filter((x) => (parseDay(x.end) as Date).getTime() > (parseDay(x.start) as Date).getTime())
    .slice()
    .sort((a, b) => a.start.localeCompare(b.start))
}

/** 学期共有几周：不足 7 天的尾巴也算一周（最后一周通常是考试周，会短） */
export function semesterWeekCount(sem: Semester): number {
  const from = parseDay(sem.start)
  const to = parseDay(sem.end)
  if (!from || !to) return 0
  const days = Math.round((to.getTime() - from.getTime()) / DAY_MS) + 1
  return Math.max(1, Math.ceil(days / 7))
}

export function semesterWeeks(sem: Semester): SemesterWeek[] {
  const from = parseDay(sem.start)
  const to = parseDay(sem.end)
  if (!from || !to) return []
  const end = new Date(to.getFullYear(), to.getMonth(), to.getDate() + 1) // 含最后一天
  const out: SemesterWeek[] = []
  for (let i = 0; i < semesterWeekCount(sem); i++) {
    const a = new Date(from.getFullYear(), from.getMonth(), from.getDate() + i * 7)
    const b = new Date(from.getFullYear(), from.getMonth(), from.getDate() + (i + 1) * 7)
    out.push({ index: i + 1, label: weekLabel(i + 1), from: a, to: b > end ? end : b })
  }
  return out
}

/** 取数窗口：[开学当天 00:00, 最后一天次日 00:00) */
export function semesterWindow(sem: Semester): { from: string; to: string; granularity: 'day' } | null {
  const from = parseDay(sem.start)
  const to = parseDay(sem.end)
  if (!from || !to) return null
  return {
    from: from.toISOString(),
    to: new Date(to.getFullYear(), to.getMonth(), to.getDate() + 1).toISOString(),
    granularity: 'day',
  }
}

/** 当前时间落在哪个学期；都不在则取最近开学的那个（假期里看上学期最自然） */
export function currentSemesterKey(list: Semester[], now = new Date()): string {
  const items = normalizeSemesters(list)
  if (!items.length) return ''
  const t = now.getTime()
  for (const sem of items) {
    const from = parseDay(sem.start) as Date
    const to = parseDay(sem.end) as Date
    if (t >= from.getTime() && t < to.getTime() + DAY_MS) return sem.key
  }
  const past = items.filter((x) => (parseDay(x.start) as Date).getTime() <= t)
  return (past.length ? past[past.length - 1] : items[0]).key
}

export function findSemester(list: Semester[], key: string): Semester | null {
  return normalizeSemesters(list).find((x) => x.key === key) ?? null
}

/*
把日粒度的时序折成学期周。

	points 的下标与 dayFrom 起的自然日一一对应；缺数的天传 null，
	禁止传 0。「只抄到两天」与「真没用电」是两件事。
	混在一起会让开学第一周看着特别省电。
*/
export function foldWeeks(
  weeks: SemesterWeek[],
  points: { at: Date; value: number | null; meters?: number }[],
): { vals: number[]; has: boolean[]; meters: number[] } {
  const vals = weeks.map(() => 0)
  const has = weeks.map(() => false)
  const meters = weeks.map(() => 0)
  for (const p of points) {
    if (p.value == null) continue
    const t = p.at.getTime()
    for (let i = 0; i < weeks.length; i++) {
      if (t >= weeks[i].from.getTime() && t < weeks[i].to.getTime()) {
        vals[i] += p.value
        has[i] = true
        // 一周里的表数取最大值：中途新装的表不该把整周的户均除小
        if (p.meters && p.meters > meters[i]) meters[i] = p.meters
        break
      }
    }
  }
  return { vals, has, meters }
}
