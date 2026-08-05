import source from './CaptchaGate.tsx?raw'

function assert(condition: unknown, message: string) {
  if (!condition) throw new Error(message)
}

export function runCaptchaGateTests() {
  const component = source.slice(source.indexOf('export default function CaptchaGate'))
  // 条件返回必须在全部 hooks 之后，避免配置异步加载时 hook 顺序白屏。
  const firstConditionalReturn = component.indexOf('if (configError)')
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

  // 已启用验证后不得因脚本失败在浏览器侧自行放行。
  assert(!component.includes('bypassed'), 'CaptchaGate 仍含客户端 bypass，会与服务端强制校验冲突')
  assert(component.includes('configError'), '配置读取失败应有明确、可重试的 UI 状态')

  const widget = source.slice(source.indexOf('export function TurnstileWidget'))
  assert(widget.includes('重新加载'), 'Turnstile 脚本失败后缺少用户可见的重试入口')
  assert(source.includes('scriptPromise = null'), 'Turnstile 脚本失败后未清理 promise，重试会复用永久失败状态')
}
