/* API 模式优先级：URL ?api= → localStorage → VITE_API_MODE → mock。
   mock = 演示数据。live = 后端 /api/v1 真实数据。
   数据不足时展示 availability。禁止伪造数值。 */

let fromQuery: string | null = null
try {
  fromQuery = new URLSearchParams(window.location.search).get('api')
  if (fromQuery === 'live' || fromQuery === 'mock') localStorage.setItem('edu-power-api-mode', fromQuery)
} catch {}

let stored: string | null = null
try {
  stored = localStorage.getItem('edu-power-api-mode')
} catch {}

export const API_MODE: 'live' | 'mock' =
  stored === 'live' || stored === 'mock'
    ? stored
    : import.meta.env.VITE_API_MODE === 'live'
      ? 'live'
      : 'mock'

export const IS_LIVE = API_MODE === 'live'
