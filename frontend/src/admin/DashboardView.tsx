/* 控制台首页。一屏展示系统是否正常。
   指标来自 admin/overview 与 health/ready。
   缺数据显示 — 并说明原因。禁止用估算值凑数。 */

import { useEffect, useState } from 'react'
import { adminApi, type AdminOverview } from './api'
import type { Readiness } from '../api/types'
import { Dot, Empty, Row, Section, Stat, fmtAgo, fmtTime, mono } from './ui'

export default function DashboardView({ onError }: { onError: (msg: string) => void }) {
  const [overview, setOverview] = useState<AdminOverview | null>(null)
  const [ready, setReady] = useState<Readiness | null>(null)
  const [tick, setTick] = useState(0)

  useEffect(() => {
    let alive = true
    Promise.all([adminApi.overview(), adminApi.readiness().catch(() => null)])
      .then(([o, r]) => {
        if (!alive) return
        setOverview(o)
        setReady(r)
      })
      .catch((e) => alive && onError(String(e instanceof Error ? e.message : e)))
    return () => {
      alive = false
    }
  }, [tick])

  // 每 30 秒刷新一次。运维盯屏时无需手动刷新。
  useEffect(() => {
    const timer = setInterval(() => setTick((n) => n + 1), 30_000)
    return () => clearInterval(timer)
  }, [])

  if (!overview) {
    return (
      <Section en="CONSOLE" title="控制台">
        <Empty title="正在读取系统状态" desc="连接 /api/v1/admin/overview。" />
      </Section>
    )
  }

  const workerTone = overview.worker === 'ready' ? 'ok' : overview.worker === 'unavailable' ? 'bad' : 'warn'
  const anomalyTone = overview.unacknowledged_critical_anomalies > 0 ? 'var(--red)' : undefined
  const coverage =
    overview.inventory.inventory_total > 0
      ? ((overview.inventory.eligible_total / overview.inventory.inventory_total) * 100).toFixed(1)
      : '—'

  return (
    <>
      <Section
        en="CONSOLE"
        title="控制台"
        desc="系统状态一览。每 30 秒自动刷新。"
        actions={<span style={{ fontSize: '12px', color: 'var(--fg3)' }}>版本 {overview.version}</span>}
      >
        <div
          data-r="split"
          style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(180px,1fr))', gap: '28px 0' }}
        >
          <Stat
            en="ELIGIBLE METERS"
            value={overview.inventory.eligible_total.toLocaleString()}
            note={`库存共 ${overview.inventory.inventory_total.toLocaleString()} 块，排除 ${overview.inventory.excluded_total.toLocaleString()} 块 · 覆盖 ${coverage}%`}
          />
          <Stat
            en="WORKER"
            value={{ ready: '正常', degraded: '降级', unavailable: '离线' }[overview.worker] || overview.worker}
            note={'心跳 ' + fmtAgo(overview.worker_heartbeat_at)}
            tone={workerTone === 'ok' ? undefined : 'var(--red)'}
          />
          <Stat
            en="OPEN ANOMALIES"
            value={overview.unacknowledged_anomalies.toLocaleString()}
            note={`其中严重 ${overview.unacknowledged_critical_anomalies} 条 · 待确认账单修订 ${overview.unacknowledged_bill_revisions} 条`}
            tone={anomalyTone}
          />
          <Stat
            en="MAIL"
            value={overview.mail_configured ? '可用' : '未配置'}
            note={
              overview.mail_configured
                ? '当前供应商 ' + overview.mail_provider
                : '推送与验证码不可用。去「推送渠道」配置。'
            }
            tone={overview.mail_configured ? undefined : 'var(--red)'}
          />
        </div>
      </Section>

      <Section en="HEALTH" title="服务状态" desc="来自 /health/ready 与 overview 的判定。">
        <div data-r="split" style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0 44px' }}>
          <div>
            <div style={{ ...mono(), marginBottom: '10px' }}>RUNTIME</div>
            <Row
              label="总体"
              value={
                <span style={{ display: 'inline-flex', alignItems: 'center', gap: '8px' }}>
                  <Dot tone={ready?.status === 'ready' ? 'ok' : ready?.status ? 'warn' : 'bad'} />
                  {ready?.status || '未知'}
                </span>
              }
            />
            <Row label="数据库" value={overview.database} />
            <Row label="迁移" value={overview.migrations} />
            <Row label="最近一次聚合" value={fmtTime(overview.last_aggregate_at) + ' · ' + fmtAgo(overview.last_aggregate_at)} />
          </div>
          <div>
            <div style={{ ...mono(), marginBottom: '10px' }}>SCHEDULES</div>
            <Row label="扫描 cron" value={overview.scan_schedule.cron || '已关闭'} />
            <Row label="绑定电表 cron" value={overview.scan_schedule.bound_cron || '已关闭'} />
            <Row
              label="扫描节流"
              value={`${overview.scan_schedule.qps} QPS · 并发 ${overview.scan_schedule.concurrency}`}
            />
            <Row label="账单 cron" value={overview.bill_schedule.cron || '已关闭'} />
            <Row
              label="账单节流"
              value={`${overview.bill_schedule.qps} QPS · 并发 ${overview.bill_schedule.concurrency}`}
            />
            <Row label="日明细 cron" value={overview.detail_schedule.cron || '已关闭'} />
            <Row
              label="日明细节流"
              value={`${overview.detail_schedule.qps} QPS · 并发 ${overview.detail_schedule.concurrency}`}
            />
          </div>
        </div>
      </Section>
    </>
  )
}
