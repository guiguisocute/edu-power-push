/* 三类上游扫描器控制台：余额、官方日明细、月账单。
   三者可并行。HTTP 请求共享总 QPS 与并发闸门。
   同类任务互斥。禁止重复轮次同时写同一类数据。 */

import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import {
  adminApi,
  type BillRun,
  type DailyDetailRun,
  type ScanRun,
  type ScannerSettingsView,
} from './api'
import { Btn, Dot, Empty, Field, Row, Section, fieldStyle, fmtAgo, fmtTime, mono } from './ui'
import SchoolView from './SchoolView'

export const STATUS_TONE: Record<string, 'ok' | 'warn' | 'bad'> = {
  pending: 'warn',
  running: 'warn',
  completed: 'ok',
  completed_with_errors: 'warn',
  interrupted: 'bad',
  canceled: 'bad',
  failed: 'bad',
}

export const STATUS_LABEL: Record<string, string> = {
  pending: '排队中',
  running: '进行中',
  completed: '已完成',
  completed_with_errors: '完成·有异常',
  interrupted: '被中断',
  canceled: '已终止',
  failed: '失败',
}

type TimedRun = Pick<ScanRun, 'status' | 'started_at' | 'finished_at' | 'heartbeat_at'>

/**
 * 轮次耗时从创建时间算起。进行中用当前时间。结束后用 finished_at 冻结。
 * 缺 finished_at 时用最后心跳，给出不再增长的值。
 */
export function formatRunDuration(run: TimedRun, nowMs = Date.now()): string {
  const startedMs = new Date(run.started_at).getTime()
  if (!Number.isFinite(startedMs)) return '—'

  const isActive = run.status === 'pending' || run.status === 'running'
  const endRaw = run.finished_at || (!isActive ? run.heartbeat_at : null)
  const endedMs = endRaw ? new Date(endRaw).getTime() : isActive ? nowMs : NaN
  if (!Number.isFinite(endedMs)) return '—'

  const totalSeconds = Math.max(0, Math.floor((endedMs - startedMs) / 1000))
  const seconds = totalSeconds % 60
  const totalMinutes = Math.floor(totalSeconds / 60)
  const minutes = totalMinutes % 60
  const totalHours = Math.floor(totalMinutes / 60)
  const hours = totalHours % 24
  const days = Math.floor(totalHours / 24)
  const pad = (value: number) => String(value).padStart(2, '0')

  if (days > 0) return `${days}天 ${pad(hours)}:${pad(minutes)}:${pad(seconds)}`
  if (totalHours > 0) return `${totalHours}:${pad(minutes)}:${pad(seconds)}`
  return `${pad(totalMinutes)}:${pad(seconds)}`
}

export function summarize(run: ScanRun): string {
  const c = run.counters
  if (run.status === 'pending') return '等待 worker 接手'
  const n = (v: number | undefined) => v ?? 0
  if (run.status === 'running') return `进度 ${n(c?.processed_total)} / ${n(c?.eligible_total)}`
  const bad = n(c?.error_total) + n(c?.parse_error_total)
  return `有效 ${n(c?.valid_total)} · 陈旧 ${n(c?.stale_total)} · 异常 ${bad}`
}

type RunKind = 'balance' | 'detail' | 'bill'
type AnyRun = ScanRun | DailyDetailRun | BillRun

function runSummary(kind: RunKind, run: AnyRun): string {
  if (kind === 'balance') return summarize(run as ScanRun)
  const c = run.counters
  if (run.status === 'pending') return '等待 worker 接手'
  if (run.status === 'running') return `进度 ${c.processed_total ?? 0} / ${c.eligible_total ?? 0}`
  if (kind === 'bill') {
    const b = (run as BillRun).counters
    return `有效 ${b.valid_total ?? 0} · 无数据 ${b.no_data_total ?? 0} · 异常 ${(b.partial_total ?? 0) + (b.error_total ?? 0)}`
  }
  const d = (run as DailyDetailRun).counters
  return `有效 ${d.valid_total ?? 0} · 无数据 ${d.no_data_total ?? 0} · 异常 ${(d.partial_total ?? 0) + (d.error_total ?? 0)}`
}

function RunHistory({
  kind,
  title,
  desc,
  runs,
  active,
  busy,
  canWrite,
  onTrigger,
  onRetry,
  onCancel,
  extraAction,
  nowMs,
}: {
  kind: RunKind
  title: string
  desc: string
  runs: AnyRun[]
  active: boolean
  busy: boolean
  canWrite: boolean
  onTrigger: () => void
  onRetry: (id: string) => void
  onCancel: (id: string) => void
  extraAction?: { label: string; title: string; onClick: () => void }
  nowMs: number
}) {
  return (
    <Section
      en={`SCANNER · ${kind.toUpperCase()}`}
      title={title}
      desc={desc}
      actions={
        <>
          {extraAction && (
            <Btn onClick={extraAction.onClick} disabled={!canWrite || busy || active} title={extraAction.title}>
              {extraAction.label}
            </Btn>
          )}
          <Btn primary onClick={onTrigger} disabled={!canWrite || busy || active}>
            {active ? '同类任务进行中' : '立即全量扫描'}
          </Btn>
        </>
      }
    >
      {runs.length === 0 ? (
        <Empty title="还没有运行记录" desc="触发或定时任务后显示进度与计数。" />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column' }}>
          {runs.map((run) => {
            // interrupted 由 worker 断点恢复。禁止另建重复 retry 子任务。
            const retryable = ['completed_with_errors', 'failed'].includes(run.status)
            // 未收尾任务可终止。interrupted 会被 worker 续跑，必须有停止入口。
            const cancelable = ['pending', 'running', 'interrupted'].includes(run.status)
            return (
              <div
                key={run.id}
                style={{
                  display: 'grid',
                  gridTemplateColumns: '112px minmax(150px,1fr) 112px 118px minmax(190px,260px) 70px',
                  alignItems: 'center',
                  gap: '16px',
                  padding: '13px 0',
                  borderBottom: '1px solid var(--line2)',
                  fontSize: '12.5px',
                }}
              >
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: '8px', minWidth: 0 }}>
                  <Dot tone={STATUS_TONE[run.status] || 'warn'} />
                  <span title={run.status} style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                    {STATUS_LABEL[run.status] || run.status}
                  </span>
                </span>
                <span style={{ color: 'var(--fg2)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {fmtTime(run.started_at)}
                  <span style={{ color: 'var(--fg3)' }}> · {run.trigger}</span>
                </span>
                <span style={{ textAlign: 'right', color: 'var(--fg3)' }}>{run.qps} QPS / {run.concurrency}</span>
                <span
                  title="自创建起计时。进行中实时更新。"
                  style={{ textAlign: 'right', color: 'var(--fg2)', fontVariantNumeric: 'tabular-nums', whiteSpace: 'nowrap' }}
                >
                  {run.status === 'pending' ? '等待 ' : run.status === 'running' ? '已用 ' : '耗时 '}
                  {formatRunDuration(run, nowMs)}
                </span>
                <span style={{ textAlign: 'right', color: 'var(--fg3)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                  {runSummary(kind, run)}
                </span>
                <span style={{ textAlign: 'right' }}>
                  {cancelable && (
                    <Btn onClick={() => onCancel(run.id)} disabled={!canWrite || busy} title="终止本任务。在途电表跑完当前块。">
                      终止
                    </Btn>
                  )}
                  {retryable && <Btn onClick={() => onRetry(run.id)} disabled={!canWrite || busy || active}>重试</Btn>}
                </span>
              </div>
            )
          })}
        </div>
      )}
    </Section>
  )
}

export default function ScannerView({
  canWrite,
  onError,
  onToast,
}: {
  canWrite: boolean
  onError: (msg: string) => void
  onToast: (msg: string) => void
}) {
  const [settings, setSettings] = useState<ScannerSettingsView | null>(null)
  const [draft, setDraft] = useState<ScannerSettingsView['settings'] | null>(null)
  const [scanRuns, setScanRuns] = useState<ScanRun[]>([])
  const [detailRuns, setDetailRuns] = useState<DailyDetailRun[]>([])
  const [billRuns, setBillRuns] = useState<BillRun[]>([])
  const [busy, setBusy] = useState(false)
  const [tick, setTick] = useState(0)
  const [nowMs, setNowMs] = useState(() => Date.now())

  useEffect(() => {
    let alive = true
    Promise.all([
      adminApi.scannerSettings(),
      adminApi.scanRuns(6),
      adminApi.dailyDetailRuns(6),
      adminApi.billRuns(6),
    ])
      .then(([s, scans, details, bills]) => {
        if (!alive) return
        setSettings(s)
        setDraft(structuredClone(s.settings))
        setScanRuns(Array.isArray(scans?.items) ? scans.items : [])
        setDetailRuns(Array.isArray(details?.items) ? details.items : [])
        setBillRuns(Array.isArray(bills?.items) ? bills.items : [])
      })
      .catch((e) => alive && onError(String(e instanceof Error ? e.message : e)))
    return () => { alive = false }
  }, [tick])

  const allRuns: AnyRun[] = [...scanRuns, ...detailRuns, ...billRuns]
  const hasActive = allRuns.some((r) => r.status === 'running' || r.status === 'pending')
  const balanceActive = scanRuns.some((r) => r.status === 'running' || r.status === 'pending')
  const detailActive = detailRuns.some((r) => r.status === 'running' || r.status === 'pending')
  const billActive = billRuns.some((r) => r.status === 'running' || r.status === 'pending')
  const activeKinds = [balanceActive, detailActive, billActive].filter(Boolean).length
  useEffect(() => {
    const timer = setInterval(() => setTick((n) => n + 1), hasActive ? 5_000 : 30_000)
    return () => clearInterval(timer)
  }, [hasActive])

  useEffect(() => {
    setNowMs(Date.now())
    if (!hasActive) return
    const timer = setInterval(() => setNowMs(Date.now()), 1_000)
    return () => clearInterval(timer)
  }, [hasActive])

  const act = async (work: () => Promise<unknown>, success: string) => {
    setBusy(true)
    try {
      await work()
      onToast(success)
      setTick((n) => n + 1)
    } catch (e) {
      onError(String(e instanceof Error ? e.message : e))
    } finally {
      setBusy(false)
    }
  }

  const save = async () => {
    if (!draft) return
    const groups = [draft.balance, draft.bills, draft.daily_details]
    if (groups.some((x) => !(x.qps > 0) || !(x.concurrency > 0) || x.retry_max < 0)) {
      return onError('QPS、并发必须为正数。重试次数禁止为负。')
    }
    if (draft.bills.month_retry_max < 0 || draft.daily_details.month_retry_max < 0) {
      return onError('月份重试次数禁止为负。')
    }
    setBusy(true)
    try {
      const saved = await adminApi.saveScannerSettings(draft)
      setSettings(saved)
      setDraft(structuredClone(saved.settings))
      onToast('配置已保存。worker 15 秒内重载。')
    } catch (e) {
      onError(String(e instanceof Error ? e.message : e))
    } finally {
      setBusy(false)
    }
  }

  const numberValue = (raw: string) => raw.trim() === '' ? 0 : Number(raw)
  const limits = settings?.limits

  return (
    <>
      {/* 学校在最前面：下面三个采集器全都以它为前提，没选学校时它们根本不会跑 */}
      <SchoolView canWrite={canWrite} />
      <RunHistory
        kind="balance"
        title="余额扫描"
        desc="采集余额与累计用量。绑定优先刷新共用节流。"
        runs={scanRuns}
        active={balanceActive}
        busy={busy}
        canWrite={canWrite}
        nowMs={nowMs}
        onTrigger={() => act(() => adminApi.createScanRun({ qps: draft?.balance.qps, concurrency: draft?.balance.concurrency }), '余额扫描已创建')}
        onRetry={(id) => act(() => adminApi.retryScanRun(id), '余额异常项已排入重试')}
        onCancel={(id) => act(() => adminApi.cancelScanRun(id), '已请求终止。worker 收到后停止。')}
      />
      <RunHistory
        kind="detail"
        title="官方日明细扫描"
        desc="采集官方日用电量。初始化从起始月份回补。"
        runs={detailRuns}
        active={detailActive}
        busy={busy}
        canWrite={canWrite}
        nowMs={nowMs}
        onTrigger={() => act(() => adminApi.createDailyDetailRun({ qps: draft?.daily_details.qps, concurrency: draft?.daily_details.concurrency }), '日明细增量扫描已创建')}
        onRetry={(id) => act(() => adminApi.retryDailyDetailRun(id), '日明细异常项已排入重试')}
        onCancel={(id) => act(() => adminApi.cancelDailyDetailRun(id), '已请求终止。worker 收到后停止。')}
        extraAction={{
          label: '历史初始化',
          title: '从 BOOTSTRAP FROM 回补官方日明细。耗时较长。',
          onClick: () => act(() => adminApi.createDailyDetailRun({
            qps: draft?.daily_details.qps,
            concurrency: draft?.daily_details.concurrency,
            initialization: true,
          }), '日明细历史初始化已创建'),
        }}
      />
      <RunHistory
        kind="bill"
        title="月账单扫描"
        desc="抓取可见月份账单。历史变化记为待确认修订。"
        runs={billRuns}
        active={billActive}
        busy={busy}
        canWrite={canWrite}
        nowMs={nowMs}
        onTrigger={() => act(() => adminApi.createBillRun({ qps: draft?.bills.qps, concurrency: draft?.bills.concurrency }), '月账单扫描已创建')}
        onRetry={(id) => act(() => adminApi.retryBillRun(id), '月账单异常项已排入重试')}
        onCancel={(id) => act(() => adminApi.cancelBillRun(id), '已请求终止。worker 收到后停止。')}
      />

      <Section
        en="SCANNERS · CONFIG"
        title="统一扫描配置"
        desc={settings?.source === 'panel'
          ? `使用面板配置。保存后自动重载。最近由 ${settings.updated_by || '未知'} 于 ${fmtTime(settings.updated_at)} 保存。`
          : '使用环境变量。首次保存后由面板接管。'}
        actions={<Btn primary onClick={save} disabled={!canWrite || busy || !draft}>保存全部配置</Btn>}
      >
        {draft && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '34px' }}>
            <ConfigGroup title="共享流量闸门">
              <Field label="QPS" hint={`共用每秒请求上限。服务器上限 ${limits?.shared?.max_qps ?? '—'}`}><input value={draft.shared.qps} onChange={(e) => setDraft({ ...draft, shared: { ...draft.shared, qps: numberValue(e.target.value) } })} inputMode="decimal" style={fieldStyle} /></Field>
              <Field label="CONCURRENCY" hint={`同时在飞请求数。上限 ${limits?.shared?.max_concurrency ?? '—'}`}><input value={draft.shared.concurrency} onChange={(e) => setDraft({ ...draft, shared: { ...draft.shared, concurrency: numberValue(e.target.value) } })} inputMode="numeric" style={fieldStyle} /></Field>
            </ConfigGroup>

            <ConfigGroup title="余额扫描" enabled={draft.balance.enabled} onEnabled={(enabled) => setDraft({ ...draft, balance: { ...draft.balance, enabled } })}>
              <Field label="FULL CRON" hint="全校余额扫描的 5 段 cron。"><input value={draft.balance.cron} onChange={(e) => setDraft({ ...draft, balance: { ...draft.balance, cron: e.target.value } })} style={fieldStyle} /></Field>
              <Field label="BOUND CRON" hint="绑定电表优先刷新。留空关闭。"><input value={draft.balance.bound_cron} onChange={(e) => setDraft({ ...draft, balance: { ...draft.balance, bound_cron: e.target.value } })} style={fieldStyle} /></Field>
              <Field label="QPS" hint={`每秒启动表数。上限 ${limits?.balance.max_qps ?? '—'}`}><input value={draft.balance.qps} onChange={(e) => setDraft({ ...draft, balance: { ...draft.balance, qps: numberValue(e.target.value) } })} inputMode="decimal" style={fieldStyle} /></Field>
              <Field label="CONCURRENCY" hint={`服务器上限 ${limits?.balance.max_concurrency ?? '—'}`}><input value={draft.balance.concurrency} onChange={(e) => setDraft({ ...draft, balance: { ...draft.balance, concurrency: numberValue(e.target.value) } })} inputMode="numeric" style={fieldStyle} /></Field>
              <Field label="RETRY MAX"><input value={draft.balance.retry_max} onChange={(e) => setDraft({ ...draft, balance: { ...draft.balance, retry_max: numberValue(e.target.value) } })} inputMode="numeric" style={fieldStyle} /></Field>
            </ConfigGroup>

            <ConfigGroup title="官方日明细" enabled={draft.daily_details.enabled} onEnabled={(enabled) => setDraft({ ...draft, daily_details: { ...draft.daily_details, enabled } })}>
              <Field label="CRON"><input value={draft.daily_details.cron} onChange={(e) => setDraft({ ...draft, daily_details: { ...draft.daily_details, cron: e.target.value } })} style={fieldStyle} /></Field>
              <Field label="补扫 CRON" hint="重扫昨日空白占位。留空禁用。"><input value={draft.daily_details.retry_cron} onChange={(e) => setDraft({ ...draft, daily_details: { ...draft.daily_details, retry_cron: e.target.value } })} style={fieldStyle} /></Field>
              <Field label="QPS" hint={`服务器上限 ${limits?.daily_details.max_qps ?? '—'}`}><input value={draft.daily_details.qps} onChange={(e) => setDraft({ ...draft, daily_details: { ...draft.daily_details, qps: numberValue(e.target.value) } })} inputMode="decimal" style={fieldStyle} /></Field>
              <Field label="CONCURRENCY" hint={`服务器上限 ${limits?.daily_details.max_concurrency ?? '—'}`}><input value={draft.daily_details.concurrency} onChange={(e) => setDraft({ ...draft, daily_details: { ...draft.daily_details, concurrency: numberValue(e.target.value) } })} inputMode="numeric" style={fieldStyle} /></Field>
              <Field label="RETRY MAX"><input value={draft.daily_details.retry_max} onChange={(e) => setDraft({ ...draft, daily_details: { ...draft.daily_details, retry_max: numberValue(e.target.value) } })} inputMode="numeric" style={fieldStyle} /></Field>
              <Field label="MONTH RETRY MAX"><input value={draft.daily_details.month_retry_max} onChange={(e) => setDraft({ ...draft, daily_details: { ...draft.daily_details, month_retry_max: numberValue(e.target.value) } })} inputMode="numeric" style={fieldStyle} /></Field>
              <Field label="BOOTSTRAP FROM" hint="历史初始化起始月，YYYY-MM。"><input value={draft.daily_details.bootstrap_from} onChange={(e) => setDraft({ ...draft, daily_details: { ...draft.daily_details, bootstrap_from: e.target.value } })} placeholder="2025-01" style={fieldStyle} /></Field>
              <Field label="AUTO BOOTSTRAP" hint="无基线时，首轮定时自动回补。"><select value={draft.daily_details.auto_bootstrap ? 'on' : 'off'} onChange={(e) => setDraft({ ...draft, daily_details: { ...draft.daily_details, auto_bootstrap: e.target.value === 'on' } })} style={fieldStyle}><option value="on">开启</option><option value="off">关闭</option></select></Field>
            </ConfigGroup>

            <ConfigGroup title="月账单" enabled={draft.bills.enabled} onEnabled={(enabled) => setDraft({ ...draft, bills: { ...draft.bills, enabled } })}>
              <Field label="CRON"><input value={draft.bills.cron} onChange={(e) => setDraft({ ...draft, bills: { ...draft.bills, cron: e.target.value } })} style={fieldStyle} /></Field>
              <Field label="QPS" hint={`服务器上限 ${limits?.bills.max_qps ?? '—'}`}><input value={draft.bills.qps} onChange={(e) => setDraft({ ...draft, bills: { ...draft.bills, qps: numberValue(e.target.value) } })} inputMode="decimal" style={fieldStyle} /></Field>
              <Field label="CONCURRENCY" hint={`服务器上限 ${limits?.bills.max_concurrency ?? '—'}`}><input value={draft.bills.concurrency} onChange={(e) => setDraft({ ...draft, bills: { ...draft.bills, concurrency: numberValue(e.target.value) } })} inputMode="numeric" style={fieldStyle} /></Field>
              <Field label="RETRY MAX"><input value={draft.bills.retry_max} onChange={(e) => setDraft({ ...draft, bills: { ...draft.bills, retry_max: numberValue(e.target.value) } })} inputMode="numeric" style={fieldStyle} /></Field>
              <Field label="MONTH RETRY MAX"><input value={draft.bills.month_retry_max} onChange={(e) => setDraft({ ...draft, bills: { ...draft.bills, month_retry_max: numberValue(e.target.value) } })} inputMode="numeric" style={fieldStyle} /></Field>
            </ConfigGroup>
          </div>
        )}
        <div style={{ marginTop: '26px', maxWidth: '560px' }}>
          <Row label="配置来源" value={settings?.source === 'panel' ? '管理面板' : '环境变量'} />
          <Row label="最近保存" value={settings?.updated_at ? `${fmtTime(settings.updated_at)} · ${fmtAgo(settings.updated_at)}` : '—'} />
          <Row label="并行状态" value={activeKinds > 0 ? `${activeKinds} / 3 类正在运行` : '空闲'} />
          <Row label="共享流量闸门" value={`${settings?.settings.shared.qps ?? '—'} QPS / ${settings?.settings.shared.concurrency ?? '—'} 并发（上限 ${limits?.shared?.max_qps ?? '—'} / ${limits?.shared?.max_concurrency ?? '—'}）`} />
        </div>
      </Section>
    </>
  )
}

function ConfigGroup({
  title,
  enabled,
  onEnabled,
  children,
}: {
  title: string
  // 共享闸门始终生效。开关可选。
  enabled?: boolean
  onEnabled?: (enabled: boolean) => void
  children: ReactNode
}) {
  return (
    <div>
      <div style={{ display: 'flex', alignItems: 'center', gap: '14px', marginBottom: '20px' }}>
        <span style={{ fontSize: '17px', fontWeight: 600 }}>{title}</span>
        {onEnabled && (
          <>
            <span style={{ ...mono(), marginLeft: 'auto' }}>{enabled ? 'SCHEDULE ON' : 'MANUAL ONLY'}</span>
            <select value={enabled ? 'on' : 'off'} onChange={(e) => onEnabled(e.target.value === 'on')} style={{ ...fieldStyle, width: '132px', paddingTop: 4 }}>
              <option value="on">定时开启</option>
              <option value="off">仅手动</option>
            </select>
          </>
        )}
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(190px,1fr))', gap: '24px 30px' }}>
        {children}
      </div>
    </div>
  )
}
