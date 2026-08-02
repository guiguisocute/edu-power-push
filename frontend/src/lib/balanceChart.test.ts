import { nearestBalancePointIndex } from './balanceChart'

export function runBalanceChartTests() {
  const cases: [number, number | null][] = [
    [-20, 0],
    [0, 0],
    [49, 2],
    [100, 4],
    [140, 4],
  ]
  for (const [x, want] of cases) {
    const got = nearestBalancePointIndex(x, 0, 100, 5)
    if (got !== want) throw new Error(`nearest point at ${x} = ${got}, want ${want}`)
  }
  if (nearestBalancePointIndex(50, 0, 100, 0) !== null || nearestBalancePointIndex(50, 0, 0, 5) !== null) {
    throw new Error('empty balance chart should not expose a point')
  }
}
