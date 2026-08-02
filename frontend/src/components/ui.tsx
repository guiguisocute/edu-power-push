/* 共享 UI 原语。与原型分段按钮与开关样式一致。 */

import type { CSSProperties, ReactNode } from 'react'
import type { SegProps } from '../lib/format'

/** 品牌标记。直角红底方块加白色闪电。与站点 favicon 同形。 */
export function BrandMark({ size = 20, style }: { size?: number; style?: CSSProperties }) {
  return (
    <svg
      viewBox="0 0 32 32"
      width={size}
      height={size}
      aria-hidden="true"
      style={{ display: 'block', flex: 'none', ...style }}
    >
      <rect width="32" height="32" fill="var(--red)" />
      <path d="M17.5 6 9 17.5h5.5L13.5 26 23 14.5h-5.5z" fill="#fff" />
    </svg>
  )
}

/** 圆角药丸分组容器 */
export function SegGroup({
  children,
  style,
  ...rest
}: { children: ReactNode; style?: CSSProperties } & Record<`data-${string}`, string | undefined>) {
  return (
    <div
      {...rest}
      style={{
        display: 'flex',
        padding: '3px',
        border: '1px solid var(--line)',
        borderRadius: '999px',
        gap: '2px',
        ...style,
      }}
    >
      {children}
    </div>
  )
}

/** 分段按钮。pad 与 fs 对应原型各处 padding 与字号。 */
export function SegBtn({
  seg,
  pad,
  fs,
  title,
}: {
  seg: SegProps
  pad: string
  fs: string
  title?: string
}) {
  return (
    <button
      onClick={seg.go}
      title={title}
      style={{
        border: 0,
        margin: 0,
        padding: pad,
        borderRadius: '999px',
        font: 'inherit',
        cursor: 'pointer',
        fontSize: fs,
        fontWeight: seg.w as CSSProperties['fontWeight'],
        background: seg.bg,
        color: seg.fg,
        transition: 'background .16s, color .16s',
      }}
    >
      {seg.label}
    </button>
  )
}

/** 开关。lg = 46×27（配置区）。sm = 42×25（渠道行）。 */
export function Toggle({
  on,
  trackOff,
  onClick,
  size = 'lg',
  style,
}: {
  on: boolean
  trackOff: string
  onClick: (e: React.MouseEvent) => void
  size?: 'lg' | 'sm'
  style?: CSSProperties
}) {
  const lg = size === 'lg'
  const w = lg ? '46px' : '42px'
  const h = lg ? '27px' : '25px'
  const k = lg ? '21px' : '19px'
  const x = on ? (lg ? '19px' : '17px') : '0px'
  return (
    <button
      onClick={onClick}
      style={{
        border: 0,
        margin: 0,
        padding: '3px',
        flex: 'none',
        width: w,
        height: h,
        borderRadius: '999px',
        cursor: 'pointer',
        transition: 'background .16s',
        background: on ? 'var(--red)' : trackOff,
        ...style,
      }}
    >
      <span
        style={{
          display: 'block',
          width: k,
          height: k,
          borderRadius: '99px',
          background: '#fff',
          transition: 'transform .16s',
          transform: `translateX(${x})`,
        }}
      />
    </button>
  )
}

/** 开关关闭态轨道色 */
export const trackOffColor = (dark: boolean) => (dark ? '#2e2e2e' : '#dcdcdc')
