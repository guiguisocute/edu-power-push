/* 推送渠道定义。移植自原型 CH_DEFS / DEFAULT_CH / PERIODS。 */

export interface RcptDef {
  k: string
  en: string
  note: string
  ph: string
  t: string
  add: string
}

export interface FieldDef {
  k: string
  l: string
  en: string
  ph: string
  t?: string
  kind?: 'napcat-target'
  span: number
}

export interface ChDef {
  id: string
  name: string
  en: string
  initial: string
  icon: string
  kind?: 'rcpt'
  rcpt?: RcptDef[]
  hint: string
  /** 可选协议说明。供 Webhook 类渠道向接收方公开请求契约。 */
  protocol?: string
  fields: FieldDef[]
  /**
   * 本站已接通投递。false = 管理端禁止标为可用。
   * 此为代码能力上限。运维仍可将已接通渠道标为 Coming Soon 或隐藏。
   */
  ready: boolean
}

export const CH_DEFS: ChDef[] = [
  {
    id: 'mail',
    name: '邮箱',
    en: 'EMAIL',
    initial: '@',
    icon: '/icons/mail-fill.svg',
    kind: 'rcpt',
    rcpt: [{ k: 'to', en: 'RECIPIENT EMAIL', note: '最多 5 个 · 每个地址均须验证', ph: 'you@qq.com', t: 'email', add: '+ 验证并添加邮箱' }],
    hint: '登录邮箱已在注册或换绑时验证。其它收件邮箱须先接收验证码。修改地址时先移除旧地址，再验证新地址。',
    fields: [],
    ready: true,
  },
  {
    id: 'sms',
    name: '短信',
    en: 'SMS',
    initial: '信',
    icon: '/icons/message-2-fill.svg',
    kind: 'rcpt',
    rcpt: [{ k: 'sms', en: 'RECIPIENT PHONE', note: '只需收件号码，可添加多个', ph: '13800000000', t: 'tel', add: '+ 添加手机号' }],
    hint: '仅用于余额预警。定时摘要使用邮箱。可减少短信条数。',
    fields: [],
    ready: false,
  },
  {
    id: 'dingtalk',
    name: '钉钉群机器人',
    en: 'DINGTALK',
    initial: '钉',
    icon: '/icons/dingding-fill.svg',
    hint: '安全设置需选择「加签」，并把关键词留空。',
    fields: [
      { k: 'webhook', l: 'Webhook 地址', en: 'WEBHOOK', ph: 'https://oapi.dingtalk.com/robot/send?access_token=', span: 2 },
      { k: 'secret', l: '加签密钥', en: 'SECRET', t: 'password', ph: 'SEC••••••••', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'lark',
    name: 'Lark 机器人',
    en: 'LARK',
    initial: 'L',
    icon: '/icons/feishu-lark.svg',
    hint: '在 Lark 群中添加 Custom Bot，复制 Webhook 并开启 Signature verification。',
    fields: [
      { k: 'webhook', l: 'Webhook 地址', en: 'WEBHOOK', ph: 'https://open.larksuite.com/open-apis/bot/v2/hook/', span: 2 },
      { k: 'secret', l: '签名校验密钥', en: 'SIGN', t: 'password', ph: '••••••••', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'wecom',
    name: '企业微信智能机器人',
    en: 'WECOM',
    initial: '企',
    icon: '/icons/wechat-fill.svg',
    hint: '使用企业微信智能机器人长连接。必须填写 Bot ID 与 Secret。再填写单聊 userid 或群聊 chatid。',
    fields: [
      { k: 'botid', l: 'Bot ID', en: 'BOT ID', ph: '企业微信后台的机器人 ID', span: 1 },
      { k: 'secret', l: 'Bot Secret', en: 'SECRET', t: 'password', ph: '••••••••', span: 1 },
      { k: 'chatid', l: '接收会话 ID', en: 'USERID / CHATID', ph: '单聊 userid 或群聊 chatid', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'wecom_webhook',
    name: '企业微信消息推送',
    en: 'WECOM WEBHOOK',
    initial: '企',
    icon: '/icons/wechat-fill.svg',
    hint: '在企业微信群消息推送配置中复制完整 Webhook。Webhook 是凭证。禁止公开。',
    fields: [
      { k: 'webhook', l: 'Webhook 地址', en: 'WEBHOOK', t: 'password', ph: 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'feishu',
    name: '飞书机器人',
    en: 'FEISHU',
    initial: '飞',
    icon: '/icons/feishu-lark.svg',
    hint: '开启签名校验后必须填写密钥。否则请求会被拒绝。',
    fields: [
      { k: 'webhook', l: 'Webhook 地址', en: 'WEBHOOK', ph: 'https://open.feishu.cn/open-apis/bot/v2/hook/', span: 2 },
      { k: 'secret', l: '签名校验密钥', en: 'SIGN', t: 'password', ph: '••••••••', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'discord',
    name: 'Discord 群组 Webhook',
    en: 'DISCORD WEBHOOK',
    initial: 'D',
    icon: '/icons/discord-fill.svg',
    hint: '在 Discord 频道 Integrations → Webhooks 中复制 Webhook URL。无需创建 Bot。无需填写 App 凭证。',
    fields: [
      { k: 'webhook', l: 'Webhook 地址', en: 'WEBHOOK', t: 'password', ph: 'https://discord.com/api/webhooks/', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'webhook',
    name: 'Webhook',
    en: 'GENERIC WEBHOOK',
    initial: 'W',
    icon: '/icons/webhook-fill.svg',
    hint: '服务端以 POST application/json 调用公开 HTTPS 地址。如果填写接口凭证，附带 Authorization: Bearer <凭证>。禁止本机、内网、链路本地地址。禁止跳转。',
    protocol: `{
  "event": "low_balance_alert | scheduled_digest | test",
  "title": "通知标题",
  "message": "完整文本",
  "sent_at": "RFC 3339 时间",
  "meter": { "number": "电表号", "building": "楼栋", "floor": "楼层", "room": "房间" },
  "data": { "balance_yuan": "余额", "threshold_yuan": "阈值", "usage_kwh": "用电量", "period": "统计周期" }
}`,
    fields: [
      { k: 'webhook', l: 'Webhook 地址', en: 'WEBHOOK URL', t: 'password', ph: 'https://notify.example.com/hooks/power', span: 2 },
      { k: 'token', l: '接口凭证（可选）', en: 'BEARER TOKEN', t: 'password', ph: '可选 Bearer Token', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'bark',
    name: 'Bark',
    en: 'BARK',
    initial: 'B',
    icon: '/icons/notification-4-fill.svg',
    hint: '支持 Bark 官方服务与自建服务。应用按官方协议 POST /push。仅发送 Device Key、标题和正文。',
    fields: [
      { k: 'base_url', l: '服务地址', en: 'SERVICE URL', ph: 'https://api.day.app', span: 2 },
      { k: 'device_key', l: 'Device Key', en: 'DEVICE KEY', t: 'password', ph: 'Bark App 中的推送 Key', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'gotify',
    name: 'Gotify',
    en: 'GOTIFY',
    initial: 'G',
    icon: '/icons/notification-badge-fill.svg',
    hint: '连接你部署的 Gotify 服务。应用令牌通过 X-Gotify-Key 发送。优先级留空时使用默认值。也可填写任意整数。',
    fields: [
      { k: 'base_url', l: '服务地址', en: 'SERVICE URL', ph: 'https://push.example.com', span: 2 },
      { k: 'token', l: '应用令牌', en: 'APPLICATION TOKEN', t: 'password', ph: 'Gotify Application Token', span: 2 },
      { k: 'priority', l: '消息优先级（可选）', en: 'PRIORITY', t: 'number', ph: '例如 5', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'whatsapp',
    name: 'WhatsApp (CallMeBot)',
    en: 'WHATSAPP · CALLMEBOT',
    initial: 'WA',
    icon: '/icons/whatsapp-fill.svg',
    hint: '适用于 CallMeBot 个人用途 API。先在 WhatsApp 中完成 API 激活。再粘贴包含 phone 与 apikey 的完整 URL。应用自动填入每次推送的 text。',
    fields: [
      { k: 'webhook', l: 'API URL', en: 'CALLMEBOT URL', t: 'password', ph: 'https://api.callmebot.com/whatsapp.php?phone=...&apikey=...', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'pushplus',
    name: 'PushPlus',
    en: 'PUSHPLUS',
    initial: 'P+',
    icon: '/icons/notification-3-fill.svg',
    hint: '使用你自己的用户 Token 或消息 Token。不收集 SecretKey。不开放短信、语音或群发。',
    fields: [
      { k: 'token', l: 'Token', en: 'TOKEN', t: 'password', ph: '32 位字符', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'mp',
    name: '微信公众号测试号',
    en: 'WECHAT MP',
    initial: '微',
    icon: '/icons/wechat-2-fill.svg',
    hint: '在测试号后台创建模板。模板须包含 {{title.DATA}} 与 {{content.DATA}}。填写关注者 OpenID。AppID 与 AppSecret 仅用于鉴权。不能确定接收人和消息模板。',
    fields: [
      { k: 'appid', l: 'AppID', en: 'APPID', ph: 'wx••••••••', span: 1 },
      { k: 'secret', l: 'AppSecret', en: 'SECRET', t: 'password', ph: '••••••••', span: 1 },
      { k: 'tpl', l: '模板 ID', en: 'TEMPLATE', ph: '模板消息 ID', span: 1 },
      { k: 'openid', l: '接收 OpenID', en: 'OPENID', ph: 'o••••••••', span: 1 },
    ],
    ready: true,
  },
  {
    id: 'serverchan_turbo',
    name: 'Server酱Turbo',
    en: 'SERVERCHAN TURBO',
    initial: 'S',
    icon: '/icons/send-plane-fill.svg',
    hint: '推送到 Server酱Turbo 已配置的通道。填写 SCT 开头的 SendKey。',
    fields: [
      { k: 'sendkey', l: 'SendKey', en: 'SENDKEY', t: 'password', ph: 'SCT••••••••', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'serverchan3',
    name: 'Server酱³',
    en: 'SERVERCHAN 3',
    initial: 'S³',
    icon: '/icons/send-plane-fill.svg',
    hint: '推送到 Server酱³ App。应用从 SendKey 自动识别服务节点。无需填写 UID 或接口地址。',
    fields: [
      { k: 'sendkey', l: 'SendKey', en: 'SENDKEY', t: 'password', ph: 'sctp123t••••••••', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'qq',
    name: 'QQ 官方机器人',
    en: 'QQ BOT',
    initial: 'Q',
    icon: '/icons/qq-fill.svg',
    hint: '使用 QQ 官方 C2C 私聊接口主动推送。用户先与机器人私聊。user_openid 从 C2C_MESSAGE_CREATE 事件取得。不再使用受白名单限制的群 OpenID。',
    fields: [
      { k: 'appid', l: 'AppID', en: 'APPID', ph: 'QQ 开放平台机器人 AppID', span: 1 },
      { k: 'secret', l: 'AppSecret', en: 'APPSECRET', t: 'password', ph: '••••••••', span: 1 },
      { k: 'user_openid', l: '用户 OpenID', en: 'USER OPENID', ph: 'C2C 事件中的 user_openid', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'napcat',
    name: 'QQ群机器人(NapCat)',
    en: 'NAPCAT · ONEBOT 11',
    initial: 'Q',
    icon: '/icons/qq-fill.svg',
    hint: '连接你自行部署的 NapCat HTTP Server。服务端必须能访问该地址。Token 可留空。在 NapCat 中配置并填写 Token。',
    fields: [
      { k: 'base_url', l: '服务地址', en: 'SERVICE URL', ph: 'http://192.168.1.100:3000', span: 2 },
      { k: 'token', l: 'Token（可选）', en: 'TOKEN', t: 'password', ph: 'NapCat HTTP Server Token', span: 2 },
      { k: 'target', l: '目标 ID', en: 'TARGET ID', ph: '群号', kind: 'napcat-target', span: 2 },
    ],
    ready: true,
  },
  {
    id: 'telegram',
    name: 'Telegram Bot',
    en: 'TELEGRAM',
    initial: 'TG',
    icon: '/icons/telegram-fill.svg',
    hint: '私聊向 Bot 发送 /start。群聊发送 /start@机器人用户名。只填 Bot Token 后点击「发送测试」。应用自动识别并保存 Chat ID。自动识别失败时手动填写。',
    fields: [
      { k: 'token', l: 'Bot Token', en: 'TOKEN', t: 'password', ph: '123456:ABC-DEF', span: 2 },
      { k: 'chat', l: 'Chat ID', en: 'PRIVATE / GROUP CHAT ID', ph: '123456789 或 -1001234567890', span: 2 },
    ],
    ready: true,
  },
]

/** 是否已接通投递。缺省 false，避免新渠道误标可用。 */
export function channelReady(def: ChDef): boolean {
  return def.ready === true
}

export function channelPresentation(
  def: ChDef,
  visibility: Record<string, boolean> = {},
  comingSoon: Record<string, boolean> = {},
) {
  const visible = visibility[def.id] !== false
  const soon = comingSoon[def.id] ?? !channelReady(def)
  return { visible, comingSoon: visible && soon, ready: visible && channelReady(def) && !soon }
}

/* ---- 渠道分类 ----
   分类仅管分组。成员在 channels。组内顺序由 channelOrder 决定。
   分类由管理面板维护。以下为运维未配置时的兜底。 */

export interface ChannelCategory {
  id: string
  name: string
  en: string
  desc?: string
  /** 成员渠道 id。不参与排序。 */
  channels: string[]
}

export interface ChannelGroup {
  id: string
  name: string
  en: string
  desc?: string
  defs: ChDef[]
}

export const DEFAULT_CHANNEL_CATEGORIES: ChannelCategory[] = [
  {
    id: 'essentials',
    name: '开箱即用',
    en: 'READY TO USE',
    desc: '安装 App 或注册免费服务。填写一个凭证即可收到推送。无需自行部署。',
    channels: ['mail', 'sms', 'pushplus', 'serverchan_turbo', 'serverchan3', 'bark', 'whatsapp'],
  },
  {
    id: 'group_bots',
    name: '群聊机器人',
    en: 'GROUP CHAT BOTS',
    desc: '在钉钉、飞书或企业微信群中添加自定义机器人。复制 Webhook 到此处。',
    channels: ['dingtalk', 'feishu', 'lark', 'wecom', 'wecom_webhook', 'discord', 'telegram'],
  },
  {
    id: 'developer',
    name: '进阶开发者渠道',
    en: 'SELF-HOSTED & DEVELOPER',
    desc: '需要自建服务或申请开发者应用。面向进阶用户。',
    channels: ['webhook', 'gotify', 'napcat', 'qq', 'mp'],
  },
]

/** 未分类渠道的兜底分组。新渠道在运维分类前落到此处。 */
export const UNCATEGORIZED_GROUP = { id: '__other__', name: '其它渠道', en: 'OTHER' }

/**
 * 将已排序渠道按分类切组。
 * 分类为空 = 不分组，返回一个无标题组，用户端平铺展示。
 */
export function groupChannelDefs(defs: ChDef[], categories: ChannelCategory[] = []): ChannelGroup[] {
  if (categories.length === 0) return [{ id: '', name: '', en: '', defs }]
  const groups: ChannelGroup[] = []
  const taken = new Set<string>()
  for (const category of categories) {
    const members = new Set(category.channels)
    // 遍历 defs。组内顺序始终来自 channelOrder。
    const picked = defs.filter((def) => !taken.has(def.id) && members.has(def.id))
    picked.forEach((def) => taken.add(def.id))
    if (picked.length > 0) groups.push({ ...category, defs: picked })
  }
  const rest = defs.filter((def) => !taken.has(def.id))
  if (rest.length > 0) groups.push({ ...UNCATEGORIZED_GROUP, defs: rest })
  return groups
}

/**
 * 将渠道移到目标分类。categoryID 为空 = 移出所有分类。
 * 必须先从旧分类摘除再加入新分类。双归属会被后端整份打回。
 */
export function assignChannelToCategory(
  categories: ChannelCategory[],
  channelID: string,
  categoryID: string,
): ChannelCategory[] {
  return categories.map((category) => {
    const channels = category.channels.filter((id) => id !== channelID)
    return category.id === categoryID ? { ...category, channels: [...channels, channelID] } : { ...category, channels }
  })
}

/** 管理面板顺序优先。未知与重复项忽略。新渠道自动补到末尾。 */
export function orderedChannelDefs(order: string[] = []): ChDef[] {
  const rank = new Map<string, number>()
  for (const id of order) {
    if (!rank.has(id)) rank.set(id, rank.size)
  }
  return [...CH_DEFS].sort((a, b) => {
    const ai = rank.get(a.id)
    const bi = rank.get(b.id)
    if (ai == null && bi == null) return CH_DEFS.indexOf(a) - CH_DEFS.indexOf(b)
    if (ai == null) return 1
    if (bi == null) return -1
    return ai - bi
  })
}

const CHANNEL_REQUIRED_KEYS: Record<string, string[]> = {
  mail: ['to'],
  sms: ['sms'],
  dingtalk: ['webhook', 'secret'],
  lark: ['webhook', 'secret'],
  wecom: ['botid', 'secret', 'chatid'],
  wecom_webhook: ['webhook'],
  feishu: ['webhook', 'secret'],
  discord: ['webhook'],
  webhook: ['webhook'],
  bark: ['base_url', 'device_key'],
  gotify: ['base_url', 'token'],
  whatsapp: ['webhook'],
  pushplus: ['token'],
  serverchan_turbo: ['sendkey'],
  serverchan3: ['sendkey'],
  mp: ['appid', 'secret', 'tpl', 'openid'],
  qq: ['appid', 'secret', 'user_openid'],
  napcat: ['base_url', 'target'],
  telegram: ['token', 'chat'],
}

function hasChannelValue(value: unknown): boolean {
  if (Array.isArray(value)) return value.some((item) => String(item ?? '').trim() !== '')
  const text = String(value ?? '').trim()
  return text !== '' && text !== 'group:' && text !== 'user:'
}

/** 启用前缺少的字段名。用于就地校验。禁止伪装成网络错误。 */
export function missingChannelFields(def: ChDef, config: ChConfig = {}): string[] {
  return (CHANNEL_REQUIRED_KEYS[def.id] || [])
    .filter((key) => !hasChannelValue(config[key]))
    .map((key) => def.fields.find((field) => field.k === key)?.l || def.rcpt?.find((field) => field.k === key)?.en || key)
}

export type ChConfig = Record<string, any>

export const DEFAULT_CH: Record<string, ChConfig> = {
  mail: { on: true, to: ['2021xxxxx@qq.com'] },
  sms: { on: false, sms: ['13800006021'] },
  dingtalk: { on: true, webhook: 'https://oapi.dingtalk.com/robot/send?access_token=a91f…', secret: '' },
  wecom: { on: false, botid: '', secret: '', chatid: '' },
  wecom_webhook: { on: false, webhook: '' },
  feishu: { on: true, webhook: 'https://open.feishu.cn/open-apis/bot/v2/hook/7d2c…', secret: '' },
  discord: { on: false, webhook: '' },
  webhook: { on: false, webhook: '', token: '' },
  bark: { on: false, base_url: 'https://api.day.app', device_key: '' },
  gotify: { on: false, base_url: '', token: '', priority: '' },
  whatsapp: { on: false, webhook: '' },
  lark: { on: false, webhook: '', secret: '' },
  pushplus: { on: false, token: '' },
  serverchan_turbo: { on: false, sendkey: '' },
  serverchan3: { on: false, sendkey: '' },
  mp: { on: false, appid: '', secret: '', tpl: '', openid: '' },
  qq: { on: false, appid: '', secret: '', user_openid: '' },
  napcat: { on: false, base_url: '', token: '', target: 'group:' },
  telegram: { on: false, token: '', chat: '' },
}

export const PERIODS = [
  { k: 'daily', label: '每天一次', en: 'DAILY' },
  { k: 'twice', label: '每天两次', en: '12 HOURS' },
  { k: 'every3', label: '每三天', en: '3 DAYS' },
  { k: 'weekly', label: '每周一次', en: 'WEEKLY' },
]

/** 默认上榜展示楼栋、楼层、房间。昵称默认隐藏。 */
export const DEFAULT_PF: Record<string, boolean> = { meter: true, bldg: true, floor: true, room: true, nick: false }
/** 默认：栋号与楼层完整，房间打码。 */
export const DEFAULT_PMASK: Record<string, boolean> = { meter: true, bldg: false, floor: false, room: true }
