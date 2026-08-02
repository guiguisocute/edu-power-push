/* 账期下拉的月份来源。
   数据在库且接口返回，但下拉不可达，与未恢复无异。 */
import { liveMonthKeys } from '../views/UsageView'

function assert(cond: unknown, msg: string) {
  if (!cond) throw new Error(msg)
}

const key = (o: { y: number; m: number }) => o.y + '-' + String(o.m + 1).padStart(2, '0')

export function runMonthKeyTests() {
  // 18 个月历史（2025-01→2026-06）全部必须可选。
  const bills = Array.from({ length: 18 }, (_, i) => {
    const d = new Date(2025, 0 + i, 1)
    return { month: d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') }
  })
  const keys = liveMonthKeys(bills)
  const got = new Set(keys.map(key))
  for (const b of bills) {
    assert(got.has(b.month), `账单里有 ${b.month} 却选不到 —— 数据恢复了但界面上够不着`)
  }

  // 无账单时下拉也不能空。刚绑表用户须能看到最近几个月。
  const empty = liveMonthKeys([])
  assert(empty.length === 12, `无账单时应回落到最近 12 个月，得到 ${empty.length}`)

  // 倒序：最近月份在前。
  for (let i = 1; i < keys.length; i++) {
    const prev = keys[i - 1]
    const cur = keys[i]
    assert(
      prev.y > cur.y || (prev.y === cur.y && prev.m > cur.m),
      `月份未按倒序排列：${key(prev)} 出现在 ${key(cur)} 之前`,
    )
  }

  // 去重：账单月与最近 12 个月重叠时，下拉禁止重复。
  assert(got.size === keys.length, '下拉里出现了重复月份')

  // 月份 0 基，与 store mKey 一致。跨年禁止算错。
  const dec = liveMonthKeys([{ month: '2024-12' }]).find((o) => o.y === 2024 && o.m === 11)
  assert(dec, '2024-12 应解析成 { y: 2024, m: 11 }（m 为 0 基）')
}
