/* 登录、注册、找回密码全屏浮层。对齐原型。
   绑表不走此处。电表号内联在概览 hero（InlineBindMeter）。
   认证方式按 features.auth 开关渲染。无短信时仅显示邮箱。 */

import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useStore, EMPTY_FORM, type FormState } from '../lib/store'
import { accountField, enabledOAuthProviders, isValidAccount } from '../config/features'
import { checkEmailDomain } from '../config/emailDomains'
import { BrandMark } from './ui'
import CaptchaGate from './CaptchaGate'
import OAuthButtons from './OAuthButtons'
import type { CaptchaAction } from '../api/client'

interface Fld {
  k: keyof FormState
  en: string
  ph: string
  note: string
  t: string
  act: string
  onAct: (() => void) | null
}

export default function AuthOverlay() {
  const store = useStore()
  const { s, set, setForm, doLogin, doRegister, sendCode, doReset, sendRegisterCode } = store
  const [visiblePasswords, setVisiblePasswords] = useState<Partial<Record<keyof FormState, boolean>>>({})
  const [isExiting, setIsExiting] = useState(false)
  const [regStep, setRegStep] = useState<1 | 2>(1)
  const [regMode, setRegMode] = useState<'email' | 'phone'>('email')
  const [animKey, setAnimKey] = useState(0)
  const [captchaReset, setCaptchaReset] = useState(0)
  const [captchaState, setCaptchaState] = useState({ required: false, ready: false, token: '' })
  /* 退场：先播动画，220ms 后再改全局 store。定时器必须可取消。
     卸载后再 set 会清掉 authView，覆盖期间别处刚设的视图。
     连点两次关闭会叠两个定时器。 */
  const exitTimer = useRef<ReturnType<typeof setTimeout> | null>(null)
  useEffect(
    () => () => {
      if (exitTimer.current) clearTimeout(exitTimer.current)
    },
    [],
  )

  const V = s.authView
  useEffect(() => setVisiblePasswords({}), [V])

  useEffect(() => {
    if (V === 'register') {
      setRegStep(1)
      setRegMode('email')
    }
  }, [V])

  if (!V && !isExiting) return null

  const U = s.user
  const F = s.form
  const isLogin = V === 'login'
  const isReg = V === 'register'
  const isFg = V === 'forgot'
  const AF = s.features.auth
  const acct = accountField(AF)
  const captchaAction: CaptchaAction = isLogin
    ? 'auth.login'
    : isFg
      ? 'auth.password_reset_code'
      : 'auth.register_code'

  const consumeCaptcha = () => {
    const token = captchaState.token
    if (captchaState.required) setCaptchaReset((value) => value + 1)
    return token
  }

  const requireCaptchaReady = () => {
    if (captchaState.ready) return true
    set({ authErr: '请先完成人机验证' })
    return false
  }

  const fld = (
    k: keyof FormState,
    en: string,
    ph: string,
    note?: string,
    t?: string,
    act?: string,
    onAct?: () => void,
  ): Fld => ({ k, en, ph, note: note || '', t: t || 'text', act: act || '', onAct: onAct || null })

  const showForgot = AF.emailLogin || AF.smsCode
  /* 第三方登录仅出现在登录页与注册第一步。
     找回密码走邮箱验证码，禁止摆第三方按钮。
     无提供方时整块不渲染。 */
  const showOAuth = (isLogin || (isReg && regStep === 1)) && enabledOAuthProviders(s.features).length > 0

  let fields: Fld[] = []
  if (isLogin) {
    fields = [
      fld('phone', acct.label, acct.ph),
      showForgot
        ? fld('pass', 'PASSWORD', '••••••••', '', 'password', '忘记密码？', () => {
            setAnimKey((k) => k + 1)
            set({
              authView: 'forgot',
              authErr: null,
              codeSent: false,
              form: { ...F, pass: '', pass2: '', code: '' },
            })
          })
        : fld('pass', 'PASSWORD', '••••••••', '', 'password'),
    ]
  } else if (isFg) {
    fields = [
      fld('phone', acct.label, acct.ph),
      fld(
        'code',
        'CODE',
        '6 位验证码',
        AF.smsCode ? '' : '验证码将发送到邮箱',
        'text',
        s.codeSent ? '重新发送' : '发送验证码',
        () => {
          if (requireCaptchaReady()) sendCode(consumeCaptcha())
        },
      ),
      fld('pass', 'NEW PASSWORD', '新密码', '', 'password'),
    ]
  } else if (isReg) {
    if (regStep === 1) {
      const isEmail = regMode === 'email'
      fields = [
        fld(
          'phone',
          isEmail ? 'EMAIL' : 'PHONE',
          isEmail ? 'your-name@domain.com' : '11 位手机号',
          isEmail ? '用于接收验证码、能耗推送与找回密码' : '用于接收验证码与找回密码',
          'text',
          AF.smsLogin
            ? isEmail
              ? '改用手机号注册'
              : '改用邮箱注册'
            : undefined,
          AF.smsLogin
            ? () => {
                setRegMode(isEmail ? 'phone' : 'email')
                setForm('phone', '')
              }
            : undefined,
        ),
        fld(
          'code',
          'VERIFICATION CODE',
          '6 位验证码',
          isEmail ? '验证码将发送到输入的邮箱' : '验证码将发送到输入的手机号',
          'text',
          s.codeSent ? '重新发送' : '发送验证码',
          () => {
            if (requireCaptchaReady()) sendRegisterCode(consumeCaptcha())
          },
        ),
      ]
    } else {
      fields = [
        fld('nick', 'NICKNAME', '推送显示名，可留空'),
        fld('pass', 'PASSWORD', '设置 8 位以上密码', '', 'password'),
        fld('pass2', 'CONFIRM PASSWORD', '再输入一次密码', '', 'password'),
      ]
    }
  }

  const authStepEn = isLogin
    ? 'SIGN IN'
    : isFg
      ? 'RESET PASSWORD'
      : regStep === 1
        ? 'CREATE ACCOUNT · STEP 1/2'
        : 'CREATE ACCOUNT · STEP 2/2'

  const authTitle = isLogin
    ? '登录'
    : isFg
      ? '重置密码'
      : '创建账号'

  const authSub = isLogin
    ? '查看余额、用电曲线与推送记录。'
    : isFg
      ? AF.smsCode
        ? '验证手机号后设置新密码。'
        : '通过邮箱验证码设置新密码。'
      : regStep === 1
        ? regMode === 'email'
          ? '第一步：验证账号 · 输入邮箱地址获取验证码。'
          : '第一步：验证账号 · 输入手机号获取验证码。'
        : '第二步：设置密码 · 请设置登录密码与名称。'

  const authCta = isLogin
    ? '登录'
    : isFg
      ? '重置密码'
      : regStep === 1
        ? '下一步'
        : '完成注册'

  const navBackLabel = isFg ? '返回登录' : U ? '返回面板' : '返回全校数据'
  const authAltText = isLogin
    ? AF.registration
      ? '还没有账号？'
      : '注册暂未开放'
    : isFg
      ? '已想起密码？'
      : '已有账号？'
  const authAltCta = isLogin ? (AF.registration ? '注册新账号' : '') : isFg ? '直接登录' : '去登录'

  const handleNextStep = () => {
    const acc = (F.phone || '').trim()
    const chk = isValidAccount(AF, acc)
    if (!chk.ok) return set({ authErr: chk.msg })
    if (acc.indexOf('@') >= 0) {
      const d = checkEmailDomain(acc)
      if (!d.ok) return set({ authErr: d.msg })
    }
    const codeVal = (F.code || '').trim()
    if (!/^\d{6}$/.test(codeVal)) {
      return set({ authErr: s.codeSent ? '请输入邮件中的 6 位验证码' : '请先获取并输入 6 位验证码' })
    }
    set({ authErr: null })
    setRegStep(2)
    setAnimKey((k) => k + 1)
  }

  const authSubmit = () => {
    if (isLogin) {
      if (!requireCaptchaReady()) return
      return doLogin(consumeCaptcha())
    }
    if (isFg) return doReset()
    if (isReg) {
      if (regStep === 1) {
        return handleNextStep()
      }
      return doRegister()
    }
  }

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!s.authBusy) authSubmit()
  }

  const navBack = () => {
    if (isFg) {
      setAnimKey((k) => k + 1)
      return set({ authView: 'login', authErr: null, codeSent: false })
    }
    if (isReg && regStep === 2) {
      setRegStep(1)
      setAnimKey((k) => k + 1)
      set({ authErr: null })
      return
    }
    // 启动退场动画
    if (exitTimer.current) return
    setIsExiting(true)
    exitTimer.current = setTimeout(() => {
      exitTimer.current = null
      setIsExiting(false)
      set({
        authView: null,
        matched: null,
        authErr: null,
        view: !U && ['overview', 'usage', 'config'].indexOf(s.view) >= 0 ? 'campus' : s.view,
      })
    }, 220)
  }

  const authAlt = () => {
    setAnimKey((k) => k + 1)
    if (isLogin) {
      setRegStep(1)
    }
    set({
      authView: isLogin ? 'register' : 'login',
      matched: null,
      authErr: null,
      codeSent: false,
      form: { ...EMPTY_FORM, phone: F.phone },
    })
  }

  return (
    <div
      className={isExiting ? 'auth-overlay-out' : 'auth-overlay-in'}
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: 90,
        background: 'var(--bg)',
        overflow: 'auto',
        display: 'flex',
        flexDirection: 'column',
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: '14px',
          padding: '18px 26px',
          borderBottom: '1px solid var(--line2)',
        }}
      >
        <button
          type="button"
          className="hv-fg"
          onClick={navBack}
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '9px',
            background: 'none',
            border: 0,
            margin: 0,
            padding: '6px 0',
            font: 'inherit',
            fontSize: '12.5px',
            color: 'var(--fg2)',
            cursor: 'pointer',
          }}
        >
          <span style={{ font: "400 14px/1 'JetBrains Mono',monospace" }}>←</span>
          {isReg && regStep === 2 ? '上一步' : navBackLabel}
        </button>
        <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: '9px' }}>
          <BrandMark size={13} />
          <span style={{ font: "500 9px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
            POWER&nbsp;·&nbsp;PUSH
          </span>
        </div>
      </div>

      <div style={{ flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center', padding: '40px 24px 60px' }}>
        <form
          key={`${V}_${regStep}_${animKey}`}
          onSubmit={handleSubmit}
          className={isExiting ? 'auth-form-fall' : 'auth-form-switch'}
          style={{
            width: '100%',
            maxWidth: '372px',
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          {isReg && (
            <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginBottom: '22px' }}>
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '7px',
                  fontSize: '12px',
                  fontWeight: regStep === 1 ? 600 : 400,
                  color: regStep === 1 ? 'var(--fg)' : 'var(--fg3)',
                  transition: 'color .2s ease',
                }}
              >
                <span
                  style={{
                    width: '18px',
                    height: '18px',
                    borderRadius: '50%',
                    background: regStep === 1 ? 'var(--fg)' : 'var(--line2)',
                    color: regStep === 1 ? 'var(--bg)' : 'var(--fg3)',
                    display: 'grid',
                    placeItems: 'center',
                    fontFamily: "'JetBrains Mono',monospace",
                    fontSize: '10.5px',
                    fontWeight: 600,
                  }}
                >
                  1
                </span>
                <span>验证账号</span>
              </div>
              <div style={{ flex: 1, height: '1px', background: 'var(--line2)' }} />
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '7px',
                  fontSize: '12px',
                  fontWeight: regStep === 2 ? 600 : 400,
                  color: regStep === 2 ? 'var(--fg)' : 'var(--fg3)',
                  transition: 'color .2s ease',
                }}
              >
                <span
                  style={{
                    width: '18px',
                    height: '18px',
                    borderRadius: '50%',
                    background: regStep === 2 ? 'var(--fg)' : 'var(--line2)',
                    color: regStep === 2 ? 'var(--bg)' : 'var(--fg3)',
                    display: 'grid',
                    placeItems: 'center',
                    fontFamily: "'JetBrains Mono',monospace",
                    fontSize: '10.5px',
                    fontWeight: 600,
                  }}
                >
                  2
                </span>
                <span>名称与密码</span>
              </div>
            </div>
          )}

          <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
            {authStepEn}
          </div>
          <div style={{ fontSize: '38px', fontWeight: 600, letterSpacing: '-.045em', lineHeight: 1.05, marginTop: '14px' }}>
            {authTitle}
          </div>
          <div style={{ fontSize: '12.5px', color: 'var(--fg3)', lineHeight: 1.6, marginTop: '12px' }}>{authSub}</div>

          {isReg && regStep === 2 && F.phone && (
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                padding: '10px 14px',
                background: 'var(--sub)',
                borderRadius: '8px',
                border: '1px solid var(--line2)',
                marginTop: '20px',
                fontSize: '12.5px',
              }}
            >
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                <span style={{ color: 'var(--fg2)', fontSize: '11.5px' }}>✓ 已验证账号</span>
                <span style={{ fontFamily: "'JetBrains Mono',monospace", fontWeight: 500, color: 'var(--fg)' }}>
                  {F.phone}
                </span>
              </div>
              <button
                type="button"
                className="hv-fg"
                onClick={() => {
                  setRegStep(1)
                  setAnimKey((k) => k + 1)
                }}
                style={{
                  background: 'none',
                  border: 0,
                  color: 'var(--red)',
                  fontSize: '11.5px',
                  cursor: 'pointer',
                  fontWeight: 500,
                  padding: 0,
                }}
              >
                修改
              </button>
            </div>
          )}

          {showOAuth && <OAuthButtons intent={isLogin ? 'login' : 'register'} />}

          <div style={{ display: 'flex', flexDirection: 'column', marginTop: showOAuth ? '20px' : '28px' }}>
            {fields.map((f) => {
              const isPassword = f.t === 'password'
              const passwordVisible = isPassword && visiblePasswords[f.k] === true
              const autoComplete =
                f.k === 'phone'
                  ? 'email'
                  : f.k === 'code'
                    ? 'one-time-code'
                    : f.k === 'nick'
                      ? 'nickname'
                      : isLogin
                        ? 'current-password'
                        : 'new-password'
              return (
                <div key={f.k} style={{ display: 'flex', flexDirection: 'column', gap: '7px', padding: '0 0 20px' }}>
                  <div style={{ display: 'flex', alignItems: 'baseline', gap: '12px' }}>
                    <span
                      style={{ font: "500 9px/1 'JetBrains Mono',monospace", letterSpacing: '.16em', color: 'var(--fg3)' }}
                    >
                      {f.en}
                    </span>
                    {f.act && (
                      <button
                        type="button"
                        className="hv-fg"
                        onClick={f.onAct || undefined}
                        disabled={f.k === 'code' && !captchaState.ready}
                        style={{
                          margin: '0 0 0 auto',
                          padding: 0,
                          background: 'none',
                          border: 0,
                          font: 'inherit',
                          fontSize: '11.5px',
                          fontWeight: 500,
                          color: 'var(--red)',
                          cursor: f.k === 'code' && !captchaState.ready ? 'default' : 'pointer',
                          opacity: f.k === 'code' && !captchaState.ready ? 0.45 : 1,
                        }}
                      >
                        {f.act}
                      </button>
                    )}
                  </div>
                  <div style={{ position: 'relative' }}>
                    <input
                      className="authfield"
                      type={passwordVisible ? 'text' : f.t}
                      value={String(F[f.k] ?? '')}
                      placeholder={f.ph}
                      autoComplete={autoComplete}
                      onChange={(e) => setForm(f.k, e.target.value)}
                      style={{
                        width: '100%',
                        background: 'none',
                        border: 0,
                        borderBottom: '1px solid var(--line)',
                        transition: 'border-color .18s ease',
                        padding: isPassword ? '9px 38px 9px 0' : '9px 0',
                        color: 'var(--fg)',
                        fontFamily: 'inherit',
                        fontSize: '17px',
                        fontWeight: 500,
                        lineHeight: 1.35,
                        letterSpacing: '-.02em',
                      }}
                    />
                    {isPassword && (
                      <button
                        type="button"
                        className="hv-fg"
                        aria-label={passwordVisible ? '隐藏密码' : '显示密码'}
                        title={passwordVisible ? '隐藏密码' : '显示密码'}
                        onClick={() => setVisiblePasswords((prev) => ({ ...prev, [f.k]: !passwordVisible }))}
                        style={{
                          position: 'absolute',
                          right: 0,
                          top: '50%',
                          width: '30px',
                          height: '30px',
                          transform: 'translateY(-50%)',
                          display: 'grid',
                          placeItems: 'center',
                          padding: 0,
                          border: 0,
                          background: 'none',
                          color: 'var(--fg3)',
                          cursor: 'pointer',
                        }}
                      >
                        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                          <path
                            d="M2.75 12s3.35-5.25 9.25-5.25S21.25 12 21.25 12 17.9 17.25 12 17.25 2.75 12 2.75 12Z"
                            stroke="currentColor"
                            strokeWidth="1.45"
                            strokeLinecap="round"
                            strokeLinejoin="round"
                          />
                          <circle cx="12" cy="12" r="2.35" stroke="currentColor" strokeWidth="1.45" />
                          {passwordVisible && (
                            <path d="m4.2 4.2 15.6 15.6" stroke="currentColor" strokeWidth="1.45" strokeLinecap="round" />
                          )}
                        </svg>
                      </button>
                    )}
                  </div>
                  {f.note && <span style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>{f.note}</span>}
                </div>
              )
            })}
          </div>

          {(!isReg || regStep === 1) && (
            <CaptchaGate
              action={captchaAction}
              resetKey={captchaReset}
              onChange={({ required, ready, token }) => setCaptchaState({ required, ready, token })}
            />
          )}

          {isReg && regStep === 2 && (
            <button
              type="button"
              onClick={() => setForm('agree', !F.agree)}
              style={{
                background: 'none',
                border: 0,
                margin: '0 0 24px',
                padding: 0,
                font: 'inherit',
                textAlign: 'left',
                cursor: 'pointer',
                display: 'flex',
                alignItems: 'center',
                gap: '11px',
                color: 'var(--fg2)',
              }}
            >
              <span
                style={{
                  width: '15px',
                  height: '15px',
                  flex: 'none',
                  border: `1px solid ${F.agree ? 'var(--fg)' : 'var(--line)'}`,
                  background: F.agree ? 'var(--fg)' : 'transparent',
                  color: 'var(--bg)',
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  fontSize: '9px',
                }}
              >
                {F.agree ? '✓' : ''}
              </span>
              <span style={{ fontSize: '12.5px', lineHeight: 1.5 }}>同意《校园能耗数据使用说明》</span>
            </button>
          )}

          {s.authErr && (
            <div style={{ display: 'flex', alignItems: 'center', gap: '9px', marginBottom: '18px' }}>
              <span style={{ width: '5px', height: '5px', background: 'var(--red)', flex: 'none' }} />
              <span style={{ fontSize: '12.5px', color: 'var(--red)' }}>{s.authErr}</span>
            </div>
          )}

          <button
            type="submit"
            className="hv-op82"
            disabled={s.authBusy || (isLogin && !captchaState.ready)}
            style={{
              margin: 0,
              padding: '15px 24px',
              border: '1px solid var(--fg)',
              borderRadius: '999px',
              font: 'inherit',
              fontSize: '13px',
              fontWeight: 600,
              cursor: s.authBusy || (isLogin && !captchaState.ready) ? 'default' : 'pointer',
              background: 'var(--fg)',
              color: 'var(--bg)',
              opacity: s.authBusy || (isLogin && !captchaState.ready) ? 0.6 : 1,
              transition: 'opacity .2s ease',
            }}
          >
            {s.authBusy ? '处理中…' : authCta}
          </button>

          {isReg && regStep === 2 && (
            <button
              type="button"
              className="hv-fg"
              onClick={() => {
                setRegStep(1)
                setAnimKey((k) => k + 1)
                set({ authErr: null })
              }}
              style={{
                marginTop: '12px',
                padding: '10px 0',
                background: 'none',
                border: 0,
                font: 'inherit',
                fontSize: '12.5px',
                color: 'var(--fg3)',
                cursor: 'pointer',
                textAlign: 'center',
              }}
            >
              返回上一步修改账号 / 验证码
            </button>
          )}

          <div
            style={{
              marginTop: '30px',
              paddingTop: '20px',
              borderTop: '1px solid var(--line2)',
              display: 'flex',
              alignItems: 'center',
              gap: '12px',
            }}
          >
            <span style={{ fontSize: '12.5px', color: 'var(--fg3)' }}>{authAltText}</span>
            {authAltCta && (
              <button
                type="button"
                className="hv-fg"
                onClick={authAlt}
                style={{
                  background: 'none',
                  border: 0,
                  marginLeft: 'auto',
                  padding: 0,
                  font: 'inherit',
                  fontSize: '12.5px',
                  fontWeight: 600,
                  color: 'var(--red)',
                  cursor: 'pointer',
                }}
              >
                {authAltCta}
              </button>
            )}
          </div>
        </form>
      </div>
    </div>
  )
}

