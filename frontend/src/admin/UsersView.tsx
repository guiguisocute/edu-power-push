/* 用户管理：查找、看状态、改角色、踢下线。
   邮箱原样展示。本页供运维找人，禁止打码。
   真正门槛是谁能打开本页。 */

import { useEffect, useState } from 'react'
import { adminApi, type AdminRole, type AdminUser } from './api'
import { Btn, Dot, Empty, Section, fieldStyle, fmtTime, mono } from './ui'

const ROLE_LABEL: Record<AdminRole, string> = { user: '普通用户', operator: '运维', admin: '管理员' }

export default function UsersView({
  canManage,
  selfID,
  onError,
  onToast,
}: {
  canManage: boolean
  selfID: string
  onError: (msg: string) => void
  onToast: (msg: string) => void
}) {
  const [items, setItems] = useState<AdminUser[]>([])
  const [total, setTotal] = useState(0)
  const [query, setQuery] = useState('')
  const [roleFilter, setRoleFilter] = useState('')
  const [busy, setBusy] = useState(false)
  const [armedAction, setArmedAction] = useState<string | null>(null)
  const [tick, setTick] = useState(0)

  useEffect(() => {
    let alive = true
    // 输入防抖。禁止每敲一字就查询一次。
    const timer = setTimeout(() => {
      adminApi
        .users({ q: query.trim(), role: roleFilter })
        .then((page) => {
          if (!alive) return
          setItems(page.items)
          setTotal(page.total)
        })
        .catch((e) => alive && onError(String(e instanceof Error ? e.message : e)))
    }, query ? 300 : 0)
    return () => {
      alive = false
      clearTimeout(timer)
    }
  }, [query, roleFilter, tick])

  const mutate = async (fn: () => Promise<unknown>, message: string) => {
    setBusy(true)
    setArmedAction(null)
    try {
      await fn()
      onToast(message)
      setTick((n) => n + 1)
    } catch (e) {
      onError(String(e instanceof Error ? e.message : e))
    } finally {
      setBusy(false)
    }
  }

  const confirmThen = (key: string, fn: () => Promise<unknown>, message: string) => {
    if (armedAction !== key) {
      setArmedAction(key)
      return
    }
    void mutate(fn, message)
  }

  return (
    <Section
      en="USERS"
      title="用户管理"
      desc={`共 ${total.toLocaleString()} 个账号。角色控制面板权限。禁用吊销全部会话。`}
      actions={
        <>
          <input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="搜邮箱 / 昵称 / 电表号"
            style={{ ...fieldStyle, width: '220px' }}
          />
          <select
            value={roleFilter}
            onChange={(e) => setRoleFilter(e.target.value)}
            style={{ ...fieldStyle, width: '130px', cursor: 'pointer' }}
          >
            <option value="">全部角色</option>
            <option value="admin">管理员</option>
            <option value="operator">运维</option>
            <option value="user">普通用户</option>
          </select>
        </>
      }
    >
      {items.length === 0 ? (
        <Empty
          title={query ? '没有匹配的账号' : '还没有账号'}
          desc={query ? '请更换关键词：邮箱、昵称或电表号。' : '用户注册后会出现在这里。'}
        />
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <div style={{ minWidth: '1040px' }}>
            <div
              style={{
                display: 'flex',
                gap: '14px',
                padding: '0 0 10px',
                borderBottom: '1px solid var(--line)',
                ...mono('9px', '.16em'),
              }}
            >
              <span style={{ flex: 1, minWidth: 0 }}>ACCOUNT</span>
              <span style={{ width: '150px', flex: 'none' }}>METER</span>
              <span style={{ width: '120px', flex: 'none' }}>ACTIVITY</span>
              <span style={{ width: '120px', flex: 'none' }}>ROLE</span>
              <span style={{ width: '350px', flex: 'none', textAlign: 'right' }}>ACTIONS</span>
            </div>
            {items.map((u) => {
              const isSelf = u.id === selfID
              return (
                <div
                  key={u.id}
                  style={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '14px',
                    padding: '14px 0',
                    borderBottom: '1px solid var(--line2)',
                    fontSize: '12.5px',
                    opacity: u.status === 'active' ? 1 : 0.5,
                  }}
                >
                  <span style={{ flex: 1, minWidth: 0 }}>
                    <span style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                      <Dot tone={u.status === 'active' ? (u.email_verified_at ? 'ok' : 'warn') : 'bad'} />
                      <span style={{ fontWeight: 500 }}>{u.nickname || u.email.split('@')[0]}</span>
                      {isSelf && <span style={{ fontSize: '11px', color: 'var(--red)' }}>（你）</span>}
                    </span>
                    <span
                      style={{
                        display: 'block',
                        font: "400 11.5px/1.5 'JetBrains Mono',monospace",
                        color: 'var(--fg3)',
                        marginTop: '4px',
                      }}
                    >
                      {u.email}
                      {!u.email_verified_at && <span style={{ color: 'var(--red)' }}> · 邮箱未验证</span>}
                    </span>
                  </span>
                  <span style={{ width: '150px', flex: 'none', color: 'var(--fg3)', minWidth: 0 }}>
                    {u.meter ? (
                      <>
                        <span style={{ font: "400 11.5px/1.5 'JetBrains Mono',monospace" }}>{u.meter}</span>
                        <span style={{ display: 'block', fontSize: '11px' }}>{u.place}</span>
                      </>
                    ) : (
                      '未绑表'
                    )}
                  </span>
                  <span style={{ width: '120px', flex: 'none', color: 'var(--fg3)', fontSize: '11.5px' }}>
                    {u.active_sessions} 个会话
                    <span style={{ display: 'block' }}>{u.channels_enabled} 个渠道</span>
                    <span style={{ display: 'block' }}>{fmtTime(u.last_login_at)}</span>
                  </span>
                  <span style={{ width: '120px', flex: 'none' }}>
                    {canManage && !isSelf ? (
                      <select
                        value={u.role}
                        disabled={busy}
                        onChange={(e) =>
                          mutate(
                            () => adminApi.updateUser(u.id, { role: e.target.value as AdminRole }),
                            '角色已更新',
                          )
                        }
                        style={{ ...fieldStyle, fontSize: '12px', cursor: 'pointer' }}
                      >
                        <option value="user">普通用户</option>
                        <option value="operator">运维</option>
                        <option value="admin">管理员</option>
                      </select>
                    ) : (
                      <span style={{ color: u.role === 'user' ? 'var(--fg3)' : 'var(--fg)' }}>{ROLE_LABEL[u.role]}</span>
                    )}
                  </span>
                  <span style={{ width: '350px', flex: 'none', display: 'flex', gap: '8px', justifyContent: 'flex-end' }}>
                    {canManage && !isSelf && (
                      <>
                        {u.active_sessions > 0 && (
                          <Btn
                            disabled={busy}
                            title="吊销全部会话。用户可重新登录。"
                            onClick={() => mutate(() => adminApi.revokeUserSessions(u.id), '已踢下线')}
                          >
                            踢下线
                          </Btn>
                        )}
                        {u.meter && (
                          <Btn
                            danger={armedAction === `unbind:${u.id}`}
                            primary={armedAction === `unbind:${u.id}`}
                            disabled={busy}
                            title="清除电表绑定。历史数据保留。"
                            onClick={() =>
                              confirmThen(
                                `unbind:${u.id}`,
                                () => adminApi.unbindUserMeter(u.id),
                                '电表绑定已清空',
                              )
                            }
                          >
                            {armedAction === `unbind:${u.id}` ? '确认解绑' : '解绑'}
                          </Btn>
                        )}
                        <Btn
                          danger={u.status === 'active'}
                          disabled={busy}
                          onClick={() =>
                            mutate(
                              () =>
                                adminApi.updateUser(u.id, {
                                  status: u.status === 'active' ? 'disabled' : 'active',
                                }),
                              u.status === 'active' ? '账号已禁用' : '账号已启用',
                            )
                          }
                        >
                          {u.status === 'active' ? '禁用' : '启用'}
                        </Btn>
                        <Btn
                          danger
                          primary={armedAction === `delete:${u.id}`}
                          disabled={busy}
                          title="永久删除账号与配置。电表历史保留。"
                          onClick={() =>
                            confirmThen(
                              `delete:${u.id}`,
                              () => adminApi.deleteUser(u.id),
                              '账号已永久删除',
                            )
                          }
                        >
                          {armedAction === `delete:${u.id}` ? '确认删号' : '删号'}
                        </Btn>
                      </>
                    )}
                  </span>
                </div>
              )
            })}
          </div>
        </div>
      )}
      {!canManage && (
        <div style={{ fontSize: '11.5px', color: 'var(--fg3)', marginTop: '18px', lineHeight: 1.7 }}>
          运维角色仅可查看。禁止改角色或启停账号。
        </div>
      )}
    </Section>
  )
}
