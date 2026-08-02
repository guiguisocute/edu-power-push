/* 账号菜单内容。PC 侧栏底部弹层与移动端顶部下拉共用。 */

import { useStore } from '../lib/store'
import { themeColors } from '../lib/format'

export default function AccountMenu({ onAction }: { onAction?: () => void }) {
  const { s, set, startBind, doLogout } = useStore()
  const dark = s.theme === 'dark'
  const { RED, FG } = themeColors(dark)
  const U = s.user

  const menuItems = [
    {
      k: '01',
      label: U && !U.meter ? '绑定电表' : '更换绑定电表',
      fg: FG,
      go: () => {
        startBind()
        onAction?.()
      },
    },
    {
      k: '02',
      label: '推送设置',
      fg: FG,
      go: () => {
        set({ menuOpen: false, view: 'config' })
        onAction?.()
      },
    },
    {
      k: '03',
      label: '账号与隐私',
      fg: FG,
      go: () => {
        set({ menuOpen: false, view: 'account' })
        onAction?.()
      },
    },
    {
      k: '04',
      label: '退出登录',
      fg: RED,
      go: () => {
        // live 必须让后端吊销 refresh family。禁止只清本地状态。
        doLogout()
        onAction?.()
      },
    },
  ]

  return (
    <>
      <div
        style={{
          padding: '14px 15px 13px',
          borderBottom: '1px solid var(--line2)',
          display: 'flex',
          flexDirection: 'column',
          gap: '7px',
        }}
      >
        <div style={{ font: "500 9px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
          METER
        </div>
        <div style={{ font: "400 11.5px/1.4 'JetBrains Mono',monospace", color: 'var(--fg2)' }}>
          {U?.meter ? U.meter + ' · ' + s.features.display.campusName : U ? '未绑定电表' : '未登录'}
        </div>
        <div style={{ fontSize: '12px', color: 'var(--fg3)' }}>{U?.place || (U ? '绑定电表后显示' : '登录后显示')}</div>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', padding: '5px' }}>
        {menuItems.map((m) => (
          <button
            key={m.k}
            className="hv-sub"
            onClick={m.go}
            style={{
              background: 'none',
              border: 0,
              margin: 0,
              padding: '10px 11px',
              font: 'inherit',
              textAlign: 'left',
              cursor: 'pointer',
              fontSize: '12.5px',
              display: 'flex',
              alignItems: 'center',
              gap: '10px',
              color: m.fg,
            }}
          >
            <span
              style={{
                font: "500 9px/1 'JetBrains Mono',monospace",
                letterSpacing: '.14em',
                color: 'var(--fg3)',
                width: '22px',
              }}
            >
              {m.k}
            </span>
            {m.label}
          </button>
        ))}
      </div>
    </>
  )
}
