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
      const onLoad = () => {
        const api = turnstileAPI()
        if (api) resolve(api)
        else reject(new Error('Turnstile did not initialize'))
      }
      script.addEventListener('load', onLoad, { once: true })
      script.addEventListener('error', () => reject(new Error('Turnstile script failed to load')), { once: true })
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
  onFailed,
}: {
  siteKey: string
  action: string
  resetKey: number
  onToken: (token: string) => void
  /** site_key 错 / 脚本挂了：通知父组件放行，别把登录按钮锁死。 */
  onFailed?: () => void
}) {
  const host = useRef<HTMLDivElement>(null)
  const widgetID = useRef<string | null>(null)
  const onTokenRef = useRef(onToken)
  const onFailedRef = useRef(onFailed)
  const [failed, setFailed] = useState(false)
  onTokenRef.current = onToken
  onFailedRef.current = onFailed

  const markFailed = () => {
    setFailed(true)
    onTokenRef.current('')
    onFailedRef.current?.()
  }

  useEffect(() => {
    let alive = true
    setFailed(false)
    // 空 site_key 直接失败放行，禁止 render 抛异常。
    if (!siteKey.trim()) {
      markFailed()
      return () => {
        alive = false
      }
    }
    loadTurnstile()
      .then((turnstile) => {
        if (!alive || !host.current) return
        widgetID.current = turnstile.render(host.current, {
          sitekey: siteKey,
          action: action.replaceAll('.', '_'),
          theme: 'auto',
          callback: (token: string) => onTokenRef.current(token),
          'expired-callback': () => onTokenRef.current(''),
          'error-callback': () => {
            if (alive) markFailed()
          },
        })
      })
      .catch(() => alive && markFailed())
    return () => {
      alive = false
      if (widgetID.current) turnstileAPI()?.remove(widgetID.current)
      widgetID.current = null
    }
  }, [action, siteKey])

  useEffect(() => {
    if (widgetID.current) {
      turnstileAPI()?.reset(widgetID.current)
      onTokenRef.current('')
    }
  }, [resetKey])

  return (
    <div style={{ minHeight: '65px', display: 'flex', alignItems: 'center' }}>
      <div ref={host} />
      {failed && (
        <span style={{ fontSize: '12px', color: 'var(--fg3)' }}>
          人机验证暂不可用，已跳过
        </span>
      )}
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
  const [token, setToken] = useState('')
  // widget 挂了（site_key 错等）时 bypass：不拦登录按钮。
  const [bypassed, setBypassed] = useState(false)
  const onChangeRef = useRef(onChange)
  onChangeRef.current = onChange

  useEffect(() => {
    let alive = true
    if (!IS_LIVE) {
      const disabled: CaptchaPublicConfig = { provider: 'disabled', site_key: '', actions: [] }
      setConfig(disabled)
      return () => {
        alive = false
      }
    }
    loadCaptchaConfig()
      .then((value) => alive && setConfig(value))
      // 配置接口挂了：当 disabled，禁止卡死登录。
      .catch(() => alive && setConfig({ provider: 'disabled', site_key: '', actions: [] }))
    return () => {
      alive = false
    }
  }, [])

  // 后端凭据不全时 provider 已是 disabled；site_key 空也不强制。
  const configured =
    !!config &&
    config.provider !== 'disabled' &&
    !!config.site_key?.trim() &&
    config.actions.includes(action)
  const required = configured && !bypassed

  useEffect(() => {
    setToken('')
    setBypassed(false)
    // resetKey 在令牌消费后故意清空 token。
  }, [action, config?.provider, config?.site_key, configured, resetKey])

  useEffect(() => {
    if (!config) return
    // bypass 后 ready=true 且 token 空：后端对缺凭证场景也会放行。
    const ready = !required || token !== '' || bypassed
    onChangeRef.current({
      required: required && !bypassed,
      ready,
      token: bypassed ? '' : token,
      provider: bypassed ? 'disabled' : config.provider,
    })
  }, [config, required, token, bypassed])

  if (!config || !configured) return null
  return (
    <div style={{ marginBottom: '18px' }}>
      <TurnstileWidget
        siteKey={config.site_key}
        action={action}
        resetKey={resetKey}
        onToken={setToken}
        onFailed={() => setBypassed(true)}
      />
    </div>
  )
}
