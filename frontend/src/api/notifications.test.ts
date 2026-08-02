import {
  defaultNotificationTemplates,
  settingsToLocal,
  settingsToRemote,
} from './notifications'

function assert(ok: unknown, message: string): asserts ok {
  if (!ok) throw new Error(message)
}

export function runNotificationSettingsTests() {
  const local = settingsToLocal({
    low_balance_alert: true,
    threshold_yuan: '10',
    scheduled_digest: true,
    period: 'daily',
    push_time: '08:00',
    templates: {
      low_balance: { title: '余额 {{.balance_yuan}}', body: '电表 {{.meter_number}}' },
      digest: { title: '摘要', body: '{{.period}} / {{.usage_kwh}}' },
    },
  })
  assert(local.notificationTemplates.lowBalance.title === '余额 {{.balance_yuan}}', '低额模板未映射到本地状态')
  assert(local.notificationTemplates.digest.body === '{{.period}} / {{.usage_kwh}}', '摘要模板未映射到本地状态')

  const remote = settingsToRemote(local)
  assert(remote.templates?.low_balance?.body === '电表 {{.meter_number}}', '低额模板未写回 API 请求')
  assert(remote.templates?.digest?.title === '摘要', '摘要模板未写回 API 请求')

  const defaults = settingsToLocal({
    low_balance_alert: true,
    threshold_yuan: '10',
    scheduled_digest: true,
    period: 'daily',
    push_time: '08:00',
  }).notificationTemplates
  assert(defaults.lowBalance.body === defaultNotificationTemplates().lowBalance.body, '旧版响应未补默认模板')
}
