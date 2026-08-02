import { buildCampusYearBars, hasBreakdownValues } from './campusYear'

function assert(cond: unknown, message: string) {
  if (!cond) throw new Error(message)
}

export function runCampusYearTests() {
  const settled = [{ month: '2026-06', total_kwh: '120.5', meters: 10 }]
  const unpublished = buildCampusYearBars(settled, '2026-07', null, 0)
  assert(unpublished.length === 1, '未发布的当月不能追加伪 0 柱')
  assert(unpublished[0].month === '2026-06' && unpublished[0].kwh === 120.5, '最近账单月必须保留')

  const publishedDaily = buildCampusYearBars(settled, '2026-07', 3.25, 9)
  assert(publishedDaily.length === 2, '拿到真实当月累计后应追加临时柱')
  assert(publishedDaily[1].provisional && publishedDaily[1].kwh === 3.25, '临时柱数值或标记错误')

  assert(!hasBreakdownValues([{ values: [null, null] }]), '全空矩阵不能追加本月列')
  assert(hasBreakdownValues([{ values: [null, '0'] }]), '真实的 0 也是有效观测，不能当成缺数')
}
