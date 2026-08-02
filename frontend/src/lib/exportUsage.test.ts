import { CSV_BOM, buildUsageRows, exportFilename, toCSV, toJSON, type UsageExportMeta } from './exportUsage'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message)
}

const META: UsageExportMeta = {
  meter: '31240718',
  place: '11栋 · 1楼 · N301',
  from: '2026-06',
  to: '2026-07',
  rate: 0.62,
  generatedAt: '2026-07-31T12:00:00.000Z',
}

export function runUsageExportTests() {
  // ---- 对齐两条序列 ----
  const rows = buildUsageRows(
    [
      { period_start: '2026-07-02T00:00:00+08:00', value: '9.80' },
      { period_start: '2026-07-01T00:00:00+08:00', value: '12.35' },
      { period_start: '2026-07-03T00:00:00+08:00', value: null },
    ],
    [
      { period_start: '2026-07-01T00:00:00+08:00', value: '88.20' },
      // 仅有余额、无用电量的日期也要出现。禁止导出少行。
      { period_start: '2026-07-04T00:00:00+08:00', value: '70.00' },
    ],
    0.62,
  )
  assert(rows.map((r) => r.date).join(',') === '2026-07-01,2026-07-02,2026-07-03,2026-07-04', '行必须按日期升序且并集')
  assert(rows[0].kwh === 12.35 && rows[0].balance === 88.2, '同一天的用电量与余额要落到同一行')
  assert(rows[0].cost === 7.66, '电费 = 用电量 × 电价，两位小数')
  assert(rows[2].kwh === null && rows[2].cost === null, '缺数的一天不能被折算出电费')
  assert(rows[3].kwh === null && rows[3].balance === 70, '只有余额的一天用电量留空')

  // 时区：UTC 2026-06-30T16:00Z 在学校时区为 7 月 1 日。
  const tz = buildUsageRows([{ period_start: '2026-06-30T16:00:00Z', value: '1.00' }], [], 0.62)
  assert(tz[0].date === '2026-07-01', '日期必须按学校所在时区归日，不能跟着浏览器时区跑')

  // ---- CSV ----
  const csv = toCSV(rows)
  assert(csv.startsWith(CSV_BOM), 'CSV 必须带 BOM，否则 Excel 双击打开是乱码')
  const lines = csv.trimEnd().split('\r\n')
  assert(lines[0] === CSV_BOM + '日期,用电量(kWh),电费(元),余额(元)', '表头列名固定：' + lines[0])
  assert(lines[1] === '2026-07-01,12.35,7.66,88.20', '数值列两位小数：' + lines[1])
  assert(lines[3] === '2026-07-03,,,', '缺数写空字段，不能填 0：' + lines[3])
  assert(csv.endsWith('\r\n'), 'CSV 末尾要留换行')

  const escaped = toCSV([{ date: '2026-07-01', kwh: 1, cost: 1, balance: 1 }])
  assert(!escaped.includes('""'), '没有特殊字符时不该多加引号')

  // ---- JSON ----
  const parsed = JSON.parse(toJSON(rows, META))
  assert(parsed.meter === '31240718' && parsed.from === '2026-06' && parsed.to === '2026-07', 'JSON 要带电表与范围')
  assert(parsed.electricity_rate_yuan_per_kwh === 0.62, 'JSON 要写清折算用的电价')
  assert(parsed.units.cost === 'CNY' && parsed.units.kwh === 'kWh', 'JSON 要写清单位')
  assert(parsed.row_count === rows.length && parsed.rows.length === rows.length, 'row_count 要和实际行数一致')

  // ---- 文件名 ----
  assert(exportFilename(META, 'csv') === '用电详情_31240718_2026-06_2026-07.csv', exportFilename(META, 'csv'))
  assert(
    exportFilename({ ...META, to: '2026-06' }, 'json') === '用电详情_31240718_2026-06.json',
    '单月不重复写两遍月份',
  )
}
