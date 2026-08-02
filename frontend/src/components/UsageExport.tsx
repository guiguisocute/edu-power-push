/* 用电详情导出。下载按钮加选范围/格式浮窗。
   范围复用 MonthWindowPicker。取数走 /me/series 日粒度。
   导出必须与页面数据一致。禁止新开后端端点。 */

import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import MonthWindowPicker from './MonthWindowPicker'
import { api } from '../api/client'
import { IS_LIVE } from '../api/mode'
import { errMsg } from '../api/errors'
import { monthIndex, type MonthKey } from '../lib/months'
import {
  buildUsageRows,
  downloadText,
  serializeUsageExport,
  type UsageExportFormat,
  type UsageExportRow,
} from '../lib/exportUsage'

const mono = (fs = '9.5px', ls = '.2em') => ({
  font: `500 ${fs}/1 'JetBrains Mono',monospace`,
  letterSpacing: ls,
  color: 'var(--fg3)',
})

/** [from 月 1 日, to 月末次日)。与 live.ts monthWindow 同为左闭右开。 */
function windowOf(from: MonthKey, to: MonthKey): { from: string; to: string } {
  const iso = (d: Date) =>
    `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
  const [fy, fm] = from.split('-').map(Number)
  const [ty, tm] = to.split('-').map(Number)
  return { from: iso(new Date(fy, fm - 1, 1)), to: iso(new Date(ty, tm, 1)) }
}

export default function UsageExport({
  meter,
  place,
  rate,
  months,
  defaultMonth,
  /** mock 逐日数据。无真实后端时导出必须与页面演示数据一致。 */
  mockRows,
}: {
  meter: string
  place: string
  rate: number
  months: MonthKey[]
  defaultMonth: MonthKey
  mockRows?: (from: MonthKey, to: MonthKey) => UsageExportRow[]
}) {
  const [open, setOpen] = useState(false)
  const [from, setFrom] = useState<MonthKey>(defaultMonth)
  const [to, setTo] = useState<MonthKey>(defaultMonth)
  const [format, setFormat] = useState<UsageExportFormat>('csv')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState('')
  const closeButton = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (!open) return
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    closeButton.current?.focus()
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => {
      document.body.style.overflow = previousOverflow
      window.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  const months12 = monthIndex(to) - monthIndex(from) + 1

  const run = async () => {
    setBusy(true)
    setError('')
    setDone('')
    try {
      const win = windowOf(from, to)
      const rows = IS_LIVE
        ? await (async () => {
            /* 两条序列并行取：串行的话跨年区间要等两轮 RTT，
               而它们之间没有任何依赖关系。 */
            const [consumption, balance] = await Promise.all([
              api.meSeries({ ...win, granularity: 'day', metric: 'consumption' }),
              api.meSeries({ ...win, granularity: 'day', metric: 'balance' }),
            ])
            return buildUsageRows(consumption.points, balance.points, rate)
          })()
        : (mockRows?.(from, to) ?? [])
      if (rows.length === 0) {
        setError('该时间段没有可导出的逐日数据，请更换时间范围')
        return
      }
      const file = serializeUsageExport(
        rows,
        { meter, place, from, to, rate, generatedAt: new Date().toISOString() },
        format,
      )
      downloadText(file.filename, file.mime, file.text)
      setDone(`已导出 ${rows.length} 天 · ${file.filename}`)
    } catch (e) {
      setError(errMsg(e))
    } finally {
      setBusy(false)
    }
  }

  const trigger = (
    <button
      type="button"
      className="hv-line-fg"
      onClick={() => {
        setOpen(true)
        setError('')
        setDone('')
      }}
      title="导出所选时间段的逐日用电与电费"
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '8px',
        margin: 0,
        padding: '9px 16px',
        borderRadius: '999px',
        border: '1px solid var(--line)',
        background: 'none',
        color: 'var(--fg2)',
        font: 'inherit',
        fontSize: '12.5px',
        cursor: 'pointer',
        whiteSpace: 'nowrap',
      }}
    >
      <svg
        viewBox="0 0 24 24"
        width="13"
        height="13"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden="true"
        style={{ flex: 'none' }}
      >
        <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
        <polyline points="7 10 12 15 17 10" />
        <line x1="12" y1="15" x2="12" y2="3" />
      </svg>
      下载
    </button>
  )

  if (!open || typeof document === 'undefined') return trigger

  return (
    <>
      {trigger}
      {createPortal(
        <div
          role="dialog"
          aria-modal="true"
          aria-label="导出用电详情"
          onClick={(event) => {
            if (event.target === event.currentTarget) setOpen(false)
          }}
          style={{
            position: 'fixed',
            inset: 0,
            zIndex: 200,
            display: 'grid',
            placeItems: 'center',
            padding: '16px',
            background: 'rgba(0,0,0,.5)',
            backdropFilter: 'blur(4px)',
            animation: 'fadein .16s ease both',
          }}
        >
          <section
            style={{
              width: 'min(520px, 100%)',
              maxHeight: 'calc(100dvh - 32px)',
              display: 'flex',
              flexDirection: 'column',
              overflowY: 'auto',
              border: '1px solid var(--line)',
              background: 'var(--bg)',
              boxShadow: '0 24px 80px rgba(0,0,0,.32)',
            }}
          >
            <header
              style={{
                display: 'flex',
                alignItems: 'flex-start',
                gap: '14px',
                padding: '20px 20px 16px',
                borderBottom: '1px solid var(--line2)',
              }}
            >
              <div style={{ display: 'flex', flexDirection: 'column', gap: '8px', minWidth: 0 }}>
                <div style={mono()}>EXPORT · CONSUMPTION DETAIL</div>
                <div style={{ fontSize: '19px', fontWeight: 600, letterSpacing: '-.03em' }}>导出用电详情</div>
              </div>
              <button
                ref={closeButton}
                type="button"
                aria-label="关闭导出"
                title="关闭"
                onClick={() => setOpen(false)}
                style={{
                  width: '32px',
                  height: '32px',
                  flex: 'none',
                  marginLeft: 'auto',
                  padding: 0,
                  border: '1px solid var(--line)',
                  borderRadius: '999px',
                  background: 'none',
                  color: 'var(--fg2)',
                  font: '400 18px/1 sans-serif',
                  cursor: 'pointer',
                }}
              >
                ×
              </button>
            </header>

            <div style={{ padding: '22px 20px', display: 'flex', flexDirection: 'column', gap: '26px' }}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
                <MonthWindowPicker
                  label="RANGE"
                  from={from}
                  to={to}
                  options={months}
                  onChange={(a, b) => {
                    setFrom(a)
                    setTo(b)
                    setDone('')
                  }}
                  hint={months12 > 1 ? `${months12} 个月` : undefined}
                />
                <span style={{ fontSize: '11.5px', color: 'var(--fg3)', lineHeight: 1.6 }}>
                  导出范围内的逐日明细。
                </span>
              </div>

              <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
                <span style={mono('9px', '.16em')}>FORMAT</span>
                <div style={{ display: 'inline-flex', border: '1px solid var(--line)', alignSelf: 'flex-start' }}>
                  {(['csv', 'json'] as UsageExportFormat[]).map((value, index) => {
                    const active = format === value
                    return (
                      <button
                        key={value}
                        type="button"
                        aria-pressed={active}
                        onClick={() => {
                          setFormat(value)
                          setDone('')
                        }}
                        style={{
                          border: 0,
                          borderRight: index === 0 ? '1px solid var(--line)' : 0,
                          background: active ? 'var(--fg)' : 'transparent',
                          color: active ? 'var(--bg)' : 'var(--fg2)',
                          padding: '10px 20px',
                          font: "500 12px/1 'JetBrains Mono',monospace",
                          letterSpacing: '.08em',
                          cursor: 'pointer',
                        }}
                      >
                        {value.toUpperCase()}
                      </button>
                    )
                  })}
                </div>
                <span style={{ fontSize: '11.5px', color: 'var(--fg3)', lineHeight: 1.6, textWrap: 'pretty' }}>
                  {format === 'csv'
                    ? 'CSV 可用表格软件直接打开。列：日期、用电量(kWh)、电费(元)、余额(元)。'
                    : 'JSON 含电表号、范围、电价与单位。可用于脚本处理。'}
                </span>
              </div>

              <div
                style={{
                  padding: '13px 14px',
                  border: '1px solid var(--line)',
                  background: 'var(--sub)',
                  fontSize: '11.5px',
                  color: 'var(--fg2)',
                  lineHeight: 1.7,
                  textWrap: 'pretty',
                }}
              >
                电费按当前电价 {rate} 元/kWh 折算。
              </div>

              {!!error && (
                <div
                  role="status"
                  style={{
                    padding: '11px 12px',
                    borderLeft: '2px solid var(--red)',
                    background: 'var(--redsoft)',
                    color: 'var(--red)',
                    fontSize: '12px',
                    lineHeight: 1.6,
                  }}
                >
                  {error}
                </div>
              )}
              {!!done && (
                <div role="status" style={{ fontSize: '12px', color: 'var(--fg2)', lineHeight: 1.6 }}>
                  {done}
                </div>
              )}
            </div>

            <footer
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '12px',
                padding: '16px 20px',
                borderTop: '1px solid var(--line2)',
                flexWrap: 'wrap',
              }}
            >
              <span style={{ fontSize: '11.5px', color: 'var(--fg3)', flex: 1, minWidth: '120px' }}>
                {place || meter || '当前电表'}
              </span>
              <button
                type="button"
                className="hv-line-fg"
                onClick={() => setOpen(false)}
                style={{
                  margin: 0,
                  padding: '9px 18px',
                  borderRadius: '999px',
                  border: '1px solid var(--line)',
                  background: 'none',
                  color: 'var(--fg2)',
                  font: 'inherit',
                  fontSize: '12.5px',
                  cursor: 'pointer',
                }}
              >
                取消
              </button>
              <button
                type="button"
                className="hv-op82"
                onClick={run}
                disabled={busy}
                style={{
                  margin: 0,
                  padding: '9px 20px',
                  borderRadius: '999px',
                  border: '1px solid var(--fg)',
                  background: 'var(--fg)',
                  color: 'var(--bg)',
                  font: 'inherit',
                  fontSize: '12.5px',
                  fontWeight: 600,
                  cursor: busy ? 'wait' : 'pointer',
                  opacity: busy ? 0.6 : 1,
                }}
              >
                {busy ? '正在导出…' : '导出'}
              </button>
            </footer>
          </section>
        </div>,
        document.body,
      )}
    </>
  )
}
