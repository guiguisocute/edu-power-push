/* 筛选下拉（楼栋/楼层）数据源。
   live：campus/scopes，仅含 eligible 电表。楼层字符串原样回传。
   禁止格式加工，否则筛选结果为空。
   mock：沿用 DORMS 与 1–6F，楼层值为下标。 */

import { useMemo } from 'react'
import { IS_LIVE } from '../api/mode'
import { useLiveScopes } from '../api/live'
import { DORMS } from './mock'

export interface Opt {
  v: string
  l: string
}

export interface ScopeOptions {
  buildings: Opt[]
  floors: Opt[]
  loading: boolean
  error: string | null
}

const ALL_BLDG: Opt = { v: 'all', l: '全部宿舍楼' }
const ALL_FLOOR: Opt = { v: 'all', l: '全部楼层' }

/** building 为当前选中楼栋值。all 表示未选。 */
export function useScopeOptions(building: string, allBuildingLabel = '全部宿舍楼'): ScopeOptions {
  const live = useLiveScopes()
  return useMemo(() => {
    const head = { ...ALL_BLDG, l: allBuildingLabel }
    if (!IS_LIVE) {
      return {
        buildings: [head, ...DORMS.map((d) => ({ v: d, l: d }))],
        floors: [ALL_FLOOR, ...Array.from({ length: 6 }, (_, i) => ({ v: String(i), l: i + 1 + 'F' }))],
        loading: false,
        error: null,
      }
    }
    const flat = (live.data?.campuses ?? []).flatMap((c) => c.buildings)
    const hit = flat.find((b) => b.name === building)
    /* 楼层标签直接用后端字符串。禁止拼接或补零，以免查询参数对不上。 */
    return {
      buildings: [head, ...flat.map((b) => ({ v: b.name, l: b.name }))],
      floors: hit ? [ALL_FLOOR, ...hit.floors.map((f) => ({ v: f, l: f }))] : [],
      loading: live.loading,
      error: live.error,
    }
  }, [live.data, live.loading, live.error, building, allBuildingLabel])
}

/** 选中楼层转查询参数。live 原样透传。mock 下标不参与请求。 */
export function floorParam(floor: string): string | undefined {
  if (!IS_LIVE || floor === 'all' || !floor) return undefined
  return floor
}

/** 选中楼栋转查询参数 */
export function buildingParam(building: string): string | undefined {
  if (!IS_LIVE || building === 'all' || !building) return undefined
  return building
}
