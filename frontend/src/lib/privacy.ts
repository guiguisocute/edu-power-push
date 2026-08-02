/* 排行榜身份展示字段。排行榜与隐私预览共用。
   live 必须用结构化 building/floor/room。
   禁止从 place 字符串硬切。 */

import type { User } from './mock'
import { IS_LIVE } from '../api/mode'

export interface PrivacyInfo {
  sampleOf: (id: string, mk: boolean) => string
  mePieces: string[]
  meLabel: string
  uRoom: string
  uMeter: string
  uFloor: string
  bldgFull: string
  bldgMask: string
}

/** 只打码数字段。例：11栋 → **栋；N301 → N**。 */
export function maskDigits(value: string): string {
  if (!value) return value
  const masked = value.replace(/[0-9]+/g, '**')
  return masked === value ? '**' : masked
}

function resolveLocation(user: User | null) {
  // 优先结构化字段
  if (user?.building || user?.floor || user?.room) {
    return {
      building: (user.building || '').trim(),
      floor: (user.floor || '').trim(),
      room: (user.room || '').trim(),
    }
  }
  /* mock 兜底：旧 place 字符串。
     live 下 toViewUser 保证绑了表就有结构化字段，走到这里只可能是没绑表。
     那就一个字段都不该编：这块预览写着「别人看到的你」。 */
  if (IS_LIVE && !user?.place) return { building: '', floor: '', room: '' }
  const place = user?.place || '河东 12 栋 · 4 楼 · 402'
  if (place.includes('·')) {
    const parts = place.split('·').map((s) => s.trim())
    return { building: parts[0] || '', floor: parts[1] || '', room: parts[2] || '' }
  }
  const m = place.match(/^(.*?)[\s]+(\d{2,4})\s*$/)
  if (m) return { building: m[1].trim(), floor: '', room: m[2] }
  return { building: place, floor: '', room: '' }
}

export function privacyInfo(
  user: User | null,
  pf: Record<string, boolean>,
  pmask: Record<string, boolean>,
): PrivacyInfo {
  const loc = resolveLocation(user)
  const bldgFull = loc.building || '—'
  const bldgMask = maskDigits(bldgFull)
  const uFloor = loc.floor || '—'
  const uRoom = loc.room || '—'
  // 同理：live 没绑表就是没有表号，不拿原型的 31240718 顶上
  const uMeter = user?.meter || (IS_LIVE ? '—' : '31240718')
  const sampleOf = (id: string, mk: boolean): string => {
    switch (id) {
      case 'meter':
        if (uMeter === '—') return uMeter
        return mk ? uMeter.slice(0, 4) + '****' : uMeter
      case 'bldg':
        return mk ? bldgMask : bldgFull
      case 'floor':
        return mk ? maskDigits(uFloor) : uFloor
      case 'room':
        return mk ? maskDigits(uRoom) : uRoom
      case 'nick':
        return user ? user.name : IS_LIVE ? '—' : '林亦'
      default:
        return ''
    }
  }
  const mePieces: string[] = []
  if (user) {
    if (pf.nick) mePieces.push(user.name)
    if (pf.bldg) mePieces.push(sampleOf('bldg', !!pmask.bldg))
    if (pf.floor) mePieces.push(sampleOf('floor', !!pmask.floor))
    if (pf.room) mePieces.push(sampleOf('room', !!pmask.room))
    if (pf.meter) mePieces.push(sampleOf('meter', !!pmask.meter))
  }
  const meLabel = mePieces.length ? mePieces.join(' · ') : '匿名用户'
  return { sampleOf, mePieces, meLabel, uRoom, uMeter, uFloor, bldgFull, bldgMask }
}
