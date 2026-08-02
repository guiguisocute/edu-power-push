/* 内联绑表。位于概览 hero 余额位置。
   大号输入框顶替大余额。匹配后原地确认地址。
   仅一个电表号，禁止用全屏浮层。入口与展开同在此行。 */

import { useState } from 'react'
import { useStore } from '../lib/store'
import { METER_MAX_LEN, METER_PLACEHOLDER } from '../lib/meter'
import CaptchaGate from './CaptchaGate'

export default function InlineBindMeter() {
  const { s, set, setForm, lookupMeter, doRebind } = useStore()
  const M = s.matched
  const cur = s.user?.meter
  const [captchaReset, setCaptchaReset] = useState(0)
  const [captchaState, setCaptchaState] = useState({ required: false, ready: false, token: '' })
  const close = () => set({ bindOpen: false, matched: null, authErr: null })
  const lookup = () => {
    if (!captchaState.ready) {
      set({ authErr: '请先完成人机验证' })
      return
    }
    lookupMeter(captchaState.token)
    if (captchaState.required) setCaptchaReset((value) => value + 1)
  }

  const solid = {
    margin: 0,
    padding: '13px 26px',
    border: '1px solid var(--fg)',
    borderRadius: '999px',
    font: 'inherit',
    fontSize: '13px',
    fontWeight: 600,
    cursor: 'pointer',
    background: 'var(--fg)',
    color: 'var(--bg)',
    whiteSpace: 'nowrap' as const,
    flex: 'none' as const,
  }
  const ghost = {
    margin: 0,
    padding: '13px 22px',
    border: '1px solid var(--line)',
    borderRadius: '999px',
    font: 'inherit',
    fontSize: '13px',
    fontWeight: 500,
    cursor: 'pointer',
    background: 'none',
    color: 'var(--fg2)',
    whiteSpace: 'nowrap' as const,
    flex: 'none' as const,
  }
  const micro = {
    font: "500 9px/1 'JetBrains Mono',monospace",
    letterSpacing: '.16em',
    color: 'var(--fg3)',
  }

  return (
    <div style={{ marginTop: '26px', animation: 'rise .24s ease both' }}>
      {M ? (
        <>
          <div style={micro}>METER MATCHED</div>
          <div
            style={{
              fontSize: 'clamp(30px,4.4vw,46px)',
              fontWeight: 600,
              letterSpacing: '-.04em',
              lineHeight: 1.1,
              marginTop: '14px',
            }}
          >
            {M.place}
          </div>
          <div
            style={{
              font: "400 13px/1 'JetBrains Mono',monospace",
              color: 'var(--fg3)',
              marginTop: '12px',
              fontVariantNumeric: 'tabular-nums',
            }}
          >
            {M.no}
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: '11px', marginTop: '26px', flexWrap: 'wrap' }}>
            <button
              className="hv-op82"
              onClick={doRebind}
              disabled={s.authBusy}
              style={{ ...solid, opacity: s.authBusy ? 0.6 : 1, cursor: s.authBusy ? 'default' : 'pointer' }}
            >
              {s.authBusy ? '绑定中…' : cur ? '确认更换' : '确认绑定'}
            </button>
            <button className="hv-line-fg" onClick={() => set({ matched: null, authErr: null })} style={ghost}>
              重填表号
            </button>
            <button
              className="hv-fg"
              onClick={close}
              style={{
                background: 'none',
                border: 0,
                margin: 0,
                padding: 0,
                font: 'inherit',
                fontSize: '12.5px',
                color: 'var(--fg3)',
                cursor: 'pointer',
              }}
            >
              取消
            </button>
          </div>
        </>
      ) : (
        <>
          <div style={{ display: 'flex', alignItems: 'baseline', gap: '12px' }}>
            <span style={micro}>METER NO.</span>
            {cur && (
              <span style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>当前绑定 {cur}</span>
            )}
          </div>
          <div style={{ display: 'flex', alignItems: 'flex-end', gap: '18px', marginTop: '8px' }}>
            <input
              className="bigfield"
              autoFocus
              value={s.form.meter}
              inputMode="numeric"
              maxLength={METER_MAX_LEN}
              placeholder={METER_PLACEHOLDER}
              onChange={(e) => setForm('meter', e.target.value.replace(/\D/g, ''))}
              onKeyDown={(e) => e.key === 'Enter' && lookup()}
              style={{
                flex: 1,
                minWidth: 0,
                background: 'none',
                border: 0,
                borderBottom: '1px solid var(--line)',
                padding: '4px 0 10px',
                color: 'var(--fg)',
                fontFamily: 'inherit',
                fontSize: 'clamp(38px,5.6vw,68px)',
                fontWeight: 600,
                lineHeight: 1,
                letterSpacing: '-.045em',
                fontVariantNumeric: 'tabular-nums',
              }}
            />
            <button
              className="hv-op82"
              onClick={lookup}
              disabled={s.authBusy || !captchaState.ready}
              style={{
                ...solid,
                opacity: s.authBusy || !captchaState.ready ? 0.6 : 1,
                cursor: s.authBusy || !captchaState.ready ? 'default' : 'pointer',
              }}
            >
              {s.authBusy ? '查询中…' : '查询'}
            </button>
          </div>
          <div style={{ marginTop: '18px' }}>
            <CaptchaGate
              action="meter.preview"
              resetKey={captchaReset}
              onChange={({ required, ready, token }) => setCaptchaState({ required, ready, token })}
            />
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: '12px', marginTop: '14px', flexWrap: 'wrap' }}>
            <span style={{ fontSize: '12px', color: 'var(--fg3)' }}>
              后勤服务——水电管理可查 · 一块表可最多同时绑定 4 个账号
            </span>
            <button
              className="hv-fg"
              onClick={close}
              style={{
                background: 'none',
                border: 0,
                margin: '0 0 0 auto',
                padding: 0,
                font: 'inherit',
                fontSize: '12.5px',
                color: 'var(--fg3)',
                cursor: 'pointer',
              }}
            >
              取消
            </button>
          </div>
        </>
      )}

      {s.authErr && (
        <div style={{ display: 'flex', alignItems: 'center', gap: '9px', marginTop: '18px' }}>
          <span style={{ width: '5px', height: '5px', background: 'var(--red)', flex: 'none' }} />
          <span style={{ fontSize: '12.5px', color: 'var(--red)' }}>{s.authErr}</span>
        </div>
      )}
    </div>
  )
}
