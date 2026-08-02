/* 侧栏。对齐原型 aside side。
   variant=static：PC 常驻（≤960px 由 CSS 隐藏）。
   variant=drawer：移动端抽屉。 */

import { useStore } from '../lib/store'
import { DEFAULT_BRAND_NAME } from '../config/features'
import { themeColors } from '../lib/format'
import { NAV_ICON, type User } from '../lib/mock'
import type { View } from '../lib/store'
import { canUseAdmin } from '../lib/adminAccess'
import AccountMenu from './AccountMenu'
import { BrandMark } from './ui'

type NavDef = [View, string, string, string]

const PERSONAL: NavDef[] = [
  ['overview', '数据概览', 'OVERVIEW', '余额 · 用电量 · 推送记录'],
  ['usage', '用电分析', 'USAGE', '余额曲线 · 用电日历 · 月度账单'],
  ['config', '推送设置', 'PUSH SETTINGS', '预警 · 定时 · 渠道'],
  ['account', '账号设置', 'ACCOUNT & PRIVACY', '账号 · 安全 · 隐私'],
]

const CAMPUS: NavDef[] = [
  ['campus', '数据看板', 'CAMPUS', '按楼栋 / 楼层查看全校负荷'],
  ['board', '排行榜', 'LEADERBOARD', '用电量 · 省电榜 · 环比涨幅'],
]

/* 管理侧六个页面。与用户页同为侧栏一行。
   共用 NavItem、view 状态与顶栏标题。不是独立 /admin 站点。 */
const SYSTEM: NavDef[] = [
  ['sys-console', '控制台', 'CONSOLE', '服务状态 · 调度 · 异常'],
  ['sys-scanner', '扫描器', 'SCANNER', '余额 / 账单 / 日明细任务'],
  ['sys-users', '用户管理', 'USERS', '角色 · 启停 · 会话 · 绑表'],
  ['sys-channels', '推送渠道', 'CHANNELS', '邮件供应商与凭证'],
  ['sys-captcha', '人机验证', 'CAPTCHA', '站点密钥 · 受保护动作'],
  ['sys-display', '前端展示', 'DISPLAY', '电价 · 校区名 · 图表开关'],
]

const VIEWS: NavDef[] = [...PERSONAL, ...CAMPUS, ...SYSTEM]

export function currentView(view: View) {
  return VIEWS.find((v) => v[0] === view) || VIEWS[0]
}

function NavItem({ v, after }: { v: [View, string, string, string]; after?: () => void }) {
  const { s, set } = useStore()
  const dark = s.theme === 'dark'
  const { RED, FG, FG2, FG3 } = themeColors(dark)
  const active = s.view === v[0]
  const icon = NAV_ICON[v[0]]
  return (
    <button
      data-r="navi"
      className="hv-fg"
      onClick={() => {
        set({ view: v[0] })
        after?.()
      }}
      style={{
        background: 'none',
        border: 0,
        borderTop: '1px solid var(--line2)',
        margin: 0,
        padding: '14px 0',
        font: 'inherit',
        textAlign: 'left',
        cursor: 'pointer',
        display: 'flex',
        alignItems: 'center',
        gap: '12px',
        whiteSpace: 'nowrap',
        color: active ? FG : FG2,
      }}
    >
      <span
        style={{
          width: '17px',
          height: '17px',
          flex: 'none',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          color: active ? RED : FG3,
        }}
      >
        <svg
          viewBox="0 0 24 24"
          width="17"
          height="17"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeLinejoin="round"
          style={{ display: 'block' }}
        >
          <path d={icon[0]}></path>
          <path d={icon[1]}></path>
        </svg>
      </span>
      <span style={{ fontSize: '13px', fontWeight: active ? 600 : 400, letterSpacing: '-.02em' }}>{v[1]}</span>
      <span style={{ marginLeft: 'auto', width: '14px', height: '1.5px', background: active ? RED : 'transparent' }} />
    </button>
  )
}

export default function Sidebar({ variant = 'static' }: { variant?: 'static' | 'drawer' }) {
  const { s, set } = useStore()
  const drawer = variant === 'drawer'
  const U: User | null = s.user
  const closeDrawer = drawer ? () => set({ mobileNavOpen: false, menuOpen: false }) : undefined

  /* 管理组只对运维/管理员出现，是「多一组选项卡」而不是「另一个后台」。
     后端每次请求仍然现查角色，这里藏起来只为了别给普通用户一排点不动的入口。 */
  const admin = canUseAdmin(U?.role)
  const navGroups = [
    { label: '账号用电', pt: '0', items: PERSONAL, tag: '' },
    { label: '全校用电', pt: '22px', items: CAMPUS, tag: '' },
    ...(admin ? [{ label: '系统管理', pt: '22px', items: SYSTEM, tag: U?.role === 'admin' ? 'ADMIN' : 'OPERATOR' }] : []),
  ]

  return (
    <aside
      data-r={drawer ? 'sidedrawer' : 'side'}
      style={{
        width: '238px',
        flex: 'none',
        ...(drawer
          ? {
              position: 'fixed',
              left: 0,
              top: 0,
              bottom: 0,
              zIndex: 70,
              overflowY: 'auto',
              animation: 'drawin .22s ease both',
              boxShadow: '14px 0 44px rgba(0,0,0,.18)',
            }
          : { position: 'sticky', top: 0, height: '100vh', zIndex: 30 }),
        borderRight: '1px solid var(--line)',
        display: 'flex',
        flexDirection: 'column',
        padding: '26px 22px 20px',
        background: 'var(--bg)',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: '11px', paddingBottom: '30px' }}>
        <BrandMark size={20} />
        <div style={{ display: 'flex', flexDirection: 'column', gap: '3px' }}>
          {/* 抬头两行都跟运维面板走：学校名在上、站点名在下。
              没配学校时只留站点名一行，免得同一个名字上下重复两遍。 */}
          <div style={{ fontWeight: 600, letterSpacing: '-.025em', fontSize: '13px' }}>
            {s.features.display.areaName || s.features.display.brandName || DEFAULT_BRAND_NAME}
          </div>
          {s.features.display.areaName && (
            <div style={{ font: "500 9px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
              {s.features.display.brandName || DEFAULT_BRAND_NAME}
            </div>
          )}
        </div>
        {drawer && (
          <button
            className="hv-line-fg"
            onClick={closeDrawer}
            title="收起菜单"
            style={{
              background: 'none',
              border: '1px solid var(--line)',
              margin: '0 0 0 auto',
              padding: 0,
              width: '30px',
              height: '30px',
              borderRadius: '999px',
              cursor: 'pointer',
              color: 'var(--fg2)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              flex: 'none',
            }}
          >
            <svg
              viewBox="0 0 24 24"
              width="15"
              height="15"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
              strokeLinecap="round"
              style={{ display: 'block' }}
            >
              <path d="M6 6l12 12M18 6L6 18"></path>
            </svg>
          </button>
        )}
      </div>
      {/* 含管理组共 12 行，小屏放不下。导航区自滚动，账号区钉底。 */}
      <nav
        data-r="nav"
        style={{ display: 'flex', flexDirection: 'column', flex: '0 1 auto', minHeight: 0, overflowY: 'auto' }}
      >
        {navGroups.map((g) => (
          <div key={g.label} data-r="navgrp" style={{ display: 'flex', flexDirection: 'column', paddingTop: g.pt }}>
            <div
              data-r="navhd"
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '8px',
                font: "500 9px/1 'JetBrains Mono',monospace",
                letterSpacing: '.2em',
                color: 'var(--fg3)',
                padding: '0 0 9px',
              }}
            >
              {g.label}
              {/* 自己是什么级别要写在入口旁边：operator 点开「用户管理」只能看，
                  不先说清楚，会以为是页面坏了 */}
              {g.tag && (
                <span style={{ marginLeft: 'auto', letterSpacing: '.14em', color: 'var(--fg2)' }}>{g.tag}</span>
              )}
            </div>
            {g.items.map((v) => (
              <NavItem key={v[0]} v={v} after={closeDrawer} />
            ))}
          </div>
        ))}
      </nav>
      <div data-r="sidefoot" style={{ marginTop: 'auto', position: 'relative', display: 'flex', flexDirection: 'column' }}>
        {s.menuOpen && (
          <>
            <div onClick={() => set({ menuOpen: false })} style={{ position: 'fixed', inset: 0, zIndex: 55 }} />
            <div
              style={{
                position: 'absolute',
                left: 0,
                right: 0,
                bottom: 'calc(100% + 8px)',
                border: '1px solid var(--line)',
                background: 'var(--bg)',
                zIndex: 56,
                boxShadow: '0 -10px 34px rgba(0,0,0,.14)',
                animation: 'menupop .18s ease both',
              }}
            >
              <AccountMenu onAction={closeDrawer} />
            </div>
          </>
        )}
        <button
          className="hv-op72"
          data-acct={U ? 'in' : 'out'}
          onClick={() => {
            if (!U) {
              set({ authView: 'login' })
              closeDrawer?.()
              return
            }
            set((p) => ({ menuOpen: !p.menuOpen }))
          }}
          style={{
            background: s.menuOpen ? 'var(--sub)' : 'transparent',
            border: 0,
            borderTop: '1px solid var(--line)',
            margin: 0,
            padding: '14px 0 4px',
            font: 'inherit',
            textAlign: 'left',
            cursor: 'pointer',
            display: 'flex',
            alignItems: 'center',
            gap: '10px',
            width: '100%',
          }}
        >
          <span
            style={{
              width: '26px',
              height: '26px',
              flex: 'none',
              background: U ? 'var(--fg)' : 'transparent',
              border: `1px solid ${U ? 'var(--fg)' : 'var(--line)'}`,
              color: U ? 'var(--bg)' : 'var(--fg3)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              font: "600 11px/1 'Instrument Sans','Noto Sans SC',sans-serif",
            }}
          >
            {U ? U.initial : '?'}
          </span>
          <span
            data-r="acctlbl"
            style={{ display: 'none', fontSize: '12.5px', fontWeight: 600, letterSpacing: '-.015em', color: 'var(--fg)' }}
          >
            登录
          </span>
          <span style={{ display: 'flex', flexDirection: 'column', gap: '3px', minWidth: 0, flex: 1 }}>
            <span
              style={{
                fontSize: '12.5px',
                fontWeight: 600,
                letterSpacing: '-.015em',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {U ? U.name : '未登录'}
            </span>
            <span
              style={{
                font: "400 10px/1 'JetBrains Mono',monospace",
                color: 'var(--fg3)',
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {U ? U.meter || '未绑定电表' : '点击登录'}
            </span>
          </span>
          <span
            style={{
              fontSize: '8px',
              color: 'var(--fg3)',
              flex: 'none',
              transition: 'transform .16s',
              transform: `rotate(${s.menuOpen ? '180deg' : '0deg'})`,
            }}
          >
            ▲
          </span>
        </button>
      </div>
    </aside>
  )
}
