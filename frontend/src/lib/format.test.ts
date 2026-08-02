import { formatKwhInUnit } from './format'

function assertEqual(actual: string, expected: string, label: string) {
  if (actual !== expected) throw new Error(`${label}: expected ${expected}, got ${actual}`)
}

export function runFormatTests() {
  assertEqual(formatKwhInUnit(81_609, '万元', 1).v, '8.16', '万元行值跟随共享表头单位')
  assertEqual(formatKwhInUnit(81_609, '元', 1).v, '81,609', '元单位保留原始金额量级')
  assertEqual(formatKwhInUnit(81_609, 'MWh', 1).v, '81.61', 'MWh 行值跟随共享表头单位')
  assertEqual(formatKwhInUnit(81_609, 'kWh', 1).v, '81,609', 'kWh 单位保留原始用量量级')
}
