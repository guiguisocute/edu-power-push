/* 前端特性开关与远程配置。
   默认值对齐后端当前能力。live 启动时查询 frontend-config。
   请求失败时回退本地默认值。字段深合并，兼容新增键。 */

import { api, type OAuthProvider } from '../api/client'
import { DEFAULT_CHANNEL_CATEGORIES, type ChannelCategory } from '../lib/channels'
import type { Semester } from '../lib/semesters'

export interface AuthFeatures {
  /** 邮箱与密码登录 */
  emailLogin: boolean
  /** 手机号与密码登录。后端无短信认证，默认关闭。 */
  smsLogin: boolean
  /** 找回密码使用邮箱验证码。端点可用，默认开启。 */
  emailCode: boolean
  /** 找回密码使用短信验证码。后端无此能力，默认关闭。 */
  smsCode: boolean
  /** 开放注册入口 */
  registration: boolean
  /** Google 登录入口。运维面板开关；可用性另查 oauth.google */
  googleOAuth: boolean
  /** GitHub 登录入口。运维面板开关；可用性另查 oauth.github */
  githubOAuth: boolean
}

/** 服务端是否配置第三方登录 client id/secret。只读。 */
export interface OAuthAvailability {
  google: boolean
  github: boolean
}

export type { OAuthProvider } from '../api/client'

/** 图表与分析能力开关。取决于上游数据。运维面板可改。 */
export interface ChartFeatures {
  /**
   * 日视图（24 小时负荷）。
   * 本校上游无可靠小时数据，默认关闭。
   * 有分时上游时可在 frontend-config 打开。
   */
  dayRange: boolean
  /**
   * 用电分析分时热力图与时段画像。
   * 默认关闭。代码归档在 views/archive，打开后挂回。
   */
  hourlyUsage: boolean
}

export interface Features {
  auth: AuthFeatures
  /* 第三方登录服务端能力。后端如实下发。
     拉不到配置时默认两项为 true。按钮可见；点击返回明确失败。 */
  oauth: OAuthAvailability
  /** 推送渠道可见性。键为 CH_DEFS id。缺省为显示。 */
  channels: Record<string, boolean>
  /** true = 展示但禁用并标 Coming Soon。false/缺省由代码能力决定。 */
  channelComingSoon: Record<string, boolean>
  /** 用户端渠道卡片顺序。缺少的渠道按代码默认顺序补到末尾。 */
  channelOrder: string[]
  /** 渠道列表分组。空数组 = 平铺展示。组内顺序看 channelOrder。 */
  channelCategories: ChannelCategory[]
  charts: ChartFeatures
  display: {
    /** 电价（元/kWh），十进制字符串。后端确认前沿用原型展示值。 */
    electricityRate: string
    campusName: string
    areaName: string
    /* 站点对外名称：侧栏抬头、页面标题、邮件页脚。
       与 areaName 分开。空串使用 DEFAULT_BRAND_NAME。 */
    brandName: string
    /** 日/周/月榜统一切换时刻。周榜固定周一，月榜固定每月 1 日。 */
    rankingRefreshTime: string
    /* 校历。学期视图横轴完全由它算出。
       空数组时视图退回近 18 周滚动窗口。 */
    semesters: Semester[]
  }
}

/** 中性兜底名称。未配置面板时使用，不指向任何学校。 */
export const DEFAULT_BRAND_NAME = 'POWER·PUSH'
export const DEFAULT_CAMPUS_NAME = '主校区'

export const DEFAULT_FEATURES: Features = {
  auth: {
    emailLogin: true,
    smsLogin: false,
    emailCode: true,
    smsCode: false,
    registration: true,
    googleOAuth: true,
    githubOAuth: true,
  },
  oauth: { google: true, github: true },
  channels: {},
  channelComingSoon: { sms: true },
  channelOrder: [],
  channelCategories: DEFAULT_CHANNEL_CATEGORIES,
  charts: {
    dayRange: false,
    hourlyUsage: false,
  },
  display: {
    electricityRate: '0.62',
    campusName: DEFAULT_CAMPUS_NAME,
    // 学校由运维面板选择。前端不预设学校。
    areaName: '',
    brandName: DEFAULT_BRAND_NAME,
    rankingRefreshTime: '09:00',
    // 校历无默认值。错误默认值比空值更差。
    semesters: [],
  },
}

/*
登录页第三方入口。两层都必须为真：
面板开关与服务端能力（client id/secret）。
仅看开关会画出必然失败的按钮。
*/
export function enabledOAuthProviders(f: Features): OAuthProvider[] {
  const providers: OAuthProvider[] = []
  if (f.auth.googleOAuth && f.oauth.google) providers.push('google')
  if (f.auth.githubOAuth && f.oauth.github) providers.push('github')
  return providers
}

/** 登录账号字段文案。按开关组合返回。 */
export function accountField(a: AuthFeatures): { label: string; ph: string } {
  if (a.smsLogin && a.emailLogin) return { label: 'ACCOUNT', ph: '手机号 / 邮箱' }
  if (a.smsLogin) return { label: 'PHONE', ph: '11 位手机号' }
  return { label: 'EMAIL', ph: '邮箱地址' }
}

/** 账号脱敏。手机号 138****6021；邮箱 z***@qq.com。 */
export function maskAccount(acc: string): string {
  if (/^1\d{10}$/.test(acc)) return acc.slice(0, 3) + '****' + acc.slice(-4)
  const at = acc.indexOf('@')
  if (at > 0) return acc.slice(0, 1) + '***' + acc.slice(at)
  return acc.slice(0, 2) + '****'
}

export function isValidAccount(a: AuthFeatures, acc: string): { ok: boolean; msg: string } {
  const v = acc.trim()
  if (a.smsLogin && /^1\d{10}$/.test(v)) return { ok: true, msg: '' }
  if (a.emailLogin && /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(v)) return { ok: true, msg: '' }
  if (a.smsLogin && a.emailLogin) return { ok: false, msg: '请输入 11 位手机号或有效邮箱' }
  if (a.smsLogin) return { ok: false, msg: '请输入 11 位手机号' }
  return { ok: false, msg: '请输入有效的邮箱地址' }
}

function isObj(x: unknown): x is Record<string, unknown> {
  return !!x && typeof x === 'object' && !Array.isArray(x)
}

function deepMerge<T>(base: T, over: unknown): T {
  if (!isObj(over)) return base
  const out: Record<string, unknown> = { ...(base as Record<string, unknown>) }
  for (const [k, v] of Object.entries(over)) {
    const b = (base as Record<string, unknown>)[k]
    out[k] = isObj(b) && isObj(v) ? deepMerge(b, v) : v !== undefined ? v : b
  }
  return out as T
}

/** 远端键为 snake_case。归一化到本地驼峰。 */
function normalizeRemote(raw: unknown): unknown {
  if (!isObj(raw)) return raw
  const auth = isObj(raw.features) && isObj((raw.features as Record<string, unknown>).auth)
    ? ((raw.features as Record<string, unknown>).auth as Record<string, unknown>)
    : undefined
  const channels = isObj(raw.features) ? (raw.features as Record<string, unknown>).channels : undefined
  const channelComingSoon = isObj(raw.features)
    ? (raw.features as Record<string, unknown>).channel_coming_soon
    : undefined
  const channelOrder = isObj(raw.features) ? (raw.features as Record<string, unknown>).channel_order : undefined
  const channelCategories = isObj(raw.features)
    ? (raw.features as Record<string, unknown>).channel_categories
    : undefined
  const chartsRaw = isObj(raw.features) ? (raw.features as Record<string, unknown>).charts : undefined
  const charts = isObj(chartsRaw) ? chartsRaw : undefined
  const display = isObj(raw.display) ? (raw.display as Record<string, unknown>) : undefined
  const oauth = isObj(raw.oauth) ? (raw.oauth as Record<string, unknown>) : undefined
  const pick = (o: Record<string, unknown> | undefined, snake: string, camel: string) =>
    o ? (o[snake] !== undefined ? o[snake] : o[camel]) : undefined
  return {
    auth: auth
      ? {
          emailLogin: pick(auth, 'email_login', 'emailLogin'),
          smsLogin: pick(auth, 'sms_login', 'smsLogin'),
          emailCode: pick(auth, 'email_code', 'emailCode'),
          smsCode: pick(auth, 'sms_code', 'smsCode'),
          registration: auth.registration,
          googleOAuth: pick(auth, 'google_oauth', 'googleOAuth'),
          githubOAuth: pick(auth, 'github_oauth', 'githubOAuth'),
        }
      : undefined,
    oauth: oauth ? { google: oauth.google, github: oauth.github } : undefined,
    channels,
    channelComingSoon,
    channelOrder: Array.isArray(channelOrder) ? channelOrder : undefined,
    /* 数组整份替换。[] 表示清空，禁止深合并回落到内置默认值。 */
    channelCategories: Array.isArray(channelCategories) ? (channelCategories as ChannelCategory[]) : undefined,
    charts: charts
      ? {
          dayRange: pick(charts, 'day_range', 'dayRange'),
          hourlyUsage: pick(charts, 'hourly_usage', 'hourlyUsage'),
        }
      : undefined,
    display: display
      ? {
          electricityRate: pick(display, 'electricity_rate', 'electricityRate'),
          campusName: pick(display, 'campus_name', 'campusName'),
          areaName: pick(display, 'area_name', 'areaName'),
          brandName: pick(display, 'brand_name', 'brandName'),
          rankingRefreshTime: pick(display, 'ranking_refresh_time', 'rankingRefreshTime'),
          // 数组整份替换。以后端为准，禁止对象合并。
          semesters: Array.isArray(display.semesters) ? display.semesters : undefined,
        }
      : undefined,
  }
}

/** 查询远程配置。404 与网络失败一律回退默认值。 */
export async function fetchRemoteFeatures(): Promise<Features> {
  try {
    const raw = await api.frontendConfig()
    return deepMerge(DEFAULT_FEATURES, normalizeRemote(raw))
  } catch {
    return DEFAULT_FEATURES
  }
}
