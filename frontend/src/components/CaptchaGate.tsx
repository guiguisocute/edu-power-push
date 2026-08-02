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
      const existing = document.querySelector<HTMLScriptElement>('script[data-power-turnstile]')
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
        script.dataset.powerTurnstile = '1'
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
  const [failed, setFailed] = useState(false)
  onTokenRef.current = onToken

  useEffect(() => {
    let alive = true
    setFailed(false)
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
            onTokenRef.current('')
            setFailed(true)
          },
        })
      })
      .catch(() => alive && setFailed(true))
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
      {failed && <span style={{ fontSize: '12px', color: 'var(--red)' }}>验证服务暂不可用</span>}
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
      .catch(() => alive && setConfig({ provider: 'disabled', site_key: '', actions: [] }))
    return () => {
      alive = false
    }
  }, [])

  const required = !!config && config.provider !== 'disabled' && config.actions.includes(action)

  useEffect(() => {
    setToken('')
    // resetKey 在令牌消费后故意清空 token。
  }, [action, config?.provider, required, resetKey])

  useEffect(() => {
    if (!config) return
    const ready = !required || token !== ''
    onChangeRef.current({ required, ready, token, provider: config.provider })
  }, [config, required, token])

  if (!config || !required) return null
  return (
    <div style={{ marginBottom: '18px' }}>
      <TurnstileWidget siteKey={config.site_key} action={action} resetKey={resetKey} onToken={setToken} />
    </div>
  )
}
