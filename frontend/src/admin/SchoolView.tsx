/* 采集目标学校。
   改完后所有采集器换目标。独占一段，不与校名文案混排。
   学校清单来自后端上游 provider。前端不内置名单。 */

import { useEffect, useMemo, useState } from 'react'
import { adminApi, type SchoolOption, type SchoolSettingsView } from './api'
import { Btn, Dot, Field, Row, Section, fieldStyle, mono } from './ui'

/** 清单可含几十所。全量渲染 option，并支持搜索。 */
function matches(school: SchoolOption, query: string): boolean {
  const q = query.trim().toLowerCase()
  if (!q) return true
  return school.id.toLowerCase().includes(q) || school.name.toLowerCase().includes(q)
}

function capabilityLabel(school: SchoolOption): string {
  const parts: string[] = []
  if (school.balance) parts.push('余额')
  if (school.bill) parts.push('月账单')
  if (school.detail) parts.push('日明细')
  return parts.length ? parts.join(' · ') : '能力未知'
}

export default function SchoolView({ canWrite }: { canWrite: boolean }) {
  const [view, setView] = useState<SchoolSettingsView | null>(null)
  const [providerName, setProviderName] = useState('')
  const [areaID, setAreaID] = useState('')
  const [areaName, setAreaName] = useState('')
  const [baseURL, setBaseURL] = useState('')
  const [query, setQuery] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState('')

  const load = () => {
    adminApi
      .schoolSettings()
      .then((next) => {
        setView(next)
        setProviderName(next.current.provider || next.providers[0]?.name || '')
        setAreaID(next.current.area_id || '')
        setAreaName(next.current.area_name || '')
        setBaseURL(next.current.base_url || '')
        setError('')
      })
      .catch((e: Error) => setError(e.message))
  }

  useEffect(load, [])

  const provider = useMemo(
    () => view?.providers.find((p) => p.name === providerName) ?? view?.providers[0],
    [view, providerName],
  )
  const schools = provider?.schools ?? []
  const visible = useMemo(() => schools.filter((s) => matches(s, query)), [schools, query])
  const selected = schools.find((s) => s.id === areaID.trim())

  /* 清单是实测结果，不是白名单。上游可新增学校。
     清单外 id 不拦截，仅提示。能否跑通由真实请求判定。 */
  const offList = areaID.trim() !== '' && schools.length > 0 && !selected

  const save = () => {
    setBusy(true)
    setError('')
    setSaved('')
    adminApi
      .saveSchoolSettings({
        provider: providerName,
        area_id: areaID.trim(),
        area_name: areaName.trim(),
        base_url: baseURL.trim(),
      })
      .then((next) => {
        setView(next)
        setSaved(next.configured ? '已保存,15 秒内切换学校' : '已保存,未选学校，采集器停止。')
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => setBusy(false))
  }

  if (!view) {
    return (
      <Section en="UPSTREAM · SCHOOL" title="学校" desc="选择采集目标。">
        <Row label={error ? '加载失败' : '加载中'} value={error || '…'} />
      </Section>
    )
  }

  return (
    <Section
      en="UPSTREAM · SCHOOL"
      title="学校"
      desc="设定采集目标学校。15 秒内 worker 接管。"
      actions={
        <Btn onClick={save} primary disabled={!canWrite || busy}>
          {busy ? '保存中…' : '保存'}
        </Btn>
      }
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: '18px' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '10px' }}>
          <Dot tone={view.configured ? 'ok' : 'warn'} />
          <span style={{ fontSize: '13px' }}>
            {view.configured
              ? `当前采集：${view.current.area_name || '(未命名)'}（id ${view.current.area_id}）`
              : '未选学校。采集器等待中。'}
          </span>
          <span style={mono('10px')}>
            {view.source === 'panel' ? 'FROM PANEL' : 'FROM .ENV'}
          </span>
        </div>

        <div
          data-r="split"
          style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(220px,1fr))', gap: '26px 34px' }}
        >
          <Field label="PROVIDER" hint="上游实现。接入后出现在列表。">
            <select
              value={provider?.name ?? ''}
              onChange={(e) => {
                setProviderName(e.target.value)
                // 换上游后清除旧 area id。旧 id 通常无效。
                setAreaID('')
                setAreaName('')
                setQuery('')
              }}
              style={fieldStyle}
              disabled={!canWrite || view.providers.length < 2}
            >
              {view.providers.map((p) => (
                <option key={p.name} value={p.name}>
                  {p.display_name}（{p.name}）
                </option>
              ))}
            </select>
          </Field>

          <Field label="SEARCH" hint={`按校名或 id 过滤。共 ${schools.length} 所。`}>
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="例如：师范 / 83"
              style={fieldStyle}
              disabled={!canWrite || schools.length === 0}
            />
          </Field>
        </div>

        {schools.length > 0 && (
          <Field label="SCHOOL" hint={`筛出 ${visible.length} 所。括号为实测能力。`}>
            <select
              value={selected ? selected.id : ''}
              onChange={(e) => {
                const next = schools.find((s) => s.id === e.target.value)
                setAreaID(next?.id ?? '')
                setAreaName(next?.name ?? '')
              }}
              style={fieldStyle}
              disabled={!canWrite}
            >
              <option value="">— 不选（停止采集）—</option>
              {visible.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}（{s.id} · {capabilityLabel(s)}）
                </option>
              ))}
            </select>
          </Field>
        )}

        <div
          data-r="split"
          style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(220px,1fr))', gap: '26px 34px' }}
        >
          <Field label="AREA ID" hint="上游 areaId。清单外可直接填写。">
            <input
              value={areaID}
              onChange={(e) => setAreaID(e.target.value)}
              placeholder="例如：83"
              style={fieldStyle}
              disabled={!canWrite}
            />
          </Field>
          <Field label="AREA NAME" hint="学校名。用于展示与日志。">
            <input
              value={areaName}
              onChange={(e) => setAreaName(e.target.value)}
              style={fieldStyle}
              disabled={!canWrite}
            />
          </Field>
          <Field label="BASE URL" hint={`上游入口。留空 = ${provider?.default_base_url || '（无默认值，必填）'}`}>
            <input
              value={baseURL}
              onChange={(e) => setBaseURL(e.target.value)}
              placeholder={provider?.default_base_url || 'https://…'}
              style={fieldStyle}
              disabled={!canWrite}
            />
          </Field>
        </div>

        {offList && (
          <Row
            label="提示"
            value={`id ${areaID.trim()} 不在实测清单。可保存；能否跑通取决于请求。`}
          />
        )}
        {error && <Row label="保存失败" value={error} />}
        {saved && !error && <Row label="已保存" value={saved} />}
      </div>
    </Section>
  )
}
