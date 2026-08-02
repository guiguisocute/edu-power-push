/* 推送渠道与规则的前后端映射。
   live 登录后查询一次，改动时 PUT。mock 纯本地。
   各渠道可真实投递。每个用户仅管理自己的凭据。 */

import { api } from './client'
import { IS_LIVE } from './mode'
import { DEFAULT_CH, type ChConfig } from '../lib/channels'
import type {
  ChannelList,
  ChannelTestResult,
  NotificationChannel,
  NotificationSettings,
} from './types'

export interface LocalNotifSettings {
  lowAlert: boolean
  threshold: number
  schedule: boolean
  period: string
  pushTime: string
  notificationTemplates: LocalNotificationTemplates
}

export interface LocalMessageTemplate {
  title: string
  body: string
}

export interface LocalNotificationTemplates {
  lowBalance: LocalMessageTemplate
  digest: LocalMessageTemplate
}

export function defaultNotificationTemplates(): LocalNotificationTemplates {
  return {
    lowBalance: {
      title: '低额度预警',
      body: [
        '当前余额：{{.balance_yuan}} 元（提醒阈值：{{.threshold_yuan}} 元）',
        '电表号：{{.meter_number}}',
        '宿舍楼栋：{{.building}}',
        '楼层：{{.floor}}',
        '房间号：{{.room}}',
        '请及时充值，余额不足会影响用电。',
      ].join('\n'),
    },
    digest: {
      title: '用电摘要',
      body: [
        '统计周期：{{.period}}',
        '本周期用电：{{.usage_kwh}} 度',
        '当前余额：{{.balance_yuan}} 元',
        '电表号：{{.meter_number}}',
        '宿舍楼栋：{{.building}}',
        '楼层：{{.floor}}',
        '房间号：{{.room}}',
      ].join('\n'),
    },
  }
}

export function normalizeNotificationTemplates(value?: Partial<LocalNotificationTemplates> | null): LocalNotificationTemplates {
  const defaults = defaultNotificationTemplates()
  const lowBalance = value?.lowBalance
  const digest = value?.digest
  return {
    lowBalance: {
      title: lowBalance?.title?.trim() ? lowBalance.title : defaults.lowBalance.title,
      body: lowBalance?.body?.trim() ? lowBalance.body : defaults.lowBalance.body,
    },
    digest: {
      title: digest?.title?.trim() ? digest.title : defaults.digest.title,
      body: digest?.body?.trim() ? digest.body : defaults.digest.body,
    },
  }
}

const clampThreshold = (value: number) => Math.min(50, Math.max(1, value))

export function settingsToLocal(s: NotificationSettings): LocalNotifSettings {
  const th = Number(s.threshold_yuan)
  const templates = s.templates
  return {
    lowAlert: s.low_balance_alert,
    threshold: Number.isFinite(th) ? clampThreshold(th) : 10,
    schedule: s.scheduled_digest,
    period: s.period,
    pushTime: s.push_time,
    notificationTemplates: normalizeNotificationTemplates({
      lowBalance: templates?.low_balance,
      digest: templates?.digest,
    }),
  }
}

export function settingsToRemote(local: LocalNotifSettings): NotificationSettings {
  return {
    low_balance_alert: local.lowAlert,
    threshold_yuan: String(clampThreshold(local.threshold)),
    scheduled_digest: local.schedule,
    period: local.period as NotificationSettings['period'],
    push_time: local.pushTime,
    timezone: 'Asia/Shanghai',
    templates: {
      low_balance: { ...local.notificationTemplates.lowBalance },
      digest: { ...local.notificationTemplates.digest },
    },
  }
}

/** 后端 Channel 映射为前端 ch[id]（on + 字段）。 */
export function channelToLocal(ch: NotificationChannel): ChConfig {
  const cfg: ChConfig = { on: ch.enabled, ...(ch.config || {}) }
  // 敏感字段：后端回掩码或 null。secret_set 为真时用占位，提交时省略以保留原值。
	for (const [k, set] of Object.entries(ch.secret_set || {})) {
		if (set) {
			cfg[k] = '••••••••'
      cfg[`__secret_${k}`] = true
    }
  }
  return cfg
}

/** 前端 ch 映射为后端 PUT body。掩码占位不回传。省略 = 保留服务端原值。 */
export function channelToRemote(local: ChConfig): { enabled: boolean; config: Record<string, unknown> } {
  const config: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(local)) {
    if (k === 'on' || k.startsWith('__secret_')) continue
    if (local[`__secret_${k}`] && (v === '••••••••' || v === '')) continue
    config[k] = v
  }
  return { enabled: !!local.on, config }
}

/** 合并服务端渠道与本地默认。未配置渠道用 DEFAULT_CH。mail 优先账号邮箱。 */
export function mergeChannels(
  remote: NotificationChannel[],
  accountEmail?: string | null,
): Record<string, ChConfig> {
  const base: Record<string, ChConfig> = {}
  for (const [id, def] of Object.entries(DEFAULT_CH)) {
    base[id] = { ...def, on: false }
    // live 默认禁止演示收件人
    if (id === 'mail') {
      base[id] = { on: false, to: accountEmail ? [accountEmail] : [''] }
    } else if (id === 'sms') {
      base[id] = { on: false, sms: [''] }
    } else {
      // 清空演示凭证
      const cleaned: ChConfig = { on: false }
      for (const k of Object.keys(def)) {
        if (k === 'on') continue
        cleaned[k] = typeof def[k] === 'string' ? '' : Array.isArray(def[k]) ? [''] : def[k]
      }
      base[id] = cleaned
    }
  }
  for (const ch of remote) {
    base[ch.channel] = channelToLocal(ch)
  }
  // 从未配置 mail：用账号邮箱作默认收件人。
  if (!remote.some((c) => c.channel === 'mail') && accountEmail) {
    base.mail = { on: true, to: [accountEmail] }
  }
  return base
}

export async function loadNotificationSettings(): Promise<LocalNotifSettings | null> {
  if (!IS_LIVE) return null
  try {
    return settingsToLocal(await api.notificationSettings())
  } catch {
    return null
  }
}

export async function saveNotificationSettings(local: LocalNotifSettings): Promise<LocalNotifSettings | null> {
  if (!IS_LIVE) return null
  try {
    return settingsToLocal(await api.saveNotificationSettings(settingsToRemote(local)))
  } catch {
    return null
  }
}

export async function loadChannels(accountEmail?: string | null): Promise<Record<string, ChConfig> | null> {
  if (!IS_LIVE) return null
  try {
    const list: ChannelList = await api.channels()
    return mergeChannels(list.channels || [], accountEmail)
  } catch {
    return null
  }
}

export async function saveAllChannels(ch: Record<string, ChConfig>): Promise<Record<string, ChConfig> | null> {
  if (!IS_LIVE) return null
  try {
    const channels = Object.entries(ch).map(([id, local]) => ({
      channel: id,
      ...channelToRemote(local),
    }))
    const list = await api.saveChannels({ channels })
    return mergeChannels(list.channels || [])
  } catch {
    return null
  }
}

export async function saveOneChannel(id: string, local: ChConfig): Promise<ChConfig | null> {
  if (!IS_LIVE) return null
  const saved = await api.saveChannel(id, channelToRemote(local))
  return channelToLocal(saved)
}

export async function testChannel(id: string): Promise<ChannelTestResult | null> {
  if (!IS_LIVE) return null
  try {
    return await api.testChannel(id)
  } catch (e) {
    throw e
  }
}
