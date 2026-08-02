/* 学期切周。算错不报错，但横轴会错周或把日算进邻周。
   重点：纯日期串时区、开学日非周一边界、不足 7 天的末周。 */
import {
  currentSemesterKey,
  foldWeeks,
  normalizeSemesters,
  parseDay,
  semesterLabel,
  semesterWeekCount,
  semesterWeeks,
  weekLabel,
  type Semester,
} from './semesters'

function assert(cond: unknown, msg: string) {
  if (!cond) throw new Error(msg)
}

export function runSemesterTests() {
  // 纯日期串必须落在本地零点。用 Date.parse 会当成 UTC，东八区变成前一天 08:00
  const d = parseDay('2026-03-02') as Date
  assert(d.getFullYear() === 2026 && d.getMonth() === 2 && d.getDate() === 2, '日期串应解析为本地当天，得到 ' + d)
  assert(d.getHours() === 0, '日期串应落在本地零点，得到 ' + d.getHours())
  assert(parseDay('2026-3-2') === null, '非补零格式应判为非法')

  assert(semesterLabel('2026-03') === '2026-03学期', '学期标签格式应为 2026-03学期')
  assert(weekLabel(1) === '第 1 周', '周标签格式应为「第 1 周」')

  // 2026-03-02 是周一，到 07-12 共 133 天 = 19 周整
  const spring: Semester = { key: '2026-03', start: '2026-03-02', end: '2026-07-12' }
  assert(semesterWeekCount(spring) === 19, '19 周整的学期应算出 19，得到 ' + semesterWeekCount(spring))
  const weeks = semesterWeeks(spring)
  assert(weeks.length === 19, '应切出 19 周，得到 ' + weeks.length)
  assert(weeks[0].label === '第 1 周' && weeks[18].label === '第 19 周', '首末周标签应连续')
  assert(weeks[0].from.getDate() === 2, '第 1 周应从开学当天起')
  // 每一周都紧接上一周，中间不能有缝
  for (let i = 1; i < weeks.length; i++) {
    assert(weeks[i].from.getTime() === weeks[i - 1].to.getTime(), `第 ${i + 1} 周与上一周之间有缝`)
  }
  // 末周不能越过学期最后一天的次日零点
  assert(weeks[18].to.getTime() === new Date(2026, 6, 13).getTime(), '末周应止于最后一天的次日零点')

  /* 开学日是周日时禁止用 ISO 周。否则第 1 周会只剩一天。 */
  const sunday: Semester = { key: '2025-09', start: '2025-09-07', end: '2025-09-27' }
  const sw = semesterWeeks(sunday)
  assert(sw.length === 3, '21 天应切成 3 周，得到 ' + sw.length)
  assert(sw[0].from.getDay() === 0 && sw[1].from.getDay() === 0, '每周都应从开学那个星期几起算')

  // 尾巴不足一周也要算作一周。否则末几天用电无处归属。
  const ragged: Semester = { key: '2026-03', start: '2026-03-02', end: '2026-03-12' }
  assert(semesterWeekCount(ragged) === 2, '11 天应算 2 周，得到 ' + semesterWeekCount(ragged))

  // 非法记录直接丢弃，且按开学时间排序
  const cleaned = normalizeSemesters([
    { key: '2026-03', start: '2026-03-02', end: '2026-07-12' },
    { key: 'bad', start: '2025-09-01', end: '2026-01-18' },
    { key: '2025-09', start: '2025-09-01', end: '2026-01-18' },
    { key: '2024-09', start: '2024-09-01', end: '2024-08-01' },
  ])
  assert(cleaned.length === 2, '应只留下 2 条合法记录，得到 ' + cleaned.length)
  assert(cleaned[0].key === '2025-09', '应按开学时间升序，首项得到 ' + cleaned[0].key)

  const list: Semester[] = [
    { key: '2025-09', start: '2025-09-01', end: '2026-01-18' },
    { key: '2026-03', start: '2026-03-02', end: '2026-07-12' },
  ]
  assert(currentSemesterKey(list, new Date(2026, 4, 1)) === '2026-03', '学期中应选中当前学期')
  // 最后一天当天仍算在学期内
  assert(currentSemesterKey(list, new Date(2026, 6, 12)) === '2026-03', '学期最后一天仍应算在学期内')
  // 假期里落到最近开学过的那个学期，而不是跳到还没开学的下一个
  assert(currentSemesterKey(list, new Date(2026, 1, 10)) === '2025-09', '寒假应回落到上一个学期')
  assert(currentSemesterKey(list, new Date(2024, 0, 1)) === '2025-09', '早于全部学期时应取最早的一个')
  assert(currentSemesterKey([], new Date()) === '', '未配置学期时应返回空串')

  // 折周：缺数天禁止当 0。否则只抄到两天会被读成很省电。
  const folded = foldWeeks(semesterWeeks(ragged), [
    { at: new Date(2026, 2, 2), value: 1, meters: 3 },
    { at: new Date(2026, 2, 8), value: 2, meters: 5 },
    { at: new Date(2026, 2, 9), value: 4, meters: 4 },
    { at: new Date(2026, 2, 10), value: null },
  ])
  assert(folded.vals[0] === 3, '第 1 周应是 3（3/2 与 3/8），得到 ' + folded.vals[0])
  assert(folded.vals[1] === 4, '第 2 周应是 4，得到 ' + folded.vals[1])
  assert(folded.has[0] && folded.has[1], '两周都应标记为有数')
  assert(folded.meters[0] === 5, '一周的表数应取最大值，得到 ' + folded.meters[0])

  // 落在学期之外的点必须被忽略，而不是挤进第 1 周
  const outside = foldWeeks(semesterWeeks(ragged), [{ at: new Date(2026, 1, 20), value: 99 }])
  assert(outside.vals.every((v) => v === 0), '学期外的数据点不应计入任何一周')
  assert(outside.has.every((h) => !h), '学期外的数据点不应把周标记为有数')
}
