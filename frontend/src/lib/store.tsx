/* 全局状态存储。对应原型 DCLogic 的 state 与方法。
   跨视图状态集中于此。切换视图不丢失状态。
   持久化键 localStorage edu-power-config。 */

import { createContext, useContext, useEffect, useRef, useState } from 'react'
import { DEFAULT_USER, DORMS, type User } from './mock'
import { CH_DEFS, DEFAULT_CH, DEFAULT_PF, DEFAULT_PMASK, type ChConfig } from './channels'
import { DEFAULT_FEATURES, isValidAccount, maskAccount, type Features } from '../config/features'
import { canUseAdmin, isAdminView, type AdminView } from './adminAccess'
import { checkEmailDomain } from '../config/emailDomains'
import { METER_HINT, isValidMeter } from './meter'
import { addMonths, currentMonth } from './months'
import { loadPrivacy, savePrivacy, type LocalPrivacy } from '../api/leaderboard'
import {
  loadChannels,
  loadNotificationSettings,
  mergeChannels,
  saveAllChannels,
  saveNotificationSettings,
  saveOneChannel,
  testChannel,
  defaultNotificationTemplates,
  normalizeNotificationTemplates,
  type LocalNotifSettings,
  type LocalNotificationTemplates,
} from '../api/notifications'
import { IS_LIVE } from '../api/mode'
import { api } from '../api/client'
import { errMsg } from '../api/errors'
import {
  bindMeter,
  bootSession,
  deleteAccount as deleteAccountRequest,
  signIn,
  signOut,
  signUp,
  startOAuthSignIn,
  toViewUser,
} from '../api/session'
import type { OAuthProvider } from '../api/client'
import type { User as ApiUser } from '../api/types'

/* 视图一维。管理侧与用户侧共用 view。可见性由 canUseAdmin 决定。 */
export type View =
  | 'overview'
  | 'usage'
  | 'config'
  | 'account'
  | 'campus'
  | 'board'
  | AdminView
export type AuthView = null | 'login' | 'register' | 'forgot'

/** 电表反查结果。仅位置与可绑定状态。禁止含余额。 */
export interface Matched {
  no: string
  place: string
}

export interface FormState {
  meter: string
  phone: string
  pass: string
  pass2: string
  code: string
  nick: string
  agree: boolean
}

export const EMPTY_FORM: FormState = { meter: '', phone: '', pass: '', pass2: '', code: '', nick: '', agree: false }

export interface AppState {
  view: View
  theme: 'light' | 'dark'
  unit: 'kwh' | 'rmb'
  /* 数据看板口径。false=总量。true=户均（合计÷当期有数表数）。
     除数用当期有数表数，禁止用在册表数。 */
  perCap: boolean
  /** 移动端抽屉侧栏是否展开。不持久化。 */
  mobileNavOpen: boolean
  /** 概览用电量图拖拽缩放 [起,止]。全局索引含端点。null=未缩放。 */
  pZoom: [number, number] | null
  /** 数据看板负荷图拖拽缩放区间 */
  cZoom: [number, number] | null
  /** 特性开关。本地默认与运维远程配置合并。 */
  features: Features
  hoverDay: number
  heat: [number, number] | null
  /** 负荷图悬浮索引。不钉住时驱动读数。mouseLeave 清除。 */
  cHover: number | null
  /** 负荷图钉住索引。钉住后读数不受悬浮影响。再点同一柱取消。 */
  cPin: number | null
  /* 楼栋走势多序列三态。身份由聚焦承担。
     cTrendKey=悬浮条。cTrendPin=钉住条。 */
  /** 走势图时间游标列索引。null=读数落在最后有数列。 */
  cTrendCol: number | null
  /**
   * 走势图浮窗是否打开。与 cTrendCol 分离。
   * 关闭浮窗只关 tip。禁止清空 col。
   */
  cTrendTipOpen: boolean
  /** 走势图悬浮聚焦的序列 key（楼栋/楼层名） */
  cTrendKey: string | null
  /** 走势图钉住的序列 key。再点同一行取消。 */
  cTrendPin: string | null
  /** 走势图钉住的时间游标。PC 点击固定。再点同一列取消。 */
  cTrendColPin: number | null
  /** 楼层对比看哪一栋。空=跟随页面范围或最费电那栋。 */
  cCmpBldg: string
  /** 热力图悬浮格子 [行, 列]。驱动浮窗与色阶游标。 */
  cHmCell: [number, number] | null
  /* 热力图色阶映射区间。存峰值比例 0..1，禁止存绝对度数。 */
  cHmLo: number
  cHmHi: number
  /* 数据看板年视图账期窗口 YYYY-MM 含端点。
     主图、构成、楼栋对比、热力图共用。 */
  cFrom: string
  cTo: string
  /** 数据看板月视图自然月 YYYY-MM。年视图双击某月钻入。 */
  cMonth: string
  pRange: 'month' | 'term' | 'year'
  /* 概览用电量账期窗口。与看板 cFrom/cTo/cMonth 同语义、各存各的。 */
  pFrom: string
  pTo: string
  pMonth: string
  /* 概览与数据看板各自选中的学期 YYYY-MM。空串由视图按校历填当前学期。 */
  pTerm: string
  cTerm: string
  cBuilding: number
  calHover: number | null
  range: 'day' | 'month' | 'term' | 'year'
  board: 'top' | 'save' | 'surge' | 'drop'
  bPeriod: 'day' | 'week' | 'month'
  bBldg: string
  bFloor: string
  bMe: boolean
  codeSent: boolean
  bldgScope: string
  floorScope: string
  mKey: string
  lowAlert: boolean
  threshold: number
  schedule: boolean
  period: string
  pushTime: string
  notificationTemplates: LocalNotificationTemplates
  joinBoard: boolean
  pf: Record<string, boolean>
  pmask: Record<string, boolean>
  ch: Record<string, ChConfig>
  open: string | null
  testing: Record<string, string | null>
  /** 推送规则与渠道的自动保存状态。不持久化。 */
  pushSync: 'idle' | 'saving' | 'saved' | 'error'
  toast: string | null
  user: User | null
  menuOpen: boolean
  /** 内联绑表面板是否展开。位于概览 hero 余额位。 */
  bindOpen: boolean
  authView: AuthView
  matched: Matched | null
  form: FormState
  authErr: string | null
  /** 认证或绑表请求在途。按钮置灰防重复提交。 */
  authBusy: boolean
  /** live 启动会话恢复是否已完成。 */
  sessionReady: boolean
}

export const initialState: AppState = {
  view: 'overview',
  theme: 'light',
  unit: 'kwh',
  perCap: false,
  mobileNavOpen: false,
  pZoom: null,
  cZoom: null,
  features: DEFAULT_FEATURES,
  /* ≥99 表示无悬停意图，视图落到最近有数日。 */
  hoverDay: 99,
  heat: null,
  cHover: null,
  cPin: null,
  cTrendCol: null,
  cTrendTipOpen: false,
  cTrendKey: null,
  cTrendPin: null,
  cTrendColPin: null,
  cCmpBldg: '',
  cHmCell: null,
  cHmLo: 0,
  cHmHi: 1,
  // 默认看最近 12 个自然月（含当月）。
  cFrom: addMonths(currentMonth(), -11),
  cTo: currentMonth(),
  cMonth: currentMonth(),
  pRange: 'month',
  pFrom: addMonths(currentMonth(), -11),
  pTo: currentMonth(),
  pMonth: currentMonth(),
  pTerm: '',
  cTerm: '',
  cBuilding: 1,
  calHover: null,
  range: 'month',
  board: 'top',
  bPeriod: 'month',
  bBldg: 'all',
  bFloor: 'all',
  bMe: false,
  codeSent: false,
  bldgScope: 'all',
  floorScope: 'all',
  /* live：默认当前自然月。mock：对齐演示日 2026-07-25（0 基月 = 6）。
     mKey 格式 y-m（m 为 0 基），与 liveMonthKeys / monthKeys 一致。 */
  mKey: IS_LIVE
    ? (() => {
        const d = new Date()
        return d.getFullYear() + '-' + d.getMonth()
      })()
    : '2026-6',
  lowAlert: true,
  threshold: 10,
  // 新号默认关闭定时摘要。用户自行打开。
  schedule: false,
  period: 'daily',
  pushTime: '08:00',
  notificationTemplates: defaultNotificationTemplates(),
  joinBoard: true,
  pf: DEFAULT_PF,
  pmask: DEFAULT_PMASK,
  ch: DEFAULT_CH,
  open: null,
  testing: {},
  pushSync: 'idle',
  toast: null,
  // live 无演示用户。登录态由启动时 refresh cookie 决定。
  user: IS_LIVE ? null : DEFAULT_USER,
  menuOpen: false,
  bindOpen: false,
  authView: null,
  matched: null,
  form: EMPTY_FORM,
  authErr: null,
  authBusy: false,
  sessionReady: !IS_LIVE,
}

type Patch = Partial<AppState> | ((p: AppState) => Partial<AppState>)

export interface Store {
  s: AppState
  /** 榜单参与与脱敏。live 写服务端。mock 只写本地。 */
  setPrivacy: (patch: Partial<LocalPrivacy>) => void
  /** 推送规则。live 写服务端。mock 只写本地。 */
  setNotifSettings: (patch: Partial<LocalNotifSettings>) => void
  /** 恢复推送规则与渠道默认值，并立即同步。 */
  resetPushDefaults: () => void
  /** 渠道开关与字段即时同步。可传入刚改配置，避免 setState 未落 ref。 */
  syncChannel: (id: string, local?: ChConfig) => void
  /** 真实测试发送。 */
  testPushChannel: (id: string, name: string) => void
  /** 账号设置：更新昵称。 */
  saveNickname: (nickname: string) => Promise<boolean>
  /** 账号设置：更新密码。成功后退出登录。 */
  changePassword: (current: string, next: string) => Promise<boolean>
  revokeSessions: () => Promise<boolean>
  /** 账号设置：向新邮箱发码。 */
  sendEmailChangeCode: (email: string) => Promise<boolean>
  /** 账号设置：确认换绑邮箱。 */
  changeEmail: (email: string, code: string, password: string) => Promise<boolean>
  /** 账号设置：注销账号。不可逆。需要当前密码。成功后未登录。 */
  deleteAccount: (password: string) => Promise<boolean>
  set: (patch: Patch) => void
  /** 提示条。ms 可延长停留时间。 */
  say: (msg: string, ms?: number) => void
  persist: (over?: Record<string, unknown>) => void
  applyTheme: (t: string) => void
  setForm: (k: keyof FormState, v: string | boolean) => void
  startBind: () => void
  lookupMeter: (captchaToken?: string) => void
  doLogin: (captchaToken?: string) => void
  /** 第三方登录。live 整页跳转提供方，本次调用不再返回。 */
  doOAuth: (provider: OAuthProvider) => void
  doRegister: () => void
  sendCode: (captchaToken?: string) => void
  doReset: () => void
  doRebind: () => void
  doLogout: () => void
  sendRegisterCode: (captchaToken?: string) => void
}

export function useCreateStore(initialView?: View): Store {
  // initialView 只来自路由（旧书签 /admin）。可见性由 App 守卫再判。
  const [s, setS] = useState<AppState>(initialView ? { ...initialState, view: initialView } : initialState)
  const ref = useRef(s)
  ref.current = s
  const toastTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const pushSyncSeq = useRef(0)

  const set = (patch: Patch) =>
    setS((p) => ({ ...p, ...(typeof patch === 'function' ? patch(p) : patch) }))

  const applyTheme = (t: string) => {
    try {
      document.documentElement.setAttribute('data-theme', t)
    } catch (e) {}
  }

  const persist = (over?: Record<string, unknown>) => {
    const st = ref.current
    const d = Object.assign(
      {
        theme: st.theme,
        unit: st.unit,
        perCap: st.perCap,
        joinBoard: st.joinBoard,
        pf: st.pf,
        pmask: st.pmask,
        lowAlert: st.lowAlert,
        threshold: st.threshold,
        schedule: st.schedule,
        period: st.period,
        pushTime: st.pushTime,
        notificationTemplates: st.notificationTemplates,
        ch: st.ch,
        user: st.user,
      },
      over || {},
    )
    // 浏览器只缓存后端掩码。禁止落盘用户刚输入的真实凭据。
    // 编辑期间保留掩码。刷新后可从 API 再取。
    if (d.ch && typeof d.ch === 'object') {
      const cachedChannels = Object.fromEntries(
        Object.entries(d.ch as Record<string, ChConfig>).map(([id, config]) => [id, { ...config }]),
      )
      for (const def of CH_DEFS) {
        const config = cachedChannels[def.id]
        if (!config) continue
        for (const field of def.fields) {
          if (field.t !== 'password') continue
          const marker = `__secret_${field.k}`
          if (config[marker]) config[field.k] = '••••••••'
          else delete config[field.k]
        }
      }
      d.ch = cachedChannels
    }
    try {
      localStorage.setItem('edu-power-config', JSON.stringify(d))
    } catch (e) {}
  }

  const say = (msg: string, ms = 2200) => {
    clearTimeout(toastTimer.current)
    set({ toast: msg })
    toastTimer.current = setTimeout(() => set({ toast: null }), ms)
  }

  /* 落到当前身份可见的视图。无权限时管理页退回概览。 */
  const viewFor = (view: View, role?: string | null): View =>
    isAdminView(view) && !canUseAdmin(role) ? 'overview' : view

  // 启动时恢复本地配置（对齐原型 componentDidMount）。
  useEffect(() => {
    let saved: any = {}
    try {
      saved = JSON.parse(localStorage.getItem('edu-power-config') || '{}')
    } catch (e) {}
    const next: Partial<AppState> = {}
    if (saved.theme) next.theme = saved.theme
    if (saved.unit) next.unit = saved.unit
    if (typeof saved.perCap === 'boolean') next.perCap = saved.perCap
    if (typeof saved.lowAlert === 'boolean') next.lowAlert = saved.lowAlert
    if (typeof saved.threshold === 'number') next.threshold = Math.min(50, Math.max(1, saved.threshold))
    if (typeof saved.schedule === 'boolean') next.schedule = saved.schedule
    if (saved.period) next.period = saved.period
    if (saved.pushTime) next.pushTime = saved.pushTime
    if (saved.notificationTemplates) next.notificationTemplates = normalizeNotificationTemplates(saved.notificationTemplates)
    // 强制参与排行。忽略本地旧的 joinBoard: false。
    next.joinBoard = true
    if (saved.pf) next.pf = Object.assign({}, DEFAULT_PF, saved.pf)
    if (saved.pmask) next.pmask = Object.assign({}, DEFAULT_PMASK, saved.pmask)
    if (saved.ch) next.ch = Object.assign({}, DEFAULT_CH, saved.ch)
    /* mock：登录态存在 localStorage。
       live：localStorage 的 user 仅回显。真伪由 refresh cookie 决定。
       先按未登录渲染，等 bootSession 再落定。禁止假登录态。 */
    if (!IS_LIVE && 'user' in saved) {
      next.user = saved.user
      if (!saved.user) next.view = 'campus'
    }
    applyTheme(next.theme || 'light')
    if (Object.keys(next).length) set(next)

    if (IS_LIVE) {
      bootSession().then(
        (u) => {
          set((p) =>
            u
              ? { user: u, sessionReady: true, view: viewFor(p.view, u.role) }
              : { user: null, sessionReady: true, view: 'campus' },
          )
          if (u) {
            syncPrivacy()
            syncPushPrefs()
          }
        },
        () => set({ user: null, sessionReady: true, view: 'campus' }),
      )
    }
    return () => clearTimeout(toastTimer.current)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const setForm = (k: keyof FormState, v: string | boolean) =>
    set((p) => ({ form: { ...p.form, [k]: v } as FormState, authErr: null }))

  /** 绑表或换表统一入口。回到概览并在 hero 展开绑表面板。 */
  const startBind = () =>
    set((p) => ({
      view: 'overview',
      bindOpen: true,
      matched: null,
      authErr: null,
      menuOpen: false,
      mobileNavOpen: false,
      form: { ...p.form, meter: '' },
    }))

  /* mock 电表反查。按原型算式推地址。 */
  const mockPlace = (m: string) => {
    if (m === DEFAULT_USER.meter) return DEFAULT_USER.place!
    const i = Number(m.slice(-3)) % DORMS.length
    const floor = 3 + (Number(m.slice(-1)) % 4)
    return DORMS[i] + ' ' + floor + '0' + ((Number(m.slice(-2)) % 9) + 1) + ' 室'
  }

  const lookupMeter = (captchaToken?: string) => {
    const m = (ref.current.form.meter || '').trim()
    if (!isValidMeter(m)) return set({ authErr: METER_HINT })
    if (!IS_LIVE) return set({ matched: { no: m, place: mockPlace(m) }, authErr: null })
    // live：meter/preview 只回位置与可绑定状态。禁止含余额。
    set({ authBusy: true, authErr: null })
    api.meterPreview(m, captchaToken).then(
      (p) => {
        if (!p.claimable) {
          const why =
            p.reason === 'already_bound'
              ? '该电表绑定名额已满，最多 4 个账号。'
              : p.reason === 'unsupported_building'
                ? '该楼栋暂不支持绑定。'
              : p.reason === 'excluded'
                ? '该电表不在推送范围内。公共区域或已停用。'
                : '该电表当前不可绑定'
          return set({ authBusy: false, matched: null, authErr: why })
        }
        set({ authBusy: false, matched: { no: p.meter, place: `${p.building} ${p.room} 室` }, authErr: null })
      },
      (e) => set({ authBusy: false, matched: null, authErr: errMsg(e) }),
    )
  }

  const doLogin = (captchaToken?: string) => {
    const f = ref.current.form
    const a = ref.current.features.auth
    if (ref.current.authBusy) return
    if (!f.phone.trim())
      return set({ authErr: a.smsLogin && a.emailLogin ? '请输入手机号或邮箱' : a.smsLogin ? '请输入手机号' : '请输入邮箱' })
    if (f.pass.length < 6) return set({ authErr: '密码至少 6 位' })
    if (!IS_LIVE) {
      const u = DEFAULT_USER
      set({ user: u, authView: null, authErr: null, form: EMPTY_FORM })
      persist({ user: u })
      return say('欢迎回来，' + u.name)
    }
    set({ authBusy: true, authErr: null })
    signIn(f.phone, f.pass, captchaToken).then(
      (u) => {
        /* 登录后落概览。绑表 CTA 在 hero。
           例外：从 /admin 进入且有权限时留在管理侧。 */
        const landing = isAdminView(ref.current.view) && canUseAdmin(u.role) ? ref.current.view : 'overview'
        set({ user: u, authView: null, authErr: null, authBusy: false, view: landing, form: EMPTY_FORM })
        persist({ user: u })
        syncPrivacy()
        syncPushPrefs()
        say(u.meter ? '欢迎回来，' + u.name : '欢迎回来。请先绑定宿舍电表。')
      },
      (e) => set({ authBusy: false, authErr: errMsg(e) }),
    )
  }

  /* 注册只创建账号，不绑电表。绑表在登录后概览页。 */
  const doRegister = () => {
    const f = ref.current.form
    const a = ref.current.features.auth
    if (ref.current.authBusy) return
    const acc = (f.phone || '').trim()
    const chk = isValidAccount(a, acc)
    if (!chk.ok) return set({ authErr: chk.msg })
    // 邮箱域名白名单。纯前端校验，不发后端。
    if (acc.indexOf('@') >= 0) {
      const d = checkEmailDomain(acc)
      if (!d.ok) return set({ authErr: d.msg })
    }
    if (f.pass.length < 8) return set({ authErr: '密码至少 8 位，请同时使用字母和数字' })
    if (f.pass !== f.pass2) return set({ authErr: '两次输入的密码不一致' })
    if (!f.agree) return set({ authErr: '请先阅读并同意数据使用说明' })
    // live 必须先验证邮箱归属。
    if (IS_LIVE && !/^\d{6}$/.test((f.code || '').trim()))
      return set({ authErr: ref.current.codeSent ? '请输入邮件中的 6 位验证码' : '请先获取邮箱验证码' })
    if (!IS_LIVE) {
      const tail = /^\d+$/.test(acc.slice(-4)) ? acc.slice(-4) : acc.split('@')[0].slice(0, 4)
      const nick = (f.nick || '').trim() || '同学' + tail
      const u: User = { name: nick, initial: nick.slice(0, 1), meter: null, place: null, phone: maskAccount(acc) }
      set({
        user: u,
        authView: null,
        matched: null,
        authErr: null,
        view: 'overview',
        form: { ...EMPTY_FORM, phone: acc },
      })
      persist({ user: u })
      return say('注册成功 · 请先绑定宿舍电表')
    }
    set({ authBusy: true, authErr: null })
    signUp(acc, f.pass, f.code, f.nick).then(
      (u) => {
        set({
          user: u,
          authView: null,
          matched: null,
          authErr: null,
          authBusy: false,
          view: 'overview',
          // 注册成功并自动登录。保留已验证邮箱。
          form: { ...EMPTY_FORM, phone: acc },
        })
        persist({ user: u })
        syncPushPrefs()
        say('注册成功 · 请先绑定宿舍电表')
      },
      (e) => set({ authBusy: false, authErr: errMsg(e) }),
    )
  }

  /*
  第三方登录。live 会整页离开。结果由回跳 ?oauth= 说明。
  mock 无真实提供方，直接落到演示账号。
  */
  const doOAuth = (provider: OAuthProvider) => {
    if (ref.current.authBusy) return
    if (!IS_LIVE) {
      const u = DEFAULT_USER
      set({ user: u, authView: null, authErr: null, form: EMPTY_FORM })
      persist({ user: u })
      return say('欢迎回来，' + u.name)
    }
    set({ authBusy: true, authErr: null })
    /* 管理员从 /admin 进入则回管理侧。其余回概览。 */
    const next = isAdminView(ref.current.view) ? '/admin' : '/'
    startOAuthSignIn(provider, next)
  }

  /** 发注册验证码。live 走 register/code。mock 只提示。 */
  const sendRegisterCode = (captchaToken?: string) => {
    const f = ref.current.form
    const a = ref.current.features.auth
    const acc = (f.phone || '').trim()
    const chk = isValidAccount(a, acc)
    if (!chk.ok) return set({ authErr: chk.msg })
    if (acc.indexOf('@') >= 0) {
      const d = checkEmailDomain(acc)
      if (!d.ok) return set({ authErr: d.msg })
    }
    if (!IS_LIVE) {
      set({ codeSent: true, authErr: null })
      return say('验证码已发送至邮箱，5 分钟内有效')
    }
    if (ref.current.authBusy) return
    set({ authBusy: true, authErr: null })
    api.registerCode(acc, captchaToken).then(
      () => {
        set({ codeSent: true, authBusy: false, authErr: null })
        say('验证码已发送至邮箱。5 分钟内有效。')
      },
      (e) => set({ authBusy: false, authErr: errMsg(e) }),
    )
  }

  /* 找回密码。live 走 password/forgot，恒 204，不枚举账号。
     mock 只提示已发送。失败时 errMsg 如实展示。 */
  const sendCode = (captchaToken?: string) => {
    const f = ref.current.form
    const a = ref.current.features.auth
    if (!f.phone.trim())
      return set({ authErr: a.smsLogin ? '请先填写手机号或邮箱' : '请先填写邮箱' })
    if (!IS_LIVE) {
      set({ codeSent: true, authErr: null })
      return say(a.smsCode ? '验证码已发送。5 分钟内有效。' : '验证码已发送至邮箱。5 分钟内有效。')
    }
    set({ authBusy: true, authErr: null })
    api.passwordForgot(f.phone.trim().toLowerCase(), captchaToken).then(
      () => {
        set({ codeSent: true, authBusy: false, authErr: null })
        say('验证码已发送至邮箱。5 分钟内有效。')
      },
      (e) => set({ authBusy: false, codeSent: false, authErr: errMsg(e) }),
    )
  }

  const doReset = () => {
    const f = ref.current.form
    const a = ref.current.features.auth
    if (!f.phone.trim())
      return set({ authErr: a.smsLogin && a.emailLogin ? '请输入手机号或邮箱' : a.smsLogin ? '请输入手机号' : '请输入邮箱' })
    if (!/^\d{6}$/.test((f.code || '').trim())) return set({ authErr: '请输入 6 位验证码' })
    if ((f.pass || '').length < 8) return set({ authErr: '新密码至少 8 位' })
    if (!IS_LIVE) {
      set({ authView: 'login', authErr: null, codeSent: false, form: EMPTY_FORM })
      return say('密码已重置。请用新密码登录。')
    }
    set({ authBusy: true, authErr: null })
    api
      .passwordReset({ email: f.phone.trim().toLowerCase(), code: f.code.trim(), password: f.pass })
      .then(
        () => {
          set({ authView: 'login', authErr: null, authBusy: false, codeSent: false, form: EMPTY_FORM })
          say('密码已重置。请用新密码登录。')
        },
        (e) => set({ authBusy: false, authErr: errMsg(e) }),
      )
  }

  /* 首次绑表与换绑同走 PUT /me/meter。文案按是否已绑区分。 */
  const doRebind = () => {
    const m = ref.current.matched
    const cur = ref.current.user
    if (!m || !cur || ref.current.authBusy) return
    const first = !cur.meter
    if (!IS_LIVE) {
      const u = { ...cur, meter: m.no, place: m.place }
      set({ user: u, bindOpen: false, matched: null, form: EMPTY_FORM })
      persist({ user: u })
      return say(first ? '已绑定电表 ' + m.no + '。请开通推送渠道。' : '已绑定新电表 ' + m.no)
    }
    set({ authBusy: true, authErr: null })
    bindMeter(m.no).then(
      (u) => {
        set({ user: u, bindOpen: false, matched: null, authBusy: false, authErr: null, form: EMPTY_FORM })
        persist({ user: u })
        say(first ? '已绑定电表 ' + m.no + '。请开通推送渠道。' : '已绑定新电表 ' + m.no)
      },
      (e) => set({ authBusy: false, authErr: errMsg(e) }),
    )
  }

  /** 登录后查询服务端榜单偏好并覆盖本地。服务端为权威。 */
  const syncPrivacy = () => {
    loadPrivacy().then((p) => {
      if (!p) return
      set(p)
      persist(p)
    })
  }

  /** 登录后查询推送规则与渠道。服务端为权威。localStorage 仅离线兜底。 */
  const syncPushPrefs = () => {
    if (!IS_LIVE) return
    Promise.all([loadNotificationSettings(), api.me().then((u) => u.email).catch(() => null)]).then(
      ([settings, email]) => {
        if (settings) {
          set(settings)
          persist({ ...settings })
        }
        loadChannels(email).then((ch) => {
          if (!ch) return
          set({ ch })
          persist({ ch })
        })
      },
    )
  }

  /* 隐私开关：先乐观更新界面，再 PUT 后端。失败提示未同步并回滚。 */
  const setPrivacy = (patch: Partial<LocalPrivacy>) => {
    const before: LocalPrivacy = { joinBoard: true, pf: ref.current.pf, pmask: ref.current.pmask }
    const next: LocalPrivacy = {
      joinBoard: true,
      pf: patch.pf ?? before.pf,
      pmask: patch.pmask ?? before.pmask,
    }
    set(next)
    persist(next)
    if (!IS_LIVE || !ref.current.user) return
    savePrivacy(next).then((saved) => {
      if (saved) {
        set(saved)
        persist(saved)
        return
      }
      set(before)
      persist(before)
      say('隐私设置同步失败。已还原。')
    })
  }

  /* 推送规则：乐观更新。live 写服务端。 */
  const setNotifSettings = (patch: Partial<LocalNotifSettings>) => {
    const before: LocalNotifSettings = {
      lowAlert: ref.current.lowAlert,
      threshold: ref.current.threshold,
      schedule: ref.current.schedule,
      period: ref.current.period,
      pushTime: ref.current.pushTime,
      notificationTemplates: ref.current.notificationTemplates,
    }
    const next: LocalNotifSettings = { ...before, ...patch }
    set(next)
    persist({ ...next })
    if (!IS_LIVE || !ref.current.user) {
      set({ pushSync: 'saved' })
      return
    }
    const seq = ++pushSyncSeq.current
    set({ pushSync: 'saving' })
    saveNotificationSettings(next).then((saved) => {
      // 字段可连续失焦并发保存。旧响应禁止覆盖更新的草稿。
      if (seq !== pushSyncSeq.current) return
      if (saved) {
        set(saved)
        persist({ ...saved })
        set({ pushSync: 'saved' })
        return
      }
      set(before)
      persist({ ...before })
      set({ pushSync: 'error' })
      say('推送规则同步失败。已还原。')
    })
  }

  const resetPushDefaults = () => {
    const st = ref.current
    const settings: LocalNotifSettings = {
      lowAlert: true,
      threshold: 10,
      schedule: false,
      period: 'daily',
      pushTime: '08:00',
      notificationTemplates: defaultNotificationTemplates(),
    }
    const ch = mergeChannels([], st.user?.email)
    set({ ...settings, ch, open: null })
    persist({ ...settings, ch })
    if (!IS_LIVE || !st.user) {
      set({ pushSync: 'saved' })
      say('已恢复推送默认值')
      return
    }
    const seq = ++pushSyncSeq.current
    set({ pushSync: 'saving' })
    Promise.all([saveNotificationSettings(settings), saveAllChannels(ch)]).then(([sSaved, chSaved]) => {
      if (sSaved) set(sSaved)
      if (chSaved) set({ ch: chSaved })
      if (seq === pushSyncSeq.current) set({ pushSync: sSaved && chSaved ? 'saved' : 'error' })
      if (sSaved && chSaved) say('已恢复推送默认值并同步')
      else say('默认值未能完整同步。请检查网络后重试。')
    })
  }

  const syncChannel = (id: string, local?: ChConfig) => {
    const cfg = local || ref.current.ch[id]
    if (!cfg) return
    const ch = { ...ref.current.ch, [id]: cfg }
    set({ ch })
    persist({ ch })
    if (!IS_LIVE || !ref.current.user) {
      set({ pushSync: 'saved' })
      return
    }
    const seq = ++pushSyncSeq.current
    set({ pushSync: 'saving' })
    saveOneChannel(id, cfg)
      .then((saved) => {
        if (!saved) {
          if (seq === pushSyncSeq.current) set({ pushSync: 'error' })
          say(id + ' 渠道同步失败')
          return
        }
        set((p) => ({ ch: { ...p.ch, [id]: saved } }))
        if (seq === pushSyncSeq.current) set({ pushSync: 'saved' })
      })
      .catch((error) => {
        if (seq === pushSyncSeq.current) set({ pushSync: 'error' })
        say(id + ' 渠道保存失败。' + errMsg(error))
      })
  }

  const testPushChannel = (id: string, name: string) => {
    set((p) => ({ testing: { ...p.testing, [id]: 'ing' } }))
    if (!IS_LIVE) {
      setTimeout(() => {
        set((p) => ({ testing: { ...p.testing, [id]: 'ok' } }))
        say(name + ' 测试消息已送达')
      }, 900)
      setTimeout(() => set((p) => ({ testing: { ...p.testing, [id]: null } })), 3600)
      return
    }
    // 测试前先推当前编辑配置。禁止测旧凭证。
    const local = ref.current.ch[id]
    const ensure = local && IS_LIVE ? saveOneChannel(id, local) : Promise.resolve(null)
    ensure
      .then((saved) => {
        if (IS_LIVE && local && !saved) throw new Error('当前渠道配置未能保存')
        if (saved) set((p) => ({ ch: { ...p.ch, [id]: saved } }))
        return testChannel(id)
      })
      .then((result) => {
        if (!result) {
          set((p) => ({ testing: { ...p.testing, [id]: null } }))
          return say(name + ' 测试失败')
        }
        if (result.status === 'ok') {
          set((p) => ({ testing: { ...p.testing, [id]: 'ok' } }))
          const outcome = result.message ? ' 测试请求已受理。' + result.message : ' 测试消息已送达'
          say(name + outcome + (result.latency_ms != null ? ` · ${result.latency_ms}ms` : ''))
          if (id === 'telegram') {
            loadChannels(ref.current.user?.email).then((channels) => {
              if (channels) set({ ch: channels })
            })
          }
        } else {
          set((p) => ({ testing: { ...p.testing, [id]: null } }))
          say(name + ' 测试失败' + (result.message ? '。' + result.message : ''))
        }
        setTimeout(() => set((p) => ({ testing: { ...p.testing, [id]: null } })), 3600)
      })
      .catch((e) => {
        set((p) => ({ testing: { ...p.testing, [id]: null } }))
        say(name + ' 测试失败。' + errMsg(e))
      })
  }

  /** 登出。live 先吊销 refresh family，再清本地态。 */
  const doLogout = () => {
    const finish = () => {
      set({ user: null, menuOpen: false, mobileNavOpen: false, bindOpen: false, matched: null, view: 'campus' })
      persist({ user: null })
      say('已退出登录')
    }
    if (!IS_LIVE) return finish()
    signOut().then(finish, finish)
  }

  const adoptApiUser = (raw: ApiUser) => {
    const u = toViewUser(raw)
    set({ user: u })
    persist({ user: u })
    return u
  }

  const saveNickname = async (nickname: string): Promise<boolean> => {
    const nick = nickname.trim()
    if (!nick) {
      say('昵称不能为空')
      return false
    }
    if (!IS_LIVE) {
      const cur = ref.current.user
      if (!cur) return false
      const u = { ...cur, name: nick, initial: nick.slice(0, 1) }
      set({ user: u })
      persist({ user: u })
      say('昵称已更新')
      return true
    }
    try {
      adoptApiUser(await api.updateProfile({ nickname: nick }))
      say('昵称已更新')
      return true
    } catch (e) {
      say(errMsg(e))
      return false
    }
  }

  const changePassword = async (current: string, next: string): Promise<boolean> => {
    if (next.length < 8) {
      say('新密码至少 8 位')
      return false
    }
    if (!IS_LIVE) {
      say('密码已修改。请重新登录。')
      doLogout()
      return true
    }
    try {
      await api.changePassword({ current_password: current, new_password: next })
      say('密码已修改。请重新登录。')
      doLogout()
      return true
    } catch (e) {
      say(errMsg(e))
      return false
    }
  }

  /** 登出全部设备（含本机）。成功后回到未登录。 */
  const revokeSessions = async (): Promise<boolean> => {
    if (!IS_LIVE) {
      say('已登出所有设备（演示）')
      doLogout()
      return true
    }
    try {
      const { revoked } = await api.revokeSessions()
      say('已登出 ' + revoked + ' 个会话。请重新登录。')
      doLogout()
      return true
    } catch (e) {
      say(errMsg(e))
      return false
    }
  }

  const sendEmailChangeCode = async (email: string): Promise<boolean> => {
    const e = email.trim().toLowerCase()
    if (!e.includes('@')) {
      say('请输入有效邮箱')
      return false
    }
    if (!IS_LIVE) {
      say('验证码已发送至新邮箱（演示）')
      return true
    }
    try {
      await api.emailChangeCode(e)
      say('验证码已发送至新邮箱。5 分钟内有效。')
      return true
    } catch (err) {
      say(errMsg(err))
      return false
    }
  }

  const changeEmail = async (email: string, code: string, password: string): Promise<boolean> => {
    if (!IS_LIVE) {
      const cur = ref.current.user
      if (!cur) return false
      const nextEmail = email.trim().toLowerCase()
      const u = { ...cur, email: nextEmail, phone: maskAccount(email) }
      const recipients = Array.isArray(ref.current.ch.mail?.to)
        ? ref.current.ch.mail.to.map((value) => value === cur.email ? nextEmail : value)
        : [nextEmail]
      const ch = { ...ref.current.ch, mail: { ...ref.current.ch.mail, to: recipients } }
      set({ user: u, ch })
      persist({ user: u, ch })
      say('登录邮箱已更新')
      return true
    }
    try {
      const user = adoptApiUser(await api.changeEmail({
        email: email.trim().toLowerCase(),
        code: code.trim(),
        password,
      }))
      const channels = await loadChannels(user.email)
      if (channels) {
        set({ ch: channels })
        persist({ ch: channels })
      }
      say('登录邮箱已更新')
      return true
    } catch (e) {
      say(errMsg(e))
      return false
    }
  }

  /* 注销账号。清除本地登录态与 localStorage 中的 user 配置。 */
  const deleteAccount = async (password: string): Promise<boolean> => {
    if (!IS_LIVE) {
      doLogout()
      say('账号已注销（演示）')
      return true
    }
    if (!password) {
      say('请输入当前密码')
      return false
    }
    try {
      await deleteAccountRequest(password)
    } catch (e) {
      say(errMsg(e))
      return false
    }
    try {
      localStorage.removeItem('edu-power-config')
    } catch (err) {}
    set({
      user: null,
      menuOpen: false,
      mobileNavOpen: false,
      bindOpen: false,
      matched: null,
      view: 'campus',
      form: EMPTY_FORM,
    })
    say('账号已注销')
    return true
  }

  return {
    s,
    set,
    say,
    persist,
    applyTheme,
    setForm,
    startBind,
    lookupMeter,
    doLogin,
    doOAuth,
    doRegister,
    sendCode,
    doReset,
    doRebind,
    doLogout,
    sendRegisterCode,
    setPrivacy,
    setNotifSettings,
    resetPushDefaults,
    syncChannel,
    testPushChannel,
    saveNickname,
    changePassword,
    revokeSessions,
    sendEmailChangeCode,
    changeEmail,
    deleteAccount,
  }
}

export const StoreCtx = createContext<Store>(null as unknown as Store)
export const useStore = () => useContext(StoreCtx)
