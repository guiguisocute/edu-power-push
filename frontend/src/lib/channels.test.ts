import {
  CH_DEFS,
  DEFAULT_CHANNEL_CATEGORIES,
  assignChannelToCategory,
  channelPresentation,
  groupChannelDefs,
  missingChannelFields,
  orderedChannelDefs,
} from './channels'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message)
}

export function runChannelPresentationTests() {
  const byID = Object.fromEntries(CH_DEFS.map((def) => [def.id, def]))

  for (const id of ['dingtalk', 'feishu', 'lark', 'wecom', 'wecom_webhook', 'discord', 'webhook', 'bark', 'gotify', 'whatsapp', 'serverchan_turbo', 'serverchan3', 'mp', 'qq', 'napcat']) {
    assert(byID[id]?.ready, `${id} 应已接通真实投递`)
  }
  assert(byID.wecom.fields.map((field) => field.k).join(',') === 'botid,secret,chatid', '企业微信应要求会话 ID')
  assert(byID.wecom_webhook.fields.map((field) => field.k).join(',') === 'webhook', '企业微信消息推送应只要求 Webhook')
  assert(byID.discord.fields.map((field) => field.k).join(',') === 'webhook', 'Discord 应只要求 Webhook')
  assert(byID.whatsapp.fields.map((field) => field.k).join(',') === 'webhook', 'WhatsApp 应只要求 CallMeBot API URL')
  assert(byID.webhook.fields.map((field) => field.k).join(',') === 'webhook,token', 'Webhook 应仅展示地址与可选凭证')
  assert(byID.webhook.protocol?.includes('balance_yuan'), 'Webhook 应展示稳定请求结构')
  assert(byID.bark.fields.map((field) => field.k).join(',') === 'base_url,device_key', 'Bark 应仅要求服务地址和 Device Key')
  assert(byID.gotify.fields.map((field) => field.k).join(',') === 'base_url,token,priority', 'Gotify 应仅要求服务地址、令牌和可选优先级')
  assert(byID.serverchan_turbo.fields.map((field) => field.k).join(',') === 'sendkey', 'Server酱Turbo 应只要求 SendKey')
  assert(byID.serverchan3.fields.map((field) => field.k).join(',') === 'sendkey', 'Server酱³ 应只要求 SendKey')
  assert(byID.mp.fields.map((field) => field.k).join(',') === 'appid,secret,tpl,openid', '微信测试号应要求模板和接收人')
  assert(byID.qq.name === 'QQ 官方机器人', 'QQ 官方渠道名称应与群机器人区分')
  assert(byID.qq.fields.map((field) => field.k).join(',') === 'appid,secret,user_openid', 'QQ 官方渠道应要求用户 OpenID')
  assert(byID.napcat.fields.map((field) => field.k).join(',') === 'base_url,token,target', 'NapCat 应只展示三个必要字段')
  assert(byID.napcat.icon === byID.qq.icon, 'NapCat 应沿用 QQ 图标')
  assert(byID.napcat.fields.find((field) => field.k === 'target')?.kind === 'napcat-target', 'NapCat 目标应提供群聊/私聊模式')
  for (const id of ['wecom', 'mp', 'qq']) {
    assert(byID[id].fields.find((field) => field.k === 'secret')?.t === 'password', `${id} 密钥必须使用密码输入框`)
  }
  assert(byID.napcat.fields.find((field) => field.k === 'token')?.t === 'password', 'NapCat Token 必须使用密码输入框')
  for (const id of ['wecom_webhook', 'discord', 'whatsapp', 'webhook']) {
    assert(byID[id].fields.find((field) => field.k === 'webhook')?.t === 'password', `${id} Webhook 必须使用密码输入框`)
  }
  assert(byID.bark.fields.find((field) => field.k === 'device_key')?.t === 'password', 'Bark Device Key 必须使用密码输入框')
  assert(byID.gotify.fields.find((field) => field.k === 'token')?.t === 'password', 'Gotify Token 必须使用密码输入框')
  for (const id of ['serverchan_turbo', 'serverchan3']) {
    assert(byID[id].fields.find((field) => field.k === 'sendkey')?.t === 'password', `${id} SendKey 必须使用密码输入框`)
  }
  assert(byID.feishu.icon === '/icons/feishu-lark.svg' && byID.lark.icon === '/icons/feishu-lark.svg', '飞书与 Lark 应使用本地品牌图标')
  /* 图标一律本地托管。校园网第三方 CDN 不稳定。禁止外链泄露访客 IP。 */
  for (const def of CH_DEFS) {
    assert(def.icon.startsWith('/icons/'), `${def.id} 的图标必须是本地文件，不能是外链：${def.icon}`)
  }
  assert(byID.mail.hint.includes('须先接收验证码'), '附加推送邮箱必须明确提示归属验证')

  const emptyDiscord = missingChannelFields(byID.discord, { on: false })
  assert(emptyDiscord.join(',') === 'Webhook 地址', '未配置 Discord 时应提示缺少 Webhook')
  const telegramTokenOnly = missingChannelFields(byID.telegram, { token: 'saved-token', chat: '' })
  assert(telegramTokenOnly.join(',') === 'Chat ID', 'Telegram Token-only 流程应只提示自动获取 Chat ID')
  assert(missingChannelFields(byID.telegram, { token: 'saved-token', chat: '123' }).length === 0, 'Telegram 配置完整后应允许启用')
  assert(missingChannelFields(byID.napcat, { base_url: 'http://127.0.0.1:3000', target: 'group:' }).join(',') === '目标 ID', 'NapCat 空目标不能被视为已填写')

  const ordered = orderedChannelDefs(['qq', 'mail', 'qq', 'unknown']).map((def) => def.id)
  assert(ordered[0] === 'qq' && ordered[1] === 'mail', '管理面板顺序应优先并忽略重复/未知渠道')
  assert(ordered.length === CH_DEFS.length, '未配置顺序的新渠道必须自动补齐')

  const larkReady = channelPresentation(byID.lark, { lark: true }, { lark: false })
  assert(larkReady.visible && larkReady.ready && !larkReady.comingSoon, 'Lark 可配置为可用')

  const larkSoon = channelPresentation(byID.lark, { lark: true }, { lark: true })
  assert(larkSoon.visible && !larkSoon.ready && larkSoon.comingSoon, '已实现渠道也可配置为 Coming Soon')

  const larkHidden = channelPresentation(byID.lark, { lark: false }, { lark: false })
  assert(!larkHidden.visible && !larkHidden.ready, '渠道可完全隐藏')

  const smsCannotBeForcedReady = channelPresentation(byID.sms, { sms: true }, { sms: false })
  assert(!smsCannotBeForcedReady.ready, '管理配置不能绕过代码能力上限')

  // ---- 分类分组 ----
  const allDefs = orderedChannelDefs([])
  const defaultGroups = groupChannelDefs(allDefs, DEFAULT_CHANNEL_CATEGORIES)
  assert(
    defaultGroups.reduce((sum, group) => sum + group.defs.length, 0) === allDefs.length,
    '分组只切列表，不能吞掉或复制渠道',
  )
  assert(
    defaultGroups.every((group) => group.id !== '__other__'),
    '内置分类必须覆盖全部渠道，否则用户端会多出一个「其它渠道」',
  )

  const flat = groupChannelDefs(allDefs, [])
  assert(flat.length === 1 && flat[0].name === '' && flat[0].defs.length === allDefs.length, '没有分类时应退回平铺列表')

  const partial = groupChannelDefs(allDefs, [{ id: 'easy', name: '开箱即用', en: 'EASY', channels: ['mail', 'unknown'] }])
  assert(partial[0].defs.map((def) => def.id).join(',') === 'mail', '未知渠道不应凭空出现在分组里')
  assert(partial[1]?.id === '__other__' && partial[1].defs.length === allDefs.length - 1, '没被分类收走的渠道应落到兜底组')

  const ordering = groupChannelDefs(orderedChannelDefs(['telegram', 'mail']), [
    { id: 'easy', name: '开箱即用', en: 'EASY', channels: ['mail', 'telegram'] },
  ])
  assert(ordering[0].defs.map((def) => def.id).join(',') === 'telegram,mail', '组内顺序仍应取自 channelOrder')

  // ---- 管理面板改归属 ----
  const before: typeof DEFAULT_CHANNEL_CATEGORIES = [
    { id: 'easy', name: '开箱即用', en: 'EASY', channels: ['mail', 'bark'] },
    { id: 'dev', name: '开发者', en: 'DEV', channels: ['webhook'] },
  ]
  const moved = assignChannelToCategory(before, 'mail', 'dev')
  assert(moved[0].channels.join(',') === 'bark', '换分类必须先从旧分类里摘掉')
  assert(moved[1].channels.join(',') === 'webhook,mail', '换分类应把渠道加进新分类')
  const detached = assignChannelToCategory(before, 'mail', '')
  assert(detached.every((category) => !category.channels.includes('mail')), '选「未分类」应把渠道移出所有分类')
  assert(before[0].channels.join(',') === 'mail,bark', '改归属禁止修改入参')
}
