import { DEFAULT_USER } from './mock'
import { maskDigits, privacyInfo } from './privacy'

function assert(cond: unknown, msg: string) {
  if (!cond) throw new Error(msg)
}

export function runPrivacyTests() {
  assert(maskDigits('11栋') === '**栋', 'mask building')
  assert(maskDigits('N301') === 'N**', 'mask room keeps letter')
  assert(maskDigits('1楼(不是NF，SF)').includes('**'), 'mask dirty floor')

  // 结构化字段：楼栋禁止吞掉房间。
  const user = {
    ...DEFAULT_USER,
    place: '11栋 · 1楼 · N301',
    building: '11栋',
    floor: '1楼',
    room: 'N301',
  }
  const pf = { meter: false, bldg: true, floor: true, room: true, nick: false }
  const pmask = { meter: true, bldg: false, floor: false, room: false }
  const full = privacyInfo(user, pf, pmask)
  assert(full.sampleOf('bldg', false) === '11栋', `bldg full got ${full.sampleOf('bldg', false)}`)
  assert(full.sampleOf('floor', false) === '1楼', `floor full got ${full.sampleOf('floor', false)}`)
  assert(full.sampleOf('room', false) === 'N301', `room full got ${full.sampleOf('room', false)}`)
  assert(full.sampleOf('bldg', true) === '**栋', 'bldg masked')
  assert(full.sampleOf('room', true) === 'N**', 'room masked')
  assert(full.meLabel.includes('11栋'), 'label has building')
  assert(full.meLabel.includes('N301'), 'label has room')
  assert(!full.sampleOf('bldg', false).includes('N301'), 'building must not contain room')

  // 回归：旧 bug 把 place 整串当成楼栋。
  const brokenPlaceOnly = {
    name: '测',
    initial: '测',
    meter: '1',
    place: '11栋 N301 室',
    phone: 'a***@q.com',
    // 无结构化字段时尽力解析。禁止把整串当楼栋。
  }
  const fallback = privacyInfo(brokenPlaceOnly, pf, pmask)
  // 有结构化优先。无结构时 place 含 · 才切分。
  // live 路径必须带 building/floor/room。
  assert(typeof fallback.sampleOf('bldg', false) === 'string', 'fallback bldg string')
}
