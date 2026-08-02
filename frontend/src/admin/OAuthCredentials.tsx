/* 第三方登录凭证。网页填写即可启用，不必改 .env 再部署。
   明文字段原样存。client secret 加密进库，读回为打码值。
   打码值原样提交表示未改。.env 作兜底，面板留空时使用。 */

import { useEffect, useState } from 'react'
import { errMsg } from '../api/errors'
import { adminApi, type OAuthSettingsView } from './api'
import { Btn, Field, Row, Section, fieldStyle, fmtTime, mono } from './ui'

const PROVIDER_LABEL: Record<string, string> = { google: 'Google', github: 'GitHub' }

/** 回调地址必须一字不差抄入提供方后台。抄错是最常见失败原因。 */
function RedirectURI({ provider, uri }: { provider: string; uri: string }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(uri)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      /* 浏览器拒绝剪贴板时不弹错。地址已显示，可手动选中复制。 */
    }
  }
  return (
    <div style={{ display: 'flex', alignItems: 'baseline', gap: '12px', padding: '10px 0 12px', borderBottom: '1px solid var(--line2)' }}>
      <span style={{ fontSize: '12.5px', color: 'var(--fg3)', flex: 'none' }}>
        {PROVIDER_LABEL[provider] || provider} 回调地址
      </span>
      <span
        style={{
          marginLeft: 'auto',
          textAlign: 'right',
          fontFamily: "'JetBrains Mono',monospace",
          fontSize: '11.5px',
          wordBreak: 'break-all',
          minWidth: 0,
        }}
      >
        {uri || <span style={{ color: 'var(--red)' }}>先填写站点地址</span>}
      </span>
      {uri && (
        <button
          type="button"
          className="hv-fg"
          onClick={copy}
          style={{
            flex: 'none',
            background: 'none',
            border: 0,
            padding: 0,
            font: 'inherit',
            fontSize: '11.5px',
            fontWeight: 500,
            color: copied ? 'var(--fg3)' : 'var(--red)',
            cursor: 'pointer',
          }}
        >
          {copied ? '已复制' : '复制'}
        </button>
      )}
    </div>
  )
}

export default function OAuthCredentials({
  canManage,
  onError,
  onToast,
  onSaved,
}: {
  canManage: boolean
  onError: (msg: string) => void
  onToast: (msg: string) => void
  /** 保存后通知外层重拉前端配置。凭证齐后开关才可启用。 */
  onSaved: () => void
}) {
  const [view, setView] = useState<OAuthSettingsView | null>(null)
  const [baseURL, setBaseURL] = useState('')
  const [googleID, setGoogleID] = useState('')
  const [githubID, setGithubID] = useState('')
  const [googleSecret, setGoogleSecret] = useState('')
  const [githubSecret, setGithubSecret] = useState('')
  const [busy, setBusy] = useState(false)

  const adopt = (next: OAuthSettingsView) => {
    setView(next)
    setBaseURL(next.settings.base_url)
    setGoogleID(next.settings.google_client_id)
    setGithubID(next.settings.github_client_id)
    setGoogleSecret(next.secret_masked.google_client_secret || '')
    setGithubSecret(next.secret_masked.github_client_secret || '')
  }

  useEffect(() => {
    if (!canManage) return
    let alive = true
    adminApi.oauthSettings().then(
      (next) => alive && adopt(next),
      (error) => alive && onError(errMsg(error)),
    )
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canManage])

  /* 凭证仅管理员可看可改。与邮件供应商、人机验证同级。
     运维可调采集参数，但拿不到可冒充本站的密钥。 */
  if (!canManage) {
    return (
      <Section en="AUTH · THIRD PARTY" title="第三方登录凭证" desc="Google / GitHub 的 client id 与 secret。">
        <div style={{ fontSize: '12.5px', color: 'var(--fg3)', lineHeight: 1.7, maxWidth: '620px' }}>
          仅管理员可查看与修改凭证。登录入口开关可照常改。
        </div>
      </Section>
    )
  }

  if (!view) {
    return (
      <Section en="AUTH · THIRD PARTY" title="第三方登录凭证">
        <div style={{ fontSize: '13px', color: 'var(--fg3)', padding: '20px 0' }}>正在读取第三方登录设置…</div>
      </Section>
    )
  }

  const save = async () => {
    if (busy) return
    setBusy(true)
    try {
      const saved = await adminApi.saveOAuthSettings({
        settings: { base_url: baseURL, google_client_id: googleID, github_client_id: githubID },
        secrets: {
          google_client_secret: googleSecret || null,
          github_client_secret: githubSecret || null,
        },
      })
      adopt(saved)
      onSaved()
      onToast('第三方登录凭证已保存。立即生效。')
    } catch (error) {
      onError(errMsg(error))
    } finally {
      setBusy(false)
    }
  }

  const status = (provider: string) => {
    if (view.configured[provider]) return <span>已配置</span>
    const missing = view.missing[provider] || []
    if (missing.length === 3) return <span style={{ color: 'var(--fg3)' }}>未配置</span>
    return <span style={{ color: 'var(--red)' }}>缺 {missing.join('、')}</span>
  }

  const secretHint = view.secrets_writable
    ? '服务端加密存储。不改则保留；清空则回落 .env。'
    : '未配置 SETTINGS_ENCRYPTION_KEY。凭证无法保存。'

  return (
    <Section
      en="AUTH · THIRD PARTY"
      title="第三方登录凭证"
      desc="保存后立即生效。留空字段回落环境变量。"
      actions={
        <Btn primary onClick={save} disabled={busy}>
          {busy ? '保存中…' : '保存凭证'}
        </Btn>
      }
    >
      {view.secrets_unreadable && (
        <div style={{ border: '1px solid var(--red)', padding: '12px 16px', marginBottom: '20px', fontSize: '12.5px', lineHeight: 1.7 }}>
          当前密钥无法解密密文。请重新填写两个 secret。
        </div>
      )}

      <div style={{ maxWidth: '620px', marginBottom: '30px' }}>
        <Field
          label="SITE BASE URL"
          hint={
            '本站访问地址。须与提供方登记一致。' +
            (view.env_base_url ? `留空则使用 ${view.env_base_url}。` : '留空则回落 .env。')
          }
        >
          <input
            value={baseURL}
            onChange={(event) => setBaseURL(event.target.value)}
            placeholder="https://power.example.edu"
            style={fieldStyle}
          />
        </Field>
      </div>

      <div style={{ ...mono(), marginBottom: '12px' }}>REDIRECT URIS · 复制到提供方后台</div>
      <div style={{ maxWidth: '620px', marginBottom: '30px' }}>
        <RedirectURI provider="google" uri={view.redirect_uris.google || ''} />
        <RedirectURI provider="github" uri={view.redirect_uris.github || ''} />
      </div>

      <div
        data-r="split"
        style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(240px,1fr))', gap: '26px 34px' }}
      >
        <Field label="GOOGLE CLIENT ID" hint="Google Cloud Console → OAuth 客户端 ID。">
          <input
            value={googleID}
            onChange={(event) => setGoogleID(event.target.value)}
            placeholder="••••••.apps.googleusercontent.com"
            style={fieldStyle}
          />
        </Field>
        <Field label="GOOGLE CLIENT SECRET" hint={secretHint}>
          <input
            type="password"
            value={googleSecret}
            onChange={(event) => setGoogleSecret(event.target.value)}
            disabled={!view.secrets_writable}
            autoComplete="new-password"
            style={fieldStyle}
          />
        </Field>
        <Field label="GITHUB CLIENT ID" hint="GitHub → Developer settings → OAuth Apps。">
          <input
            value={githubID}
            onChange={(event) => setGithubID(event.target.value)}
            placeholder="Iv1.••••••••"
            style={fieldStyle}
          />
        </Field>
        <Field label="GITHUB CLIENT SECRET" hint={secretHint}>
          <input
            type="password"
            value={githubSecret}
            onChange={(event) => setGithubSecret(event.target.value)}
            disabled={!view.secrets_writable}
            autoComplete="new-password"
            style={fieldStyle}
          />
        </Field>
      </div>

      <div style={{ maxWidth: '620px', marginTop: '30px' }}>
        <Row label="Google" value={status('google')} />
        <Row label="GitHub" value={status('github')} />
        <Row label="配置来源" value={view.source === 'panel' ? '管理面板' : '环境变量'} />
        <Row label="最近保存" value={view.updated_at ? `${fmtTime(view.updated_at)} · ${view.updated_by}` : '尚未在面板保存'} />
      </div>
    </Section>
  )
}
