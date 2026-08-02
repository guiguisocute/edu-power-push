/* 取数中的三种表达。按是否已有内容选择，禁止混用。
   TopProgress：已有内容后台刷新 → 顶部发丝线。
   Skeleton：尚无内容但已知尺寸 → 占位块，落数不跳版。
   InlineNote：尚无内容且尺寸未知 → 脉冲点加一句话。
   禁止居中转圈遮罩。 */

import { useEffect, useState, useSyncExternalStore, type CSSProperties } from 'react'
import { inFlightCount, subscribeInFlight } from '../api/client'

/* 进度线时间常数，避免闪烁。
   200ms 内返回的请求不亮线。亮线后至少保留 400ms。 */
const SHOW_AFTER_MS = 200
const MIN_VISIBLE_MS = 400

export function useIsFetching(): boolean {
  const count = useSyncExternalStore(subscribeInFlight, inFlightCount, () => 0)
  return count > 0
}

export function TopProgress() {
  const busy = useIsFetching()
  const [shown, setShown] = useState(false)

  useEffect(() => {
    if (busy) {
      const t = setTimeout(() => setShown(true), SHOW_AFTER_MS)
      return () => clearTimeout(t)
    }
    if (!shown) return
    const t = setTimeout(() => setShown(false), MIN_VISIBLE_MS)
    return () => clearTimeout(t)
  }, [busy, shown])

  if (!shown) return null
  return (
    <div
      className="load-bar"
      role="progressbar"
      aria-label="正在读取数据"
      aria-busy="true"
      /* 禁止 aria-valuenow。进度百分比未知。假百分比会误导读屏。 */
    >
      <span />
    </div>
  )
}

/** 占位块。w/h 写真内容尺寸，落数时不跳版。 */
export function Skeleton({
  w = '100%',
  h = 12,
  style,
}: {
  w?: number | string
  h?: number | string
  style?: CSSProperties
}) {
  return (
    <span
      className="skel"
      aria-hidden="true"
      style={{ display: 'block', width: w, height: h, flex: 'none', ...style }}
    />
  )
}

/** 图表占位。高度照抄图表本体。底部留基线。 */
export function ChartSkeleton({ height, bars = 24 }: { height: number; bars?: number }) {
  return (
    <div
      aria-hidden="true"
      style={{
        display: 'flex',
        alignItems: 'flex-end',
        gap: '3px',
        height: height + 'px',
        borderBottom: '1px solid var(--line)',
      }}
    >
      {Array.from({ length: bars }, (_, i) => (
        <span
          key={i}
          className="skel"
          style={{
            flex: 1,
            /* 高度按正弦铺开。禁止随机数，避免形状每次变化。 */
            height: (34 + Math.sin(i / 2.4) * 22).toFixed(1) + '%',
            animationDelay: (i % 6) * 0.09 + 's',
          }}
        />
      ))}
    </div>
  )
}

/** 一行取数中。脉冲点表示非静止错误提示。 */
export function InlineNote({ text = '读取中', style }: { text?: string; style?: CSSProperties }) {
  return (
    <span
      role="status"
      style={{ display: 'inline-flex', alignItems: 'center', gap: '8px', fontSize: '12.5px', color: 'var(--fg3)', ...style }}
    >
      <span className="skel" style={{ width: '5px', height: '5px', background: 'var(--red)' }} />
      {text}
    </span>
  )
}
