/* 外链深链解析。
   reset=1 打开重置密码浮层。
   第三方登录回跳带 ?oauth= 或 ?oauth_error=。
   此处翻成提示文案并清除参数，避免刷新重复弹出。 */

export type DeepLinkAction = { kind: 'forgot_password' } | { kind: 'none' }

export type OAuthReturn =
  | { kind: 'success'; provider: string; result: string }
  | { kind: 'error'; reason: string }
  | { kind: 'none' }

const OAUTH_PARAMS = ['oauth', 'oauth_result', 'oauth_error']

const PROVIDER_LABEL: Record<string, string> = {
  google: 'Google',
  github: 'GitHub',
}

export function providerLabel(provider: string): string {
  return PROVIDER_LABEL[provider] || provider
}

/* 失败原因码来自后端。逐条映射可读提示。 */
const OAUTH_ERROR_TEXT: Record<string, string> = {
  denied: '已取消第三方登录',
  state: '登录请求已过期，请重新点一次',
  exchange: '第三方登录失败，请稍后再试',
  email_missing: '该账号没有可用邮箱，请先在第三方平台设置并验证邮箱',
  email_unverified: '该邮箱在第三方平台尚未验证，请先完成验证再登录',
  registration_disabled: '注册暂未开放，请用已有账号登录',
  conflict: '这个邮箱已绑定其它登录方式，请用原方式登录后再关联',
  account_disabled: '账号已被停用，请联系管理员',
  disabled_entry: '该登录方式已关闭',
  unavailable: '第三方登录暂不可用',
}

/** 从 location.search 解析第三方登录回跳结果。 */
export function parseOAuthReturn(search: string): OAuthReturn {
  const raw = search.startsWith('?') ? search.slice(1) : search
  const params = new URLSearchParams(raw)
  const failure = (params.get('oauth_error') || '').trim()
  if (failure) return { kind: 'error', reason: failure }
  const provider = (params.get('oauth') || '').trim()
  if (provider) return { kind: 'success', provider, result: (params.get('oauth_result') || '').trim() }
  return { kind: 'none' }
}

/** 回跳提示语。成功分：新建账号、关联已有账号、登录。 */
export function oauthReturnMessage(value: OAuthReturn): string {
  if (value.kind === 'error') return OAUTH_ERROR_TEXT[value.reason] || '第三方登录失败，请稍后再试'
  if (value.kind !== 'success') return ''
  const name = providerLabel(value.provider)
  if (value.result === 'created') return `已用 ${name} 创建账号 · 请先绑定宿舍电表`
  if (value.result === 'linked') return `已把 ${name} 关联到你原有的账号`
  return `已通过 ${name} 登录`
}

/** 去掉第三方登录回跳参数。保留其它 query。 */
export function stripOAuthParams(search: string): string {
  const raw = search.startsWith('?') ? search.slice(1) : search
  const params = new URLSearchParams(raw)
  OAUTH_PARAMS.forEach((key) => params.delete(key))
  const next = params.toString()
  return next ? `?${next}` : ''
}

/** 从 location.search 解析动作。可带或不带前导 ?。 */
export function parseDeepLink(search: string): DeepLinkAction {
  const raw = search.startsWith('?') ? search.slice(1) : search
  const params = new URLSearchParams(raw)
  if (!params.has('reset')) return { kind: 'none' }
  const value = (params.get('reset') || '').trim().toLowerCase()
  // 兼容 reset=1，以及 true 与空值（?reset）。
  if (value === '' || value === '1' || value === 'true' || value === 'yes') {
    return { kind: 'forgot_password' }
  }
  return { kind: 'none' }
}

/** 去掉已消费的深链参数。保留其它 query。 */
export function stripDeepLinkParams(search: string): string {
  const raw = search.startsWith('?') ? search.slice(1) : search
  const params = new URLSearchParams(raw)
  params.delete('reset')
  const next = params.toString()
  return next ? `?${next}` : ''
}

/** 应用深链副作用。返回是否打开找回密码。history 替换由调用方执行。 */
export function applyDeepLinkSearch(search: string): {
  action: DeepLinkAction
  nextSearch: string
  openForgot: boolean
} {
  const action = parseDeepLink(search)
  const openForgot = action.kind === 'forgot_password'
  const nextSearch = openForgot ? stripDeepLinkParams(search) : search.startsWith('?') ? search : search ? `?${search}` : ''
  return { action, nextSearch, openForgot }
}
