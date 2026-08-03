import { densifyMonthDays, seriesDayKey } from './live'

function assert(cond: unknown, msg: string) {
  if (!cond) throw new Error(msg)
}

export function runSeriesDenseTests() {
  // UTC 午夜在美西会拨前一天；必须按上海日历对齐。
  const shanghaiAug1 = Date.parse('2026-07-31T16:00:00.000Z') // 上海 8/1 00:00
  assert(seriesDayKey(shanghaiAug1) === '2026-08-01', `seriesDayKey 应按上海日历：${seriesDayKey(shanghaiAug1)}`)

  // 仅 3 个有数日 → 铺满 31 槽，禁止 3 根巨型柱。
  const sparse = {
    at: [
      Date.parse('2026-08-01T00:00:00+08:00'),
      Date.parse('2026-08-02T00:00:00+08:00'),
      Date.parse('2026-08-03T00:00:00+08:00'),
    ],
    vals: [4.1, 5.2, 6.76] as (number | null)[],
    meters: [1, 1, 1],
  }
  const dense = densifyMonthDays(sparse, 2026, 7) // August
  assert(dense.n === 31, `8 月应有 31 槽，得到 ${dense.n}`)
  assert(dense.has[0] && dense.vals[0] === 4.1, '8/1 有数')
  assert(dense.has[1] && dense.vals[1] === 5.2, '8/2 有数')
  assert(dense.has[2] && dense.vals[2] === 6.76, '8/3 有数')
  assert(!dense.has[3] && dense.vals[3] === 0, '8/4 无数应留空（0 + has=false）')
  assert(!dense.has[30] && dense.vals[30] === 0, '8/31 无数应留空')
  assert(dense.labels[0] === '8/1' && dense.labels[14] === '8/15' && dense.labels[30] === '8/31', '刻度标签')

  // 真 0 用电 ≠ 缺数
  const zeroDay = densifyMonthDays(
    {
      at: [Date.parse('2026-02-01T00:00:00+08:00')],
      vals: [0],
      meters: [1],
    },
    2026,
    1,
  )
  assert(zeroDay.n === 28, `2026-02 应有 28 天，得到 ${zeroDay.n}`)
  assert(zeroDay.has[0] && zeroDay.vals[0] === 0, '真 0 必须 has=true')
  assert(!zeroDay.has[1], '其余日无数')

  // 空序列也要铺满，避免 n=1 单根满宽柱
  const empty = densifyMonthDays({ at: [], vals: [], meters: [] }, 2026, 3)
  assert(empty.n === 30 && empty.has.every((h) => !h), '空月仍铺满且全 has=false')
}
