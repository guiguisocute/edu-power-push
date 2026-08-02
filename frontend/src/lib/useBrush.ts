/* 图表拖拽选区缩放手势 —— 概览用电量图 / 数据看板负荷图共用。
   pointer 事件统一鼠标与触屏；容器需 touchAction:'pan-y' 保证移动端纵向滚动不受影响。

   点选（onTap）也必须走这里：一旦容器 setPointerCapture，浏览器会把随后的
   click 重定向到捕获元素本身，子元素（柱子）上挂的 onClick 永远不会触发。
   于是「未拖动的一次抬手」即为点击。触屏亦可点。不必依赖 hover。 */

import { useRef, useState } from 'react'
import type { PointerEvent } from 'react'

export interface BrushTapMeta {
  pointerType: string
}

export interface Brush {
  /** 拖拽进行中的选区（显示索引，未排序），松手后为 null */
  sel: [number, number] | null
  props: {
    onPointerDown: (e: PointerEvent<HTMLDivElement>) => void
    onPointerMove: (e: PointerEvent<HTMLDivElement>) => void
    onPointerUp: (e: PointerEvent<HTMLDivElement>) => void
    onPointerCancel: (e: PointerEvent<HTMLDivElement>) => void
  }
}

/** 光标横坐标 → 第几根柱。双击钻取要和拖拽选区用同一套换算，否则会差一根。 */
export function indexFromEvent(
  e: { clientX: number; currentTarget: Element },
  count: number,
): number {
  const r = e.currentTarget.getBoundingClientRect()
  const x = Math.min(Math.max(e.clientX - r.left, 0), r.width - 0.01)
  return Math.min(count - 1, Math.max(0, Math.floor((x / r.width) * count)))
}

export function useBrush(
  count: number,
  apply: (a: number, b: number) => void,
  onTap?: (i: number, meta: BrushTapMeta) => void,
): Brush {
  const [sel, setSel] = useState<[number, number] | null>(null)
  const selRef = useRef<[number, number] | null>(null)
  const active = useRef(false)
  const pointerType = useRef('mouse')

  const idx = (e: PointerEvent<HTMLDivElement>) => indexFromEvent(e, count)
  const update = (v: [number, number] | null) => {
    selRef.current = v
    setSel(v)
  }

  return {
    sel,
    props: {
      onPointerDown: (e) => {
        if (e.pointerType === 'mouse' && e.button !== 0) return
        try {
          e.currentTarget.setPointerCapture(e.pointerId)
        } catch {}
        active.current = true
        pointerType.current = e.pointerType || 'mouse'
        const i = idx(e)
        update([i, i])
      },
      onPointerMove: (e) => {
        if (!active.current || !selRef.current) return
        update([selRef.current[0], idx(e)])
      },
      onPointerUp: () => {
        if (!active.current) return
        active.current = false
        const v = selRef.current
        const pt = pointerType.current
        update(null)
        if (v) {
          const a = Math.min(v[0], v[1])
          const b = Math.max(v[0], v[1])
          if (b - a >= 1) apply(a, b)
          else onTap?.(a, { pointerType: pt })
        }
      },
      onPointerCancel: () => {
        active.current = false
        update(null)
      },
    },
  }
}
