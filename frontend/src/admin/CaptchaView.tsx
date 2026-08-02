import { useEffect, useState } from 'react'
import { errMsg } from '../api/errors'
import type { CaptchaAction, CaptchaProvider } from '../api/client'
import { invalidateCaptchaConfig, TurnstileWidget } from '../components/CaptchaGate'
import {
  adminApi,
  type CaptchaProbeResult,
  type CaptchaSettingsPayload,
  type CaptchaSettingsView,
} from './api'
import { Btn, Dot, Field, Row, Section, fieldStyle, fmtTime, mono } from './ui'

const EMPTY: CaptchaSettingsPayload = {
  provider: 'disabled',
  site_key: '',
  hostname: '',
  actions: ['auth.login', 'auth.register_code', 'auth.password_reset_code', 'meter.preview'],
}

const PROVIDERS: { id: CaptchaProvider; label: string; note: string }[] = [
  { id: 'disabled', label: '关闭', note: '仅保留请求限流。' },
  { id: 'turnstile', label: 'Turnstile', note: '浏览器取令牌。后端 Siteverify 校验。' },
]

const ACTIONS: { id: CaptchaAction; label: string; note: string }[] = [
  { id: 'auth.login', label: '账号登录', note: '降低撞库与凭据填充' },
  { id: 'auth.register_code', label: '注册验证码', note: '禁止批量消耗发信额度' },
  { id: 'auth.password_reset_code', label: '找回密码验证码', note: '禁止批量触发重置邮件' },
  { id: 'meter.preview', label: '电表位置预览', note: '限制宿舍位置枚举' },
]

export default function CaptchaView({
  canWrite,
  onError,
  onToast,
}: {
  canWrite: boolean
  onError: (message: string) => void
  onToast: (message: string) => void
}) {
  const [view, setView] = useState<CaptchaSettingsView | null>(null)
  const [draft, setDraft] = useState<CaptchaSettingsPayload>(EMPTY)
  const [secret, setSecret] = useState('')
  const [busy, setBusy] = useState(false)
  const [probe, setProbe] = useState<CaptchaProbeResult | null>(null)
  const [probeToken, setProbeToken] = useState('')
  const [probeReset, setProbeReset] = useState(0)

  const load = () => {
    setBusy(true)
    adminApi.captchaSettings().then(
      (next) => {
        setView(next)
        setDraft(next.settings)
        setSecret(next.secret_masked.turnstile_secret || '')
        setBusy(false)
      },
      (error) => {
        setBusy(false)
        onError(errMsg(error))
      },
    )
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const save = async () => {
    if (!canWrite || busy) return
    setBusy(true)
    try {
      const saved = await adminApi.saveCaptchaSettings({
        settings: draft,
        secrets: { turnstile_secret: secret || null },
      })
      setView(saved)
      setDraft(saved.settings)
      setSecret(saved.secret_masked.turnstile_secret || '')
      setProbe(null)
      invalidateCaptchaConfig()
      onToast('人机验证设置已生效')
    } catch (error) {
      onError(errMsg(error))
    } finally {
      setBusy(false)
    }
  }

  const runProbe = async () => {
    if (busy) return
    const activeProvider = view?.settings.provider || 'disabled'
    setBusy(true)
    try {
      const result = await adminApi.testCaptcha(probeToken)
      setProbe(result)
      if (activeProvider === 'turnstile') setProbeReset((value) => value + 1)
    } catch (error) {
      onError(errMsg(error))
    } finally {
      setBusy(false)
    }
  }

  const toggleAction = (action: CaptchaAction) => {
    const selected = draft.actions.includes(action)
    setDraft({
      ...draft,
      actions: selected ? draft.actions.filter((item) => item !== action) : [...draft.actions, action],
    })
  }

  if (!view) {
    return <div style={{ padding: '40px 0', color: 'var(--fg3)', fontSize: '13px' }}>读取人机验证设置…</div>
  }

  const savedProvider = view.settings.provider

  return (
    <>
      <Section
        en="SECURITY · CAPTCHA"
        title="人机验证"
        desc="启用后必须通过验证服务，保存后立即生效。"
        actions={<Btn primary onClick={save} disabled={!canWrite || busy}>{busy ? '处理中…' : '保存设置'}</Btn>}
      >
        <div style={{ display: 'flex', gap: '8px', flexWrap: 'wrap' }}>
          {PROVIDERS.map((provider) => {
            const selected = draft.provider === provider.id
            return (
              <button
                key={provider.id}
                type="button"
                onClick={() => canWrite && setDraft({ ...draft, provider: provider.id })}
                disabled={!canWrite}
                title={provider.note}
                style={{
                  border: `1px solid ${selected ? 'var(--fg)' : 'var(--line)'}`,
                  background: selected ? 'var(--fg)' : 'none',
                  color: selected ? 'var(--bg)' : 'var(--fg2)',
                  padding: '10px 16px',
                  font: 'inherit',
                  fontSize: '12.5px',
                  cursor: canWrite ? 'pointer' : 'default',
                }}
              >
                {provider.label}
              </button>
            )
          })}
        </div>

        <div style={{ marginTop: '30px' }}>
          <div style={{ ...mono(), marginBottom: '12px' }}>PROTECTED ACTIONS</div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(230px,1fr))', gap: '1px', background: 'var(--line2)' }}>
            {ACTIONS.map((action) => {
              const checked = draft.actions.includes(action.id)
              return (
                <label
                  key={action.id}
                  style={{ display: 'flex', gap: '12px', padding: '15px', background: 'var(--bg)', cursor: canWrite ? 'pointer' : 'default' }}
                >
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={() => toggleAction(action.id)}
                    disabled={!canWrite}
                    style={{ accentColor: 'var(--red)', flex: 'none' }}
                  />
                  <span>
                    <span style={{ display: 'block', fontSize: '13px', fontWeight: 600 }}>{action.label}</span>
                    <span style={{ display: 'block', marginTop: '5px', fontSize: '11.5px', color: 'var(--fg3)' }}>{action.note}</span>
                  </span>
                </label>
              )
            })}
          </div>
        </div>

        {draft.provider === 'turnstile' && (
          <div data-r="split" style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(240px,1fr))', gap: '26px 34px', marginTop: '30px' }}>
            <Field label="SITE KEY" hint="公开站点密钥，下发给浏览器。">
              <input value={draft.site_key} onChange={(event) => setDraft({ ...draft, site_key: event.target.value })} disabled={!canWrite} style={fieldStyle} />
            </Field>
            <Field label="SECRET KEY" hint="后端加密保存，用于 Siteverify。">
              <input type="password" value={secret} onChange={(event) => setSecret(event.target.value)} disabled={!canWrite || !view.secrets_writable} style={fieldStyle} autoComplete="new-password" />
            </Field>
            <Field label="EXPECTED HOSTNAME" hint="可选。返回 hostname 必须匹配。">
              <input value={draft.hostname} onChange={(event) => setDraft({ ...draft, hostname: event.target.value })} disabled={!canWrite} placeholder="power.example.edu" style={fieldStyle} />
            </Field>
          </div>
        )}
      </Section>

      <Section
        en="LIVE PROBE"
        title="可用性测试"
        desc="测试已保存配置。须先保存再测试。"
        actions={<Btn onClick={runProbe} disabled={busy || savedProvider === 'turnstile' && !probeToken}>{busy ? '测试中…' : '开始测试'}</Btn>}
      >
        {savedProvider === 'turnstile' && view.settings.site_key && (
          <TurnstileWidget
            siteKey={view.settings.site_key}
            action="admin.captcha_probe"
            resetKey={probeReset}
            onToken={setProbeToken}
          />
        )}
        <div style={{ maxWidth: '520px', marginTop: '14px' }}>
          <Row label="当前供应商" value={savedProvider} />
          <Row label="配置来源" value={view.source === 'panel' ? '管理面板' : '环境变量'} />
          <Row label="配置状态" value={view.ready ? '完整' : `缺少 ${view.missing.join(', ')}`} tone={view.ready ? undefined : 'var(--red)'} />
          <Row label="在线探测" value={probe ? <span style={{ display: 'inline-flex', alignItems: 'center', gap: '8px' }}><Dot tone={probe.available && probe.passed ? 'ok' : probe.available ? 'warn' : 'bad'} />{probe.passed ? '通过' : probe.available ? '等待验证' : '不可用'}</span> : '尚未执行'} />
          <Row label="最近保存" value={fmtTime(view.updated_at)} />
        </div>
      </Section>
    </>
  )
}
