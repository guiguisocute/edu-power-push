/* 推送设置与账号隐私共用视图组件。
   推送设置：预警、定时、渠道。账号设置：账号、安全、榜单隐私。
   渠道定义来自 lib/channels.ts。隐私展示来自 lib/privacy.ts。
   开关用 Toggle。内联样式对应原型。 */

import type { CSSProperties } from 'react'
import { Fragment, useEffect, useState } from 'react'
import { Eye, EyeOff, RotateCcw } from 'lucide-react'
import { useStore } from '../lib/store'
import { makeFmt, themeColors } from '../lib/format'
import { daily, RATE } from '../lib/mock'
import {
  CH_DEFS,
  DEFAULT_PF,
  DEFAULT_PMASK,
  PERIODS,
  channelPresentation,
  groupChannelDefs,
  missingChannelFields,
  orderedChannelDefs,
  type RcptDef,
} from '../lib/channels'
import { privacyInfo } from '../lib/privacy'
import { SegBtn, Toggle, trackOffColor } from '../components/ui'
import { seg } from '../lib/format'
import { IS_LIVE } from '../api/mode'
import { num, useLiveMeter, useLiveRankings } from '../api/live'
import { api } from '../api/client'
import { errMsg } from '../api/errors'
import { maskAccount } from '../config/features'
import { defaultNotificationTemplates, type LocalMessageTemplate } from '../api/notifications'

const monoLabel = (fs: string, ls: string): CSSProperties => ({
  font: `500 ${fs}/1 'JetBrains Mono',monospace`,
  letterSpacing: ls,
  color: 'var(--fg3)',
})

type AccPanel = null | 'nick' | 'email' | 'phone' | 'pass' | 'delete'
type TemplateKind = 'lowBalance' | 'digest'
type TemplateField = keyof LocalMessageTemplate

const TEMPLATE_VARIABLES: Record<TemplateKind, string[]> = {
  lowBalance: ['balance_yuan', 'threshold_yuan', 'meter_number', 'campus', 'building', 'floor', 'room', 'title', 'message', 'sent_at'],
  digest: ['balance_yuan', 'usage_kwh', 'period', 'meter_number', 'campus', 'building', 'floor', 'room', 'title', 'message', 'sent_at'],
}

function renderTemplatePreview(source: string, values: Record<string, string>): string {
  return source.replace(/\{\{\s*\.([a-z_]+)\s*\}\}/g, (token, key: string) => values[key] ?? token)
}

export default function ConfigView({ section = 'push' }: { section?: 'push' | 'account' }) {
  const {
    s,
    set,
    say,
    persist,
    setPrivacy,
    setNotifSettings,
    resetPushDefaults,
    syncChannel,
    testPushChannel,
    saveNickname,
    changePassword,
    sendEmailChangeCode,
    changeEmail,
    revokeSessions,
    deleteAccount,
    startBind,
  } = useStore()
  const dark = s.theme === 'dark'
  const { RED, OK, FG, FG2, FG3 } = themeColors(dark)
  /* 电价由运维面板下发（元/kWh）：kWh↔元 的换算全站同一个数，
     这里再默认回 mock 的 RATE，切成「元」时配置页的读数会和概览对不上。 */
  const elecRate = parseFloat(s.features?.display?.electricityRate || '') || RATE
  const { U, f2, cv } = makeFmt(s.unit, elecRate)
  const k = daily()
  const trackOff = trackOffColor(dark)

  /* ---- live 数据。mock 模式下 hooks 全部空转。
     预警文案说的是「当前余额」，必须是本账号真实余额。
     榜单预览说的是「别人看到的你」，那一行的名次与用电量同理。 */
  const meterNo = (IS_LIVE && s.user?.meter) || undefined
  const lm = useLiveMeter(meterNo)
  const lrk = useLiveRankings(s.bPeriod, 'usage', undefined, undefined, !!meterNo)

  // ---- 账号设置本地表单。不写入全局 store。
  const [accOpen, setAccOpen] = useState<AccPanel>(null)
  const [accBusy, setAccBusy] = useState(false)
  const [nickDraft, setNickDraft] = useState('')
  const [emailDraft, setEmailDraft] = useState('')
  const [emailCode, setEmailCode] = useState('')
  const [emailPass, setEmailPass] = useState('')
  const [emailCodeSent, setEmailCodeSent] = useState(false)
  const [phoneDraft, setPhoneDraft] = useState('')
  const [curPass, setCurPass] = useState('')
  const [newPass, setNewPass] = useState('')
  const [newPass2, setNewPass2] = useState('')
  // 登出全部不可撤销。点一次先进入确认态。
  const [revokeArmed, setRevokeArmed] = useState(false)
  // 注销账号：密码加手抄「注销账号」。两道都过按钮才亮。
  const [delPass, setDelPass] = useState('')
  const [delConfirm, setDelConfirm] = useState('')
  const [resetArmed, setResetArmed] = useState(false)
  const [mailAdding, setMailAdding] = useState(false)
  const [mailDraft, setMailDraft] = useState('')
  const [mailCode, setMailCode] = useState('')
  const [mailCodeSent, setMailCodeSent] = useState(false)
  const [mailVerifyBusy, setMailVerifyBusy] = useState(false)
  const [visibleSecrets, setVisibleSecrets] = useState<Record<string, boolean>>({})
  const [templateKind, setTemplateKind] = useState<TemplateKind>('lowBalance')
  const [templateField, setTemplateField] = useState<TemplateField>('body')

  useEffect(() => {
    if (s.user) setNickDraft(s.user.name || '')
  }, [s.user?.name])

  /* ---- 账号状态：右栏事实卡。仅列库中已有字段。
     缺字段显示 —。禁止编造数值。 */
  const fmtDate = (raw?: string | null) => {
    if (!raw) return '—'
    const d = new Date(raw)
    if (isNaN(d.getTime())) return '—'
    const p = (n: number) => String(n).padStart(2, '0')
    return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' + p(d.getHours()) + ':' + p(d.getMinutes())
  }
  const sessions = s.user?.activeSessions
  const accountFacts: { label: string; value: string; tone?: string }[] = s.user
    ? [
        { label: '注册于', value: fmtDate(s.user.createdAt) },
        { label: '上次登录', value: fmtDate(s.user.lastLoginAt) },
        s.user.emailVerifiedAt
          ? { label: '邮箱', value: '已验证 · ' + fmtDate(s.user.emailVerifiedAt), tone: OK }
          : { label: '邮箱', value: '未验证', tone: RED },
        {
          label: '登录中的设备',
          value: sessions == null ? '—' : sessions + ' 台',
          tone: sessions != null && sessions > 2 ? RED : undefined,
        },
        { label: '电表绑定于', value: s.user.meter ? fmtDate(s.user.boundAt) : '未绑定' },
      ]
    : []

  /* ---- 低额度预警
     这段文案讲「还剩多少、还能撑几天」，全部必须是真数。
     live 取 /me/overview 的余额与近 7 日用电。mock 才用原型的 43.87 与 daily()。
     live 下没有读数（未绑表 / 上游未抄到）时不猜数，改中性文案。 */
  const liveTotal = IS_LIVE ? num(lm.data?.latest?.total_yuan ?? null) : null
  const liveAvg7 = IS_LIVE ? num(lm.data?.recent_7d_kwh ?? null) : null
  const hasBal = !IS_LIVE || liveTotal != null
  const total = IS_LIVE ? (liveTotal ?? 0) : 43.87
  const avg7 = IS_LIVE ? (liveAvg7 != null ? liveAvg7 / 7 : 0) : k.slice(23).reduce((a, b) => a + b, 0) / 7
  const avgCost = avg7 * elecRate
  /* 已启用数仅计本站已接通且用户打开的渠道。未接通的不算。 */
  const enabledCount = CH_DEFS.filter(
    (d) => channelPresentation(d, s.features.channels, s.features.channelComingSoon).ready && s.ch[d.id]?.on,
  ).length
  const low = hasBal && total <= s.threshold
  const threshColor = low ? RED : FG
  const thresholdHint = !hasBal
    ? '余额低于阈值时会立刻向已启用渠道推送一次预警。当前还没有读到这块表的余额，暂时算不出剩余天数。'
    : low
      ? '当前余额 ' +
        total.toFixed(2) +
        ' 元已低于阈值。保存后将向 ' +
        enabledCount +
        ' 个已启用渠道推送预警。'
      : avgCost > 0
        ? '按当前日均，余额约 ' +
          Math.max(0, Math.floor((total - s.threshold) / avgCost)) +
          ' 天后跌破 ' +
          s.threshold +
          ' 元。届时触发预警。'
        : '当前余额 ' + total.toFixed(2) + ' 元。近 7 日没有可用的用电量，暂时算不出剩余天数。'

  // ---- 定时推送
  const periodLabel = (PERIODS.find((p) => p.k === s.period) || PERIODS[0]).label
  const schedHint =
    '数据源每 12 小时同步一次。推送使用最近一次余额与用电量。' + periodLabel + ' ' + s.pushTime + ' 发送。'

  // ---- 隐私设置
  const pfv = s.pf || DEFAULT_PF
  const pmk = s.pmask || DEFAULT_PMASK
  const { sampleOf, mePieces, meLabel } = privacyInfo(s.user, pfv, pmk)
  /* live 禁止榜单展示电表号。榜单匿名可读，表号会成可枚举清单。 */
  // 第 4 列：1 = 可打码/完整切换。0 = 只能展示/隐藏。
  const privFieldDefs: [string, string, string, number][] = (
    [
      ['meter', '电表号', 'METER', 1],
      ['bldg', '楼栋', 'BUILDING', 1],
      ['floor', '楼层', 'FLOOR', 1],
      ['room', '房间号', 'ROOM', 1],
      ['nick', '昵称', 'NICKNAME', 0],
    ] as [string, string, string, number][]
  ).filter((f) => !(IS_LIVE && f[0] === 'meter'))
  const privRowLabel = mePieces.length ? meLabel : '匿名用户'
  /* 这一行说的是「别人在榜单上看到的你」，名次和用电量就得是榜单上的真数：
     live 取 rankings.self（和榜单页同一个缓存键），拿不到就显示「—」，
     不能拿演示用的 #128 冒充 —— 用户会当成自己的真实名次。 */
  const liveSelf = IS_LIVE ? (lrk.data?.self ?? null) : null
  const privRowVal = IS_LIVE
    ? liveSelf
      ? cv(parseFloat(liveSelf.value_kwh) || 0).toFixed(2) + ' ' + U
      : '—'
    : f2(k[29]) + ' ' + U
  const privRowRank = IS_LIVE ? (liveSelf ? '#' + liveSelf.rank : '#—') : '#128'
  const privPreview = mePieces.length
    ? '其他用户在榜单上看到上面这行。未勾选的字段对他人隐藏。'
    : '全部字段已隐藏。你以匿名身份上榜。仅排名与用电量可见。'

  // ---- 渠道。展示与 Coming Soon 由管理面板下发。代码 ready 为能力上限。
  const presentationOf = (def: (typeof CH_DEFS)[number]) =>
    channelPresentation(def, s.features.channels, s.features.channelComingSoon)
  const visibleChannels = orderedChannelDefs(s.features.channelOrder).filter((d) => presentationOf(d).visible)
  const readyChannels = visibleChannels.filter((d) => presentationOf(d).ready)
  /* 分组只切列表。不改顺序与数量。分类为空时返回无标题组，平铺展示。 */
  const channelGroups = groupChannelDefs(visibleChannels, s.features.channelCategories)
  const setCh = (id: string, patch: Record<string, unknown>, syncNow = false) => {
    const local = { ...s.ch[id], ...patch }
    const ch = { ...s.ch, [id]: local }
    set({ ch })
    persist({ ch })
    if (syncNow) syncChannel(id, local)
  }
  const listOf = (id: string, key: string): string[] => {
    const v = s.ch[id]?.[key]
    return Array.isArray(v) ? (v.length ? v : ['']) : v ? [String(v)] : ['']
  }

  const nextPush = s.schedule ? '明日 ' + s.pushTime : '已关闭'
  const showPhoneBind = s.features.auth.smsLogin // 测试环境默认关。代码保留。
  const accountEmail = s.user?.email || ''
  const accountEmailShown = accountEmail ? maskAccount(accountEmail) : s.user?.phone || '—'

  const openAcc = (panel: AccPanel) => {
    setAccOpen((p) => (p === panel ? null : panel))
    setEmailCodeSent(false)
    setEmailCode('')
    setEmailPass('')
    setCurPass('')
    setNewPass('')
    setNewPass2('')
    if (panel === 'email') setEmailDraft('')
    if (panel === 'phone') setPhoneDraft('')
    setDelPass('')
    setDelConfirm('')
  }

  const DELETE_PHRASE = '注销账号'

  const fieldInputStyle: CSSProperties = {
    width: '100%',
    background: 'none',
    border: 0,
    borderBottom: '1px solid var(--line)',
    padding: '9px 0',
    color: 'var(--fg)',
    fontFamily: 'inherit',
    fontSize: '15px',
    fontWeight: 500,
    lineHeight: 1.35,
    letterSpacing: '-.02em',
  }
  const pillBtn = (primary: boolean): CSSProperties => ({
    margin: 0,
    padding: '9px 18px',
    borderRadius: '999px',
    font: 'inherit',
    fontSize: '12.5px',
    fontWeight: primary ? 600 : 500,
    cursor: accBusy ? 'wait' : 'pointer',
    background: primary ? 'var(--fg)' : 'none',
    color: primary ? 'var(--bg)' : 'var(--fg2)',
    border: `1px solid ${primary ? 'var(--fg)' : 'var(--line)'}`,
    opacity: accBusy ? 0.6 : 1,
  })

  const resetPush = () => {
    if (!resetArmed) {
      setResetArmed(true)
      return
    }
    setResetArmed(false)
    resetPushDefaults()
  }

  const updateNotificationTemplate = (field: TemplateField, value: string, syncNow = false) => {
    const notificationTemplates = {
      ...s.notificationTemplates,
      [templateKind]: { ...s.notificationTemplates[templateKind], [field]: value },
    }
    set({ notificationTemplates })
    if (syncNow) setNotifSettings({ notificationTemplates })
  }
  const restoreNotificationTemplate = () => {
    const notificationTemplates = {
      ...s.notificationTemplates,
      [templateKind]: defaultNotificationTemplates()[templateKind],
    }
    setNotifSettings({ notificationTemplates })
    say(templateKind === 'lowBalance' ? '低额度预警模板已恢复默认' : '定时摘要模板已恢复默认')
  }
  const appendTemplateVariable = (variable: string) => {
    const token = `{{.${variable}}}`
    const current = s.notificationTemplates[templateKind][templateField]
    const spacer = current && !/[\s\n]$/.test(current) ? ' ' : ''
    updateNotificationTemplate(templateField, current + spacer + token, true)
  }

  const activeTemplate = s.notificationTemplates[templateKind]
  /* 模板预览里的电表与位置用本账号的真值；live 下还没绑表就留「—」，
     不拿原型的 12栋 402 顶上（那会被当成系统已经知道了你的宿舍）。 */
  const previewFallback = (mockValue: string) => (IS_LIVE ? '—' : mockValue)
  const previewMeter = s.user?.meter || previewFallback('31240718')
  const previewBuilding = s.user?.building || previewFallback('12栋')
  const previewFloor = s.user?.floor || previewFallback('4楼')
  const previewRoom = s.user?.room || previewFallback('402')
  const previewValues: Record<string, string> = {
    event: templateKind === 'lowBalance' ? 'low_balance_alert' : 'scheduled_digest',
    title: templateKind === 'lowBalance' ? '低额度预警' : '用电摘要',
    sent_at: '2026-07-31T08:00:00+08:00',
    balance_yuan: total.toFixed(2),
    threshold_yuan: s.threshold.toFixed(2),
    usage_kwh: (avg7 * 7).toFixed(2),
    period: periodLabel,
    meter_number: previewMeter,
    campus: s.features.display.campusName,
    building: previewBuilding,
    floor: previewFloor,
    room: previewRoom,
    message: '',
  }
  const defaultTemplate = defaultNotificationTemplates()[templateKind]
  previewValues.message = renderTemplatePreview(defaultTemplate.body, previewValues)
  const previewTitle = renderTemplatePreview(activeTemplate.title, previewValues)
  const previewBody = renderTemplatePreview(activeTemplate.body, previewValues)

  const resetMailVerification = () => {
    setMailAdding(false)
    setMailDraft('')
    setMailCode('')
    setMailCodeSent(false)
    setMailVerifyBusy(false)
  }

  const sendMailRecipientCode = async () => {
    const email = mailDraft.trim().toLowerCase()
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) return say('请输入有效的收件邮箱')
    const current = listOf('mail', 'to').map((value) => value.trim().toLowerCase())
    if (current.includes(email)) return say('该邮箱已在收件列表中')
    setMailVerifyBusy(true)
    if (!IS_LIVE) {
      setMailVerifyBusy(false)
      setMailCodeSent(true)
      return say('验证码已发送至新收件邮箱（演示）')
    }
    try {
      await api.mailRecipientCode(email)
      setMailDraft(email)
      setMailCodeSent(true)
      say('验证码已发送至新收件邮箱。5 分钟内有效。')
    } catch (error) {
      say(errMsg(error))
    } finally {
      setMailVerifyBusy(false)
    }
  }

  const confirmMailRecipient = async () => {
    const email = mailDraft.trim().toLowerCase()
    if (!/^\d{6}$/.test(mailCode.trim())) return say('请输入邮件中的 6 位验证码')
    setMailVerifyBusy(true)
    try {
      if (IS_LIVE) await api.verifyMailRecipient(email, mailCode.trim())
      const current = listOf('mail', 'to').map((value) => value.trim().toLowerCase()).filter(Boolean)
      const next = Array.from(new Set([...current, email])).slice(0, 5)
      setCh('mail', { to: next }, true)
      resetMailVerification()
      say('邮箱已验证并加入推送收件列表')
    } catch (error) {
      setMailVerifyBusy(false)
      say(errMsg(error))
    }
  }

  const syncLabel =
    s.pushSync === 'saving'
      ? '正在保存…'
      : s.pushSync === 'error'
        ? '有一项配置未保存。请检查填写内容。'
        : IS_LIVE
          ? '所有改动已自动同步到服务器'
          : '所有改动已自动保存到本地浏览器'

  return (
    <div data-screen-label={section === 'push' ? '推送设置' : '账号设置'} style={{ animation: 'rise .28s ease both' }}>
      {section === 'push' && (
        <>
      <section
        data-r="split"
        style={{
          display: 'grid',
          gridTemplateColumns: '1fr 1fr',
          padding: '52px 0 46px',
          borderBottom: '1px solid var(--line)',
        }}
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: '24px', paddingRight: '44px' }}>
          <div style={{ display: 'flex', alignItems: 'flex-start', gap: '16px' }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
              <div style={monoLabel('9.5px', '.2em')}>LOW BALANCE ALERT</div>
              <div style={{ fontSize: '26px', fontWeight: 600, letterSpacing: '-.035em' }}>低额度预警</div>
              <div style={{ fontSize: '13px', color: 'var(--fg3)' }}>余额跌破阈值时立即向已启用渠道推送</div>
            </div>
            <Toggle
              on={s.lowAlert}
              trackOff={trackOff}
              onClick={() => {
                setNotifSettings({ lowAlert: !s.lowAlert })
              }}
              style={{ margin: '0 0 0 auto' }}
            />
          </div>
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: '16px',
              opacity: s.lowAlert ? '1' : '.34',
              transition: 'opacity .16s',
            }}
          >
            <div style={{ display: 'flex', alignItems: 'baseline', gap: '10px' }}>
              <span style={{ fontSize: '13px', color: 'var(--fg2)' }}>最低额度阈值</span>
              <span
                style={{
                  marginLeft: 'auto',
                  fontSize: '34px',
                  fontWeight: 600,
                  letterSpacing: '-.04em',
                  fontVariantNumeric: 'tabular-nums',
                  color: threshColor,
                }}
              >
                {s.threshold}
              </span>
              <span style={{ fontSize: '13px', color: 'var(--fg2)' }}>元</span>
            </div>
            <input
              type="range"
              min={1}
              max={50}
              step={1}
              value={s.threshold}
              onChange={(e) => set({ threshold: Number(e.target.value) })}
              onMouseUp={(e) => setNotifSettings({ threshold: Number((e.target as HTMLInputElement).value) })}
              onTouchEnd={(e) => setNotifSettings({ threshold: Number((e.target as HTMLInputElement).value) })}
              style={{ width: '100%', height: '4px' }}
            />
            <div
              style={{
                display: 'flex',
                justifyContent: 'space-between',
                font: "400 10.5px/1 'JetBrains Mono',monospace",
                color: 'var(--fg3)',
              }}
            >
              <span>1</span>
              <span>50</span>
            </div>
            <div style={{ fontSize: '13px', color: threshColor, lineHeight: 1.6, textWrap: 'pretty' }}>
              {thresholdHint}
            </div>
          </div>
        </div>

        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: '24px',
            paddingLeft: '44px',
            borderLeft: '1px solid var(--line)',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'flex-start', gap: '16px' }}>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
              <div style={monoLabel('9.5px', '.2em')}>SCHEDULED PUSH</div>
              <div style={{ fontSize: '26px', fontWeight: 600, letterSpacing: '-.035em' }}>定时推送</div>
              <div style={{ fontSize: '13px', color: 'var(--fg3)' }}>按固定周期推送余额与用电摘要</div>
            </div>
            <Toggle
              on={s.schedule}
              trackOff={trackOff}
              onClick={() => {
                setNotifSettings({ schedule: !s.schedule })
              }}
              style={{ margin: '0 0 0 auto' }}
            />
          </div>
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: '16px',
              opacity: s.schedule ? '1' : '.34',
              transition: 'opacity .16s',
            }}
          >
            <div style={{ fontSize: '13px', color: 'var(--fg2)' }}>推送周期</div>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2,1fr)', gap: '8px' }}>
              {PERIODS.map((p) => {
                const active = s.period === p.k
                return (
                  <button
                    key={p.k}
                    onClick={() => {
                      setNotifSettings({ period: p.k })
                    }}
                    style={{
                      margin: 0,
                      padding: '13px 15px',
                      font: 'inherit',
                      textAlign: 'left',
                      cursor: 'pointer',
                      border: `1px solid ${active ? 'var(--fg)' : 'var(--line)'}`,
                      background: active ? 'var(--fg)' : 'transparent',
                      color: active ? 'var(--bg)' : 'var(--fg2)',
                      display: 'flex',
                      flexDirection: 'column',
                      gap: '5px',
                      transition: 'background .16s, color .16s, border-color .16s',
                    }}
                  >
                    <span style={{ fontSize: '12.5px', fontWeight: active ? 600 : 400 }}>{p.label}</span>
                    <span style={{ font: "400 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.1em', opacity: 0.55 }}>
                      {p.en}
                    </span>
                  </button>
                )
              })}
            </div>
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '12px',
                paddingTop: '4px',
                borderTop: '1px solid var(--line2)',
                marginTop: '4px',
              }}
            >
              <span style={{ fontSize: '13px', color: 'var(--fg2)', paddingTop: '12px' }}>推送时刻</span>
              <input
                type="time"
                value={s.pushTime}
                onChange={(e) => set({ pushTime: e.target.value })}
                onBlur={(e) => setNotifSettings({ pushTime: e.target.value })}
                style={{
                  margin: '12px 0 0 auto',
                  padding: '9px 12px',
                  border: '1px solid var(--line)',
                  background: 'var(--bg)',
                  color: 'var(--fg)',
                  font: "400 13px/1 'JetBrains Mono',monospace",
                }}
              />
            </div>
            <div style={{ fontSize: '13px', color: 'var(--fg3)', lineHeight: 1.6, textWrap: 'pretty' }}>{schedHint}</div>
          </div>
        </div>
      </section>

      {/* ---- 推送渠道。紧跟两开关下，按管理面板分类分组。 ---- */}
      <section style={{ padding: '44px 0 40px', borderBottom: '1px solid var(--line)' }}>
        {/* data-r=hdr 必须保留。否则窄屏下 hdrside 被拉到标题右侧压字。 */}
        <div data-r="hdr" style={{ display: 'flex', alignItems: 'flex-end', gap: '16px', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
            <div style={monoLabel('9.5px', '.2em')}>CHANNELS</div>
            <div style={{ fontSize: '34px', fontWeight: 600, letterSpacing: '-.04em' }}>推送渠道</div>
          </div>
          {/* 计数再包一层。移动端会压掉 hdrside 的 baseline 对齐。 */}
          <div data-r="hdrside" style={{ marginLeft: 'auto', marginBottom: '5px' }}>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: '7px', flexWrap: 'wrap' }}>
              <span
                style={{ fontSize: '22px', fontWeight: 600, letterSpacing: '-.03em', fontVariantNumeric: 'tabular-nums' }}
              >
                {enabledCount}
              </span>
              <span style={{ fontSize: '13px', color: 'var(--fg3)' }}>
                / {readyChannels.length} 可用 · 共 {visibleChannels.length} 渠道
              </span>
            </div>
          </div>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', marginTop: '26px', borderTop: '1px solid var(--line)' }}>
          {channelGroups.map((group, groupIndex) => (
            <Fragment key={group.id || 'flat'}>
              {!!group.name && (
                <div
                  data-r="hdr"
                  style={{
                    display: 'flex',
                    alignItems: 'flex-end',
                    gap: '16px',
                    flexWrap: 'wrap',
                    padding: groupIndex === 0 ? '20px 4px 14px' : '34px 4px 14px',
                    borderBottom: '1px solid var(--line2)',
                  }}
                >
                  <div style={{ display: 'flex', flexDirection: 'column', gap: '7px', minWidth: 0 }}>
                    <div style={monoLabel('9px', '.18em')}>{group.en}</div>
                    <div style={{ fontSize: '15px', fontWeight: 600, letterSpacing: '-.02em' }}>{group.name}</div>
                  </div>
                  {!!group.desc && (
                    <div
                      data-r="hdrside"
                      style={{
                        marginLeft: 'auto',
                        maxWidth: '360px',
                        textAlign: 'right',
                        fontSize: '11.5px',
                        color: 'var(--fg3)',
                        lineHeight: 1.6,
                        textWrap: 'pretty',
                      }}
                    >
                      {group.desc}
                    </div>
                  )}
                </div>
              )}
              {group.defs.map((def) => {
                const st = s.ch[def.id] || {}
                const presentation = presentationOf(def)
                const ready = presentation.ready
                const on = ready && !!st.on
                const open = ready && s.open === def.id
                const t = s.testing[def.id]
                const isMail = def.kind === 'rcpt'
                const missing = missingChannelFields(def, st)
                const activationHint =
                  def.id === 'telegram' && !missing.includes('Bot Token') && missing.includes('Chat ID')
                    ? '请在私聊发送 /start。或在目标群发送 /start@机器人用户名。然后点击「发送测试」获取 Chat ID。'
                    : '启用前请先填写：' + missing.join('、')
                return (
                  <div
                    key={def.id}
                    style={{
                      borderBottom: '1px solid var(--line2)',
                      background: open ? 'var(--sub)' : 'transparent',
                      opacity: ready ? 1 : 0.48,
                      filter: ready ? 'none' : 'grayscale(1)',
                    }}
                  >
                    <div
                      onClick={() => {
                        if (!ready) return
                        set({ open: open ? null : def.id })
                      }}
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        gap: '16px',
                        padding: '18px 4px',
                        cursor: ready ? 'pointer' : 'default',
                      }}
                    >
                      <div
                        style={{
                          width: '32px',
                          height: '32px',
                          border: '1px solid var(--line)',
                          flex: 'none',
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'center',
                          position: 'relative',
                          background: 'var(--bg)',
                        }}
                      >
                        <span style={{ font: "500 11px/1 'JetBrains Mono',monospace", color: 'var(--fg3)' }}>
                          {def.initial}
                        </span>
                        {!!def.icon && (
                          <img
                            src={def.icon}
                            alt=""
                            style={{
                              position: 'absolute',
                              width: '17px',
                              height: '17px',
                              objectFit: 'contain',
                              background: 'var(--bg)',
                            }}
                          />
                        )}
                      </div>
                      <div style={{ display: 'flex', flexDirection: 'column', gap: '3px', minWidth: 0 }}>
                        <div style={{ fontSize: '13px', fontWeight: 600, letterSpacing: '-.015em' }}>{def.name}</div>
                        <div style={{ font: "400 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.14em', color: 'var(--fg3)' }}>
                          {def.en}
                        </div>
                      </div>
                      <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: '16px' }}>
                        <span
                          data-r="hidesm"
                          style={{
                            fontSize: '11.5px',
                            color: ready ? (on ? FG2 : FG3) : FG3,
                            whiteSpace: 'nowrap',
                          }}
                        >
                          {ready ? (on ? '已启用' : '未启用') : '暂未开放'}
                        </span>
                        {ready ? (
                          <Toggle
                            size="sm"
                            on={on}
                            trackOff={trackOff}
                            onClick={(e) => {
                              e.stopPropagation()
                              if (!on && missing.length > 0) {
                                set({ open: def.id })
                                say(def.name + ' · ' + activationHint)
                                return
                              }
                              const next = { ...st, on: !on }
                              say(def.name + (on ? ' 已关闭' : ' 已开启'))
                              syncChannel(def.id, next)
                            }}
                          />
                        ) : (
                          <span
                            style={{
                              font: "600 9.5px/1 'JetBrains Mono',monospace",
                              letterSpacing: '.14em',
                              color: 'var(--fg3)',
                              border: '1px solid var(--line)',
                              borderRadius: '999px',
                              padding: '7px 11px',
                              whiteSpace: 'nowrap',
                              userSelect: 'none',
                            }}
                          >
                            COMING SOON
                          </span>
                        )}
                        {ready ? (
                          <span
                            style={{
                              fontSize: '9px',
                              color: 'var(--fg3)',
                              transition: 'transform .16s',
                              display: 'block',
                              transform: `rotate(${open ? '180deg' : '0deg'})`,
                            }}
                          >
                            ▼
                          </span>
                        ) : (
                          <span style={{ width: '9px', flex: 'none' }} />
                        )}
                      </div>
                    </div>
                    {open && ready && (
                      <div
                        style={{
                          padding: '4px 4px 26px 54px',
                          display: 'flex',
                          flexDirection: 'column',
                          gap: '30px',
                          animation: 'menupop .18s ease both',
                        }}
                      >
                        {!on && missing.length > 0 && (
                          <div
                            role="status"
                            style={{
                              padding: '10px 12px',
                              borderLeft: `2px solid ${RED}`,
                              background: 'var(--redsoft)',
                              color: RED,
                              fontSize: '12px',
                              lineHeight: 1.6,
                            }}
                          >
                            {activationHint}
                          </div>
                        )}
                        {isMail && (
                          <div style={{ display: 'flex', flexDirection: 'column', gap: '26px', maxWidth: '560px' }}>
                            {(def.rcpt || []).map((g: RcptDef) => {
                              const arr = listOf(def.id, g.k).map((value) => value.trim()).filter(Boolean)
                              return <div key={g.k} style={{ display: 'flex', flexDirection: 'column', gap: '11px' }}>
                                <div style={{ display: 'flex', alignItems: 'baseline', gap: '12px' }}>
                                  <span style={monoLabel('9px', '.16em')}>{g.en}</span>
                                  <span style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>{g.note}</span>
                                </div>
                                {arr.map((value, index) => <div key={value} style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
                                  <span style={{ width: '16px', flex: 'none', font: "400 10px/1 'JetBrains Mono',monospace", color: 'var(--fg3)' }}>
                                    {String(index + 1).padStart(2, '0')}
                                  </span>
                                  <div style={{ flex: 1, minWidth: 0, padding: '10px 12px', border: '1px solid var(--line)', background: 'var(--sub)', color: 'var(--fg)', font: "400 12.5px/1.2 'JetBrains Mono',monospace", overflow: 'hidden', textOverflow: 'ellipsis' }}>
                                    {value}
                                  </div>
                                  <span style={{ flex: 'none', color: OK, fontSize: '11.5px', whiteSpace: 'nowrap' }}>✓ 已验证</span>
                                  <button className="hv-red" onClick={() => setCh(def.id, { [g.k]: arr.filter((_, i) => i !== index) }, true)} style={{ flex: 'none', background: 'none', border: '1px solid var(--line)', borderRadius: '999px', margin: 0, padding: '8px 14px', font: 'inherit', fontSize: '12px', color: 'var(--fg3)', cursor: 'pointer' }}>
                                    移除
                                  </button>
                                </div>)}
                                {mailAdding ? <div style={{ padding: '14px', border: '1px solid var(--line)', background: 'var(--sub)', display: 'flex', flexDirection: 'column', gap: '11px' }}>
                                  <span style={monoLabel('9px', '.16em')}>{mailCodeSent ? 'VERIFICATION CODE' : 'NEW RECIPIENT'}</span>
                                  <div style={{ display: 'flex', gap: '10px', flexWrap: 'wrap' }}>
                                    <input type="email" value={mailDraft} disabled={mailCodeSent || mailVerifyBusy} placeholder={g.ph} onChange={(event) => setMailDraft(event.target.value)} style={{ flex: '1 1 240px', minWidth: 0, padding: '10px 12px', border: '1px solid var(--line)', background: 'var(--bg)', color: 'var(--fg)', font: "400 12.5px/1.2 'JetBrains Mono',monospace" }} />
                                    {mailCodeSent && <input inputMode="numeric" maxLength={6} value={mailCode} placeholder="6 位验证码" onChange={(event) => setMailCode(event.target.value.replace(/\D/g, '').slice(0, 6))} style={{ flex: '0 1 150px', minWidth: '120px', padding: '10px 12px', border: '1px solid var(--line)', background: 'var(--bg)', color: 'var(--fg)', font: "400 12.5px/1.2 'JetBrains Mono',monospace", letterSpacing: '.12em' }} />}
                                    <button onClick={mailCodeSent ? confirmMailRecipient : sendMailRecipientCode} disabled={mailVerifyBusy} style={{ border: '1px solid var(--fg)', background: 'var(--fg)', color: 'var(--bg)', borderRadius: '999px', padding: '9px 16px', font: 'inherit', fontSize: '12px', cursor: mailVerifyBusy ? 'wait' : 'pointer', opacity: mailVerifyBusy ? .55 : 1 }}>
                                      {mailVerifyBusy ? '处理中…' : mailCodeSent ? '验证并添加' : '发送验证码'}
                                    </button>
                                    <button onClick={resetMailVerification} disabled={mailVerifyBusy} style={{ border: '1px solid var(--line)', background: 'none', color: 'var(--fg3)', borderRadius: '999px', padding: '9px 14px', font: 'inherit', fontSize: '12px', cursor: 'pointer' }}>取消</button>
                                  </div>
                                  <span style={{ fontSize: '11.5px', color: 'var(--fg3)', lineHeight: 1.6 }}>{mailCodeSent ? '验证码已发送至该邮箱。验证成功后才会写入收件列表。' : '将向新地址发送验证码。验证前不会保存或投递。'}</span>
                                </div> : <button className="hv-line-fg" onClick={() => setMailAdding(true)} disabled={arr.length >= 5} style={{ alignSelf: 'flex-start', background: 'none', border: '1px dashed var(--line)', margin: 0, padding: '9px 16px', font: 'inherit', fontSize: '12.5px', color: 'var(--fg2)', cursor: arr.length >= 5 ? 'not-allowed' : 'pointer', opacity: arr.length >= 5 ? .45 : 1 }}>
                                  {g.add}
                                </button>}
                              </div>
                            })}
                          </div>
                        )}
                        {!isMail && (
                          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(180px,1fr))', gap: '14px' }}>
                            {def.fields.map((f) => {
                              const rawValue = String(st[f.k] || '')
                              const isNapCatTarget = f.kind === 'napcat-target'
                              const targetMode = rawValue.startsWith('user:') ? 'user' : 'group'
                              const shownValue = isNapCatTarget ? rawValue.replace(/^(group|user):/, '') : rawValue
                              const secretKey = `${def.id}.${f.k}`
                              const isSecret = f.t === 'password'
                              const secretVisible = !!visibleSecrets[secretKey]
                              const isFreshSecret = isSecret && shownValue !== '' && shownValue !== '••••••••'
                              const inputID = `channel-${def.id}-${f.k}`
                              const fieldPatch = (value: string) => {
                                if (isNapCatTarget) return { [f.k]: `${targetMode}:${value}` }
                                if (isSecret) {
                                  const unchangedMask = value === '••••••••' && !!st[`__secret_${f.k}`]
                                  return { [f.k]: value, [`__secret_${f.k}`]: unchangedMask }
                                }
                                return { [f.k]: value }
                              }
                              return (
                                <div
                                  key={f.k}
                                  style={{
                                    display: 'flex',
                                    flexDirection: 'column',
                                    gap: '8px',
                                    minWidth: 0,
                                    gridColumn: `span ${f.span}`,
                                  }}
                                >
                                  <span style={{ display: 'flex', alignItems: 'center', gap: '10px', minHeight: '28px', flexWrap: 'wrap' }}>
                                    <label htmlFor={inputID} style={monoLabel('9px', '.16em')}>{f.en}</label>
                                    {isNapCatTarget && (
                                      <span
                                        role="group"
                                        aria-label="目标类型"
                                        style={{ display: 'inline-flex', border: '1px solid var(--line)', marginLeft: 'auto' }}
                                      >
                                        {([
                                          ['group', '群聊'],
                                          ['user', '私聊'],
                                        ] as const).map(([mode, label]) => (
                                          <button
                                            key={mode}
                                            type="button"
                                            aria-pressed={targetMode === mode}
                                            onMouseDown={(event) => event.preventDefault()}
                                            onClick={(event) => {
                                              event.preventDefault()
                                              setCh(def.id, { [f.k]: `${mode}:${shownValue}` }, true)
                                            }}
                                            style={{
                                              border: 0,
                                              borderRight: mode === 'group' ? '1px solid var(--line)' : 0,
                                              background: targetMode === mode ? 'var(--fg)' : 'transparent',
                                              color: targetMode === mode ? 'var(--bg)' : 'var(--fg3)',
                                              padding: '6px 10px',
                                              font: 'inherit',
                                              fontSize: '11px',
                                              cursor: 'pointer',
                                            }}
                                          >
                                            {label}
                                          </button>
                                        ))}
                                      </span>
                                    )}
                                  </span>
                                  <span style={{ position: 'relative', display: 'block' }}>
                                    <input
                                      id={inputID}
                                      type={isSecret ? (secretVisible ? 'text' : 'password') : f.t || 'text'}
                                      inputMode={isNapCatTarget ? 'numeric' : undefined}
                                      value={shownValue}
                                      placeholder={isNapCatTarget ? (targetMode === 'group' ? '群号' : 'QQ 号') : f.ph}
                                      onFocus={(event) => {
                                        if (isSecret && shownValue === '••••••••') event.currentTarget.select()
                                      }}
                                      onChange={(e) => setCh(def.id, fieldPatch(e.target.value), false)}
                                      onBlur={(e) => {
                                        setVisibleSecrets((current) => ({ ...current, [secretKey]: false }))
                                        setCh(def.id, fieldPatch(e.target.value), true)
                                      }}
                                      style={{
                                        width: '100%',
                                        padding: isSecret ? '10px 46px 10px 12px' : '10px 12px',
                                        border: '1px solid var(--line)',
                                        background: 'var(--sub)',
                                        color: 'var(--fg)',
                                        font: "400 12.5px/1.2 'JetBrains Mono',monospace",
                                      }}
                                    />
                                    {isSecret && (
                                      <button
                                        type="button"
                                        title={secretVisible ? '隐藏凭据' : '显示凭据'}
                                        aria-label={secretVisible ? '隐藏凭据' : '显示凭据'}
                                        onMouseDown={(event) => event.preventDefault()}
                                        onClick={(event) => {
                                          event.preventDefault()
                                          setVisibleSecrets((current) => ({ ...current, [secretKey]: !current[secretKey] }))
                                        }}
                                        style={{
                                          position: 'absolute',
                                          top: 0,
                                          right: 0,
                                          width: '40px',
                                          height: '100%',
                                          display: 'grid',
                                          placeItems: 'center',
                                          border: 0,
                                          borderLeft: '1px solid var(--line)',
                                          background: 'transparent',
                                          color: 'var(--fg3)',
                                          cursor: 'pointer',
                                        }}
                                      >
                                        {secretVisible ? <EyeOff size={16} strokeWidth={1.7} /> : <Eye size={16} strokeWidth={1.7} />}
                                      </button>
                                    )}
                                  </span>
                                  <span style={{ fontSize: '11.5px', color: 'var(--fg2)' }}>{f.l}</span>
                                  {isFreshSecret && (
                                    <span role="status" style={{ fontSize: '11.5px', color: 'var(--fg3)', lineHeight: 1.55 }}>
                                      此信息只会完整显示一次，保存后将隐藏，请妥善保管原始凭据。
                                    </span>
                                  )}
                                </div>
                              )
                            })}
                          </div>
                        )}
                        {def.protocol && (
                          <div style={{ border: '1px solid var(--line)', background: 'var(--sub)', padding: '14px', minWidth: 0 }}>
                            <div style={{ ...monoLabel('9px', '.16em'), marginBottom: '10px' }}>WEBHOOK REQUEST JSON</div>
                            <pre style={{ margin: 0, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', color: 'var(--fg2)', font: "400 11px/1.65 'JetBrains Mono',monospace" }}>{def.protocol}</pre>
                          </div>
                        )}
                        <div style={{ display: 'flex', alignItems: 'center', gap: '16px', flexWrap: 'wrap' }}>
                          <div
                            style={{
                              fontSize: '12px',
                              color: 'var(--fg3)',
                              lineHeight: 1.6,
                              flex: 1,
                              minWidth: '200px',
                              textWrap: 'pretty',
                            }}
                          >
                            {def.hint}
                          </div>
                          <button
                            onClick={(e) => {
                              e.stopPropagation()
                              testPushChannel(def.id, def.name)
                            }}
                            disabled={t === 'ing'}
                            style={{
                              margin: 0,
                              padding: '10px 20px',
                              borderRadius: '999px',
                              font: 'inherit',
                              fontSize: '12.5px',
                              fontWeight: 500,
                              cursor: t === 'ing' ? 'wait' : 'pointer',
                              background: t === 'ok' ? 'transparent' : 'var(--fg)',
                              color: t === 'ok' ? OK : 'var(--bg)',
                              border: `1px solid ${t === 'ok' ? OK : 'var(--fg)'}`,
                              transition: 'background .16s, color .16s, border-color .16s',
                            }}
                          >
                            {t === 'ing' ? '发送中…' : t === 'ok' ? '✓ 测试成功' : '发送测试'}
                          </button>
                        </div>
                      </div>
                    )}
                  </div>
                )
              })}
            </Fragment>
          ))}
        </div>
      </section>

      {/* ---- 非邮箱业务消息模板。排在渠道之后。
           先定推送目标，再定文案。 */}
      <section style={{ padding: '44px 0 46px', borderBottom: '1px solid var(--line)' }}>
        <div data-r="hdr" style={{ display: 'flex', alignItems: 'flex-end', gap: '16px', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
            <div style={monoLabel('9.5px', '.2em')}>MESSAGE TEMPLATES</div>
            <div style={{ fontSize: '34px', fontWeight: 600, letterSpacing: '-.04em' }}>消息模板</div>
          </div>
          <div data-r="hdrside" style={{ marginLeft: 'auto', color: FG3, fontSize: '12.5px', lineHeight: 1.6 }}>
            应用于机器人、Webhook 与其它非邮箱渠道
          </div>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginTop: '24px', flexWrap: 'wrap' }}>
          <div style={{ display: 'inline-flex', border: '1px solid var(--line)' }}>
            {([
              ['lowBalance', '低额度预警'],
              ['digest', '定时摘要'],
            ] as [TemplateKind, string][]).map(([kind, label]) => {
              const active = templateKind === kind
              return (
                <button
                  key={kind}
                  type="button"
                  onClick={() => setTemplateKind(kind)}
                  style={{
                    border: 0,
                    borderRight: kind === 'lowBalance' ? '1px solid var(--line)' : 0,
                    background: active ? 'var(--fg)' : 'transparent',
                    color: active ? 'var(--bg)' : 'var(--fg2)',
                    padding: '10px 16px',
                    font: 'inherit',
                    fontSize: '12.5px',
                    fontWeight: active ? 600 : 400,
                    cursor: 'pointer',
                  }}
                >
                  {label}
                </button>
              )
            })}
          </div>
          <button
            type="button"
            title="恢复当前模板默认值"
            aria-label="恢复当前模板默认值"
            onClick={restoreNotificationTemplate}
            className="hv-line-fg"
            style={{
              width: '38px',
              height: '38px',
              display: 'grid',
              placeItems: 'center',
              padding: 0,
              border: '1px solid var(--line)',
              background: 'transparent',
              color: FG3,
              cursor: 'pointer',
            }}
          >
            <RotateCcw size={15} strokeWidth={1.7} />
          </button>
        </div>

        <div
          data-r="split"
          style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1.08fr) minmax(280px,.92fr)', gap: '0 44px', marginTop: '26px' }}
        >
          <div style={{ minWidth: 0, display: 'flex', flexDirection: 'column', gap: '20px', paddingRight: '44px' }}>
            <label style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
              <span style={monoLabel('9px', '.16em')}>TITLE</span>
              <input
                value={activeTemplate.title}
                maxLength={200}
                onFocus={() => setTemplateField('title')}
                onChange={(event) => updateNotificationTemplate('title', event.target.value)}
                onBlur={(event) => updateNotificationTemplate('title', event.target.value, true)}
                style={{
                  width: '100%',
                  padding: '11px 12px',
                  border: '1px solid var(--line)',
                  background: 'var(--sub)',
                  color: 'var(--fg)',
                  font: "400 13px/1.4 'JetBrains Mono',monospace",
                }}
              />
            </label>
            <label style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
              <span style={monoLabel('9px', '.16em')}>BODY</span>
              <textarea
                value={activeTemplate.body}
                maxLength={4000}
                rows={9}
                onFocus={() => setTemplateField('body')}
                onChange={(event) => updateNotificationTemplate('body', event.target.value)}
                onBlur={(event) => updateNotificationTemplate('body', event.target.value, true)}
                style={{
                  width: '100%',
                  minHeight: '190px',
                  resize: 'vertical',
                  padding: '11px 12px',
                  border: '1px solid var(--line)',
                  background: 'var(--sub)',
                  color: 'var(--fg)',
                  font: "400 12.5px/1.65 'JetBrains Mono',monospace",
                }}
              />
            </label>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '10px' }}>
              <span style={monoLabel('9px', '.16em')}>INSERT VARIABLE · {templateField.toUpperCase()}</span>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: '7px' }}>
                {TEMPLATE_VARIABLES[templateKind].map((variable) => (
                  <button
                    key={variable}
                    type="button"
                    title={`插入到${templateField === 'title' ? '标题' : '正文'}`}
                    onClick={() => appendTemplateVariable(variable)}
                    style={{
                      border: '1px solid var(--line)',
                      background: 'transparent',
                      color: FG2,
                      padding: '7px 9px',
                      font: "400 10.5px/1 'JetBrains Mono',monospace",
                      cursor: 'pointer',
                    }}
                  >
                    {`{{.${variable}}}`}
                  </button>
                ))}
              </div>
            </div>
          </div>

          <div style={{ minWidth: 0, paddingLeft: '44px', borderLeft: '1px solid var(--line)' }}>
            <div style={{ ...monoLabel('9px', '.16em'), marginBottom: '12px' }}>PREVIEW · NON-EMAIL</div>
            <div style={{ border: '1px solid var(--line)', background: 'var(--sub)', padding: '18px', minHeight: '252px' }}>
              <div style={{ fontSize: '15px', fontWeight: 600, lineHeight: 1.45, overflowWrap: 'anywhere' }}>
                {previewTitle || '（标题为空）'}
              </div>
              <div
                style={{
                  height: '1px',
                  background: 'var(--line2)',
                  margin: '14px 0',
                }}
              />
              <div style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', color: FG2, fontSize: '12.5px', lineHeight: 1.7 }}>
                {previewBody || '（正文为空）'}
              </div>
            </div>
          </div>
        </div>
      </section>

      <section style={{ display: 'flex', alignItems: 'center', gap: '18px', padding: '26px 0 0', flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '9px', fontSize: '12.5px', color: s.pushSync === 'error' ? RED : FG3 }}>
          <span
            style={{
              width: '6px',
              height: '6px',
              borderRadius: '999px',
              background: s.pushSync === 'error' ? RED : s.pushSync === 'saving' ? FG3 : OK,
            }}
          />
          {syncLabel} · 下次推送 <span style={{ color: FG2 }}>{nextPush}</span>
        </div>
        <button
          className={resetArmed ? 'hv-red' : 'hv-line-fg'}
          onClick={resetPush}
          onBlur={() => setResetArmed(false)}
          style={{
            margin: '0 0 0 auto',
            padding: '10px 19px',
            borderRadius: '999px',
            font: 'inherit',
            fontSize: '12.5px',
            fontWeight: 500,
            cursor: 'pointer',
            background: resetArmed ? 'var(--redsoft)' : 'none',
            border: `1px solid ${resetArmed ? 'var(--red)' : 'var(--line)'}`,
            color: resetArmed ? 'var(--red)' : 'var(--fg2)',
          }}
        >
          {resetArmed ? '再次点击确认恢复' : '恢复推送默认值'}
        </button>
      </section>
        </>
      )}

      {section === 'account' && (
        <>

      {/* ---- 账号设置 ---- */}
      <section style={{ padding: '44px 0 40px', borderBottom: '1px solid var(--line)' }}>
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: '20px', flexWrap: 'wrap', marginBottom: '28px' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
            <div style={monoLabel('9.5px', '.2em')}>ACCOUNT</div>
            <div style={{ fontSize: '34px', fontWeight: 600, letterSpacing: '-.04em' }}>账号设置</div>
            <div style={{ fontSize: '13px', color: 'var(--fg3)', maxWidth: '520px', textWrap: 'pretty' }}>
              修改昵称、登录邮箱与密码，或更换绑定电表。
            </div>
          </div>
        </div>

        {!s.user ? (
          <div style={{ fontSize: '13px', color: 'var(--fg3)' }}>登录后可管理账号与电表绑定。</div>
        ) : (
          <div
            data-r="split"
            style={{ display: 'grid', gridTemplateColumns: 'minmax(0,1fr) minmax(0,360px)', gap: '44px' }}
          >
          <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0 }}>
            {/* 昵称 */}
            <div style={{ borderTop: '1px solid var(--line2)', padding: '16px 2px' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '16px' }}>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={monoLabel('9px', '.16em')}>NICKNAME</div>
                  <div style={{ fontSize: '15px', fontWeight: 500, marginTop: '8px', letterSpacing: '-.02em' }}>
                    {s.user.name}
                  </div>
                  <div style={{ fontSize: '12px', color: 'var(--fg3)', marginTop: '4px' }}>推送与榜单展示用</div>
                </div>
                <button className="hv-line-fg" onClick={() => openAcc('nick')} style={pillBtn(false)}>
                  {accOpen === 'nick' ? '收起' : '修改'}
                </button>
              </div>
              {accOpen === 'nick' && (
                <div style={{ marginTop: '18px', display: 'flex', flexDirection: 'column', gap: '14px', maxWidth: '360px' }}>
                  <input
                    className="authfield"
                    value={nickDraft}
                    onChange={(e) => setNickDraft(e.target.value)}
                    placeholder="推送称呼"
                    maxLength={80}
                    style={fieldInputStyle}
                  />
                  <div style={{ display: 'flex', gap: '10px' }}>
                    <button
                      className="hv-op82"
                      disabled={accBusy}
                      style={pillBtn(true)}
                      onClick={async () => {
                        setAccBusy(true)
                        const ok = await saveNickname(nickDraft)
                        setAccBusy(false)
                        if (ok) setAccOpen(null)
                      }}
                    >
                      保存昵称
                    </button>
                  </div>
                </div>
              )}
            </div>

            {/* 邮箱 */}
            <div style={{ borderTop: '1px solid var(--line2)', padding: '16px 2px' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '16px' }}>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={monoLabel('9px', '.16em')}>EMAIL</div>
                  <div
                    style={{
                      font: "400 14px/1.4 'JetBrains Mono',monospace",
                      marginTop: '8px',
                      color: 'var(--fg2)',
                    }}
                  >
                    {accountEmailShown}
                  </div>
                  <div style={{ fontSize: '12px', color: 'var(--fg3)', marginTop: '4px' }}>登录与接收推送</div>
                </div>
                <button className="hv-line-fg" onClick={() => openAcc('email')} style={pillBtn(false)}>
                  {accOpen === 'email' ? '收起' : '换绑'}
                </button>
              </div>
              {accOpen === 'email' && (
                <div style={{ marginTop: '18px', display: 'flex', flexDirection: 'column', gap: '16px', maxWidth: '400px' }}>
                  <label style={{ display: 'flex', flexDirection: 'column', gap: '7px' }}>
                    <span style={monoLabel('9px', '.16em')}>NEW EMAIL</span>
                    <input
                      className="authfield"
                      type="email"
                      value={emailDraft}
                      onChange={(e) => setEmailDraft(e.target.value)}
                      placeholder="新邮箱地址"
                      style={fieldInputStyle}
                    />
                  </label>
                  <label style={{ display: 'flex', flexDirection: 'column', gap: '7px' }}>
                    <div style={{ display: 'flex', alignItems: 'baseline', gap: '12px' }}>
                      <span style={monoLabel('9px', '.16em')}>CODE</span>
                      <button
                        className="hv-fg"
                        disabled={accBusy}
                        onClick={async () => {
                          setAccBusy(true)
                          const ok = await sendEmailChangeCode(emailDraft)
                          setAccBusy(false)
                          if (ok) setEmailCodeSent(true)
                        }}
                        style={{
                          margin: '0 0 0 auto',
                          padding: 0,
                          background: 'none',
                          border: 0,
                          font: 'inherit',
                          fontSize: '11.5px',
                          fontWeight: 500,
                          color: 'var(--red)',
                          cursor: 'pointer',
                        }}
                      >
                        {emailCodeSent ? '重新发送' : '发送验证码'}
                      </button>
                    </div>
                    <input
                      className="authfield"
                      value={emailCode}
                      onChange={(e) => setEmailCode(e.target.value)}
                      placeholder="6 位验证码"
                      style={fieldInputStyle}
                    />
                    <span style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>验证码将发送到新邮箱</span>
                  </label>
                  <label style={{ display: 'flex', flexDirection: 'column', gap: '7px' }}>
                    <span style={monoLabel('9px', '.16em')}>PASSWORD</span>
                    <input
                      className="authfield"
                      type="password"
                      value={emailPass}
                      onChange={(e) => setEmailPass(e.target.value)}
                      placeholder="当前登录密码"
                      style={fieldInputStyle}
                    />
                  </label>
                  <button
                    className="hv-op82"
                    disabled={accBusy}
                    style={{ ...pillBtn(true), alignSelf: 'flex-start' }}
                    onClick={async () => {
                      setAccBusy(true)
                      const ok = await changeEmail(emailDraft, emailCode, emailPass)
                      setAccBusy(false)
                      if (ok) setAccOpen(null)
                    }}
                  >
                    确认换绑
                  </button>
                </div>
              )}
            </div>

            {/* 手机号。smsLogin=false 时不展示。代码保留。 */}
            {showPhoneBind && (
              <div style={{ borderTop: '1px solid var(--line2)', padding: '16px 2px' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: '16px' }}>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={monoLabel('9px', '.16em')}>PHONE</div>
                    <div
                      style={{
                        font: "400 14px/1.4 'JetBrains Mono',monospace",
                        marginTop: '8px',
                        color: 'var(--fg2)',
                      }}
                    >
                      未绑定
                    </div>
                    <div style={{ fontSize: '12px', color: 'var(--fg3)', marginTop: '4px' }}>短信登录与预警（可选）</div>
                  </div>
                  <button className="hv-line-fg" onClick={() => openAcc('phone')} style={pillBtn(false)}>
                    {accOpen === 'phone' ? '收起' : '换绑'}
                  </button>
                </div>
                {accOpen === 'phone' && (
                  <div style={{ marginTop: '18px', display: 'flex', flexDirection: 'column', gap: '14px', maxWidth: '360px' }}>
                    <input
                      className="authfield"
                      type="tel"
                      value={phoneDraft}
                      onChange={(e) => setPhoneDraft(e.target.value)}
                      placeholder="11 位手机号"
                      style={fieldInputStyle}
                    />
                    <div style={{ fontSize: '12.5px', color: 'var(--fg3)' }}>
                      短信换绑接口尚未上线。保存后将提示暂不可用。
                    </div>
                    <button
                      className="hv-op82"
                      style={{ ...pillBtn(true), alignSelf: 'flex-start' }}
                      onClick={() => say('短信换绑尚未开放')}
                    >
                      确认换绑
                    </button>
                  </div>
                )}
              </div>
            )}

            {/* 密码 */}
            <div style={{ borderTop: '1px solid var(--line2)', padding: '16px 2px' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '16px' }}>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={monoLabel('9px', '.16em')}>PASSWORD</div>
                  <div style={{ fontSize: '15px', fontWeight: 500, marginTop: '8px', letterSpacing: '.12em' }}>
                    ••••••••
                  </div>
                  <div style={{ fontSize: '12px', color: 'var(--fg3)', marginTop: '4px' }}>修改后需重新登录</div>
                </div>
                <button className="hv-line-fg" onClick={() => openAcc('pass')} style={pillBtn(false)}>
                  {accOpen === 'pass' ? '收起' : '修改'}
                </button>
              </div>
              {accOpen === 'pass' && (
                <div style={{ marginTop: '18px', display: 'flex', flexDirection: 'column', gap: '16px', maxWidth: '360px' }}>
                  <label style={{ display: 'flex', flexDirection: 'column', gap: '7px' }}>
                    <span style={monoLabel('9px', '.16em')}>CURRENT</span>
                    <input
                      className="authfield"
                      type="password"
                      value={curPass}
                      onChange={(e) => setCurPass(e.target.value)}
                      placeholder="当前密码"
                      style={fieldInputStyle}
                    />
                  </label>
                  <label style={{ display: 'flex', flexDirection: 'column', gap: '7px' }}>
                    <span style={monoLabel('9px', '.16em')}>NEW PASSWORD</span>
                    <input
                      className="authfield"
                      type="password"
                      value={newPass}
                      onChange={(e) => setNewPass(e.target.value)}
                      placeholder="至少 8 位"
                      style={fieldInputStyle}
                    />
                  </label>
                  <label style={{ display: 'flex', flexDirection: 'column', gap: '7px' }}>
                    <span style={monoLabel('9px', '.16em')}>CONFIRM</span>
                    <input
                      className="authfield"
                      type="password"
                      value={newPass2}
                      onChange={(e) => setNewPass2(e.target.value)}
                      placeholder="再输入一次"
                      style={fieldInputStyle}
                    />
                  </label>
                  <button
                    className="hv-op82"
                    disabled={accBusy}
                    style={{ ...pillBtn(true), alignSelf: 'flex-start' }}
                    onClick={async () => {
                      if (newPass !== newPass2) return say('两次输入的新密码不一致')
                      setAccBusy(true)
                      const ok = await changePassword(curPass, newPass)
                      setAccBusy(false)
                      if (ok) setAccOpen(null)
                    }}
                  >
                    确认修改
                  </button>
                </div>
              )}
            </div>

            {/* 电表 */}
            <div style={{ borderTop: '1px solid var(--line2)', borderBottom: '1px solid var(--line2)', padding: '16px 2px' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '16px' }}>
                <div style={{ flex: 1, minWidth: 0 }}>
                  <div style={monoLabel('9px', '.16em')}>METER</div>
                  <div
                    style={{
                      font: "400 14px/1.4 'JetBrains Mono',monospace",
                      marginTop: '8px',
                      color: 'var(--fg2)',
                    }}
                  >
                    {s.user.meter || '未绑定'}
                  </div>
                  <div style={{ fontSize: '12px', color: 'var(--fg3)', marginTop: '4px' }}>
                    {s.user.place || '绑定后显示宿舍位置'}
                  </div>
                </div>
                <button
                  className="hv-line-fg"
                  onClick={() => {
                    startBind()
                    say(s.user?.meter ? '请在概览页确认新电表。' : '请在概览页绑定电表。')
                  }}
                  style={pillBtn(false)}
                >
                  {s.user.meter ? '换绑' : '绑定'}
                </button>
              </div>
            </div>

            {/* 注销账号不可逆。与常规项拉开距离。删除范围写在按下前。 */}
            <div style={{ marginTop: '34px', paddingTop: '20px', borderTop: '1px solid var(--line)' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '16px', flexWrap: 'wrap' }}>
                <div style={{ flex: 1, minWidth: '220px' }}>
                  <div style={{ ...monoLabel('9px', '.16em'), color: 'var(--red)' }}>DELETE ACCOUNT</div>
                  <div style={{ fontSize: '15px', fontWeight: 500, marginTop: '8px', letterSpacing: '-.02em' }}>
                    注销账号
                  </div>
                  <div style={{ fontSize: '12px', color: 'var(--fg3)', marginTop: '4px', textWrap: 'pretty' }}>
                    永久删除账号、电表绑定、推送渠道与推送记录。不可恢复。
                  </div>
                </div>
                <button
                  className="hv-red"
                  onClick={() => openAcc('delete')}
                  style={{ ...pillBtn(false), color: accOpen === 'delete' ? 'var(--fg2)' : 'var(--red)' }}
                >
                  {accOpen === 'delete' ? '收起' : '注销账号'}
                </button>
              </div>
              {accOpen === 'delete' && (
                <div
                  style={{
                    marginTop: '18px',
                    padding: '20px',
                    border: '1px solid var(--red)',
                    background: 'var(--redsoft)',
                    display: 'flex',
                    flexDirection: 'column',
                    gap: '16px',
                    maxWidth: '420px',
                  }}
                >
                  <div style={{ fontSize: '12.5px', color: 'var(--fg2)', lineHeight: 1.75, textWrap: 'pretty' }}>
                    <b style={{ color: 'var(--fg)' }}>将删除：</b>账号与登录邮箱、电表绑定、榜单偏好、推送规则与渠道（含密钥）、推送记录。
                    电表绑定解除后，其他账号可绑定该电表。
                    <br />
                    <b style={{ color: 'var(--fg)' }}>不删除：</b>电表抄表读数与月账单。
                    上述数据属于全校用电统计，不属于个人账号。
                  </div>
                  <label style={{ display: 'flex', flexDirection: 'column', gap: '7px' }}>
                    <span style={monoLabel('9px', '.16em')}>PASSWORD</span>
                    <input
                      className="authfield"
                      type="password"
                      value={delPass}
                      onChange={(e) => setDelPass(e.target.value)}
                      placeholder="当前密码"
                      style={fieldInputStyle}
                    />
                  </label>
                  <label style={{ display: 'flex', flexDirection: 'column', gap: '7px' }}>
                    <span style={monoLabel('9px', '.16em')}>CONFIRM · 输入「{DELETE_PHRASE}」</span>
                    <input
                      className="authfield"
                      value={delConfirm}
                      onChange={(e) => setDelConfirm(e.target.value)}
                      placeholder={DELETE_PHRASE}
                      style={fieldInputStyle}
                    />
                  </label>
                  <div style={{ display: 'flex', gap: '10px', alignItems: 'center', flexWrap: 'wrap' }}>
                    <button
                      className="hv-op82"
                      disabled={accBusy || !delPass || delConfirm.trim() !== DELETE_PHRASE}
                      style={{
                        ...pillBtn(true),
                        background: 'var(--red)',
                        borderColor: 'var(--red)',
                        color: '#fff',
                        opacity: accBusy || !delPass || delConfirm.trim() !== DELETE_PHRASE ? 0.45 : 1,
                        cursor:
                          accBusy || !delPass || delConfirm.trim() !== DELETE_PHRASE ? 'not-allowed' : 'pointer',
                      }}
                      onClick={async () => {
                        setAccBusy(true)
                        const ok = await deleteAccount(delPass)
                        setAccBusy(false)
                        if (ok) {
                          setAccOpen(null)
                          setDelPass('')
                          setDelConfirm('')
                        }
                      }}
                    >
                      永久注销
                    </button>
                    <button className="hv-line-fg" onClick={() => openAcc('delete')} style={pillBtn(false)}>
                      取消
                    </button>
                  </div>
                </div>
              )}
            </div>
          </div>

          {/* 右栏账号状态。仅库中已有事实。用 div 以适配窄屏折叠。 */}
          <div style={{ display: 'flex', flexDirection: 'column', minWidth: 0 }}>
            <div style={monoLabel('9px', '.16em')}>ACCOUNT STATUS</div>
            <div
              style={{
                marginTop: '14px',
                border: '1px solid var(--line)',
                padding: '18px 18px 6px',
                display: 'flex',
                flexDirection: 'column',
              }}
            >
              {accountFacts.map((f) => (
                <div
                  key={f.label}
                  style={{
                    display: 'flex',
                    alignItems: 'baseline',
                    gap: '12px',
                    padding: '9px 0 11px',
                    borderBottom: '1px solid var(--line2)',
                  }}
                >
                  <span style={{ fontSize: '12.5px', color: 'var(--fg3)', flex: 'none' }}>{f.label}</span>
                  <span
                    style={{
                      marginLeft: 'auto',
                      textAlign: 'right',
                      fontSize: '12.5px',
                      color: f.tone || 'var(--fg)',
                      fontVariantNumeric: 'tabular-nums',
                    }}
                  >
                    {f.value}
                  </span>
                </div>
              ))}
              <div style={{ padding: '16px 0 12px', display: 'flex', flexDirection: 'column', gap: '10px' }}>
                <button
                  className="hv-line-fg"
                  disabled={accBusy}
                  onClick={async () => {
                    if (!revokeArmed) return setRevokeArmed(true)
                    setAccBusy(true)
                    await revokeSessions()
                    setAccBusy(false)
                    setRevokeArmed(false)
                  }}
                  style={{ ...pillBtn(false), alignSelf: 'flex-start', color: revokeArmed ? 'var(--red)' : undefined }}
                >
                  {revokeArmed ? '确认登出全部？' : '登出所有设备'}
                </button>
              </div>
            </div>
          </div>
          </div>
        )}
      </section>

      <section style={{ padding: '44px 0 0' }}>
        <div style={{ display: 'flex', alignItems: 'flex-start', gap: '20px', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
            <div style={monoLabel('9.5px', '.2em')}>PRIVACY</div>
            <div style={{ fontSize: '34px', fontWeight: 600, letterSpacing: '-.04em' }}>隐私设置</div>
            <div style={{ fontSize: '13px', color: 'var(--fg3)', maxWidth: '460px', textWrap: 'pretty' }}>
              所有电表均参与校内排行。可设置上榜时向其他用户展示哪些字段。
            </div>
          </div>
        </div>

        <div
          data-r="split"
          style={{
            display: 'grid',
            gridTemplateColumns: '1.1fr 1fr',
            marginTop: '32px',
          }}
        >
          <div style={{ display: 'flex', flexDirection: 'column', gap: '11px', paddingRight: '44px' }}>
            <div style={monoLabel('9px', '.18em')}>VISIBLE FIELDS</div>
            <div style={{ display: 'flex', flexDirection: 'column', marginTop: '4px' }}>
              {privFieldDefs.map((f) => {
                const on = !!pfv[f[0]]
                const can = !!f[3]
                const mk = !!pmk[f[0]]
                return (
                  <div
                    key={f[0]}
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: '12px',
                      borderTop: '1px solid var(--line2)',
                      padding: '10px 2px',
                    }}
                  >
                    <button
                      className="hv-op68"
                      onClick={() => setPrivacy({ pf: { ...pfv, [f[0]]: !on } })}
                      style={{
                        flex: 1,
                        minWidth: 0,
                        background: 'none',
                        border: 0,
                        margin: 0,
                        padding: '4px 0',
                        font: 'inherit',
                        textAlign: 'left',
                        cursor: 'pointer',
                        display: 'flex',
                        alignItems: 'center',
                        gap: '13px',
                      }}
                    >
                      <span
                        style={{
                          width: '15px',
                          height: '15px',
                          flex: 'none',
                          border: `1px solid ${on ? 'var(--fg)' : 'var(--line)'}`,
                          background: on ? 'var(--fg)' : 'transparent',
                          color: on ? 'var(--bg)' : 'transparent',
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'center',
                          fontSize: '9px',
                        }}
                      >
                        {on ? '✓' : ''}
                      </span>
                      <span style={{ fontSize: '13px', fontWeight: 500, color: on ? FG : FG3 }}>{f[1]}</span>
                      <span style={{ font: "400 11px/1 'JetBrains Mono',monospace", color: 'var(--fg3)' }}>
                        {sampleOf(f[0], mk)}
                      </span>
                    </button>
                    {can && on && (
                      <div
                        style={{
                          display: 'flex',
                          padding: '2px',
                          border: '1px solid var(--line)',
                          borderRadius: '999px',
                          gap: '2px',
                          flex: 'none',
                        }}
                      >
                        {([
                          [true, '打码'],
                          [false, '完整'],
                        ] as [boolean, string][]).map((t) => (
                          <SegBtn
                            key={t[1]}
                            seg={seg(t[0] === mk, t[1], () => setPrivacy({ pmask: { ...pmk, [f[0]]: t[0] } }))}
                            pad="4px 11px"
                            fs="11.5px"
                          />
                        ))}
                      </div>
                    )}
                    <span
                      style={{
                        width: '28px',
                        flex: 'none',
                        textAlign: 'right',
                        fontSize: '11.5px',
                        color: on ? FG2 : FG3,
                      }}
                    >
                      {on ? '展示' : '隐藏'}
                    </span>
                  </div>
                )
              })}
            </div>
            <div style={{ display: 'flex', gap: '8px', marginTop: '16px' }}>
              <button
                className="hv-line-fg"
                onClick={() => {
                  const pf = { ...pfv }
                  privFieldDefs.forEach((f) => (pf[f[0]] = true))
                  setPrivacy({ pf })
                  say('已全部展示')
                }}
                style={{
                  margin: 0,
                  padding: '10px 18px',
                  border: '1px solid var(--line)',
                  borderRadius: '999px',
                  font: 'inherit',
                  fontSize: '12.5px',
                  fontWeight: 500,
                  cursor: 'pointer',
                  background: 'none',
                  color: 'var(--fg2)',
                }}
              >
                全部展示
              </button>
              <button
                className="hv-line-fg"
                onClick={() => {
                  const pf = { ...pfv }
                  privFieldDefs.forEach((f) => (pf[f[0]] = false))
                  setPrivacy({ pf })
                  say('已全部隐藏。将以匿名身份上榜。')
                }}
                style={{
                  margin: 0,
                  padding: '10px 18px',
                  border: '1px solid var(--line)',
                  borderRadius: '999px',
                  font: 'inherit',
                  fontSize: '12.5px',
                  fontWeight: 500,
                  cursor: 'pointer',
                  background: 'none',
                  color: 'var(--fg2)',
                }}
              >
                全部隐藏
              </button>
            </div>
          </div>
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: '12px',
              paddingLeft: '44px',
              borderLeft: '1px solid var(--line)',
            }}
          >
            <div style={monoLabel('9px', '.18em')}>HOW OTHERS SEE YOU</div>
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '18px',
                padding: '16px 18px',
                background: 'var(--sub)',
                border: '1px solid var(--line)',
                marginTop: '6px',
              }}
            >
              <span style={{ font: "500 13px/1 'JetBrains Mono',monospace", color: 'var(--red)', flex: 'none' }}>
                {privRowRank}
              </span>
              <span
                style={{
                  fontSize: '12.5px',
                  fontWeight: 500,
                  minWidth: 0,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}
              >
                {privRowLabel}
              </span>
              <span
                style={{
                  marginLeft: 'auto',
                  flex: 'none',
                  font: "500 12px/1 'JetBrains Mono',monospace",
                  fontVariantNumeric: 'tabular-nums',
                }}
              >
                {privRowVal}
              </span>
            </div>
            <div style={{ fontSize: '12.5px', color: 'var(--fg3)', lineHeight: 1.65, textWrap: 'pretty' }}>
              {privPreview}
            </div>
            <div style={{ marginTop: 'auto', paddingTop: '22px', display: 'flex', gap: '11px', alignItems: 'flex-start' }}>
              <span style={{ width: '5px', height: '5px', background: 'var(--red)', flex: 'none', marginTop: '7px' }} />
              <span style={{ fontSize: '12.5px', color: 'var(--fg2)', lineHeight: 1.65, textWrap: 'pretty' }}>
                同楼平均与全校房均始终以脱敏聚合值参与计算，不会暴露单个宿舍。
              </span>
            </div>
          </div>
        </div>
      </section>

        </>
      )}
    </div>
  )
}
