/* 概览页最近推送列表与技术详情。
   默认最近 5 条。更早记录就地展开并按游标翻页。
   行详情含完整时间、状态码、渠道 id、失败原文与 JSON 复制。 */

import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { PushLog } from '../api/types'
import { presentPushLog, type PushLogTone } from '../lib/pushLogs'
import { CH_DEFS } from '../lib/channels'

/** 列表占位说明行。暂无渠道、加载中、读取失败。不可点开。 */
export interface PushHistoryNotice {
  id: string
  channel: string
  summary: string
  state: string
  tone: PushLogTone
}

const KIND_LABEL: Record<string, string> = {
  low_balance: '低额度预警',
  digest: '定时摘要',
  test: '渠道测试',
  system: '系统通知',
}

const STATUS_LABEL: Record<string, string> = {
  delivered: '已送达',
  accepted: '平台已接受',
  failed: '投递失败',
  skipped: '已跳过',
}

const mono = (fs = '9px', ls = '.16em') => ({
  font: `500 ${fs}/1 'JetBrains Mono',monospace`,
  letterSpacing: ls,
  color: 'var(--fg3)',
})

/** 完整到秒并写明时区。技术详情时间须可与服务端日志对齐。 */
function fullTime(value: string): string {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return '—'
  const parts = new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).formatToParts(date)
  const get = (type: Intl.DateTimeFormatPartTypes) => parts.find((part) => part.type === type)?.value || ''
  return `${get('year')}-${get('month')}-${get('day')} ${get('hour')}:${get('minute')}:${get('second')} (UTC+8)`
}

function toneColor(tone: PushLogTone, colors: { ok: string; error: string; muted: string }) {
  return tone === 'error' ? colors.error : tone === 'ok' ? colors.ok : colors.muted
}

function DetailRow({ label, en, children }: { label: string; en: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '7px', minWidth: 0 }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: '10px' }}>
        <span style={mono()}>{en}</span>
        <span style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>{label}</span>
      </div>
      <div
        style={{
          font: "400 12.5px/1.65 'JetBrains Mono',monospace",
          color: 'var(--fg)',
          overflowWrap: 'anywhere',
          whiteSpace: 'pre-wrap',
        }}
      >
        {children}
      </div>
    </div>
  )
}

function PushLogDetail({ log, onClose }: { log: PushLog; onClose: () => void }) {
  const closeButton = useRef<HTMLButtonElement>(null)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    closeButton.current?.focus()
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKeyDown)
    return () => {
      document.body.style.overflow = previousOverflow
      window.removeEventListener('keydown', onKeyDown)
    }
  }, [onClose])

  if (typeof document === 'undefined') return null

  const channelName = CH_DEFS.find((def) => def.id === log.channel)?.name
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(JSON.stringify(log, null, 2))
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      /* 剪贴板被浏览器拒绝时不弹错：下面的原始 JSON 本来就可以手动选中复制 */
    }
  }

  return createPortal(
    <div
      role="dialog"
      aria-modal="true"
      aria-label="推送记录技术详情"
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose()
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
          width: 'min(560px, 100%)',
          maxHeight: 'calc(100dvh - 32px)',
          display: 'flex',
          flexDirection: 'column',
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
            <div style={mono('9.5px', '.2em')}>PUSH LOG · DETAIL</div>
            <div style={{ fontSize: '19px', fontWeight: 600, letterSpacing: '-.03em' }}>
              {channelName || log.channel} · {KIND_LABEL[log.kind] || log.kind}
            </div>
          </div>
          <button
            ref={closeButton}
            type="button"
            aria-label="关闭技术详情"
            title="关闭"
            onClick={onClose}
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

        <div style={{ padding: '22px 20px', overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: '20px' }}>
          <DetailRow label="发送时间" en="SENT AT">
            {fullTime(log.sent_at)}
          </DetailRow>
          <DetailRow label="渠道" en="CHANNEL">
            {channelName ? `${channelName} · ${log.channel}` : log.channel}
          </DetailRow>
          <DetailRow label="消息类型" en="KIND">
            {KIND_LABEL[log.kind] ? `${KIND_LABEL[log.kind]} · ${log.kind}` : log.kind}
          </DetailRow>
          <DetailRow label="投递状态" en="STATUS">
            {STATUS_LABEL[log.status] ? `${STATUS_LABEL[log.status]} · ${log.status}` : log.status}
          </DetailRow>
          <DetailRow label="推送内容" en="SUMMARY">
            {log.summary || '（空）'}
          </DetailRow>
          {!!log.error && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '7px' }}>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: '10px' }}>
                <span style={mono()}>ERROR</span>
                <span style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>失败原因（服务端原文）</span>
              </div>
              <div
                style={{
                  padding: '11px 12px',
                  borderLeft: '2px solid var(--red)',
                  background: 'var(--redsoft)',
                  color: 'var(--red)',
                  font: "400 12px/1.7 'JetBrains Mono',monospace",
                  overflowWrap: 'anywhere',
                  whiteSpace: 'pre-wrap',
                }}
              >
                {log.error}
              </div>
            </div>
          )}
          <DetailRow label="记录 ID" en="LOG ID">
            {log.id}
          </DetailRow>
        </div>

        <footer
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '14px',
            padding: '16px 20px',
            borderTop: '1px solid var(--line2)',
            flexWrap: 'wrap',
          }}
        >
          <span style={{ fontSize: '11.5px', color: 'var(--fg3)', lineHeight: 1.6, flex: 1, minWidth: '160px' }}>
            反馈问题时把这份 JSON 一起贴上，能省掉一轮来回。
          </span>
          <button
            type="button"
            className="hv-line-fg"
            onClick={copy}
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
            {copied ? '✓ 已复制' : '复制 JSON'}
          </button>
        </footer>
      </section>
    </div>,
    document.body,
  )
}

export default function PushHistory({
  logs,
  notice,
  colors,
  collapsedCount = 5,
  hasMore = false,
  loadingMore = false,
  onLoadMore,
}: {
  logs: PushLog[]
  /** 没有任何记录时显示的说明行（未绑表 / 未开渠道 / 加载中 / 读取失败） */
  notice?: PushHistoryNotice | null
  colors: { ok: string; error: string; muted: string }
  collapsedCount?: number
  hasMore?: boolean
  loadingMore?: boolean
  onLoadMore?: () => void
}) {
  const [expanded, setExpanded] = useState(false)
  const [detail, setDetail] = useState<PushLog | null>(null)

  const shown = expanded ? logs : logs.slice(0, collapsedCount)
  const hidden = logs.length - shown.length
  /* 折起来时不显示「加载更多」：还没看完手里的就去要更多，纯属白跑一趟 */
  const canLoadMore = expanded && hasMore && !!onLoadMore

  const row = (
    key: string,
    time: string,
    channel: string,
    summary: string,
    state: string,
    tone: PushLogTone,
    log?: PushLog,
  ) => {
    const dot = toneColor(tone, colors)
    const inner = (
      <>
        <span style={{ width: '5px', height: '5px', flex: 'none', background: dot }} />
        <span style={{ font: "400 11.5px/1 'JetBrains Mono',monospace", color: 'var(--fg3)', flex: 'none' }}>{time}</span>
        <span style={{ fontSize: '12.5px', fontWeight: 500, flex: 'none', minWidth: '112px', textAlign: 'left' }}>
          {channel}
        </span>
        <span
          data-r="hidesm"
          style={{
            fontSize: '13px',
            color: 'var(--fg2)',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
            textAlign: 'left',
          }}
        >
          {summary}
        </span>
        <span
          title={state}
          style={{
            marginLeft: 'auto',
            fontSize: '11.5px',
            flex: 'none',
            color: dot,
            maxWidth: 'min(34vw, 320px)',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            whiteSpace: 'nowrap',
          }}
        >
          {state}
        </span>
      </>
    )
    const shared = {
      display: 'flex',
      alignItems: 'center',
      gap: '30px',
      padding: '16px 0',
      borderTop: '1px solid var(--line2)',
      width: '100%',
    } as const
    if (!log) {
      return (
        <div key={key} style={shared}>
          {inner}
        </div>
      )
    }
    return (
      <button
        key={key}
        type="button"
        className="hv-sub"
        title="查看技术详情"
        onClick={() => setDetail(log)}
        style={{
          ...shared,
          margin: 0,
          border: 0,
          borderTop: '1px solid var(--line2)',
          background: 'none',
          font: 'inherit',
          color: 'inherit',
          textAlign: 'left',
          cursor: 'pointer',
        }}
      >
        {inner}
      </button>
    )
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', marginTop: '22px' }}>
      {notice && row(notice.id, '—', notice.channel, notice.summary, notice.state, notice.tone)}
      {shown.map((log) => {
        const view = presentPushLog(log)
        return row(view.id, view.time, view.channel, view.summary, view.state, view.tone, log)
      })}

      {(logs.length > collapsedCount || canLoadMore || (expanded && logs.length > 0)) && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '14px',
            padding: '16px 0 0',
            borderTop: '1px solid var(--line2)',
            flexWrap: 'wrap',
          }}
        >
          <span style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>
            {`已显示 ${shown.length} 条${hasMore ? '' : ` · 共 ${logs.length} 条`}`}
          </span>
          {canLoadMore && (
            <button
              type="button"
              className="hv-line-fg"
              onClick={onLoadMore}
              disabled={loadingMore}
              style={{
                margin: 0,
                padding: '8px 16px',
                borderRadius: '999px',
                border: '1px solid var(--line)',
                background: 'none',
                color: 'var(--fg2)',
                font: 'inherit',
                fontSize: '12px',
                cursor: loadingMore ? 'wait' : 'pointer',
                opacity: loadingMore ? 0.6 : 1,
              }}
            >
              {loadingMore ? '加载中…' : '加载更多'}
            </button>
          )}
          {(hidden > 0 || expanded) && (
            <button
              type="button"
              className="hv-fg"
              onClick={() => setExpanded((value) => !value)}
              style={{
                margin: '0 0 0 auto',
                padding: 0,
                border: 0,
                background: 'none',
                font: 'inherit',
                fontSize: '12.5px',
                color: 'var(--fg2)',
                cursor: 'pointer',
              }}
            >
              {expanded ? '收起' : `查看更早的记录（还有 ${hidden} 条）`}
            </button>
          )}
        </div>
      )}

      {detail && <PushLogDetail log={detail} onClose={() => setDetail(null)} />}
    </div>
  )
}
