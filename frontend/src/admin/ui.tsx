/* 管理面板版式原语。
   与用户端共用设计语言：CSS 变量、JetBrains Mono 小标签、直角分割线。
   禁止另起一套视觉样式。 */

import type { CSSProperties, ReactNode } from 'react'

export const mono = (fs = '9px', ls = '.16em'): CSSProperties => ({
  font: `500 ${fs}/1 'JetBrains Mono',monospace`,
  letterSpacing: ls,
  color: 'var(--fg3)',
})

export function Section({
  en,
  title,
  desc,
  actions,
  children,
}: {
  en: string
  title: string
  desc?: string
  actions?: ReactNode
  children: ReactNode
}) {
  return (
    <section style={{ padding: '38px 0 34px', borderBottom: '1px solid var(--line)' }}>
      <div style={{ display: 'flex', alignItems: 'flex-end', gap: '20px', flexWrap: 'wrap', marginBottom: '24px' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '9px', minWidth: 0 }}>
          <div style={mono('9.5px', '.2em')}>{en}</div>
          <div style={{ fontSize: '26px', fontWeight: 600, letterSpacing: '-.035em' }}>{title}</div>
          {desc && (
            <div style={{ fontSize: '12.5px', color: 'var(--fg3)', maxWidth: '640px', textWrap: 'pretty' }}>{desc}</div>
          )}
        </div>
        {actions && <div style={{ marginLeft: 'auto', display: 'flex', gap: '10px', flexWrap: 'wrap' }}>{actions}</div>}
      </div>
      {children}
    </section>
  )
}

export function Btn({
  children,
  onClick,
  primary,
  danger,
  disabled,
  title,
}: {
  children: ReactNode
  onClick?: () => void
  primary?: boolean
  danger?: boolean
  disabled?: boolean
  title?: string
}) {
  return (
    <button
      className={primary ? 'hv-op82' : 'hv-line-fg'}
      onClick={onClick}
      disabled={disabled}
      title={title}
      style={{
        margin: 0,
        padding: '9px 18px',
        borderRadius: '999px',
        font: 'inherit',
        fontSize: '12.5px',
        fontWeight: primary ? 600 : 500,
        cursor: disabled ? 'not-allowed' : 'pointer',
        background: primary ? (danger ? 'var(--red)' : 'var(--fg)') : 'none',
        color: primary ? '#fff' : danger ? 'var(--red)' : 'var(--fg2)',
        border: `1px solid ${primary ? (danger ? 'var(--red)' : 'var(--fg)') : 'var(--line)'}`,
        opacity: disabled ? 0.45 : 1,
      }}
    >
      {children}
    </button>
  )
}

export const fieldStyle: CSSProperties = {
  width: '100%',
  background: 'none',
  border: 0,
  borderBottom: '1px solid var(--line)',
  padding: '9px 0',
  color: 'var(--fg)',
  fontFamily: 'inherit',
  fontSize: '14px',
  fontWeight: 500,
  letterSpacing: '-.01em',
}

export function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: ReactNode
}) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: '7px', minWidth: 0 }}>
      <span style={mono()}>{label}</span>
      {children}
      {hint && <span style={{ fontSize: '11.5px', color: 'var(--fg3)', lineHeight: 1.6 }}>{hint}</span>}
    </label>
  )
}

/** 一行「名称 — 值」的事实条目，面板里到处在用 */
export function Row({ label, value, tone }: { label: string; value: ReactNode; tone?: string }) {
  return (
    <div
      style={{
        display: 'flex',
        alignItems: 'baseline',
        gap: '14px',
        padding: '10px 0 12px',
        borderBottom: '1px solid var(--line2)',
      }}
    >
      <span style={{ fontSize: '12.5px', color: 'var(--fg3)', flex: 'none' }}>{label}</span>
      <span
        style={{
          marginLeft: 'auto',
          textAlign: 'right',
          fontSize: '12.5px',
          color: tone || 'var(--fg)',
          fontVariantNumeric: 'tabular-nums',
          minWidth: 0,
          wordBreak: 'break-all',
        }}
      >
        {value}
      </span>
    </div>
  )
}

/** KPI 方块，和用户端 campusStats 同形 */
export function Stat({ en, value, unit, note, tone }: { en: string; value: string; unit?: string; note: string; tone?: string }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '10px', paddingRight: '24px', minWidth: 0 }}>
      <div style={mono('9px', '.18em')}>{en}</div>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: '6px' }}>
        <span
          style={{
            fontSize: '30px',
            fontWeight: 600,
            letterSpacing: '-.04em',
            color: tone || 'var(--fg)',
            fontVariantNumeric: 'tabular-nums',
          }}
        >
          {value}
        </span>
        {unit && <span style={{ fontSize: '13px', color: 'var(--fg3)' }}>{unit}</span>}
      </div>
      <div style={{ fontSize: '11.5px', color: 'var(--fg3)', lineHeight: 1.6, textWrap: 'pretty' }}>{note}</div>
    </div>
  )
}

/** 状态点：ok / warn / bad，颜色语义和用户端一致 */
export function Dot({ tone }: { tone: 'ok' | 'warn' | 'bad' }) {
  const bg = tone === 'ok' ? '#12a150' : tone === 'warn' ? '#c88a00' : 'var(--red)'
  return <span style={{ width: '7px', height: '7px', flex: 'none', background: bg, display: 'inline-block' }} />
}

/** 说明性空态：说清楚为什么没有内容，而不是留一片白 */
export function Empty({ title, desc }: { title: string; desc: string }) {
  return (
    <div style={{ padding: '22px 0' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
        <span style={{ width: '5px', height: '5px', background: 'var(--fg3)', flex: 'none' }} />
        <span style={{ fontSize: '13px', color: 'var(--fg2)' }}>{title}</span>
      </div>
      <div style={{ fontSize: '12.5px', color: 'var(--fg3)', lineHeight: 1.7, marginTop: '10px', textWrap: 'pretty' }}>
        {desc}
      </div>
    </div>
  )
}

export function fmtTime(raw?: string | null): string {
  if (!raw) return '—'
  const d = new Date(raw)
  if (isNaN(d.getTime())) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

/** 相对时间，用来一眼看出「心跳是不是停了」 */
export function fmtAgo(raw?: string | null): string {
  if (!raw) return '—'
  const d = new Date(raw)
  if (isNaN(d.getTime())) return '—'
  const secs = Math.round((Date.now() - d.getTime()) / 1000)
  if (secs < 0) return '刚刚'
  if (secs < 60) return secs + ' 秒前'
  if (secs < 3600) return Math.floor(secs / 60) + ' 分钟前'
  if (secs < 86400) return Math.floor(secs / 3600) + ' 小时前'
  return Math.floor(secs / 86400) + ' 天前'
}
