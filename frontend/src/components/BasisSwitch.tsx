/* 口径开关（总量/户均）。切换同一数的读法，不是切换数据源。
   视觉接近小标题，不是药丸或按钮组。
   激活项用字重与红下划线。 */

import type { CSSProperties } from 'react'

export interface BasisOption<T extends string> {
  v: T
  l: string
  title?: string
}

export default function BasisSwitch<T extends string>({
  label,
  value,
  options,
  onChange,
  style,
}: {
  /** 左侧等宽小标签，如 BASIS。留空则不渲染。 */
  label?: string
  value: T
  options: BasisOption<T>[]
  onChange: (v: T) => void
  style?: CSSProperties
}) {
  return (
    <div style={{ display: 'inline-flex', alignItems: 'center', gap: '12px', ...style }}>
      {label && (
        <span
          style={{
            font: "500 9.5px/1 'JetBrains Mono',monospace",
            letterSpacing: '.2em',
            color: 'var(--fg3)',
            whiteSpace: 'nowrap',
          }}
        >
          {label}
        </span>
      )}
      <div style={{ display: 'inline-flex', alignItems: 'center' }} role="group" aria-label={label || '口径'}>
        {options.map((o, i) => (
          <span key={o.v} style={{ display: 'inline-flex', alignItems: 'center' }}>
            {i > 0 && (
              <span
                aria-hidden="true"
                style={{ width: '1px', height: '12px', background: 'var(--line)', margin: '0 12px' }}
              />
            )}
            <button
              type="button"
              className="basis-btn"
              data-on={o.v === value ? '1' : '0'}
              aria-pressed={o.v === value}
              title={o.title}
              onClick={() => onChange(o.v)}
            >
              {o.l}
            </button>
          </span>
        ))}
      </div>
    </div>
  )
}
