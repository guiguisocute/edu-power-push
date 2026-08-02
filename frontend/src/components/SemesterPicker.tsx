/* 学期选择器。与 MonthWindowPicker 同形。
   等宽小标签加无边框下拉。样式共用 .mwp-select。
   校历未配置时不画空下拉，直接说明。 */

import type { CSSProperties } from 'react'
import { semesterLabel, semesterWeekCount, type Semester } from '../lib/semesters'

export default function SemesterPicker({
  value,
  options,
  onChange,
  style,
}: {
  value: string
  options: Semester[]
  onChange: (key: string) => void
  style?: CSSProperties
}) {
  const current = options.find((o) => o.key === value)
  return (
    <div style={{ display: 'inline-flex', alignItems: 'center', gap: '10px', flexWrap: 'wrap', ...style }}>
      <span
        style={{
          font: "500 9.5px/1 'JetBrains Mono',monospace",
          letterSpacing: '.2em',
          color: 'var(--fg3)',
          whiteSpace: 'nowrap',
        }}
      >
        SEMESTER
      </span>
      {options.length === 0 ? (
        <span style={{ fontSize: '12.5px', color: 'var(--fg3)' }}>校历未配置 · 暂按近 18 周显示</span>
      ) : (
        <>
          <select
            className="mwp-select"
            aria-label="选择学期"
            value={value}
            onChange={(e) => onChange(e.target.value)}
            style={{
              margin: 0,
              padding: '2px 0',
              border: 0,
              // 不透明底色。理由见 MonthWindowPicker。透明会白底白字。
              background: 'var(--bg)',
              color: 'var(--fg)',
              font: "500 13px/1 'JetBrains Mono',monospace",
              fontVariantNumeric: 'tabular-nums',
              cursor: 'pointer',
            }}
          >
            {options.map((o) => (
              <option key={o.key} value={o.key}>
                {semesterLabel(o.key)}
              </option>
            ))}
          </select>
          {current && (
            <span
              style={{
                font: "400 10.5px/1 'JetBrains Mono',monospace",
                color: 'var(--fg3)',
                whiteSpace: 'nowrap',
                fontVariantNumeric: 'tabular-nums',
              }}
            >
              {current.start.replace(/-/g, '/')}–{current.end.replace(/-/g, '/')} · {semesterWeekCount(current)} 周
            </span>
          )}
        </>
      )}
    </div>
  )
}
