import source from './CaptchaGate.tsx?raw'

function assert(condition: unknown, message: string) {
  if (!condition) throw new Error(message)
}

export function runCaptchaGateTests() {
  const component = source.slice(source.indexOf('export default function CaptchaGate'))
  // 条件返回必须在全部 hooks 之后，避免配置异步加载时 hook 顺序白屏。
  const firstConditionalReturn = component.indexOf('if (!config || !configured) return null')
  assert(firstConditionalReturn > 0, '找不到 CaptchaGate 的首个条件返回')

  const hookPattern = /\buse(?:Callback|Effect|Memo|Reducer|Ref|State)\s*\(/g
  const hooks = [...component.matchAll(hookPattern)]
  assert(hooks.length > 0, '没有解析到 CaptchaGate hooks')
  for (const hook of hooks) {
    assert(
      (hook.index ?? Number.MAX_SAFE_INTEGER) < firstConditionalReturn,
      `${hook[0]} 位于条件返回之后，会在 CAPTCHA 配置异步加载时触发 React hook 顺序白屏`,
    )
  }
}
