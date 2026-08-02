/* 概览手动刷新。真实取数，非装饰。
   live 先 POST /me/refresh，再清缓存重取 hooks。
   旧环境 404/405/501 时降级为清缓存重取。
   30s 冷却。右侧显示上次取数时间。 */

import { useEffect, useState, type CSSProperties } from 'react'
import { REFRESH_CD_MS, refreshLive, useRefreshState } from '../api/live'
import { IS_LIVE } from '../api/mode'

const pad = (n: number) => (n < 10 ? '0' + n : String(n))
const clock = (t: number) => {
  const d = new Date(t)
  return pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds())
}

export default function RefreshButton({
  meter,
  loading = false,
  style,
}: {
  meter?: string
  loading?: boolean
  style?: CSSProperties
}) {
  const r = useRefreshState()
  const [now, setNow] = useState(Date.now())

  // 冷却期间每 250ms 重渲染，驱动倒计时与进度线。
  useEffect(() => {
    if (r.until <= Date.now()) return
    const t = setInterval(() => {
      setNow(Date.now())
      if (Date.now() >= r.until) clearInterval(t)
    }, 250)
    return () => clearInterval(t)
  }, [r.until])

  const busy = loading || r.pending
  const left = Math.max(0, Math.ceil((r.until - now) / 1000))
  const cooling = left > 0
  const dis = cooling || busy
  const pct = cooling ? Math.min(100, ((r.until - now) / REFRESH_CD_MS) * 100) : 0
  const title = busy
    ? '正在取数…'
    : cooling
      ? `冷却中 · ${left} 秒后可再次刷新`
      : IS_LIVE
        ? '向后端请求一次最新读数'
        : '重新载入演示数据。mock 模式无网络请求。'

  return (
    /* 换行判据为 min-content（约按钮宽），不是整块宽。
       按钮须贴着余额大数字。宽度不足时「更新于」折到按钮下。 */
    <div
      style={{
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'flex-end',
        gap: '11px',
        rowGap: '6px',
        flexWrap: 'wrap',
        flexGrow: 1,
        flexShrink: 1,
        flexBasis: 'min-content',
        maxWidth: 'max-content',
        ...style,
      }}
    >
      <button
        className={dis ? undefined : 'hv-line-fg'}
        onClick={() => {
          if (!dis) void refreshLive(meter)
        }}
        disabled={dis}
        title={title}
        style={{
          position: 'relative',
          overflow: 'hidden',
          display: 'flex',
          alignItems: 'center',
          gap: '7px',
          background: 'none',
          border: '1px solid var(--line)',
          borderRadius: '999px',
          margin: 0,
          padding: '6px 14px',
          font: 'inherit',
          fontSize: '12px',
          color: 'var(--fg2)',
          cursor: dis ? 'default' : 'pointer',
          opacity: dis ? 0.62 : 1,
          flex: 'none',
          transition: 'opacity .2s, border-color .2s, color .2s',
        }}
      >
        <svg
          className={busy ? 'spin' : undefined}
          viewBox="0 0 24 24"
          width="11"
          height="11"
          fill="none"
          stroke="currentColor"
          strokeWidth="2.2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
          style={{ flex: 'none' }}
        >
          <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
          <polyline points="23 4 23 10 17 10" />
        </svg>
        {/* 三个状态的字宽不一样（刷新 / 刷新中 / 30s），不钉死宽度的话按钮会随状态胀缩，
            右边的「更新于」跟着左右滑，卡在换行临界点上时整块还会来回跳行。 */}
        <span
          style={{
            // 38px = 最宽的「刷新中」（12px 三个汉字 = 36px）再留一点余量
            width: '38px',
            flex: 'none',
            textAlign: 'center',
            fontSize: '12px',
            // 行高钉死：倒计时用等宽体、其余用正文字体，两者的默认行高不一样，按钮会一高一矮
            lineHeight: 1,
            fontWeight: cooling ? 500 : 400,
            fontFamily: cooling ? "'JetBrains Mono',monospace" : 'inherit',
            fontVariantNumeric: 'tabular-nums',
          }}
        >
          {busy ? '刷新中' : cooling ? `${left}s` : '刷新'}
        </span>
        {cooling && (
          <span
            style={{
              position: 'absolute',
              left: 0,
              bottom: 0,
              height: '1.5px',
              width: pct + '%',
              background: 'var(--fg3)',
              transition: 'width .25s linear',
            }}
          />
        )}
      </button>
      <span
        style={{
          font: "400 10.5px/1 'JetBrains Mono',monospace",
          color: 'var(--fg3)',
          fontVariantNumeric: 'tabular-nums',
          whiteSpace: 'nowrap',
        }}
        title={new Date(r.at).toLocaleString('zh-CN')}
      >
        更新于 {clock(r.at)}
      </span>
      {r.error && <span style={{ fontSize: '11.5px', color: 'var(--red)' }}>{r.error}</span>}
    </div>
  )
}
