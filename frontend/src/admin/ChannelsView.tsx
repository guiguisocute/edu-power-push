/* 推送渠道配置。在网页填写凭证、保存、测试并立即启用。
   邮件供应商与用户端渠道展示状态集中于此。
   展示状态三态：可用、Coming Soon、隐藏。 */

import { useEffect, useState } from 'react'
import { adminApi, type MailSettingsPayload, type MailSettingsView } from './api'
import { Btn, Dot, Empty, Field, Section, fieldStyle, fmtTime, mono } from './ui'
import { assignChannelToCategory, channelReady, orderedChannelDefs, type ChannelCategory } from '../lib/channels'

/** 各供应商字段。字段名对齐官方文档。 */
const PROVIDER_DOC: Record<string, { label: string; hint: string }> = {
  resend: {
    label: 'Resend',
    hint: '创建 re_ 开头的 key，并验证发信域名。',
  },
  smtp: {
    label: 'SMTP',
    hint: '支持 SMTP 的邮箱。465 用 TLS，587 用 STARTTLS。',
  },
  tencent_ses: {
    label: '腾讯云 SES',
    hint: '须完成发信域名与模板审核。密钥来自 CAM。',
  },
}

interface FrontendConfigShape {
  version: number
  features: Record<string, unknown> & {
    channels: Record<string, boolean>
    channel_coming_soon?: Record<string, boolean>
    channel_order?: string[]
    channel_categories?: ChannelCategory[]
  }
  display: Record<string, unknown>
  updated_at: string
}

/** 新建分类 id。面板禁止手填。避免运维造出后端拒绝的键。 */
function nextCategoryID(existing: ChannelCategory[]): string {
  const taken = new Set(existing.map((category) => category.id))
  for (let n = 1; n <= 99; n += 1) {
    const id = 'group_' + n
    if (!taken.has(id)) return id
  }
  return 'group_' + Date.now()
}

const EMPTY_MAIL: MailSettingsPayload = {
  provider: 'resend',
  from: '',
  admin_to: '',
  base_url: '',
  reply_to: '',
  smtp: { host: '', port: 465, user: '', tls_mode: 'tls' },
  tencent: { secret_id: '', region: 'ap-hongkong', from_email: '', template_id: 0 },
}

export default function ChannelsView({
  canWrite,
  onError,
  onToast,
}: {
  canWrite: boolean
  onError: (msg: string) => void
  onToast: (msg: string) => void
}) {
  const [view, setView] = useState<MailSettingsView | null>(null)
  const [frontend, setFrontend] = useState<FrontendConfigShape | null>(null)
  const [draft, setDraft] = useState<MailSettingsPayload>(EMPTY_MAIL)
  /* 凭证三态：undefined = 未改。'' = 清空。其他 = 新值。
     存 string | undefined。禁止把打码串塞进 draft。 */
  const [secretDraft, setSecretDraft] = useState<Record<string, string | undefined>>({})
  const [busy, setBusy] = useState(false)
  const [testTo, setTestTo] = useState('')
  const [tick, setTick] = useState(0)

  useEffect(() => {
    let alive = true
    Promise.all([adminApi.mailSettings(), adminApi.frontendConfig()])
      .then(([v, raw]) => {
        if (!alive) return
        setView(v)
        setFrontend(raw as FrontendConfigShape)
        setDraft({ ...EMPTY_MAIL, ...v.settings, smtp: { ...EMPTY_MAIL.smtp, ...v.settings.smtp }, tencent: { ...EMPTY_MAIL.tencent, ...v.settings.tencent } })
        setSecretDraft({})
      })
      .catch((e) => alive && onError(String(e instanceof Error ? e.message : e)))
    return () => {
      alive = false
    }
  }, [tick])

  const save = async () => {
    setBusy(true)
    try {
      // 只提交用户实际改过的凭证字段：没动的不出现在请求里，后端就会保留原值
      const secrets: Record<string, string | null> = {}
      for (const [k, v] of Object.entries(secretDraft)) {
        if (v === undefined) continue
        secrets[k] = v.trim() === '' ? null : v.trim()
      }
      const saved = await adminApi.saveMailSettings({ settings: draft, secrets })
      setView(saved)
      setSecretDraft({})
      onToast(saved.ready ? '已保存，邮件链路就绪' : '已保存，但还缺：' + saved.missing.join('、'))
    } catch (e) {
      onError(String(e instanceof Error ? e.message : e))
    } finally {
      setBusy(false)
    }
  }

  const setChannelMode = (id: string, mode: 'ready' | 'coming_soon' | 'hidden') => {
    if (!frontend) return
    setFrontend({
      ...frontend,
      features: {
        ...frontend.features,
        channels: { ...frontend.features.channels, [id]: mode !== 'hidden' },
        channel_coming_soon: {
          ...(frontend.features.channel_coming_soon || {}),
          [id]: mode === 'coming_soon',
        },
      },
    })
  }

  /* ---- 渠道分类 ----
     分类只是用户端列表的分组标题；成员名单存在分类里，组内顺序仍看下面那份 channel_order。
     成员唯一：把渠道挪进新分类时先从旧分类里摘掉，否则后端会以「一个渠道属于两个分类」打回。 */
  const categories: ChannelCategory[] = frontend?.features.channel_categories ?? []
  const putCategories = (next: ChannelCategory[]) => {
    if (!frontend) return
    setFrontend({ ...frontend, features: { ...frontend.features, channel_categories: next } })
  }
  const setCategory = (index: number, patch: Partial<ChannelCategory>) =>
    putCategories(categories.map((category, i) => (i === index ? { ...category, ...patch } : category)))
  const addCategory = () =>
    putCategories([...categories, { id: nextCategoryID(categories), name: '新分类', en: 'NEW GROUP', desc: '', channels: [] }])
  /** 删除分类仅解散分组。成员回到未分类。不会隐藏渠道。 */
  const removeCategory = (index: number) => putCategories(categories.filter((_, i) => i !== index))
  const moveCategory = (index: number, delta: -1 | 1) => {
    const to = index + delta
    if (to < 0 || to >= categories.length) return
    const next = [...categories]
    ;[next[index], next[to]] = [next[to], next[index]]
    putCategories(next)
  }
  const assignChannel = (channelID: string, categoryID: string) =>
    putCategories(assignChannelToCategory(categories, channelID, categoryID))
  const categoryOf = (channelID: string) => categories.find((category) => category.channels.includes(channelID))?.id ?? ''

  const moveChannel = (id: string, delta: -1 | 1) => {
    if (!frontend) return
    const order = orderedChannelDefs(frontend.features.channel_order).map((def) => def.id)
    const from = order.indexOf(id)
    const to = from + delta
    if (from < 0 || to < 0 || to >= order.length) return
    ;[order[from], order[to]] = [order[to], order[from]]
    setFrontend({
      ...frontend,
      features: { ...frontend.features, channel_order: order },
    })
  }

  const saveChannelPresentation = async () => {
    if (!frontend) return
    setBusy(true)
    try {
      const saved = (await adminApi.saveFrontendConfig({
        features: frontend.features,
        display: frontend.display,
      })) as FrontendConfigShape
      setFrontend(saved)
      onToast('推送渠道展示状态已下发')
    } catch (e) {
      onError(String(e instanceof Error ? e.message : e))
    } finally {
      setBusy(false)
    }
  }

  const test = async () => {
    const to = testTo.trim()
    if (!to.includes('@')) return onError('请填一个收件邮箱')
    setBusy(true)
    try {
      const result = await adminApi.testMail(to)
      if (result.ok) onToast('测试邮件已发出：' + to)
      else onError('发送失败：' + (result.error || '未知错误'))
    } catch (e) {
      onError(String(e instanceof Error ? e.message : e))
    } finally {
      setBusy(false)
    }
  }

  if (!view || !frontend) {
    return (
      <Section en="CHANNELS" title="推送渠道">
        <div style={{ fontSize: '13px', color: 'var(--fg3)', padding: '20px 0' }}>正在读取邮件配置…</div>
      </Section>
    )
  }

  const provider = draft.provider || ''
  const doc = PROVIDER_DOC[provider]
  /** 凭证输入框的值：改过就用改的，没改就显示后端给的打码串 */
  const secretValue = (field: string) =>
    secretDraft[field] !== undefined ? secretDraft[field]! : view.secret_masked[field] || ''
  const setSecret = (field: string, value: string) => setSecretDraft({ ...secretDraft, [field]: value })

  return (
    <>
      <Section
        en="CHANNELS · CATEGORIES"
        title="渠道分类"
        desc="用户端按此分组显示渠道。组内顺序由下一节控制。"
        actions={
          <Btn onClick={addCategory} disabled={!canWrite || categories.length >= 12}>
            新增分类
          </Btn>
        }
      >
        {categories.length === 0 ? (
          <Empty
            title="还没有分类"
            desc="用户端将平铺全部渠道。可按使用方式分组。"
          />
        ) : (
          <div style={{ maxWidth: '860px' }}>
            {categories.map((category, index) => (
              <div key={category.id} style={{ padding: '18px 0', borderBottom: '1px solid var(--line2)' }}>
                <div style={{ display: 'flex', alignItems: 'baseline', gap: '12px', marginBottom: '16px' }}>
                  <span style={mono()}>{category.id.toUpperCase()}</span>
                  <span style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>
                    {category.channels.length} 个渠道
                  </span>
                  <div style={{ marginLeft: 'auto', display: 'flex', gap: '6px' }}>
                    <button
                      type="button"
                      className="hv-line-fg"
                      aria-label={`上移 ${category.name}`}
                      title="上移"
                      disabled={!canWrite || index === 0}
                      onClick={() => moveCategory(index, -1)}
                      style={{ ...fieldStyle, width: '36px', padding: 0, cursor: 'pointer', opacity: index === 0 ? 0.35 : 1 }}
                    >
                      ↑
                    </button>
                    <button
                      type="button"
                      className="hv-line-fg"
                      aria-label={`下移 ${category.name}`}
                      title="下移"
                      disabled={!canWrite || index === categories.length - 1}
                      onClick={() => moveCategory(index, 1)}
                      style={{
                        ...fieldStyle,
                        width: '36px',
                        padding: 0,
                        cursor: 'pointer',
                        opacity: index === categories.length - 1 ? 0.35 : 1,
                      }}
                    >
                      ↓
                    </button>
                    <Btn danger onClick={() => canWrite && removeCategory(index)} disabled={!canWrite}>
                      删除
                    </Btn>
                  </div>
                </div>
                <div
                  data-r="split"
                  style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(210px,1fr))', gap: '20px 30px' }}
                >
                  <Field label="NAME" hint="用户端分组标题">
                    <input
                      value={category.name}
                      maxLength={40}
                      onChange={(e) => setCategory(index, { name: e.target.value })}
                      style={fieldStyle}
                      disabled={!canWrite}
                    />
                  </Field>
                  <Field label="LABEL" hint="上方等宽标签。留空不显示。">
                    <input
                      value={category.en}
                      maxLength={40}
                      onChange={(e) => setCategory(index, { en: e.target.value })}
                      style={fieldStyle}
                      disabled={!canWrite}
                    />
                  </Field>
                  <Field label="DESC" hint="说明用户须完成的操作。">
                    <input
                      value={category.desc || ''}
                      maxLength={160}
                      onChange={(e) => setCategory(index, { desc: e.target.value })}
                      style={fieldStyle}
                      disabled={!canWrite}
                    />
                  </Field>
                </div>
              </div>
            ))}
          </div>
        )}
        <div style={{ fontSize: '11.5px', color: 'var(--fg3)', lineHeight: 1.7, marginTop: '16px', textWrap: 'pretty' }}>
          在下一节选择分类归属，并点「下发展示状态」保存。删除分类仅取消分组。
        </div>
      </Section>

      <Section
        en="CHANNELS · PRESENTATION"
        title="用户端渠道展示"
        desc="设置分类、顺序与展示状态。未实现的渠道禁止标为可用。"
        actions={
          <Btn primary onClick={saveChannelPresentation} disabled={!canWrite || busy}>
            下发展示状态
          </Btn>
        }
      >
        <div style={{ maxWidth: '760px' }}>
          {orderedChannelDefs(frontend.features.channel_order).map((def, index, ordered) => {
            const visible = frontend.features.channels[def.id] !== false
            const soon = frontend.features.channel_coming_soon?.[def.id] ?? !channelReady(def)
            const mode: 'ready' | 'coming_soon' | 'hidden' = !visible ? 'hidden' : soon ? 'coming_soon' : 'ready'
            return (
              <div
                key={def.id}
                style={{
                  display: 'grid',
                  gridTemplateColumns: 'minmax(150px,1fr) minmax(150px,220px) minmax(150px,220px) auto',
                  alignItems: 'center',
                  gap: '20px',
                  padding: '14px 0',
                  borderBottom: '1px solid var(--line2)',
                }}
              >
                <div style={{ minWidth: 0 }}>
                  <div style={{ fontSize: '13.5px', fontWeight: 500 }}>{def.name}</div>
                  <div style={{ fontSize: '11.5px', color: 'var(--fg3)', marginTop: '4px' }}>
                    {channelReady(def) ? '投递已实现。可控制开放状态。' : '投递未实现。仅 Coming Soon 或隐藏。'}
                  </div>
                </div>
                <select
                  value={categoryOf(def.id)}
                  aria-label={`${def.name} 的分类`}
                  onChange={(e) => assignChannel(def.id, e.target.value)}
                  style={{ ...fieldStyle, cursor: canWrite ? 'pointer' : 'default' }}
                  disabled={!canWrite || categories.length === 0}
                >
                  <option value="">未分类</option>
                  {categories.map((category) => (
                    <option key={category.id} value={category.id}>
                      {category.name}
                    </option>
                  ))}
                </select>
                <select
                  value={mode}
                  onChange={(e) => setChannelMode(def.id, e.target.value as 'ready' | 'coming_soon' | 'hidden')}
                  style={{ ...fieldStyle, cursor: canWrite ? 'pointer' : 'default' }}
                  disabled={!canWrite}
                >
                  <option value="ready" disabled={!channelReady(def)}>可用</option>
                  <option value="coming_soon">Coming Soon</option>
                  <option value="hidden">隐藏</option>
                </select>
                <div style={{ display: 'flex', gap: '6px' }}>
                  <button
                    type="button"
                    className="hv-line-fg"
                    aria-label={`上移 ${def.name}`}
                    title="上移"
                    disabled={!canWrite || index === 0}
                    onClick={() => moveChannel(def.id, -1)}
                    style={{
                      ...fieldStyle,
                      width: '36px',
                      padding: 0,
                      cursor: !canWrite || index === 0 ? 'not-allowed' : 'pointer',
                      opacity: index === 0 ? 0.35 : 1,
                    }}
                  >
                    ↑
                  </button>
                  <button
                    type="button"
                    className="hv-line-fg"
                    aria-label={`下移 ${def.name}`}
                    title="下移"
                    disabled={!canWrite || index === ordered.length - 1}
                    onClick={() => moveChannel(def.id, 1)}
                    style={{
                      ...fieldStyle,
                      width: '36px',
                      padding: 0,
                      cursor: !canWrite || index === ordered.length - 1 ? 'not-allowed' : 'pointer',
                      opacity: index === ordered.length - 1 ? 0.35 : 1,
                    }}
                  >
                    ↓
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      </Section>

      <Section
        en="CHANNELS · MAIL"
        title="邮件推送"
        desc={
          view.source === 'panel'
            ? `使用面板配置（${view.updated_by || '未知'} 于 ${fmtTime(view.updated_at)}）。保存后立即生效。`
            : '使用环境变量配置。保存后改由面板接管。'
        }
        actions={
          <>
            <Btn onClick={() => setTick((n) => n + 1)} disabled={busy}>
              重新读取
            </Btn>
            <Btn primary onClick={save} disabled={!canWrite || busy}>
              保存配置
            </Btn>
          </>
        }
      >
        {!view.secrets_writable && (
          <div
            style={{
              border: '1px solid var(--red)',
              padding: '14px 16px',
              marginBottom: '24px',
              fontSize: '12.5px',
              lineHeight: 1.7,
              textWrap: 'pretty',
            }}
          >
            未配置 <code>SETTINGS_ENCRYPTION_KEY</code>。凭证无法加密存储。仅可改非敏感字段。
            在 <code>.env</code> 添加 32 字节 base64 密钥后重启（
            <code>openssl rand -base64 32</code>）。
            <span style={{ display: 'block', marginTop: '8px', color: 'var(--fg3)' }}>
              禁止明文存 API key。未配置密钥时拒绝保存凭证。
            </span>
          </div>
        )}
        {view.secrets_unreadable && (
          <div
            style={{
              border: '1px solid var(--red)',
              padding: '14px 16px',
              marginBottom: '24px',
              fontSize: '12.5px',
              lineHeight: 1.7,
            }}
          >
            当前 <code>SETTINGS_ENCRYPTION_KEY</code> 无法解密库中凭证。请重新填写并保存。
          </div>
        )}

        <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginBottom: '22px', fontSize: '13px' }}>
          <Dot tone={view.ready ? 'ok' : 'bad'} />
          {view.ready ? (
            <span>
              邮件链路就绪 · 当前供应商 <b>{view.effective_provider}</b>
            </span>
          ) : (
            <span style={{ color: 'var(--red)' }}>无法发信，缺少：{view.missing.join('、')}</span>
          )}
        </div>

        <div
          data-r="split"
          style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(230px,1fr))', gap: '26px 34px' }}
        >
          <Field label="PROVIDER" hint={doc?.hint}>
            <select
              value={provider}
              onChange={(e) => setDraft({ ...draft, provider: e.target.value })}
              style={{ ...fieldStyle, cursor: 'pointer' }}
              disabled={!canWrite}
            >
              {view.providers.map((p) => (
                <option key={p} value={p}>
                  {PROVIDER_DOC[p]?.label || p}
                </option>
              ))}
              <option value="">关闭邮件</option>
            </select>
          </Field>
          <Field label="FROM" hint="发件人。可带显示名。">
            <input
              value={draft.from}
              onChange={(e) => setDraft({ ...draft, from: e.target.value })}
              style={fieldStyle}
              disabled={!canWrite}
            />
          </Field>
          <Field label="REPLY TO" hint="可选。用户回信地址。">
            <input
              value={draft.reply_to}
              onChange={(e) => setDraft({ ...draft, reply_to: e.target.value })}
              style={fieldStyle}
              disabled={!canWrite}
            />
          </Field>
          <Field label="ADMIN TO" hint="运维告警收件人。">
            <input
              value={draft.admin_to}
              onChange={(e) => setDraft({ ...draft, admin_to: e.target.value })}
              style={fieldStyle}
              disabled={!canWrite}
            />
          </Field>
          <Field label="SITE BASE URL" hint="邮件按钮链接前缀。留空则按钮无效。">
            <input
              value={draft.base_url}
              onChange={(e) => setDraft({ ...draft, base_url: e.target.value })}
              placeholder="https://…"
              style={fieldStyle}
              disabled={!canWrite}
            />
          </Field>
        </div>

        {provider === 'resend' && (
          <div style={{ marginTop: '30px', maxWidth: '460px' }}>
            <Field
              label="RESEND API KEY"
              hint={
                view.secret_set.resend_api_key
                  ? '已配置。覆盖即更换；清空则回落环境变量。'
                  : '必填。re_ 开头，在 Resend 控制台创建。'
              }
            >
              <input
                value={secretValue('resend_api_key')}
                onChange={(e) => setSecret('resend_api_key', e.target.value)}
                onFocus={() => {
                  // 聚焦时清打码串。禁止在圆点后继续输入。
                  if (secretDraft.resend_api_key === undefined) setSecret('resend_api_key', '')
                }}
                placeholder="re_…"
                autoComplete="off"
                spellCheck={false}
                style={fieldStyle}
                disabled={!canWrite || !view.secrets_writable}
              />
            </Field>
          </div>
        )}

        {provider === 'smtp' && (
          <div
            data-r="split"
            style={{
              marginTop: '30px',
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fit,minmax(200px,1fr))',
              gap: '26px 34px',
            }}
          >
            <Field label="HOST">
              <input
                value={draft.smtp.host}
                onChange={(e) => setDraft({ ...draft, smtp: { ...draft.smtp, host: e.target.value } })}
                style={fieldStyle}
                disabled={!canWrite}
              />
            </Field>
            <Field label="PORT" hint="465 = 隐式 TLS，587 = STARTTLS">
              <input
                value={String(draft.smtp.port || '')}
                onChange={(e) => setDraft({ ...draft, smtp: { ...draft.smtp, port: Number(e.target.value) || 0 } })}
                inputMode="numeric"
                style={fieldStyle}
                disabled={!canWrite}
              />
            </Field>
            <Field label="TLS MODE">
              <select
                value={draft.smtp.tls_mode || 'tls'}
                onChange={(e) => setDraft({ ...draft, smtp: { ...draft.smtp, tls_mode: e.target.value } })}
                style={{ ...fieldStyle, cursor: 'pointer' }}
                disabled={!canWrite}
              >
                <option value="tls">tls（465）</option>
                <option value="starttls">starttls（587）</option>
              </select>
            </Field>
            <Field label="USER">
              <input
                value={draft.smtp.user}
                onChange={(e) => setDraft({ ...draft, smtp: { ...draft.smtp, user: e.target.value } })}
                autoComplete="off"
                style={fieldStyle}
                disabled={!canWrite}
              />
            </Field>
            <Field
              label="PASSWORD"
              hint={view.secret_set.smtp_pass ? '已配置。覆盖即更换；清空则回落环境变量。' : '邮箱授权码或密码。'}
            >
              <input
                value={secretValue('smtp_pass')}
                onChange={(e) => setSecret('smtp_pass', e.target.value)}
                onFocus={() => {
                  if (secretDraft.smtp_pass === undefined) setSecret('smtp_pass', '')
                }}
                autoComplete="off"
                style={fieldStyle}
                disabled={!canWrite || !view.secrets_writable}
              />
            </Field>
          </div>
        )}

        {provider === 'tencent_ses' && (
          <div
            data-r="split"
            style={{
              marginTop: '30px',
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fit,minmax(200px,1fr))',
              gap: '26px 34px',
            }}
          >
            <Field label="SECRET ID">
              <input
                value={draft.tencent.secret_id}
                onChange={(e) => setDraft({ ...draft, tencent: { ...draft.tencent, secret_id: e.target.value } })}
                autoComplete="off"
                style={fieldStyle}
                disabled={!canWrite}
              />
            </Field>
            <Field
              label="SECRET KEY"
              hint={view.secret_set.tencent_secret_key ? '已配置。覆盖即更换。' : '来自腾讯云 CAM。'}
            >
              <input
                value={secretValue('tencent_secret_key')}
                onChange={(e) => setSecret('tencent_secret_key', e.target.value)}
                onFocus={() => {
                  if (secretDraft.tencent_secret_key === undefined) setSecret('tencent_secret_key', '')
                }}
                autoComplete="off"
                style={fieldStyle}
                disabled={!canWrite || !view.secrets_writable}
              />
            </Field>
            <Field label="REGION">
              <input
                value={draft.tencent.region}
                onChange={(e) => setDraft({ ...draft, tencent: { ...draft.tencent, region: e.target.value } })}
                placeholder="ap-hongkong"
                style={fieldStyle}
                disabled={!canWrite}
              />
            </Field>
            <Field label="FROM EMAIL" hint="必须是已审核的发信地址。">
              <input
                value={draft.tencent.from_email}
                onChange={(e) => setDraft({ ...draft, tencent: { ...draft.tencent, from_email: e.target.value } })}
                style={fieldStyle}
                disabled={!canWrite}
              />
            </Field>
            <Field label="TEMPLATE ID" hint="腾讯云模板 ID。">
              <input
                value={String(draft.tencent.template_id || '')}
                onChange={(e) =>
                  setDraft({ ...draft, tencent: { ...draft.tencent, template_id: Number(e.target.value) || 0 } })
                }
                inputMode="numeric"
                style={fieldStyle}
                disabled={!canWrite}
              />
            </Field>
          </div>
        )}

        <div style={{ marginTop: '34px', paddingTop: '24px', borderTop: '1px solid var(--line2)' }}>
          <div style={{ ...mono(), marginBottom: '14px' }}>TEST SEND</div>
          <div style={{ display: 'flex', gap: '14px', alignItems: 'flex-end', flexWrap: 'wrap' }}>
            <div style={{ flex: 1, minWidth: '220px', maxWidth: '340px' }}>
              <input
                value={testTo}
                onChange={(e) => setTestTo(e.target.value)}
                placeholder="收件邮箱"
                style={fieldStyle}
              />
            </div>
            <Btn onClick={test} disabled={busy || !view.ready}>
              发送测试邮件
            </Btn>
          </div>
          <div style={{ fontSize: '11.5px', color: 'var(--fg3)', lineHeight: 1.7, marginTop: '12px', textWrap: 'pretty' }}>
            使用已保存配置发送。须先保存再测试。
          </div>
        </div>
      </Section>

    </>
  )
}
