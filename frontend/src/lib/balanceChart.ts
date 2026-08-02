/** 将鼠标或触点横坐标吸附到最近余额记录。 */
export function nearestBalancePointIndex(clientX: number, left: number, width: number, count: number): number | null {
  if (count <= 0 || width <= 0) return null
  if (count === 1) return 0
  const ratio = Math.max(0, Math.min(1, (clientX - left) / width))
  return Math.round(ratio * (count - 1))
}
