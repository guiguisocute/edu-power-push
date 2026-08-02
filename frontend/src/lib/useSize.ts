/* 元素实测宽度。坐标轴标签疏密必须按实测宽度。
   桌面与手机可放标签数差数倍。禁止写死 step。 */

import { useEffect, useState, type RefObject } from 'react'

export function useElementWidth<T extends HTMLElement>(ref: RefObject<T | null>): number {
  const [w, setW] = useState(0)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    // SSR 或无 ResizeObserver：一次性读数。
    if (typeof ResizeObserver === 'undefined') {
      setW(el.getBoundingClientRect().width)
      return
    }
    const ro = new ResizeObserver((entries) => {
      for (const e of entries) setW(e.contentRect.width)
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [ref])
  return w
}

/** 触屏或粗指针。手机走点选。鼠标桌面保持悬停。 */
export function useIsCoarsePointer(): boolean {
  const [coarse, setCoarse] = useState(() =>
    typeof window !== 'undefined' ? window.matchMedia('(pointer: coarse)').matches : false,
  )
  useEffect(() => {
    const mq = window.matchMedia('(pointer: coarse)')
    const sync = () => setCoarse(mq.matches)
    sync()
    mq.addEventListener('change', sync)
    return () => mq.removeEventListener('change', sync)
  }, [])
  return coarse
}
