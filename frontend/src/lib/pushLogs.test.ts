import { formatPushLogTime, presentPushLog } from './pushLogs'

function assert(cond: unknown, msg: string) {
  if (!cond) throw new Error(msg)
}

export function runPushLogTests() {
  assert(formatPushLogTime('2026-07-25T09:42:10Z') === '07-25 17:42', '推送时间必须固定换算为 Asia/Shanghai')
  assert(formatPushLogTime('not-a-date') === '—', '非法时间不应渲染 Invalid Date')

  const base = {
    id: 'delivered',
    sent_at: '2026-07-25T17:42:10+08:00',
    channel: 'telegram',
    kind: 'test' as const,
    status: 'delivered' as const,
    summary: '测试推送 · Telegram Bot',
  }
  const delivered = presentPushLog(base)
  assert(delivered.channel === 'Telegram Bot', '渠道代码必须显示为用户可读名称')
  assert(delivered.state === '已送达' && delivered.tone === 'ok', '成功投递状态映射错误')

  const accepted = presentPushLog({ ...base, id: 'accepted', channel: 'pushplus', status: 'accepted' })
  assert(accepted.channel === 'PushPlus' && accepted.state === '平台已接受', 'PushPlus accepted 状态映射错误')

  const failed = presentPushLog({ ...base, id: 'failed', status: 'failed', error: '上游超时' })
  assert(failed.state === '失败 · 上游超时' && failed.tone === 'error', '失败原因没有真实展示')

  const skipped = presentPushLog({ ...base, id: 'skipped', status: 'skipped' })
  assert(skipped.state === '已跳过' && skipped.tone === 'muted', '跳过状态映射错误')

  const unknown = presentPushLog({ ...base, id: 'unknown', channel: 'custom' })
  assert(unknown.channel === 'custom', '未知渠道不应被错误冒充成已有渠道')
}
