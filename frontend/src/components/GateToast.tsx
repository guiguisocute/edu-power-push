/* 未登录 Gate 遮罩与 Toast。对齐原型对应片段。 */

import { useStore } from '../lib/store'

/** mode: signin = 未登录。bind = 已登录未绑表。推送与用电分析以电表为前提。 */
export function Gate({ mode = 'signin' }: { mode?: 'signin' | 'bind' }) {
  const { s, set, startBind } = useStore()
  const bind = mode === 'bind'
  const gateEn = bind ? 'METER REQUIRED' : 'SIGN IN REQUIRED'
  const gateTitle = bind
    ? s.view === 'config'
      ? '绑定电表后配置推送'
      : '绑定电表后查看用电'
    : s.view === 'config'
      ? '登录后配置推送'
      : '登录后查看用电'
  const gateDesc = bind
    ? '预警阈值、定时摘要与用电曲线均以绑定电表为准。'
    : '绑定电表号后可接收余额预警与用电摘要。'
  const gateTags = [{ l: '余额预警' }, { l: '定时摘要' }, { l: '用电分析' }]

  /* 遮罩铺满内容区。侧栏保持可点。卡片用 fixed 居中。
     内容超一屏时 absolute 会落到长页中点，不是屏幕中央。
     外层 pointerEvents:none 穿透到侧栏。卡片收回 auto。 */
  return (
    <>
      <div
        style={{
          position: 'absolute',
          left: 0,
          right: 0,
          top: 0,
          bottom: 0,
          zIndex: 40,
          background: 'color-mix(in srgb, var(--bg) 74%, transparent)',
        }}
      />
      <div
        style={{
          position: 'fixed',
          inset: 0,
          zIndex: 41,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: '24px',
          pointerEvents: 'none',
        }}
      >
      <div
        style={{
          width: '100%',
          maxWidth: '376px',
          display: 'flex',
          flexDirection: 'column',
          pointerEvents: 'auto',
          animation: 'rise .28s ease both',
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
          <span style={{ width: '5px', height: '5px', background: 'var(--red)', flex: 'none' }} />
          <span style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
            {gateEn}
          </span>
        </div>
        <div style={{ fontSize: '26px', fontWeight: 600, letterSpacing: '-.035em', lineHeight: 1.2, marginTop: '15px' }}>
          {gateTitle}
        </div>
        <div style={{ fontSize: '13px', color: 'var(--fg2)', lineHeight: 1.6, marginTop: '10px' }}>{gateDesc}</div>
        <div
          style={{
            display: 'flex',
            flexWrap: 'wrap',
            gap: '8px 20px',
            marginTop: '22px',
            paddingTop: '18px',
            borderTop: '1px solid var(--line)',
          }}
        >
          {gateTags.map((t) => (
            <span
              key={t.l}
              style={{ display: 'flex', alignItems: 'center', gap: '7px', fontSize: '12.5px', color: 'var(--fg2)' }}
            >
              <span style={{ width: '4px', height: '4px', background: 'var(--red)' }} />
              {t.l}
            </span>
          ))}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '10px', marginTop: '26px' }}>
          {bind ? (
            <button
              className="hv-op82"
              onClick={startBind}
              style={{
                margin: 0,
                padding: '12px 24px',
                border: '1px solid var(--fg)',
                borderRadius: '999px',
                font: 'inherit',
                fontSize: '13px',
                fontWeight: 600,
                cursor: 'pointer',
                background: 'var(--fg)',
                color: 'var(--bg)',
              }}
            >
              绑定电表
            </button>
          ) : (
            s.features.auth.registration && (
            <button
              className="hv-op82"
              onClick={() => set({ authView: 'register', matched: null, authErr: null })}
              style={{
                margin: 0,
                padding: '12px 24px',
                border: '1px solid var(--fg)',
                borderRadius: '999px',
                font: 'inherit',
                fontSize: '13px',
                fontWeight: 600,
                cursor: 'pointer',
                background: 'var(--fg)',
                color: 'var(--bg)',
              }}
            >
              注册
            </button>
            )
          )}
          {!bind && (
            <button
              className="hv-line-fg"
              onClick={() => set({ authView: 'login', authErr: null })}
              style={{
                margin: 0,
                padding: '12px 24px',
                border: '1px solid var(--line)',
                borderRadius: '999px',
                font: 'inherit',
                fontSize: '13px',
                fontWeight: 500,
                cursor: 'pointer',
                background: 'none',
                color: 'var(--fg2)',
              }}
            >
              登录
            </button>
          )}
          <button
            className="hv-fg"
            onClick={() => set({ view: 'campus' })}
            style={{
              background: 'none',
              border: 0,
              marginLeft: 'auto',
              padding: 0,
              font: 'inherit',
              fontSize: '12.5px',
              color: 'var(--fg3)',
              cursor: 'pointer',
            }}
          >
            查看全校数据 →
          </button>
        </div>
      </div>
      </div>
    </>
  )
}

export function Toast() {
  const { s } = useStore()
  if (!s.toast) return null
  return (
    <div
      key={s.toast}
      style={{
        position: 'fixed',
        left: '50%',
        bottom: '34px',
        transform: 'translateX(-50%)',
        zIndex: 80,
        display: 'flex',
        alignItems: 'center',
        gap: '11px',
        padding: '13px 24px',
        borderRadius: '999px',
        background: 'var(--fg)',
        color: 'var(--bg)',
        fontSize: '13px',
        fontWeight: 500,
        animation: 'toastin .2s ease both',
        boxShadow: '0 10px 34px rgba(0,0,0,.22)',
      }}
    >
      <span style={{ width: '5px', height: '5px', borderRadius: '99px', background: 'var(--red)' }} />
      {s.toast}
    </div>
  )
}
