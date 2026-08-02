/* 顶栏。对齐原型 header top。 */

import { useStore } from '../lib/store'
import { seg } from '../lib/format'
import { SegGroup, SegBtn } from './ui'
import { currentView } from './Sidebar'

export default function TopBar() {
  const { s, set, persist, say, applyTheme } = useStore()
  const dark = s.theme === 'dark'
  const RMB = s.unit === 'rmb'
  const cur = currentView(s.view)

  const unitTabs = ([
    ['kwh', 'kWh'],
    ['rmb', '元'],
  ] as const).map((u) => ({
    seg: seg(s.unit === u[0], u[1], () => {
      set({ unit: u[0] })
      persist({ unit: u[0] })
      say(u[0] === 'rmb' ? '已按 0.62 元/kWh 折算为电费' : '已切回用电量 kWh')
    }),
    title: u[0] === 'rmb' ? '按 0.62 元/kWh 将用电量折算为电费' : '显示原始用电量 kWh',
  }))

  return (
    <header
      data-r="top"
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '16px',
        padding: '0 44px',
        height: '57px',
        borderBottom: '1px solid var(--line)',
        position: 'sticky',
        top: 0,
        background: 'var(--bg)',
        zIndex: 20,
      }}
    >
      <button
        className="hv-line-fg m-only-flex"
        onClick={() => set({ mobileNavOpen: true })}
        title="打开菜单"
        style={{
          display: 'none',
          background: 'none',
          border: '1px solid var(--line)',
          margin: 0,
          padding: 0,
          width: '32px',
          height: '32px',
          borderRadius: '999px',
          cursor: 'pointer',
          color: 'var(--fg2)',
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
          <path d="M4 7h16M4 12h16M4 17h10"></path>
        </svg>
      </button>
      <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
        {cur[2]}
      </div>
      <div data-r="hidesm" style={{ width: '1px', height: '14px', background: 'var(--line)' }} />
      <div data-r="hidesm" style={{ fontSize: '13px', color: 'var(--fg2)' }}>
        {cur[3]}
      </div>
      <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: '16px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '9px' }}>
          <span data-r="hidesm" style={{ fontSize: '11.5px', color: 'var(--fg3)', whiteSpace: 'nowrap' }}>
            切换全站数值单位
          </span>
          <SegGroup>
            {unitTabs.map((u, i) => (
              <SegBtn key={i} seg={u.seg} pad="5px 13px" fs="12px" title={u.title} />
            ))}
          </SegGroup>
          <span
            data-r="hidesm"
            style={{
              font: "400 10.5px/1 'JetBrains Mono',monospace",
              color: RMB ? (dark ? '#FF3B4E' : '#D6001C') : 'var(--fg3)',
            }}
          >
            {RMB ? '× 0.62 元/kWh' : '0.62 元/kWh'}
          </span>
        </div>
        <button
          className="hv-line-fg"
          onClick={() => {
            const t = dark ? 'light' : 'dark'
            applyTheme(t)
            set({ theme: t })
            persist({ theme: t })
          }}
          title="切换深浅色"
          style={{
            background: 'none',
            border: '1px solid var(--line)',
            margin: 0,
            padding: 0,
            width: '32px',
            height: '32px',
            borderRadius: '999px',
            cursor: 'pointer',
            color: 'var(--fg2)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            fontSize: '12px',
          }}
        >
          {dark ? '☀' : '☾'}
        </button>
      </div>
    </header>
  )
}
