import type { PushLog } from '../api/types'
import { CH_DEFS } from './channels'

export type PushLogTone = 'ok' | 'error' | 'muted'

export interface PushLogPresentation {
  id: string
  time: string
  channel: string
  summary: string
  state: string
  tone: PushLogTone
}

/** 后端时间统一按学校时区显示。避免浏览器时区导致日期串台。 */
export function formatPushLogTime(value: string): string {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return '—'
  const parts = new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).formatToParts(date)
  const get = (type: Intl.DateTimeFormatPartTypes) => parts.find((part) => part.type === type)?.value || '00'
  return `${get('month')}-${get('day')} ${get('hour')}:${get('minute')}`
}

export function presentPushLog(log: PushLog): PushLogPresentation {
  const channel = CH_DEFS.find((def) => def.id === log.channel)?.name || (log.channel === 'system' ? '系统' : log.channel)
  if (log.status === 'failed') {
    return {
      id: log.id,
      time: formatPushLogTime(log.sent_at),
      channel,
      summary: log.summary,
      state: log.error ? `失败 · ${log.error}` : '失败',
      tone: 'error',
    }
  }
  if (log.status === 'skipped') {
    return {
      id: log.id,
      time: formatPushLogTime(log.sent_at),
      channel,
      summary: log.summary,
      state: log.error ? `已跳过 · ${log.error}` : '已跳过',
      tone: 'muted',
    }
  }
  return {
    id: log.id,
    time: formatPushLogTime(log.sent_at),
    channel,
    summary: log.summary,
    state: log.status === 'accepted' ? '平台已接受' : '已送达',
    tone: 'ok',
  }
}
