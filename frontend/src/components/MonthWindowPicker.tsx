/* 账期窗口选择器。直接选起始月与终止月，选完即重绘。
   支持跨页任意区间。无边框无底色，仅标签加下拉。 */

import type { CSSProperties } from 'react'
import { clampRange, monthLabel, type MonthKey } from '../lib/months'

const MONO = "500 13px/1 'JetBrains Mono',monospace"

function MonthSelect({
  value,
  options,
  onChange,
  ariaLabel,
}: {
  value: MonthKey
  options: MonthKey[]
  onChange: (v: MonthKey) => void
  ariaLabel: string
}) {
  return (
    <select
      className="mwp-select"
      aria-label={ariaLabel}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      style={{
        margin: 0,
        padding: '2px 0',
        border: 0,
        /* 必须用不透明色。禁止 none 与 transparent。
           Chrome 下拉浮层用 select 的 background-color。
           透明时深色模式会白底白字。--bg 为页面底色。 */
        background: 'var(--bg)',
        color: 'var(--fg)',
        font: MONO,
        fontVariantNumeric: 'tabular-nums',
        cursor: 'pointer',
      }}
    >
      {options.map((o) => (
        <option key={o} value={o}>
          {monthLabel(o)}
        </option>
      ))}
    </select>
  )
}

export default function MonthWindowPicker({
  label,
  from,
  to,
  options,
  onChange,
  hint,
  style,
}: {
  /** 框内等宽小标签，如 RANGE / MONTH */
  label: string
  from: MonthKey
  /** 省略 = 单月模式，只渲染一个下拉 */
  to?: MonthKey | null
  options: MonthKey[]
  /** 单月模式下 to 与 from 相同 */
  onChange: (from: MonthKey, to: MonthKey) => void
  /** 右侧极短补充。不传则不显示。 */
  hint?: string
  style?: CSSProperties
}) {
  const single = to == null
  /* 当前值必须在选项里。切换楼栋后可选月会变。
     选中月掉出列表时，界面显示月与取数月会对不上。 */
  const opts = [...options]
  for (const v of single ? [from] : [from, to as MonthKey]) {
    if (v && !opts.includes(v)) opts.push(v)
  }
  opts.sort((a, b) => b.localeCompare(a))
  return (
    <div
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '10px',
        flexWrap: 'wrap',
        ...style,
      }}
    >
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
      <MonthSelect
        value={from}
        options={opts}
        ariaLabel={single ? '选择月份' : '起始月'}
        onChange={(v) => {
          const [a, b] = single ? [v, v] : clampRange(v, to as MonthKey, 'from')
          onChange(a, b)
        }}
      />
      {!single && (
        <>
          <span aria-hidden="true" style={{ color: 'var(--fg3)', font: MONO }}>
            –
          </span>
          <MonthSelect
            value={to as MonthKey}
            options={opts}
            ariaLabel="终止月"
            onChange={(v) => {
              const [a, b] = clampRange(from, v, 'to')
              onChange(a, b)
            }}
          />
        </>
      )}
      {hint ? (
        <span
          style={{
            font: "400 10.5px/1 'JetBrains Mono',monospace",
            color: 'var(--fg3)',
            whiteSpace: 'nowrap',
            fontVariantNumeric: 'tabular-nums',
          }}
        >
          {hint}
        </span>
      ) : null}
    </div>
  )
}
