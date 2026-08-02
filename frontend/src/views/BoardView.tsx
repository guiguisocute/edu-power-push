/* 排行榜视图。对齐原型 isBoard 区块。
   数据来自 lib/mock.ts 与 lib/privacy.ts。
   内联样式对应原型。style-hover 用 .hv-* 类。 */

import { useEffect } from 'react'
import { useStore } from '../lib/store'
import { makeFmt, themeColors, seg } from '../lib/format'
import {
  boardData,
  isBound,
  myBoard,
  rnd,
  DORMS,
  type BoardFilter,
  type BoardKind,
  type BPeriod,
  type BoardRowData,
} from '../lib/mock'
import { privacyInfo } from '../lib/privacy'
import { buildingParam, floorParam, useScopeOptions } from '../lib/scopes'
import { SegGroup, SegBtn } from '../components/ui'
import { InlineNote } from '../components/Loading'
import { IS_LIVE } from '../api/mode'
import { availNote, useLiveRankings } from '../api/live'
import LiveNote from '../components/LiveNote'
import { fallbackRankingPeriods, formatRankingMoment, formatRankingRange } from '../lib/rankingPeriods'

export default function BoardView() {
  const { s, set, say, startBind } = useStore()
  const dark = s.theme === 'dark'
  const { RED, OK, FG3 } = themeColors(dark)
  // 电价由运维面板下发。切「元」时折算价须与概览、用电分析同源。
  const elecRate = parseFloat(s.features?.display?.electricityRate || '') || 0.62
  const { U, cv } = makeFmt(s.unit, elecRate)
  /* 用户文案用中文单位。U 为 kWh/元 简写，表头英文继续用。 */
  const unitZh = s.unit === 'rmb' ? '元' : '千瓦时'

  // ---- 榜单数据。live 走 campus/rankings。房间由后端提供，电表号不下发。
  const F: BoardFilter = { board: s.board, bPeriod: s.bPeriod, bBldg: s.bBldg, bFloor: s.bFloor }
  const MODE_MAP = { top: 'usage', save: 'saving', surge: 'surge', drop: 'drop' } as const
  const lrk = useLiveRankings(s.bPeriod, MODE_MAP[s.board], buildingParam(s.bBldg), floorParam(s.bFloor))
  const scopeOpts = useScopeOptions(s.bBldg)
  /* 业务范围收紧后，本地仍记着已下线楼栋。
     禁止用 option 外的旧值查询并渲染空白页。 */
  useEffect(() => {
    if (!IS_LIVE || scopeOpts.loading || s.bBldg === 'all') return
    if (!scopeOpts.buildings.some((o) => o.v === s.bBldg)) {
      set({ bBldg: 'all', bFloor: 'all', bMe: false })
    }
  }, [scopeOpts.loading, scopeOpts.buildings, s.bBldg, set])
  /* live 身份文案由后端按脱敏偏好渲染为 label。
     明文楼栋房间仅 is_self 时下发。榜单匿名可读。
     脱敏必须在服务端。前端仅显示收到的字段。 */
  const rows: BoardRowData[] = IS_LIVE
    ? (lrk.data?.items ?? []).map((e) => ({
        rank: String(e.rank).padStart(2, '0'),
        meter: e.name || '未注册用户',
        place: e.label || '—',
        kwh: parseFloat(e.value_kwh),
        delta: e.change_ratio != null ? e.change_ratio * 100 : NaN,
      }))
    : boardData(F)
  /* 我的名次来自 rankings.self。后端全量计算，与 Top N 截断无关。 */
  const liveSelf = IS_LIVE ? (lrk.data?.self ?? null) : null
  const liveSelfRank = liveSelf?.rank ?? 0
  const rmax = Math.max(...rows.map((r) => r.kwh), 0.0001)
  const isSurge = s.board === 'surge'
  const isDay = s.bPeriod === 'day'
  // live 固定请求 Top 50。加载中空数组不是 Top 0，避免首屏口径闪烁。
  const topN = IS_LIVE ? 50 : rows.length
  const dg = (v: number) => cv(v).toFixed(isDay ? 2 : 1)

  // ---- 我的位置。live 用 rankings.self。mock 用 myBoard 本地推算。
  /* 未登录或未绑表都拿不到我的位置。文案不同。
     live 走后端 self。mock 的 myBoard 一律返回 null。
     me! 断言仅当 meIn 为真时可达。 */
  const bound = isBound(s.user)
  const me = IS_LIVE ? null : myBoard(F, s.user)
  const meIn = !!(me && !me.out)
  const { meLabel } = privacyInfo(s.user, s.pf, s.pmask)
  const meRankNo = meIn ? me!.rank! : 0

  const mkRow = (r: BoardRowData, i: number, isMe: boolean) => {
    const unreg = r.meter === '未注册用户'
    return {
      rank: r.rank,
      meter: r.meter,
      place: r.place,
      kwh: dg(r.kwh),
      w: (Math.min(1, r.kwh / rmax) * 100).toFixed(1) + '%',
      rankColor: isMe ? RED : i < 3 ? RED : FG3,
      nameWeight: isMe ? '600' : '400',
      nameColor: unreg ? FG3 : isMe ? 'var(--fg)' : 'var(--fg2)',
      rowBg: isMe ? 'color-mix(in srgb, var(--red) 7%, transparent)' : 'transparent',
      barColor: isMe || i < 3 ? RED : 'color-mix(in srgb, var(--fg) 30%, transparent)',
      delta: isNaN(r.delta) ? '—' : (r.delta > 0 ? '+' : '') + r.delta.toFixed(1) + '%',
      deltaColor: isNaN(r.delta) ? FG3 : isSurge ? RED : r.delta > 0 ? RED : OK,
    }
  }
  type Row = ReturnType<typeof mkRow>

  // 高亮「我」那一行。live 用 is_self。mock 用本地名次。
  const boardRows = rows.map((r, i) =>
    mkRow(r, i, IS_LIVE ? (lrk.data?.items ?? [])[i]?.is_self === true : meIn && me!.rank! === i + 1),
  )
  /* 附近三行。live 用后端邻居（各自脱敏）。mock 合成假数据。
     已在 Top N 内时不再单独列出。 */
  const liveNearShown = !!(s.bMe && liveSelf && !liveSelf.in_list && liveSelf.neighbors?.length)
  const liveNearRows: Row[] = liveNearShown
    ? liveSelf!.neighbors.map((e) =>
        mkRow(
          {
            rank: String(e.rank).padStart(2, '0'),
            meter: e.name || (e.is_self ? s.user?.name || '匿名用户' : '未注册用户'),
            place: e.label || '—',
            kwh: parseFloat(e.value_kwh),
            delta: e.change_ratio != null ? e.change_ratio * 100 : NaN,
          },
          99,
          e.is_self,
        ),
      )
    : []
  const mockNearShown = !IS_LIVE && !!(s.bMe && meIn && me!.rank! > topN)
  const nearShown = IS_LIVE ? liveNearShown : mockNearShown
  const nearRows: Row[] = IS_LIVE
    ? liveNearRows
    : mockNearShown
    ? [-1, 0, 1]
        .map((off): Row | null => {
          const rk = me!.rank! + off
          if (rk < 1 || rk > me!.total!) return null
          const dir = s.board === 'save' ? -1 : 1
          const jitter =
            off === 0 ? 0 : (dir * (off < 0 ? 1 : -1) * (0.6 + rnd(rk, s.board.length + 9) * 1.8)) / (isDay ? 30 : 1)
          const kwh = Math.max(0.05, me!.kwh! + jitter)
          const isSelf = off === 0
          return mkRow(
            {
              rank: String(rk).padStart(2, '0'),
              meter: isSelf ? s.user?.name || '匿名用户' : rk % 3 === 0 ? '未注册用户' : '同学' + rk,
              place: isSelf
                ? s.user?.place || ''
                : DORMS[rk % DORMS.length] + ' · ' + (1 + (rk % 6)) + ' 楼 · **',
              kwh: kwh,
              delta: isSelf ? me!.delta! : Math.round((rnd(rk, 17) * 30 - 12) * 10) / 10,
            },
            99,
            isSelf,
          )
        })
        .filter((x): x is Row => x !== null)
    : []

  // ---- 标题与分段
  const perLbl = { day: '日', week: '周', month: '月' }[s.bPeriod]
  const boardTitles = {
    top: perLbl + '用电量 Top ' + topN,
    save: '省电榜 Top ' + topN,
    surge: '环比涨幅 Top ' + topN,
    drop: '环比降幅 Top ' + topN,
  }
  const boardTitle = boardTitles[s.board]

  /* 日期必须来自同一份榜单响应。禁止用浏览器今天反推缓存范围。
     mock 与加载中用占位边界。live 数据到达后换服务端边界。 */
  const rankingRefreshTime = s.features.display.rankingRefreshTime || '09:00'
  const fallbackPeriods = fallbackRankingPeriods(
    s.bPeriod,
    IS_LIVE ? new Date() : new Date(2026, 6, 25),
    rankingRefreshTime,
  )
  const currentPeriod = lrk.data?.current_period ?? (IS_LIVE ? null : fallbackPeriods.current)
  const previousPeriod = lrk.data?.previous_period ?? (IS_LIVE ? null : fallbackPeriods.previous)
  const currentPeriodLabel = currentPeriod ? formatRankingRange(currentPeriod) : '—'
  const previousPeriodLabel = previousPeriod ? formatRankingRange(previousPeriod) : '—'
  const updatedAt = lrk.data?.updated_at ?? (IS_LIVE ? null : fallbackPeriods.updatedAt)
  const nextUpdateAt = lrk.data?.next_update_at ?? (IS_LIVE ? null : fallbackPeriods.nextUpdateAt)
  const refreshLabel = updatedAt && nextUpdateAt
    ? `更新于 ${formatRankingMoment(updatedAt)} · 下次 ${formatRankingMoment(nextUpdateAt)}`
    : '更新时间 —'
  const boardDefs: [BoardKind, string][] = [
    ['top', '用电量'],
    ['save', '省电榜'],
    ['surge', '涨幅'],
    ['drop', '降幅'],
  ]
  /* 与数据看板一致：日→周→月。筛选在左，周期在右。 */
  const bPeriodDefs: [BPeriod, string][] = [
    ['day', '日'],
    ['week', '周'],
    ['month', '月'],
  ]

  const boardSchedule = {
    month: `每月 1 日 ${rankingRefreshTime}`,
    week: `每周一 ${rankingRefreshTime}`,
    day: `每日 ${rankingRefreshTime}`,
  }[s.bPeriod]
  const boardRule = `半静态榜单 · ${boardSchedule} 更新` + (s.board === 'save' ? ' · 按非空房日归一化' : '')
  /* 楼栋与楼层选项。live 来自 campus/scopes。mock 来自原型常量。 */
  const bBldgOpts = scopeOpts.buildings
  const bFloorOpts = scopeOpts.floors
  const boardUnitEn =
    (s.board === 'save' ? 'OCCUPIED-DAY NORMALIZED · ' : '') +
    { day: 'DAILY', week: 'WEEKLY', month: 'MONTHLY' }[s.bPeriod] +
    ' / ' +
    U

  // ---- 我的位置卡片
  /* 名次含义随口径翻转。用电量榜第 1 用得最多。省电榜第 1 用得最少。
     禁止对两种榜单都说「超过全校 x%」。 */
  const selfAhead = liveSelf ? Math.max(0, Math.round((1 - liveSelf.percentile) * 100)) : 0
  const selfTopPct = Math.max(1, Math.round((liveSelf?.percentile ?? 0) * 100))
  const selfStanding = {
    top: '用电超过全校 ' + selfAhead + '% 的宿舍',
    save: '比全校 ' + selfAhead + '% 的宿舍更省电',
    surge: '环比涨幅排在前 ' + selfTopPct + '%',
    drop: '环比降幅排在前 ' + selfTopPct + '%',
  }[s.board]
  const meHasVal = IS_LIVE ? !!liveSelf : meIn
  const meRank = IS_LIVE
    ? liveSelfRank > 0
      ? '#' + liveSelfRank
      : '—'
    : bound
      ? meIn
        ? '#' + meRankNo
        : '—'
      : '—'
  const meRankColor = (IS_LIVE ? !!liveSelf : meIn) ? 'var(--fg)' : FG3
  /* 名称与位置用后端按隐私偏好渲染的字段。与公开行同一套。
     禁止用 user.place 或电表号明文。 */
  const mePlace = IS_LIVE
    ? !s.user
      ? '登录后查看排名'
      : !bound
        ? '绑定电表后查看排名'
        : liveSelf
          ? [liveSelf.name, liveSelf.label].filter(Boolean).join(' · ') || liveSelf.name || '匿名用户'
          : '当前筛选范围没有你的电表'
    : !s.user
      ? '登录后查看排名'
      : !bound
        ? '绑定电表后查看排名'
        : meIn
          ? meLabel
          : '当前筛选范围没有你的电表'
  const meNote = IS_LIVE
    ? !s.user
      ? '登录并绑定电表后，可与全校宿舍对比用电量。'
      : !bound
        ? '绑定宿舍电表后可查看全校名次。'
        : liveSelf
          ? selfStanding + ' · 共 ' + liveSelf.total.toLocaleString() + ' 户 · 展示与隐私设置一致'
          : '当前筛选范围没有你的电表。请更换楼栋或楼层。'
    : !s.user
      ? '绑定电表后可与全校宿舍对比用电量。'
      : !bound
        ? '绑定宿舍电表后可查看全校名次与环比。'
        : meIn
          ? '超过全校 ' + me!.pct! + '% 的宿舍 · 共 ' + me!.total!.toLocaleString() + ' 户 · 展示与隐私设置一致'
          : me!.reason === 'floor'
            ? '切换楼层筛选可查看位置'
            : '切换楼栋筛选可查看位置'
  // live 用量与环比来自 self。不依赖是否在当前页 items 中。
  const liveSelfDelta = liveSelf?.change_ratio != null ? liveSelf.change_ratio * 100 : NaN
  const meKwh = IS_LIVE
    ? liveSelf
      ? dg(parseFloat(liveSelf.value_kwh)) + ' ' + unitZh
      : ''
    : meIn
      ? dg(me!.kwh!) + ' ' + unitZh
      : ''
  const meDelta = IS_LIVE
    ? liveSelf && !isNaN(liveSelfDelta)
      ? (liveSelfDelta > 0 ? '+' : '') + liveSelfDelta.toFixed(1) + '% 环比'
      : ''
    : meIn
      ? (me!.delta! > 0 ? '+' : '') + me!.delta!.toFixed(1) + '% 环比'
      : ''
  const meDeltaColor = IS_LIVE
    ? liveSelf && liveSelfDelta > 0
      ? RED
      : liveSelf
        ? OK
        : FG3
    : meIn
      ? me!.delta! > 0
        ? RED
        : OK
      : FG3
  const meCta = IS_LIVE
    ? !s.user
      ? '去登录'
      : !bound
        ? '绑定电表'
        : !liveSelf
          ? '重置筛选'
          : liveSelf.in_list
            ? '已在榜内'
            : s.bMe
              ? '收起'
              : '查看我的位置'
    : !s.user
      ? '去登录'
      : !bound
        ? '绑定电表'
        : !meIn
          ? '重置筛选'
          : me!.rank! <= topN
            ? '已在榜内'
            : s.bMe
              ? '收起'
              : '查看我的位置'
  const meAction = () => {
    if (IS_LIVE) {
      if (!s.user) return set({ authView: 'login' })
      if (!bound) return startBind()
      if (!liveSelf) return set({ bBldg: 'all', bFloor: 'all' })
      if (liveSelf.in_list) return say('你已在 Top ' + topN + ' 榜单内')
      return set({ bMe: !s.bMe })
    }
    if (!s.user) return set({ authView: 'login' })
    if (!bound) return startBind()
    if (me!.out) return set({ bBldg: 'all', bFloor: 'all' })
    if (me!.rank! <= topN) return say('你已在 Top ' + topN + ' 榜单内')
    set({ bMe: !s.bMe })
  }
  const nearAnchorRank = IS_LIVE ? (liveSelf?.rank ?? 0) : meIn ? me!.rank! : 0
  const nearGapNote = nearShown ? '省略第 ' + (topN + 1) + ' – ' + (nearAnchorRank - 2) + ' 位' : ''

  // ---- 单行渲染。榜单行与近位行共用结构。手机为两行紧凑布局。
  const rowNode = (r: Row, key: number) => (
    <div
      key={key}
      className="board-row"
      style={{
        display: 'flex',
        alignItems: 'center',
        gap: '30px',
        padding: '14px 0',
        borderBottom: '1px solid var(--line2)',
        background: r.rowBg,
      }}
    >
      <span
        className="board-row-rank"
        style={{ width: '30px', flex: 'none', font: "500 13px/1 'JetBrains Mono',monospace", color: r.rankColor }}
      >
        {r.rank}
      </span>
      <span
        className="board-row-name"
        style={{
          width: '132px',
          flex: 'none',
          display: 'flex',
          justifyContent: 'center',
          fontSize: '12.5px',
          fontWeight: r.nameWeight as '400' | '600',
          color: r.nameColor,
          textAlign: 'center',
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {r.meter}
      </span>
      <span
        className="board-row-place"
        style={{
          width: '240px',
          flex: 'none',
          fontSize: '12.5px',
          fontWeight: r.nameWeight,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
        }}
      >
        {r.place}
      </span>
      <span
        className="board-row-bar"
        style={{ flex: 1, minWidth: 0, display: 'flex', alignItems: 'center', gap: '12px' }}
      >
        <span style={{ flex: 1, height: '4px', background: 'var(--line2)' }}>
          <span
            style={{
              display: 'block',
              height: '100%',
              background: r.barColor,
              width: r.w,
              transition: 'width .35s cubic-bezier(.22,.61,.36,1)',
            }}
          />
        </span>
        <span
          className="board-row-val"
          style={{
            font: "500 12px/1 'JetBrains Mono',monospace",
            fontVariantNumeric: 'tabular-nums',
            minWidth: '72px',
            textAlign: 'right',
            whiteSpace: 'nowrap',
          }}
        >
          {r.kwh}
          <span style={{ fontWeight: 400, color: 'var(--fg3)', marginLeft: '3px' }}>{unitZh}</span>
        </span>
      </span>
      <span
        className="board-row-delta"
        style={{ width: '72px', flex: 'none', textAlign: 'right', fontSize: '12.5px', fontWeight: 500, color: r.deltaColor }}
      >
        {r.delta}
      </span>
    </div>
  )

  return (
    <div data-screen-label="排行榜" style={{ animation: 'rise .28s ease both' }}>
      <section style={{ padding: '52px 0 0' }}>
        <div style={{ display: 'flex', alignItems: 'flex-end', gap: '30px', flexWrap: 'wrap' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: '9px' }}>
            <div style={{ font: "500 9.5px/1 'JetBrains Mono',monospace", letterSpacing: '.2em', color: 'var(--fg3)' }}>
              LEADERBOARD · {s.features.display.campusName}
            </div>
            <div style={{ fontSize: '34px', fontWeight: 600, letterSpacing: '-.04em' }}>{boardTitle}</div>
          </div>
          <div
            data-r="hdrside"
            style={{ marginLeft: 'auto', display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: '7px' }}
          >
            <span style={{ font: "500 9px/1 'JetBrains Mono',monospace", letterSpacing: '.18em', color: 'var(--fg3)' }}>
              STATISTICAL PERIOD
            </span>
            <span style={{ font: "500 12px/1 'JetBrains Mono',monospace", fontVariantNumeric: 'tabular-nums' }}>
              {lrk.loading && IS_LIVE ? '正在确认统计周期…' : currentPeriodLabel}
            </span>
            <span style={{ fontSize: '11px', color: 'var(--fg3)' }}>环比基准 {previousPeriodLabel}</span>
            <span style={{ fontSize: '11px', color: 'var(--fg3)' }}>{refreshLabel}</span>
          </div>
        </div>

        {/* 左：楼栋与楼层筛选、日/周/月。右：用电量/省电榜/涨幅/降幅。 */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: '14px',
            flexWrap: 'wrap',
            marginTop: '30px',
            padding: '15px 0',
            borderTop: '1px solid var(--line)',
            borderBottom: '1px solid var(--line)',
          }}
        >
          <span style={{ font: "500 9px/1 'JetBrains Mono',monospace", letterSpacing: '.18em', color: 'var(--fg3)' }}>
            FILTER
          </span>
          <select
            value={s.bBldg}
            onChange={(e) => set({ bBldg: e.target.value, bFloor: 'all', bMe: false })}
            style={{
              padding: '8px 12px',
              border: '1px solid var(--line)',
              background: 'var(--bg)',
              color: 'var(--fg)',
              font: "400 12.5px/1 'Instrument Sans','Noto Sans SC',sans-serif",
              cursor: 'pointer',
            }}
          >
            {bBldgOpts.map((o) => (
              <option key={o.v} value={o.v}>
                {o.l}
              </option>
            ))}
          </select>
          {s.bBldg !== 'all' && (
            <select
              value={s.bFloor}
              onChange={(e) => set({ bFloor: e.target.value, bMe: false })}
              style={{
                padding: '8px 12px',
                border: '1px solid var(--line)',
                background: 'var(--bg)',
                color: 'var(--fg)',
                font: "400 12.5px/1 'Instrument Sans','Noto Sans SC',sans-serif",
                cursor: 'pointer',
              }}
            >
              {bFloorOpts.map((o) => (
                <option key={o.v} value={o.v}>
                  {o.l}
                </option>
              ))}
            </select>
          )}
          <SegGroup>
            {bPeriodDefs.map((p) => (
              <SegBtn
                key={p[0]}
                seg={seg(s.bPeriod === p[0], p[1], () => set({ bPeriod: p[0] }))}
                pad="5px 14px"
                fs="12px"
              />
            ))}
          </SegGroup>
          <SegGroup data-r="hdrside" style={{ marginLeft: 'auto', flexWrap: 'wrap' }}>
            {boardDefs.map((b) => (
              <SegBtn
                key={b[0]}
                seg={seg(s.board === b[0], b[1], () => set({ board: b[0], bMe: false }))}
                pad="5px 14px"
                fs="12px"
              />
            ))}
          </SegGroup>
        </div>

        <div className="board-me-wrap" style={{ marginTop: '22px' }}>
          <div
            className="board-me"
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: '26px',
              padding: '17px 20px',
              background: 'var(--sub)',
              border: '1px solid var(--line)',
            }}
          >
            <div
              className="board-me-rank"
              style={{ display: 'flex', flexDirection: 'column', gap: '7px', width: '104px', flex: 'none' }}
            >
              <span
                style={{ font: "500 9px/1 'JetBrains Mono',monospace", letterSpacing: '.18em', color: 'var(--fg3)' }}
              >
                MY RANK
              </span>
              <span
                style={{
                  font: "600 24px/1 'JetBrains Mono',monospace",
                  letterSpacing: '-.03em',
                  color: meRankColor,
                }}
              >
                {meRank}
              </span>
            </div>
            <div
              className="board-me-meta"
              style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column', gap: '7px' }}
            >
              <span
                style={{
                  fontSize: '13px',
                  fontWeight: 500,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}
              >
                {mePlace}
              </span>
              <span style={{ fontSize: '11.5px', color: 'var(--fg3)', textWrap: 'pretty' }}>{meNote}</span>
            </div>
            {meHasVal && (
              <div
                className="board-me-val"
                style={{ display: 'flex', flexDirection: 'column', gap: '7px', alignItems: 'flex-end', flex: 'none' }}
              >
                <span
                  style={{
                    font: "500 15px/1 'JetBrains Mono',monospace",
                    fontVariantNumeric: 'tabular-nums',
                    whiteSpace: 'nowrap',
                  }}
                >
                  {meKwh}
                </span>
                <span style={{ fontSize: '11.5px', fontWeight: 500, color: meDeltaColor }}>{meDelta}</span>
              </div>
            )}
            {meCta && (
              <button
                className="hv-line-fg board-me-cta"
                onClick={meAction}
                style={{
                  flex: 'none',
                  margin: 0,
                  padding: '9px 18px',
                  border: '1px solid var(--line)',
                  borderRadius: '999px',
                  font: 'inherit',
                  fontSize: '12.5px',
                  fontWeight: 500,
                  cursor: 'pointer',
                  background: 'none',
                  color: 'var(--fg)',
                }}
              >
                {meCta}
              </button>
            )}
          </div>
        </div>

        <div className="board-list-wrap" style={{ marginTop: '26px' }}>
          <div className="board-list">
            <div
              className="board-row board-head"
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: '30px',
                padding: '0 0 11px',
                borderBottom: '1px solid var(--line)',
                font: "500 9px/1 'JetBrains Mono',monospace",
                letterSpacing: '.18em',
                color: 'var(--fg3)',
              }}
            >
              <span className="board-row-rank" style={{ width: '30px', flex: 'none' }}>
                NO.
              </span>
              <span
                className="board-row-name"
                style={{
                  width: '132px',
                  flex: 'none',
                  display: 'flex',
                  justifyContent: 'center',
                  textAlign: 'center',
                }}
              >
                NAME
              </span>
              <span className="board-row-place" style={{ width: '240px', flex: 'none' }}>
                LOCATION
              </span>
              <span className="board-row-bar" style={{ flex: 1, minWidth: 0 }}>
                {boardUnitEn}
              </span>
              <span className="board-row-delta" style={{ width: '72px', flex: 'none', textAlign: 'right' }}>
                MOM
              </span>
            </div>
            <div
              key={s.board + '-' + s.bPeriod + '-' + s.bBldg + '-' + s.bFloor + '-' + s.unit}
              className="chart-anim"
              style={{ display: 'flex', flexDirection: 'column' }}
            >
              {IS_LIVE && rows.length === 0 && (
                <div
                  style={{
                    padding: '26px 0',
                    borderBottom: '1px solid var(--line2)',
                    fontSize: '12.5px',
                    color: 'var(--fg3)',
                  }}
                >
                  {lrk.loading ? <InlineNote text="榜单读取中" /> : '暂无榜单数据。需要至少一个完整统计周期的扫描差分。'}
                </div>
              )}
              {boardRows.map((r, i) => rowNode(r, i))}
            </div>
            {nearShown && (
              <div style={{ display: 'flex', flexDirection: 'column' }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: '14px', padding: '13px 0' }}>
                  <span
                    style={{
                      font: "400 12px/1 'JetBrains Mono',monospace",
                      color: 'var(--fg3)',
                      letterSpacing: '.3em',
                    }}
                  >
                    ···
                  </span>
                  <span style={{ fontSize: '11.5px', color: 'var(--fg3)' }}>{nearGapNote}</span>
                </div>
                {nearRows.map((r, i) => rowNode(r, i))}
              </div>
            )}
          </div>
        </div>
        {IS_LIVE && (
          <LiveNote
            text={
              availNote(lrk.data?.availability, lrk.data?.quality, lrk.error) +
              (lrk.data ? ` · 已排除 ${lrk.data.excluded_count} 户（周期覆盖不足或异常未确认）` : '')
            }
          />
        )}
        <div style={{ marginTop: '36px', fontSize: '11.5px', color: 'var(--fg3)', textWrap: 'pretty' }}>
          {IS_LIVE
            ? '房间信息由后端脱敏下发 · 数据来源：后勤能源管理平台 · ' + boardRule
            : '电表号后三位与房间号已脱敏 · 数据来源：后勤能源管理平台 · ' + boardRule}
        </div>
      </section>
    </div>
  )
}
