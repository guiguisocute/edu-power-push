/* 榜单脱敏偏好的前后端映射。
   所有电表强制参与排行。此处仅同步展示与打码字段。
   live 登录后查询一次，改动时 PUT。mock 纯本地。
   后端禁止榜单展示电表号。live 下 FIELD_KEYS 不含 meter。 */

import { api } from './client'
import { IS_LIVE } from './mode'
import type { LeaderboardPreference } from './types'

/** live 下可配身份字段。前端键名映射后端字段。 */
export const FIELD_KEYS = ['bldg', 'floor', 'room', 'nick'] as const

/** 与 store 三字段同形，便于 set 与 persist。 */
export interface LocalPrivacy extends Record<string, unknown> {
  joinBoard: boolean
  pf: Record<string, boolean>
  pmask: Record<string, boolean>
}

export function toLocal(pref: LeaderboardPreference): LocalPrivacy {
  return {
    joinBoard: true, // 强制参与。忽略服务端历史 opted_in=false。
    pf: {
      meter: false,
      bldg: pref.show_building,
      floor: pref.show_floor,
      room: pref.show_room,
      nick: pref.show_nickname,
    },
    pmask: {
      meter: true,
      bldg: pref.mask_building,
      floor: pref.mask_floor,
      room: pref.mask_room !== false, // 缺省打码
    },
  }
}

function toRemote(local: LocalPrivacy): Partial<LeaderboardPreference> {
  return {
    opted_in: true,
    show_building: !!local.pf.bldg,
    show_floor: !!local.pf.floor,
    show_room: !!local.pf.room,
    show_nickname: !!local.pf.nick,
    mask_building: !!local.pmask.bldg,
    mask_floor: !!local.pmask.floor,
    mask_room: !!local.pmask.room,
  }
}

/** 登录后查询。未登录、mock 或端点缺失返回 null。调用方沿用本地值。 */
export async function loadPrivacy(): Promise<LocalPrivacy | null> {
  if (!IS_LIVE) return null
  try {
    return toLocal(await api.leaderboardPreference())
  } catch {
    return null
  }
}

/** 保存偏好。返回后端确认值。失败返回 null。 */
export async function savePrivacy(local: LocalPrivacy): Promise<LocalPrivacy | null> {
  if (!IS_LIVE) return null
  try {
    return toLocal(await api.saveLeaderboardPreference(toRemote(local)))
  } catch {
    return null
  }
}
