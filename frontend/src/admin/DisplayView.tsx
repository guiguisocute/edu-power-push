/* 用户端前端展示配置。对应 GET /api/v1/frontend-config。
   改完对所有访客立刻生效。用户端每次启动会查询。
   每个开关旁写明改后效果。 */

import { useEffect, useState } from 'react'
import { adminApi } from './api'
import OAuthCredentials from './OAuthCredentials'
import { Btn, Field, Row, Section, fieldStyle, fmtTime, mono } from './ui'
import { Toggle, trackOffColor } from '../components/ui'
import { parseDay, semesterLabel, semesterWeekCount, type Semester } from '../lib/semesters'

interface ConfigShape {
  version: number
  features: {
    auth: {
      email_login: boolean
      sms_login: boolean
      email_code: boolean
      sms_code: boolean
      registration: boolean
      google_oauth?: boolean
      github_oauth?: boolean
    }
    channels: Record<string, boolean>
    channel_coming_soon?: Record<string, boolean>
    charts?: {
      day_range?: boolean
      hourly_usage?: boolean
    }
  }
  display: {
    electricity_rate?: string
    empty_room_threshold_kwh?: string
    ranking_refresh_time: string
    campus_name: string
    area_name: string
    brand_name: string
    semesters?: Semester[]
  }
  /* 服务端是否配置第三方登录 client id/secret。只读。面板另有专节填写。 */
  oauth?: { google: boolean; github: boolean }
  updated_at: string
}

type AuthKey = keyof ConfigShape['features']['auth']
type AuthRow = [AuthKey, string, string, boolean]

/* 后端会拒绝的组合直接禁用，避免点后才收到 400。
   email_login 必须开启。短信后端尚无此能力。
   无 client id/secret 时第三方登录开关灰掉，并在说明写明下一步。 */
function authRows(oauth: ConfigShape['oauth']): AuthRow[] {
  const missing = (name: string) => `缺少 ${name} 凭证。在下方填写后可开启。`
  return [
    ['email_login', '邮箱登录', '唯一密码登录。禁止关闭。', true],
    ['registration', '开放注册', '关闭后隐藏注册入口。', false],
    ['email_code', '邮箱验证码找回密码', '关闭后隐藏「忘记密码」。', false],
    [
      'google_oauth',
      'Google 登录',
      oauth?.google
        ? '显示 Google 登录。已验证邮箱关联已有账号。'
        : missing('Google'),
      !oauth?.google,
    ],
    [
      'github_oauth',
      'GitHub 登录',
      oauth?.github
        ? '显示 GitHub 登录。须有已验证邮箱。'
        : missing('GitHub'),
      !oauth?.github,
    ],
    ['sms_login', '短信登录', '未实现短信认证。禁止开启。', true],
    ['sms_code', '短信验证码', '未实现短信认证。禁止开启。', true],
  ]
}


/* 老库缺这两个键时按开着算。
   缺键表示展示意愿。能否使用取决于服务端是否有凭证。
   不补则保存会把 undefined 写成 false，关掉本可用入口。 */
function withOAuthDefaults(config: ConfigShape): ConfigShape {
  const auth = config.features.auth
  return {
    ...config,
    features: {
      ...config.features,
      auth: {
        ...auth,
        google_oauth: auth.google_oauth ?? true,
        github_oauth: auth.github_oauth ?? true,
      },
    },
  }
}

export default function DisplayView({
  canWrite,
  canManageSecrets,
  dark,
  onError,
  onToast,
}: {
  canWrite: boolean
  /** 凭证类设置只有管理员能碰（与邮件供应商、人机验证同级） */
  canManageSecrets: boolean
  dark: boolean
  onError: (msg: string) => void
  onToast: (msg: string) => void
}) {
  const [config, setConfig] = useState<ConfigShape | null>(null)
  const [busy, setBusy] = useState(false)
  const trackOff = trackOffColor(dark)

  const loadConfig = () =>
    adminApi
      .frontendConfig()
      .then((raw) => withOAuthDefaults(raw as ConfigShape))
      .catch((e) => {
        onError(String(e instanceof Error ? e.message : e))
        return null
      })

  useEffect(() => {
    let alive = true
    loadConfig().then((next) => {
      if (alive && next) setConfig(next)
    })
    return () => {
      alive = false
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const save = async () => {
    if (!config) return
    setBusy(true)
    try {
      const saved = (await adminApi.saveFrontendConfig({
        features: config.features,
        display: config.display,
      })) as ConfigShape
      setConfig(withOAuthDefaults(saved))
      onToast('配置已保存。空房口径立即生效。')
    } catch (e) {
      onError(String(e instanceof Error ? e.message : e))
    } finally {
      setBusy(false)
    }
  }

  if (!config) {
    return (
      <Section en="DISPLAY" title="前端展示">
        <div style={{ fontSize: '13px', color: 'var(--fg3)', padding: '20px 0' }}>正在读取前端配置…</div>
      </Section>
    )
  }

  const setAuth = (key: keyof ConfigShape['features']['auth'], value: boolean) =>
    setConfig({ ...config, features: { ...config.features, auth: { ...config.features.auth, [key]: value } } })
  /* 校历。学期标识（key）不让手填：它必须等于开学月份，改开始日期时自动跟着走，
     否则下拉里写着 3 月、图却从 2 月开始，后端也会直接拒掉这份配置。 */
  const semesters = config.display.semesters ?? []
  const putSemesters = (next: Semester[]) =>
    setConfig({ ...config, display: { ...config.display, semesters: next } })
  const setSemester = (i: number, patch: Partial<Semester>) =>
    putSemesters(semesters.map((x, j) => (j === i ? { ...x, ...patch } : x)))
  const removeSemester = (i: number) => putSemesters(semesters.filter((_, j) => j !== i))
  const addSemester = () => {
    if (!canWrite) return
    const today = new Date()
    const day = (d: Date) =>
      d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0')
    const start = day(today)
    // 默认 18 周：本校常见学期长度，运维改起止即可
    const end = day(new Date(today.getFullYear(), today.getMonth(), today.getDate() + 18 * 7 - 1))
    putSemesters([...semesters, { key: start.slice(0, 7), start, end }])
  }
  const charts = config.features.charts || { day_range: false, hourly_usage: false }
  const setChart = (key: 'day_range' | 'hourly_usage', value: boolean) =>
    setConfig({
      ...config,
      features: {
        ...config.features,
        charts: { ...charts, [key]: value },
      },
    })

  return (
    <>
      <Section
        en="DISPLAY · AUTH"
        title="登录与注册"
        desc="控制登录页入口。灰色项不可启用。"
        actions={
          <Btn primary onClick={save} disabled={!canWrite || busy}>
            下发配置
          </Btn>
        }
      >
        <div style={{ maxWidth: '620px' }}>
          {authRows(config.oauth).map(([key, label, note, locked]) => (
            <div
              key={key}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '16px',
                padding: '14px 0',
                borderBottom: '1px solid var(--line2)',
                opacity: locked ? 0.55 : 1,
              }}
            >
              <div style={{ minWidth: 0 }}>
                <div style={{ fontSize: '13.5px', fontWeight: 500 }}>{label}</div>
                <div style={{ fontSize: '11.5px', color: 'var(--fg3)', marginTop: '4px' }}>{note}</div>
              </div>
              <Toggle
                on={!!config.features.auth[key]}
                trackOff={trackOff}
                size="sm"
                style={{ marginLeft: 'auto' }}
                onClick={() => {
                  if (locked || !canWrite) return
                  setAuth(key, !config.features.auth[key])
                }}
              />
            </div>
          ))}
        </div>
      </Section>

      {/* 凭证紧挨着它控制的那两个开关：配好之后上面的 Google / GitHub 两行会立刻不再是灰的 */}
      <OAuthCredentials
        canManage={canManageSecrets}
        onError={onError}
        onToast={onToast}
        onSaved={() => {
          loadConfig().then((next) => next && setConfig(next))
        }}
      />

      <Section
        en="DISPLAY · CHARTS"
        title="图表能力"
        desc="依赖上游分时数据。无数据时保持关闭。"
        actions={
          <Btn primary onClick={save} disabled={!canWrite || busy}>
            下发配置
          </Btn>
        }
      >
        <div style={{ maxWidth: '620px' }}>
          {(
            [
              [
                'day_range' as const,
                '数据看板 · 日视图',
                '24 小时负荷柱。抄表稀疏时关闭。',
              ],
              [
                'hourly_usage' as const,
                '用电分析 · 分时热力图',
                '分时热力图。打开后挂到用电分析页。',
              ],
            ] as const
          ).map(([key, label, note]) => (
            <div
              key={key}
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '16px',
                padding: '14px 0',
                borderBottom: '1px solid var(--line2)',
              }}
            >
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: '13.5px', fontWeight: 500 }}>{label}</div>
                <div style={{ fontSize: '12px', color: 'var(--fg3)', marginTop: '4px', lineHeight: 1.5 }}>{note}</div>
              </div>
              <Toggle
                on={!!charts[key]}
                trackOff={trackOff}
                size="sm"
                onClick={() => canWrite && setChart(key, !charts[key])}
              />
            </div>
          ))}
        </div>
      </Section>

      <Section
        en="DISPLAY · SEMESTERS"
        title="校历"
        desc="定义学期周轴。未配置时使用近 18 周。"
        actions={
          <Btn primary onClick={save} disabled={!canWrite || busy}>
            下发配置
          </Btn>
        }
      >
        <div style={{ maxWidth: '760px' }}>
          {semesters.length === 0 && (
            <div style={{ fontSize: '13px', color: 'var(--fg3)', padding: '4px 0 16px' }}>
              未配置学期。添加后按校历分周。
            </div>
          )}
          {semesters.map((sem, i) => {
            const start = parseDay(sem.start)
            const end = parseDay(sem.end)
            /* 立刻回显周数与错误。日期算错不报错，会让横轴错一周。
               必须在下发前可见。 */
            const bad =
              !start || !end
                ? '日期格式应为 YYYY-MM-DD'
                : end.getTime() <= start.getTime()
                  ? '结束日期必须晚于开始日期'
                  : sem.key !== sem.start.slice(0, 7)
                    ? '学期标识须与开学月一致：' + sem.start.slice(0, 7)
                    : ''
            return (
              <div
                key={i}
                style={{
                  display: 'grid',
                  gridTemplateColumns: 'minmax(150px,1fr) minmax(150px,1fr) auto auto',
                  alignItems: 'end',
                  gap: '18px',
                  padding: '18px 0',
                  borderBottom: '1px solid var(--line2)',
                }}
              >
                <Field label="START" hint="第 1 周的第一天">
                  <input
                    type="date"
                    value={sem.start}
                    onChange={(e) => setSemester(i, { start: e.target.value, key: e.target.value.slice(0, 7) })}
                    style={fieldStyle}
                    disabled={!canWrite}
                  />
                </Field>
                <Field label="END" hint="学期最后一天（含）">
                  <input
                    type="date"
                    value={sem.end}
                    onChange={(e) => setSemester(i, { end: e.target.value })}
                    style={fieldStyle}
                    disabled={!canWrite}
                  />
                </Field>
                <div style={{ paddingBottom: '9px', minWidth: '132px' }}>
                  <div style={{ ...mono(), marginBottom: '7px' }}>PREVIEW</div>
                  <div style={{ fontSize: '13px', fontWeight: 500 }}>
                    {sem.key ? semesterLabel(sem.key) : '—'}
                  </div>
                  <div style={{ fontSize: '11.5px', color: bad ? 'var(--red)' : 'var(--fg3)', marginTop: '5px' }}>
                    {bad || '第 1 周 – 第 ' + semesterWeekCount(sem) + ' 周'}
                  </div>
                </div>
                <div style={{ paddingBottom: '9px' }}>
                  <Btn danger onClick={() => canWrite && removeSemester(i)} disabled={!canWrite}>
                    删除
                  </Btn>
                </div>
              </div>
            )
          })}
          <div style={{ marginTop: '18px' }}>
            <Btn onClick={addSemester} disabled={!canWrite}>
              + 添加学期
            </Btn>
          </div>
        </div>
      </Section>

      <Section
        en="DISPLAY · TEXT"
        title="站点与统计"
        desc="配置站点名称、电费单价与空房判定阈值。"
      >
        <div
          data-r="split"
          style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(220px,1fr))', gap: '26px 34px' }}
        >
          <Field label="BRAND NAME" hint="站点名称。出现在侧栏、标题与邮件。">
            <input
              value={config.display.brand_name || ''}
              onChange={(e) => setConfig({ ...config, display: { ...config.display, brand_name: e.target.value } })}
              placeholder="POWER·PUSH"
              style={fieldStyle}
              disabled={!canWrite}
            />
          </Field>
          <Field label="AREA NAME" hint="学校名。留空则仅显示站点名。">
            <input
              value={config.display.area_name}
              onChange={(e) => setConfig({ ...config, display: { ...config.display, area_name: e.target.value } })}
              style={fieldStyle}
              disabled={!canWrite}
            />
          </Field>
          <Field label="CAMPUS NAME" hint="校区名。">
            <input
              value={config.display.campus_name}
              onChange={(e) => setConfig({ ...config, display: { ...config.display, campus_name: e.target.value } })}
              style={fieldStyle}
              disabled={!canWrite}
            />
          </Field>
          <Field label="ELECTRICITY RATE" hint="元/kWh。用户端换算金额。">
            <input
              value={config.display.electricity_rate || ''}
              onChange={(e) =>
                setConfig({ ...config, display: { ...config.display, electricity_rate: e.target.value } })
              }
              inputMode="decimal"
              placeholder="0.62"
              style={fieldStyle}
              disabled={!canWrite}
            />
          </Field>
          <Field
            label="EMPTY ROOM THRESHOLD"
            hint="日用电量低于此值记为空房。默认 0.3 kWh。"
          >
            <input
              value={config.display.empty_room_threshold_kwh ?? '0.3'}
              onChange={(e) =>
                setConfig({
                  ...config,
                  display: { ...config.display, empty_room_threshold_kwh: e.target.value },
                })
              }
              type="number"
              inputMode="decimal"
              min="0.0001"
              max="10"
              step="0.1"
              placeholder="0.3"
              style={fieldStyle}
              disabled={!canWrite}
            />
          </Field>
          <Field
            label="RANKING REFRESH TIME"
            hint="日/周/月榜在此时刻切换周期。"
          >
            <input
              type="time"
              value={config.display.ranking_refresh_time || '09:00'}
              onChange={(e) =>
                setConfig({ ...config, display: { ...config.display, ranking_refresh_time: e.target.value } })
              }
              style={fieldStyle}
              disabled={!canWrite}
            />
          </Field>
        </div>
        <div style={{ marginTop: '28px', maxWidth: '460px' }}>
          <div style={{ ...mono(), marginBottom: '10px' }}>REVISION</div>
          <Row label="配置版本" value={'v' + config.version} />
          <Row label="最近下发" value={fmtTime(config.updated_at)} />
        </div>
      </Section>
    </>
  )
}
