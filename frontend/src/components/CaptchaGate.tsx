import { useEffect, useRef, useState } from 'react'
import {
  api,
  type CaptchaAction,
  type CaptchaProvider,
  type CaptchaPublicConfig,
} from '../api/client'
import { IS_LIVE } from '../api/mode'

interface TurnstileAPI {
  render: (container: HTMLElement, options: Record<string, unknown>) => string
  reset: (widgetID: string) => void
  remove: (widgetID: string) => void
}

type WidgetPhase = 'loading' | 'waiting' | 'verified' | 'expired' | 'error'

interface GateState {
  required: boolean
  ready: boolean
  token: string
  provider: CaptchaProvider
}

let configPromise: Promise<CaptchaPublicConfig> | null = null
let configLoadedAt = 0
let scriptPromise: Promise<TurnstileAPI> | null = null

export function invalidateCaptchaConfig() {
  configPromise = null
  configLoadedAt = 0
}

export function loadCaptchaConfig(): Promise<CaptchaPublicConfig> {
  if (!configPromise || Date.now() - configLoadedAt > 30_000) {
    configLoadedAt = Date.now()
    configPromise = api.captchaConfig().catch((error) => {
      configPromise = null
      throw error
    })
  }
  return configPromise
}

function turnstileAPI(): TurnstileAPI | undefined {
  return (window as unknown as { turnstile?: TurnstileAPI }).turnstile
}

function loadTurnstile(): Promise<TurnstileAPI> {
  const loaded = turnstileAPI()
  if (loaded) return Promise.resolve(loaded)
  if (!scriptPromise) {
    scriptPromise = new Promise((resolve, reject) => {
      const existing = document.querySelector<HTMLScriptElement>('script[data-jxnu-turnstile]')
      const script = existing || document.createElement('script')
      let settled = false
      const timer = window.setTimeout(() => fail(new Error('Turnstile script timed out')), 12_000)
      const cleanup = () => {
        window.clearTimeout(timer)
        script.removeEventListener('load', onLoad)
        script.removeEventListener('error', onError)
      }
      const fail = (error: Error) => {
        if (settled) return
        settled = true
        cleanup()
        scriptPromise = null
        // 已失败的 script 不复用；用户点击重试时重新创建干净实例。
        if (!turnstileAPI()) script.remove()
        reject(error)
      }
      const onLoad = () => {
        const api = turnstileAPI()
        if (!api) return fail(new Error('Turnstile did not initialize'))
        if (settled) return
        settled = true
        cleanup()
        resolve(api)
      }
      const onError = () => fail(new Error('Turnstile script failed to load'))
      script.addEventListener('load', onLoad, { once: true })
      script.addEventListener('error', onError, { once: true })
      if (!existing) {
        script.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'
        script.async = true
        script.defer = true
        script.dataset.jxnuTurnstile = '1'
        document.head.appendChild(script)
      }
    })
  }
  return scriptPromise
}

export function TurnstileWidget({
  siteKey,
  action,
  resetKey,
  onToken,
}: {
  siteKey: string
  action: string
  resetKey: number
  onToken: (token: string) => void
}) {
  const host = useRef<HTMLDivElement>(null)
  const widgetID = useRef<string | null>(null)
  const onTokenRef = useRef(onToken)
  const [phase, setPhase] = useState<WidgetPhase>('loading')
  const [retryKey, setRetryKey] = useState(0)
  onTokenRef.current = onToken

  useEffect(() => {
    let alive = true
    let refreshTimer: number | undefined
    setPhase('loading')
    onTokenRef.current('')
    const markFailed = () => {
      if (!alive) return
      setPhase('error')
      onTokenRef.current('')
    }
    const refreshExpired = () => {
      if (!alive) return
      setPhase('expired')
      onTokenRef.current('')
      // Turnstile 令牌会过期且只能使用一次。自动换新挑战，避免长时间停留
      // 在登录页的运维看到按钮永久锁灰。
      refreshTimer = window.setTimeout(() => {
        if (!alive || !widgetID.current) return
        try {
          turnstileAPI()?.reset(widgetID.current)
          setPhase('waiting')
        } catch {
          markFailed()
        }
      }, 650)
    }
    // 空 site_key 直接报配置错误，禁止 render 抛异常。
    if (!siteKey.trim()) {
      markFailed()
      return () => {
        alive = false
      }
    }
    loadTurnstile()
      .then((turnstile) => {
        if (!alive || !host.current) return
        setPhase('waiting')
        widgetID.current = turnstile.render(host.current, {
          sitekey: siteKey,
          action: action.replaceAll('.', '_'),
          theme: 'auto',
          callback: (token: string) => {
            if (!alive) return
            setPhase('verified')
            onTokenRef.current(token)
          },
          'expired-callback': refreshExpired,
          'timeout-callback': refreshExpired,
          'error-callback': markFailed,
        })
      })
      .catch(() => alive && markFailed())
    return () => {
      alive = false
      if (refreshTimer !== undefined) window.clearTimeout(refreshTimer)
      if (widgetID.current) turnstileAPI()?.remove(widgetID.current)
      widgetID.current = null
    }
  }, [action, siteKey, retryKey])

  useEffect(() => {
    if (widgetID.current) {
      turnstileAPI()?.reset(widgetID.current)
      onTokenRef.current('')
      setPhase('waiting')
    }
  }, [resetKey])

  const status =
    phase === 'loading'
      ? '正在加载安全验证…'
      : phase === 'verified'
        ? '验证完成'
        : phase === 'expired'
          ? '验证已过期，正在刷新…'
          : phase === 'error'
            ? '安全验证加载失败'
            : '请完成人机验证'
  const tone = phase === 'verified' ? 'var(--fg)' : phase === 'error' || phase === 'expired' ? 'var(--red)' : 'var(--fg3)'

  return (
    <div
      aria-live="polite"
      style={{
        border: '1px solid var(--line)',
        background: 'var(--sub)',
        padding: '13px 14px 12px',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: '9px' }}>
        <span style={{ width: '5px', height: '5px', flex: 'none', borderRadius: '50%', background: tone }} />
        <span style={{ font: "500 9px/1 'JetBrains Mono',monospace", letterSpacing: '.14em', color: tone }}>
          SECURITY CHECK
        </span>
        <span style={{ marginLeft: 'auto', fontSize: '11.5px', color: tone }}>{status}</span>
      </div>
      {phase === 'error' && (
        <div style={{ display: 'flex', alignItems: 'center', gap: '14px', marginTop: '13px' }}>
          <span style={{ flex: 1, minWidth: 0, fontSize: '12px', lineHeight: 1.55, color: 'var(--fg3)' }}>
            请检查网络或浏览器拦截设置后重试。
          </span>
          <button
            type="button"
            className="hv-line-fg"
            onClick={() => setRetryKey((value) => value + 1)}
            style={{
              flex: 'none',
              border: '1px solid var(--line)',
              borderRadius: '999px',
              padding: '7px 13px',
              background: 'none',
              color: 'var(--fg)',
              font: 'inherit',
              fontSize: '11.5px',
              cursor: 'pointer',
            }}
          >
            重新加载
          </button>
        </div>
      )}
      <div
        style={{
          minHeight: phase === 'error' ? 0 : '65px',
          display: phase === 'error' ? 'none' : 'flex',
          alignItems: 'center',
          marginTop: phase === 'error' ? 0 : '10px',
          overflow: 'hidden',
        }}
      >
        <div ref={host} />
      </div>
    </div>
  )
}

export default function CaptchaGate({
  action,
  resetKey,
  onChange,
}: {
  action: CaptchaAction
  resetKey: number
  onChange: (state: GateState) => void
}) {
  const [config, setConfig] = useState<CaptchaPublicConfig | null>(null)
  const [configError, setConfigError] = useState(false)
  const [configReload, setConfigReload] = useState(0)
  const [token, setToken] = useState({ action, value: '' })
  const onChangeRef = useRef(onChange)
  onChangeRef.current = onChange

  useEffect(() => {
    let alive = true
    setConfig(null)
    setConfigError(false)
    if (!IS_LIVE) {
      const disabled: CaptchaPublicConfig = { provider: 'disabled', site_key: '', actions: [] }
      setConfig(disabled)
      return () => {
        alive = false
      }
    }
    loadCaptchaConfig()
      .then((value) => alive && setConfig(value))
      .catch(() => alive && setConfigError(true))
    return () => {
      alive = false
    }
  }, [configReload])

  // 后端凭据不全时 provider 已是 disabled；site_key 空也不强制。
  const configured =
    !!config &&
    config.provider !== 'disabled' &&
    !!config.site_key?.trim() &&
    config.actions.includes(action)
  useEffect(() => {
    setToken({ action, value: '' })
  }, [action, config?.provider, config?.site_key, configured])

  useEffect(() => {
    setToken({ action, value: '' })
  }, [action, resetKey])

  useEffect(() => {
    if (!config) {
      // 配置尚未确认或读取失败时保持按钮锁定，避免把未知状态当成 disabled。
      onChangeRef.current({ required: true, ready: false, token: '', provider: 'disabled' })
      return
    }
    const ready = !configured || (token.action === action && token.value !== '')
    onChangeRef.current({
      required: configured,
      ready,
      token: configured && token.action === action ? token.value : '',
      provider: config.provider,
    })
  }, [action, config, configured, token])

  if (configError) {
    return (
      <div style={{ marginBottom: '18px', border: '1px solid var(--line)', background: 'var(--sub)', padding: '14px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
          <span style={{ width: '5px', height: '5px', flex: 'none', borderRadius: '50%', background: 'var(--red)' }} />
          <span style={{ flex: 1, minWidth: 0, fontSize: '12.5px', color: 'var(--fg2)' }}>无法确认人机验证配置</span>
          <button
            type="button"
            className="hv-line-fg"
            onClick={() => {
              invalidateCaptchaConfig()
              setConfigReload((value) => value + 1)
            }}
            style={{ border: 0, background: 'none', padding: 0, color: 'var(--red)', font: 'inherit', fontSize: '12px', cursor: 'pointer' }}
          >
            重试
          </button>
        </div>
      </div>
    )
  }

  if (!config) {
    return (
      <div style={{ marginBottom: '18px', fontSize: '12px', color: 'var(--fg3)' }} aria-live="polite">
        正在确认安全验证配置…
      </div>
    )
  }

  if (!configured) return null
  return (
    <div style={{ marginBottom: '18px' }}>
      <TurnstileWidget
        siteKey={config.site_key}
        action={action}
        resetKey={resetKey}
        onToken={(value) => setToken({ action, value })}
      />
    </div>
  )
}
