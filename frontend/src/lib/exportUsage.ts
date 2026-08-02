/* 用电详情导出。组装、序列化与下载均为纯函数，便于单测。
   导出逐日明细：日期、用电量、电费、余额。
   缺数写空，禁止写 0。0 表示零用电；空表示未抄表。 */

export interface UsageExportRow {
  /** YYYY-MM-DD，学校时区 */
  date: string
  kwh: number | null
  /** kwh × 电价，两位小数。kwh 缺失时为 null。 */
  cost: number | null
  balance: number | null
}

export interface UsageExportMeta {
  meter: string
  place: string
  from: string
  to: string
  /** 元/kWh */
  rate: number
  generatedAt: string
}

export type UsageExportFormat = 'csv' | 'json'

/** 时序桶转 YYYY-MM-DD（学校时区）。避免浏览器时区推前或推后一天。 */
export function bucketDate(periodStart: string): string {
  const date = new Date(periodStart)
  if (!Number.isFinite(date.getTime())) return ''
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(date)
  return parts
}

interface SeriesLikePoint {
  period_start: string
  value?: string | null
}

/**
 * 将用电量与余额两条日序列按日期对齐为表格行。
 * 以用电量日期为准，再并上仅有余额的日期。
 */
export function buildUsageRows(
  consumption: SeriesLikePoint[],
  balance: SeriesLikePoint[],
  rate: number,
): UsageExportRow[] {
  const kwhByDate = new Map<string, number | null>()
  for (const point of consumption) {
    const date = bucketDate(point.period_start)
    if (!date) continue
    const value = point.value == null ? null : parseFloat(point.value)
    kwhByDate.set(date, value == null || !Number.isFinite(value) ? null : value)
  }
  const balanceByDate = new Map<string, number | null>()
  for (const point of balance) {
    const date = bucketDate(point.period_start)
    if (!date) continue
    const value = point.value == null ? null : parseFloat(point.value)
    balanceByDate.set(date, value == null || !Number.isFinite(value) ? null : value)
  }
  const dates = Array.from(new Set([...kwhByDate.keys(), ...balanceByDate.keys()])).sort()
  return dates.map((date) => {
    const kwh = kwhByDate.get(date) ?? null
    return {
      date,
      kwh,
      cost: kwh == null ? null : Math.round(kwh * rate * 100) / 100,
      balance: balanceByDate.get(date) ?? null,
    }
  })
}

/* 逗号、引号、换行必须转义。否则一逗号会错位整列。
   RFC 4180：字段用双引号包住，内部双引号写成两个。 */
function csvCell(value: string): string {
  return /[",\n\r]/.test(value) ? '"' + value.replace(/"/g, '""') + '"' : value
}

/** Excel 按 UTF-8 打开 CSV 需要 BOM。否则中文表头乱码。 */
export const CSV_BOM = '﻿'

export function toCSV(rows: UsageExportRow[]): string {
  const header = ['日期', '用电量(kWh)', '电费(元)', '余额(元)']
  const lines = [header.map(csvCell).join(',')]
  for (const row of rows) {
    lines.push(
      [
        row.date,
        row.kwh == null ? '' : row.kwh.toFixed(2),
        row.cost == null ? '' : row.cost.toFixed(2),
        row.balance == null ? '' : row.balance.toFixed(2),
      ]
        .map(csvCell)
        .join(','),
    )
  }
  // 末尾保留换行。否则部分工具会把最后一行当成未完片段。
  return CSV_BOM + lines.join('\r\n') + '\r\n'
}

export function toJSON(rows: UsageExportRow[], meta: UsageExportMeta): string {
  return JSON.stringify(
    {
      meter: meta.meter,
      place: meta.place,
      from: meta.from,
      to: meta.to,
      electricity_rate_yuan_per_kwh: meta.rate,
      generated_at: meta.generatedAt,
      /* 单位写入文件。cost 与 balance 单位为 CNY。 */
      units: { kwh: 'kWh', cost: 'CNY', balance: 'CNY' },
      row_count: rows.length,
      rows,
    },
    null,
    2,
  )
}

export function exportFilename(meta: UsageExportMeta, format: UsageExportFormat): string {
  const meter = meta.meter.replace(/[^\w-]/g, '') || 'meter'
  const span = meta.from === meta.to ? meta.from : `${meta.from}_${meta.to}`
  return `用电详情_${meter}_${span}.${format}`
}

/** 在浏览器下载文件。SSR 下跳过。 */
export function downloadText(filename: string, mime: string, text: string): void {
  if (typeof document === 'undefined' || typeof URL.createObjectURL !== 'function') return
  const blob = new Blob([text], { type: mime })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  // 立即 revoke 会打断部分浏览器的下载。下一帧再释放。
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

export function serializeUsageExport(
  rows: UsageExportRow[],
  meta: UsageExportMeta,
  format: UsageExportFormat,
): { filename: string; mime: string; text: string } {
  return format === 'json'
    ? { filename: exportFilename(meta, 'json'), mime: 'application/json;charset=utf-8', text: toJSON(rows, meta) }
    : { filename: exportFilename(meta, 'csv'), mime: 'text/csv;charset=utf-8', text: toCSV(rows) }
}
