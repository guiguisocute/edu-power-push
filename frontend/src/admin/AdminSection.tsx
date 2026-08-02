/* 管理侧内容区。主站侧栏中的管理选项卡页面。
   共用外壳、侧栏、会话与设计语言。
   安全由服务端保证：角色分层与会话控制。
   前端仅隐藏普通用户入口，不是门禁本身。 */

import { useEffect, useState } from 'react'
import { adminApi, type AdminSession } from './api'
import ErrorBoundary from './ErrorBoundary'
import { useStore } from '../lib/store'
import type { AdminView } from '../lib/adminAccess'
import { Empty, mono } from './ui'
import DashboardView from './DashboardView'
import ScannerView from './ScannerView'
import UsersView from './UsersView'
import ChannelsView from './ChannelsView'
import DisplayView from './DisplayView'
import CaptchaView from './CaptchaView'

const LABEL: Record<AdminView, string> = {
  'sys-console': '控制台',
  'sys-scanner': '扫描器',
  'sys-users': '用户管理',
  'sys-channels': '推送渠道',
  'sys-captcha': '人机验证',
  'sys-display': '前端展示',
}

/** 环境角标。区分测试与生产，避免误操作生产。 */
const ENV_LABEL: Record<string, { text: string; bg: string }> = {
  development: { text: 'DEV', bg: '#6b7280' },
  test: { text: 'TEST', bg: '#c88a00' },
  production: { text: 'PROD', bg: 'var(--red)' },
}

export default function AdminSection({ view }: { view: AdminView }) {
  const { s, say } = useStore()
  const [session, setSession] = useState<AdminSession | null>(null)
  const [denied, setDenied] = useState<'forbidden' | 'unauthorized' | null>(null)

  const err = (msg: string) => say(msg, 6000)

  /* 侧栏靠 /me 角色显示入口，不够权威。
     此处向 /admin/session 再确认，并取环境、版本与凭证可存状态。
     403 = 角色不足。401 = 会话失效。文案必须区分。 */
  useEffect(() => {
    let alive = true
    adminApi
      .session()
      .then((next) => {
        if (!alive) return
        setSession(next)
        setDenied(null)
      })
      .catch((e) => {
        if (!alive) return
        setDenied((e as { status?: number }).status === 403 ? 'forbidden' : 'unauthorized')
      })
    return () => {
      alive = false
    }
  }, [])

  if (denied) {
    return (
      <div style={{ padding: '56px 0' }}>
        <Empty
          title={denied === 'forbidden' ? '账号已无管理权限' : '会话已失效'}
          desc={
            denied === 'forbidden'
              ? '账号已被降为普通用户。刷新后入口消失。'
              : '登录已过期。请重新登录。'
          }
        />
      </div>
    )
  }

  /* 校验返回前禁止渲染。提前渲染会先显示只读再变可写。 */
  if (!session) {
    return (
      <div style={{ padding: '56px 0' }}>
        <Empty title="正在校验管理权限" desc="确认账号角色与环境。" />
      </div>
    )
  }

  const canManage = session.actor.role === 'admin'
  const env = ENV_LABEL[session.environment] || ENV_LABEL.development

  return (
    <>
      {/* 身份、环境与版本状态带必须常驻。管理与用户侧共用界面时的分界。 */}
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: '12px',
          flexWrap: 'wrap',
          padding: '18px 0 0',
        }}
      >
        <span style={mono('9.5px', '.2em')}>SYSTEM ADMIN</span>
        <span
          style={{
            font: "600 9px/1 'JetBrains Mono',monospace",
            letterSpacing: '.14em',
            padding: '4px 7px',
            background: env.bg,
            color: '#fff',
          }}
        >
          {env.text}
        </span>
        <span style={{ fontSize: '12px', color: 'var(--fg3)' }}>
          {session.actor.label}
          <span style={{ marginLeft: '8px', color: 'var(--fg2)' }}>{session.actor.role}</span>
        </span>
        <span style={{ marginLeft: 'auto', fontSize: '12px', color: 'var(--fg3)' }}>版本 {session.version}</span>
      </div>

      {!session.secrets_ready && (
        <div
          style={{
            border: '1px solid var(--red)',
            padding: '12px 16px',
            marginTop: '18px',
            fontSize: '12.5px',
            lineHeight: 1.7,
          }}
        >
          未配置 <code>SETTINGS_ENCRYPTION_KEY</code>。凭证无法保存。见「推送渠道」。
        </div>
      )}

      {/* 每页各自兜底。一处渲染错误仅影响该页。 */}
      <ErrorBoundary label={LABEL[view]} resetKey={view}>
        {view === 'sys-console' && <DashboardView onError={err} />}
        {view === 'sys-scanner' && <ScannerView canWrite onError={err} onToast={say} />}
        {view === 'sys-users' && (
          <UsersView canManage={canManage} selfID={session.actor.user_id} onError={err} onToast={say} />
        )}
        {view === 'sys-channels' && <ChannelsView canWrite={canManage} onError={err} onToast={say} />}
        {view === 'sys-captcha' && <CaptchaView canWrite={canManage} onError={err} onToast={say} />}
        {view === 'sys-display' && (
          <DisplayView
            canWrite
            canManageSecrets={canManage}
            dark={s.theme === 'dark'}
            onError={err}
            onToast={say}
          />
        )}
      </ErrorBoundary>
    </>
  )
}
