/* 电表号格式。
   mock 为 8 位。真实库存多为 12 位。
   契约为 ^[0-9]{6,32}$。live 必须按契约放宽。 */

import { IS_LIVE } from '../api/mode'

/** 契约上限。mock 保持 8 位以对齐原型。 */
export const METER_MAX_LEN = IS_LIVE ? 32 : 8
export const METER_PLACEHOLDER = IS_LIVE ? '电表号' : '00000000'

const LIVE_RE = /^\d{6,32}$/
const MOCK_RE = /^\d{8}$/

export function isValidMeter(no: string): boolean {
  return (IS_LIVE ? LIVE_RE : MOCK_RE).test(no)
}

export const METER_HINT = IS_LIVE
  ? '电表号为 6–32 位数字，请核对后重试'
  : '电表号为 8 位数字，请核对后重试'
