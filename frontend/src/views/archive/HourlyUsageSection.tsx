/* 归档：分时用电（热力图与时段画像）。
   本校无可靠小时数据，默认不挂载。
   features.charts.hourly_usage 打开后由 UsageView 引入。 */

import { useStore } from '../../lib/store'
import { makeFmt, themeColors } from '../../lib/format'
import { monthHours, type DayRec } from '../../lib/mock'
import { IS_LIVE } from '../../api/mode'
import { useLiveHeatmap } from '../../api/live'
import LiveNote from '../../components/LiveNote'

export default function HourlyUsageSection({
  days,
  m,
  monthLabel,
}: {
  days: DayRec[]
  m: number
  monthLabel: string
}) {
  const { s, set } = useStore()
  const { RED, FG2 } = themeColors(s.theme === 'dark')
  const rate = parseFloat(s.features.display.electricityRate) || 0.62
  const { U, f2, alt } = makeFmt(s.unit, rate)
  const lhm = useLiveHeatmap()

  const HM = monthHours(days)
  const cmax = Math.max(...HM.map((r) => Math.max(...r))) || 1
  const hs = s.heat
  const pad = (n: number) => String(n).padStart(2, '0')
  const hcell =
    hs && hs[0] < days.length
      ? hs
      : (() => {
          let b = [0, 0]
          let mv = -1
          HM.forEach((r, d) =>
            r.forEach((v, h) => {
              if (v > mv) {
                mv = v
                b = [d, h]
              }
            }),
          )
          return b
        })()

  const hourSum = Array.from({ length: 24 }, (_, h) => HM.reduce((a, r) => a + r[h], 0) / (days.length || 1))
  const dayTot = hourSum.reduce((a, x) => a + x, 0) || 1
  const bandRows = (
    [
      ['深夜', '00–07', 0, 8],
      ['白天', '08–18', 8, 19],
      ['晚间', '19–23', 19, 24],
    ] as const
  ).map((b, i) => {
    const v = hourSum.slice(b[2], b[3]).reduce((a, x) => a + x, 0)
    const p = (v / dayTot) * 100
    return {
      name: b[0],
      hours: b[1],
      perHour: v / (b[3] - b[2]),
      kwh: f2(v),
      pct: p.toFixed(0) + '%',
      w: p.toFixed(1) + '%',
      cost: (v * rate).toFixed(2),
      alt: alt(v),
      bg: i === 2 ? RED : 'color-mix(in srgb, var(--fg) ' + (i === 1 ? 34 : 20) + '%, transparent)',
      fg: i === 2 ? RED : FG2,
    }
  })
  const pk = bandRows.reduce((a, b) => (a.perHour > b.perHour ? a : b))
  const topHours = hourSum
    .map((v, h) => ({ h, v }))
    .sort((a, b) => b.v - a.v)
    .slice(0, 3)
    .map((x, i) => ({
      rank: pad(i + 1),
      hour: pad(x.h) + ':00–' + pad((x.h + 1) % 24) + ':00',
      kwh: f2(x.v),
      pct: ((x.v / dayTot) * 100).toFixed(1) + '%',
    }))

  const heatRows = HM.map((r, d) => ({
    label: m + 1 + '.' + pad(days[d].d),
    cells: r.map((v, h) => ({
      o: (0.06 + (v / cmax) * 0.94).toFixed(3),
      ring: hs && hs[0] === d && hs[1] === h ? '1.5px solid var(--fg)' : 'none',
      on: () => set({ heat: [d, h] as [number, number] }),
    })),
  }))
  const hourCols = Array.from({ length: 24 }, (_, i) => ({ l: pad(i), o: i % 3 === 0 ? '1' : '0' }))
  const resetHeat = () => set({ heat: null })
  const heatLegend = [0.12, 0.32, 0.55, 0.78, 1].map((o) => ({ o: String(o) }))
  /* live 下整块热力图与时段画像都被 heatwrap 的 display:none 藏起来了（上游没有小时数据），
     可这两个读数在 heatwrap 外面 —— 不一起遮掉，页头就会挂着一个由日用电摊出来的「峰值小时」。 */
  const heatLabel =
    !IS_LIVE && days.length
      ? m + 1 + '.' + pad(days[hcell[0]]?.d ?? 1) + ' · ' + pad(hcell[1]) + ':00' + (hs ? '' : ' （峰值）')
      : '—'
  const heatValue = !IS_LIVE && days.length && HM[hcell[0]] ? f2(HM[hcell[0]][hcell[1]]) + ' ' + U : '—'
  const heatSub = monthLabel + ' · ' + days.length + ' 天 × 24 小时'
  const dayTotal = f2(dayTot)
  const bandInsight =
    pk.name +
    '（' +
    pk.hours +
    '）单位小时用电最高，' +
    f2(pk.perHour) +
    ' ' +
    U +
    '/小时，占全天 ' +
    pk.pct +
    '、约 ' +
    pk.cost +
    ' 元/天，是最值得压的时段。'
  const unitLabel = U

  return (
    <section style={{ padding: '44px 0 40px', borderBottom: '1px solid var(--line)' }}>
      <div style={{ display: 'flex', alignItems: 'flex-end', gap: '38px', flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
          <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
            HOURLY HEATMAP · {heatSub}
          </div>
          <div style={{ fontSize: '34px', fontWeight: 600, letterSpacing: '-.04em' }}>分时用电</div>
        </div>
        <div style={{ marginLeft: 'auto', display: 'flex', flexDirection: 'column', gap: '5px', alignItems: 'flex-end' }}>
          <div style={{ font: "400 11px/1 'JetBrains Mono',monospace", color: 'var(--fg3)' }}>{heatLabel}</div>
          <div style={{ fontSize: '19px', fontWeight: 600, fontVariantNumeric: 'tabular-nums' }}>{heatValue}</div>
        </div>
      </div>
      <div
        data-r="heatwrap"
        style={IS_LIVE ? { display: 'none' } : { display: 'flex', alignItems: 'stretch', marginTop: '30px' }}
      >
        <div data-r="scroll" style={{ flex: 'none' }}>
          <div style={{ minWidth: 0 }}>
            <div style={{ display: 'grid', gridTemplateColumns: '46px auto', gap: '10px', justifyContent: 'start' }}>
              <div />
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(24,14px)', gap: '3px' }}>
                {hourCols.map((h, i) => (
                  <div
                    key={i}
                    style={{
                      font: "400 9px/1 'JetBrains Mono',monospace",
                      color: 'var(--fg3)',
                      textAlign: 'center',
                      opacity: h.o,
                    }}
                  >
                    {h.l}
                  </div>
                ))}
              </div>
            </div>
            <div
              key={monthLabel + '-heat'}
              className="chart-anim"
              onMouseLeave={resetHeat}
              style={{ display: 'flex', flexDirection: 'column', gap: '3px', marginTop: '9px' }}
            >
              {heatRows.map((row, i) => (
                <div
                  key={i}
                  style={{
                    display: 'grid',
                    gridTemplateColumns: '46px auto',
                    gap: '10px',
                    justifyContent: 'start',
                    alignItems: 'center',
                  }}
                >
                  <div
                    style={{
                      font: "400 10px/1 'JetBrains Mono',monospace",
                      color: 'var(--fg3)',
                      textAlign: 'right',
                    }}
                  >
                    {row.label}
                  </div>
                  <div style={{ display: 'grid', gridTemplateColumns: 'repeat(24,14px)', gap: '3px' }}>
                    {row.cells.map((c, j) => (
                      <div
                        key={j}
                        onMouseEnter={c.on}
                        style={{
                          width: '14px',
                          height: '14px',
                          cursor: 'crosshair',
                          background: 'var(--red)',
                          opacity: c.o,
                          outline: c.ring,
                        }}
                      />
                    ))}
                  </div>
                </div>
              ))}
            </div>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginTop: '20px', marginLeft: '56px' }}>
              <span style={{ fontSize: '11px', color: 'var(--fg3)' }}>低</span>
              {heatLegend.map((g, i) => (
                <span key={i} style={{ width: '11px', height: '11px', background: 'var(--red)', opacity: g.o }} />
              ))}
              <span style={{ fontSize: '11px', color: 'var(--fg3)' }}>高</span>
            </div>
          </div>
        </div>

        <div
          data-r="heatside"
          style={{
            flex: 1,
            minWidth: '280px',
            paddingLeft: '44px',
            marginLeft: '44px',
            borderLeft: '1px solid var(--line)',
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'baseline', gap: '12px' }}>
            <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
              TIME-OF-DAY SPLIT
            </div>
            <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'baseline', gap: '5px' }}>
              <span
                style={{
                  fontSize: '19px',
                  fontWeight: 600,
                  letterSpacing: '-.03em',
                  fontVariantNumeric: 'tabular-nums',
                }}
              >
                {dayTotal}
              </span>
              <span style={{ fontSize: '12px', color: 'var(--fg3)' }}>kWh / 天</span>
            </div>
          </div>
          <div style={{ display: 'flex', height: '26px', width: '100%', marginTop: '16px' }}>
            {bandRows.map((b, i) => (
              <div key={i} title={b.name} style={{ height: '100%', background: b.bg, width: b.w }} />
            ))}
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', marginTop: '6px' }}>
            {bandRows.map((b, i) => (
              <div
                key={i}
                style={{
                  display: 'flex',
                  alignItems: 'baseline',
                  gap: '12px',
                  padding: '13px 0',
                  borderBottom: '1px solid var(--line2)',
                }}
              >
                <span style={{ width: '9px', height: '9px', flex: 'none', background: b.bg, transform: 'translateY(1px)' }} />
                <span style={{ fontSize: '13px', fontWeight: 500, color: b.fg }}>{b.name}</span>
                <span style={{ font: "400 10.5px/1 'JetBrains Mono',monospace", color: 'var(--fg3)' }}>{b.hours}</span>
                <span
                  style={{
                    marginLeft: 'auto',
                    font: "500 12.5px/1 'JetBrains Mono',monospace",
                    fontVariantNumeric: 'tabular-nums',
                  }}
                >
                  {b.pct}
                </span>
                <span
                  style={{
                    width: '74px',
                    textAlign: 'right',
                    fontSize: '12px',
                    color: 'var(--fg3)',
                    fontVariantNumeric: 'tabular-nums',
                  }}
                >
                  {b.kwh} {unitLabel}
                </span>
                <span
                  style={{
                    width: '74px',
                    textAlign: 'right',
                    fontSize: '12px',
                    color: 'var(--fg3)',
                    fontVariantNumeric: 'tabular-nums',
                  }}
                >
                  {b.alt}
                </span>
              </div>
            ))}
          </div>

          <div
            style={{
              font: "500 9.5px/1 'JetBrains Mono',monospace",
              letterSpacing: '.2em',
              color: 'var(--fg3)',
              marginTop: '26px',
            }}
          >
            HOTTEST HOURS
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', marginTop: '4px' }}>
            {topHours.map((h, i) => (
              <div
                key={i}
                style={{
                  display: 'flex',
                  alignItems: 'baseline',
                  gap: '14px',
                  padding: '11px 0',
                  borderBottom: '1px solid var(--line2)',
                }}
              >
                <span style={{ font: "500 10.5px/1 'JetBrains Mono',monospace", color: 'var(--red)' }}>{h.rank}</span>
                <span style={{ font: "400 12.5px/1 'JetBrains Mono',monospace" }}>{h.hour}</span>
                <span
                  style={{
                    marginLeft: 'auto',
                    fontSize: '12px',
                    color: 'var(--fg3)',
                    fontVariantNumeric: 'tabular-nums',
                  }}
                >
                  {h.pct}
                </span>
                <span
                  style={{
                    width: '80px',
                    textAlign: 'right',
                    font: "500 12.5px/1 'JetBrains Mono',monospace",
                    fontVariantNumeric: 'tabular-nums',
                  }}
                >
                  {h.kwh} {unitLabel}
                </span>
              </div>
            ))}
          </div>

          <div style={{ marginTop: 'auto', paddingTop: '22px', display: 'flex', gap: '11px', alignItems: 'flex-start' }}>
            <span style={{ width: '5px', height: '5px', background: 'var(--red)', flex: 'none', marginTop: '7px' }} />
            <span style={{ fontSize: '12.5px', color: 'var(--fg2)', lineHeight: 1.65, textWrap: 'pretty' }}>
              {bandInsight}
            </span>
          </div>
        </div>
      </div>
      {IS_LIVE && (
        <LiveNote
          mt="26px"
          text={
            lhm.data
              ? lhm.data.message + '（' + lhm.data.reason_code + '）'
              : '上游无法提供小时级用电数据，分时热力图与时段画像暂不可用'
          }
        />
      )}
    </section>
  )
}
