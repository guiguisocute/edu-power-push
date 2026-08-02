/* 月份区间算术。

   防的是跨年与月末：用 Date.setMonth 连续加减时，「1 月往前退一个月」和
   「31 号那天算上个月」都会跳月。这套界面上跳月的表现是某个月份凭空消失，
   或者选了区间却画出别的月份，出了问题很难一眼看出是日期算错。 */
import {
  addMonths,
  clampRange,
  currentMonth,
  monthLabel,
  monthOptions,
  monthShort,
  monthSpan,
  monthsBetween,
} from './months'

function assert(cond: unknown, msg: string) {
  if (!cond) throw new Error(msg)
}

export function runMonthTests() {
  // 跨年前后各一步
  assert(addMonths('2026-01', -1) === '2025-12', '1 月往前退一个月应到上一年 12 月')
  assert(addMonths('2025-12', 1) === '2026-01', '12 月往后进一个月应到下一年 1 月')
  assert(addMonths('2026-07', -11) === '2025-08', '默认区间：当月往前 11 个月')
  assert(addMonths('2026-03', 0) === '2026-03', '零位移应原样返回')

  // 月末不影响：31 号那天算「上个月」也不该跳到两个月前
  const endOfMonth = new Date(2026, 6, 31)
  assert(currentMonth(endOfMonth) === '2026-07', '7 月 31 日的当月应是 2026-07')
  assert(addMonths(currentMonth(endOfMonth), -1) === '2026-06', '7 月 31 日往前一个月应是 6 月')

  // 区间展开：含端点、升序、跨年不断
  const span = monthsBetween('2025-11', '2026-02')
  assert(span.join(',') === '2025-11,2025-12,2026-01,2026-02', '跨年区间展开错误：' + span.join(','))
  assert(monthSpan('2025-08', '2026-07') === 12, '25/08–26/07 应是 12 个月')
  assert(monthSpan('2026-03', '2026-03') === 1, '单月区间应是 1 个月')
  assert(monthsBetween('2026-05', '2026-01').length === 0, '起止颠倒应返回空数组')
  assert(monthSpan('2026-05', '2026-01') === 0, '起止颠倒的月数应是 0')

  // 端点颠倒时以刚改动的一端为准。禁止丢掉用户刚选的值。
  assert(clampRange('2026-09', '2026-05', 'from').join() === '2026-09,2026-09', '改起始月后应把终止月跟上')
  assert(clampRange('2026-09', '2026-05', 'to').join() === '2026-05,2026-05', '改终止月后应把起始月跟下')
  assert(clampRange('2025-01', '2026-01', 'from').join() === '2025-01,2026-01', '合法区间不该被改动')

  // 下拉：覆盖到最早账单月且连续。缺账单月仍可作端点。
  const now = new Date(2026, 6, 15)
  const opts = monthOptions(['2025-01', '2025-06'], 12, now)
  assert(opts[0] === '2025-01', '下拉应从最早的账单月开始，得到 ' + opts[0])
  assert(opts[opts.length - 1] === '2026-07', '下拉应到当月为止，得到 ' + opts[opts.length - 1])
  assert(opts.length === 19, '2025-01 到 2026-07 共 19 个月，得到 ' + opts.length)
  assert(opts.includes('2025-09'), '中间没有账单的月份也必须可选，否则跨空档的区间选不出来')
  for (let i = 1; i < opts.length; i++) {
    assert(addMonths(opts[i - 1], 1) === opts[i], `下拉月份不连续：${opts[i - 1]} 之后是 ${opts[i]}`)
  }
  // 无账单时下拉也不能空。
  const bare = monthOptions([], 12, now)
  assert(bare.length === 12, '无账单时应回落到最近 12 个月，得到 ' + bare.length)
  assert(bare[bare.length - 1] === '2026-07', '兜底下拉的末项应是当月')

  assert(monthLabel('2026-07') === '2026/07', '完整标签格式应为 2026/07')
  assert(monthShort('2026-07') === '26/07', '短标签格式应为 26/07')
}
